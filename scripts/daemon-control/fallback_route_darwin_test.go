// SPDX-License-Identifier: MIT

//go:build darwin

package daemoncontrol

import (
	"net"
	"net/netip"
	"syscall"
	"testing"

	"golang.org/x/net/route"
)

func TestPlannedSplitRejectsForeignMoreSpecificRIBRoute(t *testing.T) {
	planned := netip.MustParsePrefix("198.51.100.0/24")
	foreign := fallbackRouteMessage(8, syscall.RTF_UP, [4]byte{198, 51, 100, 200}, [4]byte{255, 255, 255, 255}, "utun8")
	if !hasForeignMoreSpecificRoute([]route.Message{foreign}, planned, nil) {
		t.Fatal("accepted foreign shadow away from probe")
	}
	endpoint := &PhysicalEndpointRoute{target: netip.MustParseAddr("198.51.100.200"), gateway: netip.MustParseAddr("192.0.2.1"), iface: &net.Interface{Index: 8, Name: "utun8"}}
	foreign.Addrs[syscall.RTAX_GATEWAY] = &route.Inet4Addr{IP: [4]byte{192, 0, 2, 2}}
	if !hasForeignMoreSpecificRoute([]route.Message{foreign}, planned, endpoint) {
		t.Fatal("accepted foreign route sharing endpoint prefix")
	}
}

func TestPlannedSplitIgnoresNonRouteRIBMessage(t *testing.T) {
	if hasForeignMoreSpecificRoute([]route.Message{&route.InterfaceMessage{}}, netip.MustParsePrefix("198.51.100.0/24"), nil) {
		t.Fatal("non-route RIB message became a conflict")
	}
}

func TestSyntheticFallbackRouteOwnership(t *testing.T) {
	configured := &SyntheticFallbackRoute{
		name:   "utun7",
		prefix: syntheticPrecedencePrefix,
		iface:  &net.Interface{Index: 7, Name: "utun7"},
	}
	defaultRoute := fallbackRouteMessage(7, syscall.RTF_UP, [4]byte{}, [4]byte{}, "utun7")
	if configured.isFallbackRoute(defaultRoute) {
		t.Fatal("treated default route as the synthetic fallback")
	}
	foreign := fallbackRouteMessage(8, syscall.RTF_UP|syscall.RTF_STATIC, configured.prefix.Addr().As4(), syntheticFallbackMask, "utun8")
	if !configured.isFallbackRoute(foreign) || configured.ownsRoute(foreign) {
		t.Fatal("did not reject a foreign synthetic fallback")
	}
	owned := fallbackRouteMessage(7, syscall.RTF_UP|syscall.RTF_STATIC, configured.prefix.Addr().As4(), syntheticFallbackMask, "utun7")
	if !configured.ownsRoute(owned) {
		t.Fatal("did not accept owned synthetic fallback")
	}
	host := *owned
	host.Flags |= syscall.RTF_HOST
	if configured.isFallbackRoute(&host) {
		t.Fatal("accepted a host route as fallback")
	}
	hostPrefix := &SyntheticFallbackRoute{prefix: netip.MustParsePrefix("198.51.100.11/32")}
	nonHost := fallbackRouteMessage(7, syscall.RTF_UP|syscall.RTF_STATIC, hostPrefix.prefix.Addr().As4(), [4]byte{255, 255, 255, 255}, "utun7")
	if hostPrefix.isFallbackRoute(nonHost) {
		t.Fatal("accepted a non-host route as /32 split route")
	}
	nonHost.Flags |= syscall.RTF_HOST
	if !hostPrefix.isFallbackRoute(nonHost) {
		t.Fatal("rejected a host route as /32 split route")
	}
	shadow := fallbackRouteMessage(8, syscall.RTF_UP|syscall.RTF_HOST, configured.probe().As4(), [4]byte{}, "utun8")
	if !configured.isMoreSpecificRoute(shadow) {
		t.Fatal("did not reject a foreign host route shadowing the fallback probe")
	}
	partialShadow := fallbackRouteMessage(8, syscall.RTF_UP, configured.prefix.Addr().As4(), [4]byte{255, 255, 255, 128}, "utun8")
	if !configured.isMoreSpecificRoute(partialShadow) {
		t.Fatal("did not reject a foreign partial prefix shadowing the fallback probe")
	}
	if !configured.canRecoverShadowedRoute(shadow, true) {
		t.Fatal("did not allow owner-checked cleanup of a shadowed fallback")
	}
	if configured.canRecoverShadowedRoute(defaultRoute, true) {
		t.Fatal("accepted RIB ownership without an effective shadow route")
	}
	lookup := effectiveRouteLookupAddrs(configured.probe())
	if _, ok := lookup[syscall.RTAX_IFP].(*route.LinkAddr); !ok || lookup[syscall.RTAX_NETMASK] != nil {
		t.Fatal("fallback lookup did not request only destination and interface metadata")
	}
}

