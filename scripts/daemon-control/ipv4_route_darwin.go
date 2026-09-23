// SPDX-License-Identifier: MIT

//go:build darwin

package daemoncontrol

/*
#include <arpa/inet.h>
#include <net/if.h>
#include <netinet/in.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/socket.h>
#include <sys/sockio.h>
#include <stdlib.h>
#include <unistd.h>

static int set_address(const char *name, const char *local, const char *peer) {
	struct ifaliasreq request = {0};
	struct sockaddr_in *addr = (struct sockaddr_in *)&request.ifra_addr;
	struct sockaddr_in *broadaddr = (struct sockaddr_in *)&request.ifra_broadaddr;
	struct sockaddr_in *mask = (struct sockaddr_in *)&request.ifra_mask;
	int fd, result;
	strncpy(request.ifra_name, name, sizeof(request.ifra_name) - 1);
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
	if (result == 0) {
		struct ifreq flags = {0};
		strncpy(flags.ifr_name, name, sizeof(flags.ifr_name) - 1);
		result = ioctl(fd, SIOCGIFFLAGS, &flags);
		if (result == 0) {
			flags.ifr_flags |= IFF_UP;
			result = ioctl(fd, SIOCSIFFLAGS, &flags);
		}
	}
	close(fd);
	return result;
}

static int delete_address(const char *name, const char *local) {
	struct ifreq request = {0};
	int fd, result;
	strncpy(request.ifr_name, name, sizeof(request.ifr_name) - 1);
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
	"syscall"
	"unsafe"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

// IPv4Route owns one synthetic address and one /32 route on one utun.
type IPv4Route struct {
	name, local          string
	target               netip.Addr
	iface                *net.Interface
	addressSet, routeSet bool
}

func ConfigureSyntheticIPv4Route(name, localText, peerText, targetText string) (*IPv4Route, error) {
	if os.Geteuid() != 0 || !utunName.MatchString(name) {
		return nil, errors.New("synthetic IPv4 route requires root and a utun")
	}
	local, peer, target, err := ipv4(localText, peerText, targetText)
	if err != nil {
		return nil, err
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}
	configured := &IPv4Route{name: name, local: local.String(), target: target, iface: iface}
	if existing, err := configured.request(syscall.RTM_GET); err == nil && existing.Err == nil {
		return nil, errors.New("refusing to replace an existing route")
	}
	cName, cLocal, cPeer := C.CString(name), C.CString(local.String()), C.CString(peer.String())
	defer C.free(unsafe.Pointer(cName))
	defer C.free(unsafe.Pointer(cLocal))
	defer C.free(unsafe.Pointer(cPeer))
	if C.set_address(cName, cLocal, cPeer) != 0 {
		return nil, errors.New("set synthetic IPv4 address")
	}
	configured.addressSet = true
	if err := configured.add(); err != nil {
		return nil, errors.Join(err, configured.removeAddress())
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
	return errors.Join(problems...)
}

func (configured *IPv4Route) removeAddress() error {
	cName, cLocal := C.CString(configured.name), C.CString(configured.local)
	defer C.free(unsafe.Pointer(cName))
	defer C.free(unsafe.Pointer(cLocal))
	if C.delete_address(cName, cLocal) != 0 {
		return errors.New("delete synthetic IPv4 address")
	}
	configured.addressSet = false
	return nil
}

func (configured *IPv4Route) verify() error {
	message, err := configured.request(syscall.RTM_GET)
	if err != nil {
		return err
	}
	link, ok := message.Addrs[syscall.RTAX_IFP].(*route.LinkAddr)
	if !ok || link.Index != configured.iface.Index || message.Flags&syscall.RTF_HOST == 0 {
		return errors.New("synthetic route ownership changed")
	}
	return nil
}

func (configured *IPv4Route) write(kind int) error {
	message, err := configured.request(kind)
	if err != nil {
		return err
	}
	if message.Err == nil && kind == syscall.RTM_ADD {
		configured.routeSet = true
	}
	return message.Err
}

func (configured *IPv4Route) request(kind int) (*route.RouteMessage, error) {
	fd, err := unix.Socket(unix.AF_ROUTE, unix.SOCK_RAW, unix.AF_UNSPEC)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	message := &route.RouteMessage{Version: syscall.RTM_VERSION, Type: kind, Flags: syscall.RTF_UP | syscall.RTF_HOST | syscall.RTF_STATIC, ID: uintptr(os.Getpid()), Seq: 1, Addrs: []route.Addr{
		&route.Inet4Addr{IP: configured.target.As4()},
		&route.LinkAddr{Index: configured.iface.Index, Name: configured.name},
		&route.Inet4Addr{IP: [4]byte{255, 255, 255, 255}},
	}}
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
	if !ok || result.ID != uintptr(os.Getpid()) {
		return nil, errors.New("unexpected route reply")
	}
	return result, nil
}
