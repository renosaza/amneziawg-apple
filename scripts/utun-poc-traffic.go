// SPDX-License-Identifier: MIT
//
// Synthetic macOS-only traffic proof for three independent amneziawg-go pairs.
package main

import (
	"bufio"
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const uapiDir = "/var/run/amneziawg"

var utunName = regexp.MustCompile(`^utun[0-9]+$`)

type device struct {
	label, address, peerAddress string
	port                        int
	privateKey, publicKey       string
	name                        string
	cmd                         *exec.Cmd
	explicitRoute               bool
}

type routeState struct {
	interfaceName, gateway, flags string
}

type baseline struct {
	interfaces map[string]bool
	routes     map[string]routeState
}

func key(label string) (string, string, error) {
	seed := sha256.Sum256([]byte("amneziawg-apple-utun-poc:" + label))
	private, err := ecdh.X25519().NewPrivateKey(seed[:])
	if err != nil {
		return "", "", err
	}
	return hex.EncodeToString(private.Bytes()), hex.EncodeToString(private.PublicKey().Bytes()), nil
}

func topology() ([]*device, error) {
	addresses := [][2]string{{"192.0.2.1", "192.0.2.2"}, {"192.0.2.5", "192.0.2.6"}, {"192.0.2.9", "192.0.2.10"}}
	devices := make([]*device, 0, 6)
	for i, pair := range addresses {
		server := &device{label: fmt.Sprintf("pair%d-server", i+1), address: pair[0], peerAddress: pair[1], port: 51101 + i}
		client := &device{label: fmt.Sprintf("pair%d-client", i+1), address: pair[1], peerAddress: pair[0]}
		var err error
		server.privateKey, server.publicKey, err = key(server.label)
		if err != nil {
			return nil, err
		}
		client.privateKey, client.publicKey, err = key(client.label)
		if err != nil {
			return nil, err
		}
		devices = append(devices, server, client)
	}
	return devices, nil
}

func run(ctx context.Context, path string, args ...string) error {
	command := exec.CommandContext(ctx, path, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", path, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func output(ctx context.Context, path string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, path, args...)
	result, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", path, strings.Join(args, " "), err, strings.TrimSpace(string(result)))
	}
	return string(result), nil
}

func routeField(output, key string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if value, found := strings.CutPrefix(line, key+":"); found {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func currentRoute(ctx context.Context, address string) (routeState, error) {
	result, err := output(ctx, "/sbin/route", "-n", "get", address)
	if err != nil {
		return routeState{}, err
	}
	return routeState{interfaceName: routeField(result, "interface"), gateway: routeField(result, "gateway"), flags: routeField(result, "flags")}, nil
}

func testAddresses(devices []*device) []string {
	addresses := make([]string, 0, len(devices))
	for _, d := range devices {
		addresses = append(addresses, d.address)
	}
	return addresses
}

func routeAllowed(route routeState) bool {
	return !strings.HasPrefix(route.interfaceName, "utun") && !strings.Contains(route.flags, "HOST")
}

func captureBaseline(ctx context.Context, devices []*device) (baseline, error) {
	interfaces, err := output(ctx, "/sbin/ifconfig", "-l")
	if err != nil {
		return baseline{}, err
	}
	state := baseline{interfaces: make(map[string]bool), routes: make(map[string]routeState)}
	for _, name := range strings.Fields(interfaces) {
		state.interfaces[name] = true
	}
	for _, address := range testAddresses(devices) {
		route, err := currentRoute(ctx, address)
		if err != nil {
			return baseline{}, err
		}
		if !routeAllowed(route) {
			return baseline{}, fmt.Errorf("test destination %s already has a conflicting route via %s", address, route.interfaceName)
		}
		state.routes[address] = route
	}
	return state, nil
}

func start(ctx context.Context, binary, directory string, d *device, state baseline, claimed map[string]bool) error {
	nameFile := filepath.Join(directory, d.label+".name")
	d.cmd = exec.Command(binary, "-f", "utun")
	d.cmd.Env = append(os.Environ(), "WG_TUN_NAME_FILE="+nameFile, "LOG_LEVEL=error")
	d.cmd.Stdout = io.Discard
	d.cmd.Stderr = io.Discard
	if err := d.cmd.Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		name, err := os.ReadFile(nameFile)
		if err == nil {
			d.name = strings.TrimSpace(string(name))
			if !utunName.MatchString(d.name) {
				return fmt.Errorf("%s reported invalid interface %q", d.label, d.name)
			}
			if state.interfaces[d.name] || claimed[d.name] {
				return fmt.Errorf("%s claimed baseline or duplicate interface %s", d.label, d.name)
			}
			if _, err := os.Stat(filepath.Join(uapiDir, d.name+".sock")); err == nil {
				if _, err := uapi(d.name, "get=1"); err == nil {
					claimed[d.name] = true
					return nil
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("%s did not create a utun and UAPI socket", d.label)
}

func parseReply(reply string) (map[string]string, error) {
	fields := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(reply), "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid UAPI reply line %q", line)
		}
		fields[parts[0]] = parts[1]
	}
	if fields["errno"] != "0" {
		return nil, fmt.Errorf("UAPI errno=%q", fields["errno"])
	}
	return fields, nil
}

func uapi(name, request string) (map[string]string, error) {
	connection, err := net.DialTimeout("unix", filepath.Join(uapiDir, name+".sock"), time.Second)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(connection, "%s\n\n", strings.TrimSpace(request)); err != nil {
		return nil, err
	}
	var reply strings.Builder
	reader := bufio.NewReader(connection)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		reply.WriteString(line)
		if line == "\n" {
			break
		}
	}
	return parseReply(reply.String())
}

func setRequest(request string) string {
	return "set=1\n" + request
}

func configure(server, client *device) error {
	serverRequest := fmt.Sprintf("private_key=%s\nlisten_port=%d\nreplace_peers=true\npublic_key=%s\nallowed_ip=%s/32", server.privateKey, server.port, client.publicKey, client.address)
	if _, err := uapi(server.name, setRequest(serverRequest)); err != nil {
		return fmt.Errorf("configure %s: %w", server.label, err)
	}
	clientRequest := fmt.Sprintf("private_key=%s\nreplace_peers=true\npublic_key=%s\nallowed_ip=%s/32\nendpoint=127.0.0.1:%d\npersistent_keepalive_interval=1", client.privateKey, server.publicKey, server.address, server.port)
	if _, err := uapi(client.name, setRequest(clientRequest)); err != nil {
		return fmt.Errorf("configure %s: %w", client.label, err)
	}
	return nil
}

func routeUses(ctx context.Context, address, name string) bool {
	route, err := currentRoute(ctx, address)
	return err == nil && route.interfaceName == name
}

func configureInterface(ctx context.Context, d *device) error {
	if err := run(ctx, "/sbin/ifconfig", d.name, "inet", d.address, d.peerAddress, "up"); err != nil {
		return err
	}
	if routeUses(ctx, d.peerAddress, d.name) {
		return nil
	}
	if err := run(ctx, "/sbin/route", "-n", "add", "-host", d.peerAddress, "-interface", d.name); err != nil {
		return err
	}
	d.explicitRoute = true
	return nil
}

func positive(fields map[string]string, key string) error {
	value, err := strconv.ParseUint(fields[key], 10, 64)
	if err != nil || value == 0 {
		return fmt.Errorf("%s is not positive", key)
	}
	return nil
}

func verify(d *device) error {
	fields, err := uapi(d.name, "get=1")
	if err != nil {
		return err
	}
	for _, key := range []string{"last_handshake_time_sec", "rx_bytes", "tx_bytes"} {
		if err := positive(fields, key); err != nil {
			return fmt.Errorf("%s: %w", d.label, err)
		}
	}
	return nil
}

func verifyBaseline(ctx context.Context, state baseline) error {
	interfaces, err := output(ctx, "/sbin/ifconfig", "-l")
	if err != nil {
		return err
	}
	currentInterfaces := make(map[string]bool)
	for _, name := range strings.Fields(interfaces) {
		currentInterfaces[name] = true
	}
	for name := range state.interfaces {
		if !currentInterfaces[name] {
			return fmt.Errorf("baseline interface %s disappeared", name)
		}
	}
	for address, expected := range state.routes {
		actual, err := currentRoute(ctx, address)
		if err != nil {
			return err
		}
		if actual != expected {
			return fmt.Errorf("baseline route changed for %s", address)
		}
	}
	return nil
}

func stop(ctx context.Context, devices []*device, directory string, state baseline) error {
	var cleanupErrors []error
	for _, d := range devices {
		if d.explicitRoute {
			if !routeUses(ctx, d.peerAddress, d.name) {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("refusing to delete %s: it is no longer owned by %s", d.peerAddress, d.name))
				continue
			}
			if err := run(ctx, "/sbin/route", "-n", "delete", "-host", d.peerAddress, "-interface", d.name); err != nil {
				cleanupErrors = append(cleanupErrors, err)
			}
		}
	}
	for _, d := range devices {
		if d.cmd != nil && d.cmd.Process != nil {
			if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
				cleanupErrors = append(cleanupErrors, err)
			}
		}
	}
	for _, d := range devices {
		if d.cmd == nil || d.cmd.Process == nil {
			continue
		}
		done := make(chan error, 1)
		go func(command *exec.Cmd) { done <- command.Wait() }(d.cmd)
		select {
		case err := <-done:
			if err != nil {
				if _, expected := err.(*exec.ExitError); !expected {
					cleanupErrors = append(cleanupErrors, err)
				}
			}
		case <-time.After(3 * time.Second):
			if err := d.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				cleanupErrors = append(cleanupErrors, err)
			}
			if err := <-done; err != nil {
				if _, expected := err.(*exec.ExitError); !expected {
					cleanupErrors = append(cleanupErrors, err)
				}
			}
		}
		_ = os.Remove(filepath.Join(directory, d.label+".name"))
	}
	_ = os.Remove(directory)
	if err := verifyBaseline(ctx, state); err != nil {
		cleanupErrors = append(cleanupErrors, err)
	}
	return errors.Join(cleanupErrors...)
}

