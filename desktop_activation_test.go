//go:build desktop

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wailsapp/wails/v2/pkg/options"

	appbootstrap "telegram-companion/internal/app"
	"telegram-companion/internal/bootstrapstate"
	"telegram-companion/internal/buildinfo"
	"telegram-companion/internal/domain"
	"telegram-companion/internal/license"
	"telegram-companion/internal/revocation"
	secretservice "telegram-companion/internal/service/secrets"
	wailsbindings "telegram-companion/internal/transport/wails"
	"telegram-companion/internal/updater"
	"telegram-companion/internal/usecase"
)

func TestDesktopRevocationRuntimeOrdersTerminalDeactivation(t *testing.T) {
	events := &desktopRevocationEvents{}
	checker := desktopRevocationChecker{decision: revocation.Revoked}
	runtime, err := newDesktopRevocationRuntime(
		desktopTerminalGate{events: events},
		desktopLicensedRuntimeStub{events: events},
		desktopRelaunchRequest{events: events},
		func(context.Context) { events.add("quit") },
		checker,
		"license-runtime-terminal",
		func(time.Duration) revocation.SupervisorTimer { return newImmediateDesktopTimer() },
		func() time.Duration { return 0 },
	)
	require.NoError(t, err)
	runtime.Startup(context.Background())
	require.Eventually(t, func() bool { return len(events.snapshot()) == 5 }, time.Second, time.Millisecond)
	require.Equal(t, []string{"gate:revoked", "reject", "shutdown", "relaunch", "quit"}, events.snapshot())

	runtime.Stop()
	runtime.handleTerminal(revocation.Revoked)
	require.Equal(t, []string{"gate:revoked", "reject", "shutdown", "relaunch", "quit"}, events.snapshot())
}

func TestDesktopRevocationRuntimeFailsSafeBeforeQuit(t *testing.T) {
	for _, test := range []struct {
		name       string
		target     desktopLicensedRuntimeStub
		relauncher desktopRelaunchRequest
		want       []string
	}{
		{
			name:   "operation rejection failure",
			target: desktopLicensedRuntimeStub{rejectErr: errors.New("operation did not stop")},
			want:   []string{"gate:check_required", "reject", "shutdown"},
		},
		{
			name:   "relaunch failure",
			target: desktopLicensedRuntimeStub{}, relauncher: desktopRelaunchRequest{err: errors.New("relaunch unavailable")},
			want: []string{"gate:check_required", "reject", "shutdown", "relaunch"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := &desktopRevocationEvents{}
			test.target.events = events
			test.relauncher.events = events
			runtime, err := newDesktopRevocationRuntime(
				desktopTerminalGate{events: events}, test.target, test.relauncher,
				func(context.Context) { events.add("quit") },
				desktopRevocationChecker{decision: revocation.CheckRequired, err: errors.New("offline beyond grace")},
				"license-runtime-fail-safe",
				func(time.Duration) revocation.SupervisorTimer { return newImmediateDesktopTimer() },
				func() time.Duration { return 0 },
			)
			require.NoError(t, err)
			runtime.Startup(context.Background())
			require.Eventually(t, func() bool { return len(events.snapshot()) == len(test.want) }, time.Second, time.Millisecond)
			require.Equal(t, test.want, events.snapshot())
			runtime.Stop()
		})
	}
}

func TestDesktopRevocationRuntimeShutsDownServicesAfterOperationRejectionTimeout(t *testing.T) {
	events := &desktopRevocationEvents{}
	runtime, err := newDesktopRevocationRuntime(
		desktopTerminalGate{events: events},
		desktopLicensedRuntimeStub{events: events, waitForRejectDeadline: true},
		desktopRelaunchRequest{events: events},
		func(context.Context) { events.add("quit") },
		desktopRevocationChecker{decision: revocation.Revoked},
		"license-runtime-stuck-operation",
		func(time.Duration) revocation.SupervisorTimer { return newImmediateDesktopTimer() },
		func() time.Duration { return 0 },
	)
	require.NoError(t, err)
	runtime.rejectTimeout = time.Millisecond

	runtime.handleTerminal(revocation.Revoked)

	require.Equal(t, []string{"gate:revoked", "reject", "shutdown"}, events.snapshot())
}

func TestDesktopRevocationRuntimeStopBeforeStartupPreventsChecks(t *testing.T) {
	events := &desktopRevocationEvents{}
	checker := &countingDesktopRevocationChecker{decision: revocation.Revoked}
	runtime, err := newDesktopRevocationRuntime(
		desktopTerminalGate{events: events},
		desktopLicensedRuntimeStub{events: events},
		desktopRelaunchRequest{events: events},
		func(context.Context) { events.add("quit") },
		checker,
		"license-runtime-stopped",
		func(time.Duration) revocation.SupervisorTimer { return newImmediateDesktopTimer() },
		func() time.Duration { return 0 },
	)
	require.NoError(t, err)
	runtime.Stop()
	runtime.Startup(context.Background())
	require.Never(t, func() bool { return checker.calls.Load() != 0 }, 50*time.Millisecond, time.Millisecond)
	require.Empty(t, events.snapshot())
}

type desktopRevocationEvents struct {
	mu     sync.Mutex
	events []string
}

func (events *desktopRevocationEvents) add(event string) {
	events.mu.Lock()
	defer events.mu.Unlock()
	events.events = append(events.events, event)
}

func (events *desktopRevocationEvents) snapshot() []string {
	events.mu.Lock()
	defer events.mu.Unlock()
	return append([]string(nil), events.events...)
}

type desktopTerminalGate struct{ events *desktopRevocationEvents }

func (gate desktopTerminalGate) ApplyRevocationDecision(decision revocation.Decision) license.Snapshot {
	gate.events.add("gate:" + string(decision))
	state := license.StateCheckRequired
	if decision == revocation.Revoked {
		state = license.StateRevoked
	}
	return license.Snapshot{Mode: buildinfo.ChannelPublicMacOSARM64, State: state}
}

