// SPDX-License-Identifier: MIT

//go:build manualruntime

package daemoncontrol

import (
	"encoding/json"
	"os"
	"testing"
)

func newTunnelBackendForManualRuntime(binary string) (Backend, error) {
	backend, err := newTunnelBackend(binary)
	if err != nil {
		return nil, err
	}
	configured := backend.(*tunnelBackend)
	configured.syntheticIPv4Routes = true
	configured.syntheticEndpointRoutes = true
	configured.syntheticFallbackRoute = true
	configured.allowRoutePlanRuntime = true
	return backend, nil
}

func TestManualRoutePlanStartAndCleanup(t *testing.T) {
	if os.Getenv("AMNEZIAWG_DAEMON_RUNTIME") != "1" {
		t.Skip("manual disposable-runner test")
	}
	backend, err := newTunnelBackendForManualRuntime(os.Getenv("AMNEZIAWG_DAEMON_BINARY"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("cleanup failed: %v", err)
		}
	})
	const config = "private_key=1111111111111111111111111111111111111111111111111111111111111111\npublic_key=2222222222222222222222222222222222222222222222222222222222222222\nallowed_ip=198.51.100.0/24\nendpoint=198.51.100.10:1"
	const plan = `{"local_address":"192.0.2.2/32","routes":[{"destination":"198.51.100.0/24","owner":"tunnel"},{"destination":"198.51.100.10/32","owner":"physicalEndpoint"}]}`
	frame, err := json.Marshal(request{Version: 1, Operation: "start", ProfileID: profileID, Config: config, RoutePlan: json.RawMessage(plan)})
	if err != nil {
		t.Fatal(err)
	}
	started := exchange(t, server, 501, requestFrame(t, string(frame)))
	if !started.OK {
		t.Fatalf("route-plan start stage=%s: %#v", backend.(*tunnelBackend).lastStartStage, started)
	}
	process := server.profiles[profileID].value.(*tunnelProcess)
	if process.route == nil || process.fallbackRoute == nil || process.precedenceEndpointRoute == nil {
		t.Fatal("route plan did not own synthetic resources")
	}
	if err := process.route.requireOwnedAddress(); err != nil {
		t.Fatal(err)
	}
	if err := process.fallbackRoute.verify(); err != nil {
		t.Fatal(err)
	}
	if err := process.precedenceEndpointRoute.verify(); err != nil {
		t.Fatal(err)
	}
	addressRoute, splitRoute, endpointRoute := process.route, process.fallbackRoute, process.precedenceEndpointRoute
	const configTwo = "private_key=3333333333333333333333333333333333333333333333333333333333333333\npublic_key=4444444444444444444444444444444444444444444444444444444444444444\nallowed_ip=203.0.113.0/24\nendpoint=203.0.113.10:1"
	const planTwo = `{"local_address":"192.0.2.3/32","routes":[{"destination":"203.0.113.0/24","owner":"tunnel"},{"destination":"203.0.113.10/32","owner":"physicalEndpoint"}]}`
	secondFrame, err := json.Marshal(request{Version: 1, Operation: "start", ProfileID: profileIDTwo, Config: configTwo, RoutePlan: json.RawMessage(planTwo)})
	if err != nil {
		t.Fatal(err)
	}
	secondStarted := exchange(t, server, 501, requestFrame(t, string(secondFrame)))
	if !secondStarted.OK {
		t.Fatalf("second route-plan start stage=%s: %#v", backend.(*tunnelBackend).lastStartStage, secondStarted)
	}
	second := server.profiles[profileIDTwo].value.(*tunnelProcess)
	if second.route == nil || second.fallbackRoute == nil || second.precedenceEndpointRoute == nil {
		t.Fatal("second route plan did not own synthetic resources")
	}
	if err := second.route.requireOwnedAddress(); err != nil {
		t.Fatal(err)
	}
	if err := second.fallbackRoute.verify(); err != nil {
		t.Fatal(err)
	}
	if err := second.precedenceEndpointRoute.verify(); err != nil {
		t.Fatal(err)
	}
	failedFrame, err := json.Marshal(request{Version: 1, Operation: "start", ProfileID: profileIDThree, Config: config, RoutePlan: json.RawMessage(plan)})
	if err != nil {
		t.Fatal(err)
	}
	failed := exchange(t, server, 501, requestFrame(t, string(failedFrame)))
	if failed.OK || failed.Error != "planned_local_address_conflict" {
		t.Fatalf("route-plan collision start: %#v", failed)
	}
	if _, found := server.profiles[profileIDThree]; found {
		t.Fatal("failed route-plan start retained a session")
	}
	if err := process.route.requireOwnedAddress(); err != nil {
		t.Fatal(err)
	}
	if err := process.fallbackRoute.verify(); err != nil {
		t.Fatal(err)
	}
	if err := process.precedenceEndpointRoute.verify(); err != nil {
		t.Fatal(err)
	}
	if stopped := server.apply(request{Operation: "stop", ProfileID: profileID}); !stopped.OK {
		t.Fatalf("route-plan stop: %#v", stopped)
	}
	if err := addressRoute.proveAbsentAfterTunnelExit(); err != nil {
		t.Fatal(err)
	}
	if err := splitRoute.proveAbsentAfterTunnelExit(); err != nil {
		t.Fatal(err)
	}
	if err := endpointRoute.proveAbsent(); err != nil {
		t.Fatal(err)
	}
	if status := server.apply(request{Operation: "status", ProfileID: profileIDTwo}); !status.OK {
		t.Fatalf("second route-plan session changed after first stop: %#v", status)
	}
	if err := second.route.requireOwnedAddress(); err != nil {
		t.Fatal(err)
	}
	if err := second.fallbackRoute.verify(); err != nil {
		t.Fatal(err)
	}
	if err := second.precedenceEndpointRoute.verify(); err != nil {
		t.Fatal(err)
	}
	if stopped := server.apply(request{Operation: "stop", ProfileID: profileIDTwo}); !stopped.OK {
		t.Fatalf("second route-plan stop: %#v", stopped)
	}
}

