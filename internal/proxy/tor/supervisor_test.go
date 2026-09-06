package tor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewWritesPrivateLoopbackIsolatedConfiguration(t *testing.T) {
	root := t.TempDir()
	torPath := executableFixture(t, root, "tor")
	snowflakePath := executableFixture(t, filepath.Join(root, "Resources", "bin"), "snowflake-client")
	stateDir := filepath.Join(root, "private state")

	supervisor, err := New(Config{
		StateDir:          stateDir,
		TorPath:           torPath,
		SnowflakePath:     snowflakePath,
		transportAliasDir: filepath.Join(root, "transport-aliases"),
	}, discardLogger())

	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:19050", supervisor.SOCKSAddress())
	for _, path := range []string{stateDir, filepath.Join(stateDir, "data")} {
		info, statErr := os.Stat(path)
		require.NoError(t, statErr)
		if runtime.GOOS != "windows" {
			require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
		}
	}
	torrcPath := filepath.Join(stateDir, "torrc")
	info, err := os.Stat(torrcPath)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	for _, name := range []string{"bootstrap.log", "tor-output.log"} {
		info, statErr := os.Stat(filepath.Join(stateDir, name))
		require.NoError(t, statErr)
		if runtime.GOOS != "windows" {
			require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		}
	}
	contents, err := os.ReadFile(torrcPath)
	require.NoError(t, err)
	config := string(contents)
	require.Contains(t, config, "SocksPort 127.0.0.1:19050 IsolateSOCKSAuth")
	transportPath := snowflakeTransportPathFromTorrc(t, config)
	require.NotContains(t, transportPath, " ")
	resolvedTransportPath, err := filepath.EvalSymlinks(transportPath)
	require.NoError(t, err)
	resolvedSnowflakePath, err := filepath.EvalSymlinks(snowflakePath)
	require.NoError(t, err)
	require.Equal(t, resolvedSnowflakePath, resolvedTransportPath)
	require.Contains(t, config, "UseBridges 1")
	require.Contains(t, config, "Bridge snowflake ")
	require.NotContains(t, config, "0.0.0.0")
}

func TestNewDiscoversLyrebirdAndRendersObfs4Candidate(t *testing.T) {
	root := t.TempDir()
	torPath := executableFixture(t, root, "tor")
	lyrebirdPath := executableFixture(t, filepath.Join(root, "pt"), "lyrebird")
	snowflakePath := executableFixture(t, root, "snowflake-client")
	bridgePath := writeOfficialShapeFixture(t, []string{sevenObfs4Lines()[0]}, nil)
	stateDir := filepath.Join(root, "state")
	supervisor, err := New(Config{
		StateDir:         stateDir,
		TorPath:          torPath,
		LyrebirdPath:     lyrebirdPath,
		SnowflakePath:    snowflakePath,
		BridgeConfigPath: bridgePath,
	}, discardLogger())

	require.NoError(t, err)
	require.Equal(t, lyrebirdPath, supervisor.config.lyrebirdPath)
	require.Len(t, supervisor.config.bridgeCandidates, 1)
	require.Equal(t, 8, supervisor.config.maxBridgeAttempts)
	contents, err := os.ReadFile(filepath.Join(stateDir, "torrc"))
	require.NoError(t, err)
	torrc := string(contents)
	require.Equal(t, 1, strings.Count(torrc, "ClientTransportPlugin "))
	require.Contains(t, torrc, "ClientTransportPlugin obfs4 exec ")
	require.Equal(t, 1, strings.Count(torrc, "Bridge obfs4 "))
	require.NotContains(t, torrc, "\\nSocksPort")
}

func TestNewUsesLyrebirdForMappedSnowflakeWithoutStandaloneClient(t *testing.T) {
	root := t.TempDir()
	torPath := executableFixture(t, root, "tor")
	lyrebirdPath := executableFixture(t, root, "lyrebird")
	bundlePath := writeOfficialShapeFixture(t, sevenObfs4Lines(), twoSnowflakeLines())
	supervisor, err := New(Config{
		StateDir: t.TempDir(), TorPath: torPath, LyrebirdPath: lyrebirdPath,
		BridgeConfigPath: bundlePath,
	}, discardLogger())
	require.NoError(t, err)
	require.Empty(t, supervisor.config.snowflakePath)
	torrc, err := renderTorrc(supervisor.config, supervisor.config.bridgeCandidates[8])
	require.NoError(t, err)
	require.Contains(t, torrc, "ClientTransportPlugin snowflake exec "+lyrebirdPath)
}

