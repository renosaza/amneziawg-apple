// SPDX-License-Identifier: MIT

//go:build darwin

package daemoncontrol

import (
	"errors"
	"syscall"
	"testing"
)

func TestSyntheticIPv4Validation(t *testing.T) {
	if _, _, _, err := ipv4("192.0.2.2", "192.0.2.1", "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ipv4("192.0.2.2", "192.0.2.2", "192.0.2.10"); err == nil {
		t.Fatal("accepted duplicate synthetic address")
	}
	if _, _, _, err := ipv4("::1", "192.0.2.1", "192.0.2.10"); err == nil {
		t.Fatal("accepted non-IPv4 address")
	}
}

func TestSyntheticIPv4AddressStateValidation(t *testing.T) {
	if err := requireAddressState("preflight", addressStateAbsent, addressStateAbsent); err != nil {
		t.Fatal(err)
	}
	if err := requireAddressState("cleanup", addressStateAbsent, addressStateOwned); err == nil {
		t.Fatal("accepted an absent address as owned")
	}
	if err := requireAddressState("cleanup", addressStateErrorCode, addressStateOwned); err == nil {
		t.Fatal("accepted an unreadable address state")
	}
}

func TestSyntheticIPv4CleanupStateTransitions(t *testing.T) {
	configured := &IPv4Route{routeSet: true, addressSet: true, upChanged: true}
	configured.recordRouteWrite(syscall.RTM_DELETE, nil)
	configured.recordAddressRemoval(nil)
	configured.recordFlagRestore(nil)
	if configured.routeSet || configured.addressSet || configured.upChanged {
		t.Fatal("successful cleanup did not release ownership")
	}

	configured = &IPv4Route{routeSet: true, addressSet: true, upChanged: true}
	cleanupErr := errors.New("cleanup failed")
	configured.recordRouteWrite(syscall.RTM_DELETE, cleanupErr)
	configured.recordAddressRemoval(cleanupErr)
	configured.recordFlagRestore(cleanupErr)
	if !configured.routeSet || !configured.addressSet || !configured.upChanged {
		t.Fatal("failed cleanup released ownership")
	}
}
