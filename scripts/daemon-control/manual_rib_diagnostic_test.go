// SPDX-License-Identifier: MIT

//go:build darwin && manualruntime

package daemoncontrol

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"syscall"
	"testing"

	"golang.org/x/net/route"
)

const manualRIBDiagnosticLimit = 12

// TestManualPlannedEndpointRIBDiagnostic reads only the route lookup and RIB
// for a TEST-NET endpoint. It deliberately neither starts a backend nor sends UAPI.
func TestManualPlannedEndpointRIBDiagnostic(t *testing.T) {
	if os.Getenv("AMNEZIAWG_DAEMON_RUNTIME") != "1" {
		t.Skip("manual disposable-runner diagnostic")
	}
	target := netip.MustParseAddr("203.0.113.20")
	configured := &PhysicalEndpointRoute{target: target}
	lookup, err := configured.lookup()
	t.Logf("MANUAL-TAG RTM_GET target=%s %s", target, routeDiagnostic(lookup))
	if err != nil || lookup == nil || lookup.Err != nil {
		t.Logf("MANUAL-TAG RTM_GET error=%v", errors.Join(err, routeMessageError(lookup)))
		return
	}
	gateway, iface, metadataErr := physicalGateway(lookup)
	t.Logf("MANUAL-TAG RTM_GET physical gateway=%s interface=%s index=%d error=%v", gateway, interfaceName(iface), interfaceIndex(iface), metadataErr)
	rib, err := route.FetchRIB(syscall.AF_INET, route.RIBTypeRoute, 0)
	if err != nil {
		t.Logf("MANUAL-TAG RIB fetch error=%v", err)
		return
	}
	messages, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		t.Logf("MANUAL-TAG RIB parse error=%v", err)
		return
	}
	t.Logf("MANUAL-TAG RIB messages=%d target=%s gateway=%s interface=%s index=%d", len(messages), target, gateway, interfaceName(iface), interfaceIndex(iface))
	printed := 0
	for _, parsed := range messages {
		message, ok := parsed.(*route.RouteMessage)
		if !ok {
			continue
		}
		prefix, prefixOK := routePrefix(message)
		if !noNetmaskDefault(message) && (!prefixOK || !prefix.Contains(target)) {
			continue
		}
		if printed == manualRIBDiagnosticLimit {
			break
		}
		printed++
		t.Logf("MANUAL-TAG RIB candidate=%d %s", printed, ribCandidateDiagnostic(message, target, gateway, iface))
	}
	if printed == manualRIBDiagnosticLimit {
		t.Logf("MANUAL-TAG RIB candidates truncated=true")
	}
	if metadataErr != nil {
		t.Logf("MANUAL-TAG RIB selector skipped=%v", metadataErr)
	} else if prefix, err := selectBaseRoute(messages, target, gateway, iface); err != nil {
		t.Logf("MANUAL-TAG RIB selector error=%v", err)
	} else {
		t.Logf("MANUAL-TAG RIB selector prefix=%s", prefix)
	}
}

func ribCandidateDiagnostic(message *route.RouteMessage, target, gateway netip.Addr, iface *net.Interface) string {
	prefix, prefixOK := routePrefix(message)
	actualGateway, index, name, metadataErr := physicalRouteMetadata(message)
	return fmt.Sprintf("%s prefix=%s prefix-parse=%t contains-target=%t gateway=%s gateway-mismatch=%s ifp=%s/%d ifp-mismatch=%s physical-flags=%t no-netmask-default=%t metadata-error=%v",
		routeDiagnostic(message), prefix, prefixOK, prefixOK && prefix.Contains(target), actualGateway, gatewayMismatch(actualGateway, gateway), name, index, interfaceMismatch(index, name, iface), message.Flags&(syscall.RTF_UP|syscall.RTF_GATEWAY) == syscall.RTF_UP|syscall.RTF_GATEWAY, noNetmaskDefault(message), metadataErr)
}

func gatewayMismatch(actual, expected netip.Addr) string {
	if !expected.IsValid() {
		return "unavailable"
	}
	return fmt.Sprint(actual != expected)
}

func interfaceMismatch(index int, name string, expected *net.Interface) string {
	if expected == nil {
		return "unavailable"
	}
	return fmt.Sprint(index != expected.Index || name != expected.Name)
}

func routeDiagnostic(message *route.RouteMessage) string {
	if message == nil {
		return "route=nil"
	}
	return fmt.Sprintf("flags=%#x index=%d dst=%v gateway=%v ifp=%v netmask=%v", message.Flags, message.Index, routeAddress(message, syscall.RTAX_DST), routeAddress(message, syscall.RTAX_GATEWAY), routeAddress(message, syscall.RTAX_IFP), routeAddress(message, syscall.RTAX_NETMASK))
}

func noNetmaskDefault(message *route.RouteMessage) bool {
	destination, ok := routeAddress(message, syscall.RTAX_DST).(*route.Inet4Addr)
	return ok && message.Flags&syscall.RTF_HOST == 0 && destination.IP == [4]byte{} && routeAddress(message, syscall.RTAX_NETMASK) == nil
}

func routeMessageError(message *route.RouteMessage) error {
	if message == nil {
		return nil
	}
	return message.Err
}

func interfaceName(iface *net.Interface) string {
	if iface == nil {
		return ""
	}
	return iface.Name
}

func interfaceIndex(iface *net.Interface) int {
	if iface == nil {
		return 0
	}
	return iface.Index
}
