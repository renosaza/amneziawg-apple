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

func main() {
	socket := flag.String("socket", "", "root-owned directory socket path")
	uidText := flag.String("uid", "", "authorized non-root macOS UID")
	binaryPath := flag.String("binary", "", "trusted absolute amneziawg-go path")
	checkIdle := flag.Bool("check-idle", false, "exit successfully only when the daemon has no sessions")
	prepareStop := flag.Bool("prepare-stop", false, "atomically refuse new sessions when the daemon is idle")
	flag.Parse()
	if *checkIdle || *prepareStop {
		if flag.NArg() != 0 || *socket == "" || *uidText != "" || *binaryPath != "" {
			fmt.Fprintln(os.Stderr, "usage: daemon-control-poc -check-idle|-prepare-stop -socket /var/run/name.sock")
			os.Exit(2)
		}
		if *checkIdle == *prepareStop {
			fmt.Fprintln(os.Stderr, "choose exactly one control operation")
			os.Exit(2)
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
	backend, err := daemoncontrol.NewTunnelBackend(*binaryPath)
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
	OK       bool `json:"ok"`
	Profiles []struct {
		ID string `json:"id"`
	} `json:"profiles"`
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
	request, err := json.Marshal(struct {
		Version   int    `json:"version"`
		Operation string `json:"operation"`
	}{Version: 1, Operation: operation})
	if err != nil {
		return listResponse{}, err
	}
	if err := writeFrame(connection, request); err != nil {
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