func selfCheck() error {
	devices, err := topology()
	if err != nil {
		return err
	}
	repeatedKey, _, err := key(devices[0].label)
	if err != nil || len(devices) != 6 || devices[0].privateKey == devices[0].publicKey || devices[0].privateKey != repeatedKey {
		return fmt.Errorf("deterministic topology check failed")
	}
	keys, addresses := make(map[string]bool), make(map[string]bool)
	for _, d := range devices {
		if keys[d.publicKey] || addresses[d.address] || d.address == d.peerAddress {
			return fmt.Errorf("topology is not unique")
		}
		keys[d.publicKey], addresses[d.address] = true, true
	}
	if !routeAllowed(routeState{interfaceName: "en0", flags: "<UP,GATEWAY>"}) || routeAllowed(routeState{interfaceName: "utun0"}) || routeAllowed(routeState{flags: "<UP,HOST>"}) {
		return fmt.Errorf("route collision guard failed")
	}
	fields, err := parseReply("rx_bytes=42\ntx_bytes=42\nlast_handshake_time_sec=1\nerrno=0\n")
	if err != nil {
		return err
	}
	for _, field := range []string{"rx_bytes", "tx_bytes", "last_handshake_time_sec"} {
		if err := positive(fields, field); err != nil {
			return err
		}
	}
	if _, err := parseReply("errno=1\n"); err == nil {
		return fmt.Errorf("UAPI error reply was accepted")
	}
	if setRequest("private_key=synthetic") != "set=1\nprivate_key=synthetic" {
		return fmt.Errorf("UAPI set operation prefix is missing")
	}
	return nil
}

