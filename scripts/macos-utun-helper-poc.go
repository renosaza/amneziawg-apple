// SPDX-License-Identifier: MIT
//
// A deliberately narrow, root-only macOS helper prototype. It owns one empty
// amneziawg-go utun process and proves its UAPI socket can be queried.
// It deliberately accepts no VPN configuration and changes no addresses,
// routes, DNS, PF rules, or application state.
// ponytail: this is a CLI POC; replace persisted state with authenticated IPC
// only after a separately reviewed production helper design exists.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	stateRoot   = "/var/run"
	statePrefix = "amneziawg-helper-poc."
	uapiDir     = "/var/run/amneziawg"
)

var utunName = regexp.MustCompile(`^utun[0-9]+$`)

type state struct {
	directory string
	binary    string
	pid       int
	started   string
	name      string
}

func fail(format string, arguments ...any) error {
	return fmt.Errorf("macos-utun-helper-poc: "+format, arguments...)
}

func requireMacOSRoot() error {
	if runtime.GOOS != "darwin" {
		return fail("macOS is required")
	}
	if os.Geteuid() != 0 {
		return fail("start, status, and stop require sudo")
	}
	return nil
}

func baselineUtuns() (map[string]bool, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	baseline := make(map[string]bool)
	for _, iface := range interfaces {
		if strings.HasPrefix(iface.Name, "utun") {
			baseline[iface.Name] = true
		}
	}
	return baseline, nil
}

func ownedRegular(info os.FileInfo, mode os.FileMode) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0 && info.Mode().IsRegular() && info.Mode().Perm() == mode
}

func validateBackendPath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\n\r") {
		return fail("-binary must be a clean absolute path")
	}
	return nil
}

func trustedAncestor(path string) error {
	for directory := filepath.Dir(path); ; directory = filepath.Dir(directory) {
		info, err := os.Lstat(directory)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || stat.Uid != 0 || info.Mode().Perm()&022 != 0 {
			return fail("backend ancestor %s must be a root-owned, non-writable directory", directory)
		}
		if directory == "/" {
			return nil
		}
	}
}

func validateBackend(path string) error {
	if err := validateBackendPath(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&022 != 0 || info.Mode()&0111 == 0 {
		return fail("-binary must be a root-owned, non-writable executable file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return fail("-binary must be owned by root")
	}
	return trustedAncestor(path)
}

func safeStateDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) != stateRoot || !strings.HasPrefix(filepath.Base(path), statePrefix) {
		return fail("refusing unexpected state directory")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !ownedDirectory(info) {
		return fail("state directory must be a root-owned 0700 directory")
	}
	return nil
}

func ownedDirectory(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0 && info.IsDir() && info.Mode().Perm() == 0700
}

func createStateDirectory() (string, error) {
	directory, err := os.MkdirTemp(stateRoot, statePrefix)
	if err != nil {
		return "", err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		_ = os.Remove(directory)
		return "", err
	}
	if err := safeStateDirectory(directory); err != nil {
		_ = os.Remove(directory)
		return "", err
	}
	return directory, nil
}

func writeStateFile(directory, name, value string) error {
	file, err := os.OpenFile(filepath.Join(directory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.WriteString(file, value+"\n")
	return err
}

func readStateFile(directory, name string) (string, error) {
	path := filepath.Join(directory, name)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !ownedRegular(info, 0600) {
		return "", fail("state file %s must be a root-owned 0600 regular file", name)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(content))
	if value == "" || strings.ContainsAny(value, "\x00\n\r") {
		return "", fail("invalid state file %s", name)
	}
	return value, nil
}

func readName(directory string) (string, error) {
	content, err := os.ReadFile(filepath.Join(directory, "name"))
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(content))
	if !utunName.MatchString(name) {
		return "", fail("invalid utun name %q", name)
	}
	return name, nil
}

func psValue(pid int, field string) (string, bool, error) {
	command := exec.Command("/bin/ps", "-p", strconv.Itoa(pid), "-o", field+"=")
	output, err := command.Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
			return "", false, nil
		}
		return "", false, err
	}
	value := strings.TrimSpace(string(output))
	return value, value != "", nil
}

func readState(directory string) (state, error) {
	if err := safeStateDirectory(directory); err != nil {
		return state{}, err
	}
	binary, err := readStateFile(directory, "binary")
	if err != nil {
		return state{}, err
	}
	if err := validateBackendPath(binary); err != nil {
		return state{}, err
	}
	pidText, err := readStateFile(directory, "pid")
	if err != nil {
		return state{}, err
	}
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid < 1 {
		return state{}, fail("invalid recorded PID")
	}
	started, err := readStateFile(directory, "started")
	if err != nil {
		return state{}, err
	}
	name, err := readName(directory)
	if err != nil {
		return state{}, err
	}
	return state{directory: directory, binary: binary, pid: pid, started: started, name: name}, nil
}