func TestManualSyntheticThreeTunnelLifecycle(t *testing.T) {
	if os.Getenv("AMNEZIAWG_DAEMON_RUNTIME") != "1" {
		t.Skip("manual disposable-runner test")
	}
	backend, err := newTunnelBackendForManualRuntime(os.Getenv("AMNEZIAWG_DAEMON_BINARY"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("cleanup failed: %v", err)
		}
	})
	profiles := []struct {
		id     string
		config string
	}{
		{profileID, "private_key=0101010101010101010101010101010101010101010101010101010101010101"},
		{profileIDTwo, "private_key=0202020202020202020202020202020202020202020202020202020202020202"},
		{profileIDThree, "private_key=0303030303030303030303030303030303030303030303030303030303030303"},
	}
	for _, profile := range profiles {
		if started := server.apply(request{Operation: "start", ProfileID: profile.id, Config: profile.config}); !started.OK {
			t.Fatalf("start %s failed: %#v", profile.id, started)
		}
	}
	for _, profile := range profiles {
		if status := server.apply(request{Operation: "status", ProfileID: profile.id}); !status.OK {
			t.Fatalf("status %s failed: %#v", profile.id, status)
		}
	}
	routeTargets := make(map[string]bool)
	endpointTargets := make(map[string]bool)
	for _, profile := range profiles {
		process, ok := server.profiles[profile.id].value.(*tunnelProcess)
		if !ok || process.route == nil || process.endpointRoute == nil {
			t.Fatalf("missing owned synthetic route for %s", profile.id)
		}
		if err := process.route.verify(); err != nil {
			t.Fatal(err)
		}
		if err := process.route.requireOwnedAddress(); err != nil {
			t.Fatal(err)
		}
		if err := process.endpointRoute.verify(); err != nil {
			t.Fatal(err)
		}
		if routeTargets[process.route.target.String()] {
			t.Fatal("synthetic routes share a target")
		}
		routeTargets[process.route.target.String()] = true
		if endpointTargets[process.endpointRoute.target.String()] {
			t.Fatal("synthetic endpoint routes share a target")
		}
		endpointTargets[process.endpointRoute.target.String()] = true
		if profile.id == profileID {
			if process.fallbackRoute == nil || process.precedenceEndpointRoute == nil {
				t.Fatal("missing fallback route owner")
			}
			if err := process.fallbackRoute.verify(); err != nil {
				t.Fatal(err)
			}
			if err := process.precedenceEndpointRoute.verify(); err != nil {
				t.Fatal(err)
			}
			if !process.fallbackRoute.prefix.Contains(process.precedenceEndpointRoute.target) {
				t.Fatal("physical endpoint does not shadow the synthetic fallback")
			}
		} else if profile.id == profileIDTwo {
			if process.fallbackRoute == nil || process.fallbackRoute.prefix != syntheticSplitPrefixes[1] || process.precedenceEndpointRoute != nil {
				t.Fatal("missing /32 synthetic split route owner")
			}
			if err := process.fallbackRoute.verify(); err != nil {
				t.Fatal(err)
			}
		} else if process.fallbackRoute != nil || process.precedenceEndpointRoute != nil {
			t.Fatal("unexpected synthetic split route owner")
		}
		t.Logf("owned synthetic route target=%s interface=%s index=%d endpoint=%s physical=%s", process.route.target, process.route.name, process.route.iface.Index, process.endpointRoute.target, process.endpointRoute.iface.Name)
	}
	middle := server.profiles[profileID].value.(*tunnelProcess)
	middleRoute := middle.route
	middleEndpointRoute := middle.endpointRoute
	middleFallbackRoute := middle.fallbackRoute
	middlePrecedenceEndpointRoute := middle.precedenceEndpointRoute
	if stopped := server.apply(request{Operation: "stop", ProfileID: profileID}); !stopped.OK {
		t.Fatalf("stop fallback-owning tunnel failed: %#v", stopped)
	}
	if err := middleRoute.proveAbsentAfterTunnelExit(); err != nil {
		t.Fatal(err)
	}
	if err := middleEndpointRoute.proveAbsent(); err != nil {
		t.Fatal(err)
	}
	if err := middleFallbackRoute.proveAbsentAfterTunnelExit(); err != nil {
		t.Fatal(err)
	}
	if err := middlePrecedenceEndpointRoute.proveAbsent(); err != nil {
		t.Fatal(err)
	}
	for _, profile := range []struct{ id string }{{profileIDTwo}, {profileIDThree}} {
		if status := server.apply(request{Operation: "status", ProfileID: profile.id}); !status.OK {
			t.Fatalf("remaining status %s failed: %#v", profile.id, status)
		}
		process := server.profiles[profile.id].value.(*tunnelProcess)
		if err := process.route.verify(); err != nil {
			t.Fatal(err)
		}
		if err := process.endpointRoute.verify(); err != nil {
			t.Fatal(err)
		}
		if profile.id == profileIDTwo {
			if process.fallbackRoute == nil {
				t.Fatal("/32 split route cleanup changed a surviving session")
			}
			if err := process.fallbackRoute.verify(); err != nil {
				t.Fatal(err)
			}
		} else if process.fallbackRoute != nil || process.precedenceEndpointRoute != nil {
			t.Fatal("split route cleanup changed a surviving session")
		}
	}
	const replacementID = "44444444-2222-4333-8444-555555555555"
	if started := server.apply(request{Operation: "start", ProfileID: replacementID, Config: "private_key=0404040404040404040404040404040404040404040404040404040404040404"}); !started.OK {
		t.Fatalf("replacement start failed at %s: %#v", backend.(*tunnelBackend).lastStartStage, started)
	}
	replacement := server.profiles[replacementID].value.(*tunnelProcess)
	if replacement.route == nil || replacement.endpointRoute == nil || replacement.fallbackRoute == nil || replacement.precedenceEndpointRoute == nil || replacement.route.target != middleRoute.target || replacement.endpointRoute.target != middleEndpointRoute.target || replacement.precedenceEndpointRoute.target != middlePrecedenceEndpointRoute.target {
		t.Fatal("stopped session route slot was not reused")
	}
	if err := replacement.route.verify(); err != nil {
		t.Fatal(err)
	}
	if err := replacement.endpointRoute.verify(); err != nil {
		t.Fatal(err)
	}
	if err := replacement.fallbackRoute.verify(); err != nil {
		t.Fatal(err)
	}
	if err := replacement.precedenceEndpointRoute.verify(); err != nil {
		t.Fatal(err)
	}
}
