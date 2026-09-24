// SPDX-License-Identifier: MIT

//go:build darwin

package daemoncontrol

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

const tunnelStatePrefix = "amneziawg-daemon."

var utunName = regexp.MustCompile(`^utun[0-9]+$`)

type syntheticIPv4RouteSpec struct{ local, peer, target string }

var syntheticIPv4RouteSpecs = [...]syntheticIPv4RouteSpec{
	{local: "192.0.2.2", peer: "192.0.2.1", target: "192.0.2.10"},
	{local: "192.0.2.4", peer: "192.0.2.3", target: "192.0.2.11"},
	{local: "192.0.2.6", peer: "192.0.2.5", target: "192.0.2.12"},
}

var syntheticEndpointRouteTargets = [...]string{"203.0.113.10", "203.0.113.11", "203.0.113.12"}

const syntheticPrecedenceEndpointTarget = "198.51.100.10"

type tunnelBackend struct {
	binary                  string
	syntheticIPv4Routes     bool
	syntheticEndpointRoutes bool
	syntheticFallbackRoute  bool
	routesMu                sync.Mutex
	routeSlots              [len(syntheticIPv4RouteSpecs)]bool
}
type tunnelProcess struct {
	command                 *exec.Cmd
	done                    <-chan struct{}
	dir                     string
	name                    string
	baseline                map[string]bool
	route                   *IPv4Route
	endpointRoute           *PhysicalEndpointRoute
	fallbackRoute           *SyntheticFallbackRoute
	precedenceEndpointRoute *PhysicalEndpointRoute
	routeSlot               int
}

func newTunnelBackend(binary string) (Backend, error) {
	if os.Geteuid() != 0 || !trustedBinary(binary) {
		return nil, errors.New("real tunnel backend requires a trusted root executable")
	}
	return &tunnelBackend{binary: binary}, nil
}

func trustedBinary(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\n\r") {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&022 != 0 || info.Mode()&0111 == 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return false
	}
	for directory := filepath.Dir(path); ; directory = filepath.Dir(directory) {
		info, err := os.Lstat(directory)
		if err != nil {
			return false
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || stat.Uid != 0 || info.Mode().Perm()&022 != 0 {
			return false
		}
		if directory == "/" {
			return true
		}
	}
}

func (backend *tunnelBackend) Start(config string) (Session, error) {
	baseline, err := currentUtuns()
	if err != nil {
		return Session{}, err
	}
	directory, err := os.MkdirTemp("/var/run", tunnelStatePrefix)
	if err != nil {
		return Session{}, err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		_ = os.Remove(directory)
		return Session{}, err
	}
	nameFile := filepath.Join(directory, "name")
	command := exec.Command(backend.binary, "-f", "utun")
	command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "WG_TUN_NAME_FILE=" + nameFile, "LOG_LEVEL=error"}
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		_ = os.Remove(directory)
		return Session{}, err
	}
	done := make(chan struct{})
	go func() { _ = command.Wait(); close(done) }()
	process := &tunnelProcess{command: command, done: done, dir: directory, baseline: baseline, routeSlot: -1}
	name, err := waitForTunnel(process, nameFile)
	if err == nil {
		process.name = name
		err = uapi(name, "set=1\n"+config)
	}
	if err == nil && backend.syntheticIPv4Routes {
		err = backend.configureSyntheticIPv4Route(process)
	}
	if err == nil && backend.syntheticEndpointRoutes {
		err = backend.configureSyntheticEndpointRoute(process)
	}
	if err == nil && backend.syntheticFallbackRoute {
		err = backend.configureSyntheticFallbackRoute(process)
	}
	if err != nil {
		if cleanupErr := backend.Stop(Session{value: process}); cleanupErr != nil {
			return Session{value: process}, errors.Join(err, cleanupErr)
		}
		return Session{}, err
	}
	return Session{value: process}, nil
}

func (backend *tunnelBackend) Status(session Session) error {
	process, ok := session.value.(*tunnelProcess)
	if !ok || process.name == "" {
		return errors.New("invalid tunnel session")
	}
	select {
	case <-process.done:
		return errors.New("owned backend exited")
	default:
	}
	return uapi(process.name, "get=1")
}

func (backend *tunnelBackend) Stop(session Session) error {
	process, ok := session.value.(*tunnelProcess)
	if !ok || process.command.Process == nil {
		return errors.New("invalid tunnel session")
	}
	select {
	case <-process.done:
		if err := process.closePrecedenceEndpointRoute(); err != nil {
			return err
		}
		if process.fallbackRoute != nil {
			if err := process.fallbackRoute.proveAbsentAfterTunnelExit(); err != nil {
				return err
			}
			process.fallbackRoute = nil
		}
		if err := process.closeEndpointRoute(); err != nil {
			return err
		}
		if process.route != nil {
			if err := process.route.proveAbsentAfterTunnelExit(); err != nil {
				return err
			}
			process.route = nil
		}
	default:
		if err := process.closePrecedenceEndpointRoute(); err != nil {
			return err
		}
		if process.fallbackRoute != nil {
			if err := process.fallbackRoute.Close(); err != nil {
				return err
			}
			process.fallbackRoute = nil
		}
		if err := process.closeEndpointRoute(); err != nil {
			return err
		}
		if process.route != nil {
			if err := process.route.Close(); err != nil {
				return err
			}
			process.route = nil
		}
		if err := process.command.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		select {
		case <-process.done:
		case <-time.After(3 * time.Second):
			if err := process.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return err
			}
			<-process.done
		}
	}
	if process.name != "" {
		if err := waitGone(process.name); err != nil {
			return err
		}
	}
	_ = os.Remove(filepath.Join(process.dir, "name"))
	if err := os.Remove(process.dir); err != nil {
		return err
	}
	backend.releaseRouteSlot(process)
	return nil
}

