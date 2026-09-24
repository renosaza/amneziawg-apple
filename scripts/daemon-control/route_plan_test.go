// SPDX-License-Identifier: MIT

package daemoncontrol

import (
	"encoding/json"
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

type recordedRoutePlanBackend struct {
	plannedFakeBackend
	plan routePlan
}

func (backend *recordedRoutePlanBackend) StartWithRoutePlan(config string, plan routePlan) (Session, error) {
	backend.plan = plan
	return backend.plannedFakeBackend.StartWithRoutePlan(config, plan)
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
		{profileIDThree, "private_key=synthetic\npublic_key=peer-three\nallowed_ip=10.2.2.2/32\nendpoint=203.0.113.10:51820", `{"local_address":"10.2.2.1/32","routes":[{"destination":"10.2.2.2/32","owner":"tunnel"},{"destination":"203.0.113.10/32","owner":"physicalEndpoint"}]}`},
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
	if stopped := server.apply(request{Operation: "stop", ProfileID: profileID}); !stopped.OK || len(server.planned) != len(profiles)-1 {
		t.Fatalf("stop first profile: %#v planned=%d", stopped, len(server.planned))
	}
	for _, id := range []string{profileIDTwo, profileIDThree} {
		if status := server.apply(request{Operation: "status", ProfileID: id}); !status.OK {
			t.Fatalf("remaining profile %s changed: %#v", id, status)
		}
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

func TestRoutePlanRejectsTwoOwnersForDestination(t *testing.T) {
	plan := []byte(`{"local_address":"10.25.0.2/32","routes":[{"destination":"192.0.2.10/32","owner":"tunnel"},{"destination":"192.0.2.10/32","owner":"physicalEndpoint"}]}`)
	if err := validRoutePlan(plan); err == nil {
		t.Fatal("accepted two owners for one destination")
	}
}

func TestFullRoutePlanReachesOnlyRoutePlanBackend(t *testing.T) {
	backend := &recordedRoutePlanBackend{}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	plan := fullPlanJSON(t, []string{"192.0.2.0/31"})
	config := "private_key=synthetic\npublic_key=peer\nallowed_ip=0.0.0.0/0\nendpoint=192.0.2.10:51820"
	response := exchange(t, server, 501, requestFrame(t, fmt.Sprintf(
		`{"version":1,"operation":"start","profile_id":"%s","config":%q,"route_plan":%s}`,
		profileID, config, plan,
	)))
	if !response.OK {
		t.Fatalf("full plan: %#v", response)
	}
	if starts, _ := backend.counts(); starts != 1 || !backend.plan.isDefaultTunnel() {
		t.Fatalf("full plan did not reach route backend: starts=%d plan=%#v", starts, backend.plan)
	}
}

func TestFullRoutePlanRequiresCanonicalComplement(t *testing.T) {
	config := "private_key=synthetic\npublic_key=peer\nallowed_ip=0.0.0.0/0\nendpoint=192.0.2.10:51820"
	valid := fullPlanJSON(t, []string{"192.0.2.0/31"})
	if err := validRoutePlanMatchesConfig([]byte(valid), config); err != nil {
		t.Fatalf("valid full plan: %v", err)
	}
	var plan routePlan
	if err := json.Unmarshal([]byte(valid), &plan); err != nil {
		t.Fatal(err)
	}
	var routes []routePlanRoute
	if err := json.Unmarshal(plan.Routes, &routes); err != nil {
		t.Fatal(err)
	}
	for index := range routes {
		if routes[index].Owner == "tunnel" {
			routes[index].Destination = "0.0.0.0/2"
			break
		}
	}
	plan.Routes, _ = json.Marshal(routes)
	broken, _ := json.Marshal(plan)
	if err := validRoutePlanMatchesConfig(broken, config); err == nil {
		t.Fatal("accepted a full plan with a non-complement tunnel route")
	}
}

func TestFullRouteRejectsUnexcludedActivePeerEndpointInBothOrders(t *testing.T) {
	fullConfig := "private_key=synthetic\npublic_key=sticky\nallowed_ip=0.0.0.0/0\nendpoint=192.0.2.10:51820"
	splitConfig := "private_key=synthetic\npublic_key=corporate\nallowed_ip=10.25.0.0/24\nendpoint=198.51.100.10:51820"
	splitPlan := `{"local_address":"10.25.0.2/32","routes":[{"destination":"10.25.0.0/24","owner":"tunnel"},{"destination":"198.51.100.10/32","owner":"physicalEndpoint"}]}`
	start := func(server *Server, id, config, plan string) response {
		return exchange(t, server, 501, requestFrame(t, fmt.Sprintf(`{"version":1,"operation":"start","profile_id":"%s","config":%q,"route_plan":%s}`, id, config, plan)))
	}
	for _, firstFull := range []bool{true, false} {
		server, err := NewServerWithBackend(501, &plannedFakeBackend{})
		if err != nil {
			t.Fatal(err)
		}
		fullPlan := strings.Replace(fullPlanJSON(t, []string{"192.168.31.0/24"}), "10.25.0.2/32", "10.100.0.2/32", 1)
		if firstFull {
			if response := start(server, profileID, fullConfig, fullPlan); !response.OK {
				t.Fatalf("full first: %#v", response)
			}
			if response := start(server, profileIDTwo, splitConfig, splitPlan); response.Error != "planned_endpoint_conflict" {
				t.Fatalf("split after unexcluded full: %#v", response)
			}
		} else {
			if response := start(server, profileIDTwo, splitConfig, splitPlan); !response.OK {
				t.Fatalf("split first: %#v", response)
			}
			if response := start(server, profileID, fullConfig, fullPlan); response.Error != "planned_endpoint_conflict" {
				t.Fatalf("full after unexcluded split: %#v", response)
			}
		}
	}

	server, err := NewServerWithBackend(501, &plannedFakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	fullPlan := strings.Replace(fullPlanJSON(t, []string{"192.168.31.0/24", "10.25.0.0/24", "198.51.100.10/32"}), "10.25.0.2/32", "10.100.0.2/32", 1)
	if response := start(server, profileID, fullConfig, fullPlan); !response.OK {
		t.Fatalf("full with endpoint exclusion: %#v", response)
	}
	if response := start(server, profileIDTwo, splitConfig, splitPlan); !response.OK {
		t.Fatalf("split after endpoint exclusion: %#v", response)
	}
}

func TestIPv4ComplementPreservesExclusions(t *testing.T) {
	excluded := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/31"), netip.MustParsePrefix("198.51.100.10/32")}
	routes := ipv4TunnelComplement(excluded)
	if len(routes) == 0 || len(routes) > maxRoutePlanRoutes {
		t.Fatalf("complement route count=%d", len(routes))
	}
	for _, route := range routes {
		for _, exclusion := range excluded {
			if route.Overlaps(exclusion) {
				t.Fatalf("%s overlaps %s", route, exclusion)
			}
		}
	}
	for _, reserved := range []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("224.0.0.0/3"),
	} {
		if containsIPv4Prefix(routes, reserved.Addr()) {
			t.Fatalf("tunnel complement captured reserved %s", reserved)
		}
	}
	for _, address := range []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("203.0.113.1")} {
		if !containsIPv4Prefix(routes, address) {
			t.Fatalf("complement lost %s", address)
		}
	}
}

func TestFullRouteComplementFitsProtocolCap(t *testing.T) {
	excluded := []netip.Prefix{
		netip.MustParsePrefix("10.25.0.0/24"),
		netip.MustParsePrefix("10.1.1.2/32"),
		netip.MustParsePrefix("192.168.31.0/24"),
		netip.MustParsePrefix("198.51.100.18/32"),
		netip.MustParsePrefix("203.0.113.103/32"),
	}
	if count := len(ipv4TunnelComplement(excluded)); count > maxRoutePlanRoutes {
		t.Fatalf("five IPv4 exclusions need %d routes; cap=%d", count, maxRoutePlanRoutes)
	}
}

func containsIPv4Prefix(routes []netip.Prefix, address netip.Addr) bool {
	for _, route := range routes {
		if route.Contains(address) {
			return true
		}
	}
	return false
}

func fullPlanJSON(t *testing.T, excluded []string) string {
	t.Helper()
	prefixes := make([]netip.Prefix, 0, len(excluded))
	for _, value := range excluded {
		prefixes = append(prefixes, netip.MustParsePrefix(value))
	}
	routes := make([]routePlanRoute, 0, len(prefixes)+len(ipv4TunnelComplement(prefixes))+1)
	for _, prefix := range ipv4TunnelComplement(prefixes) {
		routes = append(routes, routePlanRoute{Destination: prefix.String(), Owner: "tunnel"})
	}
	for _, prefix := range prefixes {
		routes = append(routes, routePlanRoute{Destination: prefix.String(), Owner: "excluded"})
	}
	routes = append(routes, routePlanRoute{Destination: "192.0.2.10/32", Owner: "physicalEndpoint"})
	plan, err := json.Marshal(routePlan{LocalAddress: "10.25.0.2/32", Routes: mustJSON(t, routes)})
	if err != nil {
		t.Fatal(err)
	}
	return string(plan)
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestLegacyDefaultAllowedIPDoesNotReachBackend(t *testing.T) {
	backend := &fakeBackend{}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	for index, allowedIP := range []string{"0.0.0.0/0", "::/0"} {
		response := exchange(t, server, 501, requestFrame(t, fmt.Sprintf(
			`{"version":1,"operation":"start","profile_id":"aaaaaaaa-2222-4333-8444-%012x","config":%q}`,
			index+1, "private_key=synthetic\npublic_key=peer\nallowed_ip="+allowedIP,
		)))
		if response.OK || response.Error != "invalid_request" {
			t.Fatalf("%s: %#v", allowedIP, response)
		}
	}
	if starts, _ := backend.counts(); starts != 0 {
		t.Fatalf("legacy default route reached backend: starts=%d", starts)
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