func TestSyntheticFallbackSpecificPrecedence(t *testing.T) {
	fallback := &SyntheticFallbackRoute{
		name:   "utun7",
		prefix: syntheticPrecedencePrefix,
		iface:  &net.Interface{Index: 7, Name: "utun7"},
	}
	specific := &PhysicalEndpointRoute{
		target:  netip.MustParseAddr("198.51.100.10"),
		prefix:  syntheticPrecedencePrefix,
		gateway: netip.MustParseAddr("192.168.1.1"),
		iface:   &net.Interface{Index: 4, Name: "en0"},
	}
	if !fallback.prefix.Contains(specific.target) || fallback.prefix.Bits() >= 32 {
		t.Fatal("specific endpoint is not inside a narrower fallback prefix")
	}
	fallbackRoute := fallbackRouteMessage(7, syscall.RTF_UP|syscall.RTF_STATIC, fallback.prefix.Addr().As4(), syntheticFallbackMask, "utun7")
	specificRoute := physicalRouteMessage(4, syscall.RTF_UP|syscall.RTF_HOST|syscall.RTF_GATEWAY|syscall.RTF_STATIC, specific.target.As4(), specific.gateway.As4(), "en0")
	if !fallback.ownsRoute(fallbackRoute) || !specific.ownsRoute(specificRoute) {
		t.Fatal("did not retain both fallback and more-specific route owners")
	}
}

func TestSyntheticFallbackRetainsUnknownAddOutcome(t *testing.T) {
	configured := &SyntheticFallbackRoute{}
	configured.recordRouteWrite(syscall.RTM_ADD, errRouteOutcomeUnknown)
	if !configured.routeSet {
		t.Fatal("lost fallback recovery state after an unknown add outcome")
	}
	configured = &SyntheticFallbackRoute{}
	configured.recordRouteWrite(syscall.RTM_ADD, syscall.EEXIST)
	if configured.routeSet {
		t.Fatal("retained fallback recovery state after a kernel-rejected add")
	}
}

func TestSyntheticSplitRouteSupportsCanonicalPrefixSizes(t *testing.T) {
	for _, prefix := range []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("198.51.100.11/32")} {
		configured := &SyntheticFallbackRoute{prefix: prefix}
		if configured.probe() != prefix.Addr() && prefix.Bits() == 32 {
			t.Fatal("host route probe changed destination")
		}
		expected := [4]byte{255, 255, 255, 0}
		if prefix.Bits() == 32 {
			expected[3] = 255
		}
		if prefixMask(prefix) != expected {
			t.Fatalf("wrong mask for %s", prefix)
		}
	}
}

func fallbackRouteMessage(index, flags int, destination, mask [4]byte, name string) *route.RouteMessage {
	addrs := make([]route.Addr, syscall.RTAX_IFP+1)
	addrs[syscall.RTAX_DST] = &route.Inet4Addr{IP: destination}
	addrs[syscall.RTAX_GATEWAY] = &route.LinkAddr{Index: index, Name: name}
	addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: mask}
	addrs[syscall.RTAX_IFP] = &route.LinkAddr{Index: index, Name: name}
	return &route.RouteMessage{Flags: flags, Index: index, Addrs: addrs}
}
