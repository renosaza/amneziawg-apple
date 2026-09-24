// SPDX-License-Identifier: MIT

//go:build darwin

package daemoncontrol

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"syscall"

	"golang.org/x/net/route"
)

var syntheticEndpointPrefix = netip.MustParsePrefix("203.0.113.0/24")
var syntheticPrecedencePrefix = netip.MustParsePrefix("198.51.100.0/24")
var syntheticPhysicalProbe = netip.MustParseAddr("203.0.113.254")

// PhysicalEndpointRoute owns one synthetic endpoint host route via the active physical gateway.
type PhysicalEndpointRoute struct {
	target, gateway netip.Addr
	prefix          netip.Prefix
	basePrefix      netip.Prefix
	iface           *net.Interface
	routeSet        bool
}

type physicalBaseRoute struct {
	prefix  netip.Prefix
	gateway netip.Addr
	iface   *net.Interface
}

// configurePlannedPhysicalEndpointRoute records the endpoint's pre-existing
// physical route and proves it remains in the RIB after installing the owned host route.
func configurePlannedPhysicalEndpointRoute(target netip.Addr) (*PhysicalEndpointRoute, error) {
	if os.Geteuid() != 0 || !target.Is4() || !target.IsGlobalUnicast() {
		return nil, errors.New("planned endpoint route requires IPv4 root")
	}
	configured := &PhysicalEndpointRoute{target: target}
	existing, err := configured.lookup()
	if err != nil || existing.Err != nil {
		return nil, fmt.Errorf("planned endpoint preflight-get: %w", errors.Join(err, existing.Err))
	}
	if configured.isTargetHostRoute(existing) {
		return nil, errors.New("planned endpoint preflight-host-collision")
	}
	base, err := configured.findBaseRoute(target)
	if err != nil {
		return nil, fmt.Errorf("planned endpoint preflight-rib: %w", err)
	}
	configured.basePrefix, configured.gateway, configured.iface = base.prefix, base.gateway, base.iface
	if err := configured.add(); err != nil {
		return configured.cleanupFailedRouteAdd(fmt.Errorf("planned endpoint add: %w", err))
	}
	if err := configured.verify(); err != nil || !configured.baseRouteInRIB() {
		return configured.cleanupFailedRouteAdd(fmt.Errorf("planned endpoint verify-base-rib: %w", errors.Join(err, errors.New("effective endpoint route changed"))))
	}
	return configured, nil
}

func (configured *PhysicalEndpointRoute) findBaseRoute(target netip.Addr) (physicalBaseRoute, error) {
	rib, err := route.FetchRIB(syscall.AF_INET, route.RIBTypeRoute, 0)
	if err != nil {
		return physicalBaseRoute{}, err
	}
	messages, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return physicalBaseRoute{}, err
	}
	return selectPhysicalBaseRoute(messages, target)
}

// selectPhysicalBaseRoute chooses the most-specific physical RIB route for a
// planned endpoint. An existing non-host tunnel route may be the effective
// route, so it is deliberately ignored here. An exact host route is always
// rejected, including one through utun, because its ownership is not ours.
func selectPhysicalBaseRoute(messages []route.Message, target netip.Addr) (physicalBaseRoute, error) {
	return selectPhysicalBaseRouteWithInterfaceByIndex(messages, target, net.InterfaceByIndex)
}

func selectPhysicalBaseRouteWithInterfaceByIndex(messages []route.Message, target netip.Addr, interfaceByIndex func(int) (*net.Interface, error)) (physicalBaseRoute, error) {
	var selected physicalBaseRoute
	for _, parsed := range messages {
		message, ok := parsed.(*route.RouteMessage)
		if !ok || message.Flags&syscall.RTF_UP == 0 {
			continue
		}
		prefix, ok := routePrefix(message)
		if !ok || !prefix.Contains(target) {
			continue
		}
		if prefix.Bits() == 32 {
			return physicalBaseRoute{}, errors.New("foreign endpoint host route")
		}
		gateway, iface, err := physicalRIBRouteMetadata(message, interfaceByIndex)
		if err != nil {
			if !isConfirmedUtunRIBRoute(message, interfaceByIndex) {
				return physicalBaseRoute{}, fmt.Errorf("incomplete physical base route: %w", err)
			}
			continue
		}
		if selected.prefix.IsValid() && selected.prefix.Bits() == prefix.Bits() {
			return physicalBaseRoute{}, errors.New("ambiguous physical base route")
		}
		if !selected.prefix.IsValid() || prefix.Bits() > selected.prefix.Bits() {
			selected = physicalBaseRoute{prefix: prefix, gateway: gateway, iface: iface}
		}
	}
	if !selected.prefix.IsValid() {
		return physicalBaseRoute{}, errors.New("physical base route unavailable")
	}
	return selected, nil
}

