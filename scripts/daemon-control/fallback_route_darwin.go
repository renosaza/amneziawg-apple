// SPDX-License-Identifier: MIT

//go:build darwin

package daemoncontrol

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"syscall"

	"golang.org/x/net/route"
)

var syntheticFallbackMask = [4]byte{255, 255, 255, 0}

// SyntheticFallbackRoute owns the narrow fallback prefix for the precedence POC.
type SyntheticFallbackRoute struct {
	name     string
	prefix   netip.Prefix
	iface    *net.Interface
	routeSet bool
}

func configureSyntheticFallbackRoute(process *tunnelProcess) (*SyntheticFallbackRoute, error) {
	if process == nil || os.Geteuid() != 0 || !utunName.MatchString(process.name) {
		return nil, errors.New("synthetic fallback route requires root and a utun")
	}
	iface, err := net.InterfaceByName(process.name)
	if err != nil {
		return nil, err
	}
	configured := &SyntheticFallbackRoute{name: process.name, prefix: syntheticPrecedencePrefix, iface: iface}
	existing, err := configured.lookup()
	if err != nil || existing.Err != nil {
		return nil, errors.Join(err, existing.Err)
	}
	if configured.isFallbackRoute(existing) {
		return nil, errors.New("refusing to replace an existing fallback route")
	}
	if configured.isMoreSpecificRoute(existing) {
		return nil, errors.New("refusing a fallback route shadowed by a more-specific route")
	}
	if err := configured.add(); err != nil {
		return configured.cleanupFailedRouteAdd(err)
	}
	if err := configured.verify(); err != nil {
		return configured.cleanupFailedRouteAdd(err)
	}
	return configured, nil
}

func (configured *SyntheticFallbackRoute) add() error { return configured.write(syscall.RTM_ADD) }

func (configured *SyntheticFallbackRoute) Close() error {
	if !configured.routeSet {
		return nil
	}
	message, err := configured.lookup()
	if err != nil || message.Err != nil {
		return errors.Join(err, message.Err)
	}
	if configured.ownsRoute(message) {
		return configured.write(syscall.RTM_DELETE)
	}
	ownedInRIB, err := configured.ownsRouteInRIB()
	if err != nil {
		return err
	}
	if !configured.canRecoverShadowedRoute(message, ownedInRIB) {
		return errors.New("synthetic fallback route ownership changed")
	}
	return configured.write(syscall.RTM_DELETE)
}

func (configured *SyntheticFallbackRoute) verify() error {
	message, err := configured.lookup()
	if err != nil || message.Err != nil {
		return errors.Join(err, message.Err)
	}
	if !configured.ownsRoute(message) {
		return errors.New("synthetic fallback route ownership changed")
	}
	return nil
}

// proveAbsentAfterTunnelExit releases ownership only when the exited utun no
// longer owns or shadows the synthetic fallback route.
func (configured *SyntheticFallbackRoute) proveAbsentAfterTunnelExit() error {
	interfaces, err := currentUtuns()
	if err != nil {
		return err
	}
	if interfaces[configured.name] {
		return errors.New("owned utun remains after backend exit")
	}
	ownedInRIB, err := configured.ownsRouteInRIB()
	if err != nil {
		return err
	}
	if ownedInRIB {
		return errors.New("synthetic fallback route remains after backend exit")
	}
	message, err := configured.lookup()
	if err != nil {
		return err
	}
	if message == nil {
		return errors.New("synthetic fallback route lookup returned no message")
	}
	if message.Err != nil {
		return message.Err
	}
	if configured.isFallbackRoute(message) {
		return errors.New("synthetic fallback route remains effective after backend exit")
	}
	configured.routeSet = false
	return nil
}

func (configured *SyntheticFallbackRoute) write(kind int) error {
	message, err := requestRouteMessage(kind, syscall.RTF_UP|syscall.RTF_STATIC, configured.routeAddrs())
	if err != nil {
		configured.recordRouteWrite(kind, err)
		return err
	}
	configured.recordRouteWrite(kind, message.Err)
	return message.Err
}

func (configured *SyntheticFallbackRoute) recordRouteWrite(kind int, err error) {
	if err == nil {
		configured.routeSet = kind == syscall.RTM_ADD
	} else if kind == syscall.RTM_ADD && errors.Is(err, errRouteOutcomeUnknown) {
		configured.routeSet = true
	}
}

