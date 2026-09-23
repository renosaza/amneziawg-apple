// SPDX-License-Identifier: MIT

//go:build darwin

package daemoncontrol

import "testing"

func TestTunnelBackendDefaultsToNoSyntheticRoutes(t *testing.T) {
	backend := &tunnelBackend{}
	if backend.syntheticIPv4Routes || backend.syntheticEndpointRoutes || backend.syntheticFallbackRoute {
		t.Fatal("normal daemon backend enabled synthetic route binding")
	}
}
