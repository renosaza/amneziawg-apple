// SPDX-License-Identifier: MIT

//go:build darwin

package daemoncontrol

import (
	"encoding/json"
	"testing"
)

func TestManualRoutePlanAcceptsRouteOrder(t *testing.T) {
	plan := routePlan{LocalAddress: "192.0.2.2/32", Routes: json.RawMessage(`[{"destination":"198.51.100.10/32","owner":"physicalEndpoint"},{"destination":"198.51.100.0/24","owner":"tunnel"}]`)}
	if !isManualRoutePlan(plan) {
		t.Fatal("rejected reversed fixed manual route plan")
	}
}

func TestTunnelBackendDefaultsToNoSyntheticRoutes(t *testing.T) {
	backend := &tunnelBackend{}
	if backend.syntheticIPv4Routes || backend.syntheticEndpointRoutes || backend.syntheticFallbackRoute {
		t.Fatal("normal daemon backend enabled synthetic route binding")
	}
}
