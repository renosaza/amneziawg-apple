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

// PhysicalEndpointRoute owns, or safely reuses, one endpoint host route via the active physical gateway.
type PhysicalEndpointRoute struct {
	target, gateway netip.Addr
	prefix          netip.Prefix
	basePrefix      netip.Prefix
	iface           *net.Interface
	routeSet        bool
	reused          bool
}

type physicalBaseRoute struct {
	prefix  netip.Prefix
	gateway netip.Addr
	iface   *net.Interface
}

const (
	physicalEndpointStageEffectiveLookup = "physical_endpoint_effective_lookup"
	physicalEndpointStageRIBSelection    = "physical_endpoint_rib_selection"
	physicalEndpointStageReuseHost       = "physical_endpoint_reuse_host"
	physicalEndpointStageRTMAdd          = "physical_endpoint_rtm_add"
	physicalEndpointStageVerifyHost      = "physical_endpoint_verify_host"
	physicalEndpointStageVerifyBaseRIB   = "physical_endpoint_verify_base_rib"
)

// physicalEndpointRouteFailure carries a fixed diagnostic stage while keeping
// the original error available to existing cleanup and error propagation.
type physicalEndpointRouteFailure struct {
	stage string
	err   error
}

func (failure *physicalEndpointRouteFailure) Error() string { return failure.err.Error() }
func (failure *physicalEndpointRouteFailure) Unwrap() error { return failure.err }

func failedPhysicalEndpointRoute(stage string, err error) error {
	return &physicalEndpointRouteFailure{stage: stage, err: err}
}

func physicalEndpointRouteDiagnostic(err error) (string, string, bool) {
	var failure *physicalEndpointRouteFailure
	if !errors.As(err, &failure) {
		return "", "", false
	}
	return failure.stage, physicalEndpointDiagnosticCode(err), true
}

// configurePlannedPhysicalEndpointRoute records the endpoint's pre-existing
// physical route and proves it remains in the RIB after installing the owned host route.
func configurePlannedPhysicalEndpointRoute(target netip.Addr) (*PhysicalEndpointRoute, error) {
	if os.Geteuid() != 0 || !target.Is4() || !target.IsGlobalUnicast() {
		return nil, errors.New("planned endpoint route requires IPv4 root")
	}
	configured := &PhysicalEndpointRoute{target: target}
	existing, err := configured.lookup()
	if err != nil || existing == nil || existing.Err != nil {
		var routeErr error
		if existing != nil {
			routeErr = existing.Err
		} else {
			routeErr = errors.New("planned endpoint preflight-get returned no route")
		}
		return nil, failedPhysicalEndpointRoute(physicalEndpointStageEffectiveLookup, fmt.Errorf("planned endpoint preflight-get: %w", errors.Join(err, routeErr)))
	}
	base, err := configured.findBaseRoute(target, configured.isTargetHostRoute(existing))
	if err != nil {
		return nil, failedPhysicalEndpointRoute(physicalEndpointStageRIBSelection, fmt.Errorf("planned endpoint preflight-rib: %w", err))
	}
	configured.basePrefix, configured.gateway, configured.iface = base.prefix, base.gateway, base.iface
	if configured.isTargetHostRoute(existing) {
		if !configured.matchesReusableHostRoute(existing) {
			return nil, failedPhysicalEndpointRoute(physicalEndpointStageReuseHost, errPhysicalEndpointReuseRejected)
		}
		configured.reused = true
		return configured, nil
	}
	if err := configured.add(); err != nil {
		return configured.cleanupFailedRouteAdd(failedPhysicalEndpointRoute(physicalEndpointStageRTMAdd, fmt.Errorf("planned endpoint add: %w", err)))
	}
	if err := configured.verify(); err != nil {
		return configured.cleanupFailedRouteAdd(failedPhysicalEndpointRoute(physicalEndpointStageVerifyHost, fmt.Errorf("planned endpoint verify-host: %w", err)))
	}
	if !configured.baseRouteInRIB() {
		return configured.cleanupFailedRouteAdd(failedPhysicalEndpointRoute(physicalEndpointStageVerifyBaseRIB, errors.New("effective endpoint route changed")))
	}
	return configured, nil
}

