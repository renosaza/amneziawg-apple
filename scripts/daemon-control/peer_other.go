// SPDX-License-Identifier: MIT

//go:build !darwin

package daemoncontrol

import (
	"errors"
	"net"
)

func peerUID(_ *net.UnixConn) (uint32, error) {
	return 0, errors.New("daemon control requires macOS peer credentials")
}