type desktopLicensedRuntimeStub struct {
	events                *desktopRevocationEvents
	rejectErr             error
	waitForRejectDeadline bool
}

func (runtime desktopLicensedRuntimeStub) RejectNewOperations(ctx context.Context) error {
	runtime.events.add("reject")
	if runtime.waitForRejectDeadline {
		<-ctx.Done()
		return ctx.Err()
	}
	return runtime.rejectErr
}

func (runtime desktopLicensedRuntimeStub) Shutdown(context.Context) { runtime.events.add("shutdown") }

type desktopRelaunchRequest struct {
	events *desktopRevocationEvents
	err    error
}

func (request desktopRelaunchRequest) Request(context.Context) error {
	request.events.add("relaunch")
	return request.err
}

type desktopRevocationChecker struct {
	decision revocation.Decision
	err      error
}

func (checker desktopRevocationChecker) Check(context.Context, string) (revocation.Decision, error) {
	return checker.decision, checker.err
}

type countingDesktopRevocationChecker struct {
	decision revocation.Decision
	calls    atomic.Int32
}

func (checker *countingDesktopRevocationChecker) Check(context.Context, string) (revocation.Decision, error) {
	checker.calls.Add(1)
	return checker.decision, nil
}

type immediateDesktopTimer struct{ channel chan time.Time }

func newImmediateDesktopTimer() *immediateDesktopTimer {
	timer := &immediateDesktopTimer{channel: make(chan time.Time, 1)}
	timer.channel <- time.Now()
	return timer
}

func (timer *immediateDesktopTimer) C() <-chan time.Time { return timer.channel }
func (*immediateDesktopTimer) Stop() bool                { return true }

func TestNewDesktopLicenseGateConsumesSignedRevocationDecisionBeforeWorkspace(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 17)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	licenseID := "desktop-license-revoked"
	handle, err := revocation.DeriveHandle(licenseID)
	require.NoError(t, err)
	envelope, err := revocation.Sign(revocation.Payload{
		Schema:      revocation.ManifestSchema,
		KeyID:       buildinfo.ExpectedRevocationKeyID,
		Sequence:    1,
		GeneratedAt: now.Format("2006-01-02T15:04:05Z"),
		Entries: []revocation.Entry{{
			Kind: revocation.EntryKindLicenseIDSHA256, Value: handle.String(), RevokedAt: now.Format("2006-01-02T15:04:05Z"),
		}},
	}, privateKey)
	require.NoError(t, err)
	info := publicBuildInfo()
	info.LicensePublicKey = base64.StdEncoding.EncodeToString(publicKey)
	info.RevocationManifestURL = buildinfo.ExpectedRevocationManifestURL
	info.RevocationKeyID = buildinfo.ExpectedRevocationKeyID
	info.RevocationPublicKey = base64.StdEncoding.EncodeToString(publicKey)
	stateStore := &desktopRevocationStateStore{}
	gate, checker, err := newDesktopLicenseGateWithDependencies(
		info,
		desktopTokenStore{token: "locally-signed-token"},
		stateStore,
		desktopRevocationFetcher{body: []byte(envelope)},
		func() (string, error) { return "DESKTOP-MACHINE", nil },
		func(string, license.VerifyOptions) (license.Payload, error) {
			return license.Payload{LicenseID: licenseID}, nil
		},
		func() time.Time { return now },
	)
	require.NoError(t, err)
	require.NotNil(t, checker)
	require.Equal(t, license.StateRevoked, gate.Snapshot().State)
	require.False(t, gate.Authorized())
	require.Len(t, stateStore.state.RevokedHandles, 1)

	factoryCalls := 0
	program, err := newDesktopProgram(info, gate.Authorized(), func() (*DesktopApp, error) {
		factoryCalls++
		return &DesktopApp{}, nil
	})
	require.NoError(t, err)
	require.Equal(t, desktopModeActivation, program.mode)
	require.Zero(t, factoryCalls)
}

func TestNewDesktopLicenseGateRejectsInvalidRevocationConfiguration(t *testing.T) {
	info := publicBuildInfo()
	info.LicensePublicKey = base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))
	info.RevocationKeyID = buildinfo.ExpectedRevocationKeyID
	info.RevocationPublicKey = "not-canonical-base64"
	_, _, err := newDesktopLicenseGateWithDependencies(
		info, desktopTokenStore{}, &desktopRevocationStateStore{}, desktopRevocationFetcher{},
		func() (string, error) { return "DESKTOP-MACHINE", nil }, license.ParseAndVerify, time.Now,
	)
	require.Error(t, err)
}

type desktopTokenStore struct{ token string }

func (store desktopTokenStore) Load() (string, error) { return store.token, nil }
func (desktopTokenStore) Save(string) error           { return nil }

type desktopRevocationFetcher struct{ body []byte }

func (fetcher desktopRevocationFetcher) Fetch(context.Context) ([]byte, error) {
	return append([]byte(nil), fetcher.body...), nil
}

type desktopRevocationStateStore struct{ state revocation.SecureState }

func (store *desktopRevocationStateStore) Load(context.Context) (revocation.SecureState, error) {
	return store.state, nil
}

func (store *desktopRevocationStateStore) Save(_ context.Context, state revocation.SecureState) error {
	store.state = state
	return nil
}

type bootstrapTestSecrets struct {
	values map[string][]byte
}

