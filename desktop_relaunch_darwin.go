//go:build desktop && darwin

package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	desktopRelaunchMarkerEnvironment = "_TELEGRAM_COMPANION_RELAUNCH_HANDOFF"
	desktopRelaunchMarkerVersion     = "v1:"
	desktopRelaunchTokenBytes        = 32
)

type desktopRelaunchCoordinator struct {
	mu       sync.Mutex
	prepared bool
	writer   *os.File

	executable     func() (string, error)
	arguments      func() []string
	environment    func() []string
	random         io.Reader
	pipe           func() (*os.File, *os.File, error)
	startProcess   func(string, []string, *os.ProcAttr) (*os.Process, error)
	releaseProcess func(*os.Process) error
}

func newDesktopRelaunchCoordinator() *desktopRelaunchCoordinator {
	return &desktopRelaunchCoordinator{}
}

func (r *desktopRelaunchCoordinator) Request(context.Context) error {
	if r == nil {
		return errors.New("desktop relaunch is unavailable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.prepared {
		return nil
	}

	executable := r.executable
	if executable == nil {
		executable = os.Executable
	}
	path, err := executable()
	if err != nil || strings.TrimSpace(path) == "" {
		return errors.New("desktop executable is unavailable")
	}
	pipe := r.pipe
	if pipe == nil {
		pipe = os.Pipe
	}
	reader, writer, err := pipe()
	if err != nil {
		return errors.New("desktop relaunch pipe is unavailable")
	}
	closePipe := func() {
		_ = reader.Close()
		_ = writer.Close()
	}

	random := r.random
	if random == nil {
		random = rand.Reader
	}
	token := make([]byte, desktopRelaunchTokenBytes)
	if _, err := io.ReadFull(random, token); err != nil {
		closePipe()
		return errors.New("desktop relaunch marker is unavailable")
	}
	if err := writeDesktopRelaunchToken(writer, token); err != nil {
		closePipe()
		return errors.New("desktop relaunch pipe is unavailable")
	}

	arguments := r.arguments
	if arguments == nil {
		arguments = func() []string { return os.Args }
	}
	environment := r.environment
	if environment == nil {
		environment = os.Environ
	}
	startProcess := r.startProcess
	if startProcess == nil {
		startProcess = os.StartProcess
	}
	process, err := startProcess(path, arguments(), &os.ProcAttr{
		Dir: filepath.Dir(path),
		Env: desktopRelaunchChildEnvironment(environment(), desktopRelaunchMarker(token)),
		Files: []*os.File{
			os.Stdin,
			os.Stdout,
			os.Stderr,
			reader,
		},
	})
	if err != nil || process == nil {
		closePipe()
		return errors.New("desktop relaunch failed")
	}

	_ = reader.Close()
	r.writer = writer
	r.prepared = true
	releaseProcess := r.releaseProcess
	if releaseProcess == nil {
		releaseProcess = (*os.Process).Release
	}
	_ = releaseProcess(process)
	return nil
}

func (r *desktopRelaunchCoordinator) Release() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	writer := r.writer
	r.writer = nil
	r.mu.Unlock()
	if writer == nil {
		return nil
	}
	if err := writer.Close(); err != nil {
		return errors.New("desktop relaunch handoff release failed")
	}
	return nil
}

func awaitDesktopRelaunchHandoff() error {
	marker, present := os.LookupEnv(desktopRelaunchMarkerEnvironment)
	if !present {
		return nil
	}
	if err := os.Unsetenv(desktopRelaunchMarkerEnvironment); err != nil {
		return errors.New("desktop relaunch marker could not be removed")
	}
	if _, err := parseDesktopRelaunchMarker(marker); err != nil {
		return err
	}
	if err := validateDesktopRelaunchDescriptor(uintptr(3)); err != nil {
		return err
	}
	handoff := os.NewFile(uintptr(3), "desktop-relaunch-handoff")
	if handoff == nil {
		return errors.New("desktop relaunch handoff is unavailable")
	}
	defer handoff.Close()
	return awaitDesktopRelaunchHandoffFrom(marker, handoff)
}

func validateDesktopRelaunchDescriptor(descriptor uintptr) error {
	var status unix.Stat_t
	if err := unix.Fstat(int(descriptor), &status); err != nil || status.Mode&unix.S_IFMT != unix.S_IFIFO {
		return errors.New("desktop relaunch handoff descriptor is invalid")
	}
	return nil
}

func awaitDesktopRelaunchHandoffFrom(marker string, handoff *os.File) error {
	token, err := parseDesktopRelaunchMarker(marker)
	if err != nil {
		return err
	}
	if handoff == nil {
		return errors.New("desktop relaunch handoff is unavailable")
	}
	info, err := handoff.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return errors.New("desktop relaunch handoff descriptor is invalid")
	}
	received := make([]byte, desktopRelaunchTokenBytes)
	if _, err := io.ReadFull(handoff, received); err != nil {
		return errors.New("desktop relaunch handoff marker is invalid")
	}
	if subtle.ConstantTimeCompare(received, token) != 1 {
		return errors.New("desktop relaunch handoff marker is invalid")
	}
	if _, err := io.Copy(io.Discard, handoff); err != nil {
		return errors.New("desktop relaunch handoff wait failed")
	}
	return nil
}

func desktopRelaunchMarker(token []byte) string {
	return desktopRelaunchMarkerVersion + base64.RawURLEncoding.EncodeToString(token)
}

func parseDesktopRelaunchMarker(marker string) ([]byte, error) {
	if !strings.HasPrefix(marker, desktopRelaunchMarkerVersion) {
		return nil, errors.New("desktop relaunch marker is invalid")
	}
	token, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(marker, desktopRelaunchMarkerVersion))
	if err != nil || len(token) != desktopRelaunchTokenBytes {
		return nil, errors.New("desktop relaunch marker is invalid")
	}
	return token, nil
}

func desktopRelaunchChildEnvironment(environment []string, marker string) []string {
	prefix := desktopRelaunchMarkerEnvironment + "="
	child := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		child = append(child, entry)
	}
	return append(child, prefix+marker)
}

func writeDesktopRelaunchToken(writer io.Writer, token []byte) error {
	for len(token) > 0 {
		written, err := writer.Write(token)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		token = token[written:]
	}
	return nil
}