func (configured *PhysicalEndpointRoute) findBaseRoute(target netip.Addr, ignoreTargetHost bool) (physicalBaseRoute, error) {
	rib, err := route.FetchRIB(syscall.AF_INET, route.RIBTypeRoute, 0)
	if err != nil {
		return physicalBaseRoute{}, err
	}
	messages, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return physicalBaseRoute{}, err
	}
	return selectPhysicalBaseRoute(messages, target, ignoreTargetHost)
}

func (configured *PhysicalEndpointRoute) findReplacementBaseRoute() (physicalBaseRoute, error) {
	rib, err := route.FetchRIB(syscall.AF_INET, route.RIBTypeRoute, 0)
	if err != nil {
		return physicalBaseRoute{}, err
	}
	messages, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return physicalBaseRoute{}, err
	}
	filtered := make([]route.Message, 0, len(messages))
	for _, message := range messages {
		if routeMessage, ok := message.(*route.RouteMessage); ok && configured.ownsKnownHostRoute(routeMessage) {
			continue
		}
		filtered = append(filtered, message)
	}
	return selectPhysicalBaseRoute(filtered, configured.target, false)
}

// selectPhysicalBaseRoute chooses the most-specific physical RIB route for a
// planned endpoint. An existing non-host tunnel route may be the effective
// route, so it is deliberately ignored here. A caller may skip the endpoint's
// exact host route only while separately proving that it is reusable.
func selectPhysicalBaseRoute(messages []route.Message, target netip.Addr, ignoreTargetHost bool) (physicalBaseRoute, error) {
	return selectPhysicalBaseRouteWithInterfaceByIndex(messages, target, ignoreTargetHost, net.InterfaceByIndex)
}

