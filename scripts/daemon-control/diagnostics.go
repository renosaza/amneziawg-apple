// SPDX-License-Identifier: MIT

package daemoncontrol

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/netip"
	"regexp"
	"strings"
	"syscall"
)

// Diagnostics is opt-in. It deliberately accepts structured values instead of
// errors or protocol payloads, which may contain private key material.
type Diagnostics interface {
	Event(DiagnosticEvent)
}

// DiagnosticEvent has only allowlisted, non-secret lifecycle fields.
type DiagnosticEvent struct {
	Event       string `json:"event"`
	OperationID string `json:"operation_id,omitempty"`
	Session     string `json:"session,omitempty"`
	Stage       string `json:"stage,omitempty"`
	Class       string `json:"class,omitempty"`
	Code        string `json:"code,omitempty"`
	RouteOwner  string `json:"route_owner,omitempty"`
	Interface   string `json:"interface,omitempty"`
	Prefix      string `json:"prefix,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	Result      string `json:"result,omitempty"`
	Pending     bool   `json:"pending,omitempty"`
}

type diagnostics struct {
	logger           *log.Logger
	includeEndpoints bool
}

var diagnosticToken = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)
var diagnosticInterface = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)
var diagnosticUTUN = regexp.MustCompile(`^utun[0-9]+$`)

// NewDiagnostics writes one JSON object per event. Endpoint addresses need a
// separate explicit opt-in because they are often sensitive operational data.
func NewDiagnostics(writer io.Writer, includeEndpoints bool) Diagnostics {
	if writer == nil {
		return nil
	}
	return &diagnostics{logger: log.New(writer, "amneziawg-daemon diagnostic ", 0), includeEndpoints: includeEndpoints}
}

func (logger *diagnostics) Event(event DiagnosticEvent) {
	if logger == nil || logger.logger == nil {
		return
	}
	event.Event = safeDiagnosticToken(event.Event)
	event.OperationID = safeDiagnosticToken(event.OperationID)
	event.Session = safeDiagnosticSession(event.Session)
	event.Stage = safeDiagnosticToken(event.Stage)
	event.Class = safeDiagnosticToken(event.Class)
	event.RouteOwner = safeDiagnosticToken(event.RouteOwner)
	event.Interface = safeDiagnosticInterface(event.Interface)
	event.Prefix = safeDiagnosticPrefix(event.Prefix)
	event.Result = safeDiagnosticToken(event.Result)
	if logger.includeEndpoints {
		event.Endpoint = safeDiagnosticEndpoint(event.Endpoint)
	} else {
		event.Endpoint = ""
	}
	encoded, err := json.Marshal(event)
	if err == nil {
		logger.logger.Print(string(encoded))
	}
}

// diagnosticErrorCode intentionally maps errors to a small fixed vocabulary.
// Never log Error(), because UAPI and process errors can contain profile data.
func diagnosticErrorCode(err error) string {
	switch {
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return "permission_denied"
	case errors.Is(err, syscall.ENOENT):
		return "not_found"
	case errors.Is(err, syscall.EEXIST):
		return "already_exists"
	case errors.Is(err, syscall.ETIMEDOUT):
		return "timeout"
	case err != nil:
		return "operation_failed"
	default:
		return ""
	}
}

func safeDiagnosticToken(value string) string {
	if diagnosticToken.MatchString(value) {
		return value
	}
	return ""
}

func safeDiagnosticSession(value string) string {
	if validUUID(strings.ToLower(value)) {
		return strings.ToLower(value)
	}
	if diagnosticUTUN.MatchString(value) {
		return value
	}
	return ""
}

func safeDiagnosticInterface(value string) string {
	if diagnosticInterface.MatchString(value) {
		return value
	}
	return ""
}

func safeDiagnosticPrefix(value string) string {
	prefix, err := netip.ParsePrefix(value)
	if err == nil && prefix == prefix.Masked() && prefix.String() == value {
		return value
	}
	return ""
}

func safeDiagnosticEndpoint(value string) string {
	address, err := netip.ParseAddr(value)
	if err == nil && address.String() == value {
		return value
	}
	return ""
}

type diagnosticBackend interface {
	SetDiagnostics(Diagnostics)
}
