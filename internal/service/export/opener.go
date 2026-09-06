package export

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
)

type CommandRunner interface {
	Run(ctx context.Context, command string, args ...string) error
}

type OSFileOpener struct {
	exportPath string
	goos       string
	runner     CommandRunner
}

func NewOSFileOpener(exportPath string) *OSFileOpener {
	return NewOSFileOpenerWithRunner(exportPath, runtime.GOOS, execCommandRunner{})
}

func NewOSFileOpenerWithRunner(exportPath, goos string, runner CommandRunner) *OSFileOpener {
	return &OSFileOpener{exportPath: exportPath, goos: goos, runner: runner}
}

func (o *OSFileOpener) Open(ctx context.Context, path string) error {
	if o == nil || o.runner == nil || o.exportPath == "" {
		return errors.New("file opener dependencies are required")
	}
	if path != o.exportPath {
		return errors.New("only the configured keyword export path may be opened")
	}
	var command string
	switch o.goos {
	case "darwin":
		command = "/usr/bin/open"
	case "linux":
		command = "/usr/bin/xdg-open"
	default:
		return fmt.Errorf("unsupported operating system %q", o.goos)
	}
	if err := o.runner.Run(ctx, command, path); err != nil {
		return fmt.Errorf("open keyword export: %w", err)
	}
	return nil
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, command string, args ...string) error {
	return exec.CommandContext(ctx, command, args...).Run()
}
