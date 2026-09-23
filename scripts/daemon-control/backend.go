// SPDX-License-Identifier: MIT

package daemoncontrol

import "errors"

// Session is opaque to the control protocol and never serializes configuration.
type Session struct{ value any }

// Backend exists only to keep lifecycle logic unit-testable without a root VPN.
type Backend interface {
	Start(config string) (Session, error)
	Stop(Session) error
	Status(Session) error
}

type memoryBackend struct{}

func (memoryBackend) Start(string) (Session, error) { return Session{}, nil }
func (memoryBackend) Stop(Session) error            { return nil }
func (memoryBackend) Status(Session) error          { return nil }

func NewTunnelBackend(binary string) (Backend, error) {
	return newTunnelBackend(binary)
}

var errBackendUnavailable = errors.New("real tunnel backend is unavailable on this platform")
