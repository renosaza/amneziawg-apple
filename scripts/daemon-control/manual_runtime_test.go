// SPDX-License-Identifier: MIT

//go:build manualruntime

package daemoncontrol

import (
	"os"
	"testing"
)

func TestManualSyntheticTunnelLifecycle(t *testing.T) {
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
	started := server.apply(request{Operation: "start", ProfileID: profileID, Config: "private_key=0101010101010101010101010101010101010101010101010101010101010101"})
	if !started.OK {
		t.Fatalf("start failed: %#v", started)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			if response := server.apply(request{Operation: "stop", ProfileID: profileID}); !response.OK {
				t.Errorf("cleanup failed: %#v", response)
			}
		}
	})
	if status := server.apply(request{Operation: "status", ProfileID: profileID}); !status.OK {
		t.Fatalf("status failed: %#v", status)
	}
	if stopped := server.apply(request{Operation: "stop", ProfileID: profileID}); !stopped.OK {
		t.Fatalf("stop failed: %#v", stopped)
	}
	stopped = true
}
