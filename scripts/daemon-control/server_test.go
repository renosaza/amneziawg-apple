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
	"sync"
	"testing"
	"time"
)

const profileID = "11111111-2222-4333-8444-555555555555"
const profileIDTwo = "22222222-2222-4333-8444-555555555555"
const profileIDThree = "33333333-2222-4333-8444-555555555555"
const syntheticConfig = "private_key=synthetic"

type fakeBackend struct {
	mu                     sync.Mutex
	config                 string
	starts                 int
	stops                  int
	fail                   bool
	failStarts             int
	status                 error
	stopFailures           int
	returnSessionOnFailure bool
}

func (backend *fakeBackend) Start(config string) (Session, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.config, backend.starts = config, backend.starts+1
	if backend.fail {
		if backend.returnSessionOnFailure {
			return Session{value: backend.starts}, errors.New("synthetic failure")
		}
		return Session{}, errors.New("synthetic failure")
	}
	if backend.failStarts > 0 {
		backend.failStarts--
		return Session{}, errors.New("synthetic failure")
	}
	return Session{value: backend.starts}, nil
}

func TestFailedStartRetainsSessionForCleanupRetry(t *testing.T) {
	backend := &fakeBackend{fail: true, returnSessionOnFailure: true}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	if response := server.apply(request{Operation: "start", ProfileID: profileID, Config: syntheticConfig}); response.Error != "start_failed" {
		t.Fatalf("start: %#v", response)
	}
	if response := server.apply(request{Operation: "start", ProfileID: profileID, Config: syntheticConfig}); response.Error != "already_running" {
		t.Fatalf("retry discarded failed session: %#v", response)
	}
	backend.fail = false
	if response := server.apply(request{Operation: "stop", ProfileID: profileID}); !response.OK {
		t.Fatalf("cleanup retry: %#v", response)
	}
}
func (backend *fakeBackend) Stop(Session) error {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.stops++
	if backend.stopFailures > 0 {
		backend.stopFailures--
		return errors.New("synthetic stop failure")
	}
	return nil
}
func (backend *fakeBackend) Status(Session) error {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.status
}

func (backend *fakeBackend) counts() (starts, stops int) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.starts, backend.stops
}

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
	starts, _ := backend.counts()
	if backend.config != syntheticConfig || starts != 1 {
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
	_, stops := backend.counts()
	if stops != 1 {
		t.Fatalf("backend was not stopped")
	}
	notFound := exchange(t, server, 501, requestFrame(t, `{"version":1,"operation":"status","profile_id":"`+profileID+`"}`))
	if notFound.OK || notFound.Error != "not_found" {
		t.Fatalf("unexpected missing status response: %#v", notFound)
	}
}

