// SPDX-License-Identifier: MIT

//go:build darwin

package daemoncontrol

import (
	"errors"
	"net"
	"net/netip"
	"strings"
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

func TestSyntheticIPv4PreflightAcceptsDefaultRouteButRejectsTargetHostRoute(t *testing.T) {
	configured := &IPv4Route{target: netip.MustParseAddr("192.0.2.10")}
	defaultRoute := &route.RouteMessage{Flags: syscall.RTF_UP, Addrs: []route.Addr{
		&route.Inet4Addr{IP: [4]byte{0, 0, 0, 0}},
		nil,
		&route.Inet4Addr{IP: [4]byte{0, 0, 0, 0}},
	}}
	if configured.isTargetHostRoute(defaultRoute) {
		t.Fatal("treated the default route as a target host route")
	}
	targetHostRoute := &route.RouteMessage{Flags: syscall.RTF_UP | syscall.RTF_HOST, Addrs: []route.Addr{
		&route.Inet4Addr{IP: [4]byte{192, 0, 2, 10}},
		nil,
		&route.Inet4Addr{IP: [4]byte{255, 255, 255, 255}},
	}}
	if !configured.isTargetHostRoute(targetHostRoute) {
		t.Fatal("did not identify the target host route")
	}
}

func TestSyntheticIPv4OwnedRouteRequestIncludesIFP(t *testing.T) {
	configured := &IPv4Route{
		name:   "utun7",
		target: netip.MustParseAddr("192.0.2.10"),
		iface:  &net.Interface{Index: 7, Name: "utun7"},
	}
	addrs := configured.ownedRouteRequestAddrs()
	link, ok := addrs[syscall.RTAX_IFP].(*route.LinkAddr)
	if !ok || link.Index != configured.iface.Index || link.Name != configured.name {
		t.Fatal("owned route request does not ask for the recorded utun interface")
	}
	if addrs[syscall.RTAX_GATEWAY] != nil {
		t.Fatal("owned route request placed the interface in the gateway slot")
	}
}

func TestSyntheticIPv4RouteDiagnosticIncludesOnlyBoundedRouteMetadata(t *testing.T) {
	configured := &IPv4Route{
		name:   "utun7",
		target: netip.MustParseAddr("192.0.2.10"),
		iface:  &net.Interface{Index: 7, Name: "utun7"},
	}
	message := &route.RouteMessage{Flags: syscall.RTF_UP | syscall.RTF_HOST, Index: 7, Addrs: []route.Addr{
		&route.Inet4Addr{IP: [4]byte{192, 0, 2, 10}},
		&route.LinkAddr{Name: "utun7", Index: 7, Addr: []byte{1, 2, 3, 4, 5, 6}},
		&route.Inet4Addr{IP: [4]byte{255, 255, 255, 255}},
		nil,
		&route.LinkAddr{Name: "utun7", Index: 7, Addr: []byte{1, 2, 3, 4, 5, 6}},
	}}
	diagnostic := configured.formatRouteOwnershipDiagnostic(message, "0.0.0.0/0(flags=0x1,index=1,ifp=link(name=\"en0\",index=1))")
	for _, expected := range []string{"expected dst=192.0.2.10", "utun=\"utun7\"", "ifp_index=7", "rtm_index=7", "dst=192.0.2.10", "netmask=255.255.255.255", "gateway=link(name=\"utun7\",index=7)", "covering=0.0.0.0/0"} {
		if !strings.Contains(diagnostic, expected) {
			t.Fatalf("diagnostic missing %q: %s", expected, diagnostic)
		}
	}
	if strings.Contains(diagnostic, "010203040506") {
		t.Fatalf("diagnostic exposed link-layer address: %s", diagnostic)
	}
}
