// SPDX-License-Identifier: MIT

//go:build darwin

package daemoncontrol

import (
	"encoding/json"
	"net/netip"
	"reflect"
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
	var plan routePlan
	if err := json.Unmarshal([]byte(fullPlanJSON(t, []string{"192.0.2.0/31"})), &plan); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.StartWithRoutePlan("", plan); err == nil || !strings.Contains(err.Error(), "full route runtime unavailable") {
		t.Fatalf("full route ran without explicit flag: %v", err)
	}
}

func TestFullRouteCapabilityNeedsBothRootFlags(t *testing.T) {
	for _, test := range []struct {
		routes, full bool
		want         []string
	}{
		{false, false, nil},
		{true, false, nil},
		{false, true, nil},
		{true, true, []string{"ipv4-full-route"}},
	} {
		backend := &tunnelBackend{allowRoutePlanRuntime: test.routes, allowFullRouteRuntime: test.full}
		if got := backend.capabilities(); !reflect.DeepEqual(got, test.want) {
			t.Fatalf("route=%t full=%t capabilities=%#v", test.routes, test.full, got)
		}
	}
}

func TestTunnelBackendDefaultsToNoSyntheticRoutes(t *testing.T) {
	backend := &tunnelBackend{}
	if backend.syntheticIPv4Routes || backend.syntheticEndpointRoutes || backend.syntheticFallbackRoute {
		t.Fatal("normal daemon backend enabled synthetic route binding")
	}
}

func TestPlannedPeerEndpointResetRetainsOnlyPublicPeerAndLiteralEndpoint(t *testing.T) {
	key := strings.Repeat("a", 64)
	config := "private_key=" + strings.Repeat("b", 64) + "\npublic_key=" + key + "\nendpoint=203.0.113.10:51820"
	publicKey, endpoint, err := plannedPeerEndpointReset(config, netip.MustParseAddr("203.0.113.10"))
	if err != nil || publicKey != key || endpoint != "203.0.113.10:51820" {
		t.Fatalf("reset fields=%q %q err=%v", publicKey, endpoint, err)
	}
	if _, _, err := plannedPeerEndpointReset(config, netip.MustParseAddr("203.0.113.11")); err == nil {
		t.Fatal("accepted an endpoint that differs from the route plan")
	}
}

func TestEndpointRefreshRemainsPendingUntilUAPISucceeds(t *testing.T) {
	process := &tunnelProcess{endpointRefreshPending: true}
	if !process.needsEndpointRefresh(false) {
		t.Fatal("failed endpoint refresh was discarded")
	}
	process.endpointRefreshPending = false
	if process.needsEndpointRefresh(false) {
		t.Fatal("successful endpoint refresh remained pending")
	}
	if !process.needsEndpointRefresh(true) {
		t.Fatal("changed endpoint did not request refresh")
	}
}
