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
	configured, err := configureSyntheticIPv4Route(process, "192.0.2.2", "192.0.2.1", "192.0.2.10")
	if configured != nil {
		t.Cleanup(func() {
			if err := configured.Close(); err != nil {
				t.Errorf("route cleanup failed: %v", err)
				return
			}
			t.Logf("removed synthetic route target=%s interface=%s index=%d", configured.target, configured.name, configured.iface.Index)
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := configured.verify(); err != nil {
		t.Fatal(err)
	}
	t.Logf("owned synthetic route target=%s interface=%s index=%d", configured.target, configured.name, configured.iface.Index)
	endpointRoute, err := configureSyntheticPhysicalEndpointRoute("203.0.113.10")
	if endpointRoute != nil {
		t.Cleanup(func() {
			if err := endpointRoute.Close(); err != nil {
				t.Errorf("endpoint route cleanup failed: %v", err)
				return
			}
			t.Logf("removed synthetic endpoint route target=%s gateway=%s interface=%s index=%d", endpointRoute.target, endpointRoute.gateway, endpointRoute.iface.Name, endpointRoute.iface.Index)
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := endpointRoute.verify(); err != nil {
		t.Fatal(err)
	}
	t.Logf("owned synthetic endpoint route target=%s gateway=%s interface=%s index=%d", endpointRoute.target, endpointRoute.gateway, endpointRoute.iface.Name, endpointRoute.iface.Index)
	precedenceEndpoint, err := configureSyntheticPrecedenceEndpointRoute("198.51.100.10")
	if precedenceEndpoint != nil {
		t.Cleanup(func() {
			if err := precedenceEndpoint.Close(); err != nil {
				t.Errorf("precedence endpoint cleanup failed: %v", err)
				return
			}
			t.Logf("removed synthetic precedence endpoint target=%s gateway=%s interface=%s index=%d", precedenceEndpoint.target, precedenceEndpoint.gateway, precedenceEndpoint.iface.Name, precedenceEndpoint.iface.Index)
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	fallbackRoute, err := configureSyntheticFallbackRoute(process)
	if fallbackRoute != nil {
		t.Cleanup(func() {
			if err := fallbackRoute.Close(); err != nil {
				t.Errorf("fallback route cleanup failed: %v", err)
				return
			}
			t.Logf("removed synthetic fallback prefix=%s interface=%s index=%d", fallbackRoute.prefix, fallbackRoute.name, fallbackRoute.iface.Index)
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := fallbackRoute.verify(); err != nil {
		t.Fatal(err)
	}
	if err := precedenceEndpoint.verify(); err != nil {
		t.Fatal(err)
	}
	t.Logf("owned synthetic fallback prefix=%s interface=%s index=%d", fallbackRoute.prefix, fallbackRoute.name, fallbackRoute.iface.Index)
	t.Logf("owned synthetic precedence endpoint target=%s gateway=%s interface=%s index=%d", precedenceEndpoint.target, precedenceEndpoint.gateway, precedenceEndpoint.iface.Name, precedenceEndpoint.iface.Index)
	if stopped := server.apply(request{Operation: "stop", ProfileID: profileIDTwo}); !stopped.OK {
		t.Fatalf("stop middle tunnel failed: %#v", stopped)
	}
	for _, profile := range []struct{ id string }{{profileID}, {profileIDThree}} {
		if status := server.apply(request{Operation: "status", ProfileID: profile.id}); !status.OK {
			t.Fatalf("remaining status %s failed: %#v", profile.id, status)
		}
	}
}
