// SPDX-License-Identifier: MIT

//go:build darwin

package daemoncontrol

/*
#include <arpa/inet.h>
#include <ifaddrs.h>
#include <net/if.h>
#include <netinet/in.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/socket.h>
#include <sys/sockio.h>
#include <stdlib.h>
#include <unistd.h>

enum {
	ADDRESS_STATE_ERROR = -1,
	ADDRESS_STATE_ABSENT = 0,
	ADDRESS_STATE_OWNED = 1,
	ADDRESS_STATE_CONFLICT = 2,
};

static int copy_name(char *destination, size_t size, const char *source) {
	if (strnlen(source, size) >= size) return -1;
	strncpy(destination, source, size - 1);
	return 0;
}

static int get_flags(const char *name, int *result) {
	struct ifreq request = {0};
	int fd, status;
	if (copy_name(request.ifr_name, sizeof(request.ifr_name), name) != 0) return -1;
	fd = socket(AF_INET, SOCK_DGRAM, 0);
	if (fd == -1) return -1;
	status = ioctl(fd, SIOCGIFFLAGS, &request);
	if (status == 0) *result = (unsigned short)request.ifr_flags;
	close(fd);
	return status;
}

static int set_flags(const char *name, int flags) {
	struct ifreq request = {0};
	int fd, status;
	if (copy_name(request.ifr_name, sizeof(request.ifr_name), name) != 0) return -1;
	request.ifr_flags = (short)flags;
	fd = socket(AF_INET, SOCK_DGRAM, 0);
	if (fd == -1) return -1;
	status = ioctl(fd, SIOCSIFFLAGS, &request);
	close(fd);
	return status;
}

static int address_preflight(const char *local) {
	struct ifaddrs *interfaces = NULL, *item;
	struct in_addr wanted;
	int state = ADDRESS_STATE_ABSENT;
	if (inet_pton(AF_INET, local, &wanted) != 1) return ADDRESS_STATE_ERROR;
	if (getifaddrs(&interfaces) != 0) return ADDRESS_STATE_ERROR;
	for (item = interfaces; item != NULL; item = item->ifa_next) {
		struct sockaddr_in *address;
		if (item->ifa_addr == NULL || item->ifa_addr->sa_family != AF_INET) continue;
		address = (struct sockaddr_in *)item->ifa_addr;
		if (memcmp(&address->sin_addr, &wanted, sizeof(wanted)) == 0) {
			state = ADDRESS_STATE_CONFLICT;
			break;
		}
	}
	freeifaddrs(interfaces);
	return state;
}

static int address_ownership(const char *name, const char *local, const char *peer) {
	struct ifaddrs *interfaces = NULL, *item;
	struct in_addr wanted_local, wanted_peer;
	int state = ADDRESS_STATE_ABSENT;
	if (inet_pton(AF_INET, local, &wanted_local) != 1 ||
	    inet_pton(AF_INET, peer, &wanted_peer) != 1) return ADDRESS_STATE_ERROR;
	if (getifaddrs(&interfaces) != 0) return ADDRESS_STATE_ERROR;
	for (item = interfaces; item != NULL; item = item->ifa_next) {
		struct sockaddr_in *address, *destination;
		if (item->ifa_addr == NULL || item->ifa_addr->sa_family != AF_INET) continue;
		address = (struct sockaddr_in *)item->ifa_addr;
		if (memcmp(&address->sin_addr, &wanted_local, sizeof(wanted_local)) != 0) continue;
		if (item->ifa_name == NULL || strcmp(item->ifa_name, name) != 0 ||
		    item->ifa_dstaddr == NULL || item->ifa_dstaddr->sa_family != AF_INET) {
			state = ADDRESS_STATE_CONFLICT;
			break;
		}
		destination = (struct sockaddr_in *)item->ifa_dstaddr;
		if (memcmp(&destination->sin_addr, &wanted_peer, sizeof(wanted_peer)) != 0) {
			state = ADDRESS_STATE_CONFLICT;
			break;
		}
		state = ADDRESS_STATE_OWNED;
	}
	freeifaddrs(interfaces);
	return state;
}

static int set_address(const char *name, const char *local, const char *peer) {
	struct ifaliasreq request = {0};
	struct sockaddr_in *addr = (struct sockaddr_in *)&request.ifra_addr;
	struct sockaddr_in *broadaddr = (struct sockaddr_in *)&request.ifra_broadaddr;
	struct sockaddr_in *mask = (struct sockaddr_in *)&request.ifra_mask;
	int fd, result;
	if (copy_name(request.ifra_name, sizeof(request.ifra_name), name) != 0) return -1;
	addr->sin_len = sizeof(*addr); addr->sin_family = AF_INET;
	broadaddr->sin_len = sizeof(*broadaddr); broadaddr->sin_family = AF_INET;
	mask->sin_len = sizeof(*mask); mask->sin_family = AF_INET;
	if (inet_pton(AF_INET, local, &addr->sin_addr) != 1 ||
	    inet_pton(AF_INET, peer, &broadaddr->sin_addr) != 1 ||
	    inet_pton(AF_INET, "255.255.255.255", &mask->sin_addr) != 1)
		return -1;
	fd = socket(AF_INET, SOCK_DGRAM, 0);
	if (fd == -1) return -1;
	result = ioctl(fd, SIOCAIFADDR, &request);
	close(fd);
	return result;
}

static int delete_address(const char *name, const char *local) {
	struct ifreq request = {0};
	int fd, result;
	if (copy_name(request.ifr_name, sizeof(request.ifr_name), name) != 0) return -1;
	request.ifr_addr.sa_len = sizeof(struct sockaddr_in);
	request.ifr_addr.sa_family = AF_INET;
	if (inet_pton(AF_INET, local, &((struct sockaddr_in *)&request.ifr_addr)->sin_addr) != 1)
		return -1;
	fd = socket(AF_INET, SOCK_DGRAM, 0);
	if (fd == -1) return -1;
	result = ioctl(fd, SIOCDIFADDR, &request);
	close(fd);
	return result;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

// IPv4Route owns one synthetic address and one /32 route on one utun.
type IPv4Route struct {
	name, local, peer    string
	target               netip.Addr
	iface                *net.Interface
	addressSet, routeSet bool
	initialFlags         int
	upChanged            bool
}

var routeSequence atomic.Uint32

const routeDiagnosticLimit = 6

var (
	addressStateErrorCode = int(C.ADDRESS_STATE_ERROR)
	addressStateAbsent    = int(C.ADDRESS_STATE_ABSENT)
	addressStateOwned     = int(C.ADDRESS_STATE_OWNED)
)

func configureSyntheticIPv4Route(process *tunnelProcess, localText, peerText, targetText string) (*IPv4Route, error) {
	if process == nil || os.Geteuid() != 0 || !utunName.MatchString(process.name) {
		return nil, errors.New("synthetic IPv4 route requires root and a utun")
	}
	local, peer, target, err := ipv4(localText, peerText, targetText)
	if err != nil {
		return nil, err
	}
	iface, err := net.InterfaceByName(process.name)
	if err != nil {
		return nil, err
	}
	configured := &IPv4Route{name: process.name, local: local.String(), peer: peer.String(), target: target, iface: iface}
	if existing, err := configured.request(syscall.RTM_GET); err == nil && existing.Err == nil && configured.isTargetHostRoute(existing) {
		return nil, errors.New("refusing to replace an existing route")
	}
	if err := configured.preflightAddress(); err != nil {
		return nil, err
	}
	flags, err := configured.interfaceFlags()
	if err != nil {
		return nil, err
	}
	configured.initialFlags = flags
	cName, cLocal, cPeer := C.CString(process.name), C.CString(local.String()), C.CString(peer.String())
	defer C.free(unsafe.Pointer(cName))
	defer C.free(unsafe.Pointer(cLocal))
	defer C.free(unsafe.Pointer(cPeer))
	if C.set_address(cName, cLocal, cPeer) != 0 {
		return nil, errors.New("set synthetic IPv4 address")
	}
	configured.addressSet = true
	if flags&int(C.IFF_UP) == 0 {
		configured.upChanged = true
		if err := configured.setInterfaceFlags(flags | int(C.IFF_UP)); err != nil {
			return nil, errors.Join(err, configured.Close())
		}
	}
	if err := configured.add(); err != nil {
		return nil, errors.Join(err, configured.Close())
	}
	if err := configured.verify(); err != nil {
		return nil, errors.Join(err, configured.Close())
	}
	return configured, nil
}

func ipv4(values ...string) (netip.Addr, netip.Addr, netip.Addr, error) {
	var result [3]netip.Addr
	for index, value := range values {
		address, err := netip.ParseAddr(value)
		if err != nil || !address.Is4() || !address.IsGlobalUnicast() {
			return netip.Addr{}, netip.Addr{}, netip.Addr{}, errors.New("synthetic address must be global IPv4")
		}
		result[index] = address
	}
	if result[0] == result[1] || result[0] == result[2] || result[1] == result[2] {
		return netip.Addr{}, netip.Addr{}, netip.Addr{}, errors.New("synthetic addresses must differ")
	}
	return result[0], result[1], result[2], nil
}

func (configured *IPv4Route) add() error { return configured.write(syscall.RTM_ADD) }

func (configured *IPv4Route) Close() error {
	var problems []error
	if configured.routeSet {
		if err := configured.verify(); err != nil {
			problems = append(problems, err)
		} else if err := configured.write(syscall.RTM_DELETE); err != nil {
			problems = append(problems, err)
		}
	}
	if configured.addressSet {
		problems = append(problems, configured.removeAddress())
	}
	if configured.upChanged {
		problems = append(problems, configured.restoreInterfaceFlags())
	}
	return errors.Join(problems...)
}

func (configured *IPv4Route) removeAddress() error {
	if err := configured.requireOwnedAddress(); err != nil {
		return err
	}
	cName, cLocal := C.CString(configured.name), C.CString(configured.local)
	defer C.free(unsafe.Pointer(cName))
	defer C.free(unsafe.Pointer(cLocal))
	if C.delete_address(cName, cLocal) != 0 {
		return errors.New("delete synthetic IPv4 address")
	}
	configured.recordAddressRemoval(nil)
	return nil
}

func (configured *IPv4Route) preflightAddress() error {
	cLocal := C.CString(configured.local)
	defer C.free(unsafe.Pointer(cLocal))
	state := int(C.address_preflight(cLocal))
	return requireAddressState("preflight synthetic IPv4 address", state, addressStateAbsent)
}

func (configured *IPv4Route) requireOwnedAddress() error {
	cName, cLocal, cPeer := C.CString(configured.name), C.CString(configured.local), C.CString(configured.peer)
	defer C.free(unsafe.Pointer(cName))
	defer C.free(unsafe.Pointer(cLocal))
	defer C.free(unsafe.Pointer(cPeer))
	state := int(C.address_ownership(cName, cLocal, cPeer))
	return requireAddressState("verify synthetic IPv4 address ownership", state, addressStateOwned)
}

func requireAddressState(operation string, state, expected int) error {
	if state == expected {
		return nil
	}
	if state == addressStateErrorCode {
		return fmt.Errorf("%s: inspect addresses", operation)
	}
	return fmt.Errorf("%s: unexpected address state %d", operation, state)
}

func (configured *IPv4Route) interfaceFlags() (int, error) {
	cName := C.CString(configured.name)
	defer C.free(unsafe.Pointer(cName))
	var flags C.int
	if C.get_flags(cName, &flags) != 0 {
		return 0, errors.New("read utun flags")
	}
	return int(flags), nil
}

func (configured *IPv4Route) setInterfaceFlags(flags int) error {
	cName := C.CString(configured.name)
	defer C.free(unsafe.Pointer(cName))
	if C.set_flags(cName, C.int(flags)) != 0 {
		return errors.New("set utun flags")
	}
	actual, err := configured.interfaceFlags()
	if err != nil || actual != flags {
		return errors.Join(errors.New("verify utun flags"), err)
	}
	return nil
}

func (configured *IPv4Route) restoreInterfaceFlags() error {
	actual, err := configured.interfaceFlags()
	if err != nil {
		return err
	}
	desired := actual
	if configured.initialFlags&int(C.IFF_UP) == 0 {
		desired &^= int(C.IFF_UP)
	} else {
		desired |= int(C.IFF_UP)
	}
	if desired == actual {
		configured.recordFlagRestore(nil)
		return nil
	}
	if err := configured.setInterfaceFlags(desired); err != nil {
		return err
	}
	configured.recordFlagRestore(nil)
	return nil
}

func (configured *IPv4Route) verify() error {
	message, err := configured.requestOwnedRoute()
	if err != nil {
		return err
	}
	if message.Err != nil {
		return message.Err
	}
	if !configured.matches(message) {
		return errors.New(configured.routeOwnershipDiagnostic(message))
	}
	return nil
}

func (configured *IPv4Route) write(kind int) error {
	message, err := configured.request(kind)
	if err != nil {
		return err
	}
	configured.recordRouteWrite(kind, message.Err)
	return message.Err
}

func (configured *IPv4Route) recordRouteWrite(kind int, err error) {
	if err != nil {
		return
	}
	if kind == syscall.RTM_ADD {
		configured.routeSet = true
	}
	if kind == syscall.RTM_DELETE {
		configured.routeSet = false
	}
}

func (configured *IPv4Route) recordAddressRemoval(err error) {
	if err == nil {
		configured.addressSet = false
	}
}

func (configured *IPv4Route) recordFlagRestore(err error) {
	if err == nil {
		configured.upChanged = false
	}
}

func (configured *IPv4Route) request(kind int) (*route.RouteMessage, error) {
	return configured.requestWithAddrs(kind, []route.Addr{
		&route.Inet4Addr{IP: configured.target.As4()},
		&route.LinkAddr{Index: configured.iface.Index, Name: configured.name},
		&route.Inet4Addr{IP: [4]byte{255, 255, 255, 255}},
	})
}

func (configured *IPv4Route) requestOwnedRoute() (*route.RouteMessage, error) {
	return configured.requestWithAddrs(syscall.RTM_GET, configured.ownedRouteRequestAddrs())
}

func (configured *IPv4Route) ownedRouteRequestAddrs() []route.Addr {
	addrs := make([]route.Addr, syscall.RTAX_IFP+1)
	addrs[syscall.RTAX_DST] = &route.Inet4Addr{IP: configured.target.As4()}
	addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte{255, 255, 255, 255}}
	addrs[syscall.RTAX_IFP] = &route.LinkAddr{Index: configured.iface.Index, Name: configured.name}
	return addrs
}

func (configured *IPv4Route) requestWithAddrs(kind int, addrs []route.Addr) (*route.RouteMessage, error) {
	fd, err := unix.Socket(unix.AF_ROUTE, unix.SOCK_RAW, unix.AF_UNSPEC)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	sequence := int(routeSequence.Add(1))
	message := &route.RouteMessage{Version: syscall.RTM_VERSION, Type: kind, Flags: syscall.RTF_UP | syscall.RTF_HOST | syscall.RTF_STATIC, ID: uintptr(os.Getpid()), Seq: sequence, Addrs: addrs}
	payload, err := message.Marshal()
	if err != nil {
		return nil, err
	}
	if _, err := unix.Write(fd, payload); err != nil {
		return nil, err
	}
	buffer := make([]byte, 4096)
	n, err := unix.Read(fd, buffer)
	if err != nil {
		return nil, err
	}
	messages, err := route.ParseRIB(route.RIBTypeRoute, buffer[:n])
	if err != nil || len(messages) != 1 {
		return nil, fmt.Errorf("invalid route reply: %w", err)
	}
	result, ok := messages[0].(*route.RouteMessage)
	if !ok {
		return nil, errors.New("unexpected route reply type")
	}
	if result.ID != uintptr(os.Getpid()) || result.Seq != sequence || result.Type != kind {
		return nil, fmt.Errorf("unexpected route reply: got type=%d id=%d seq=%d", result.Type, result.ID, result.Seq)
	}
	return result, nil
}

func (configured *IPv4Route) matches(message *route.RouteMessage) bool {
	if !configured.isTargetHostRoute(message) {
		return false
	}
	link, linkOK := routeAddress(message, syscall.RTAX_IFP).(*route.LinkAddr)
	return linkOK && link.Index == configured.iface.Index
}

func (configured *IPv4Route) routeOwnershipDiagnostic(message *route.RouteMessage) string {
	return configured.formatRouteOwnershipDiagnostic(message, configured.coveringRoutes())
}

func (configured *IPv4Route) formatRouteOwnershipDiagnostic(message *route.RouteMessage, covering string) string {
	return fmt.Sprintf("synthetic route ownership changed: expected dst=%s netmask=255.255.255.255 utun=%q ifp_index=%d; actual flags=%#x rtm_index=%d dst=%s netmask=%s gateway=%s ifp=%s; covering=%s", configured.target, configured.name, configured.iface.Index, message.Flags, message.Index, routeAddressValue(message, syscall.RTAX_DST), routeAddressValue(message, syscall.RTAX_NETMASK), routeAddressValue(message, syscall.RTAX_GATEWAY), routeAddressValue(message, syscall.RTAX_IFP), covering)
}

func (configured *IPv4Route) coveringRoutes() string {
	rib, err := route.FetchRIB(syscall.AF_INET, route.RIBTypeRoute, 0)
	if err != nil {
		return "unavailable"
	}
	messages, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return "unparseable"
	}
	var summaries []string
	for _, parsed := range messages {
		message, ok := parsed.(*route.RouteMessage)
		if !ok {
			continue
		}
		prefix, ok := routePrefix(message)
		if !ok || !prefix.Contains(configured.target) {
			continue
		}
		summaries = append(summaries, fmt.Sprintf("%s(flags=%#x,index=%d,ifp=%s)", prefix, message.Flags, message.Index, routeAddressValue(message, syscall.RTAX_IFP)))
		if len(summaries) == routeDiagnosticLimit {
			return strings.Join(summaries, ",") + ",truncated"
		}
	}
	if len(summaries) == 0 {
		return "none"
	}
	return strings.Join(summaries, ",")
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

func routeAddress(message *route.RouteMessage, index int) route.Addr {
	if index >= len(message.Addrs) {
		return nil
	}
	return message.Addrs[index]
}

func routeAddressValue(message *route.RouteMessage, index int) string {
	switch address := routeAddress(message, index).(type) {
	case *route.Inet4Addr:
		return netip.AddrFrom4(address.IP).String()
	case *route.Inet6Addr:
		return netip.AddrFrom16(address.IP).String()
	case *route.LinkAddr:
		return fmt.Sprintf("link(name=%q,index=%d)", address.Name, address.Index)
	case nil:
		return "absent"
	default:
		return fmt.Sprintf("family=%d", address.Family())
	}
}

func (configured *IPv4Route) isTargetHostRoute(message *route.RouteMessage) bool {
	destination, destinationOK := routeAddress(message, syscall.RTAX_DST).(*route.Inet4Addr)
	mask, maskOK := routeAddress(message, syscall.RTAX_NETMASK).(*route.Inet4Addr)
	return destinationOK && maskOK && message.Flags&syscall.RTF_HOST != 0 && destination.IP == configured.target.As4() && mask.IP == [4]byte{255, 255, 255, 255}
}
