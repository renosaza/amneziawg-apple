// SPDX-License-Identifier: MIT

//go:build manualruntime

package daemoncontrol

import (
	"os"
	"testing"
)

func TestManualSyntheticThreeTunnelLifecycle(t *testing.T) {
	if os.Getenv("AMNEZIAWG_DAEMON_RUNTIME") != "1" {
		t.Skip("manual disposable-runner test")
	}
	backend, err := NewTunnelBackend(os.Getenv("AMNEZIAWG_DAEMON_BINARY"))
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
	process, ok := server.profiles[profileID].value.(*tunnelProcess)
	if !ok {
		t.Fatal("missing owned tunnel process")
	}
	configured, err := ConfigureSyntheticIPv4Route(process.name, "192.0.2.2", "192.0.2.1", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := configured.Close(); err != nil {
			t.Errorf("route cleanup failed: %v", err)
		}
	})
	if err := configured.verify(); err != nil {
		t.Fatal(err)
	}
	if stopped := server.apply(request{Operation: "stop", ProfileID: profileIDTwo}); !stopped.OK {
		t.Fatalf("stop middle tunnel failed: %#v", stopped)
	}
	for _, profile := range []struct{ id string }{{profileID}, {profileIDThree}} {
		if status := server.apply(request{Operation: "status", ProfileID: profile.id}); !status.OK {
			t.Fatalf("remaining status %s failed: %#v", profile.id, status)
		}
	}
}
