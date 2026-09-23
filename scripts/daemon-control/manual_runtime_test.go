// SPDX-License-Identifier: MIT

//go:build manualruntime

package daemoncontrol

import (
	"os"
	"testing"
)

func newTunnelBackendForManualRuntime(binary string) (Backend, error) {
	backend, err := newTunnelBackend(binary)
	if err != nil {
		return nil, err
	}
	backend.(*tunnelBackend).syntheticIPv4Routes = true
	return backend, nil
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
	targets := make(map[string]bool)
	for _, profile := range profiles {
		process, ok := server.profiles[profile.id].value.(*tunnelProcess)
		if !ok || process.route == nil {
			t.Fatalf("missing owned synthetic route for %s", profile.id)
		}
		if err := process.route.verify(); err != nil {
			t.Fatal(err)
		}
		if targets[process.route.target.String()] {
			t.Fatal("synthetic routes share a target")
		}
		targets[process.route.target.String()] = true
		t.Logf("owned synthetic route target=%s interface=%s index=%d", process.route.target, process.route.name, process.route.iface.Index)
	}
	middle := server.profiles[profileIDTwo].value.(*tunnelProcess)
	middleRoute := middle.route
	if stopped := server.apply(request{Operation: "stop", ProfileID: profileIDTwo}); !stopped.OK {
		t.Fatalf("stop middle tunnel failed: %#v", stopped)
	}
	if err := middleRoute.proveAbsentAfterTunnelExit(); err != nil {
		t.Fatal(err)
	}
	for _, profile := range []struct{ id string }{{profileID}, {profileIDThree}} {
		if status := server.apply(request{Operation: "status", ProfileID: profile.id}); !status.OK {
			t.Fatalf("remaining status %s failed: %#v", profile.id, status)
		}
		process := server.profiles[profile.id].value.(*tunnelProcess)
		if err := process.route.verify(); err != nil {
			t.Fatal(err)
		}
	}
	const replacementID = "44444444-2222-4333-8444-555555555555"
	if started := server.apply(request{Operation: "start", ProfileID: replacementID, Config: "private_key=0404040404040404040404040404040404040404040404040404040404040404"}); !started.OK {
		t.Fatalf("replacement start failed: %#v", started)
	}
	replacement := server.profiles[replacementID].value.(*tunnelProcess)
	if replacement.route == nil || replacement.route.target != middleRoute.target {
		t.Fatal("stopped session route slot was not reused")
	}
	if err := replacement.route.verify(); err != nil {
		t.Fatal(err)
	}
}
