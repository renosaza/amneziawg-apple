// SPDX-License-Identifier: MIT

// Package daemoncontrol is a deliberately small, synthetic control-plane POC.
// It keeps no profile data: the fake backend only owns in-memory running IDs.
package daemoncontrol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxFrameBytes  = 4 * 1024
	maxProfiles    = 1
	maxConnections = 16
	maxSocketPath  = 103 // Darwin sun_path has room for a trailing NUL.
	maxConfigBytes = 2 * 1024
)

var umaskMu sync.Mutex

type request struct {
	Version   int    `json:"version"`
	Operation string `json:"operation"`
	ProfileID string `json:"profile_id,omitempty"`
	Config    string `json:"config,omitempty"`
}

type Profile struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type response struct {
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Profile  *Profile  `json:"profile,omitempty"`
	Profiles []Profile `json:"profiles,omitempty"`
}

// Server owns one user's ephemeral fake backend state.
type Server struct {
	allowedUID  uint32
	mu          sync.Mutex
	profiles    map[string]Session
	backend     Backend
	connections chan struct{}
	closed      bool
	closeOnce   sync.Once
	closeError  error
}

func NewServer(allowedUID uint32) (*Server, error) {
	return NewServerWithBackend(allowedUID, memoryBackend{})
}

func NewServerWithBackend(allowedUID uint32, backend Backend) (*Server, error) {
	if allowedUID == 0 {
		return nil, errors.New("allowed UID must be a non-root user")
	}
	if backend == nil {
		return nil, errors.New("backend is required")
	}
	return &Server{
		allowedUID:  allowedUID,
		profiles:    make(map[string]Session),
		backend:     backend,
		connections: make(chan struct{}, maxConnections),
	}, nil
}

func (server *Server) handleConnection(connection *net.UnixConn) {
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return
	}
	uid, err := peerUID(connection)
	if err != nil {
		return
	}
	server.serve(connection, uid)
}

func (server *Server) serve(connection io.ReadWriter, uid uint32) {
	if uid != server.allowedUID {
		return
	}
	frame, err := readFrame(connection)
	if err != nil {
		_ = writeResponse(connection, response{Error: "invalid_request"})
		return
	}
	request, err := decodeRequest(frame)
	if err != nil {
		_ = writeResponse(connection, response{Error: "invalid_request"})
		return
	}
	_ = writeResponse(connection, server.apply(request))
}

func (server *Server) apply(request request) response {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.closed {
		return response{Error: "shutting_down"}
	}

	switch request.Operation {
	case "list":
		profiles := make([]Profile, 0, len(server.profiles))
		for id, session := range server.profiles {
			if server.backend.Status(session) != nil {
				profiles = append(profiles, Profile{ID: id, Status: "degraded"})
				continue
			}
			profiles = append(profiles, Profile{ID: id, Status: "running"})
		}
		sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
		return response{OK: true, Profiles: profiles}
	case "start":
		if _, found := server.profiles[request.ProfileID]; found {
			return response{Error: "already_running"}
		}
		if len(server.profiles) == maxProfiles {
			return response{Error: "capacity"}
		}
		session, err := server.backend.Start(request.Config)
		if err != nil {
			return response{Error: "start_failed"}
		}
		server.profiles[request.ProfileID] = session
		return response{OK: true, Profile: &Profile{ID: request.ProfileID, Status: "running"}}
	case "stop":
		session, found := server.profiles[request.ProfileID]
		if !found {
			return response{Error: "not_found"}
		}
		if err := server.backend.Stop(session); err != nil {
			return response{Error: "stop_failed"}
		}
		delete(server.profiles, request.ProfileID)
		return response{OK: true, Profile: &Profile{ID: request.ProfileID, Status: "stopped"}}
	case "status":
		session, found := server.profiles[request.ProfileID]
		if !found {
			return response{Error: "not_found"}
		}
		if err := server.backend.Status(session); err != nil {
			return response{Error: "status_failed"}
		}
		return response{OK: true, Profile: &Profile{ID: request.ProfileID, Status: "running"}}
	default:
		return response{Error: "invalid_request"}
	}
}

// Close prevents new operations and stops every child owned by this server.
func (server *Server) Close() error {
	server.closeOnce.Do(func() {
		server.mu.Lock()
		server.closed = true
		sessions := server.profiles
		server.profiles = make(map[string]Session)
		server.mu.Unlock()
		var problems []error
		for _, session := range sessions {
			if err := server.backend.Stop(session); err != nil {
				problems = append(problems, err)
			}
		}
		server.closeError = errors.Join(problems...)
	})
	return server.closeError
}

