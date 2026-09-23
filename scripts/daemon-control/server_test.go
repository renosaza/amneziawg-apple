// SPDX-License-Identifier: MIT

package daemoncontrol

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const profileID = "11111111-2222-4333-8444-555555555555"

func exchange(t *testing.T, server *Server, uid uint32, frame []byte) response {
	t.Helper()
	client, daemon := net.Pipe()
	done := make(chan struct{})
	go func() {
		server.serve(daemon, uid)
		_ = daemon.Close()
		close(done)
	}()
	if _, err := client.Write(frame); err != nil {
		t.Fatal(err)
	}
	responseFrame, err := readFrame(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	<-done
	var response response
	if err := json.Unmarshal(responseFrame, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func requestFrame(t *testing.T, request string) []byte {
	t.Helper()
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(request)))
	return append(header[:], []byte(request)...)
}

func TestStartStopStatusAndList(t *testing.T) {
	server, err := NewServer(501)
	if err != nil {
		t.Fatal(err)
	}
	start := exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"start","profile_id":"`+profileID+`"}`))
	if !start.OK || start.Profile == nil || start.Profile.Status != "running" {
		t.Fatalf("unexpected start response: %#v", start)
	}
	status := exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"status","profile_id":"`+profileID+`"}`))
	if !status.OK || status.Profile == nil || status.Profile.ID != profileID {
		t.Fatalf("unexpected status response: %#v", status)
	}
	list := exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"list"}`))
	if !list.OK || len(list.Profiles) != 1 || list.Profiles[0].ID != profileID {
		t.Fatalf("unexpected list response: %#v", list)
	}
	stop := exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"stop","profile_id":"`+profileID+`"}`))
	if !stop.OK || stop.Profile == nil || stop.Profile.Status != "stopped" {
		t.Fatalf("unexpected stop response: %#v", stop)
	}
	notFound := exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"status","profile_id":"`+profileID+`"}`))
	if notFound.OK || notFound.Error != "not_found" {
		t.Fatalf("unexpected missing status response: %#v", notFound)
	}
}

func TestRejectsUnauthorizedUIDBeforeRequest(t *testing.T) {
	server, err := NewServer(501)
	if err != nil {
		t.Fatal(err)
	}
	response := exchange(t, server, 502, requestFrame(t, `{"version":1,"operation":"start","profile_id":"`+profileID+`"}`))
	if response.OK || response.Error != "unauthorized" {
		t.Fatalf("unexpected unauthorized response: %#v", response)
	}
	list := exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"list"}`))
	if !list.OK || len(list.Profiles) != 0 {
		t.Fatalf("unauthorized request changed state: %#v", list)
	}
}

func TestRejectsOversizedAndMalformedFrames(t *testing.T) {
	server, err := NewServer(501)
	if err != nil {
		t.Fatal(err)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], maxFrameBytes+1)
	oversized := exchange(t, server, 501, header[:])
	if oversized.OK || oversized.Error != "invalid_request" {
		t.Fatalf("unexpected oversized response: %#v", oversized)
	}
	malformed := exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"list","extra":true}`))
	if malformed.OK || malformed.Error != "invalid_request" {
		t.Fatalf("unexpected malformed response: %#v", malformed)
	}
}

func TestPeerUIDUsesDarwinCredentials(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin-only credential API")
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "control.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		connection, err := listener.AcceptUnix()
		if err != nil {
			done <- err
			return
		}
		defer connection.Close()
		uid, err := peerUID(connection)
		if err == nil && uid != uint32(os.Getuid()) {
			err = io.ErrUnexpectedEOF
		}
		done <- err
	}()
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