func (s *bootstrapTestSecrets) Get(_ context.Context, name string) ([]byte, error) {
	value, ok := s.values[name]
	if !ok {
		return nil, bootstrapstate.ErrSecretNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *bootstrapTestSecrets) Set(_ context.Context, name string, value []byte) error {
	if s.values == nil {
		s.values = make(map[string][]byte)
	}
	s.values[name] = append([]byte(nil), value...)
	return nil
}

func (s *bootstrapTestSecrets) SetBatch(ctx context.Context, values map[string][]byte, commit func() error) error {
	previous := make(map[string][]byte, len(values))
	missing := make(map[string]bool, len(values))
	for name, value := range values {
		old, ok := s.values[name]
		if ok {
			previous[name] = append([]byte(nil), old...)
		} else {
			missing[name] = true
		}
		if err := s.Set(ctx, name, value); err != nil {
			return err
		}
	}
	if err := commit(); err != nil {
		for name := range values {
			if missing[name] {
				delete(s.values, name)
			} else {
				s.values[name] = previous[name]
			}
		}
		return err
	}
	return nil
}

func TestNewDesktopProgramDoesNotConstructPublicRuntimeBeforeActivation(t *testing.T) {
	calls := 0
	program, err := newDesktopProgram(publicBuildInfo(), false, func() (*DesktopApp, error) {
		calls++
		return &DesktopApp{}, nil
	})

	require.NoError(t, err)
	require.Equal(t, desktopModeActivation, program.mode)
	require.Nil(t, program.app)
	require.Equal(t, 0, calls)
}

func TestNewDesktopProgramConstructsRuntimeForAuthorizedPublicBuild(t *testing.T) {
	calls := 0
	app := &DesktopApp{}
	program, err := newDesktopProgram(publicBuildInfo(), true, func() (*DesktopApp, error) {
		calls++
		return app, nil
	})

	require.NoError(t, err)
	require.Equal(t, desktopModeWorkspace, program.mode)
	require.Same(t, app, program.app)
	require.Equal(t, 1, calls)
}

func TestNewDesktopProgramKeepsInternalBuildBehavior(t *testing.T) {
	calls := 0
	app := &DesktopApp{}
	program, err := newDesktopProgram(buildinfo.Info{
		Channel:   buildinfo.ChannelInternal,
		ProductID: buildinfo.ProductID,
		Version:   "0.6.0",
	}, false, func() (*DesktopApp, error) {
		calls++
		return app, nil
	})

	require.NoError(t, err)
	require.Equal(t, desktopModeWorkspace, program.mode)
	require.Same(t, app, program.app)
	require.Equal(t, 1, calls)
}

func TestNewDesktopProgramMapsUnknownPublicRuntimeFailureToRecovery(t *testing.T) {
	want := errors.New("runtime failed")
	program, err := newDesktopProgram(publicBuildInfo(), true, func() (*DesktopApp, error) {
		return nil, want
	})

	require.NoError(t, err)
	require.Equal(t, desktopModeRecovery, program.mode)
	require.Equal(t, desktopRecoveryRuntimeUnavailable, program.recoveryCode)
}

func TestNewDesktopProgramMapsAuthorizedPublicFailureToSafeRecovery(t *testing.T) {
	raw := errors.New("raw runtime cause /Users/private account-secret")
	program, err := newDesktopProgram(publicBuildInfo(), true, func() (*DesktopApp, error) {
		return nil, errors.Join(secretservice.ErrKeyringUnavailable, raw)
	})

	require.NoError(t, err)
	require.Equal(t, desktopModeRecovery, program.mode)
	require.Equal(t, desktopRecoveryKeychainUnavailable, program.recoveryCode)
	require.Nil(t, program.app)
	require.NotContains(t, string(program.recoveryCode), "Users")
}

func TestNewDesktopProgramKeepsInternalRuntimeFailureFailFast(t *testing.T) {
	want := errors.New("internal runtime failed")
	program, err := newDesktopProgram(buildinfo.Info{
		Channel: buildinfo.ChannelInternal, ProductID: buildinfo.ProductID, Version: "0.8.2",
	}, true, func() (*DesktopApp, error) { return nil, want })

	require.ErrorIs(t, err, want)
	require.Equal(t, desktopProgram{}, program)
}

func TestNewDesktopProgramMapsInvalidPublicRuntimeFactoryResultsToRecovery(t *testing.T) {
	tests := []struct {
		name    string
		factory func() (*DesktopApp, error)
	}{
		{name: "missing factory"},
		{name: "nil runtime", factory: func() (*DesktopApp, error) { return nil, nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			program, err := newDesktopProgram(publicBuildInfo(), true, test.factory)
			require.NoError(t, err)
			require.Equal(t, desktopModeRecovery, program.mode)
			require.Equal(t, desktopRecoveryRuntimeUnavailable, program.recoveryCode)
			require.Nil(t, program.app)
		})
	}
}

func publicBuildInfo() buildinfo.Info {
	return buildinfo.Info{
		Channel:    buildinfo.ChannelPublicMacOSARM64,
		ProductID:  buildinfo.ProductID,
		Version:    "0.8.2",
		AppcastURL: "https://updates.example.test/appcast.xml",
	}
}

func publicEmptyBuildInfo() buildinfo.Info {
	info := publicBuildInfo()
	info.BootstrapMode = buildinfo.BootstrapModeEmpty
	return info
}

func TestRunDesktopProgramActivationModeDoesNotBindWorkspace(t *testing.T) {
	startup := wailsbindings.NewStartupBindings("activation", "", nil, nil, nil, nil, nil)
	activation := wailsbindings.NewActivationBindings(nil, nil, nil, nil)
	updates := wailsbindings.NewUpdateBindings(nil, nil)
	var received *options.App

	err := runDesktopProgram(desktopProgram{mode: desktopModeActivation}, startup, activation, updates, func(config *options.App) error {
		received = config
		return nil
	})

	require.NoError(t, err)
	require.NotNil(t, received)
	require.Equal(t, []interface{}{startup, activation, updates}, received.Bind)
}

func TestRunDesktopProgramRecoveryModeDoesNotBindOrStartWorkspace(t *testing.T) {
	startup := wailsbindings.NewStartupBindings("recovery", "profile_blocked", nil, nil, nil, nil, nil)
	activation := wailsbindings.NewActivationBindings(nil, nil, nil, nil)
	updates := wailsbindings.NewUpdateBindings(nil, nil)
	app := &DesktopApp{bindings: wailsbindings.NewBindings(usecase.NewAutomationController(nil))}
	var received *options.App

	err := runDesktopProgram(desktopProgram{
		mode: desktopModeRecovery, app: app, recoveryCode: desktopRecoveryProfileBlocked,
	}, startup, activation, updates, func(config *options.App) error {
		received = config
		config.OnStartup(context.Background())
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, []interface{}{startup, activation, updates}, received.Bind)
}

func TestRunDesktopProgramEnablesNativeMacFullscreenControls(t *testing.T) {
	startup := wailsbindings.NewStartupBindings("activation", "", nil, nil, nil, nil, nil)
	activation := wailsbindings.NewActivationBindings(nil, nil, nil, nil)
	updates := wailsbindings.NewUpdateBindings(nil, nil)
	var received *options.App

	err := runDesktopProgram(desktopProgram{mode: desktopModeActivation}, startup, activation, updates, func(config *options.App) error {
		received = config
		return nil
	})

	require.NoError(t, err)
	require.NotNil(t, received)
	require.False(t, received.DisableResize)
	require.NotNil(t, received.Mac)
	require.False(t, received.Mac.DisableZoom)
	require.False(t, received.Mac.DisableEscapeExitsFullscreen)
}

func TestRunDesktopProgramWorkspaceBindsExistingApplication(t *testing.T) {
	startup := wailsbindings.NewStartupBindings("workspace", "", nil, nil, nil, nil, nil)
	activation := wailsbindings.NewActivationBindings(nil, nil, nil, nil)
	updates := wailsbindings.NewUpdateBindings(nil, nil)
	app := &DesktopApp{bindings: wailsbindings.NewBindings(usecase.NewAutomationController(nil))}
	var received *options.App

	err := runDesktopProgram(desktopProgram{mode: desktopModeWorkspace, app: app}, startup, activation, updates, func(config *options.App) error {
		received = config
		return nil
	})

	require.NoError(t, err)
	require.NotNil(t, received)
	require.Equal(t, []interface{}{startup, activation, updates, app.Bindings()}, received.Bind)
}

func TestResolveBuildDesktopPathsUsesStablePublicMacDirectory(t *testing.T) {
	configRoot := filepath.Join(string(filepath.Separator), "Users", "denis", "Library", "Application Support")
	executable := filepath.Join(string(filepath.Separator), "Applications", "Telegram Companion.app", "Contents", "MacOS", "telegram-companion")

	paths, err := resolveBuildDesktopPaths(publicBuildInfo(), "", configRoot, executable)

	require.NoError(t, err)
	require.Equal(t, filepath.Join(configRoot, "Telegram Companion"), paths.root)
}

func TestResolveBuildDesktopPathsIgnoresPublicDataRootOverride(t *testing.T) {
	configRoot := filepath.Join(string(filepath.Separator), "Users", "denis", "Library", "Application Support")
	executable := filepath.Join(string(filepath.Separator), "Applications", "Telegram Companion.app", "Contents", "MacOS", "telegram-companion")

	paths, err := resolveBuildDesktopPaths(publicBuildInfo(), filepath.Join(string(filepath.Separator), "tmp", "redirected"), configRoot, executable)

	require.NoError(t, err)
	require.Equal(t, filepath.Join(configRoot, "Telegram Companion"), paths.root)
}

func TestResolveBuildDesktopPathsKeepsInternalDirectoryIsolated(t *testing.T) {
	configRoot := filepath.Join(string(filepath.Separator), "home", "codex", ".config")
	executable := filepath.Join(string(filepath.Separator), "opt", "telegram-companion", "telegram-companion")

	paths, err := resolveBuildDesktopPaths(buildinfo.Info{Channel: buildinfo.ChannelInternal}, "", configRoot, executable)

	require.NoError(t, err)
	require.Equal(t, filepath.Join(configRoot, "telegram-companion"), paths.root)
}

func TestImportBundledDesktopStateImportsAuthorizedPublicBundle(t *testing.T) {
	ctx := context.Background()
	resources := t.TempDir()
	targetRoot := t.TempDir()
	sourceData := filepath.Join(t.TempDir(), "data")
	require.NoError(t, os.MkdirAll(sourceData, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sourceData, "app.db"), []byte("master-db"), 0o600))
	seedKey := []byte("0123456789abcdef0123456789abcdef")
	accountSecret := bytes.Repeat([]byte{0x44}, 32)
	grantProvider := testSeedGrantProvider{grant: license.SeedGrant{ID: "master-state", Key: seedKey}}
	bundlePath := filepath.Join(resources, "bootstrap-state", bootstrapstate.BundleFile)
	require.NoError(t, bootstrapstate.Pack(ctx, bootstrapstate.PackConfig{
		SourceData: sourceData,
		OutputPath: bundlePath,
		BundleID:   "master-state",
		AppVersion: "0.8.2",
		Key:        seedKey,
		Secrets: &bootstrapTestSecrets{values: completeDesktopOperationalSecrets(map[string][]byte{
			"telegram-account-credentials-v1": accountSecret,
		})},
	}))

	importedSecrets := &bootstrapTestSecrets{}
	imported, err := importBundledDesktopState(ctx, publicBuildInfo(), desktopPaths{root: targetRoot, resources: resources}, grantProvider, importedSecrets)

	require.NoError(t, err)
	require.True(t, imported)
	require.FileExists(t, filepath.Join(targetRoot, "data", "app.db"))
	require.Equal(t, accountSecret, importedSecrets.values["telegram-account-credentials-v1"])
}

func TestImportBundledDesktopStateSkipsInternalBuild(t *testing.T) {
	imported, err := importBundledDesktopState(
		context.Background(),
		buildinfo.Info{Channel: buildinfo.ChannelInternal, Version: "0.8.2"},
		desktopPaths{root: t.TempDir(), resources: t.TempDir()},
		nil,
		&bootstrapTestSecrets{},
	)
	require.NoError(t, err)
	require.False(t, imported)
}

func TestImportBundledDesktopStateSkipsLegacyLicenseWithoutSeedGrant(t *testing.T) {
	targetRoot := t.TempDir()

	imported, err := importBundledDesktopState(
		context.Background(),
		publicBuildInfo(),
		desktopPaths{root: targetRoot, resources: t.TempDir()},
		testSeedGrantProvider{err: license.ErrNoSeedGrant},
		&bootstrapTestSecrets{},
	)

	require.Equal(t, desktopRecoverySeedLicenseRequired, desktopRecoveryCodeForError(err))
	require.False(t, imported)
	require.NoDirExists(t, filepath.Join(targetRoot, "data"))
}

func TestImportBundledDesktopStateRejectsCleanPublicProfileWithoutBundle(t *testing.T) {
	root := t.TempDir()
	imported, err := importBundledDesktopState(
		context.Background(), publicBuildInfo(), desktopPaths{root: root, resources: t.TempDir()},
		testSeedGrantProvider{grant: license.SeedGrant{ID: "seed", Key: []byte("0123456789abcdef0123456789abcdef")}},
		&bootstrapTestSecrets{},
	)

	require.False(t, imported)
	require.Equal(t, desktopRecoverySeedUnavailable, desktopRecoveryCodeForError(err))
	require.NoDirExists(t, filepath.Join(root, "data"))
}

func TestImportBundledDesktopStateEmptyPublicProfileDoesNotRequireSeedGrant(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "proxy", "tor-snowflake"), 0o700))

	imported, err := importBundledDesktopState(
		context.Background(), publicEmptyBuildInfo(), desktopPaths{root: root, resources: t.TempDir()}, nil, nil,
	)

	require.NoError(t, err)
	require.False(t, imported)
}