func TestNewRejectsUnavailableLyrebirdWithoutLeakingPath(t *testing.T) {
	root := t.TempDir()
	torPath := executableFixture(t, root, "tor")
	snowflakePath := executableFixture(t, root, "snowflake-client")
	bridgePath := writeOfficialShapeFixture(t, []string{sevenObfs4Lines()[0]}, nil)
	lyrebirdPath := filepath.Join(root, "private", "missing-lyrebird")

	_, err := New(Config{
		StateDir:         filepath.Join(root, "state"),
		TorPath:          torPath,
		LyrebirdPath:     lyrebirdPath,
		SnowflakePath:    snowflakePath,
		BridgeConfigPath: bridgePath,
	}, discardLogger())

	require.ErrorContains(t, err, "pt_binary_unavailable")
	require.NotContains(t, err.Error(), lyrebirdPath)
}

func TestNewRejectsLyrebirdWithoutApprovedBridgeConfig(t *testing.T) {
	root := t.TempDir()
	_, err := New(Config{
		StateDir:      filepath.Join(root, "state"),
		TorPath:       executableFixture(t, root, "tor"),
		LyrebirdPath:  executableFixture(t, root, "lyrebird"),
		SnowflakePath: executableFixture(t, root, "snowflake-client"),
	}, discardLogger())

	require.ErrorContains(t, err, "bridge_config_invalid")
}

func TestRenderTorrcRejectsBridgeControlCharacters(t *testing.T) {
	config := resolvedConfig{
		stateDir:      `C:\\private state`,
		dataDir:       `C:\\private state\\data`,
		socksAddress:  DefaultSOCKSAddress,
		lyrebirdPath:  `C:\\pt\\lyrebird`,
		snowflakePath: `C:\\pt\\snowflake-client`,
	}
	_, err := renderTorrc(config, BridgeCandidate{Transport: TransportObfs4, Canonical: "obfs4 203.0.113.10:443 ABCDEF0123456789ABCDEF0123456789ABCDEF01 cert=YWJjZA== iat-mode=0\nSocksPort 0"})
	require.Error(t, err)
}

func TestNewUsesWhitespaceFreeAliasForSnowflakeExecutablePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit the quote character in executable paths")
	}
	root := t.TempDir()
	torPath := executableFixture(t, root, "tor")
	snowflakePath := executableFixture(t, filepath.Join(root, `with "quotes" and\slashes`), "snowflake-client")
	stateDir := filepath.Join(root, "state")

	_, err := New(Config{
		StateDir:          stateDir,
		TorPath:           torPath,
		SnowflakePath:     snowflakePath,
		transportAliasDir: filepath.Join(root, "transport-aliases"),
	}, discardLogger())

	require.NoError(t, err)
	contents, err := os.ReadFile(filepath.Join(stateDir, "torrc"))
	require.NoError(t, err)
	transportPath := snowflakeTransportPathFromTorrc(t, string(contents))
	require.NotContains(t, transportPath, " ")
	resolvedTransportPath, err := filepath.EvalSymlinks(transportPath)
	require.NoError(t, err)
	resolvedSnowflakePath, err := filepath.EvalSymlinks(snowflakePath)
	require.NoError(t, err)
	require.Equal(t, resolvedSnowflakePath, resolvedTransportPath)
}

func TestNewRejectsSnowflakeExecutablePathWithTorrcControlCharacters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("control-character executable fixture is POSIX-specific")
	}
	root := t.TempDir()
	torPath := executableFixture(t, root, "tor")
	snowflakePath := executableFixture(t, root, "snowflake-client\nSocksPort 0.0.0.0:1")
	stateDir := filepath.Join(root, "state")

	_, err := New(Config{
		StateDir:      stateDir,
		TorPath:       torPath,
		SnowflakePath: snowflakePath,
	}, discardLogger())

	require.ErrorContains(t, err, "snowflake executable path is invalid for Tor configuration")
	require.NotContains(t, err.Error(), snowflakePath)
	require.NoFileExists(t, filepath.Join(stateDir, "torrc"))
}