func processIsOwned(value state) (bool, error) {
	started, exists, err := psValue(value.pid, "lstart")
	if err != nil || !exists {
		return false, err
	}
	command, exists, err := psValue(value.pid, "command")
	if err != nil || !exists {
		return false, err
	}
	if started != value.started || command != value.binary+" -f utun" {
		return false, fail("recorded process identity no longer matches")
	}
	return true, nil
}

func uapiReady(name string) error {
	if !utunName.MatchString(name) {
		return fail("invalid utun name")
	}
	socket := filepath.Join(uapiDir, name+".sock")
	info, err := os.Lstat(socket)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fail("UAPI path is not a socket")
	}
	connection, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return err
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	if _, err := io.WriteString(connection, "get=1\n\n"); err != nil {
		return err
	}
	reader := bufio.NewReader(connection)
	fields := make(map[string]string)
	for total := 0; total < 64*1024; {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		total += len(line)
		if line == "\n" {
			break
		}
		key, value, found := strings.Cut(strings.TrimSuffix(line, "\n"), "=")
		if !found || key == "" {
			return fail("invalid UAPI reply")
		}
		fields[key] = value
	}
	if fields["errno"] != "0" {
		return fail("UAPI get failed")
	}
	return nil
}

func validateNewUtun(name string, baseline map[string]bool) error {
	if baseline[name] {
		return fail("backend reported baseline interface %s", name)
	}
	return nil
}

func waitForReady(directory string, baseline map[string]bool) (string, error) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		name, err := readName(directory)
		if err == nil {
			if err := validateNewUtun(name, baseline); err != nil {
				return "", err
			}
			if err := uapiReady(name); err == nil {
				return name, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", fail("backend did not create a usable utun UAPI socket")
}

func stopChild(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case <-done:
		return nil
	case <-time.After(3 * time.Second):
		if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		<-done
		return nil
	}
}

func removeState(directory string) error {
	if err := safeStateDirectory(directory); err != nil {
		return err
	}
	var problems []error
	for _, name := range []string{"binary", "pid", "started", "name"} {
		if err := os.Remove(filepath.Join(directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			problems = append(problems, err)
		}
	}
	if err := os.Remove(directory); err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

func start(binary string) (state, error) {
	if err := requireMacOSRoot(); err != nil {
		return state{}, err
	}
	baseline, err := baselineUtuns()
	if err != nil {
		return state{}, err
	}
	if err := validateBackend(binary); err != nil {
		return state{}, err
	}
	directory, err := createStateDirectory()
	if err != nil {
		return state{}, err
	}
	command := exec.Command(binary, "-f", "utun")
	command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "WG_TUN_NAME_FILE=" + filepath.Join(directory, "name"), "LOG_LEVEL=error"}
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		_ = os.Remove(directory)
		return state{}, err
	}
	rollback := func(cause error) (state, error) {
		return state{}, errors.Join(cause, stopChild(command), removeState(directory))
	}
	started, exists, err := psValue(command.Process.Pid, "lstart")
	if err != nil || !exists {
		return rollback(errors.Join(err, fail("backend exited before recording its identity")))
	}
	if err := writeStateFile(directory, "binary", binary); err != nil {
		return rollback(err)
	}
	if err := writeStateFile(directory, "pid", strconv.Itoa(command.Process.Pid)); err != nil {
		return rollback(err)
	}
	if err := writeStateFile(directory, "started", started); err != nil {
		return rollback(err)
	}
	name, err := waitForReady(directory, baseline)
	if err != nil {
		return rollback(err)
	}
	return state{directory: directory, binary: binary, pid: command.Process.Pid, started: started, name: name}, nil
}