func TestEmptyPublicProfileReachesRuntimeAfterAuthorizationWithoutSeedBundle(t *testing.T) {
	root := t.TempDir()
	runtimeCalls := 0
	program, err := newDesktopProgram(publicEmptyBuildInfo(), true, func() (*DesktopApp, error) {
		imported, importErr := importBundledDesktopState(
			context.Background(), publicEmptyBuildInfo(), desktopPaths{root: root, resources: t.TempDir()}, nil, nil,
		)
		if importErr != nil {
			return nil, importErr
		}
		require.False(t, imported)
		require.NoError(t, os.MkdirAll(filepath.Join(root, "data"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(root, "data", "app.db"), []byte("runtime-owned-db"), 0o600))
		runtimeCalls++
		return &DesktopApp{}, nil
	})

	require.NoError(t, err)
	require.Equal(t, desktopModeWorkspace, program.mode)
	require.Equal(t, 1, runtimeCalls)
	require.FileExists(t, filepath.Join(root, "data", "app.db"))
}

func TestEmptyPublicProfileRestartsAfterRuntimeCreatesPristineState(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	resources := t.TempDir()
	paths := desktopPaths{root: root, resources: resources}
	writeDesktopTorFixture(t, resources)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	grants := &countingSeedGrantProvider{}
	secrets := persistentDesktopSecrets{values: completeDesktopOperationalSecrets(nil), calls: make(map[string]int)}
	initialSecrets := cloneDesktopSecrets(secrets.values)

	imported, err := importBundledDesktopState(ctx, publicEmptyBuildInfo(), paths, grants, nil)
	require.NoError(t, err)
	require.False(t, imported)
	require.Zero(t, grants.calls)

	var first *DesktopApp
	program, err := newDesktopProgram(publicEmptyBuildInfo(), true, func() (*DesktopApp, error) {
		first, err = newDesktopAppWithPaths(ctx, paths, log, &secrets)
		return first, err
	})
	require.NoError(t, err)
	require.Equal(t, desktopModeWorkspace, program.mode)
	requireEmptyDesktopRuntime(t, first)
	require.FileExists(t, filepath.Join(root, "data", "app.db"))
	require.DirExists(t, filepath.Join(root, "data", "backups"))
	require.DirExists(t, filepath.Join(root, "proxy", "tor-snowflake", "data"))
	for _, name := range []string{"torrc", "bootstrap.log", "tor-output.log"} {
		require.FileExists(t, filepath.Join(root, "proxy", "tor-snowflake", name))
	}
	require.Equal(t, initialSecrets, secrets.values)
	first.Shutdown(ctx)

	state, err := appbootstrap.InspectEmptyPublicProfile(root)
	require.NoError(t, err)
	require.Equal(t, appbootstrap.FirstLaunchTargetExistingProfile, state)
	require.NoFileExists(t, filepath.Join(root, "bootstrap-state", "imported.json"))
	imported, err = importBundledDesktopState(ctx, publicEmptyBuildInfo(), paths, grants, nil)
	require.NoError(t, err)
	require.False(t, imported)
	require.Zero(t, grants.calls)

	var restarted *DesktopApp
	program, err = newDesktopProgram(publicEmptyBuildInfo(), true, func() (*DesktopApp, error) {
		restarted, err = newDesktopAppWithPaths(ctx, paths, log, &secrets)
		return restarted, err
	})
	require.NoError(t, err)
	require.Equal(t, desktopModeWorkspace, program.mode)
	requireEmptyDesktopRuntime(t, restarted)
	require.Equal(t, initialSecrets, secrets.values)
	require.Equal(t, map[string]int{
		"outbound-target-key":             2,
		"proxy-credentials-v1":            2,
		"scout-message-key":               2,
		"telegram-account-credentials-v1": 2,
	}, secrets.calls)
	restarted.Shutdown(ctx)
}

func TestUnauthorizedEmptyPublicProfileDoesNotConstructRuntimeOrWriteData(t *testing.T) {
	root := t.TempDir()
	runtimeCalls := 0
	program, err := newDesktopProgram(publicEmptyBuildInfo(), false, func() (*DesktopApp, error) {
		runtimeCalls++
		require.NoError(t, os.MkdirAll(filepath.Join(root, "data"), 0o700))
		return &DesktopApp{}, nil
	})

	require.NoError(t, err)
	require.Equal(t, desktopModeActivation, program.mode)
	require.Zero(t, runtimeCalls)
	require.NoDirExists(t, filepath.Join(root, "data"))
}

func TestCleanPublicProfileDoesNotReachRuntimeWithoutSuccessfulImport(t *testing.T) {
	runtimeCalls := 0
	program, err := newDesktopProgram(publicBuildInfo(), true, func() (*DesktopApp, error) {
		imported, importErr := importBundledDesktopState(
			context.Background(), publicBuildInfo(), desktopPaths{root: t.TempDir(), resources: t.TempDir()},
			testSeedGrantProvider{grant: license.SeedGrant{ID: "seed", Key: []byte("0123456789abcdef0123456789abcdef")}},
			&bootstrapTestSecrets{},
		)
		if importErr != nil {
			return nil, importErr
		}
		if !imported {
			return nil, errDesktopSeedUnavailable
		}
		runtimeCalls++
		return &DesktopApp{}, nil
	})

	require.NoError(t, err)
	require.Equal(t, desktopModeRecovery, program.mode)
	require.Equal(t, desktopRecoverySeedUnavailable, program.recoveryCode)
	require.Zero(t, runtimeCalls)
}

func TestInitiallyCleanPublicProfileDoesNotAcceptConcurrentProfileWhenImportReturnsFalse(t *testing.T) {
	root := t.TempDir()
	resources := t.TempDir()
	bundlePath := filepath.Join(resources, "bootstrap-state", bootstrapstate.BundleFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(bundlePath), 0o700))
	require.NoError(t, os.WriteFile(bundlePath, []byte("import-is-stubbed"), 0o600))
	previousImport := importFirstLaunchSeed
	t.Cleanup(func() { importFirstLaunchSeed = previousImport })
	importFirstLaunchSeed = func(_ context.Context, config appbootstrap.FirstLaunchSeedConfig) (bool, error) {
		path := filepath.Join(config.TargetRoot, "data", "sessions", "concurrent.session")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("concurrent-profile"), 0o600))
		return false, nil
	}

	imported, err := importBundledDesktopState(
		context.Background(), publicBuildInfo(), desktopPaths{root: root, resources: resources},
		testSeedGrantProvider{grant: license.SeedGrant{ID: "seed", Key: []byte("0123456789abcdef0123456789abcdef")}},
		&bootstrapTestSecrets{},
	)

	require.False(t, imported)
	require.Equal(t, desktopRecoveryProfileBlocked, desktopRecoveryCodeForError(err))
	require.FileExists(t, filepath.Join(root, "data", "sessions", "concurrent.session"))
	concurrentData, readErr := os.ReadFile(filepath.Join(root, "data", "sessions", "concurrent.session"))
	require.NoError(t, readErr)
	require.Equal(t, []byte("concurrent-profile"), concurrentData)
}

