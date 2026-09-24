// SPDX-License-Identifier: MIT

package daemoncontrol

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type diagnosticFailureBackend struct{}

func (diagnosticFailureBackend) Start(string) (Session, error) {
	return Session{}, errors.New("PRIVATE_KEY_MARKER raw backend failure")
}
func (diagnosticFailureBackend) Stop(Session) error   { return nil }
func (diagnosticFailureBackend) Status(Session) error { return nil }

func TestDiagnosticsSanitizeSecretsAndIdentifyFailureStage(t *testing.T) {
	var output bytes.Buffer
	server, err := NewServerWithBackend(501, diagnosticFailureBackend{})
	if err != nil {
		t.Fatal(err)
	}
	server.SetDiagnostics(NewDiagnostics(&output, false))
	response := server.apply(request{
		Operation: "start",
		ProfileID: profileID,
		Config:    "private_key=CONFIG_SECRET_MARKER",
	})
	if response.Error != "start_failed" {
		t.Fatalf("response = %#v", response)
	}
	logged := output.String()
	if strings.Contains(logged, "PRIVATE_KEY_MARKER") || strings.Contains(logged, "CONFIG_SECRET_MARKER") {
		t.Fatalf("diagnostics leaked secret material: %q", logged)
	}
	if !strings.Contains(logged, `"operation_id":"start-1"`) || !strings.Contains(logged, `"stage":"backend"`) || !strings.Contains(logged, `"class":"start_failed"`) || !strings.Contains(logged, `"code":"operation_failed"`) {
		t.Fatalf("diagnostics did not identify failure stage: %q", logged)
	}
}

func TestDiagnosticsRequireEndpointOptInAndRejectUnexpectedFields(t *testing.T) {
	var output bytes.Buffer
	logger := NewDiagnostics(&output, false)
	logger.Event(DiagnosticEvent{
		Event: "route", Session: profileID, Stage: "physical_endpoint", RouteOwner: "physical_endpoint",
		Interface: "en0", Prefix: "10.25.0.0/24", Endpoint: "203.0.113.10", Result: "selected",
		Class: "PRIVATE_KEY_MARKER",
	})
	logged := output.String()
	if strings.Contains(logged, "203.0.113.10") || strings.Contains(logged, "PRIVATE_KEY_MARKER") {
		t.Fatalf("diagnostics emitted unapproved field: %q", logged)
	}
	if !strings.Contains(logged, `"prefix":"10.25.0.0/24"`) || !strings.Contains(logged, `"interface":"en0"`) {
		t.Fatalf("diagnostics omitted route selection: %q", logged)
	}
}