func isConfirmedUtunRIBRoute(message *route.RouteMessage, interfaceByIndex func(int) (*net.Interface, error)) bool {
	if message == nil || interfaceByIndex == nil || message.Index <= 0 {
		return false
	}
	iface, err := interfaceByIndex(message.Index)
	return err == nil && iface != nil && iface.Index == message.Index && utunName.MatchString(iface.Name)
}

func physicalRIBRouteMetadata(message *route.RouteMessage, interfaceByIndex func(int) (*net.Interface, error)) (netip.Addr, *net.Interface, error) {
	if message == nil || interfaceByIndex == nil || message.Flags&(syscall.RTF_UP|syscall.RTF_GATEWAY) != syscall.RTF_UP|syscall.RTF_GATEWAY || message.Index <= 0 {
		return netip.Addr{}, nil, errors.New("incomplete physical RIB route")
	}
	gatewayAddress, gatewayOK := routeAddress(message, syscall.RTAX_GATEWAY).(*route.Inet4Addr)
	if !gatewayOK {
		return netip.Addr{}, nil, errors.New("incomplete physical RIB gateway")
	}
	gateway := netip.AddrFrom4(gatewayAddress.IP)
	if !gateway.IsGlobalUnicast() {
		return netip.Addr{}, nil, errors.New("physical RIB gateway unavailable")
	}
	iface, err := interfaceByIndex(message.Index)
	if err != nil || iface == nil || iface.Index != message.Index || iface.Name == "" || iface.Flags&net.FlagLoopback != 0 || utunName.MatchString(iface.Name) {
		return netip.Addr{}, nil, errors.New("physical RIB interface unavailable")
	}
	if address := routeAddress(message, syscall.RTAX_IFP); address != nil {
		link, ok := address.(*route.LinkAddr)
		if !ok || link.Index != iface.Index || link.Name != iface.Name {
			return netip.Addr{}, nil, errors.New("physical RIB interface changed")
		}
	}
	return gateway, iface, nil
}

func selectBaseRoute(messages []route.Message, target, gateway netip.Addr, iface *net.Interface) (netip.Prefix, error) {
	return selectBaseRouteWithInterfaceByIndex(messages, target, gateway, iface, net.InterfaceByIndex)
}

func selectBaseRouteWithInterfaceByIndex(messages []route.Message, target, gateway netip.Addr, iface *net.Interface, interfaceByIndex func(int) (*net.Interface, error)) (netip.Prefix, error) {
	var selected netip.Prefix
	for _, parsed := range messages {
		message, ok := parsed.(*route.RouteMessage)
		if !ok {
			continue
		}
		prefix, ok := routePrefix(message)
		if !ok || !prefix.Contains(target) {
			continue
		}
		if !matchesRIBPhysicalRoute(message, gateway, iface, interfaceByIndex) {
			continue
		}
		if selected.IsValid() && selected.Bits() == prefix.Bits() {
			return netip.Prefix{}, errors.New("ambiguous base route")
		}
		if !selected.IsValid() || prefix.Bits() > selected.Bits() {
			selected = prefix
		}
	}
	if !selected.IsValid() {
		return netip.Prefix{}, errors.New("base route unavailable")
	}
	return selected, nil
}

func configureSyntheticPhysicalEndpointRoute(targetText string) (*PhysicalEndpointRoute, error) {
	return configureSyntheticPhysicalEndpointRouteInPrefix(targetText, syntheticEndpointPrefix)
}

