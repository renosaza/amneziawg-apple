// SPDX-License-Identifier: MIT

package daemoncontrol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const profileID = "11111111-2222-4333-8444-555555555555"
const syntheticConfig = "private_key=synthetic"

type fakeBackend struct {
	config string
	starts int
	stops  int
	fail   bool
}

func (backend *fakeBackend) Start(config string) (Session, error) {
	backend.config, backend.starts = config, backend.starts+1
	if backend.fail {
		return Session{}, errors.New("synthetic failure")
	}
	return Session{}, nil
}
func (backend *fakeBackend) Stop(Session) error   { backend.stops++; return nil }
func (backend *fakeBackend) Status(Session) error { return nil }

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
	backend := &fakeBackend{}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	start := exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"start","profile_id":"`+profileID+`","config":"`+syntheticConfig+`"}`))
	if !start.OK || start.Profile == nil || start.Profile.Status != "running" {
		t.Fatalf("unexpected start response: %#v", start)
	}
	if backend.config != syntheticConfig || backend.starts != 1 {
		t.Fatalf("backend received unexpected config")
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
	if backend.stops != 1 {
		t.Fatalf("backend was not stopped")
	}
	notFound := exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"status","profile_id":"`+profileID+`"}`))
	if notFound.OK || notFound.Error != "not_found" {
		t.Fatalf("unexpected missing status response: %#v", notFound)
	}
}

func TestRejectsInvalidOrFailedStart(t *testing.T) {
	server, err := NewServerWithBackend(501, &fakeBackend{fail: true})
	if err != nil {
		t.Fatal(err)
	}
	failed := exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"start","profile_id":"`+profileID+`","config":"`+syntheticConfig+`"}`))
	if failed.OK || failed.Error != "start_failed" {
		t.Fatalf("unexpected start failure: %#v", failed)
	}
	invalid := exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"start","profile_id":"`+profileID+`","config":"get=1"}`))
	if invalid.OK || invalid.Error != "invalid_request" {
		t.Fatalf("unexpected invalid config: %#v", invalid)
	}
}

func TestCloseStopsOwnedSessionsAndRejectsNewWork(t *testing.T) {
	backend := &fakeBackend{}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	if response := server.apply(request{Operation: "start", ProfileID: profileID, Config: syntheticConfig}); !response.OK {
		t.Fatalf("start: %#v", response)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if backend.stops != 1 {
		t.Fatalf("stops=%d", backend.stops)
	}
	if response := server.apply(request{Operation: "list"}); response.Error != "shutting_down" {
		t.Fatalf("unexpected close response: %#v", response)
	}
}

func TestConfigAllowsAWGFields(t *testing.T) {
	config := "private_key=synthetic\nJc=4\nJmin=40\nHeaderProtectionKey=synthetic\nRandomTrailers=1"
	if err := validConfig(config); err != nil {
		t.Fatalf("AWG config was rejected: %v", err)
	}
}

func TestRejectsUnauthorizedUIDBeforeRequest(t *testing.T) {
	server, err := NewServer(501)
	if err != nil {
		t.Fatal(err)
	}
	client, daemon := net.Pipe()
	done := make(chan struct{})
	go func() {
		server.serve(daemon, 502)
		_ = daemon.Close()
		close(done)
	}()
	if err := client.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(requestFrame(t, `{"version":1,"operation":"start","profile_id":"`+profileID+`","config":"`+syntheticConfig+`"}`)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("unauthorized connection was not dropped: %v", err)
	}
	_ = client.Close()
	<-done
	list := exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"list"}`))
	if !list.OK || len(list.Profiles) != 0 {
		t.Fatalf("unauthorized request changed state: %#v", list)
	}
}

func TestMaximumListFitsOneFrame(t *testing.T) {
	server, err := NewServer(501)
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= maxProfiles; index++ {
		id := fmt.Sprintf("%08x-2222-4333-8444-555555555555", index)
		if response := server.apply(request{Operation: "start", ProfileID: id}); !response.OK {
			t.Fatalf("failed to start profile %d: %#v", index, response)
		}
	}
	list := server.apply(request{Operation: "list"})
	if len(list.Profiles) != maxProfiles || writeResponse(io.Discard, list) != nil {
		t.Fatalf("maximum list does not fit a frame: %#v", list)
	}
	if response := server.apply(request{Operation: "start", ProfileID: "ffffffff-2222-4333-8444-555555555555"}); response.Error != "capacity" {
		t.Fatalf("unexpected capacity response: %#v", response)
	}
}

func TestRejectsTooLongSocketPathAndSymlinkAncestor(t *testing.T) {
	tooLong := "/" + strings.Repeat("a", maxSocketPath)
	if err := safeSocketPath(tooLong); err == nil {
		t.Fatal("accepted an overlong Darwin socket path")
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := safeSocketPath(filepath.Join(link, "control.sock")); err == nil {
		t.Fatal("accepted a symlink socket ancestor")
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
	directory, err := os.MkdirTemp("/tmp", "awgd-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
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