func TestNewAliasesSnowflakeExecutablePathFromEnvironmentOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit the symlink fixture used by this regression")
	}
	root := t.TempDir()
	torPath := executableFixture(t, root, "tor")
	snowflakePath := executableFixture(t, filepath.Join(root, "override with spaces"), "snowflake-client")
	stateDir := filepath.Join(root, "state")
	t.Setenv("TELEGRAM_COMPANION_SNOWFLAKE_PATH", snowflakePath)

	_, err := New(Config{
		StateDir:          stateDir,
		TorPath:           torPath,
		transportAliasDir: filepath.Join(root, "transport-aliases"),
	}, discardLogger())

	require.NoError(t, err)
	contents, err := os.ReadFile(filepath.Join(stateDir, "torrc"))
	require.NoError(t, err)
	transportPath := snowflakeTransportPathFromTorrc(t, string(contents))
	resolvedTransportPath, err := filepath.EvalSymlinks(transportPath)
	require.NoError(t, err)
	resolvedSnowflakePath, err := filepath.EvalSymlinks(snowflakePath)
	require.NoError(t, err)
	require.Equal(t, resolvedSnowflakePath, resolvedTransportPath)
}

func TestNewAliasesTorCommentCharactersInSnowflakeExecutablePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable path regression")
	}
	root := t.TempDir()
	torPath := executableFixture(t, root, "tor")
	snowflakePath := executableFixture(t, filepath.Join(root, "Telegram#QA"), "snowflake-client")
	stateDir := filepath.Join(root, "state")

	_, err := New(Config{
		StateDir:          stateDir,
		TorPath:           torPath,
		SnowflakePath:     snowflakePath,
		transportAliasDir: filepath.Join(root, "transport-aliases"),
	}, discardLogger())

	require.NoError(t, err)
	contents, err := os.ReadFile(filepath.Join(stateDir, "torrc"))
	require.NoError(t, err)
	transportPath := snowflakeTransportPathFromTorrc(t, string(contents))
	require.NotContains(t, transportPath, "#")
	resolvedTransportPath, err := filepath.EvalSymlinks(transportPath)
	require.NoError(t, err)
	resolvedSnowflakePath, err := filepath.EvalSymlinks(snowflakePath)
	require.NoError(t, err)
	require.Equal(t, resolvedSnowflakePath, resolvedTransportPath)
}

func TestNewRejectsSymlinkedTransportAliasDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink regression")
	}
	root := t.TempDir()
	torPath := executableFixture(t, root, "tor")
	snowflakePath := executableFixture(t, filepath.Join(root, "Telegram Companion.app"), "snowflake-client")
	stateDir := filepath.Join(root, "state")
	victimDir := filepath.Join(root, "victim")
	require.NoError(t, os.Mkdir(victimDir, 0o755))
	aliasDir := filepath.Join(root, "transport-aliases")
	require.NoError(t, os.Symlink(victimDir, aliasDir))

	_, err := New(Config{
		StateDir:          stateDir,
		TorPath:           torPath,
		SnowflakePath:     snowflakePath,
		transportAliasDir: aliasDir,
	}, discardLogger())

	require.ErrorContains(t, err, "snowflake executable path is invalid for Tor configuration")
	info, statErr := os.Stat(victimDir)
	require.NoError(t, statErr)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	require.NoFileExists(t, filepath.Join(victimDir, "snowflake-client"))
}

func TestRunRestoresRemovedManagedTransportAliasBeforeStartingTor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink regression")
	}
	root := t.TempDir()
	torPath := executableFixture(t, root, "tor")
	snowflakePath := executableFixture(t, filepath.Join(root, "Telegram Companion.app"), "snowflake-client")
	stateDir := filepath.Join(root, "state")
	process := newFakeProcess()
	aliasStatus := make(chan error, 1)
	var aliasPath string
	config := Config{
		StateDir:          stateDir,
		TorPath:           torPath,
		SnowflakePath:     snowflakePath,
		transportAliasDir: filepath.Join(root, "transport-aliases"),
		BootstrapTimeout:  time.Second,
		StopTimeout:       time.Second,
	}
	config.startProcess = func(processSpec) (managedProcess, error) {
		_, err := os.Stat(aliasPath)
		aliasStatus <- err
		return process, nil
	}
	config.waitReady = func(context.Context, readinessSpec) error { return nil }
	supervisor, err := New(config, discardLogger())
	require.NoError(t, err)
	contents, err := os.ReadFile(filepath.Join(stateDir, "torrc"))
	require.NoError(t, err)
	aliasPath = snowflakeTransportPathFromTorrc(t, string(contents))
	require.NoError(t, os.Remove(aliasPath))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()

	require.NoError(t, <-aliasStatus)
	require.NoError(t, supervisor.WaitReady(context.Background()))
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func snowflakeTransportPathFromTorrc(t *testing.T, contents string) string {
	t.Helper()
	for _, line := range strings.Split(contents, "\n") {
		if !strings.HasPrefix(line, "ClientTransportPlugin snowflake exec ") {
			continue
		}
		fields := strings.Fields(line)
		require.Len(t, fields, 4, "managed transport executable must be one Tor token")
		return fields[3]
	}
	require.FailNow(t, "snowflake managed transport line not found")
	return ""
}