func configureSyntheticPrecedenceEndpointRoute(targetText string) (*PhysicalEndpointRoute, error) {
	return configureSyntheticPhysicalEndpointRouteInPrefix(targetText, syntheticPrecedencePrefix)
}

func configureSyntheticPhysicalEndpointRouteInPrefix(targetText string, prefix netip.Prefix) (*PhysicalEndpointRoute, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("synthetic endpoint route requires root")
	}
	target, err := syntheticEndpointInPrefix(targetText, prefix)
	if err != nil {
		return nil, err
	}
	configured := &PhysicalEndpointRoute{target: target, prefix: prefix}
	existing, err := configured.lookup()
	if err != nil || existing.Err != nil {
		return nil, errors.Join(err, existing.Err)
	}
	if configured.isTargetHostRoute(existing) {
		return nil, errors.New("refusing to replace an existing endpoint host route")
	}
	configured.gateway, configured.iface, err = physicalGateway(existing)
	if err != nil {
		return nil, err
	}
	if err := configured.validatePhysicalGateway(); err != nil {
		return nil, err
	}
	if err := configured.add(); err != nil {
		return configured.cleanupFailedRouteAdd(err)
	}
	if err := configured.verify(); err != nil {
		return configured.cleanupFailedRouteAdd(err)
	}
	if err := configured.validatePhysicalGateway(); err != nil {
		return configured.cleanupFailedRouteAdd(err)
	}
	return configured, nil
}

func syntheticEndpoint(text string) (netip.Addr, error) {
	return syntheticEndpointInPrefix(text, syntheticEndpointPrefix)
}

func syntheticEndpointInPrefix(text string, prefix netip.Prefix) (netip.Addr, error) {
	target, err := netip.ParseAddr(text)
	if err != nil || !target.Is4() || (prefix != syntheticEndpointPrefix && prefix != syntheticPrecedencePrefix) || !prefix.Contains(target) {
		return netip.Addr{}, errors.New("synthetic endpoint must be allowed TEST-NET IPv4")
	}
	return target, nil
}

func physicalGateway(message *route.RouteMessage) (netip.Addr, *net.Interface, error) {
	gateway, index, name, err := physicalRouteMetadata(message)
	if err != nil {
		return netip.Addr{}, nil, err
	}
	iface, err := net.InterfaceByIndex(index)
	if err != nil || iface.Name != name || iface.Flags&net.FlagLoopback != 0 {
		return netip.Addr{}, nil, errors.New("effective endpoint route physical interface changed")
	}
	return gateway, iface, nil
}

func physicalRouteMetadata(message *route.RouteMessage) (netip.Addr, int, string, error) {
	if message == nil {
		return netip.Addr{}, 0, "", errors.New("effective endpoint route is unavailable")
	}
	if message.Flags&(syscall.RTF_UP|syscall.RTF_GATEWAY) != syscall.RTF_UP|syscall.RTF_GATEWAY {
		return netip.Addr{}, 0, "", errors.New("effective endpoint route has no physical gateway")
	}
	gatewayAddress, gatewayOK := routeAddress(message, syscall.RTAX_GATEWAY).(*route.Inet4Addr)
	interfaceAddress, interfaceOK := routeAddress(message, syscall.RTAX_IFP).(*route.LinkAddr)
	if !gatewayOK || !interfaceOK || interfaceAddress.Index <= 0 || interfaceAddress.Name == "" || message.Index != interfaceAddress.Index {
		return netip.Addr{}, 0, "", errors.New("effective endpoint route has incomplete physical metadata")
	}
	gateway := netip.AddrFrom4(gatewayAddress.IP)
	if !gateway.IsGlobalUnicast() || utunName.MatchString(interfaceAddress.Name) {
		return netip.Addr{}, 0, "", errors.New("effective endpoint route is not physical")
	}
	return gateway, interfaceAddress.Index, interfaceAddress.Name, nil
}

func (configured *PhysicalEndpointRoute) add() error { return configured.write(syscall.RTM_ADD) }

func (configured *PhysicalEndpointRoute) Close() error {
	if !configured.routeSet {
		return nil
	}
	if err := configured.verify(); err != nil {
		return err
	}
	return configured.write(syscall.RTM_DELETE)
}