func TestQuiesceRejectsActiveSessionsAndBlocksNewOnSuccess(t *testing.T) {
	server, err := NewServerWithBackend(501, &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	if response := server.apply(request{Operation: "start", ProfileID: profileID, Config: syntheticConfig}); !response.OK {
		t.Fatalf("start: %#v", response)
	}
	if response := server.apply(request{Operation: "quiesce"}); response.Error != "active_sessions" {
		t.Fatalf("active quiesce: %#v", response)
	}
	if response := server.apply(request{Operation: "stop", ProfileID: profileID}); !response.OK {
		t.Fatalf("stop: %#v", response)
	}
	if response := server.apply(request{Operation: "quiesce"}); !response.OK {
		t.Fatalf("idle quiesce: %#v", response)
	}
	if response := server.apply(request{Operation: "start", ProfileID: profileID, Config: syntheticConfig}); response.Error != "shutting_down" {
		t.Fatalf("start after quiesce: %#v", response)
	}
}

func TestThreeProfilesRemainIndependent(t *testing.T) {
	backend := &fakeBackend{}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{profileID, profileIDTwo, profileIDThree} {
		if response := server.apply(request{Operation: "start", ProfileID: id, Config: syntheticConfig}); !response.OK {
			t.Fatalf("start %s: %#v", id, response)
		}
	}
	if response := server.apply(request{Operation: "start", ProfileID: profileIDTwo, Config: syntheticConfig}); response.Error != "already_running" {
		t.Fatalf("duplicate start: %#v", response)
	}
	if response := server.apply(request{Operation: "start", ProfileID: "44444444-2222-4333-8444-555555555555", Config: syntheticConfig}); response.Error != "capacity" {
		t.Fatalf("capacity: %#v", response)
	}
	if starts, _ := backend.counts(); starts != 3 {
		t.Fatalf("starts=%d", starts)
	}
	if response := server.apply(request{Operation: "stop", ProfileID: profileIDTwo}); !response.OK {
		t.Fatalf("stop middle profile: %#v", response)
	}
	for _, id := range []string{profileID, profileIDThree} {
		if response := server.apply(request{Operation: "status", ProfileID: id}); !response.OK {
			t.Fatalf("remaining profile %s: %#v", id, response)
		}
	}
	list := server.apply(request{Operation: "list"})
	if !list.OK || len(list.Profiles) != 2 || list.Profiles[0].ID != profileID || list.Profiles[1].ID != profileIDThree {
		t.Fatalf("remaining profiles: %#v", list)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	_, stops := backend.counts()
	if stops != 3 {
		t.Fatalf("stops=%d", stops)
	}
}

func TestFailedStartDoesNotClaimProfile(t *testing.T) {
	backend := &fakeBackend{failStarts: 1}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	if response := server.apply(request{Operation: "start", ProfileID: profileID, Config: syntheticConfig}); response.Error != "start_failed" {
		t.Fatalf("failed start: %#v", response)
	}
	if response := server.apply(request{Operation: "list"}); !response.OK || len(response.Profiles) != 0 {
		t.Fatalf("failed start claimed a profile: %#v", response)
	}
	if response := server.apply(request{Operation: "start", ProfileID: profileID, Config: syntheticConfig}); !response.OK {
		t.Fatalf("retry after failed start: %#v", response)
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
	_, stops := backend.counts()
	if stops != 1 {
		t.Fatalf("stops=%d", stops)
	}
	if response := server.apply(request{Operation: "list"}); response.Error != "shutting_down" {
		t.Fatalf("unexpected close response: %#v", response)
	}
}

func TestStatusFailureRetainsOwnedSessionForClose(t *testing.T) {
	backend := &fakeBackend{status: errors.New("synthetic UAPI failure")}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	if response := server.apply(request{Operation: "start", ProfileID: profileID, Config: syntheticConfig}); !response.OK {
		t.Fatalf("start: %#v", response)
	}
	if response := server.apply(request{Operation: "status", ProfileID: profileID}); response.Error != "status_failed" {
		t.Fatalf("status: %#v", response)
	}
	list := server.apply(request{Operation: "list"})
	if !list.OK || len(list.Profiles) != 1 || list.Profiles[0].Status != "degraded" {
		t.Fatalf("list: %#v", list)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	_, stops := backend.counts()
	if stops != 1 {
		t.Fatalf("stops=%d", stops)
	}
}

func TestConcurrentCloseAndOperation(t *testing.T) {
	backend := &fakeBackend{}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	if response := server.apply(request{Operation: "start", ProfileID: profileID, Config: syntheticConfig}); !response.OK {
		t.Fatalf("start: %#v", response)
	}
	done := make(chan struct{})
	go func() { _ = server.Close(); close(done) }()
	_ = server.apply(request{Operation: "status", ProfileID: profileID})
	<-done
	_, stops := backend.counts()
	if stops != 1 {
		t.Fatalf("stops=%d", stops)
	}
}

func TestCloseRetainsFailedStopForRetry(t *testing.T) {
	backend := &fakeBackend{stopFailures: 1}
	server, err := NewServerWithBackend(501, backend)
	if err != nil {
		t.Fatal(err)
	}
	if response := server.apply(request{Operation: "start", ProfileID: profileID, Config: syntheticConfig}); !response.OK {
		t.Fatalf("start: %#v", response)
	}
	if err := server.Close(); err == nil {
		t.Fatal("first close unexpectedly succeeded")
	}
	if response := server.apply(request{Operation: "list"}); response.Error != "shutting_down" {
		t.Fatalf("new work accepted: %#v", response)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	_, stops := backend.counts()
	if stops != 2 {
		t.Fatalf("stops=%d", stops)
	}
}

func TestConfigAllowsAWGFields(t *testing.T) {
	config := "private_key=synthetic\nJc=4\nJmin=40\nHeaderProtectionKey=synthetic\nRandomTrailers=1"
	if err := validConfig(config); err != nil {
		t.Fatalf("AWG config was rejected: %v", err)
	}
}

func TestConfigAllowsOneTrailingNewline(t *testing.T) {
	if err := validConfig("private_key=synthetic\n"); err != nil {
		t.Fatalf("single trailing newline was rejected: %v", err)
	}
	for _, config := range []string{
		"private_key=synthetic\n\n",
		"private_key=synthetic\n\n\n",
		"private_key=synthetic\n\npublic_key=synthetic",
	} {
		if err := validConfig(config); err == nil {
			t.Fatalf("blank config line was accepted: %q", config)
		}
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

func staleSocketPath(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "dcp-stale.")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "control.sock")
}

func TestRecoverStaleSocket(t *testing.T) {
	path := staleSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := recoverStaleSocket(path, uint32(os.Geteuid())); err != nil {
		t.Fatalf("recover stale socket: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket remained: %v", err)
	}
}

func TestRecoverStaleSocketRefusesLiveOrUnexpectedSocket(t *testing.T) {
	path := staleSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	uid := uint32(os.Geteuid())
	if err := recoverStaleSocket(path, uid); err == nil {
		t.Fatal("recovered a live socket")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	// Closing with unlink enabled removed the live socket; recreate a stale one.
	listener, err = net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := recoverStaleSocket(path, uid); err == nil {
		t.Fatal("recovered a wrong-mode socket")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := recoverStaleSocket(path, uid+1); err == nil {
		t.Fatal("recovered a wrong-owner socket")
	}
}
