// SPDX-License-Identifier: MIT

package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/renosaza/amneziawg-daemon-control-poc"
)

const maxFrameBytes = 4 * 1024

const manualRoutePlanProfileID = "11111111-2222-4333-8444-555555555555"
const manualRoutePlanProfileIDTwo = "22222222-3333-4444-8555-666666666666"
const manualRoutePlanProfileIDThree = "33333333-4444-4555-8666-777777777777"
const manualFullRoutePlanProfileID = "44444444-5555-4666-8777-888888888888"

var manualRoutePlanRequest = struct {
	Version   int             `json:"version"`
	Operation string          `json:"operation"`
	ProfileID string          `json:"profile_id"`
	Config    string          `json:"config,omitempty"`
	RoutePlan json.RawMessage `json:"route_plan,omitempty"`
}{
	Version:   1,
	Operation: "start",
	ProfileID: manualRoutePlanProfileID,
	Config: "private_key=1111111111111111111111111111111111111111111111111111111111111111\n" +
		"public_key=2222222222222222222222222222222222222222222222222222222222222222\n" +
		"allowed_ip=198.51.100.0/24\nendpoint=198.51.100.10:1",
	RoutePlan: json.RawMessage(`{"local_address":"192.0.2.2/32","routes":[{"destination":"198.51.100.0/24","owner":"tunnel"},{"destination":"198.51.100.10/32","owner":"physicalEndpoint"}]}`),
}

var manualRoutePlanRequestTwo = struct {
	Version   int             `json:"version"`
	Operation string          `json:"operation"`
	ProfileID string          `json:"profile_id"`
	Config    string          `json:"config,omitempty"`
	RoutePlan json.RawMessage `json:"route_plan,omitempty"`
}{
	Version:   1,
	Operation: "start",
	ProfileID: manualRoutePlanProfileIDTwo,
	Config: "private_key=3333333333333333333333333333333333333333333333333333333333333333\n" +
		"public_key=4444444444444444444444444444444444444444444444444444444444444444\n" +
		"allowed_ip=203.0.113.0/24\nendpoint=203.0.113.10:1",
	RoutePlan: json.RawMessage(`{"local_address":"192.0.2.3/32","routes":[{"destination":"203.0.113.0/24","owner":"tunnel"},{"destination":"203.0.113.10/32","owner":"physicalEndpoint"}]}`),
}

var manualRoutePlanRequestThree = struct {
	Version   int             `json:"version"`
	Operation string          `json:"operation"`
	ProfileID string          `json:"profile_id"`
	Config    string          `json:"config,omitempty"`
	RoutePlan json.RawMessage `json:"route_plan,omitempty"`
}{
	Version:   1,
	Operation: "start",
	ProfileID: manualRoutePlanProfileIDThree,
	Config: "private_key=5555555555555555555555555555555555555555555555555555555555555555\n" +
		"public_key=6666666666666666666666666666666666666666666666666666666666666666\n" +
		"allowed_ip=192.0.2.128/25\nendpoint=192.0.2.250:1",
	RoutePlan: json.RawMessage(`{"local_address":"192.0.2.4/32","routes":[{"destination":"192.0.2.128/25","owner":"tunnel"},{"destination":"192.0.2.250/32","owner":"physicalEndpoint"}]}`),
}

var manualFullRoutePlanRequest = struct {
	Version   int             `json:"version"`
	Operation string          `json:"operation"`
	ProfileID string          `json:"profile_id"`
	Config    string          `json:"config,omitempty"`
	RoutePlan json.RawMessage `json:"route_plan,omitempty"`
}{
	Version:   1,
	Operation: "start",
	ProfileID: manualFullRoutePlanProfileID,
	Config: "private_key=7777777777777777777777777777777777777777777777777777777777777777\n" +
		"public_key=8888888888888888888888888888888888888888888888888888888888888888\n" +
		"allowed_ip=0.0.0.0/0\nendpoint=203.0.113.10:1",
	RoutePlan: manualFullRoutePlan(),
}