func decodeRequest(frame []byte) (request, error) {
	decoder := json.NewDecoder(bytes.NewReader(frame))
	decoder.DisallowUnknownFields()
	var request request
	if err := decoder.Decode(&request); err != nil {
		return request, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return request, errors.New("request has trailing JSON")
	}
	if request.Version != 1 {
		return request, errors.New("unsupported protocol version")
	}
	switch request.Operation {
	case "list":
		if request.ProfileID != "" || request.Config != "" {
			return request, errors.New("list does not accept a profile")
		}
	case "start":
		request.ProfileID = strings.ToLower(request.ProfileID)
		if !validUUID(request.ProfileID) {
			return request, errors.New("profile ID must be a UUID")
		}
		if err := validConfig(request.Config); err != nil {
			return request, err
		}
	case "stop", "status":
		request.ProfileID = strings.ToLower(request.ProfileID)
		if !validUUID(request.ProfileID) || request.Config != "" {
			return request, errors.New("invalid profile operation")
		}
	default:
		return request, errors.New("unsupported operation")
	}
	return request, nil
}

func validConfig(config string) error {
	if config == "" || len(config) > maxConfigBytes || strings.ContainsAny(config, "\x00\r") {
		return errors.New("invalid UAPI config size")
	}
	for _, line := range strings.Split(config, "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found || key == "" || value == "" || key == "set" || key == "get" || key == "errno" {
			return errors.New("invalid UAPI config field")
		}
		for _, character := range key {
			if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '_') {
				return errors.New("invalid UAPI config key")
			}
		}
	}
	return nil
}

func validUUID(value string) bool {
	if len(value) != 36 || value == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func readFrame(reader io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > maxFrameBytes {
		return nil, errors.New("invalid frame length")
	}
	frame := make([]byte, length)
	_, err := io.ReadFull(reader, frame)
	return frame, err
}

func writeResponse(writer io.Writer, response response) error {
	frame, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if len(frame) > maxFrameBytes {
		return errors.New("response exceeds frame limit")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(frame)))
	if _, err := writer.Write(header[:]); err != nil {
		return err
	}
	_, err = writer.Write(frame)
	return err
}

// Listener owns a root-created, user-readable control socket. No data is
// persisted; closing it removes only the exact socket it created.
type Listener struct {
	listener   *net.UnixListener
	path       string
	allowedUID uint32
	closeOnce  sync.Once
	closeError error
}

func (server *Server) Listen(path string) (*Listener, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("daemon control listener requires root")
	}
	if err := safeSocketPath(path); err != nil {
		return nil, err
	}
	umaskMu.Lock()
	previousUmask := syscall.Umask(0077)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	syscall.Umask(previousUmask)
	umaskMu.Unlock()
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := os.Chown(path, int(server.allowedUID), -1); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return &Listener{listener: listener, path: path, allowedUID: server.allowedUID}, nil
}

func safeSocketPath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\n\r") {
		return errors.New("socket path must be clean and absolute")
	}
	if len(path) > maxSocketPath {
		return errors.New("socket path is too long")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return errors.New("refusing to replace an existing socket path")
		}
		return err
	}
	for directory := filepath.Dir(path); ; directory = filepath.Dir(directory) {
		info, err := os.Lstat(directory)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || stat.Uid != 0 || info.Mode().Perm()&022 != 0 {
			return errors.New("socket ancestor must be root-owned and non-writable")
		}
		if directory == "/" {
			break
		}
	}
	return nil
}

func (server *Server) Serve(listener *Listener) error {
	for {
		connection, err := listener.listener.AcceptUnix()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		select {
		case server.connections <- struct{}{}:
			go func() {
				defer func() { <-server.connections }()
				server.handleConnection(connection)
			}()
		default:
			_ = connection.Close()
		}
	}
}

func (listener *Listener) Close() error {
	listener.closeOnce.Do(func() {
		listener.closeError = listener.listener.Close()
		if err := removeOwnedSocket(listener.path, listener.allowedUID); err != nil && !errors.Is(err, os.ErrNotExist) {
			listener.closeError = errors.Join(listener.closeError, err)
		}
	})
	return listener.closeError
}

func removeOwnedSocket(path string, allowedUID uint32) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || stat.Uid != allowedUID || info.Mode().Perm() != 0600 {
		return fmt.Errorf("refusing to remove unexpected control socket %s", path)
	}
	return os.Remove(path)
}