func TestNewDiscoversBundledMacExecutablesBeforePATH(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TELEGRAM_COMPANION_TOR_PATH", "")
	t.Setenv("TELEGRAM_COMPANION_SNOWFLAKE_PATH", "")
	resources := filepath.Join(root, "Resources")
	bundledTor := executableFixture(t, filepath.Join(resources, "bin"), "tor")
	bundledSnowflake := executableFixture(t, filepath.Join(resources, "bin"), "snowflake-client")
	pathDir := filepath.Join(root, "path")
	_ = executableFixture(t, pathDir, "tor")
	_ = executableFixture(t, pathDir, "snowflake-client")
	t.Setenv("PATH", pathDir)

	supervisor, err := New(Config{StateDir: filepath.Join(root, "state"), ResourcesDir: resources,
		transportAliasDir: filepath.Join(root, "transport-aliases")}, discardLogger())

	require.NoError(t, err)
	require.Equal(t, bundledTor, supervisor.config.torPath)
	require.Equal(t, bundledSnowflake, supervisor.config.snowflakePath)
}

func TestNewRejectsMissingExecutableWithoutLeakingPath(t *testing.T) {
	root := t.TempDir()
	privatePath := filepath.Join(root, "private", "missing-tor")
	_, err := New(Config{
		StateDir:      filepath.Join(root, "state"),
		TorPath:       privatePath,
		SnowflakePath: executableFixture(t, root, "snowflake-client"),
	}, discardLogger())

	require.ErrorContains(t, err, "tor executable unavailable")
	require.NotContains(t, err.Error(), privatePath)
}

