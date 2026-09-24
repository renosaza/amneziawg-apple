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
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	protocolVersion    = 1
	maxFrameBytes      = 4 * 1024
	maxProfiles        = 3
	maxConnections     = 16
	maxSocketPath      = 103 // Darwin sun_path has room for a trailing NUL.
	maxConfigBytes     = 2 * 1024
	maxRoutePlanRoutes = 16
)

var umaskMu sync.Mutex

type request struct {
	Version   int             `json:"version"`
	Operation string          `json:"operation"`
	ProfileID string          `json:"profile_id,omitempty"`
	Config    string          `json:"config,omitempty"`
	RoutePlan json.RawMessage `json:"route_plan,omitempty"`
}

// routePlan matches WireGuardKit's MacOSDaemonRoutePlan JSON shape. It is
// validated at the IPC boundary only; the experimental daemon does not apply it.
type routePlan struct {
	LocalAddress string          `json:"local_address"`
	Routes       json.RawMessage `json:"routes"`
}

type routePlanRoute struct {
	Destination string `json:"destination"`
	Owner       string `json:"owner"`
}

// routePlanReservation contains only the non-secret address ownership needed
// to keep concurrently starting route-plan sessions disjoint.
type routePlanReservation struct {
	local    netip.Addr
	tunnel   netip.Prefix
	endpoint netip.Addr
}

type Profile struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type response struct {
	OK              bool      `json:"ok"`
	Error           string    `json:"error,omitempty"`
	ProtocolVersion int       `json:"protocol_version,omitempty"`
	Profile         *Profile  `json:"profile,omitempty"`
	Profiles        []Profile `json:"profiles,omitempty"`
}