func TestAtomicTargetChangeEntersProfileBlockedRecoveryWithoutRuntime(t *testing.T) {
	root := t.TempDir()
	resources := t.TempDir()
	bundlePath := filepath.Join(resources, "bootstrap-state", bootstrapstate.BundleFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(bundlePath), 0o700))
	require.NoError(t, os.WriteFile(bundlePath, []byte("import-is-stubbed"), 0o600))
	previousImport := importFirstLaunchSeed
	t.Cleanup(func() { importFirstLaunchSeed = previousImport })
	importFirstLaunchSeed = func(context.Context, appbootstrap.FirstLaunchSeedConfig) (bool, error) {
		return false, appbootstrap.ErrFirstLaunchSeedTargetChanged
	}
	runtimeCalls := 0

	program, err := newDesktopProgram(publicBuildInfo(), true, func() (*DesktopApp, error) {
		imported, importErr := importBundledDesktopState(
			context.Background(), publicBuildInfo(), desktopPaths{root: root, resources: resources},
			testSeedGrantProvider{grant: license.SeedGrant{ID: "seed", Key: []byte("0123456789abcdef0123456789abcdef")}},
			&bootstrapTestSecrets{},
		)
		if importErr != nil {
			return nil, importErr
		}
		if !imported {
			return nil, errDesktopProfileBlocked
		}
		runtimeCalls++
		return &DesktopApp{}, nil
	})

	require.NoError(t, err)
	require.Equal(t, desktopModeRecovery, program.mode)
	require.Equal(t, desktopRecoveryProfileBlocked, program.recoveryCode)
	require.Zero(t, runtimeCalls)
}