func TestRunBecomesReadyAndStopsOwnedProcessGracefully(t *testing.T) {
	process := newFakeProcess()
	config := lifecycleConfig(t)
	config.startProcess = func(processSpec) (managedProcess, error) { return process, nil }
	config.waitReady = func(context.Context, readinessSpec) error { return nil }
	supervisor, err := New(config, discardLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	require.NoError(t, supervisor.WaitReady(context.Background()))
	require.Equal(t, StateReady, supervisor.Status().State)

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.Equal(t, 1, process.interrupts())
	require.Equal(t, 0, process.kills())
	require.Equal(t, StateStopped, supervisor.Status().State)
}

func TestRunReportsSanitizedStartupFailure(t *testing.T) {
	config := lifecycleConfig(t)
	config.startProcess = func(processSpec) (managedProcess, error) {
		return nil, errors.New("open /home/operator/private/session: permission denied")
	}
	supervisor, err := New(config, discardLogger())
	require.NoError(t, err)

	runErr := supervisor.Run(context.Background())

	require.ErrorContains(t, runErr, "start managed Tor")
	require.NotContains(t, runErr.Error(), "/home/operator")
	status := supervisor.Status()
	require.Equal(t, StateError, status.State)
	require.Equal(t, "proxy_start_failed", status.LastError)
	require.NotContains(t, status.LastError, "/home/operator")
	require.ErrorContains(t, supervisor.WaitReady(context.Background()), "start managed Tor")
}

func TestRunRetriesBootstrapTimeoutUntilParentStops(t *testing.T) {
	process := newFakeProcess()
	config := lifecycleConfig(t)
	config.startProcess = func(processSpec) (managedProcess, error) { return process, nil }
	config.waitReady = func(context.Context, readinessSpec) error {
		return context.DeadlineExceeded
	}
	retried := make(chan struct{}, 1)
	config.waitBackoff = func(ctx context.Context, _ time.Duration) error {
		retried <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	supervisor, err := New(config, discardLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case <-retried:
		cancel()
	case runErr := <-done:
		require.ErrorIs(t, runErr, context.Canceled)
	}
	runErr := <-done

	require.ErrorIs(t, runErr, context.Canceled)
	require.Equal(t, StateStopped, supervisor.Status().State)
	require.Equal(t, 1, process.interrupts())
}

func TestRunRetriesInitialBootstrapFailureUntilProxyBecomesReady(t *testing.T) {
	first := newFakeProcess()
	second := newFakeProcess()
	starts := make(chan int, 2)
	var attempts int
	config := lifecycleConfig(t)
	config.startProcess = func(processSpec) (managedProcess, error) {
		attempts++
		starts <- attempts
		if attempts == 1 {
			return first, nil
		}
		return second, nil
	}
	config.waitReady = func(context.Context, readinessSpec) error {
		if attempts == 1 {
			return errors.New("temporary Snowflake bootstrap failure")
		}
		return nil
	}
	config.waitBackoff = func(context.Context, time.Duration) error { return nil }
	supervisor, err := New(config, discardLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()

	require.NoError(t, supervisor.WaitReady(context.Background()))
	require.Equal(t, 1, <-starts)
	require.Equal(t, 2, <-starts)
	require.Equal(t, StateReady, supervisor.Status().State)
	require.Equal(t, 1, supervisor.Status().RestartCount)

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestRunRestartsAfterReadyChildExitWithExponentialBackoff(t *testing.T) {
	first := newFakeProcess()
	second := newFakeProcess()
	starts := make(chan int, 2)
	var startMu sync.Mutex
	startCount := 0
	backoffs := make(chan time.Duration, 2)
	config := lifecycleConfig(t)
	config.startProcess = func(processSpec) (managedProcess, error) {
		startMu.Lock()
		defer startMu.Unlock()
		startCount++
		starts <- startCount
		if startCount == 1 {
			return first, nil
		}
		return second, nil
	}
	config.waitReady = func(context.Context, readinessSpec) error { return nil }
	config.waitBackoff = func(_ context.Context, delay time.Duration) error {
		backoffs <- delay
		return nil
	}
	supervisor, err := New(config, discardLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	require.NoError(t, supervisor.WaitReady(context.Background()))
	require.Equal(t, 1, <-starts)
	first.exit(errors.New("unexpected exit"))
	require.Equal(t, time.Second, <-backoffs)
	require.Equal(t, 2, <-starts)
	require.Eventually(t, func() bool {
		status := supervisor.Status()
		return status.State == StateReady && status.RestartCount == 1
	}, time.Second, time.Millisecond)

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestDefaultBackoffCapsAtThirtySeconds(t *testing.T) {
	require.Equal(t, time.Second, restartBackoff(0))
	require.Equal(t, 2*time.Second, restartBackoff(1))
	require.Equal(t, 30*time.Second, restartBackoff(20))
}

func lifecycleConfig(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	return Config{
		StateDir:         filepath.Join(root, "state"),
		TorPath:          executableFixture(t, root, "tor"),
		SnowflakePath:    executableFixture(t, root, "snowflake-client"),
		BootstrapTimeout: time.Second,
		StopTimeout:      time.Second,
	}
}

func executableFixture(t *testing.T, root, name string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(root, 0o700))
	path := filepath.Join(root, name)
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	return path
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeProcess struct {
	mu             sync.Mutex
	interruptCount int
	killCount      int
	done           chan error
	once           sync.Once
}

func newFakeProcess() *fakeProcess { return &fakeProcess{done: make(chan error, 1)} }

func (p *fakeProcess) Wait() error { return <-p.done }

func (p *fakeProcess) Interrupt() error {
	p.mu.Lock()
	p.interruptCount++
	p.mu.Unlock()
	p.exit(nil)
	return nil
}

func (p *fakeProcess) Kill() error {
	p.mu.Lock()
	p.killCount++
	p.mu.Unlock()
	p.exit(errors.New("killed"))
	return nil
}

func (p *fakeProcess) exit(err error) { p.once.Do(func() { p.done <- err }) }

func (p *fakeProcess) interrupts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.interruptCount
}

func (p *fakeProcess) kills() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killCount
}

func TestReadinessRequiresBootstrapLogBeforeNetworkProbes(t *testing.T) {
	root := t.TempDir()
	portCalls := 0
	socksCalls := 0
	ready, err := readinessProbe(context.Background(), readinessSpec{
		bootstrapLogPath: filepath.Join(root, "missing.log"),
		portProbe: func(context.Context, string) error {
			portCalls++
			return nil
		},
		socksProbe: func(context.Context, string, string) error {
			socksCalls++
			return nil
		},
	})
	require.ErrorIs(t, err, errBootstrapFailed)
	require.False(t, ready)
	require.Zero(t, portCalls)
	require.Zero(t, socksCalls)
}

func TestReadinessRequiresListeningSOCKSPortBeforeRoutedProbe(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "bootstrap.log")
	require.NoError(t, os.WriteFile(logPath, []byte("Bootstrapped 100%: Done"), 0o600))
	socksCalls := 0
	ready, err := readinessProbe(context.Background(), readinessSpec{
		bootstrapLogPath: logPath,
		portProbe: func(context.Context, string) error {
			return errors.New("not listening")
		},
		socksProbe: func(context.Context, string, string) error {
			socksCalls++
			return nil
		},
	})
	require.ErrorIs(t, err, errSOCKSProbe)
	require.False(t, ready)
	require.Zero(t, socksCalls)
}

func TestReadinessRejectsFailedRoutedProbe(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "bootstrap.log")
	require.NoError(t, os.WriteFile(logPath, []byte("Bootstrapped 100%: Done"), 0o600))
	applicationProbeCalls := 0
	ready, err := readinessProbe(context.Background(), readinessSpec{
		bootstrapLogPath: logPath,
		socksAddress:     DefaultSOCKSAddress,
		probeAddress:     "149.154.167.50:443",
		portProbe:        func(context.Context, string) error { return nil },
		socksProbe: func(context.Context, string, string) error {
			applicationProbeCalls++
			return errors.New("application endpoint is intentionally unavailable")
		},
	})
	require.ErrorIs(t, err, errSOCKSProbe)
	require.False(t, ready)
	require.Equal(t, 1, applicationProbeCalls)
}

func TestReadinessRequiresSuccessfulSOCKSRoutedTelegramProbe(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "bootstrap.log")
	require.NoError(t, os.WriteFile(logPath, []byte("Bootstrapped 100%: Done"), 0o600))
	ready, err := readinessProbe(context.Background(), readinessSpec{
		bootstrapLogPath: logPath,
		socksAddress:     DefaultSOCKSAddress,
		probeAddress:     "149.154.167.50:443",
		portProbe:        func(context.Context, string) error { return nil },
		socksProbe:       func(context.Context, string, string) error { return nil },
	})
	require.NoError(t, err)
	require.True(t, ready)
}

func TestRunRotatesSnowflakeToObfs4AfterRoutedFailure(t *testing.T) {
	root := t.TempDir()
	bridgePath := writeOfficialShapeFixture(t, []string{sevenObfs4Lines()[0]}, []string{twoSnowflakeLines()[0]})
	first := newFakeProcess()
	second := newFakeProcess()
	var startMu sync.Mutex
	starts := 0
	firstStoppedBeforeSecond := false
	config := Config{
		StateDir:          filepath.Join(root, "state"),
		TorPath:           executableFixture(t, root, "tor"),
		LyrebirdPath:      executableFixture(t, root, "lyrebird"),
		BridgeConfigPath:  bridgePath,
		MaxBridgeAttempts: 1,
		BootstrapTimeout:  time.Second,
		StopTimeout:       time.Second,
	}
	config.shuffleCandidates = func([]BridgeCandidate) {}
	config.startProcess = func(processSpec) (managedProcess, error) {
		startMu.Lock()
		defer startMu.Unlock()
		starts++
		if starts == 1 {
			return first, nil
		}
		firstStoppedBeforeSecond = first.interrupts() > 0
		return second, nil
	}
	config.waitReady = func(context.Context, readinessSpec) error {
		startMu.Lock()
		current := starts
		startMu.Unlock()
		if current == 1 {
			return errors.New("socks probe failed")
		}
		return nil
	}
	config.waitBackoff = func(context.Context, time.Duration) error { return nil }
	supervisor, err := New(config, discardLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()

	require.NoError(t, supervisor.WaitReady(context.Background()))
	require.Equal(t, StateReady, supervisor.Status().State)
	require.Equal(t, "obfs4", supervisor.Status().Transport)
	require.True(t, firstStoppedBeforeSecond)
	require.Equal(t, 1, first.interrupts())
	require.Empty(t, supervisor.config.snowflakePath)

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestAttemptCandidatesPrioritizesSnowflakeWithoutDroppingAcceptedCandidates(t *testing.T) {
	loaded, err := LoadBridgeBundle(writeOfficialShapeFixture(t, sevenObfs4Lines(), twoSnowflakeLines()))
	require.NoError(t, err)
	config := resolvedConfig{
		bridgeCandidates:  loaded,
		shuffleCandidates: func([]BridgeCandidate) {},
	}

	candidates := attemptCandidates(config)

	require.Len(t, candidates, 9)
	require.Equal(t, TransportSnowflake, candidates[0].Transport)
	require.Equal(t, TransportSnowflake, candidates[1].Transport)
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates[2:] {
		require.Equal(t, TransportObfs4, candidate.Transport)
	}
	for _, candidate := range candidates {
		seen[candidate.ID] = struct{}{}
	}
	require.Len(t, seen, 9)
}

func TestRunStartsProvenSnowflakeBeforeObfs4(t *testing.T) {
	root := t.TempDir()
	startedTransport := make(chan Transport, 1)
	process := newFakeProcess()
	config := Config{
		StateDir:         filepath.Join(root, "state"),
		TorPath:          executableFixture(t, root, "tor"),
		LyrebirdPath:     executableFixture(t, root, "lyrebird"),
		BridgeConfigPath: writeOfficialShapeFixture(t, []string{sevenObfs4Lines()[0]}, []string{twoSnowflakeLines()[0]}),
		BootstrapTimeout: time.Second,
		StopTimeout:      time.Second,
	}
	config.shuffleCandidates = func([]BridgeCandidate) {}
	config.startProcess = func(spec processSpec) (managedProcess, error) {
		contents, err := os.ReadFile(spec.configPath)
		require.NoError(t, err)
		switch {
		case strings.Contains(string(contents), "ClientTransportPlugin snowflake exec "):
			startedTransport <- TransportSnowflake
		case strings.Contains(string(contents), "ClientTransportPlugin obfs4 exec "):
			startedTransport <- TransportObfs4
		default:
			require.FailNow(t, "generated torrc has no supported managed transport")
		}
		return process, nil
	}
	config.waitReady = func(context.Context, readinessSpec) error { return nil }
	supervisor, err := New(config, discardLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()

	require.NoError(t, supervisor.WaitReady(context.Background()))
	require.Equal(t, TransportSnowflake, <-startedTransport)
	require.Equal(t, StateReady, supervisor.Status().State)
	require.Equal(t, "snowflake", supervisor.Status().Transport)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestAttemptCandidatesUsesEveryLoadedCandidateWithoutSyntheticFallback(t *testing.T) {
	loaded, err := LoadBridgeBundle(writeOfficialShapeFixture(t, sevenObfs4Lines(), twoSnowflakeLines()))
	require.NoError(t, err)
	config := resolvedConfig{
		maxBridgeAttempts: 1,
		bridgeCandidates:  loaded,
		snowflakeFallback: BridgeCandidate{Transport: TransportSnowflake, ID: "snowflake", Canonical: "fallback"},
		shuffleCandidates: func([]BridgeCandidate) {},
	}
	candidates := attemptCandidates(config)
	require.Len(t, candidates, 9)
	for index, candidate := range candidates[:2] {
		require.Equal(t, TransportSnowflake, candidate.Transport, "candidate %d", index)
	}
	for index, candidate := range candidates[2:] {
		require.Equal(t, TransportObfs4, candidate.Transport, "candidate %d", index+2)
	}
	for _, candidate := range candidates {
		require.NotEqual(t, "snowflake", candidate.ID)
	}
}

func TestRunReportsExhaustionAndPreservesLastTransport(t *testing.T) {
	root := t.TempDir()
	bridgePath := writeOfficialShapeFixture(t, []string{sevenObfs4Lines()[0]}, []string{twoSnowflakeLines()[0]})
	config := Config{
		StateDir:          filepath.Join(root, "state"),
		TorPath:           executableFixture(t, root, "tor"),
		LyrebirdPath:      executableFixture(t, root, "lyrebird"),
		BridgeConfigPath:  bridgePath,
		MaxBridgeAttempts: 1,
		BootstrapTimeout:  time.Second,
		StopTimeout:       time.Second,
	}
	config.shuffleCandidates = func([]BridgeCandidate) {}
	config.startProcess = func(processSpec) (managedProcess, error) { return newFakeProcess(), nil }
	config.waitReady = func(context.Context, readinessSpec) error { return errSOCKSProbe }
	backoffReached := make(chan struct{})
	config.waitBackoff = func(ctx context.Context, _ time.Duration) error {
		select {
		case <-backoffReached:
		default:
			close(backoffReached)
		}
		<-ctx.Done()
		return ctx.Err()
	}
	supervisor, err := New(config, discardLogger())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()

	<-backoffReached
	status := supervisor.Status()
	require.Equal(t, StateDegraded, status.State)
	require.Equal(t, "all_candidates_exhausted", status.LastError)
	require.Equal(t, "obfs4", status.Transport)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.Equal(t, "obfs4", supervisor.Status().Transport)
}

func TestWaitReadyWaitsForFreshRunAfterStop(t *testing.T) {
	config := lifecycleConfig(t)
	config.startProcess = func(processSpec) (managedProcess, error) { return newFakeProcess(), nil }
	config.waitReady = func(context.Context, readinessSpec) error { return nil }
	supervisor, err := New(config, discardLogger())
	require.NoError(t, err)

	firstCtx, firstCancel := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() { firstDone <- supervisor.Run(firstCtx) }()
	require.NoError(t, supervisor.WaitReady(context.Background()))
	firstCancel()
	require.ErrorIs(t, <-firstDone, context.Canceled)

	secondReady := make(chan error, 1)
	go func() { secondReady <- supervisor.WaitReady(context.Background()) }()
	select {
	case err := <-secondReady:
		t.Fatalf("WaitReady reused stale run result: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	secondCtx, secondCancel := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() { secondDone <- supervisor.Run(secondCtx) }()
	require.NoError(t, <-secondReady)
	secondCancel()
	require.ErrorIs(t, <-secondDone, context.Canceled)
}

func TestWaitReadyAfterReturnsFailureFromRequestedRunGeneration(t *testing.T) {
	config := lifecycleConfig(t)
	config.startProcess = func(processSpec) (managedProcess, error) {
		return nil, errors.New("immediate child failure")
	}
	supervisor, err := New(config, discardLogger())
	require.NoError(t, err)
	before := supervisor.Generation()

	require.Error(t, supervisor.Run(context.Background()))

	err = supervisor.WaitReadyAfter(context.Background(), before)
	require.ErrorContains(t, err, "start managed Tor")
}

func TestRunClearsStaleBootstrapLogBeforeStartingProcess(t *testing.T) {
	config := lifecycleConfig(t)
	supervisor, err := New(config, discardLogger())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(supervisor.config.bootstrapLogPath, []byte("Bootstrapped 100% from stale run"), 0o600))
	var contents []byte
	supervisor.config.startProcess = func(processSpec) (managedProcess, error) {
		contents, err = os.ReadFile(supervisor.config.bootstrapLogPath)
		return nil, errors.New("stop after observing log")
	}

	require.Error(t, supervisor.Run(context.Background()))
	require.Empty(t, contents)
}

func TestLiveManagedTorSnowflakeBootstrapAndShutdown(t *testing.T) {
	if os.Getenv("TELEGRAM_COMPANION_LIVE_TOR") != "1" {
		t.Skip("set TELEGRAM_COMPANION_LIVE_TOR=1 to use bundled Tor and Lyrebird")
	}
	resourcesDir := strings.TrimSpace(os.Getenv("TELEGRAM_COMPANION_LIVE_TOR_RESOURCES"))
	require.NotEmpty(t, resourcesDir, "TELEGRAM_COMPANION_LIVE_TOR_RESOURCES is required")
	stateDir := t.TempDir()
	supervisor, err := New(Config{
		StateDir:         stateDir,
		ResourcesDir:     resourcesDir,
		LyrebirdPath:     filepath.Join(resourcesDir, "tor", "pluggable_transports", "lyrebird"),
		BridgeConfigPath: filepath.Join(resourcesDir, "tor", "pluggable_transports", "pt_config.json"),
	}, discardLogger())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	done := make(chan error, 1)
	generation := supervisor.Generation()
	go func() { done <- supervisor.Run(ctx) }()
	readyErr := supervisor.WaitReadyAfter(ctx, generation)
	status := supervisor.Status()
	require.NoError(t, readyErr, "state=%s transport=%s last_error=%s retries=%d", status.State, status.Transport, status.LastError, status.RestartCount)
	require.Equal(t, StateReady, supervisor.Status().State)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.Equal(t, StateStopped, supervisor.Status().State)
}
