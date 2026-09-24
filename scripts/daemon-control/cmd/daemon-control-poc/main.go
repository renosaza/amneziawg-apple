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
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/renosaza/amneziawg-daemon-control-poc"
)

const maxFrameBytes = 4 * 1024

const manualRoutePlanProfileID = "11111111-2222-4333-8444-555555555555"
const manualRoutePlanProfileIDTwo = "22222222-3333-4444-8555-666666666666"

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

func main() {
	socket := flag.String("socket", "", "root-owned directory socket path")
	uidText := flag.String("uid", "", "authorized non-root macOS UID")
	binaryPath := flag.String("binary", "", "trusted absolute amneziawg-go path")
	allowRoutePlans := flag.Bool("allow-route-plan-runtime", false, "enable the experimental root-controlled route-plan runtime")
	checkIdle := flag.Bool("check-idle", false, "exit successfully only when the daemon has no sessions")
	prepareStop := flag.Bool("prepare-stop", false, "atomically refuse new sessions when the daemon is idle")
	manualRoutePlanStart := flag.Bool("manual-route-plan-start", false, "send the fixed synthetic route-plan start request")
	manualRoutePlanStop := flag.Bool("manual-route-plan-stop", false, "stop the fixed synthetic route-plan session")
	manualRoutePlanStartTwo := flag.Bool("manual-route-plan-start-two", false, "send the second fixed synthetic route-plan start request")
	manualRoutePlanStopTwo := flag.Bool("manual-route-plan-stop-two", false, "stop the second fixed synthetic route-plan session")
	flag.Parse()
	if *checkIdle || *prepareStop || *manualRoutePlanStart || *manualRoutePlanStop || *manualRoutePlanStartTwo || *manualRoutePlanStopTwo {
		if flag.NArg() != 0 || *socket == "" || *uidText != "" || *binaryPath != "" {
			fmt.Fprintln(os.Stderr, "usage: daemon-control-poc -check-idle|-prepare-stop|-manual-route-plan-start|-manual-route-plan-stop|-manual-route-plan-start-two|-manual-route-plan-stop-two -socket /var/run/name.sock")
			os.Exit(2)
		}
		operations := 0
		for _, selected := range []bool{*checkIdle, *prepareStop, *manualRoutePlanStart, *manualRoutePlanStop, *manualRoutePlanStartTwo, *manualRoutePlanStopTwo} {
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
	uid, err := parseUID(*uidText)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	backend, err := daemoncontrol.NewTunnelBackendWithRoutePlanRuntime(*binaryPath, *allowRoutePlans)
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