func runProof(ctx context.Context, binary string) (err error) {
	devices, err := topology()
	if err != nil {
		return err
	}
	state, err := captureBaseline(ctx, devices)
	if err != nil {
		return err
	}
	directory, err := os.MkdirTemp("/private/tmp", "amneziawg-utun-traffic-poc.")
	if err != nil {
		return err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		_ = os.Remove(directory)
		return err
	}
	defer func() { err = errors.Join(err, stop(context.Background(), devices, directory, state)) }()
	claimed := make(map[string]bool)
	for _, d := range devices {
		if err := start(ctx, binary, directory, d, state, claimed); err != nil {
			return err
		}
	}
	for i := 0; i < len(devices); i += 2 {
		if err := configure(devices[i], devices[i+1]); err != nil {
			return err
		}
	}
	for _, d := range devices {
		if err := configureInterface(ctx, d); err != nil {
			return err
		}
	}
	for i := 1; i < len(devices); i += 2 {
		if err := run(ctx, "/sbin/ping", "-n", "-c", "1", "-W", "1000", devices[i-1].address); err != nil {
			return err
		}
	}
	for _, d := range devices {
		if err := verify(d); err != nil {
			return err
		}
		fmt.Printf("%s: %s %s -> %s\n", d.label, d.name, d.address, d.peerAddress)
	}
	return nil
}

func main() {
	self := flag.Bool("self-check", false, "validate synthetic data without touching the network")
	binary := flag.String("binary", "", "absolute path to amneziawg-go")
	flag.Parse()
	if *self {
		if err := selfCheck(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if runtime.GOOS != "darwin" || os.Geteuid() != 0 || !filepath.IsAbs(*binary) {
		fmt.Fprintln(os.Stderr, "requires macOS, sudo, and -binary with an absolute path")
		os.Exit(2)
	}
	info, err := os.Stat(*binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		fmt.Fprintln(os.Stderr, "-binary must name an executable file")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := runProof(ctx, *binary); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
