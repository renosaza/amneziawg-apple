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

func TestSyntheticPhysicalEndpointValidation(t *testing.T) {
	if _, err := syntheticEndpoint("203.0.113.10"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"192.0.2.10", "203.0.114.10", "::1"} {
		if _, err := syntheticEndpoint(target); err == nil {
			t.Fatalf("accepted non-TEST-NET-3 endpoint %q", target)
		}
	}
	if _, err := syntheticEndpointInPrefix("1.1.1.1", netip.PrefixFrom(netip.MustParseAddr("0.0.0.0"), 0)); err == nil {
		t.Fatal("accepted a non-synthetic endpoint prefix")
	}
}

func TestSyntheticEndpointRouteTargetsAreDistinct(t *testing.T) {
	if len(syntheticEndpointRouteTargets) != len(syntheticIPv4RouteSpecs) {
		t.Fatal("synthetic endpoint routes do not match tunnel slot capacity")
	}
	seen := make(map[string]bool)
	for _, target := range syntheticEndpointRouteTargets {
		endpoint, err := syntheticEndpoint(target)
		if err != nil || seen[endpoint.String()] {
			t.Fatalf("invalid or duplicate endpoint target: %s", target)
		}
		seen[endpoint.String()] = true
	}
	if seen[syntheticPhysicalProbe.String()] || syntheticPrecedencePrefix.Contains(syntheticPhysicalProbe) {
		t.Fatal("physical gateway probe collides with a synthetic endpoint or fallback route")
	}
}

func TestSyntheticPhysicalEndpointOwnership(t *testing.T) {
	configured := &PhysicalEndpointRoute{
		target:  netip.MustParseAddr("203.0.113.10"),
		gateway: netip.MustParseAddr("192.168.1.1"),
		iface:   &net.Interface{Index: 7, Name: "en0"},
	}
	defaultRoute := physicalRouteMessage(7, syscall.RTF_UP|syscall.RTF_GATEWAY, [4]byte{}, [4]byte{192, 168, 1, 1}, "en0")
	if configured.isTargetHostRoute(defaultRoute) {
		t.Fatal("treated default route as an endpoint host route")
	}
	existingHost := physicalRouteMessage(7, syscall.RTF_UP|syscall.RTF_HOST|syscall.RTF_GATEWAY, configured.target.As4(), configured.gateway.As4(), "en0")
	if !configured.isTargetHostRoute(existingHost) {
		t.Fatal("did not detect an existing endpoint host route")
	}
	owned := physicalRouteMessage(7, syscall.RTF_UP|syscall.RTF_HOST|syscall.RTF_GATEWAY|syscall.RTF_STATIC, configured.target.As4(), configured.gateway.As4(), "en0")
	if !configured.ownsRoute(owned) {
		t.Fatal("did not accept owned endpoint route")
	}
	wrongGateway := physicalRouteMessage(7, owned.Flags, configured.target.As4(), [4]byte{192, 168, 1, 254}, "en0")
	if configured.ownsRoute(wrongGateway) {
		t.Fatal("accepted endpoint route through a changed gateway")
	}
	wrongInterface := physicalRouteMessage(8, owned.Flags, configured.target.As4(), configured.gateway.As4(), "en1")
	if configured.ownsRoute(wrongInterface) {
		t.Fatal("accepted endpoint route through a changed interface")
	}
	nonStatic := *owned
	nonStatic.Flags &^= syscall.RTF_STATIC
	if configured.ownsRoute(&nonStatic) {
		t.Fatal("accepted non-static endpoint route")
	}
	ribOwned := *owned
	ribOwned.Addrs = append([]route.Addr(nil), owned.Addrs...)
	ribOwned.Addrs[syscall.RTAX_IFP] = nil
	if !configured.ownsRIBRouteWithInterfaceByIndex(&ribOwned, func(index int) (*net.Interface, error) {
		if index != configured.iface.Index {
			t.Fatalf("lookup index = %d", index)
		}
		return configured.iface, nil
	}) {
		t.Fatal("did not recognize owned endpoint route without RIB interface metadata")
	}
	lookup := effectiveRouteLookupAddrs(configured.target)
	if _, ok := lookup[syscall.RTAX_IFP].(*route.LinkAddr); !ok || lookup[syscall.RTAX_NETMASK] != nil {
		t.Fatal("effective lookup did not request only destination and interface metadata")
	}
	write := configured.routeAddrs()
	if _, ok := write[syscall.RTAX_GATEWAY].(*route.Inet4Addr); !ok {
		t.Fatal("endpoint route write omitted IPv4 gateway")
	}
	if _, ok := write[syscall.RTAX_NETMASK].(*route.Inet4Addr); !ok {
		t.Fatal("endpoint route write omitted host netmask")
	}
}

