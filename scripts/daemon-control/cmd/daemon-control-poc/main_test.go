// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func TestBoundedDiagnosticLogRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diagnostics.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(bytes.Repeat([]byte("x"), int(diagnosticLogLimit))); err != nil {
		t.Fatal(err)
	}
	logger := &boundedDiagnosticLog{file: file, size: diagnosticLogLimit}
	if _, err := logger.Write([]byte("event\n")); err != nil {
		t.Fatal(err)
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(path + ".1")
	if err != nil || len(previous) != int(diagnosticLogLimit) {
		t.Fatalf("previous diagnostic log = %d bytes, %v", len(previous), err)
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != "event\n" {
		t.Fatalf("current diagnostic log = %q, %v", current, err)
	}
}

func shortSocketPath(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "dcp.")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "control.sock")
}

func TestDaemonIsIdle(t *testing.T) {
	path := shortSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer connection.Close()
		if _, err := readFrame(connection); err != nil {
			return
		}
		frame, _ := json.Marshal(listResponse{OK: true})
		_ = writeFrame(connection, frame)
	}()
	if err := daemonIsIdle(path); err != nil {
		t.Fatalf("idle daemon rejected: %v", err)
	}
}

func TestDaemonIsIdleRejectsActiveSessions(t *testing.T) {
	path := shortSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer connection.Close()
		if _, err := readFrame(connection); err != nil {
			return
		}
		frame, _ := json.Marshal(listResponse{OK: true, Profiles: []struct {
			ID string `json:"id"`
		}{{ID: "synthetic"}}})
		_ = writeFrame(connection, frame)
	}()
	if err := daemonIsIdle(path); err == nil {
		t.Fatal("active daemon accepted as idle")
	}
}

func TestManualRoutePlanRequestsAreFixedAndFramed(t *testing.T) {
	path := shortSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	requests := make(chan map[string]any, 12)
	go func() {
		for _, response := range []string{
			`{"ok":true,"protocol_version":1}`,
			`{"ok":true,"profile":{"id":"11111111-2222-4333-8444-555555555555","status":"running"}}`,
			`{"ok":true,"protocol_version":1}`,
			`{"ok":true,"profile":{"id":"11111111-2222-4333-8444-555555555555","status":"stopped"}}`,
			`{"ok":true,"protocol_version":1}`,
			`{"ok":true,"profile":{"id":"22222222-3333-4444-8555-666666666666","status":"running"}}`,
			`{"ok":true,"protocol_version":1}`,
			`{"ok":true,"profile":{"id":"22222222-3333-4444-8555-666666666666","status":"stopped"}}`,
			`{"ok":true,"protocol_version":1}`,
			`{"ok":true,"profile":{"id":"33333333-4444-4555-8666-777777777777","status":"running"}}`,
			`{"ok":true,"protocol_version":1}`,
			`{"ok":true,"profile":{"id":"33333333-4444-4555-8666-777777777777","status":"stopped"}}`,
		} {
			connection, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			frame, err := readFrame(connection)
			if err == nil {
				var request map[string]any
				if json.Unmarshal(frame, &request) == nil {
					requests <- request
				}
			}
			_ = writeFrame(connection, []byte(response))
			_ = connection.Close()
		}
	}()
	if err := daemonManualRoutePlanStart(path); err != nil {
		t.Fatal(err)
	}
	if err := daemonManualRoutePlanStop(path); err != nil {
		t.Fatal(err)
	}
	if err := daemonManualRoutePlanStartTwo(path); err != nil {
		t.Fatal(err)
	}
	if err := daemonManualRoutePlanStopTwo(path); err != nil {
		t.Fatal(err)
	}
	if err := daemonManualRoutePlanStartThree(path); err != nil {
		t.Fatal(err)
	}
	if err := daemonManualRoutePlanStopThree(path); err != nil {
		t.Fatal(err)
	}
	helloStart, start, helloStop, stop := <-requests, <-requests, <-requests, <-requests
	if helloStart["operation"] != "hello" || start["operation"] != "start" || start["profile_id"] != manualRoutePlanProfileID {
		t.Fatalf("unexpected start requests: %#v %#v", helloStart, start)
	}
	if helloStop["operation"] != "hello" {
		t.Fatalf("unexpected stop hello request: %#v", helloStop)
	}
	if start["config"] != manualRoutePlanRequest.Config {
		t.Fatalf("unexpected synthetic configuration: %#v", start["config"])
	}
	if stop["operation"] != "stop" || stop["profile_id"] != manualRoutePlanProfileID {
		t.Fatalf("unexpected stop request: %#v", stop)
	}
	plan, ok := start["route_plan"].(map[string]any)
	if !ok || plan["local_address"] != "192.0.2.2/32" {
		t.Fatalf("unexpected route plan: %#v", start["route_plan"])
	}
	routes, ok := plan["routes"].([]any)
	if !ok || len(routes) != 2 {
		t.Fatalf("unexpected routes: %#v", plan["routes"])
	}
	helloStartTwo, startTwo, helloStopTwo, stopTwo := <-requests, <-requests, <-requests, <-requests
	if helloStartTwo["operation"] != "hello" || startTwo["operation"] != "start" || startTwo["profile_id"] != manualRoutePlanProfileIDTwo {
		t.Fatalf("unexpected second start requests: %#v %#v", helloStartTwo, startTwo)
	}
	if startTwo["config"] != manualRoutePlanRequestTwo.Config {
		t.Fatalf("unexpected second synthetic configuration: %#v", startTwo["config"])
	}
	if helloStopTwo["operation"] != "hello" || stopTwo["operation"] != "stop" || stopTwo["profile_id"] != manualRoutePlanProfileIDTwo {
		t.Fatalf("unexpected second stop requests: %#v %#v", helloStopTwo, stopTwo)
	}
	helloStartThree, startThree, helloStopThree, stopThree := <-requests, <-requests, <-requests, <-requests
	if helloStartThree["operation"] != "hello" || startThree["operation"] != "start" || startThree["profile_id"] != manualRoutePlanProfileIDThree {
		t.Fatalf("unexpected third start requests: %#v %#v", helloStartThree, startThree)
	}
	if startThree["config"] != manualRoutePlanRequestThree.Config {
		t.Fatalf("unexpected third synthetic configuration: %#v", startThree["config"])
	}
	if helloStopThree["operation"] != "hello" || stopThree["operation"] != "stop" || stopThree["profile_id"] != manualRoutePlanProfileIDThree {
		t.Fatalf("unexpected third stop requests: %#v %#v", helloStopThree, stopThree)
	}
}

