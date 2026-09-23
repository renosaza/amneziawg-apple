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
	valid := `{"routes":[{"destination":"192.0.2.0/24","owner":"tunnel"},{"destination":"192.0.2.10/32","owner":"physicalEndpoint"}]}`
	response := exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"start","profile_id":"`+profileID+`","config":"`+syntheticConfig+`","route_plan":`+valid+`}`))
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