func selectPhysicalBaseRouteWithInterfaceByIndex(messages []route.Message, target netip.Addr, ignoreTargetHost bool, interfaceByIndex func(int) (*net.Interface, error)) (physicalBaseRoute, error) {
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
		if prefix.Bits() == 32 && !ignoreTargetHost {
			return physicalBaseRoute{}, errors.New("foreign endpoint host route")
		}
		if prefix.Bits() == 32 {
			continue
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

// Rebind changes the daemon-owned host route in place. It never deletes the
// old route first: uncertainty keeps the old route and reports degradation.
func (configured *PhysicalEndpointRoute) Rebind() (bool, error) {
	if !configured.routeSet {
		if configured.reused {
			return false, errors.Join(configured.verify(), configured.baseRouteInRIBError())
		}
		return false, errors.New("endpoint route is not owned")
	}
	next, err := configured.findReplacementBaseRoute()
	if err != nil {
		return false, fmt.Errorf("endpoint rebind preflight: %w", err)
	}
	decision, err := endpointRebindDecisionFor(configured.currentBase(), next)
	if err != nil {
		return false, err
	}
	if decision == endpointRebindUnchanged {
		return false, errors.Join(configured.verify(), configured.baseRouteInRIBError())
	}
	if err := configured.writeWithBase(syscall.RTM_CHANGE, next); err != nil {
		return configured.reconcileRebindFailure(next, err)
	}
	if err := configured.verifyWithBase(next); err != nil {
		// The route change can succeed while the post-change RIB proof is
		// incomplete. Retain actual new ownership so Stop can still clean it.
		if owns, ownershipErr := configured.ownsRouteWithBase(next); ownershipErr == nil && owns {
			configured.setBase(next)
		}
		return false, err
	}
	configured.setBase(next)
	return true, nil
}

type endpointRebindDecision uint8

const (
	endpointRebindUnchanged endpointRebindDecision = iota
	endpointRebindChange
)

func endpointRebindDecisionFor(current, next physicalBaseRoute) (endpointRebindDecision, error) {
	if !validPhysicalBaseRoute(current) || !validPhysicalBaseRoute(next) {
		return 0, errors.New("endpoint rebind physical route unavailable")
	}
	if samePhysicalBaseRoute(current, next) {
		return endpointRebindUnchanged, nil
	}
	return endpointRebindChange, nil
}

func validPhysicalBaseRoute(base physicalBaseRoute) bool {
	return base.prefix.IsValid() && base.gateway.IsValid() && base.gateway.IsGlobalUnicast() && base.iface != nil && base.iface.Index > 0 && base.iface.Name != "" && base.iface.Flags&net.FlagLoopback == 0 && !utunName.MatchString(base.iface.Name)
}

func samePhysicalBaseRoute(left, right physicalBaseRoute) bool {
	return left.prefix == right.prefix && left.gateway == right.gateway && left.iface != nil && right.iface != nil && left.iface.Index == right.iface.Index && left.iface.Name == right.iface.Name
}

func (configured *PhysicalEndpointRoute) currentBase() physicalBaseRoute {
	return physicalBaseRoute{prefix: configured.basePrefix, gateway: configured.gateway, iface: configured.iface}
}

func (configured *PhysicalEndpointRoute) setBase(base physicalBaseRoute) {
	configured.basePrefix, configured.gateway, configured.iface = base.prefix, base.gateway, base.iface
}

func (configured *PhysicalEndpointRoute) reconcileRebindFailure(next physicalBaseRoute, writeErr error) (bool, error) {
	nextOwned, nextErr := configured.ownsRouteWithBase(next)
	current := configured.currentBase()
	currentOwned, currentErr := configured.ownsRouteWithBase(current)
	base, stateErr := resolvedRebindBase(current, next, nextErr == nil && nextOwned, currentErr == nil && currentOwned)
	configured.setBase(base)
	if stateErr == nil {
		if configured.baseRouteInRIBError() == nil {
			return true, nil
		}
		return false, fmt.Errorf("endpoint rebind changed without RIB proof: %w", writeErr)
	}
	return false, fmt.Errorf("endpoint rebind %v: %w", stateErr, writeErr)
}

func resolvedRebindBase(current, next physicalBaseRoute, nextOwned, currentOwned bool) (physicalBaseRoute, error) {
	if nextOwned {
		return next, nil
	}
	if currentOwned {
		return current, errors.New("rejected")
	}
	return current, errors.New("outcome unknown")
}

func (configured *PhysicalEndpointRoute) ownsRouteWithBase(base physicalBaseRoute) (bool, error) {
	message, err := configured.lookup()
	if err != nil {
		return false, err
	}
	if message == nil {
		return false, errors.New("endpoint route lookup returned no message")
	}
	if message.Err != nil {
		return false, message.Err
	}
	previous := configured.currentBase()
	configured.setBase(base)
	owned := configured.ownsRoute(message)
	configured.setBase(previous)
	return owned, nil
}

func (configured *PhysicalEndpointRoute) verifyWithBase(base physicalBaseRoute) error {
	previous := configured.currentBase()
	configured.setBase(base)
	err := errors.Join(configured.verify(), configured.baseRouteInRIBError())
	configured.setBase(previous)
	return err
}

func (configured *PhysicalEndpointRoute) baseRouteInRIBError() error {
	if configured.baseRouteInRIB() {
		return nil
	}
	return errors.New("endpoint base route changed")
}

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
	if !configured.ownsRoute(message) && !(configured.reused && configured.matchesReusableHostRoute(message)) {
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

func (configured *PhysicalEndpointRoute) writeWithBase(kind int, base physicalBaseRoute) error {
	message, err := requestRouteMessage(kind, syscall.RTF_UP|syscall.RTF_HOST|syscall.RTF_GATEWAY|syscall.RTF_STATIC, configured.routeAddrsFor(base))
	if err != nil {
		return err
	}
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

// matchesRIBPhysicalRoute resolves the live interface for every RIB form,
// including Darwin's observed nil RTAX_IFP form.
func matchesRIBPhysicalRoute(message *route.RouteMessage, gateway netip.Addr, iface *net.Interface, interfaceByIndex func(int) (*net.Interface, error)) bool {
	actualGateway, actualInterface, err := physicalRIBRouteMetadata(message, interfaceByIndex)
	return err == nil && iface != nil && actualGateway == gateway && actualInterface.Index == iface.Index && actualInterface.Name == iface.Name
}

func (configured *PhysicalEndpointRoute) routeAddrs() []route.Addr {
	return configured.routeAddrsFor(configured.currentBase())
}

func (configured *PhysicalEndpointRoute) routeAddrsFor(base physicalBaseRoute) []route.Addr {
	addrs := make([]route.Addr, syscall.RTAX_IFP+1)
	addrs[syscall.RTAX_DST] = &route.Inet4Addr{IP: configured.target.As4()}
	addrs[syscall.RTAX_GATEWAY] = &route.Inet4Addr{IP: base.gateway.As4()}
	addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte{255, 255, 255, 255}}
	addrs[syscall.RTAX_IFP] = &route.LinkAddr{Index: base.iface.Index, Name: base.iface.Name}
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
	return configured.ownsRIBRouteWithInterfaceByIndex(message, net.InterfaceByIndex) || (configured.reused && configured.matchesReusableHostRoute(message))
}

func (configured *PhysicalEndpointRoute) matchesReusableHostRoute(message *route.RouteMessage) bool {
	return configured.matchesReusableHostRouteWithInterfaceByIndex(message, net.InterfaceByIndex)
}

func (configured *PhysicalEndpointRoute) matchesReusableHostRouteWithInterfaceByIndex(message *route.RouteMessage, interfaceByIndex func(int) (*net.Interface, error)) bool {
	if !configured.isTargetHostRoute(message) || !validPhysicalBaseRoute(configured.currentBase()) {
		return false
	}
	gateway, iface, err := physicalRIBRouteMetadata(message, interfaceByIndex)
	return err == nil && gateway == configured.gateway && iface.Index == configured.iface.Index && iface.Name == configured.iface.Name
}

// ownsKnownHostRoute deliberately avoids a live interface lookup. During an
// uplink transition the old interface may already be gone, but its exact,
// daemon-owned static host route must still be excluded from base-route
// discovery before it can be changed in place.
func (configured *PhysicalEndpointRoute) ownsKnownHostRoute(message *route.RouteMessage) bool {
	if !configured.isTargetHostRoute(message) || configured.iface == nil || message.Flags&(syscall.RTF_STATIC|syscall.RTF_GATEWAY) != syscall.RTF_STATIC|syscall.RTF_GATEWAY || message.Index != configured.iface.Index {
		return false
	}
	gateway, gatewayOK := routeAddress(message, syscall.RTAX_GATEWAY).(*route.Inet4Addr)
	if !gatewayOK || gateway.IP != configured.gateway.As4() {
		return false
	}
	if address := routeAddress(message, syscall.RTAX_IFP); address != nil {
		link, ok := address.(*route.LinkAddr)
		return ok && link.Index == configured.iface.Index && link.Name == configured.iface.Name
	}
	return true
}

func (configured *PhysicalEndpointRoute) ownsRIBRouteWithInterfaceByIndex(message *route.RouteMessage, interfaceByIndex func(int) (*net.Interface, error)) bool {
	return configured.isTargetHostRoute(message) && message.Flags&(syscall.RTF_STATIC|syscall.RTF_GATEWAY) == syscall.RTF_STATIC|syscall.RTF_GATEWAY &&
		matchesRIBPhysicalRoute(message, configured.gateway, configured.iface, interfaceByIndex)
}

func (configured *PhysicalEndpointRoute) isTargetHostRoute(message *route.RouteMessage) bool {
	destination, destinationOK := routeAddress(message, syscall.RTAX_DST).(*route.Inet4Addr)
	return destinationOK && message.Flags&(syscall.RTF_UP|syscall.RTF_HOST) == syscall.RTF_UP|syscall.RTF_HOST && destination.IP == configured.target.As4()
}
