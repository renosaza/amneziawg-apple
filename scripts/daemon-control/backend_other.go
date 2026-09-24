// SPDX-License-Identifier: MIT

//go:build !darwin

package daemoncontrol

func newTunnelBackend(string) (Backend, error) { return nil, errBackendUnavailable }
func newTunnelBackendWithRoutePlanRuntime(string, bool) (Backend, error) {
	return nil, errBackendUnavailable
}