func (configured *PhysicalEndpointRoute) verify() error {
	message, err := configured.lookup()
	if err != nil || message.Err != nil {
		return errors.Join(err, message.Err)
	}
	if !configured.ownsRoute(message) {
		return errors.New("synthetic endpoint route ownership changed")
	}
	return nil
}

func (configured *PhysicalEndpointRoute) proveAbsent() error {
	message, err := configured.lookup()
	if err != nil {
		return err
	}
	if message == nil {
		return errors.New("synthetic endpoint route lookup returned no message")
	}
	if message.Err != nil {
		return message.Err
	}
	if configured.isTargetHostRoute(message) {
		return errors.New("synthetic endpoint route remains after cleanup")
	}
	configured.routeSet = false
	return nil
}

func (configured *PhysicalEndpointRoute) write(kind int) error {
	message, err := requestRouteMessage(kind, syscall.RTF_UP|syscall.RTF_HOST|syscall.RTF_GATEWAY|syscall.RTF_STATIC, configured.routeAddrs())
	if err != nil {
		configured.recordRouteWrite(kind, err)
		return err
	}
	configured.recordRouteWrite(kind, message.Err)
	return message.Err
}

func (configured *PhysicalEndpointRoute) recordRouteWrite(kind int, err error) {
	if err == nil {
		configured.routeSet = kind == syscall.RTM_ADD
	} else if kind == syscall.RTM_ADD && errors.Is(err, errRouteOutcomeUnknown) {
		configured.routeSet = true
	}
}

func (configured *PhysicalEndpointRoute) cleanupFailedRouteAdd(err error) (*PhysicalEndpointRoute, error) {
	if !configured.routeSet {
		return nil, err
	}
	if cleanupErr := configured.Close(); cleanupErr != nil {
		return configured, errors.Join(err, cleanupErr)
	}
	return nil, err
}

func (configured *PhysicalEndpointRoute) lookup() (*route.RouteMessage, error) {
	return requestRouteMessage(syscall.RTM_GET, syscall.RTF_UP|syscall.RTF_HOST, effectiveRouteLookupAddrs(configured.target))
}

func (configured *PhysicalEndpointRoute) validatePhysicalGateway() error {
	message, err := requestRouteMessage(syscall.RTM_GET, syscall.RTF_UP|syscall.RTF_HOST, effectiveRouteLookupAddrs(syntheticPhysicalProbe))
	if err != nil || message.Err != nil {
		return errors.Join(err, message.Err)
	}
	gateway, iface, err := physicalGateway(message)
	if err != nil || !configured.matchesPhysicalGateway(gateway, iface) {
		return errors.New("effective endpoint gateway changed")
	}
	return nil
}

func (configured *PhysicalEndpointRoute) matchesPhysicalGateway(gateway netip.Addr, iface *net.Interface) bool {
	return iface != nil && gateway == configured.gateway && iface.Index == configured.iface.Index && iface.Name == configured.iface.Name
}

func (configured *PhysicalEndpointRoute) baseRouteInRIB() bool {
	rib, err := route.FetchRIB(syscall.AF_INET, route.RIBTypeRoute, 0)
	if err != nil {
		return false
	}
	messages, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return false
	}
	for _, parsed := range messages {
		message, ok := parsed.(*route.RouteMessage)
		if !ok {
			continue
		}
		if configured.matchesBaseRoute(message) {
			return true
		}
	}
	return false
}

func (configured *PhysicalEndpointRoute) matchesBaseRoute(message *route.RouteMessage) bool {
	return configured.matchesBaseRouteWithInterfaceByIndex(message, net.InterfaceByIndex)
}

func (configured *PhysicalEndpointRoute) matchesBaseRouteWithInterfaceByIndex(message *route.RouteMessage, interfaceByIndex func(int) (*net.Interface, error)) bool {
	prefix, ok := routePrefix(message)
	return ok && prefix == configured.basePrefix && matchesRIBPhysicalRoute(message, configured.gateway, configured.iface, interfaceByIndex)
}

