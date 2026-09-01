package tor

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
)

type processSpec struct {
	path       string
	configPath string
	workingDir string
	outputPath string
}

type managedProcess interface {
	Wait() error
	Interrupt() error
	Kill() error
}

type processStarter func(processSpec) (managedProcess, error)

type readinessSpec struct {
	bootstrapLogPath string
	socksAddress     string
	probeAddress     string
	portProbe        func(context.Context, string) error
	socksProbe       func(context.Context, string, string) error
}

type readinessWaiter func(context.Context, readinessSpec) error
type backoffWaiter func(context.Context, time.Duration) error
type readinessProbeFunc func(context.Context, string, string) error

var (
	errBootstrapFailed = errors.New("bootstrap_failed")
	errSOCKSProbe      = errors.New("socks_probe_failed")
)

type osManagedProcess struct{ command *exec.Cmd }

func startOSProcess(spec processSpec) (managedProcess, error) {
	output, err := os.OpenFile(spec.outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	command := exec.Command(spec.path, "-f", spec.configPath)
	command.Dir = spec.workingDir
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		_ = output.Close()
		return nil, err
	}
	_ = output.Close()
	return &osManagedProcess{command: command}, nil
}

func (p *osManagedProcess) Wait() error { return p.command.Wait() }

func (p *osManagedProcess) Interrupt() error {
	err := p.command.Process.Signal(os.Interrupt)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (p *osManagedProcess) Kill() error {
	err := p.command.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func waitForReadiness(ctx context.Context, spec readinessSpec) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		ready, err := readinessChecks(ctx, spec)
		if err == nil && ready {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastErr
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func readinessProbe(ctx context.Context, spec readinessSpec) (bool, error) {
	return readinessChecks(ctx, spec)
}

func readinessChecks(ctx context.Context, spec readinessSpec) (bool, error) {
	contents, err := os.ReadFile(spec.bootstrapLogPath)
	if err != nil || !strings.Contains(string(contents), "Bootstrapped 100%") {
		return false, errBootstrapFailed
	}
	portProbe := spec.portProbe
	if portProbe == nil {
		portProbe = probeTCPPort
	}
	if err := portProbe(ctx, spec.socksAddress); err != nil {
		return false, errSOCKSProbe
	}
	if spec.probeAddress == "" {
		return false, errSOCKSProbe
	}
	socksProbe := spec.socksProbe
	if socksProbe == nil {
		socksProbe = probeSOCKSRoute
	}
	if err := socksProbe(ctx, spec.socksAddress, spec.probeAddress); err != nil {
		return false, errSOCKSProbe
	}
	return true, nil
}

func probeSOCKSRoute(ctx context.Context, socksAddress, probeAddress string) error {
	if socksAddress == "" || probeAddress == "" {
		return errSOCKSProbe
	}
	dialer, err := xproxy.SOCKS5("tcp", socksAddress, nil, &net.Dialer{Timeout: 2 * time.Second})
	if err != nil {
		return err
	}
	contextDialer, ok := dialer.(xproxy.ContextDialer)
	if !ok {
		return errors.New("SOCKS dialer does not support context")
	}
	connection, err := contextDialer.DialContext(ctx, "tcp", probeAddress)
	if err != nil {
		return err
	}
	return connection.Close()
}

func probeTCPPort(ctx context.Context, address string) error {
	portProbe := net.Dialer{Timeout: 500 * time.Millisecond}
	connection, err := portProbe.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	return connection.Close()
}

func waitForBackoff(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
