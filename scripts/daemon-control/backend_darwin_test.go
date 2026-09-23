// SPDX-License-Identifier: MIT

//go:build darwin

package daemoncontrol

import "testing"

func TestTunnelBackendDefaultsToNoSyntheticRoutes(t *testing.T) {
	if (&tunnelBackend{}).syntheticIPv4Routes {
		t.Fatal("normal daemon backend enabled synthetic route binding")
	}
}