func TestSyntheticPhysicalEndpointRejectsUtunDiscovery(t *testing.T) {
	message := physicalRouteMessage(7, syscall.RTF_UP|syscall.RTF_GATEWAY, [4]byte{}, [4]byte{192, 168, 1, 1}, "utun7")
	if _, _, _, err := physicalRouteMetadata(message); err == nil {
		t.Fatal("accepted a utun as the physical endpoint route")
	}
}

func TestSyntheticPhysicalEndpointReadsGatewayAndInterface(t *testing.T) {
	message := physicalRouteMessage(7, syscall.RTF_UP|syscall.RTF_GATEWAY, [4]byte{}, [4]byte{192, 168, 1, 1}, "en0")
	gateway, index, name, err := physicalRouteMetadata(message)
	if err != nil || gateway != netip.MustParseAddr("192.168.1.1") || index != 7 || name != "en0" {
		t.Fatalf("physical gateway metadata = %s %d %q %v", gateway, index, name, err)
	}
}

func TestPlannedEndpointBaseRouteIdentity(t *testing.T) {
	configured := &PhysicalEndpointRoute{basePrefix: netip.MustParsePrefix("192.0.2.0/24"), gateway: netip.MustParseAddr("192.0.2.1"), iface: &net.Interface{Index: 7, Name: "en0"}}
	message := physicalRouteMessage(7, syscall.RTF_UP|syscall.RTF_GATEWAY, [4]byte{192, 0, 2, 0}, [4]byte{192, 0, 2, 1}, "en0")
	message.Addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte{255, 255, 255, 0}}
	if !configured.matchesBaseRoute(message) {
		t.Fatal("did not match base route")
	}
	message.Index = 8
	if configured.matchesBaseRoute(message) {
		t.Fatal("accepted changed base route")
	}
	message.Index = 7
	message.Addrs[syscall.RTAX_IFP] = &route.LinkAddr{Index: 8, Name: "en1"}
	if configured.matchesBaseRoute(message) {
		t.Fatal("accepted changed interface metadata")
	}
	message.Addrs[syscall.RTAX_IFP] = &route.LinkAddr{Index: 7, Name: "en0"}
	message.Flags &^= syscall.RTF_GATEWAY
	if configured.matchesBaseRoute(message) {
		t.Fatal("accepted non-gateway base route")
	}
}

func TestPlannedEndpointRIBBaseRouteWithoutIFP(t *testing.T) {
	target, gateway := netip.MustParseAddr("192.0.2.9"), netip.MustParseAddr("192.0.2.1")
	iface := &net.Interface{Index: 7, Name: "en0"}
	lookup := func(index int) (*net.Interface, error) {
		if index != iface.Index {
			t.Fatalf("lookup index = %d", index)
		}
		return iface, nil
	}
	base := physicalRouteMessage(7, syscall.RTF_UP|syscall.RTF_GATEWAY, [4]byte{}, gateway.As4(), "en0")
	base.Addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte{0, 0, 0, 0}}
	base.Addrs[syscall.RTAX_IFP] = nil
	lan := physicalRouteMessage(7, syscall.RTF_UP|syscall.RTF_GATEWAY, [4]byte{192, 0, 2, 0}, gateway.As4(), "en0")
	lan.Addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte{255, 255, 255, 0}}
	lan.Addrs[syscall.RTAX_IFP] = nil
	selected, err := selectBaseRouteWithInterfaceByIndex([]route.Message{base, lan}, target, gateway, iface, lookup)
	if err != nil || selected != netip.MustParsePrefix("192.0.2.0/24") {
		t.Fatalf("selected=%v err=%v", selected, err)
	}

	for _, mutate := range []func(*route.RouteMessage){
		func(message *route.RouteMessage) { message.Index = 0 },
		func(message *route.RouteMessage) { message.Index = 8 },
		func(message *route.RouteMessage) {
			message.Addrs[syscall.RTAX_GATEWAY] = &route.Inet4Addr{IP: [4]byte{192, 0, 2, 2}}
		},
		func(message *route.RouteMessage) { message.Flags &^= syscall.RTF_GATEWAY },
	} {
		candidate := *lan
		candidate.Addrs = append([]route.Addr(nil), lan.Addrs...)
		mutate(&candidate)
		if matchesRIBPhysicalRoute(&candidate, gateway, iface, lookup) {
			t.Fatal("accepted incomplete RIB base route")
		}
	}
	if matchesRIBPhysicalRoute(lan, gateway, iface, func(int) (*net.Interface, error) {
		return &net.Interface{Index: 7, Name: "utun7"}, nil
	}) {
		t.Fatal("accepted a utun RIB fallback")
	}
	if matchesRIBPhysicalRoute(lan, gateway, iface, func(int) (*net.Interface, error) {
		return &net.Interface{Index: 7, Name: "en0", Flags: net.FlagLoopback}, nil
	}) {
		t.Fatal("accepted a loopback RIB fallback")
	}
	if matchesRIBPhysicalRoute(lan, gateway, iface, func(int) (*net.Interface, error) {
		return &net.Interface{Index: 7, Name: "en1"}, nil
	}) {
		t.Fatal("accepted a renamed RIB interface")
	}
	configured := &PhysicalEndpointRoute{basePrefix: netip.MustParsePrefix("192.0.2.0/24"), gateway: gateway, iface: iface}
	if !configured.matchesBaseRouteWithInterfaceByIndex(lan, lookup) {
		t.Fatal("post-add base identity rejected the accepted RIB fallback")
	}
}

