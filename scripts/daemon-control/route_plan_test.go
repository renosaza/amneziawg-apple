// SPDX-License-Identifier: MIT

package daemoncontrol

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
)

type partialRoutePlanBackend struct{ fakeBackend }

func (backend *partialRoutePlanBackend) StartWithRoutePlan(string, routePlan) (Session, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.starts++
	return Session{value: backend.starts}, errors.New("synthetic partial start")
}

type plannedFakeBackend struct{ fakeBackend }

func (backend *plannedFakeBackend) StartWithRoutePlan(string, routePlan) (Session, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.starts++
	return Session{value: backend.starts}, nil
}

func TestRoutePlanStartIsValidatedBeforeBackendStart(t *testing.T) {
	backend := &fakeBackend{}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	config := "private_key=synthetic\npublic_key=peer\nallowed_ip=192.0.2.0/24\nendpoint=192.0.2.10:51820"
	valid := `{"local_address":"192.0.2.2/32","routes":[{"destination":"192.0.2.0/24","owner":"tunnel"},{"destination":"192.0.2.10/32","owner":"physicalEndpoint"}]}`
	response := exchange(t, server, 501, requestFrame(t, fmt.Sprintf(`{"version":1,"operation":"start","profile_id":"%s","config":%q,"route_plan":%s}`, profileID, config, valid)))
	if response.OK || response.Error != "start_failed" {
		t.Fatalf("unsupported backend accepted plan: %#v", response)
	}
	if starts, _ := backend.counts(); starts != 0 {
		t.Fatalf("starts=%d", starts)
	}

	invalid := `{"routes":[{"destination":"0.0.0.0/0","owner":"tunnel"}]}`
	response = exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"start","profile_id":"`+profileID+`","config":"`+syntheticConfig+`","route_plan":`+invalid+`}`))
	if response.OK || response.Error != "invalid_request" {
		t.Fatalf("invalid plan: %#v", response)
	}
	if starts, _ := backend.counts(); starts != 0 {
		t.Fatalf("invalid plan reached backend: starts=%d", starts)
	}
}

func TestRoutePlanPartialStartRetainsReservation(t *testing.T) {
	backend := &partialRoutePlanBackend{}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	const config = "private_key=synthetic\npublic_key=peer\nallowed_ip=192.0.2.0/24\nendpoint=192.0.2.10:51820"
	const plan = `{"local_address":"192.0.2.2/32","routes":[{"destination":"192.0.2.0/24","owner":"tunnel"},{"destination":"192.0.2.10/32","owner":"physicalEndpoint"}]}`
	response := exchange(t, server, 501, requestFrame(t, fmt.Sprintf(
		`{"version":1,"operation":"start","profile_id":"%s","config":%q,"route_plan":%s}`,
		profileID, config, plan,
	)))
	if response.Error != "start_failed" {
		t.Fatalf("partial start: %#v", response)
	}
	response = exchange(t, server, 501, requestFrame(t, fmt.Sprintf(
		`{"version":1,"operation":"start","profile_id":"%s","config":%q,"route_plan":%s}`,
		profileIDTwo, config, plan,
	)))
	if response.Error != "planned_local_address_conflict" {
		t.Fatalf("second planned start: %#v", response)
	}
	if starts, _ := backend.counts(); starts != 1 {
		t.Fatalf("starts=%d", starts)
	}
	if response := server.apply(request{Operation: "stop", ProfileID: profileID}); !response.OK {
		t.Fatalf("cleanup partial start: %#v", response)
	}
}

func TestRoutePlanReservationsAllowDistinctSplits(t *testing.T) {
	backend := &plannedFakeBackend{}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	profiles := []struct {
		id, config, plan string
	}{
		{profileID, "private_key=synthetic\npublic_key=peer-one\nallowed_ip=10.25.0.0/24\nendpoint=192.0.2.10:51820", `{"local_address":"10.25.0.2/32","routes":[{"destination":"10.25.0.0/24","owner":"tunnel"},{"destination":"192.0.2.10/32","owner":"physicalEndpoint"}]}`},
		{profileIDTwo, "private_key=synthetic\npublic_key=peer-two\nallowed_ip=10.1.1.2/32\nendpoint=198.51.100.10:51820", `{"local_address":"10.1.1.1/32","routes":[{"destination":"10.1.1.2/32","owner":"tunnel"},{"destination":"198.51.100.10/32","owner":"physicalEndpoint"}]}`},
	}
	for _, profile := range profiles {
		response := exchange(t, server, 501, requestFrame(t, fmt.Sprintf(
			`{"version":1,"operation":"start","profile_id":"%s","config":%q,"route_plan":%s}`,
			profile.id, profile.config, profile.plan,
		)))
		if !response.OK {
			t.Fatalf("start %s: %#v", profile.id, response)
		}
	}
	if starts, _ := backend.counts(); starts != len(profiles) || len(server.planned) != len(profiles) {
		t.Fatalf("starts=%d planned=%d", starts, len(server.planned))
	}
	if stopped := server.apply(request{Operation: "stop", ProfileID: profileID}); !stopped.OK || len(server.planned) != 1 {
		t.Fatalf("stop first profile: %#v planned=%d", stopped, len(server.planned))
	}
	if status := server.apply(request{Operation: "status", ProfileID: profileIDTwo}); !status.OK {
		t.Fatalf("remaining profile changed: %#v", status)
	}
}

func TestRoutePlanReservationsRejectConflictsAndLegacyMixing(t *testing.T) {
	backend := &plannedFakeBackend{}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	const config = "private_key=synthetic\npublic_key=peer\nallowed_ip=10.25.0.0/24\nendpoint=192.0.2.10:51820"
	const plan = `{"local_address":"10.25.0.2/32","routes":[{"destination":"10.25.0.0/24","owner":"tunnel"},{"destination":"192.0.2.10/32","owner":"physicalEndpoint"}]}`
	start := func(id, configuration, routePlan string) response {
		return exchange(t, server, 501, requestFrame(t, fmt.Sprintf(`{"version":1,"operation":"start","profile_id":"%s","config":%q,"route_plan":%s}`, id, configuration, routePlan)))
	}
	if response := start(profileID, config, plan); !response.OK {
		t.Fatalf("first plan: %#v", response)
	}
	for name, candidate := range map[string]string{
		"overlap":  `{"local_address":"10.1.1.1/32","routes":[{"destination":"10.25.0.0/25","owner":"tunnel"},{"destination":"198.51.100.10/32","owner":"physicalEndpoint"}]}`,
		"endpoint": `{"local_address":"10.1.1.1/32","routes":[{"destination":"10.1.1.2/32","owner":"tunnel"},{"destination":"192.0.2.10/32","owner":"physicalEndpoint"}]}`,
	} {
		configuration := "private_key=synthetic\npublic_key=peer\nallowed_ip=10.25.0.0/25\nendpoint=198.51.100.10:51820"
		if name == "endpoint" {
			configuration = "private_key=synthetic\npublic_key=peer\nallowed_ip=10.1.1.2/32\nendpoint=192.0.2.10:51820"
		}
		response := start(profileIDTwo, configuration, candidate)
		want := "planned_route_conflict"
		if name == "endpoint" {
			want = "planned_endpoint_conflict"
		}
		if response.Error != want {
			t.Fatalf("%s conflict: %#v", name, response)
		}
	}
	if response := exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"start","profile_id":"`+profileIDTwo+`","config":"private_key=synthetic"}`)); response.Error != "planned_session_active" {
		t.Fatalf("legacy session mixed with planned: %#v", response)
	}
	if stopped := server.apply(request{Operation: "stop", ProfileID: profileID}); !stopped.OK {
		t.Fatalf("stop planned profile: %#v", stopped)
	}
	if response := exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"start","profile_id":"`+profileID+`","config":"private_key=synthetic"}`)); !response.OK {
		t.Fatalf("legacy session: %#v", response)
	}
	if response := start(profileIDTwo, config, plan); response.Error != "unplanned_session_active" {
		t.Fatalf("planned session mixed with legacy: %#v", response)
	}
}

func TestRoutePlanFailedStopRetainsReservation(t *testing.T) {
	backend := &plannedFakeBackend{fakeBackend: fakeBackend{stopFailures: 1}}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	const config = "private_key=synthetic\npublic_key=peer\nallowed_ip=10.25.0.0/24\nendpoint=192.0.2.10:51820"
	const plan = `{"local_address":"10.25.0.2/32","routes":[{"destination":"10.25.0.0/24","owner":"tunnel"},{"destination":"192.0.2.10/32","owner":"physicalEndpoint"}]}`
	start := func(id string) response {
		return exchange(t, server, 501, requestFrame(t, fmt.Sprintf(`{"version":1,"operation":"start","profile_id":"%s","config":%q,"route_plan":%s}`, id, config, plan)))
	}
	if response := start(profileID); !response.OK {
		t.Fatalf("start: %#v", response)
	}
	if response := server.apply(request{Operation: "stop", ProfileID: profileID}); response.Error != "stop_failed" {
		t.Fatalf("failed stop: %#v", response)
	}
	if response := start(profileIDTwo); response.Error != "planned_local_address_conflict" {
		t.Fatalf("failed stop released plan: %#v", response)
	}
	if response := server.apply(request{Operation: "stop", ProfileID: profileID}); !response.OK {
		t.Fatalf("retry stop: %#v", response)
	}
	if response := start(profileIDTwo); !response.OK {
		t.Fatalf("released plan after confirmed stop: %#v", response)
	}
}

func TestRoutePlanBoundaryRejectsUnsupportedInput(t *testing.T) {
	plans := []string{
		`null`,
		`{}`,
		`{"routes":null}`,
		`{"routes":[],"uplink":"en0"}`,
		`{"routes":[{"destination":"0.0.0.0/1","owner":"tunnel"}]}`,
		`{"routes":[{"destination":"2001:db8::/64","owner":"tunnel"}]}`,
		`{"routes":[{"destination":"192.0.2.1/24","owner":"tunnel"}]}`,
		`{"routes":[{"destination":"192.0.2.0/24","owner":"unknown"}]}`,
		`{"routes":[{"destination":"192.0.2.0/24","owner":"physicalEndpoint"}]}`,
		`{"routes":[{"destination":"192.0.2.0/24","owner":"tunnel"},{"destination":"192.0.2.0/24","owner":"physicalEndpoint"}]}`,
		`{"routes":[{"destination":"192.0.2.0/24","owner":"tunnel","gateway":"en0"}]}`,
	}
	routes := make([]string, maxRoutePlanRoutes+1)
	for index := range routes {
		routes[index] = fmt.Sprintf(`{"destination":"192.0.2.%d/32","owner":"tunnel"}`, index)
	}
	plans = append(plans, `{"routes":[`+strings.Join(routes, ",")+`]}`)
	for _, plan := range plans {
		if _, err := decodeRequest([]byte(`{"version":1,"operation":"start","profile_id":"` + profileID + `","config":"` + syntheticConfig + `","route_plan":` + plan + `}`)); err == nil {
			t.Fatalf("accepted invalid route plan %s", plan)
		}
	}
}

func TestRoutePlanIsStartOnly(t *testing.T) {
	if _, err := decodeRequest([]byte(`{"version":1,"operation":"status","profile_id":"` + profileID + `","route_plan":{"routes":[]}}`)); err == nil {
		t.Fatal("accepted route plan outside start")
	}
}

func TestRoutePlanRequiresCanonicalIPv4LocalAddress(t *testing.T) {
	if err := validRoutePlan([]byte(`{"local_address":"192.0.2.2/32","routes":[]}`)); err != nil {
		t.Fatalf("valid local address: %v", err)
	}
	for _, plan := range []string{
		`{"routes":[]}`,
		`{"local_address":"not-an-address","routes":[]}`,
		`{"local_address":"192.0.2.2/24","routes":[]}`,
		`{"local_address":"192.0.2.2/32,192.0.2.3/32","routes":[]}`,
		`{"local_address":"2001:db8::2/128","routes":[]}`,
		`{"local_address":"0.0.0.0/32","routes":[]}`,
		`{"local_address":"0.1.2.3/32","routes":[]}`,
		`{"local_address":"240.0.0.1/32","routes":[]}`,
		`{"local_address":"127.0.0.1/32","routes":[]}`,
		`{"local_address":"169.254.1.1/32","routes":[]}`,
		`{"local_address":"224.0.0.1/32","routes":[]}`,
		`{"local_address":"255.255.255.255/32","routes":[]}`,
	} {
		if err := validRoutePlan([]byte(plan)); err == nil {
			t.Fatalf("accepted invalid local address: %s", plan)
		}
	}
}

func TestRoutePlanRejectsUnusablePhysicalEndpoint(t *testing.T) {
	for _, address := range []string{"0.0.0.1", "240.0.0.1"} {
		plan := `{"local_address":"192.0.2.2/32","routes":[{"destination":"192.0.2.0/24","owner":"tunnel"},{"destination":"` + address + `/32","owner":"physicalEndpoint"}]}`
		if err := validRoutePlan([]byte(plan)); err == nil {
			t.Fatalf("accepted %s", address)
		}
	}
	for _, address := range []string{"10.0.0.1", "192.0.2.1"} {
		if !usableIPv4Address(netip.MustParseAddr(address)) {
			t.Fatalf("rejected %s", address)
		}
	}
}

func TestRoutePlanRequiresOneExactUAPIPeerBeforeBackendStart(t *testing.T) {
	const secret = "synthetic-private-key"
	const plan = `{"local_address":"10.25.0.2/32","routes":[{"destination":"10.25.0.0/24","owner":"tunnel"},{"destination":"192.0.2.10/32","owner":"physicalEndpoint"}]}`
	const validConfig = "private_key=" + secret + "\npublic_key=peer-one\nallowed_ip=10.25.0.0/24\nendpoint=192.0.2.10:51820\n"

	backend := &fakeBackend{}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	response := exchange(t, server, 501, requestFrame(t, fmt.Sprintf(`{"version":1,"operation":"start","profile_id":"%s","config":%q,"route_plan":%s}`, profileID, validConfig, plan)))
	if response.OK || response.Error != "start_failed" {
		t.Fatalf("unsupported backend accepted valid plan: %#v", response)
	}
	if starts, _ := backend.counts(); starts != 0 {
		t.Fatalf("starts=%d", starts)
	}

	for index, invalid := range []string{
		strings.Replace(validConfig, "allowed_ip=10.25.0.0/24", "allowed_ip=10.25.0.0/24\nallowed_ip=10.1.1.2/32", 1),
		strings.Replace(validConfig, "public_key=peer-one", "public_key=peer-one\npublic_key=peer-two", 1),
		strings.Replace(validConfig, "allowed_ip=10.25.0.0/24", "allowed_ip=10.1.1.2/32", 1),
		strings.Replace(validConfig, "endpoint=192.0.2.10:51820", "endpoint=192.0.2.11:51820", 1),
	} {
		id := fmt.Sprintf("aaaaaaaa-2222-4333-8444-%012x", index+1)
		response := exchange(t, server, 501, requestFrame(t, fmt.Sprintf(
			`{"version":1,"operation":"start","profile_id":"%s","config":%q,"route_plan":%s}`,
			id, invalid, plan,
		)))
		if response.OK || response.Error != "invalid_request" {
			t.Fatalf("invalid plan %d: %#v", index, response)
		}
		if strings.Contains(fmt.Sprintf("%#v", response), secret) {
			t.Fatalf("invalid plan %d leaked config", index)
		}
		if starts, _ := backend.counts(); starts != 0 {
			t.Fatalf("invalid plan %d reached backend: starts=%d", index, starts)
		}
	}
}