func TestIncompletePublicSeedIsIncompatibleAndNeverReachesRuntime(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	resources := t.TempDir()
	source := filepath.Join(t.TempDir(), "data")
	require.NoError(t, os.MkdirAll(source, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(source, "app.db"), []byte("seed-db"), 0o600))
	seedKey := []byte("0123456789abcdef0123456789abcdef")
	bundlePath := filepath.Join(resources, "bootstrap-state", bootstrapstate.BundleFile)
	incomplete := completeDesktopOperationalSecrets(nil)
	delete(incomplete, "telegram-account-credentials-v1")
	require.NoError(t, bootstrapstate.Pack(ctx, bootstrapstate.PackConfig{
		SourceData: source, OutputPath: bundlePath, BundleID: "seed", AppVersion: "0.8.2",
		Key: seedKey, Secrets: &bootstrapTestSecrets{values: incomplete},
	}))
	stored := &bootstrapTestSecrets{}
	runtimeCalls := 0

	program, err := newDesktopProgram(publicBuildInfo(), true, func() (*DesktopApp, error) {
		imported, importErr := importBundledDesktopState(
			ctx, publicBuildInfo(), desktopPaths{root: root, resources: resources},
			testSeedGrantProvider{grant: license.SeedGrant{ID: "seed", Key: seedKey}}, stored,
		)
		if importErr != nil {
			return nil, importErr
		}
		if !imported {
			return nil, errDesktopSeedUnavailable
		}
		runtimeCalls++
		return &DesktopApp{}, nil
	})

	require.NoError(t, err)
	require.Equal(t, desktopModeRecovery, program.mode)
	require.Equal(t, desktopRecoverySeedIncompatible, program.recoveryCode)
	require.Zero(t, runtimeCalls)
	require.Empty(t, stored.values)
	require.NoDirExists(t, filepath.Join(root, "data"))
	require.NoDirExists(t, filepath.Join(root, "bootstrap-state"))
}

