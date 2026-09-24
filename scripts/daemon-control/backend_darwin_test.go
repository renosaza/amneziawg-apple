// SPDX-License-Identifier: MIT

//go:build darwin

package daemoncontrol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManualRoutePlanAcceptsRouteOrder(t *testing.T) {
	plan := routePlan{LocalAddress: "192.0.2.2/32", Routes: json.RawMessage(`[{"destination":"198.51.100.10/32","owner":"physicalEndpoint"},{"destination":"198.51.100.0/24","owner":"tunnel"}]`)}
	if !isManualRoutePlan(plan) {
		t.Fatal("rejected reversed fixed manual route plan")
	}
}

func TestFullRouteRuntimeIsDisabledWithoutSeparateFlag(t *testing.T) {
	backend := &tunnelBackend{allowRoutePlanRuntime: true}
	plan := routePlan{LocalAddress: "10.25.0.2/32", Routes: json.RawMessage(`[
        {"destination":"0.0.0.0/1","owner":"tunnel"},
        {"destination":"128.0.0.0/1","owner":"tunnel"},
        {"destination":"192.0.2.0/31","owner":"excluded"},
        {"destination":"192.0.2.10/32","owner":"physicalEndpoint"}
    ]`)}
	if _, err := backend.StartWithRoutePlan("", plan); err == nil || !strings.Contains(err.Error(), "full route runtime unavailable") {
		t.Fatalf("full route ran without explicit flag: %v", err)
	}
}

func TestTunnelBackendDefaultsToNoSyntheticRoutes(t *testing.T) {
	backend := &tunnelBackend{}
	if backend.syntheticIPv4Routes || backend.syntheticEndpointRoutes || backend.syntheticFallbackRoute {
		t.Fatal("normal daemon backend enabled synthetic route binding")
	}
}
