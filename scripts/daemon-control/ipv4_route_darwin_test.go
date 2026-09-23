// SPDX-License-Identifier: MIT

//go:build darwin

package daemoncontrol

import (
	"errors"
	"net"
	"net/netip"
	"syscall"
	"testing"

	"golang.org/x/net/route"
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

func TestSyntheticIPv4RouteWriteOutcome(t *testing.T) {
	configured := &IPv4Route{}
	configured.recordRouteWrite(syscall.RTM_ADD, errRouteOutcomeUnknown)
	if !configured.routeSet {
		t.Fatal("lost route recovery state after an unknown add outcome")
	}
	configured = &IPv4Route{}
	configured.recordRouteWrite(syscall.RTM_ADD, syscall.EEXIST)
	if configured.routeSet {
		t.Fatal("retained route recovery state after a kernel-rejected add")
	}
}

func TestSyntheticIPv4RouteLookupOwnership(t *testing.T) {
	configured := &IPv4Route{
		name:   "utun7",
		target: netip.MustParseAddr("192.0.2.10"),
		iface:  &net.Interface{Index: 7, Name: "utun7"},
	}
	defaultRoute := routeMessage(7, syscall.RTF_UP, [4]byte{0, 0, 0, 0})
	if configured.isTargetHostRoute(defaultRoute) {
		t.Fatal("treated the default route as a target host route")
	}
	foreignExact := routeMessage(8, syscall.RTF_UP|syscall.RTF_HOST|syscall.RTF_STATIC, [4]byte{192, 0, 2, 10})
	if !configured.isTargetHostRoute(foreignExact) || configured.ownsRoute(foreignExact) {
		t.Fatal("did not reject a foreign exact host route")
	}
	ownedExact := routeMessage(7, syscall.RTF_UP|syscall.RTF_HOST|syscall.RTF_STATIC, [4]byte{192, 0, 2, 10})
	if !configured.ownsRoute(ownedExact) {
		t.Fatal("did not accept the owned exact host route")
	}
	nonStatic := *ownedExact
	nonStatic.Flags &^= syscall.RTF_STATIC
	if configured.ownsRoute(&nonStatic) {
		t.Fatal("accepted a non-static route")
	}
	wrongMessageIndex := *ownedExact
	wrongMessageIndex.Index = 8
	if configured.ownsRoute(&wrongMessageIndex) {
		t.Fatal("accepted a route with a foreign route-message index")
	}
	wrongGateway := routeMessage(7, syscall.RTF_UP|syscall.RTF_HOST|syscall.RTF_STATIC, [4]byte{192, 0, 2, 10})
	wrongGateway.Addrs[syscall.RTAX_GATEWAY] = &route.LinkAddr{Index: 8, Name: "utun8"}
	if configured.ownsRoute(wrongGateway) {
		t.Fatal("accepted a route with a foreign gateway interface")
	}
	wrongInterface := routeMessage(7, syscall.RTF_UP|syscall.RTF_HOST|syscall.RTF_STATIC, [4]byte{192, 0, 2, 10})
	wrongInterface.Addrs[syscall.RTAX_IFP] = &route.LinkAddr{Index: 8, Name: "utun8"}
	if configured.ownsRoute(wrongInterface) {
		t.Fatal("accepted a route with a foreign interface address")
	}
	addrs := configured.routeLookupAddrs()
	link, ok := addrs[syscall.RTAX_IFP].(*route.LinkAddr)
	if !ok || link.Index != configured.iface.Index || link.Name != configured.name {
		t.Fatal("route lookup does not ask for the recorded utun interface")
	}
	if addrs[syscall.RTAX_NETMASK] != nil {
		t.Fatal("route lookup included a netmask")
	}
}

func routeMessage(index, flags int, destination [4]byte) *route.RouteMessage {
	addrs := make([]route.Addr, syscall.RTAX_IFP+1)
	addrs[syscall.RTAX_DST] = &route.Inet4Addr{IP: destination}
	addrs[syscall.RTAX_GATEWAY] = &route.LinkAddr{Index: index, Name: "utun"}
	addrs[syscall.RTAX_IFP] = &route.LinkAddr{Index: index, Name: "utun"}
	return &route.RouteMessage{Flags: flags, Index: index, Addrs: addrs}
}