// matchesRIBPhysicalRoute accepts complete RIB metadata, or Darwin's observed
// nil RTAX_IFP form only after resolving the recorded physical interface again.
func matchesRIBPhysicalRoute(message *route.RouteMessage, gateway netip.Addr, iface *net.Interface, interfaceByIndex func(int) (*net.Interface, error)) bool {
	if message == nil {
		return false
	}
	if routeAddress(message, syscall.RTAX_IFP) != nil {
		actualGateway, index, name, err := physicalRouteMetadata(message)
		return err == nil && actualGateway == gateway && iface != nil && index == iface.Index && name == iface.Name
	}
	if iface == nil || interfaceByIndex == nil || message.Index <= 0 || message.Index != iface.Index || message.Flags&(syscall.RTF_UP|syscall.RTF_GATEWAY) != syscall.RTF_UP|syscall.RTF_GATEWAY {
		return false
	}
	actualGateway, gatewayOK := routeAddress(message, syscall.RTAX_GATEWAY).(*route.Inet4Addr)
	if !gatewayOK || netip.AddrFrom4(actualGateway.IP) != gateway {
		return false
	}
	actualInterface, err := interfaceByIndex(message.Index)
	return err == nil && actualInterface != nil && actualInterface.Index == iface.Index && actualInterface.Name == iface.Name && actualInterface.Flags&net.FlagLoopback == 0 && !utunName.MatchString(actualInterface.Name)
}

func (configured *PhysicalEndpointRoute) routeAddrs() []route.Addr {
	addrs := make([]route.Addr, syscall.RTAX_IFP+1)
	addrs[syscall.RTAX_DST] = &route.Inet4Addr{IP: configured.target.As4()}
	addrs[syscall.RTAX_GATEWAY] = &route.Inet4Addr{IP: configured.gateway.As4()}
	addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte{255, 255, 255, 255}}
	addrs[syscall.RTAX_IFP] = &route.LinkAddr{Index: configured.iface.Index, Name: configured.iface.Name}
	return addrs
}

func effectiveRouteLookupAddrs(target netip.Addr) []route.Addr {
	addrs := make([]route.Addr, syscall.RTAX_IFP+1)
	addrs[syscall.RTAX_DST] = &route.Inet4Addr{IP: target.As4()}
	addrs[syscall.RTAX_IFP] = &route.LinkAddr{}
	return addrs
}

func (configured *PhysicalEndpointRoute) ownsRoute(message *route.RouteMessage) bool {
	if !configured.isTargetHostRoute(message) || message.Flags&syscall.RTF_STATIC == 0 || message.Flags&syscall.RTF_GATEWAY == 0 || message.Index != configured.iface.Index {
		return false
	}
	gateway, gatewayOK := routeAddress(message, syscall.RTAX_GATEWAY).(*route.Inet4Addr)
	interfaceAddress, interfaceOK := routeAddress(message, syscall.RTAX_IFP).(*route.LinkAddr)
	return gatewayOK && interfaceOK && gateway.IP == configured.gateway.As4() && interfaceAddress.Index == configured.iface.Index && interfaceAddress.Name == configured.iface.Name
}

func (configured *PhysicalEndpointRoute) ownsRIBRoute(message *route.RouteMessage) bool {
	return configured.ownsRIBRouteWithInterfaceByIndex(message, net.InterfaceByIndex)
}

func (configured *PhysicalEndpointRoute) ownsRIBRouteWithInterfaceByIndex(message *route.RouteMessage, interfaceByIndex func(int) (*net.Interface, error)) bool {
	return configured.isTargetHostRoute(message) && message.Flags&(syscall.RTF_STATIC|syscall.RTF_GATEWAY) == syscall.RTF_STATIC|syscall.RTF_GATEWAY &&
		matchesRIBPhysicalRoute(message, configured.gateway, configured.iface, interfaceByIndex)
}

func (configured *PhysicalEndpointRoute) isTargetHostRoute(message *route.RouteMessage) bool {
	destination, destinationOK := routeAddress(message, syscall.RTAX_DST).(*route.Inet4Addr)
	return destinationOK && message.Flags&(syscall.RTF_UP|syscall.RTF_HOST) == syscall.RTF_UP|syscall.RTF_HOST && destination.IP == configured.target.As4()
}
