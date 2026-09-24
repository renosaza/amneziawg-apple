// SPDX-License-Identifier: MIT

package daemoncontrol

import (
	"bytes"
	"encoding/json"
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

type operationRecordingBackend struct {
	fakeBackend
	operationID string
}

func (backend *operationRecordingBackend) SetDiagnosticOperationID(operationID string) {
	backend.operationID = operationID
}

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

func TestDiagnosticsIdentifyEarlyRouteConflict(t *testing.T) {
	var output bytes.Buffer
	server, err := NewServerWithBackend(501, &plannedFakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	server.SetDiagnostics(NewDiagnostics(&output, false))
	config := "private_key=synthetic\npublic_key=peer\nallowed_ip=10.25.0.0/24\nendpoint=192.0.2.10:51820"
	plan := json.RawMessage(`{"local_address":"10.25.0.2/32","routes":[{"destination":"10.25.0.0/24","owner":"tunnel"},{"destination":"192.0.2.10/32","owner":"physicalEndpoint"}]}`)
	if response := server.apply(request{Operation: "start", ProfileID: profileID, Config: config, RoutePlan: plan}); !response.OK {
		t.Fatalf("first start = %#v", response)
	}
	if response := server.apply(request{Operation: "start", ProfileID: profileIDTwo, Config: config, RoutePlan: plan}); response.Error != "planned_local_address_conflict" {
		t.Fatalf("conflict = %#v", response)
	}
	logged := output.String()
	if !strings.Contains(logged, `"operation_id":"start-2"`) || !strings.Contains(logged, `"stage":"reservation"`) || !strings.Contains(logged, `"class":"route_conflict"`) || !strings.Contains(logged, `"code":"planned_local_address_conflict"`) {
		t.Fatalf("diagnostics omitted early conflict: %q", logged)
	}
}

func TestServerPropagatesOperationIDToBackend(t *testing.T) {
	backend := &operationRecordingBackend{}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	if response := server.apply(request{Operation: "start", ProfileID: profileID, Config: syntheticConfig}); !response.OK {
		t.Fatalf("start = %#v", response)
	}
	if backend.operationID != "start-1" {
		t.Fatalf("backend start operation ID = %q", backend.operationID)
	}
	if response := server.apply(request{Operation: "stop", ProfileID: profileID}); !response.OK {
		t.Fatalf("stop = %#v", response)
	}
	if backend.operationID != "stop-2" {
		t.Fatalf("backend stop operation ID = %q", backend.operationID)
	}
}
