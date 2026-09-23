// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/renosaza/amneziawg-daemon-control-poc"
)

func main() {
	socket := flag.String("socket", "", "root-owned directory socket path")
	uidText := flag.String("uid", "", "authorized non-root macOS UID")
	flag.Parse()
	if flag.NArg() != 0 || *socket == "" || *uidText == "" {
		fmt.Fprintln(os.Stderr, "usage: daemon-control-poc -socket /var/run/name.sock -uid <macOS-uid>")
		os.Exit(2)
	}
	uid, err := parseUID(*uidText)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	server, err := daemoncontrol.NewServer(uid)
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
		os.Exit(1)
	}
}

func parseUID(value string) (uint32, error) {
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil || parsed == 0 {
		return 0, errors.New("-uid must be a non-root unsigned 32-bit integer")
	}
	return uint32(parsed), nil
}