func waitForExit(value state) error {
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		owned, err := processIsOwned(value)
		if err != nil {
			return err
		}
		if !owned {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fail("backend did not stop after SIGTERM")
}

func deviceGone(name string) (bool, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return false, err
	}
	for _, iface := range interfaces {
		if iface.Name == name {
			return false, nil
		}
	}
	_, err = os.Lstat(filepath.Join(uapiDir, name+".sock"))
	if err == nil {
		return false, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return false, err
}

func waitForDeviceGone(name string) error {
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		gone, err := deviceGone(name)
		if err != nil {
			return err
		}
		if gone {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fail("recorded utun or UAPI socket still exists")
}

func stop(value state) error {
	owned, err := processIsOwned(value)
	if err != nil {
		return err
	}
	if !owned {
		gone, err := deviceGone(value.name)
		if err != nil {
			return err
		}
		if !gone {
			return fail("recorded process is absent but its utun or UAPI socket remains")
		}
		return removeState(value.directory)
	}
	process, err := os.FindProcess(value.pid)
	if err != nil {
		return err
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	if err := waitForExit(value); err != nil {
		owned, checkErr := processIsOwned(value)
		if checkErr != nil || !owned {
			return errors.Join(err, checkErr)
		}
		if killErr := process.Signal(syscall.SIGKILL); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			return errors.Join(err, killErr)
		}
		if err := waitForExit(value); err != nil {
			return err
		}
	}
	if err := waitForDeviceGone(value.name); err != nil {
		return err
	}
	return removeState(value.directory)
}

func selfCheck() error {
	if !utunName.MatchString("utun12") || utunName.MatchString("utun12.sock") {
		return fail("utun-name validation failed")
	}
	if err := validateNewUtun("utun12", map[string]bool{"utun12": true}); err == nil {
		return fail("baseline utun was accepted")
	}
	if err := safeStatePathForCheck("/var/run/" + statePrefix + "abc"); err != nil {
		return err
	}
	if err := safeStatePathForCheck("/var/run/" + statePrefix + "../escape"); err == nil {
		return fail("state path traversal was accepted")
	}
	if err := validateBackendPath("/usr/local/libexec/amneziawg-go-helper-poc"); err != nil {
		return err
	}
	if err := validateBackendPath("/usr/local/libexec/../escape"); err == nil {
		return fail("backend path traversal was accepted")
	}
	if runtime.GOOS == "darwin" {
		if err := validateBackend("/usr/bin/true"); err != nil {
			return err
		}
	}
	if err := parseUAPIReplyForCheck("public_key=synthetic\nerrno=0\n\n"); err != nil {
		return err
	}
	if err := parseUAPIReplyForCheck("errno=1\n\n"); err == nil {
		return fail("UAPI error reply was accepted")
	}
	return nil
}

func safeStatePathForCheck(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) != stateRoot || !strings.HasPrefix(filepath.Base(path), statePrefix) {
		return fail("invalid state path")
	}
	return nil
}

func parseUAPIReplyForCheck(reply string) error {
	fields := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSuffix(reply, "\n"), "\n") {
		if line == "" {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || key == "" {
			return fail("invalid UAPI reply")
		}
		fields[key] = value
	}
	if fields["errno"] != "0" {
		return fail("UAPI get failed")
	}
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  scripts/macos-utun-helper-poc.go self-check")
	fmt.Fprintln(os.Stderr, "  sudo helper start -binary /absolute/root-owned/amneziawg-go")
	fmt.Fprintln(os.Stderr, "  sudo helper status -state-dir /var/run/amneziawg-helper-poc.XXXXXX")
	fmt.Fprintln(os.Stderr, "  sudo helper stop -state-dir /var/run/amneziawg-helper-poc.XXXXXX")
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "self-check" {
		if err := selfCheck(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "start":
		flags := flag.NewFlagSet("start", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		binary := flags.String("binary", "", "")
		if err := flags.Parse(os.Args[2:]); err != nil || flags.NArg() != 0 || *binary == "" {
			usage()
			os.Exit(2)
		}
		value, err := start(*binary)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("state_dir=%s\npid=%d\nutun=%s\nuapi=ready\n", value.directory, value.pid, value.name)
	case "status", "stop":
		flags := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		directory := flags.String("state-dir", "", "")
		if err := flags.Parse(os.Args[2:]); err != nil || flags.NArg() != 0 || *directory == "" {
			usage()
			os.Exit(2)
		}
		if err := requireMacOSRoot(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		value, err := readState(*directory)
		if err == nil && os.Args[1] == "status" {
			var owned bool
			owned, err = processIsOwned(value)
			if err == nil && !owned {
				err = fail("recorded process is not running")
			}
			if err == nil {
				err = uapiReady(value.name)
			}
			if err == nil {
				fmt.Printf("state_dir=%s\npid=%d\nutun=%s\nuapi=ready\n", value.directory, value.pid, value.name)
			}
		}
		if err == nil && os.Args[1] == "stop" {
			err = stop(value)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}
