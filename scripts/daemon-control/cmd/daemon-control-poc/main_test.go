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