func TestWrongLengthPublicSeedIsIncompatibleAndPreservesStoredSecrets(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	resources := t.TempDir()
	source := filepath.Join(t.TempDir(), "data")
	require.NoError(t, os.MkdirAll(source, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(source, "app.db"), []byte("seed-db"), 0o600))
	seedKey := []byte("0123456789abcdef0123456789abcdef")
	bundlePath := filepath.Join(resources, "bootstrap-state", bootstrapstate.BundleFile)
	invalid := completeDesktopOperationalSecrets(nil)
	invalid["scout-message-key"] = bytes.Repeat([]byte{0xa5}, 31)
	require.NoError(t, bootstrapstate.Pack(ctx, bootstrapstate.PackConfig{
		SourceData: source, OutputPath: bundlePath, BundleID: "seed", AppVersion: "0.8.2",
		Key: seedKey, Secrets: &bootstrapTestSecrets{values: invalid},
	}))
	prior := completeDesktopOperationalSecrets(map[string][]byte{
		"scout-message-key": bytes.Repeat([]byte{0x77}, 32),
	})
	prior["unrelated-secret"] = []byte("preserve-unrelated")
	stored := &bootstrapTestSecrets{values: cloneDesktopSecrets(prior)}
	runtimeCalls := 0

	program, err := newDesktopProgram(publicBuildInfo(), true, func() (*DesktopApp, error) {
		imported, importErr := importBundledDesktopState(
			ctx, publicBuildInfo(), desktopPaths{root: root, resources: resources},
			testSeedGrantProvider{grant: license.SeedGrant{ID: "seed", Key: seedKey}}, stored,
		)
		if importErr != nil {
			return nil, importErr
		}
		if !imported {
			return nil, errDesktopSeedUnavailable
		}
		runtimeCalls++
		return &DesktopApp{}, nil
	})

	require.NoError(t, err)
	require.Equal(t, desktopModeRecovery, program.mode)
	require.Equal(t, desktopRecoverySeedIncompatible, program.recoveryCode)
	require.Zero(t, runtimeCalls)
	require.Equal(t, prior, stored.values)
	require.NoDirExists(t, filepath.Join(root, "data"))
	require.NoDirExists(t, filepath.Join(root, "bootstrap-state"))
}

func TestImportBundledDesktopStateRejectsIncompatiblePublicBundles(t *testing.T) {
	ctx := context.Background()
	seedKey := []byte("0123456789abcdef0123456789abcdef")
	tests := []struct {
		name      string
		bundleID  string
		bundleVer string
		grant     license.SeedGrant
		corrupt   bool
	}{
		{name: "wrong key", bundleID: "seed", bundleVer: "0.8.2", grant: license.SeedGrant{ID: "seed", Key: []byte("abcdef0123456789abcdef0123456789")}},
		{name: "wrong id", bundleID: "seed", bundleVer: "0.8.2", grant: license.SeedGrant{ID: "other", Key: seedKey}},
		{name: "wrong version", bundleID: "seed", bundleVer: "0.7.0", grant: license.SeedGrant{ID: "seed", Key: seedKey}},
		{name: "corrupt", bundleID: "seed", bundleVer: "0.8.2", grant: license.SeedGrant{ID: "seed", Key: seedKey}, corrupt: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resources := t.TempDir()
			bundlePath := filepath.Join(resources, "bootstrap-state", bootstrapstate.BundleFile)
			require.NoError(t, os.MkdirAll(filepath.Dir(bundlePath), 0o700))
			if test.corrupt {
				require.NoError(t, os.WriteFile(bundlePath, []byte("corrupt-secret-/Users/private"), 0o600))
			} else {
				source := filepath.Join(t.TempDir(), "data")
				require.NoError(t, os.MkdirAll(source, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(source, "app.db"), []byte("seed-db"), 0o600))
				require.NoError(t, bootstrapstate.Pack(ctx, bootstrapstate.PackConfig{
					SourceData: source, OutputPath: bundlePath, BundleID: test.bundleID,
					AppVersion: test.bundleVer, Key: seedKey, Secrets: &bootstrapTestSecrets{},
				}))
			}
			root := t.TempDir()
			before := snapshotDesktopTree(t, root)

			imported, err := importBundledDesktopState(ctx, publicBuildInfo(), desktopPaths{root: root, resources: resources}, testSeedGrantProvider{grant: test.grant}, &bootstrapTestSecrets{})

			require.False(t, imported)
			require.Equal(t, desktopRecoverySeedIncompatible, desktopRecoveryCodeForError(err))
			require.Equal(t, before, snapshotDesktopTree(t, root))
		})
	}
}

func TestImportBundledDesktopStateBlocksUnknownProfileWithoutGrantOrMutation(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "unknown-partial-profile"), []byte("keep"), 0o600))
	before := snapshotDesktopTree(t, root)

	imported, err := importBundledDesktopState(context.Background(), publicBuildInfo(), desktopPaths{root: root, resources: t.TempDir()}, nil, nil)

	require.False(t, imported)
	require.Equal(t, desktopRecoveryProfileBlocked, desktopRecoveryCodeForError(err))
	require.Equal(t, before, snapshotDesktopTree(t, root))
}