func (configured *SyntheticFallbackRoute) cleanupFailedRouteAdd(err error) (*SyntheticFallbackRoute, error) {
	if !configured.routeSet {
		return nil, err
	}
	if cleanupErr := configured.Close(); cleanupErr != nil {
		return configured, errors.Join(err, cleanupErr)
	}
	return nil, err
}

func (configured *SyntheticFallbackRoute) lookup() (*route.RouteMessage, error) {
	return requestRouteMessage(syscall.RTM_GET, syscall.RTF_UP, effectiveRouteLookupAddrs(configured.probe()))
}

func (configured *SyntheticFallbackRoute) probe() netip.Addr {
	return configured.prefix.Addr().Next()
}

func (configured *SyntheticFallbackRoute) routeAddrs() []route.Addr {
	addrs := make([]route.Addr, syscall.RTAX_IFP+1)
	addrs[syscall.RTAX_DST] = &route.Inet4Addr{IP: configured.prefix.Addr().As4()}
	addrs[syscall.RTAX_GATEWAY] = &route.LinkAddr{Index: configured.iface.Index, Name: configured.name}
	addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: syntheticFallbackMask}
	addrs[syscall.RTAX_IFP] = &route.LinkAddr{Index: configured.iface.Index, Name: configured.name}
	return addrs
}

func (configured *SyntheticFallbackRoute) ownsRoute(message *route.RouteMessage) bool {
	if !configured.isFallbackRoute(message) || message.Flags&syscall.RTF_STATIC == 0 || message.Index != configured.iface.Index {
		return false
	}
	gateway, gatewayOK := routeAddress(message, syscall.RTAX_GATEWAY).(*route.LinkAddr)
	interfaceAddress, interfaceOK := routeAddress(message, syscall.RTAX_IFP).(*route.LinkAddr)
	return gatewayOK && interfaceOK && gateway.Index == configured.iface.Index && gateway.Name == configured.name && interfaceAddress.Index == configured.iface.Index && interfaceAddress.Name == configured.name
}

func (configured *SyntheticFallbackRoute) isFallbackRoute(message *route.RouteMessage) bool {
	prefix, ok := routePrefix(message)
	return ok && message.Flags&syscall.RTF_UP != 0 && message.Flags&syscall.RTF_HOST == 0 && prefix == configured.prefix
}

func (configured *SyntheticFallbackRoute) isMoreSpecificRoute(message *route.RouteMessage) bool {
	prefix, ok := routePrefix(message)
	return ok && message.Flags&syscall.RTF_UP != 0 && prefix.Contains(configured.probe()) && prefix.Bits() > configured.prefix.Bits()
}

func (configured *SyntheticFallbackRoute) canRecoverShadowedRoute(effective *route.RouteMessage, ownedInRIB bool) bool {
	return ownedInRIB && configured.isMoreSpecificRoute(effective)
}

func (configured *SyntheticFallbackRoute) ownsRouteInRIB() (bool, error) {
	rib, err := route.FetchRIB(syscall.AF_INET, route.RIBTypeRoute, 0)
	if err != nil {
		return false, err
	}
	messages, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return false, err
	}
	for _, parsed := range messages {
		message, ok := parsed.(*route.RouteMessage)
		if ok && configured.ownsRoute(message) {
			return true, nil
		}
	}
	return false, nil
}

func routePrefix(message *route.RouteMessage) (netip.Prefix, bool) {
	destination, ok := routeAddress(message, syscall.RTAX_DST).(*route.Inet4Addr)
	if !ok {
		return netip.Prefix{}, false
	}
	if message.Flags&syscall.RTF_HOST != 0 {
		return netip.PrefixFrom(netip.AddrFrom4(destination.IP), 32), true
	}
	mask, ok := routeAddress(message, syscall.RTAX_NETMASK).(*route.Inet4Addr)
	if !ok {
		return netip.Prefix{}, false
	}
	ones, bits := net.IPMask(mask.IP[:]).Size()
	if bits != 32 {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(netip.AddrFrom4(destination.IP), ones).Masked(), true
}
