// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

const installedDiagnosticLogPath = "/private/var/db/amneziawg-daemon-control-poc/diagnostics.jsonl"
const diagnosticLogLimit int64 = 512 * 1024

// boundedDiagnosticLog keeps the opt-in local file small. Its fixed path is
// inside the root-owned daemon directory; callers cannot choose a file path.
type boundedDiagnosticLog struct {
	mu   sync.Mutex
	file *os.File
	size int64
}

func openBoundedDiagnosticLog(path string) (*boundedDiagnosticLog, error) {
	if path != installedDiagnosticLogPath {
		return nil, errors.New("invalid diagnostics log path")
	}
	directory := filepath.Dir(path)
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&022 != 0 {
		return nil, errors.New("unsafe diagnostics directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return nil, errors.New("unsafe diagnostics directory")
	}
	if info, err := os.Lstat(path); err == nil {
		fileStat, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !ok || fileStat.Uid != 0 || fileStat.Gid != 0 || info.Mode().Perm() != 0600 {
			return nil, errors.New("unsafe diagnostics log")
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("cannot inspect diagnostics log")
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, errors.New("cannot open diagnostics log")
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return nil, errors.New("cannot secure diagnostics log")
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("unsafe diagnostics log")
	}
	return &boundedDiagnosticLog{file: file, size: info.Size()}, nil
}

func (log *boundedDiagnosticLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.file == nil || int64(len(data)) > diagnosticLogLimit {
		return 0, io.ErrShortWrite
	}
	if log.size+int64(len(data)) > diagnosticLogLimit {
		if err := log.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := log.file.Write(data)
	log.size += int64(n)
	return n, err
}

func (log *boundedDiagnosticLog) Close() error {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.file == nil {
		return nil
	}
	err := log.file.Close()
	log.file = nil
	return err
}

func (log *boundedDiagnosticLog) rotate() error {
	path := log.file.Name()
	if err := log.file.Close(); err != nil {
		return err
	}
	if err := os.Remove(path + ".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(path, path+".1"); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	log.file, log.size = file, 0
	return nil
}