// Server owns one user's ephemeral backend sessions.
type Server struct {
	allowedUID  uint32
	mu          sync.Mutex
	profiles    map[string]Session
	backend     Backend
	connections chan struct{}
	closed      bool
	planned     map[string]routePlanReservation
	closeMu     sync.Mutex
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
		planned:     make(map[string]routePlanReservation),
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
	case "hello":
		return response{OK: true, ProtocolVersion: protocolVersion}
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
	case "quiesce":
		if len(server.profiles) != 0 {
			return response{Error: "active_sessions"}
		}
		server.closed = true
		return response{OK: true}
	case "start":
		if _, found := server.profiles[request.ProfileID]; found {
			return response{Error: "already_running"}
		}
		if len(server.profiles) == maxProfiles {
			return response{Error: "capacity"}
		}
		if err := validRoutePlanMatchesConfig(request.RoutePlan, request.Config); err != nil {
			return response{Error: "invalid_request"}
		}
		var reservation routePlanReservation
		if request.RoutePlan != nil {
			var err error
			reservation, err = routePlanReservationFor(request.RoutePlan)
			if err != nil {
				return response{Error: "invalid_request"}
			}
			if len(server.profiles) != len(server.planned) {
				return response{Error: "unplanned_session_active"}
			}
			if conflict := server.plannedConflict(reservation); conflict != "" {
				return response{Error: conflict}
			}
			server.planned[request.ProfileID] = reservation
		} else if len(server.planned) != 0 {
			return response{Error: "planned_session_active"}
		}
		session, err := server.start(request)
		if err != nil {
			if session.value != nil {
				server.profiles[request.ProfileID] = session
			} else if request.RoutePlan != nil {
				delete(server.planned, request.ProfileID)
			}
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
		delete(server.planned, request.ProfileID)
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

func (server *Server) start(request request) (Session, error) {
	if request.RoutePlan == nil {
		return server.backend.Start(request.Config)
	}
	backend, ok := server.backend.(routePlanBackend)
	if !ok {
		return Session{}, errors.New("route plan backend unavailable")
	}
	var plan routePlan
	if err := json.Unmarshal(request.RoutePlan, &plan); err != nil {
		return Session{}, err
	}
	return backend.StartWithRoutePlan(request.Config, plan)
}

// Close prevents new operations and stops every child owned by this server.
func (server *Server) Close() error {
	server.closeMu.Lock()
	defer server.closeMu.Unlock()
	server.mu.Lock()
	server.closed = true
	sessions := make(map[string]Session, len(server.profiles))
	for id, session := range server.profiles {
		sessions[id] = session
	}
	server.mu.Unlock()
	var problems []error
	for id, session := range sessions {
		if err := server.backend.Stop(session); err != nil {
			problems = append(problems, err)
			continue
		}
		server.mu.Lock()
		delete(server.profiles, id)
		delete(server.planned, id)
		server.mu.Unlock()
	}
	return errors.Join(problems...)
}

func (server *Server) plannedConflict(candidate routePlanReservation) string {
	for _, existing := range server.planned {
		if candidate.local == existing.local {
			return "planned_local_address_conflict"
		}
		if candidate.tunnel.Overlaps(existing.tunnel) {
			return "planned_route_conflict"
		}
		if candidate.endpoint == existing.endpoint {
			return "planned_endpoint_conflict"
		}
	}
	return ""
}

func routePlanReservationFor(raw json.RawMessage) (routePlanReservation, error) {
	var plan routePlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return routePlanReservation{}, errors.New("invalid route plan")
	}
	local, err := netip.ParsePrefix(plan.LocalAddress)
	if err != nil {
		return routePlanReservation{}, errors.New("invalid route plan")
	}
	var routes []routePlanRoute
	if err := json.Unmarshal(plan.Routes, &routes); err != nil {
		return routePlanReservation{}, errors.New("invalid route plan")
	}
	reservation := routePlanReservation{local: local.Addr()}
	for _, route := range routes {
		prefix, err := netip.ParsePrefix(route.Destination)
		if err != nil {
			return routePlanReservation{}, errors.New("invalid route plan")
		}
		if route.Owner == "tunnel" {
			reservation.tunnel = prefix
		} else if route.Owner == "physicalEndpoint" {
			reservation.endpoint = prefix.Addr()
		}
	}
	if !reservation.local.IsValid() || !reservation.tunnel.IsValid() || !reservation.endpoint.IsValid() {
		return routePlanReservation{}, errors.New("invalid route plan")
	}
	return reservation, nil
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
	if request.Version != protocolVersion {
		return request, errors.New("unsupported protocol version")
	}
	switch request.Operation {
	case "hello", "list", "quiesce":
		if request.ProfileID != "" || request.Config != "" || request.RoutePlan != nil {
			return request, errors.New("operation does not accept a profile")
		}
	case "start":
		request.ProfileID = strings.ToLower(request.ProfileID)
		if !validUUID(request.ProfileID) {
			return request, errors.New("profile ID must be a UUID")
		}
		if err := validConfig(request.Config); err != nil {
			return request, err
		}
		if err := validRoutePlan(request.RoutePlan); err != nil {
			return request, err
		}
	case "stop", "status":
		request.ProfileID = strings.ToLower(request.ProfileID)
		if !validUUID(request.ProfileID) || request.Config != "" || request.RoutePlan != nil {
			return request, errors.New("invalid profile operation")
		}
	default:
		return request, errors.New("unsupported operation")
	}
	return request, nil
}

func validRoutePlan(raw json.RawMessage) error {
	if raw == nil {
		return nil
	}
	if bytes.Equal(raw, []byte("null")) {
		return errors.New("invalid route plan")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var plan routePlan
	if err := decoder.Decode(&plan); err != nil {
		return errors.New("invalid route plan")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid route plan")
	}
	if plan.Routes == nil || bytes.Equal(plan.Routes, []byte("null")) {
		return errors.New("invalid route plan")
	}
	localAddress, err := netip.ParsePrefix(plan.LocalAddress)
	if err != nil || !localAddress.Addr().Is4() || localAddress.Bits() != 32 ||
		localAddress != localAddress.Masked() || localAddress.String() != plan.LocalAddress ||
		!usableIPv4Address(localAddress.Addr()) {
		return errors.New("invalid route plan")
	}
	decoder = json.NewDecoder(bytes.NewReader(plan.Routes))
	decoder.DisallowUnknownFields()
	var routes []routePlanRoute
	if err := decoder.Decode(&routes); err != nil {
		return errors.New("invalid route plan")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid route plan")
	}
	if len(routes) > maxRoutePlanRoutes {
		return errors.New("invalid route plan")
	}
	seen := make(map[netip.Prefix]struct{}, len(routes))
	for _, route := range routes {
		prefix, err := netip.ParsePrefix(route.Destination)
		if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() || prefix.String() != route.Destination {
			return errors.New("invalid route plan")
		}
		if route.Owner != "tunnel" && route.Owner != "physicalEndpoint" && route.Owner != "physicalExcluded" {
			return errors.New("invalid route plan")
		}
		isDefaultTunnel := route.Owner == "tunnel" && prefix.Bits() == 0 && prefix.Addr().IsUnspecified()
		if !isDefaultTunnel && (!usableIPv4Address(prefix.Addr()) || prefix.Bits() <= 1) {
			return errors.New("invalid route plan")
		}
		if route.Owner == "tunnel" && prefix.Bits() == 0 && !isDefaultTunnel {
			return errors.New("invalid route plan")
		}
		if route.Owner == "physicalEndpoint" && prefix.Bits() != 32 {
			return errors.New("invalid route plan")
		}
		if _, duplicate := seen[prefix]; duplicate {
			return errors.New("invalid route plan")
		}
		seen[prefix] = struct{}{}
	}
	return nil
}

func usableIPv4Address(address netip.Addr) bool {
	if !address.Is4() || !address.IsGlobalUnicast() {
		return false
	}
	bytes := address.As4()
	return bytes[0] != 0 && bytes[0] < 224 && bytes[0] != 127 &&
		!(bytes[0] == 169 && bytes[1] == 254)
}

// validRoutePlanMatchesConfig only binds a route plan to the UAPI fields it
// describes. It deliberately does not reconstruct route ownership or apply
// native routes.
func validRoutePlanMatchesConfig(raw json.RawMessage, config string) error {
	if raw == nil {
		return nil
	}
	if err := validConfig(config); err != nil {
		return err
	}
	if err := validRoutePlan(raw); err != nil {
		return err
	}
	var plan routePlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return errors.New("invalid route plan")
	}
	var routes []routePlanRoute
	if err := json.Unmarshal(plan.Routes, &routes); err != nil {
		return errors.New("invalid route plan")
	}

	var allowedIP netip.Prefix
	var endpoint netip.Addr
	var peers, allowedIPs, endpoints int
	for _, line := range strings.Split(config, "\n") {
		key, value, _ := strings.Cut(line, "=")
		switch key {
		case "public_key":
			peers++
		case "allowed_ip":
			prefix, err := netip.ParsePrefix(value)
			if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() {
				return errors.New("invalid route plan")
			}
			allowedIPs++
			allowedIP = prefix
		case "endpoint":
			address, err := netip.ParseAddrPort(value)
			if err != nil || !usableIPv4Address(address.Addr()) {
				return errors.New("invalid route plan")
			}
			endpoints++
			endpoint = address.Addr()
		}
	}
	if peers != 1 || allowedIPs != 1 || endpoints != 1 {
		return errors.New("invalid route plan")
	}
	var tunnelRoute netip.Prefix
	var physicalEndpoint netip.Addr
	var tunnels, physicalEndpoints int
	var hasPhysicalExclusions bool
	for _, route := range routes {
		prefix, err := netip.ParsePrefix(route.Destination)
		if err != nil {
			return errors.New("invalid route plan")
		}
		switch route.Owner {
		case "tunnel":
			tunnels++
			tunnelRoute = prefix
		case "physicalEndpoint":
			physicalEndpoints++
			physicalEndpoint = prefix.Addr()
		case "physicalExcluded":
			hasPhysicalExclusions = true
		}
	}
	// ExcludeIPs is a NetworkExtension-only field and is intentionally absent
	// from UAPI. A root daemon cannot bind a physicalExcluded route to trusted
	// configuration, so it must reject it before any backend side effect.
	if hasPhysicalExclusions || allowedIP.Bits() == 0 {
		return errors.New("unsupported route plan runtime")
	}
	if tunnels != 1 || physicalEndpoints != 1 || tunnelRoute != allowedIP || physicalEndpoint != endpoint {
		return errors.New("invalid route plan")
	}
	return nil
}

func validConfig(config string) error {
	if config == "" || len(config) > maxConfigBytes || strings.ContainsAny(config, "\x00\r") {
		return errors.New("invalid UAPI config size")
	}
	lines := strings.Split(config, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
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
	if err := recoverStaleSocket(path, server.allowedUID); err != nil {
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

func recoverStaleSocket(path string, allowedUID uint32) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 || stat.Uid != allowedUID || info.Mode().Perm() != 0600 {
		return fmt.Errorf("refusing to recover unexpected control socket %s", path)
	}
	connection, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		return fmt.Errorf("refusing to recover active control socket %s", path)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("refusing to recover uncertain control socket %s", path)
	}
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	currentStat, ok := current.Sys().(*syscall.Stat_t)
	if !ok || current.Mode()&os.ModeSymlink != 0 || current.Mode()&os.ModeSocket == 0 || currentStat.Uid != allowedUID || current.Mode().Perm() != 0600 || currentStat.Dev != stat.Dev || currentStat.Ino != stat.Ino {
		return fmt.Errorf("control socket changed during stale recovery %s", path)
	}
	return os.Remove(path)
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
