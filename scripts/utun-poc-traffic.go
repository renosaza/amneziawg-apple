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

func checkIdle(ctx context.Context) error {
	interfaces, err := output(ctx, "/sbin/ifconfig", "-l")
	if err != nil {
		return err
	}
	for _, name := range strings.Fields(interfaces) {
		if strings.HasPrefix(name, "utun") {
			return fmt.Errorf("existing %s found; use an idle runner", name)
		}
	}
	return nil
}

func start(ctx context.Context, binary, directory string, d *device) error {
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
			if _, err := os.Stat(filepath.Join(uapiDir, d.name+".sock")); err == nil {
				return nil
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

func configure(server, client *device) error {
	serverRequest := fmt.Sprintf("private_key=%s\nlisten_port=%d\nreplace_peers=true\npublic_key=%s\nallowed_ip=%s/32", server.privateKey, server.port, client.publicKey, client.address)
	if _, err := uapi(server.name, serverRequest); err != nil {
		return fmt.Errorf("configure %s: %w", server.label, err)
	}
	clientRequest := fmt.Sprintf("private_key=%s\nreplace_peers=true\npublic_key=%s\nallowed_ip=%s/32\nendpoint=127.0.0.1:%d\npersistent_keepalive_interval=1", client.privateKey, server.publicKey, server.address, server.port)
	if _, err := uapi(client.name, clientRequest); err != nil {
		return fmt.Errorf("configure %s: %w", client.label, err)
	}
	return nil
}

func routeUses(ctx context.Context, address, name string) bool {
	result, err := output(ctx, "/sbin/route", "-n", "get", address)
	return err == nil && strings.Contains(result, "interface: "+name)
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

func stop(ctx context.Context, devices []*device, directory string) {
	for _, d := range devices {
		if d.explicitRoute {
			_ = run(ctx, "/sbin/route", "-n", "delete", "-host", d.peerAddress, "-interface", d.name)
		}
	}
	for _, d := range devices {
		if d.cmd != nil && d.cmd.Process != nil {
			_ = d.cmd.Process.Signal(syscall.SIGTERM)
		}
	}
	for _, d := range devices {
		if d.cmd == nil || d.cmd.Process == nil {
			continue
		}
		done := make(chan error, 1)
		go func(command *exec.Cmd) { done <- command.Wait() }(d.cmd)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = d.cmd.Process.Kill()
			<-done
		}
		_ = os.Remove(filepath.Join(directory, d.label+".name"))
	}
	_ = os.Remove(directory)
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
	if err := checkIdle(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	devices, err := topology()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	directory, err := os.MkdirTemp("/private/tmp", "amneziawg-utun-traffic-poc.")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer stop(context.Background(), devices, directory)
	for _, d := range devices {
		if err := start(ctx, *binary, directory, d); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	for i := 0; i < len(devices); i += 2 {
		if err := configure(devices[i], devices[i+1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	for _, d := range devices {
		if err := configureInterface(ctx, d); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	for i := 1; i < len(devices); i += 2 {
		if err := run(ctx, "/sbin/ping", "-n", "-c", "1", "-W", "1000", devices[i-1].address); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	for _, d := range devices {
		if err := verify(d); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%s: %s %s -> %s\n", d.label, d.name, d.address, d.peerAddress)
	}
}
