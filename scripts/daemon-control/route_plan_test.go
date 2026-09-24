// SPDX-License-Identifier: MIT

package daemoncontrol

import (
	"fmt"
	"strings"
	"testing"
)

func TestRoutePlanStartIsValidatedBeforeBackendStart(t *testing.T) {
	backend := &fakeBackend{}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	config := "private_key=synthetic\nallowed_ip=192.0.2.0/24\nendpoint=192.0.2.10:51820"
	valid := `{"routes":[{"destination":"192.0.2.0/24","owner":"tunnel"},{"destination":"192.0.2.10/32","owner":"physicalEndpoint"}]}`
	response := exchange(t, server, 501, requestFrame(t, fmt.Sprintf(`{"version":1,"operation":"start","profile_id":"%s","config":%q,"route_plan":%s}`, profileID, config, valid)))
	if !response.OK {
		t.Fatalf("valid plan: %#v", response)
	}
	if starts, _ := backend.counts(); starts != 1 {
		t.Fatalf("starts=%d", starts)
	}
	if response := server.apply(request{Operation: "stop", ProfileID: profileID}); !response.OK {
		t.Fatalf("stop: %#v", response)
	}

	invalid := `{"routes":[{"destination":"0.0.0.0/0","owner":"tunnel"}]}`
	response = exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"start","profile_id":"`+profileID+`","config":"`+syntheticConfig+`","route_plan":`+invalid+`}`))
	if response.OK || response.Error != "invalid_request" {
		t.Fatalf("invalid plan: %#v", response)
	}
	if starts, _ := backend.counts(); starts != 1 {
		t.Fatalf("invalid plan reached backend: starts=%d", starts)
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

func TestRoutePlanMustMatchUAPIBeforeBackendStart(t *testing.T) {
	const secret = "synthetic-private-key"
	const config = "private_key=" + secret + "\npublic_key=peer-one\nallowed_ip=10.25.0.0/24\nendpoint=192.0.2.10:51820\npublic_key=peer-two\nallowed_ip=10.1.1.2/32\nendpoint=198.51.100.20:51820\n"
	const validPlan = `{"routes":[{"destination":"10.25.0.0/24","owner":"tunnel"},{"destination":"10.1.1.2/32","owner":"tunnel"},{"destination":"192.0.2.10/32","owner":"physicalEndpoint"},{"destination":"198.51.100.20/32","owner":"physicalEndpoint"}]}`
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
	if !response.OK {
		t.Fatalf("valid multi-peer plan: %#v", response)
	}
	if starts, _ := backend.counts(); starts != 1 {
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
		if starts, _ := backend.counts(); starts != 1 {
			t.Fatalf("invalid plan %d reached backend: starts=%d", index, starts)
		}
	}
}
