// SPDX-License-Identifier: MIT

package daemoncontrol

import (
	"errors"
	"fmt"
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

func TestRoutePlanStartIsValidatedBeforeBackendStart(t *testing.T) {
	backend := &fakeBackend{}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	config := "private_key=synthetic\nallowed_ip=192.0.2.0/24\nendpoint=192.0.2.10:51820"
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

func TestRoutePlanPartialStartRetainsSingletonReservation(t *testing.T) {
	backend := &partialRoutePlanBackend{}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	const config = "private_key=synthetic\nallowed_ip=192.0.2.0/24\nendpoint=192.0.2.10:51820"
	const plan = `{"local_address":"192.0.2.2/32","routes":[{"destination":"192.0.2.0/24","owner":"tunnel"},{"destination":"192.0.2.10/32","owner":"physicalEndpoint"}]}`
	for _, id := range []string{profileID, profileIDTwo} {
		response := exchange(t, server, 501, requestFrame(t, fmt.Sprintf(
			`{"version":1,"operation":"start","profile_id":"%s","config":%q,"route_plan":%s}`,
			id, config, plan,
		)))
		if id == profileID && response.Error != "start_failed" {
			t.Fatalf("partial start: %#v", response)
		}
		if id == profileIDTwo && response.Error != "planned_session_active" {
			t.Fatalf("second planned start: %#v", response)
		}
	}
	if starts, _ := backend.counts(); starts != 1 {
		t.Fatalf("starts=%d", starts)
	}
	if response := server.apply(request{Operation: "stop", ProfileID: profileID}); !response.OK {
		t.Fatalf("cleanup partial start: %#v", response)
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

func TestRoutePlanMustMatchUAPIBeforeBackendStart(t *testing.T) {
	const secret = "synthetic-private-key"
	const config = "private_key=" + secret + "\npublic_key=peer-one\nallowed_ip=10.25.0.0/24\nendpoint=192.0.2.10:51820\npublic_key=peer-two\nallowed_ip=10.1.1.2/32\nendpoint=198.51.100.20:51820\n"
	const validPlan = `{"local_address":"10.25.0.2/32","routes":[{"destination":"10.25.0.0/24","owner":"tunnel"},{"destination":"10.1.1.2/32","owner":"tunnel"},{"destination":"192.0.2.10/32","owner":"physicalEndpoint"},{"destination":"198.51.100.20/32","owner":"physicalEndpoint"}]}`
	const tunnelOnlyPlan = `{"routes":[{"destination":"10.25.0.0/24","owner":"tunnel"}]}`
	const hostnameConfig = "private_key=" + secret + "\nallowed_ip=10.25.0.0/24\nendpoint=vpn.example.test:51820\n"
	const endpointWithoutRouteConfig = "private_key=" + secret + "\nallowed_ip=10.25.0.0/24\nendpoint=192.0.2.10:51820\n"

	backend := &fakeBackend{}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	response := exchange(t, server, 501, requestFrame(t, fmt.Sprintf(
		`{"version":1,"operation":"start","profile_id":"%s","config":%q,"route_plan":%s}`,
		profileID, config, validPlan,
	)))
	if response.OK || response.Error != "start_failed" {
		t.Fatalf("unsupported backend accepted multi-peer plan: %#v", response)
	}
	if starts, _ := backend.counts(); starts != 0 {
		t.Fatalf("starts=%d", starts)
	}

	for index, invalid := range []struct {
		config string
		plan   string
	}{
		{config, strings.Replace(validPlan, "10.25.0.0/24", "10.26.0.0/24", 1)},
		{config, strings.Replace(validPlan, "198.51.100.20/32", "198.51.100.21/32", 1)},
		{hostnameConfig, tunnelOnlyPlan},
		{endpointWithoutRouteConfig, tunnelOnlyPlan},
	} {
		id := fmt.Sprintf("aaaaaaaa-2222-4333-8444-%012x", index+1)
		response := exchange(t, server, 501, requestFrame(t, fmt.Sprintf(
			`{"version":1,"operation":"start","profile_id":"%s","config":%q,"route_plan":%s}`,
			id, invalid.config, invalid.plan,
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
