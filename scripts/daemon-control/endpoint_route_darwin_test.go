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