func (process *tunnelProcess) closePrecedenceEndpointRoute() error {
	if process.precedenceEndpointRoute == nil {
		return nil
	}
	if err := process.precedenceEndpointRoute.Close(); err != nil {
		return err
	}
	process.precedenceEndpointRoute = nil
	return nil
}

func (process *tunnelProcess) closeEndpointRoute() error {
	if process.endpointRoute == nil {
		return nil
	}
	if err := process.endpointRoute.Close(); err != nil {
		return err
	}
	process.endpointRoute = nil
	return nil
}

func (backend *tunnelBackend) configureSyntheticIPv4Route(process *tunnelProcess) error {
	backend.routesMu.Lock()
	defer backend.routesMu.Unlock()
	for index, claimed := range backend.routeSlots {
		if claimed {
			continue
		}
		backend.routeSlots[index] = true
		process.routeSlot = index
		spec := syntheticIPv4RouteSpecs[index]
		route, err := configureSyntheticIPv4Route(process, spec.local, spec.peer, spec.target)
		process.route = route
		return err
	}
	return errors.New("synthetic IPv4 route capacity reached")
}

func (backend *tunnelBackend) configureSyntheticEndpointRoute(process *tunnelProcess) error {
	if process.routeSlot < 0 || process.routeSlot >= len(syntheticEndpointRouteTargets) {
		return errors.New("synthetic endpoint route requires an owned tunnel slot")
	}
	route, err := configureSyntheticPhysicalEndpointRoute(syntheticEndpointRouteTargets[process.routeSlot])
	process.endpointRoute = route
	return err
}

func (backend *tunnelBackend) configureSyntheticFallbackRoute(process *tunnelProcess) error {
	if process.routeSlot < 0 || process.routeSlot >= len(syntheticSplitPrefixes) {
		return nil
	}
	if process.routeSlot == 0 {
		endpointRoute, err := configureSyntheticPrecedenceEndpointRoute(syntheticPrecedenceEndpointTarget)
		process.precedenceEndpointRoute = endpointRoute
		if err != nil {
			return err
		}
	}
	route, err := configureSyntheticSplitRoute(process, syntheticSplitPrefixes[process.routeSlot])
	process.fallbackRoute = route
	return err
}

func (backend *tunnelBackend) releaseRouteSlot(process *tunnelProcess) {
	if process.routeSlot < 0 || process.routeSlot >= len(backend.routeSlots) {
		return
	}
	backend.routesMu.Lock()
	backend.routeSlots[process.routeSlot] = false
	backend.routesMu.Unlock()
	process.routeSlot = -1
}

func currentUtuns() (map[string]bool, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	result := make(map[string]bool)
	for _, iface := range interfaces {
		if strings.HasPrefix(iface.Name, "utun") {
			result[iface.Name] = true
		}
	}
	return result, nil
}

func waitForTunnel(process *tunnelProcess, nameFile string) (string, error) {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		select {
		case <-process.done:
			return "", errors.New("backend exited before readiness")
		default:
		}
		content, err := os.ReadFile(nameFile)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(content))
		if !utunName.MatchString(name) || process.baseline[name] {
			return "", errors.New("backend did not create a unique utun")
		}
		if err := uapi(name, "get=1"); err == nil {
			return name, nil
		}
	}
	return "", errors.New("backend did not create a usable utun")
}

func uapi(name, request string) error {
	if !utunName.MatchString(name) {
		return errors.New("invalid tunnel name")
	}
	connection, err := net.DialTimeout("unix", filepath.Join("/var/run/amneziawg", name+".sock"), time.Second)
	if err != nil {
		return err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.WriteString(connection, strings.TrimSpace(request)+"\n\n"); err != nil {
		return err
	}
	reader := bufio.NewReader(connection)
	for total := 0; total < 64*1024; {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		total += len(line)
		if line == "\n" {
			return errors.New("invalid UAPI reply")
		}
		key, value, found := strings.Cut(strings.TrimSuffix(line, "\n"), "=")
		if !found || key == "" {
			return errors.New("invalid UAPI reply")
		}
		if key == "errno" {
			if value == "0" {
				return nil
			}
			return fmt.Errorf("UAPI errno=%s", value)
		}
	}
	return errors.New("UAPI reply too large")
}

func waitGone(name string) error {
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		interfaces, err := currentUtuns()
		if err != nil {
			return err
		}
		if !interfaces[name] {
			if _, err := os.Lstat(filepath.Join("/var/run/amneziawg", name+".sock")); errors.Is(err, os.ErrNotExist) {
				return nil
			}
		}
	}
	return errors.New("owned tunnel did not stop")
}