func main() {
	socket := flag.String("socket", "", "root-owned directory socket path")
	uidText := flag.String("uid", "", "authorized non-root macOS UID")
	binaryPath := flag.String("binary", "", "trusted absolute amneziawg-go path")
	allowRoutePlans := flag.Bool("allow-route-plan-runtime", false, "enable the experimental root-controlled route-plan runtime")
	allowFullRoutes := flag.Bool("allow-full-route-runtime", false, "enable experimental logical IPv4 full-route plans (requires -allow-route-plan-runtime)")
	checkIdle := flag.Bool("check-idle", false, "exit successfully only when the daemon has no sessions")
	prepareStop := flag.Bool("prepare-stop", false, "atomically refuse new sessions when the daemon is idle")
	manualRoutePlanStart := flag.Bool("manual-route-plan-start", false, "send the fixed synthetic route-plan start request")
	manualRoutePlanStop := flag.Bool("manual-route-plan-stop", false, "stop the fixed synthetic route-plan session")
	manualRoutePlanStartTwo := flag.Bool("manual-route-plan-start-two", false, "send the second fixed synthetic route-plan start request")
	manualRoutePlanStopTwo := flag.Bool("manual-route-plan-stop-two", false, "stop the second fixed synthetic route-plan session")
	manualRoutePlanStartThree := flag.Bool("manual-route-plan-start-three", false, "send the third fixed synthetic route-plan start request")
	manualRoutePlanStopThree := flag.Bool("manual-route-plan-stop-three", false, "stop the third fixed synthetic route-plan session")
	manualRoutePlanAssertOneThree := flag.Bool("manual-route-plan-assert-one-three", false, "assert only the first and third fixed synthetic route-plan sessions are running")
	manualFullRoutePlanStart := flag.Bool("manual-full-route-plan-start", false, "start the fixed synthetic full-route plan")
	manualFullRoutePlanStop := flag.Bool("manual-full-route-plan-stop", false, "stop the fixed synthetic full-route plan")
	manualRoutePlanAssertFullThree := flag.Bool("manual-route-plan-assert-full-three", false, "assert only the full and third fixed synthetic route-plan sessions are running")
	manualRoutePlanAssertThree := flag.Bool("manual-route-plan-assert-three", false, "assert only the third fixed synthetic route-plan session is running")
	flag.Parse()
	if *checkIdle || *prepareStop || *manualRoutePlanStart || *manualRoutePlanStop || *manualRoutePlanStartTwo || *manualRoutePlanStopTwo || *manualRoutePlanStartThree || *manualRoutePlanStopThree || *manualRoutePlanAssertOneThree || *manualFullRoutePlanStart || *manualFullRoutePlanStop || *manualRoutePlanAssertFullThree || *manualRoutePlanAssertThree {
		if flag.NArg() != 0 || *socket == "" || *uidText != "" || *binaryPath != "" {
			fmt.Fprintln(os.Stderr, "usage: daemon-control-poc fixed control operation -socket /var/run/name.sock")
			os.Exit(2)
		}
		operations := 0
		for _, selected := range []bool{*checkIdle, *prepareStop, *manualRoutePlanStart, *manualRoutePlanStop, *manualRoutePlanStartTwo, *manualRoutePlanStopTwo, *manualRoutePlanStartThree, *manualRoutePlanStopThree, *manualRoutePlanAssertOneThree, *manualFullRoutePlanStart, *manualFullRoutePlanStop, *manualRoutePlanAssertFullThree, *manualRoutePlanAssertThree} {
			if selected {
				operations++
			}
		}
		if operations != 1 {
			fmt.Fprintln(os.Stderr, "choose exactly one control operation")
			os.Exit(2)
		}
		if *manualRoutePlanStart {
			if err := daemonManualRoutePlanStart(*socket); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		if *manualRoutePlanStop {
			if err := daemonManualRoutePlanStop(*socket); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		if *manualRoutePlanStartTwo {
			if err := daemonManualRoutePlanStartTwo(*socket); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		if *manualRoutePlanStopTwo {
			if err := daemonManualRoutePlanStopTwo(*socket); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		if *manualRoutePlanStartThree {
			if err := daemonManualRoutePlanStartThree(*socket); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		if *manualRoutePlanStopThree {
			if err := daemonManualRoutePlanStopThree(*socket); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		if *manualRoutePlanAssertOneThree {
			if err := daemonManualRoutePlanAssertProfiles(*socket, manualRoutePlanProfileID, manualRoutePlanProfileIDThree); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		if *manualFullRoutePlanStart {
			if err := daemonManualRoutePlanStartRequest(*socket, manualFullRoutePlanRequest); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		if *manualFullRoutePlanStop {
			if err := daemonManualRoutePlanStopRequest(*socket, manualFullRoutePlanProfileID); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		if *manualRoutePlanAssertFullThree {
			if err := daemonManualRoutePlanAssertProfiles(*socket, manualFullRoutePlanProfileID, manualRoutePlanProfileIDThree); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		if *manualRoutePlanAssertThree {
			if err := daemonManualRoutePlanAssertProfiles(*socket, manualRoutePlanProfileIDThree); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		operation := "list"
		if *prepareStop {
			operation = "quiesce"
		}
		response, err := daemonRequest(*socket, operation)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if *prepareStop {
			return
		}
		if len(response.Profiles) != 0 {
			fmt.Fprintln(os.Stderr, "daemon has active sessions")
			os.Exit(1)
		}
		return
	}
	if flag.NArg() != 0 || *socket == "" || *uidText == "" || *binaryPath == "" {
		fmt.Fprintln(os.Stderr, "usage: daemon-control-poc -socket /var/run/name.sock -uid <macOS-uid> -binary /root-owned/amneziawg-go")
		os.Exit(2)
	}
	if *allowFullRoutes && !*allowRoutePlans {
		fmt.Fprintln(os.Stderr, "-allow-full-route-runtime requires -allow-route-plan-runtime")
		os.Exit(2)
	}
	uid, err := parseUID(*uidText)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	backend, err := daemoncontrol.NewTunnelBackendWithFullRouteRuntime(*binaryPath, *allowRoutePlans, *allowFullRoutes)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	server, err := daemoncontrol.NewServerWithBackend(uid, backend)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	listener, err := server.Listen(*socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer listener.Close()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		_ = listener.Close()
	}()
	if err := server.Serve(listener); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if cleanupErr := cleanup(server); cleanupErr != nil {
			fmt.Fprintln(os.Stderr, "daemon cleanup failed after retries")
		}
		os.Exit(1)
	}
	if err := cleanup(server); err != nil {
		fmt.Fprintln(os.Stderr, "daemon cleanup failed after retries")
		os.Exit(1)
	}
}

// manualFullRoutePlan creates a fixed TEST-NET plan for the disposable
// full-route smoke test. It deliberately has no user-controlled inputs.
func manualFullRoutePlan() json.RawMessage {
	excluded := []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"), // synthetic LAN and second split prefix
		netip.MustParsePrefix("198.51.100.0/24"),
	}
	tunnelExcluded := append(append([]netip.Prefix(nil), excluded...), netip.MustParsePrefix("203.0.113.10/32"))
	routes := make([]map[string]string, 0, 64)
	for _, prefix := range manualIPv4TunnelComplement(tunnelExcluded) {
		routes = append(routes, map[string]string{"destination": prefix.String(), "owner": "tunnel"})
	}
	for _, prefix := range excluded {
		routes = append(routes, map[string]string{"destination": prefix.String(), "owner": "excluded"})
	}
	routes = append(routes, map[string]string{"destination": "203.0.113.10/32", "owner": "physicalEndpoint"})
	plan, err := json.Marshal(map[string]any{"local_address": "192.0.2.6/32", "routes": routes})
	if err != nil || len(plan) >= maxFrameBytes {
		panic("fixed full-route smoke plan is invalid")
	}
	return plan
}

func manualIPv4TunnelComplement(excluded []netip.Prefix) []netip.Prefix {
	routes := []netip.Prefix{
		netip.MustParsePrefix("1.0.0.0/8"), netip.MustParsePrefix("2.0.0.0/7"),
		netip.MustParsePrefix("4.0.0.0/6"), netip.MustParsePrefix("8.0.0.0/5"),
		netip.MustParsePrefix("16.0.0.0/4"), netip.MustParsePrefix("32.0.0.0/3"),
		netip.MustParsePrefix("64.0.0.0/2"), netip.MustParsePrefix("128.0.0.0/2"),
		netip.MustParsePrefix("192.0.0.0/3"),
	}
	for _, exclusion := range excluded {
		next := make([]netip.Prefix, 0, len(routes))
		for _, route := range routes {
			next = append(next, manualSubtractIPv4Prefix(route, exclusion)...)
		}
		routes = next
	}
	return routes
}

func manualSubtractIPv4Prefix(route, exclusion netip.Prefix) []netip.Prefix {
	if !route.Overlaps(exclusion) {
		return []netip.Prefix{route}
	}
	if exclusion.Bits() <= route.Bits() && exclusion.Contains(route.Addr()) {
		return nil
	}
	if route.Bits() >= exclusion.Bits() || !route.Contains(exclusion.Addr()) {
		return []netip.Prefix{route}
	}
	left := netip.PrefixFrom(route.Addr(), route.Bits()+1).Masked()
	rightAddress := left.Addr().As4()
	byteIndex := route.Bits() / 8
	rightAddress[byteIndex] |= 1 << (7 - (route.Bits() % 8))
	right := netip.PrefixFrom(netip.AddrFrom4(rightAddress), route.Bits()+1)
	return append(manualSubtractIPv4Prefix(left, exclusion), manualSubtractIPv4Prefix(right, exclusion)...)
}

type listResponse struct {
	OK              bool `json:"ok"`
	ProtocolVersion int  `json:"protocol_version,omitempty"`
	Profile         *struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"profile,omitempty"`
	Profiles []struct {
		ID string `json:"id"`
	} `json:"profiles"`
}

func daemonManualRoutePlanStart(socket string) error {
	return daemonManualRoutePlanStartRequest(socket, manualRoutePlanRequest)
}

func daemonManualRoutePlanStop(socket string) error {
	return daemonManualRoutePlanStopRequest(socket, manualRoutePlanProfileID)
}

func daemonManualRoutePlanStartTwo(socket string) error {
	return daemonManualRoutePlanStartRequest(socket, manualRoutePlanRequestTwo)
}

func daemonManualRoutePlanStopTwo(socket string) error {
	return daemonManualRoutePlanStopRequest(socket, manualRoutePlanProfileIDTwo)
}

func daemonManualRoutePlanStartThree(socket string) error {
	return daemonManualRoutePlanStartRequest(socket, manualRoutePlanRequestThree)
}

func daemonManualRoutePlanStopThree(socket string) error {
	return daemonManualRoutePlanStopRequest(socket, manualRoutePlanProfileIDThree)
}

func daemonManualRoutePlanAssertProfiles(socket string, expected ...string) error {
	if err := daemonHello(socket); err != nil {
		return err
	}
	response, err := daemonRequest(socket, "list")
	if err != nil || len(response.Profiles) != len(expected) {
		return errors.New("daemon synthetic route-plan session set changed")
	}
	actual := make([]string, 0, len(response.Profiles))
	for _, profile := range response.Profiles {
		actual = append(actual, profile.ID)
	}
	sort.Strings(actual)
	sort.Strings(expected)
	for index := range expected {
		if actual[index] != expected[index] {
			return errors.New("daemon synthetic route-plan session set changed")
		}
	}
	return nil
}

func daemonManualRoutePlanStartRequest(socket string, request struct {
	Version   int             `json:"version"`
	Operation string          `json:"operation"`
	ProfileID string          `json:"profile_id"`
	Config    string          `json:"config,omitempty"`
	RoutePlan json.RawMessage `json:"route_plan,omitempty"`
}) error {
	if err := daemonHello(socket); err != nil {
		return err
	}
	response, err := daemonExchange(socket, request)
	if err != nil || response.Profile == nil || response.Profile.ID != request.ProfileID || response.Profile.Status != "running" {
		return errors.New("daemon synthetic route-plan start failed")
	}
	return nil
}

func daemonManualRoutePlanStopRequest(socket, profileID string) error {
	if err := daemonHello(socket); err != nil {
		return err
	}
	response, err := daemonExchange(socket, struct {
		Version   int    `json:"version"`
		Operation string `json:"operation"`
		ProfileID string `json:"profile_id"`
	}{Version: 1, Operation: "stop", ProfileID: profileID})
	if err != nil || response.Profile == nil || response.Profile.ID != profileID || response.Profile.Status != "stopped" {
		return errors.New("daemon synthetic route-plan stop failed")
	}
	return nil
}

func daemonHello(socket string) error {
	hello, err := daemonRequest(socket, "hello")
	if err != nil || hello.ProtocolVersion != 1 || hello.Profile != nil || len(hello.Profiles) != 0 {
		return errors.New("daemon hello failed")
	}
	return nil
}

// daemonIsIdle sends only the existing list request and never prints profile IDs.
func daemonIsIdle(socket string) error {
	response, err := daemonRequest(socket, "list")
	if err != nil {
		return err
	}
	if len(response.Profiles) != 0 {
		return errors.New("daemon has active sessions")
	}
	return nil
}

// daemonRequest sends only a fixed, keyless control operation.
func daemonRequest(socket, operation string) (listResponse, error) {
	return daemonExchange(socket, struct {
		Version   int    `json:"version"`
		Operation string `json:"operation"`
	}{Version: 1, Operation: operation})
}

func daemonExchange(socket string, request any) (listResponse, error) {
	if !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || len(socket) > 103 {
		return listResponse{}, errors.New("invalid control socket path")
	}
	connection, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return listResponse{}, errors.New("daemon control socket is unavailable")
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return listResponse{}, err
	}
	frame, err := json.Marshal(request)
	if err != nil {
		return listResponse{}, err
	}
	if err := writeFrame(connection, frame); err != nil {
		return listResponse{}, errors.New("daemon control request failed")
	}
	responseFrame, err := readFrame(connection)
	if err != nil {
		return listResponse{}, errors.New("daemon control response failed")
	}
	var response listResponse
	if err := json.Unmarshal(responseFrame, &response); err != nil || !response.OK {
		return listResponse{}, errors.New("daemon control rejected control operation")
	}
	return response, nil
}

func writeFrame(writer io.Writer, frame []byte) error {
	if len(frame) == 0 || len(frame) > maxFrameBytes {
		return errors.New("invalid request frame")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(frame)))
	if _, err := writer.Write(header[:]); err != nil {
		return err
	}
	_, err := writer.Write(frame)
	return err
}

func readFrame(reader io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > maxFrameBytes {
		return nil, errors.New("invalid response frame")
	}
	frame := make([]byte, length)
	_, err := io.ReadFull(reader, frame)
	return frame, err
}

func cleanup(server *daemoncontrol.Server) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if err = server.Close(); err == nil {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return err
}

func parseUID(value string) (uint32, error) {
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil || parsed == 0 {
		return 0, errors.New("-uid must be a non-root unsigned 32-bit integer")
	}
	return uint32(parsed), nil
}