func TestSelectPlannedEndpointBaseRoute(t *testing.T) {
	target, gateway := netip.MustParseAddr("192.0.2.9"), netip.MustParseAddr("192.0.2.1")
	iface := &net.Interface{Index: 7, Name: "en0"}
	base := physicalRouteMessage(7, syscall.RTF_UP|syscall.RTF_GATEWAY, [4]byte{}, gateway.As4(), "en0")
	base.Addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte{0, 0, 0, 0}}
	lan := physicalRouteMessage(7, syscall.RTF_UP|syscall.RTF_GATEWAY, [4]byte{192, 0, 2, 0}, gateway.As4(), "en0")
	lan.Addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte{255, 255, 255, 0}}
	selected, err := selectBaseRoute([]route.Message{base, lan}, target, gateway, iface)
	if err != nil || selected != netip.MustParsePrefix("192.0.2.0/24") {
		t.Fatalf("selected=%v err=%v", selected, err)
	}
	wrong := physicalRouteMessage(8, syscall.RTF_UP|syscall.RTF_GATEWAY, [4]byte{192, 0, 2, 0}, gateway.As4(), "en1")
	wrong.Addrs[syscall.RTAX_NETMASK] = lan.Addrs[syscall.RTAX_NETMASK]
	if _, err := selectBaseRoute([]route.Message{wrong}, target, gateway, iface); err == nil {
		t.Fatal("accepted mismatched interface")
	}
	if _, err := selectBaseRoute([]route.Message{lan, lan}, target, gateway, iface); err == nil {
		t.Fatal("accepted ambiguous route")
	}
	if _, err := selectBaseRoute(nil, target, gateway, iface); err == nil {
		t.Fatal("accepted missing route")
	}
}

func TestSelectPhysicalBaseRouteIgnoresUnrelatedUtun(t *testing.T) {
	target := netip.MustParseAddr("203.0.113.18")
	physical := physicalRouteMessage(7, syscall.RTF_UP|syscall.RTF_GATEWAY, [4]byte{}, [4]byte{192, 0, 2, 1}, "en0")
	physical.Addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte{0, 0, 0, 0}}
	unrelated := physicalRouteMessage(9, syscall.RTF_UP|syscall.RTF_GATEWAY, [4]byte{203, 0, 113, 0}, [4]byte{10, 0, 0, 1}, "utun9")
	unrelated.Addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte{255, 255, 255, 0}}
	lookup := func(index int) (*net.Interface, error) {
		switch index {
		case 7:
			return &net.Interface{Index: 7, Name: "en0"}, nil
		case 9:
			return &net.Interface{Index: 9, Name: "utun9"}, nil
		default:
			return nil, net.ErrClosed
		}
	}
	selected, err := selectPhysicalBaseRouteWithInterfaceByIndex([]route.Message{unrelated, physical}, target, lookup)
	if err != nil || selected.prefix != netip.MustParsePrefix("0.0.0.0/0") || selected.gateway != netip.MustParseAddr("192.0.2.1") || selected.iface.Name != "en0" {
		t.Fatalf("selected=%#v err=%v", selected, err)
	}
}

