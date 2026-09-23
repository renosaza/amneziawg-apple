// SPDX-License-Identifier: MIT

//go:build darwin

package daemoncontrol

import (
	"net"

	"golang.org/x/sys/unix"
)

func peerUID(connection *net.UnixConn) (uint32, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return 0, err
	}
	var uid uint32
	var peerError error
	if err := raw.Control(func(fd uintptr) {
		credential, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			peerError = err
			return
		}
		uid = credential.Uid
	}); err != nil {
		return 0, err
	}
	return uid, peerError
}