func TestManualRoutePlanAssertProfiles(t *testing.T) {
	path := shortSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for _, response := range []string{
			`{"ok":true,"protocol_version":1}`,
			`{"ok":true,"profiles":[{"id":"11111111-2222-4333-8444-555555555555"},{"id":"33333333-4444-4555-8666-777777777777"}]}`,
		} {
			connection, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			defer connection.Close()
			if _, err := readFrame(connection); err == nil {
				_ = writeFrame(connection, []byte(response))
			}
		}
	}()
	if err := daemonManualRoutePlanAssertProfiles(path, manualRoutePlanProfileIDThree, manualRoutePlanProfileID); err != nil {
		t.Fatal(err)
	}
}

func TestManualFullRoutePlanIsFixedAndExcludesSyntheticRoutes(t *testing.T) {
	var plan struct {
		LocalAddress string `json:"local_address"`
		Routes       []struct {
			Destination string `json:"destination"`
			Owner       string `json:"owner"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(manualFullRoutePlanRequest.RoutePlan, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.LocalAddress != "192.0.2.6/32" || len(plan.Routes) == 0 {
		t.Fatalf("unexpected fixed full plan: %#v", plan)
	}
	if frame, err := json.Marshal(manualFullRoutePlanRequest); err != nil || len(frame) > maxFrameBytes {
		t.Fatalf("full-route frame is not bounded: len=%d err=%v", len(frame), err)
	}
	for _, address := range []string{"203.0.113.1", "203.0.113.10", "192.0.2.200", "198.51.100.20", "169.254.1.1"} {
		parsed := netip.MustParseAddr(address)
		contained := false
		for _, route := range plan.Routes {
			if route.Owner != "tunnel" {
				continue
			}
			if netip.MustParsePrefix(route.Destination).Contains(parsed) {
				contained = true
			}
		}
		switch address {
		case "203.0.113.1":
			if !contained {
				t.Fatalf("full route lost %s", address)
			}
		default:
			if contained {
				t.Fatalf("full route captured excluded %s", address)
			}
		}
	}
}

func TestManualFullRoutePlanStartAndStopAreFixed(t *testing.T) {
	path := shortSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	requests := make(chan map[string]any, 4)
	go func() {
		for _, response := range []string{
			`{"ok":true,"protocol_version":1}`,
			`{"ok":true,"profile":{"id":"44444444-5555-4666-8777-888888888888","status":"running"}}`,
			`{"ok":true,"protocol_version":1}`,
			`{"ok":true,"profile":{"id":"44444444-5555-4666-8777-888888888888","status":"stopped"}}`,
		} {
			connection, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			frame, err := readFrame(connection)
			if err == nil {
				var request map[string]any
				if json.Unmarshal(frame, &request) == nil {
					requests <- request
				}
			}
			_ = writeFrame(connection, []byte(response))
			_ = connection.Close()
		}
	}()
	if err := daemonManualRoutePlanStartRequest(path, manualFullRoutePlanRequest); err != nil {
		t.Fatal(err)
	}
	if err := daemonManualRoutePlanStopRequest(path, manualFullRoutePlanProfileID); err != nil {
		t.Fatal(err)
	}
	helloStart, start, helloStop, stop := <-requests, <-requests, <-requests, <-requests
	if helloStart["operation"] != "hello" || start["operation"] != "start" || start["profile_id"] != manualFullRoutePlanProfileID {
		t.Fatalf("unexpected full-route start: %#v %#v", helloStart, start)
	}
	if helloStop["operation"] != "hello" || stop["operation"] != "stop" || stop["profile_id"] != manualFullRoutePlanProfileID {
		t.Fatalf("unexpected full-route stop: %#v %#v", helloStop, stop)
	}
}

func TestDaemonExchangeReportsOnlyKnownNonSecretErrorCodes(t *testing.T) {
	for _, test := range []struct {
		name, response, want string
	}{
		{"start", `{"ok":false,"error":"start_failed"}`, "daemon control rejected start_failed"},
		{"unknown", `{"ok":false,"error":"private_key=should-not-echo"}`, "daemon control rejected control operation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := shortSocketPath(t)
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			go func() {
				connection, err := listener.AcceptUnix()
				if err != nil {
					return
				}
				defer connection.Close()
				_, _ = readFrame(connection)
				_ = writeFrame(connection, []byte(test.response))
			}()
			if _, err := daemonRequest(path, "list"); err == nil || err.Error() != test.want {
				t.Fatalf("error=%v want %q", err, test.want)
			}
		})
	}
}