func TestSelectPhysicalBaseRouteFailsClosed(t *testing.T) {
	target := netip.MustParseAddr("203.0.113.18")
	lookup := func(index int) (*net.Interface, error) {
		if index == 7 {
			return &net.Interface{Index: 7, Name: "en0"}, nil
		}
		return nil, net.ErrClosed
	}
	physical := func(destination, mask [4]byte) *route.RouteMessage {
		message := physicalRouteMessage(7, syscall.RTF_UP|syscall.RTF_GATEWAY, destination, [4]byte{192, 0, 2, 1}, "en0")
		message.Addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: mask}
		return message
	}
	defaultRoute := physical([4]byte{}, [4]byte{})
	hostRoute := physical(target.As4(), [4]byte{255, 255, 255, 255})
	if _, err := selectPhysicalBaseRouteWithInterfaceByIndex([]route.Message{defaultRoute, hostRoute}, target, lookup); err == nil {
		t.Fatal("accepted a foreign endpoint host route")
	}
	utunHost := physicalRouteMessage(9, syscall.RTF_UP|syscall.RTF_HOST|syscall.RTF_GATEWAY, target.As4(), [4]byte{10, 0, 0, 1}, "utun9")
	utunHost.Addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte{255, 255, 255, 255}}
	utunLookup := func(index int) (*net.Interface, error) {
		if index == 9 {
			return &net.Interface{Index: 9, Name: "utun9"}, nil
		}
		return lookup(index)
	}
	if _, err := selectPhysicalBaseRouteWithInterfaceByIndex([]route.Message{defaultRoute, utunHost}, target, utunLookup); err == nil {
		t.Fatal("accepted an endpoint host route through utun")
	}
	duplicate := physical([4]byte{}, [4]byte{})
	if _, err := selectPhysicalBaseRouteWithInterfaceByIndex([]route.Message{defaultRoute, duplicate}, target, lookup); err == nil {
		t.Fatal("accepted ambiguous equal physical routes")
	}
	incomplete := physical([4]byte{}, [4]byte{})
	incomplete.Addrs[syscall.RTAX_GATEWAY] = nil
	if _, err := selectPhysicalBaseRouteWithInterfaceByIndex([]route.Message{incomplete}, target, lookup); err == nil {
		t.Fatal("accepted an incomplete physical route")
	}
	incompletePrefix := physical([4]byte{203, 0, 113, 0}, [4]byte{255, 255, 255, 0})
	incompletePrefix.Addrs[syscall.RTAX_GATEWAY] = nil
	if _, err := selectPhysicalBaseRouteWithInterfaceByIndex([]route.Message{defaultRoute, incompletePrefix}, target, lookup); err == nil {
		t.Fatal("used a less-specific physical route after an incomplete physical candidate")
	}
	staleIFP := physical([4]byte{}, [4]byte{})
	if _, _, err := physicalRIBRouteMetadata(staleIFP, func(int) (*net.Interface, error) {
		return &net.Interface{Index: 7, Name: "utun7"}, nil
	}); err == nil {
		t.Fatal("accepted RTAX_IFP metadata after the live interface became utun")
	}
}

func TestSyntheticPhysicalEndpointRejectsChangedGateway(t *testing.T) {
	configured := &PhysicalEndpointRoute{
		gateway: netip.MustParseAddr("192.168.1.1"),
		iface:   &net.Interface{Index: 7, Name: "en0"},
	}
	if !configured.matchesPhysicalGateway(netip.MustParseAddr("192.168.1.1"), &net.Interface{Index: 7, Name: "en0"}) {
		t.Fatal("did not retain the recorded gateway and interface")
	}
	if configured.matchesPhysicalGateway(netip.MustParseAddr("192.168.1.254"), &net.Interface{Index: 7, Name: "en0"}) || configured.matchesPhysicalGateway(netip.MustParseAddr("192.168.1.1"), &net.Interface{Index: 8, Name: "en1"}) {
		t.Fatal("accepted a changed gateway or interface")
	}
}

func TestSyntheticPhysicalEndpointRetainsUnknownAddOutcome(t *testing.T) {
	configured := &PhysicalEndpointRoute{}
	configured.recordRouteWrite(syscall.RTM_ADD, errRouteOutcomeUnknown)
	if !configured.routeSet {
		t.Fatal("lost route recovery state after an unknown add outcome")
	}
	configured = &PhysicalEndpointRoute{}
	configured.recordRouteWrite(syscall.RTM_ADD, syscall.EEXIST)
	if configured.routeSet {
		t.Fatal("retained route recovery state after a kernel-rejected add")
	}
}

func physicalRouteMessage(index, flags int, destination, gateway [4]byte, name string) *route.RouteMessage {
	addrs := make([]route.Addr, syscall.RTAX_IFP+1)
	addrs[syscall.RTAX_DST] = &route.Inet4Addr{IP: destination}
	addrs[syscall.RTAX_GATEWAY] = &route.Inet4Addr{IP: gateway}
	addrs[syscall.RTAX_IFP] = &route.LinkAddr{Index: index, Name: name}
	return &route.RouteMessage{Flags: flags, Index: index, Addrs: addrs}
}
