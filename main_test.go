//go:build desktop

package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gotd/td/tgerr"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/wailsapp/wails/v2/pkg/options"
	bolt "go.etcd.io/bbolt"
	"telegram-companion/internal/domain"
	proxyroutes "telegram-companion/internal/proxy/routes"
	"telegram-companion/internal/repository/sqlite"
	sqlitemigrations "telegram-companion/internal/repository/sqlite/migrations"
	messagecrypto "telegram-companion/internal/service/crypto"
	telegramgotd "telegram-companion/internal/telegram/gotd"
	wailsbindings "telegram-companion/internal/transport/wails"
	"telegram-companion/internal/usecase"
	analyticsusecase "telegram-companion/internal/usecase/analytics"
	"telegram-companion/internal/usecase/runtimeconfig"
	"telegram-companion/internal/usecase/scouting"
)

func TestResolveDesktopPathsUsesStableConfigRootOrExplicitOverride(t *testing.T) {
	paths, err := resolveDesktopPaths("", filepath.Join("/home", "user", ".config"), filepath.Join("/opt", "Telegram Companion", "telegram-companion"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/home", "user", ".config", "telegram-companion"), paths.root)
	require.Equal(t, filepath.Join("/opt", "Telegram Companion"), paths.resources)

	paths, err = resolveDesktopPaths(filepath.Join("/srv", "companion"), filepath.Join("/ignored", "config"), filepath.Join("/opt", "app"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/srv", "companion"), paths.root)
	_, err = resolveDesktopPaths("relative-root", "/config", "/opt/app")
	require.ErrorContains(t, err, "absolute")
}

func TestDesktopLogOutputWritesOwnerOnlyDiagnosticLog(t *testing.T) {
	root := t.TempDir()
	output, closeOutput := desktopLogOutput(root)
	t.Cleanup(closeOutput)

	_, err := io.WriteString(output, "connection diagnostic\n")
	require.NoError(t, err)

	path := filepath.Join(root, "logs", "desktop.log")
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "connection diagnostic\n", string(contents))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestAccountStatusErrorCodeClassifiesUnavailableProxyRoute(t *testing.T) {
	err := fmt.Errorf("resolve account route: %w", proxyroutes.ErrRouteUnavailable)
	require.Equal(t, "proxy_unavailable", accountStatusErrorCode(err))
}

func TestResolveDesktopPathsUsesMacAppResourcesForPackagedModel(t *testing.T) {
	executable := filepath.Join("/Applications", "Telegram Companion.app", "Contents", "MacOS", "telegram-companion")
	paths, err := resolveDesktopPaths("", filepath.Join("/Users", "operator", "Library", "Application Support"), executable)
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/Applications", "Telegram Companion.app", "Contents", "Resources"), paths.resources)
	require.Equal(t, filepath.Join(paths.resources, "models", "multilingual-e5-small"), modelDirectory(paths.resources))
}

func TestMigrateLegacyWorkingDataCopiesOnceWithPrivateModes(t *testing.T) {
	legacy := t.TempDir()
	stable := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(legacy, "data", "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "data", "nested", "state.db"), []byte("legacy"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "data", "application-state.bolt"), []byte("legacy-state"), 0o600))

	require.NoError(t, migrateLegacyWorkingData(legacy, stable))
	destination := filepath.Join(stable, "data", "nested", "state.db")
	content, err := os.ReadFile(destination)
	require.NoError(t, err)
	require.Equal(t, "legacy", string(content))
	info, err := os.Stat(destination)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.NoError(t, os.WriteFile(destination, []byte("stable"), 0o600))
	require.NoError(t, migrateLegacyWorkingData(legacy, stable))
	content, err = os.ReadFile(destination)
	require.NoError(t, err)
	require.Equal(t, "stable", string(content), "one-time migration must not overwrite stable state")
	_, err = os.Stat(filepath.Join(legacy, "data", "nested", "state.db"))
	require.NoError(t, err, "legacy state must remain available after safe copy")
}

func TestMigrateLegacyWorkingDataSkipsDevelopmentArtifactsAndSymlinks(t *testing.T) {
	legacy := t.TempDir()
	stable := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(legacy, "data", "gotd-import-staging", "batch", "source", "account-0"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(legacy, "data", "acceptance-venv", "bin"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "data", "application-state.bolt"), []byte("legacy-state"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "data", "gotd-import-staging", "batch", "source", "account-0", "session.json"), []byte("session"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "data", "acceptance-venv", "bin", "python"), []byte("test-runtime"), 0o700))
	require.NoError(t, os.Symlink("lib", filepath.Join(legacy, "data", "acceptance-venv", "lib64")))

	require.NoError(t, migrateLegacyWorkingData(legacy, stable))
	content, err := os.ReadFile(filepath.Join(stable, "data", "gotd-import-staging", "batch", "source", "account-0", "session.json"))
	require.NoError(t, err)
	require.Equal(t, "session", string(content))
	_, err = os.Stat(filepath.Join(stable, "data", "acceptance-venv"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestMigrateLegacyWorkingDataRepairsMissingGotdStagingWithoutOverwritingStableData(t *testing.T) {
	legacy := t.TempDir()
	stable := t.TempDir()
	sessionDir := filepath.Join(legacy, "data", "gotd-import-staging", "batch", "source", "account-0")
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "session.json"), []byte("session"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "data", "application-state.bolt"), []byte("legacy-state"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(stable, "data"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(stable, "data", "app.db"), []byte("stable"), 0o600))

	require.NoError(t, migrateLegacyWorkingData(legacy, stable))
	stableDB, err := os.ReadFile(filepath.Join(stable, "data", "app.db"))
	require.NoError(t, err)
	require.Equal(t, "stable", string(stableDB))
	session, err := os.ReadFile(filepath.Join(stable, "data", "gotd-import-staging", "batch", "source", "account-0", "session.json"))
	require.NoError(t, err)
	require.Equal(t, "session", string(session))
}

func TestDiscoveredLegacyMigrationIgnoresLaunchCWDAndUsesKnownHomeRoot(t *testing.T) {
	home := t.TempDir()
	stable := filepath.Join(home, ".config", "telegram-companion")
	legacy := filepath.Join(home, "projects", "telegram-companion")
	unrelated := filepath.Join(home, "Downloads", "unrelated-launch")
	for root, content := range map[string]string{legacy: "intended", unrelated: "wrong-cwd"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "data"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(root, "data", "application-state.bolt"), []byte(content), 0o600))
	}
	t.Chdir(unrelated)

	require.NoError(t, migrateDiscoveredLegacyData(home, filepath.Join(home, "Applications", "telegram-companion"), stable))
	content, err := os.ReadFile(filepath.Join(stable, "data", "application-state.bolt"))
	require.NoError(t, err)
	require.Equal(t, "intended", string(content))
}

func TestDiscoveredLegacyMigrationRepairsStagingFromLaterCandidate(t *testing.T) {
	home := t.TempDir()
	stable := filepath.Join(home, ".config", "telegram-companion")
	first := filepath.Join(home, "projects", "telegram-companion", "data")
	second := filepath.Join(home, "Documents", "telegram-companion", "data")
	for _, root := range []string{first, second} {
		require.NoError(t, os.MkdirAll(root, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(root, "application-state.bolt"), []byte("marker"), 0o600))
	}
	session := filepath.Join(second, "gotd-import-staging", "batch", "source", "account-0", "session.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(session), 0o700))
	require.NoError(t, os.WriteFile(session, []byte("session"), 0o600))

	require.NoError(t, migrateDiscoveredLegacyData(home, filepath.Join(home, "Applications", "telegram-companion"), stable))
	content, err := os.ReadFile(filepath.Join(stable, "data", "gotd-import-staging", "batch", "source", "account-0", "session.json"))
	require.NoError(t, err)
	require.Equal(t, "session", string(content))
}

func TestDiscoveredLegacyMigrationRejectsUnmarkedDataDirectory(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, "projects", "telegram-companion")
	stable := filepath.Join(home, ".config", "telegram-companion")
	require.NoError(t, os.MkdirAll(filepath.Join(legacy, "data", "nested"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "data", "nested", "state.db"), []byte("unrelated"), 0o600))

	require.NoError(t, migrateDiscoveredLegacyData(home, filepath.Join(home, "Applications", "telegram-companion"), stable))
	_, err := os.Stat(filepath.Join(stable, "data"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestLegacyMigrationRecognizesHistoricalBoltAppDatabase(t *testing.T) {
	legacy := t.TempDir()
	stable := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(legacy, "data"), 0o700))
	legacyDB := filepath.Join(legacy, "data", "app.db")
	db, err := bolt.Open(legacyDB, 0o600, nil)
	require.NoError(t, err)
	require.NoError(t, db.Update(func(tx *bolt.Tx) error {
		_, createErr := tx.CreateBucket([]byte("settings"))
		return createErr
	}))
	require.NoError(t, db.Close())

	require.NoError(t, migrateLegacyWorkingData(legacy, stable))
	_, err = os.Stat(filepath.Join(stable, "data", "app.db"))
	require.NoError(t, err)
}

func TestAccountStatusLogNeverIncludesUnderlyingIdentityOrSessionPath(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	logAccountStatus(logger, telegramgotd.AccountStatusEvent{
		AccountID: "opaque-account", Status: "backoff", Revision: 7,
		Err: &os.PathError{Op: "lstat", Path: "/Users/private/+79990001122/session.json", Err: os.ErrPermission},
	})
	require.NotContains(t, output.String(), "/Users/private")
	require.NotContains(t, output.String(), "+79990001122")
	require.NotContains(t, output.String(), "permission denied")
	require.Contains(t, output.String(), `"error_code":"session_io"`)
}

func TestAccountStatusLogClassifiesNetworkErrorsWithoutLeakingDetails(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	logAccountStatus(logger, telegramgotd.AccountStatusEvent{
		AccountID: "opaque-account", Status: telegramgotd.RuntimeBackoff,
		Err: &net.OpError{
			Op:  "dial",
			Net: "tcp",
			Err: errors.New("temporary connection detail"),
		},
	})

	require.Contains(t, output.String(), `"error_kind":"network"`)
	require.NotContains(t, output.String(), "temporary connection detail")
}

type runtimeStatusRecorderFake struct {
	accountID domain.ID
	status    string
	errorCode string
}

func (f *runtimeStatusRecorderFake) RecordRuntimeStatus(_ context.Context, accountID domain.ID, status, errorCode string, _ time.Time) error {
	f.accountID = accountID
	f.status = status
	f.errorCode = errorCode
	return nil
}

func TestAccountStatusCallbackPersistsSanitizedRuntimeState(t *testing.T) {
	recorder := &runtimeStatusRecorderFake{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	event := telegramgotd.AccountStatusEvent{
		AccountID: "opaque-account", Status: telegramgotd.RuntimeBackoff,
		Err: &os.PathError{Op: "lstat", Path: "/Users/private/+79990001122/session.json", Err: os.ErrPermission},
	}

	require.NoError(t, persistAndLogAccountStatus(context.Background(), recorder, logger, event))
	require.Equal(t, domain.ID("opaque-account"), recorder.accountID)
	require.Equal(t, "partial", recorder.status)
	require.Equal(t, "session_io", recorder.errorCode)
}

func TestAccountStatusCallbackPersistsDuplicatedSessionAsTerminalError(t *testing.T) {
	recorder := &runtimeStatusRecorderFake{}
	event := telegramgotd.AccountStatusEvent{
		AccountID: "opaque-account",
		Status:    telegramgotd.RuntimeSessionInvalid,
		Err:       tgerr.New(406, "AUTH_KEY_DUPLICATED"),
	}

	require.NoError(t, persistAndLogAccountStatus(context.Background(), recorder, slog.New(slog.NewTextHandler(io.Discard, nil)), event))
	require.Equal(t, "error", recorder.status)
	require.Equal(t, "rpc_auth_key_duplicated", recorder.errorCode)
}

func TestAccountStatusCallbackDoesNotReportReadyBeforeConnected(t *testing.T) {
	recorder := &runtimeStatusRecorderFake{}
	event := telegramgotd.AccountStatusEvent{AccountID: "opaque-account", Status: telegramgotd.RuntimeConnecting}

	require.NoError(t, persistAndLogAccountStatus(context.Background(), recorder, slog.New(slog.NewTextHandler(io.Discard, nil)), event))
	require.Equal(t, "joining", recorder.status)
	require.NotEqual(t, "ready", recorder.status)
}

func TestAccountStatusErrorCodeUsesSafeTelegramRPCType(t *testing.T) {
	require.Equal(t, "rpc_channel_private", accountStatusErrorCode(tgerr.New(400, "CHANNEL_PRIVATE")))
	require.Equal(t, "runtime_error", accountStatusErrorCode(errors.New("internal detail must remain private")))
}

type floodWaitStatusRecorderFake struct {
	runtimeStatusRecorderFake
	floodWaitUntil time.Time
}

func (f *floodWaitStatusRecorderFake) RecordFloodWait(_ context.Context, accountID domain.ID, until, _ time.Time) error {
	f.accountID = accountID
	f.floodWaitUntil = until
	return nil
}

func TestAccountStatusCallbackPersistsTelegramFloodWait(t *testing.T) {
	recorder := &floodWaitStatusRecorderFake{}
	before := time.Now().UTC()
	event := telegramgotd.AccountStatusEvent{
		AccountID: "opaque-account",
		Status:    telegramgotd.RuntimeBackoff,
		Err:       &telegramgotd.FloodWaitError{Duration: 45 * time.Second},
	}

	require.NoError(t, persistAndLogAccountStatus(context.Background(), recorder, slog.New(slog.NewTextHandler(io.Discard, nil)), event))
	require.Equal(t, domain.ID("opaque-account"), recorder.accountID)
	require.True(t, recorder.floodWaitUntil.After(before.Add(44*time.Second)))
	require.Empty(t, recorder.status)
}

func TestRuntimeStoppedRemainsEligibleAcrossStopStartAndProcessRestart(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	store := sqlite.NewProductionStore(db)
	now := time.Date(2026, 7, 12, 18, 0, 0, 0, time.UTC)
	require.NoError(t, store.EnsureAccounts(ctx, []domain.Account{{
		ID: "opaque-account", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/session", CreatedAt: now, UpdatedAt: now,
	}}))

	require.NoError(t, persistAndLogAccountStatus(ctx, store.Accounts(), slog.New(slog.NewTextHandler(io.Discard, nil)), telegramgotd.AccountStatusEvent{
		AccountID: "opaque-account", Status: telegramgotd.RuntimeStopped,
	}))
	active, err := store.Accounts().ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, domain.AccountStatus("stopped"), active[0].Status)
}

type rootRetentionPruner struct{ called chan time.Time }

func (p *rootRetentionPruner) PruneBefore(_ context.Context, before time.Time) (int64, error) {
	p.called <- before
	return 0, nil
}

func TestStorageRetentionUsesDesktopRootLifecycle(t *testing.T) {
	pruner := &rootRetentionPruner{called: make(chan time.Time, 1)}
	app := &DesktopApp{
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		retention: scouting.NewRetention(pruner, systemClock{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.startStorageMaintenance(ctx)
	select {
	case before := <-pruner.called:
		require.WithinDuration(t, time.Now().Add(-scouting.RetentionPeriod), before, time.Second)
	case <-time.After(time.Second):
		t.Fatal("root retention did not run")
	}
	app.stopStorageMaintenance()
}

type desktopTestSecrets struct{}

type recordingProxySecrets struct {
	name string
	size int
}

func (s *recordingProxySecrets) GetOrCreate(_ context.Context, name string, size int) ([]byte, error) {
	s.name = name
	s.size = size
	return make([]byte, size), nil
}

type shutdownAnalytics struct {
	started  chan struct{}
	finished chan struct{}
}

func (s *shutdownAnalytics) Analyze(ctx context.Context, _ analyticsusecase.AnalysisRequest) (analyticsusecase.AnalysisResult, error) {
	close(s.started)
	<-ctx.Done()
	time.Sleep(25 * time.Millisecond)
	close(s.finished)
	return analyticsusecase.AnalysisResult{}, ctx.Err()
}
func (*shutdownAnalytics) CompareTopics(context.Context, analyticsusecase.TopicComparisonRequest) ([]analyticsusecase.TopicSummary, error) {
	return nil, nil
}
func (*shutdownAnalytics) ModerationStates(context.Context) (map[analyticsusecase.CandidateKey]string, error) {
	return nil, nil
}
func (*shutdownAnalytics) Moderate(context.Context, string, string, string) error { return nil }
func (*shutdownAnalytics) AddCandidatesToKeywords(context.Context, []analyticsusecase.CandidateRef) (int, error) {
	return 0, nil
}

func (desktopTestSecrets) GetOrCreate(_ context.Context, _ string, size int) ([]byte, error) {
	return make([]byte, size), nil
}

func TestNewProxyProfileStoreUsesDedicatedKeyringSecret(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	secrets := &recordingProxySecrets{}

	store, err := newProxyProfileStore(ctx, db, secrets)
	require.NoError(t, err)
	require.NotNil(t, store)
	require.Equal(t, "proxy-credentials-v1", secrets.name)
	require.Equal(t, 32, secrets.size)
}

func TestDesktopAppStartupKeepsAutomationStopped(t *testing.T) {
	started := make(chan struct{})
	runner := usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	controller := usecase.NewAutomationController(runner)
	app := &DesktopApp{bindings: wailsbindings.NewBindings(controller), log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	app.Startup(context.Background())
	select {
	case <-started:
		t.Fatal("desktop startup unexpectedly started automation")
	case <-time.After(50 * time.Millisecond):
	}
	require.False(t, controller.Running())
}

func TestDesktopAppStartupStartsConnectivityButKeepsMessageAutomationStopped(t *testing.T) {
	connectivityStarted := make(chan struct{})
	connectivity := usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		close(connectivityStarted)
		<-ctx.Done()
		return ctx.Err()
	}))
	messagesStarted := make(chan struct{})
	messages := usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		close(messagesStarted)
		<-ctx.Done()
		return ctx.Err()
	}))
	app := &DesktopApp{
		bindings: wailsbindings.NewBindings(messages), connectivity: connectivity,
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	app.Startup(context.Background())
	select {
	case <-connectivityStarted:
	case <-time.After(time.Second):
		t.Fatal("desktop startup did not start Telegram connectivity")
	}
	select {
	case <-messagesStarted:
		t.Fatal("desktop startup unexpectedly started message automation")
	case <-time.After(50 * time.Millisecond):
	}
	require.True(t, connectivity.Running())
	require.False(t, messages.Running())

	app.Shutdown(context.Background())
	require.False(t, connectivity.Running())
}

func TestDesktopAppConnectivitySurvivesStartupContextCancellation(t *testing.T) {
	connectivityStarted := make(chan struct{})
	connectivity := usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		close(connectivityStarted)
		<-ctx.Done()
		return ctx.Err()
	}))
	app := &DesktopApp{
		bindings:     wailsbindings.NewBindings(usecase.NewAutomationController(nil)),
		connectivity: connectivity,
		log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	startupCtx, cancelStartup := context.WithCancel(context.Background())

	app.Startup(startupCtx)
	select {
	case <-connectivityStarted:
	case <-time.After(time.Second):
		t.Fatal("desktop startup did not start Telegram connectivity")
	}
	cancelStartup()

	require.Never(t, func() bool { return !connectivity.Running() }, 100*time.Millisecond, 5*time.Millisecond)
	app.Shutdown(context.Background())
	require.False(t, connectivity.Running())
}

type failThenBlockAutomation struct {
	calls   atomic.Int32
	started chan struct{}
}

func (r *failThenBlockAutomation) Run(ctx context.Context) error {
	if r.calls.Add(1) == 1 {
		return errors.New("temporary connectivity failure")
	}
	close(r.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestResilientAutomationRestartsConnectivityAfterTransientFailure(t *testing.T) {
	inner := &failThenBlockAutomation{started: make(chan struct{})}
	runner := &resilientAutomation{
		runner:     inner,
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		retryDelay: time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	select {
	case <-inner.started:
	case <-time.After(time.Second):
		t.Fatal("connectivity was not restarted after a transient failure")
	}
	require.EqualValues(t, 2, inner.calls.Load())
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestMessageAutomationEnablesOutboundOnlyWhileDeliveryRuns(t *testing.T) {
	config := runtimeconfig.NewStore(runtimeconfig.Snapshot{OutboundPaused: true})
	deliveryStarted := make(chan struct{})
	runner := &messageAutomation{
		config: config,
		delivery: usecase.AutomationRunnerFunc(func(ctx context.Context) error {
			close(deliveryStarted)
			<-ctx.Done()
			return ctx.Err()
		}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	select {
	case <-deliveryStarted:
	case <-time.After(time.Second):
		t.Fatal("delivery did not start")
	}
	require.False(t, config.Current().OutboundPaused)

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.True(t, config.Current().OutboundPaused)
}

type analyticsSchedulerLifecycleFake struct {
	restored        chan struct{}
	stopped         chan struct{}
	stopErr         error
	waitForDeadline bool
}

func (f *analyticsSchedulerLifecycleFake) Restore(context.Context) error {
	if f.restored != nil {
		close(f.restored)
	}
	return nil
}

func (f *analyticsSchedulerLifecycleFake) Shutdown(ctx context.Context) error {
	if f.stopped != nil {
		close(f.stopped)
	}
	if f.waitForDeadline {
		<-ctx.Done()
		return ctx.Err()
	}
	return f.stopErr
}

func TestDesktopAppOwnsAnalyticsSchedulerLifecycleIndependently(t *testing.T) {
	runner := usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	lifecycle := &analyticsSchedulerLifecycleFake{restored: make(chan struct{}), stopped: make(chan struct{})}
	app := &DesktopApp{
		bindings: wailsbindings.NewBindings(runner), analyticsScheduler: lifecycle,
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	app.Startup(context.Background())
	select {
	case <-lifecycle.restored:
	case <-time.After(time.Second):
		t.Fatal("analytics scheduler was not restored at startup")
	}
	app.Shutdown(context.Background())
	select {
	case <-lifecycle.stopped:
	case <-time.After(time.Second):
		t.Fatal("analytics scheduler was not stopped at shutdown")
	}
}

func TestDesktopAppShutdownContinuesAfterAnalyticsSchedulerFailure(t *testing.T) {
	runner := usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	require.NoError(t, runner.Start(context.Background()))
	closed := false
	app := &DesktopApp{
		bindings:           wailsbindings.NewBindings(runner),
		analyticsScheduler: &analyticsSchedulerLifecycleFake{waitForDeadline: true},
		log:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		shutdownTimeout:    time.Millisecond,
		close: []func() error{func() error {
			closed = true
			return nil
		}},
	}

	app.Shutdown(context.Background())

	require.False(t, runner.Running())
	require.True(t, closed)
}

func TestDesktopAppStartupMaximisesWindow(t *testing.T) {
	controller := usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	maximised := false
	app := &DesktopApp{
		bindings: wailsbindings.NewBindings(controller),
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		maximizeWindow: func(context.Context) {
			maximised = true
		},
	}

	app.Startup(context.Background())
	require.True(t, maximised)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, controller.Stop(ctx))
}

func TestRunDesktopReturnsWailsRuntimeFailure(t *testing.T) {
	want := errors.New("wails runtime failed")
	app := &DesktopApp{
		bindings: wailsbindings.NewBindings(usecase.NewAutomationController(nil)),
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := runDesktop(app, func(*options.App) error { return want })

	require.ErrorIs(t, err, want)
}

func TestRunDesktopStartsMaximised(t *testing.T) {
	app := &DesktopApp{
		bindings: wailsbindings.NewBindings(usecase.NewAutomationController(nil)),
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	var received *options.App

	require.NoError(t, runDesktop(app, func(config *options.App) error {
		received = config
		return nil
	}))
	require.NotNil(t, received)
	require.Equal(t, options.Maximised, received.WindowStartState)
}

func TestRunDesktopEnablesNativeMacFullscreenControls(t *testing.T) {
	app := &DesktopApp{
		bindings: wailsbindings.NewBindings(usecase.NewAutomationController(nil)),
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	var received *options.App

	require.NoError(t, runDesktop(app, func(config *options.App) error {
		received = config
		return nil
	}))
	require.NotNil(t, received)
	require.NotNil(t, received.Mac)
	require.False(t, received.Mac.DisableZoom)
	require.False(t, received.Mac.DisableEscapeExitsFullscreen)
}

func TestWithDesktopSingleInstanceConfiguresLockAndActivatesExistingWindow(t *testing.T) {
	var received *options.App
	var calls []string
	var activatedContexts []context.Context
	originalStartupCalled := false
	runner := withDesktopSingleInstance(
		func(config *options.App) error {
			received = config
			return nil
		},
		func(ctx context.Context) {
			calls = append(calls, "unminimise")
			activatedContexts = append(activatedContexts, ctx)
		},
		func(ctx context.Context) {
			calls = append(calls, "show")
			activatedContexts = append(activatedContexts, ctx)
		},
	)
	config := &options.App{
		OnStartup: func(context.Context) {
			originalStartupCalled = true
		},
	}

	require.NoError(t, runner(config))
	require.Same(t, config, received)
	require.NotNil(t, received.SingleInstanceLock)
	require.Equal(t, "b9f6d837-c30e-46e0-a718-f7a3d1e2c689", received.SingleInstanceLock.UniqueId)
	require.NotNil(t, received.SingleInstanceLock.OnSecondInstanceLaunch)

	received.SingleInstanceLock.OnSecondInstanceLaunch(options.SecondInstanceData{})
	require.Empty(t, calls)

	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "primary")
	received.OnStartup(ctx)

	require.True(t, originalStartupCalled)
	require.Equal(t, []string{"unminimise", "show"}, calls)
	require.Equal(t, []context.Context{ctx, ctx}, activatedContexts)

	calls = nil
	activatedContexts = nil
	received.SingleInstanceLock.OnSecondInstanceLaunch(options.SecondInstanceData{})

	require.Equal(t, []string{"unminimise", "show"}, calls)
	require.Equal(t, []context.Context{ctx, ctx}, activatedContexts)
}

func TestNativeDialogTitlesFollowSelectedLocale(t *testing.T) {
	require.Equal(t, "Импорт данных AI", nativeDialogTitle("ru", "import"))
	require.Equal(t, "Экспорт кандидатов", nativeDialogTitle("ru", "export"))
	require.Equal(t, "Import AI data", nativeDialogTitle("en", "import"))
	require.Equal(t, "Export candidates", nativeDialogTitle("en", "export"))
}

func TestDesktopAppShutdownClosesResources(t *testing.T) {
	closed := 0
	controller := usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	app := &DesktopApp{
		bindings: wailsbindings.NewBindings(controller),
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		close:    []func() error{func() error { closed++; return nil }},
	}
	app.Shutdown(context.Background())
	app.Shutdown(context.Background())
	if closed != 1 {
		t.Fatalf("desktop shutdown close calls = %d, want 1", closed)
	}
}

func TestDesktopShutdownWaitsForActiveAnalysisBeforeClosingResources(t *testing.T) {
	analysis := &shutdownAnalytics{started: make(chan struct{}), finished: make(chan struct{})}
	bindings := wailsbindings.NewBindings(usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})))
	bindings.ConfigureProduction(nil, analysis, nil, nil)
	closedAfterAnalysis := false
	app := &DesktopApp{
		bindings: bindings,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		close: []func() error{func() error {
			select {
			case <-analysis.finished:
				closedAfterAnalysis = true
			default:
			}
			return nil
		}},
	}
	_, err := bindings.StartAnalysis(wailsbindings.AnalysisRequestDTO{SourceScope: "all"})
	if err != nil {
		t.Fatal(err)
	}
	<-analysis.started

	app.Shutdown(context.Background())

	if !closedAfterAnalysis {
		t.Fatal("desktop resources closed before active analysis exited")
	}
}

type shutdownBlockingBackup struct {
	started chan struct{}
	release chan struct{}
}

func (b *shutdownBlockingBackup) Create(ctx context.Context, _ domain.BackupKind) (domain.BackupRecord, error) {
	close(b.started)
	<-b.release
	return domain.BackupRecord{}, ctx.Err()
}

func (*shutdownBlockingBackup) Restore(context.Context, string, string) error   { return nil }
func (*shutdownBlockingBackup) ExportRecoveryKey(context.Context, string) error { return nil }
func (*shutdownBlockingBackup) HasVerifiedMonthly(context.Context, int, time.Month, *time.Location) (bool, error) {
	return false, nil
}

func TestDesktopShutdownNeverClosesResourcesWhileCancelledOperationStillRuns(t *testing.T) {
	backup := &shutdownBlockingBackup{started: make(chan struct{}), release: make(chan struct{})}
	bindings := wailsbindings.NewBindingsWithBackups(
		usecase.NewAutomationController(nil), nil, usecase.NewBackupService(backup),
	)
	bindings.SetRootContext(context.Background())
	closed := make(chan struct{}, 1)
	app := &DesktopApp{
		bindings:        bindings,
		log:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		shutdownTimeout: 20 * time.Millisecond,
		close:           []func() error{func() error { closed <- struct{}{}; return nil }},
	}
	operationDone := make(chan error, 1)
	go func() {
		_, err := bindings.CreateBackup("daily")
		operationDone <- err
	}()
	<-backup.started

	app.Shutdown(context.Background())
	select {
	case <-closed:
		t.Fatal("desktop closed resources while a cancelled operation still used them")
	default:
	}
	close(backup.release)
	require.ErrorIs(t, <-operationDone, context.Canceled)
}

func TestPromoteLegacyDatabaseMovesBoltBeforeSQLiteOpen(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "app.db")
	legacy := filepath.Join(root, "application-state.bolt")
	if err := os.WriteFile(database, []byte("not-sqlite-legacy-state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := promoteLegacyDatabase(database, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(database); !os.IsNotExist(err) {
		t.Fatalf("app.db still exists: %v", err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("legacy state missing: %v", err)
	}
}

func TestProductionDesktopCompositionPreservesPreexistingAccountsWithoutLocalImportService(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeDesktopTorFixture(t, root)
	expected := seedSyntheticPreexistingAccounts(t, root)
	beforeArtifacts := snapshotExistingAccountArtifacts(t, root)

	app, err := newDesktopAppAt(context.Background(), root, slog.New(slog.NewTextHandler(io.Discard, nil)), desktopTestSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Shutdown(context.Background()) })
	requireNoLocalImportService(t, app)
	if err := app.bindings.ProductionReady(); err != nil {
		t.Fatal(err)
	}
	accounts, err := app.bindings.GetAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != len(expected) {
		t.Fatalf("accounts = %d, want %d", len(accounts), len(expected))
	}
	got := make(map[string]string, len(accounts))
	for _, account := range accounts {
		got[account.ID] = account.Role
	}
	require.Equal(t, expected, got)
	require.Equal(t, beforeArtifacts, snapshotExistingAccountArtifacts(t, root))
	for _, catalog := range []string{string(domain.SourceCatalogOutbound), string(domain.SourceCatalogScout)} {
		rows, err := app.bindings.GetCatalog(catalog)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 0 {
			t.Fatalf("production composition injected %s catalog rows: %#v", catalog, rows)
		}
	}
}

func TestProductionDesktopCompositionStartsWithoutPreloadedAccounts(t *testing.T) {
	root := t.TempDir()
	writeDesktopTorFixture(t, root)
	app, err := newDesktopAppAt(context.Background(), root, slog.New(slog.NewTextHandler(io.Discard, nil)), desktopTestSecrets{})
	require.NoError(t, err)
	t.Cleanup(func() { app.Shutdown(context.Background()) })

	requireNoLocalImportService(t, app)
	accounts, err := app.bindings.GetAccounts()
	require.NoError(t, err)
	require.Empty(t, accounts)
}

func TestProductionDesktopCompositionDoesNotDiscoverStagedSessions(t *testing.T) {
	root := t.TempDir()
	writeDesktopTorFixture(t, root)
	stagedSession := filepath.Join(
		root, "data", "gotd-"+"import-staging", "fixture", "profile",
		"account-0", "session.json",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(stagedSession), 0o700))
	require.NoError(t, os.WriteFile(stagedSession, []byte("synthetic-session-state"), 0o600))
	metadataPath := filepath.Join(root, "data", "tdata", "fixture", "tdata", "account.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(metadataPath), 0o700))
	require.NoError(t, os.WriteFile(
		metadataPath, []byte(`{"app_id":1,"app_hash":"fixture"}`), 0o600,
	))

	app, err := newDesktopAppAt(
		context.Background(), root,
		slog.New(slog.NewTextHandler(io.Discard, nil)), desktopTestSecrets{},
	)
	require.NoError(t, err)
	t.Cleanup(func() { app.Shutdown(context.Background()) })

	accounts, err := app.bindings.GetAccounts()
	require.NoError(t, err)
	require.Empty(t, accounts, "removed New Account staging must not create accounts")
}

func seedSyntheticPreexistingAccounts(t *testing.T, root string) map[string]string {
	t.Helper()
	ctx := context.Background()
	databasePath := filepath.Join(root, "data", "app.db")
	db, err := sqlite.Open(ctx, databasePath)
	require.NoError(t, err)
	store := sqlite.NewProductionStore(db)
	accounts := []domain.Account{
		{ID: "account-existing-scout", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountStopped, SessionPath: filepath.Join(root, "data", "sessions", "scout.session")},
		{ID: "account-existing-spammer", Role: domain.AccountRoleSpammer, Status: domain.AccountStopped, SessionPath: filepath.Join(root, "data", "sessions", "spammer.session")},
	}
	for _, account := range accounts {
		require.NoError(t, os.MkdirAll(filepath.Dir(account.SessionPath), 0o700))
		require.NoError(t, os.WriteFile(account.SessionPath, []byte("synthetic-session-state"), 0o600))
	}
	require.NoError(t, store.EnsureAccounts(ctx, accounts))
	credentialStore, err := newAccountCredentialStore(ctx, db, desktopTestSecrets{})
	require.NoError(t, err)
	for _, account := range accounts {
		require.NoError(t, credentialStore.Save(ctx, account.ID, messagecrypto.AppCredentials{
			AppID: 1, AppHash: "existing-fixture",
		}))
	}
	require.NoError(t, db.Close())
	return map[string]string{
		"account-existing-scout":   string(domain.AccountRoleScoutAnalyst),
		"account-existing-spammer": string(domain.AccountRoleSpammer),
	}
}

type existingAccountArtifact struct {
	SessionPath      string
	SessionBytes     []byte
	CredentialNonce  []byte
	CredentialCipher []byte
}

func snapshotExistingAccountArtifacts(t *testing.T, root string) map[string]existingAccountArtifact {
	t.Helper()
	databasePath := filepath.Join(root, "data", "app.db")
	db, err := sql.Open("sqlite", databasePath)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	rows, err := db.Query(`SELECT accounts.id, accounts.session_path,
		telegram_account_credentials.nonce, telegram_account_credentials.ciphertext
		FROM accounts JOIN telegram_account_credentials
		ON telegram_account_credentials.account_id = accounts.id
		ORDER BY accounts.id`)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	result := make(map[string]existingAccountArtifact)
	for rows.Next() {
		var id, sessionPath string
		var nonce, ciphertext []byte
		require.NoError(t, rows.Scan(&id, &sessionPath, &nonce, &ciphertext))
		sessionBytes, err := os.ReadFile(sessionPath)
		require.NoError(t, err)
		result[id] = existingAccountArtifact{
			SessionPath:      sessionPath,
			SessionBytes:     append([]byte(nil), sessionBytes...),
			CredentialNonce:  append([]byte(nil), nonce...),
			CredentialCipher: append([]byte(nil), ciphertext...),
		}
	}
	require.NoError(t, rows.Err())
	return result
}

func requireNoLocalImportService(t *testing.T, app *DesktopApp) {
	t.Helper()
	if _, exists := reflect.TypeOf(app).Elem().FieldByName("tdata" + "Import"); exists {
		t.Fatal("desktop composition still retains a local import service")
	}
	if _, exists := reflect.TypeOf(app.bindings).Elem().FieldByName("tdata" + "Import"); exists {
		t.Fatal("bindings still retain a local import service")
	}
}

func writeDesktopTorFixture(t *testing.T, root string) {
	t.Helper()
	t.Setenv("TELEGRAM_COMPANION_TOR_PATH", "")
	t.Setenv("TELEGRAM_COMPANION_SNOWFLAKE_PATH", "")
	for _, path := range []string{
		filepath.Join(root, "bin", "tor"),
		filepath.Join(root, "tor", "pluggable_transports", "lyrebird"),
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	}
	bridgeConfigPath := filepath.Join(root, "tor", "pluggable_transports", "pt_config.json")
	bridgeConfig := `{"pluggableTransports":{"lyrebird":"ClientTransportPlugin meek_lite,obfs2,obfs3,obfs4,scramblesuit,webtunnel exec ${pt_path}lyrebird","snowflake":"ClientTransportPlugin snowflake exec ${pt_path}lyrebird"},"bridges":{"obfs4":["obfs4 203.0.113.10:443 ABCDEF0123456789ABCDEF0123456789ABCDEF01 cert=YWJjZA iat-mode=0"]}}`
	require.NoError(t, os.WriteFile(bridgeConfigPath, []byte(bridgeConfig), 0o600))
}

func TestProductionUpgradeQuarantinesSeededCatalogAndPreservesLegacyAccountIdentity(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeDesktopTorFixture(t, root)
	sessionPath := filepath.Join(root, "data", "gotd-import-staging", "fixture", "north", "account-0", "session.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(sessionPath), 0o700))
	require.NoError(t, os.WriteFile(sessionPath, []byte("{}"), 0o600))
	metadataDir := filepath.Join(root, "data", "tdata", "fixture", "tdata")
	require.NoError(t, os.MkdirAll(metadataDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(metadataDir, "account.json"), []byte(`{"app_id":1,"app_hash":"fixture"}`), 0o600))

	databasePath := filepath.Join(root, "data", "app.db")
	db, err := sql.Open("sqlite", databasePath)
	require.NoError(t, err)
	legacyMigrations := fstest.MapFS{}
	for _, name := range []string{"000001_init.sql", "000002_scout_message_tombstones.sql", "000003_delivery_queue.sql"} {
		migration, err := fs.ReadFile(sqlitemigrations.FS, name)
		require.NoError(t, err)
		legacyMigrations[name] = &fstest.MapFile{Data: migration}
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, legacyMigrations)
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	now := "2026-07-12T12:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO accounts
		(id,phone_masked,display_name,role,status,session_path,public_replies_sent,private_messages_sent,last_error,created_at,updated_at)
		VALUES ('account-6940','***40','','scout_analyst','ready',?,17,9,'',?,?)`, sessionPath, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO outbound_channels
		(id,telegram_chat_id,title,link,topic,status,active,created_at,updated_at)
		VALUES ('boat-chat-outbound','legacy-out','legacy','https://t.me/+legacyAcceptance','legacy','ready',1,?,?)`, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO scout_chats
		(id,telegram_chat_id,title,link,topic,status,active,created_at,updated_at)
		VALUES ('boat-chat-scout','legacy-scout','legacy','https://t.me/+legacyAcceptance','legacy','ready',1,?,?)`, now, now)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	app, err := newDesktopAppAt(ctx, root, slog.New(slog.NewTextHandler(io.Discard, nil)), desktopTestSecrets{})
	require.NoError(t, err)
	t.Cleanup(func() { app.Shutdown(context.Background()) })
	accounts, err := app.bindings.GetAccounts()
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, "account-6940", accounts[0].ID)
	require.Equal(t, string(domain.AccountRoleScoutAnalyst), accounts[0].Role)
	require.Equal(t, int64(0), accounts[0].PublicRepliesSent)
	require.Equal(t, int64(0), accounts[0].PrivateMessagesSent)
	require.Equal(t, "***40", accounts[0].PhoneMasked)
	for _, catalog := range []string{string(domain.SourceCatalogOutbound), string(domain.SourceCatalogScout)} {
		rows, err := app.bindings.GetCatalog(catalog)
		require.NoError(t, err)
		require.Empty(t, rows)
	}
}

type orderedProxyRuntime struct {
	events     chan string
	started    chan struct{}
	generation atomic.Uint64
}

func (p *orderedProxyRuntime) Run(ctx context.Context) error {
	p.generation.Add(1)
	p.events <- "proxy-run"
	p.started <- struct{}{}
	<-ctx.Done()
	p.events <- "proxy-stop"
	return ctx.Err()
}

func (p *orderedProxyRuntime) WaitReady(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.started:
		p.events <- "proxy-ready"
		return nil
	}
}

type orderedAutomationRuntime struct {
	name   string
	events chan string
}

func (r orderedAutomationRuntime) Run(ctx context.Context) error {
	r.events <- r.name
	<-ctx.Done()
	return ctx.Err()
}

type orderedRouteHealthRuntime struct {
	events chan string
}

func (r orderedRouteHealthRuntime) CheckAll(context.Context) error {
	r.events <- "routes-check"
	return nil
}

func (r orderedRouteHealthRuntime) Run(ctx context.Context, _ time.Duration) error {
	r.events <- "routes-run"
	<-ctx.Done()
	return ctx.Err()
}

func TestProductionAutomationStartsFreshProxyAndTelegramWithoutDeliveryOnEveryRun(t *testing.T) {
	events := make(chan string, 20)
	proxy := &orderedProxyRuntime{events: events, started: make(chan struct{}, 1)}
	runner := &productionAutomation{
		proxy:  proxy,
		routes: orderedRouteHealthRuntime{events: events},
		gotd:   orderedAutomationRuntime{name: "gotd", events: events},
	}

	for cycle := 0; cycle < 2; cycle++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- runner.Run(ctx) }()

		require.Equal(t, "proxy-run", <-events)
		require.Equal(t, "proxy-ready", <-events)
		require.Equal(t, "routes-check", <-events)
		started := map[string]bool{<-events: true, <-events: true}
		require.True(t, started["routes-run"])
		require.True(t, started["gotd"])
		cancel()
		require.ErrorIs(t, <-done, context.Canceled)
		require.Equal(t, "proxy-stop", <-events)
	}
}

func TestSystemProxyRouteParsesTorSOCKSAddress(t *testing.T) {
	route, err := systemProxyRoute("127.0.0.1:19050")
	require.NoError(t, err)
	require.Equal(t, domain.SystemProxyRouteID, route.ID)
	require.Equal(t, "socks5", route.Protocol)
	require.Equal(t, "127.0.0.1", route.Host)
	require.Equal(t, 19050, route.Port)
	require.True(t, route.Enabled)
}

func TestSystemProxyRouteRejectsInvalidAddress(t *testing.T) {
	_, err := systemProxyRoute("127.0.0.1")
	require.Error(t, err)
}

func (p *orderedProxyRuntime) Generation() uint64 { return p.generation.Load() }

func (p *orderedProxyRuntime) WaitReadyAfter(ctx context.Context, generation uint64) error {
	if err := p.WaitReady(ctx); err != nil {
		return err
	}
	if p.Generation() <= generation {
		return errors.New("proxy readiness came from a stale generation")
	}
	return nil
}
