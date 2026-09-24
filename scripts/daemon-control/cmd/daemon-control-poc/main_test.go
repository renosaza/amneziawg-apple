// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
)

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
	requests := make(chan map[string]any, 4)
	go func() {
		for _, response := range []string{
			`{"ok":true,"protocol_version":1}`,
			`{"ok":true,"profile":{"id":"11111111-2222-4333-8444-555555555555","status":"running"}}`,
			`{"ok":true,"protocol_version":1}`,
			`{"ok":true,"profile":{"id":"11111111-2222-4333-8444-555555555555","status":"stopped"}}`,
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
}