func TestImportBundledDesktopStateRecognizedProfileBypassesBundleAndGrant(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "data", "sessions"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "data", "sessions", "existing.session"), []byte("keep"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bootstrap-state"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bootstrap-state", "imported.json"), []byte(`{"schema_version":1,"bundle_id":"seed","app_version":"0.8.2","created_at":"2026-08-13T12:00:00Z"}`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "proxy", "tor-snowflake", "data"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "proxy", "tor-snowflake", "torrc"), []byte("managed-runtime"), 0o600))
	before := snapshotDesktopTree(t, root)

	imported, err := importBundledDesktopState(context.Background(), publicBuildInfo(), desktopPaths{root: root, resources: t.TempDir()}, nil, nil)

	require.NoError(t, err)
	require.False(t, imported)
	require.Equal(t, before, snapshotDesktopTree(t, root))
}

func completeDesktopOperationalSecrets(overrides map[string][]byte) map[string][]byte {
	values := map[string][]byte{
		"outbound-target-key":             bytes.Repeat([]byte{0x11}, 32),
		"proxy-credentials-v1":            bytes.Repeat([]byte{0x22}, 32),
		"scout-message-key":               bytes.Repeat([]byte{0x33}, 32),
		"telegram-account-credentials-v1": bytes.Repeat([]byte{0x44}, 32),
	}
	for name, value := range overrides {
		values[name] = append([]byte(nil), value...)
	}
	return values
}

func cloneDesktopSecrets(values map[string][]byte) map[string][]byte {
	cloned := make(map[string][]byte, len(values))
	for name, value := range values {
		cloned[name] = append([]byte(nil), value...)
	}
	return cloned
}

func snapshotDesktopTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	require.NoError(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot[relative] = string(data)
		} else {
			snapshot[relative] = entry.Type().String()
		}
		return nil
	}))
	return snapshot
}

type testSeedGrantProvider struct {
	grant license.SeedGrant
	err   error
}

func (s testSeedGrantProvider) SeedGrant() (license.SeedGrant, error) {
	return s.grant, s.err
}

type countingSeedGrantProvider struct{ calls int }

func (s *countingSeedGrantProvider) SeedGrant() (license.SeedGrant, error) {
	s.calls++
	return license.SeedGrant{}, license.ErrNoSeedGrant
}

type persistentDesktopSecrets struct {
	values map[string][]byte
	calls  map[string]int
}

func requireEmptyDesktopRuntime(t *testing.T, app *DesktopApp) {
	t.Helper()
	_, err := app.bindings.GetDriveAccountImportStatus()
	require.NoError(t, err)
	accounts, err := app.bindings.GetAccounts()
	require.NoError(t, err)
	require.Empty(t, accounts)
	for _, catalog := range []string{string(domain.SourceCatalogOutbound), string(domain.SourceCatalogScout)} {
		rows, err := app.bindings.GetCatalog(catalog)
		require.NoError(t, err)
		require.Empty(t, rows)
	}
}

func (s *persistentDesktopSecrets) GetOrCreate(_ context.Context, name string, size int) ([]byte, error) {
	s.calls[name]++
	value, found := s.values[name]
	if !found {
		value = make([]byte, size)
		s.values[name] = value
	}
	return append([]byte(nil), value...), nil
}

func TestNewDesktopUpdateServiceOnlyEnablesAuthorizedPublicWorkspace(t *testing.T) {
	tests := []struct {
		name    string
		info    buildinfo.Info
		program desktopProgram
		want    updater.State
		wantNil bool
	}{
		{
			name:    "public activation screen",
			info:    publicBuildInfo(),
			program: desktopProgram{mode: desktopModeActivation},
			wantNil: true,
		},
		{
			name:    "authorized public workspace",
			info:    publicBuildInfo(),
			program: desktopProgram{mode: desktopModeWorkspace},
			want:    updater.StateIdle,
		},
		{
			name: "internal workspace",
			info: buildinfo.Info{
				Channel:   buildinfo.ChannelInternal,
				ProductID: buildinfo.ProductID,
				Version:   "0.7.0",
			},
			program: desktopProgram{mode: desktopModeWorkspace},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newDesktopUpdateService(tt.info, tt.program)
			if tt.wantNil {
				require.Nil(t, service)
				return
			}
			require.NotNil(t, service)
			t.Cleanup(service.Stop)
			require.Equal(t, tt.want, service.Snapshot().State)
		})
	}
}

func TestNewDesktopUpdateServiceDoesNotConstructDriverBeforeAuthorizedWorkspace(t *testing.T) {
	tests := []struct {
		name      string
		info      buildinfo.Info
		program   desktopProgram
		wantCalls int
	}{
		{name: "public activation", info: publicBuildInfo(), program: desktopProgram{mode: desktopModeActivation}},
		{
			name:    "internal workspace",
			info:    buildinfo.Info{Channel: buildinfo.ChannelInternal, ProductID: buildinfo.ProductID},
			program: desktopProgram{mode: desktopModeWorkspace},
		},
		{name: "public workspace", info: publicBuildInfo(), program: desktopProgram{mode: desktopModeWorkspace}, wantCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			var appcastURL string
			service := newDesktopUpdateServiceWithDriver(tt.info, tt.program, func(value string) updater.Driver {
				calls++
				appcastURL = value
				return desktopUpdateDriver{}
			})
			if tt.wantCalls == 0 {
				require.Nil(t, service)
			} else {
				require.NotNil(t, service)
				t.Cleanup(service.Stop)
			}

			require.Equal(t, tt.wantCalls, calls)
			if tt.wantCalls == 1 {
				require.Equal(t, tt.info.AppcastURL, appcastURL)
			}
		})
	}
}

type desktopUpdateDriver struct{}

func (desktopUpdateDriver) Check(context.Context) (updater.Update, error) {
	return updater.Update{}, nil
}
func (desktopUpdateDriver) Download(context.Context, func(int)) error { return nil }
func (desktopUpdateDriver) Install(context.Context) error             { return nil }
func (desktopUpdateDriver) Stop() error                               { return nil }
