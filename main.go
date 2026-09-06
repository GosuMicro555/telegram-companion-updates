//go:build desktop

package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tgerr"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/sync/errgroup"

	analyticsai "telegram-companion/internal/analytics/ai"
	"telegram-companion/internal/buildinfo"
	"telegram-companion/internal/domain"
	"telegram-companion/internal/license"
	proxyroutes "telegram-companion/internal/proxy/routes"
	torproxy "telegram-companion/internal/proxy/tor"
	"telegram-companion/internal/repository/sqlite"
	"telegram-companion/internal/revocation"
	messagecrypto "telegram-companion/internal/service/crypto"
	exportservice "telegram-companion/internal/service/export"
	secretservice "telegram-companion/internal/service/secrets"
	telegramgotd "telegram-companion/internal/telegram/gotd"
	wailsbindings "telegram-companion/internal/transport/wails"
	"telegram-companion/internal/usecase"
	analyticsusecase "telegram-companion/internal/usecase/analytics"
	importsusecase "telegram-companion/internal/usecase/imports"
	"telegram-companion/internal/usecase/runtimeconfig"
	"telegram-companion/internal/usecase/scouting"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed assets/icon.png
var appIcon []byte

type secretKeyStore interface {
	GetOrCreate(context.Context, string, int) ([]byte, error)
}

type DesktopApp struct {
	bindings           *wailsbindings.Bindings
	connectivity       *usecase.AutomationController
	scheduledDM        *usecase.AutomationController
	proxyProfiles      *sqlite.ProxyProfileStore
	analyticsScheduler analyticsSchedulerLifecycle
	log                *slog.Logger
	close              []func() error
	retention          *scouting.Retention
	maintenanceMu      sync.Mutex
	maintenanceCancel  context.CancelFunc
	maintenanceWG      sync.WaitGroup
	shutdownTimeout    time.Duration
	shutdownOnce       sync.Once
	maximizeWindow     func(context.Context)
}

type analyticsSchedulerLifecycle interface {
	Restore(context.Context) error
	Shutdown(context.Context) error
}

type desktopPaths struct {
	root      string
	resources string
}

var desktopSecretService = "telegram-companion"
var desktopWindowTitle = "Telegram Companion"

func NewDesktopApp() *DesktopApp {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	configRoot, err := os.UserConfigDir()
	if err != nil {
		panic(err)
	}
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	override := os.Getenv("TELEGRAM_COMPANION_DATA_ROOT")
	paths, err := resolveDesktopPaths(override, configRoot, executable)
	if err != nil {
		panic(err)
	}
	if strings.TrimSpace(override) == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			panic(homeErr)
		}
		if err := migrateDiscoveredLegacyData(home, executable, paths.root); err != nil {
			panic(err)
		}
	}
	app, err := newDesktopAppWithPaths(context.Background(), paths, log, secretservice.NewSecretStore(desktopSecretService))
	if err != nil {
		panic(err)
	}
	return app
}

func newDesktopAppAt(ctx context.Context, repoDir string, log *slog.Logger, secrets secretKeyStore) (*DesktopApp, error) {
	return newDesktopAppWithPaths(ctx, desktopPaths{root: repoDir, resources: repoDir}, log, secrets)
}

func newDesktopAppWithPaths(ctx context.Context, paths desktopPaths, log *slog.Logger, secrets secretKeyStore) (*DesktopApp, error) {
	if log == nil || secrets == nil {
		return nil, errors.New("desktop logger and secret store are required")
	}
	dataDir := filepath.Join(paths.root, "data")
	databasePath := filepath.Join(dataDir, "app.db")
	legacyPath := filepath.Join(dataDir, "application-state.bolt")
	if err := promoteLegacyDatabase(databasePath, legacyPath); err != nil {
		return nil, err
	}
	db, err := openProductionDatabase(ctx, paths.root, secrets)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*DesktopApp, error) { return nil, errors.Join(cause, db.Close()) }
	store := sqlite.NewProductionStore(db)
	proxyProfiles, err := newProxyProfileStore(ctx, db, secrets)
	if err != nil {
		return fail(err)
	}
	if _, err := os.Stat(legacyPath); err == nil {
		if _, err := sqlite.NewLegacyImporter(db).Import(ctx, legacyPath); err != nil {
			return fail(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail(err)
	}

	// Imports commit accounts and credentials together. On restart, the database
	// is authoritative; staging files must not recreate accounts or change IDs.
	accounts, err := store.ListAccounts(ctx)
	if err != nil {
		return fail(err)
	}

	key, err := secrets.GetOrCreate(ctx, "scout-message-key", chacha20poly1305.KeySize)
	if err != nil {
		return fail(err)
	}
	cipher, err := messagecrypto.NewMessageCipher(key, nil)
	clear(key)
	if err != nil {
		return fail(err)
	}
	senderReferenceKey, err := secrets.GetOrCreate(ctx, "outbound-target-key", chacha20poly1305.KeySize)
	if err != nil {
		return fail(err)
	}
	senderReferences := telegramgotd.NewSenderReferences(senderReferenceKey)
	clear(senderReferenceKey)
	clock := systemClock{}
	messageStore := sqlite.NewMessageStore(db, databasePath, sqlite.SystemDiskProbe{}, 1<<30)
	collector := scouting.NewCollector(messageStore, cipher, clock)
	metrics := scouting.NewMetrics(messageStore, clock)
	rowSource := sqlite.NewEncryptedAnalyticsRowSource(db, cipher)
	exportPath := exportservice.DefaultKeywordExportPath(paths.root)
	exporter := exportservice.NewKeywordExporter(exportPath)
	analyticsStore := sqlite.NewAnalyticsStore(db, rowSource)
	analyticsSchedulerStore := sqlite.NewAnalyticsSchedulerStore(db)
	analyzer := analyticsusecase.NewAnalyzer(analyticsStore, exporter)
	canonicalAnalyzer := analyticsusecase.NewCanonicalService(analyticsStore, analyticsSchedulerStore)

	provider, providerClose := productionEmbeddingProvider(ctx, modelDirectory(paths.resources))
	importer := importsusecase.NewImportService(sqlite.NewImportStore(db), provider,
		importsusecase.WithLimits(importsusecase.Limits{SnapshotDirectory: filepath.Join(dataDir, "import-snapshots")}))
	opener := desktopFileOpener{opener: exportservice.NewOSFileOpener(exportPath)}

	accountCredentials, err := newAccountCredentialStore(ctx, db, secrets)
	if err != nil {
		if providerClose != nil {
			_ = providerClose()
		}
		return fail(err)
	}
	credentials, err := loadAccountCredentials(ctx, dataDir, accounts, accountCredentials)
	if err != nil {
		if providerClose != nil {
			_ = providerClose()
		}
		return fail(err)
	}
	proxyMode := strings.TrimSpace(os.Getenv("TELEGRAM_COMPANION_PROXY_MODE"))
	if proxyMode != "" && proxyMode != "tor_snowflake" {
		return fail(errors.New("desktop proxy mode must be tor_snowflake"))
	}
	proxySupervisor, err := torproxy.New(torproxy.Config{
		StateDir:         filepath.Join(paths.root, "proxy", "tor-snowflake"),
		ResourcesDir:     paths.resources,
		LyrebirdPath:     filepath.Join(paths.resources, "tor", "pluggable_transports", "lyrebird"),
		BridgeConfigPath: filepath.Join(paths.resources, "tor", "pluggable_transports", "pt_config.json"),
	}, log)
	if err != nil {
		if providerClose != nil {
			_ = providerClose()
		}
		return fail(err)
	}
	systemRoute, err := systemProxyRoute(proxySupervisor.SOCKSAddress())
	if err != nil {
		if providerClose != nil {
			_ = providerClose()
		}
		return fail(err)
	}
	routeRegistry := proxyroutes.NewRegistry(proxyProfiles, systemRoute, proxyroutes.WithSystemHealth(func() bool {
		return proxySupervisor.Status().State == torproxy.StateReady
	}))
	factory := telegramgotd.NewPerAccountClientFactoryWithAccountResolver(
		credentials, store, telegramgotd.ProductionSessionBarrier(), telegramgotd.NewRegistryResolver(routeRegistry),
	)
	controller := usecase.NewAutomationController(nil)
	bindings := wailsbindings.NewBindings(controller, store)
	configureDriveAccountImport(bindings, accountCredentials, factory, telegramgotd.NewRegistryResolver(routeRegistry), dataDir, credentials)
	wailsbindings.ConfigureAccountRests(bindings, usecase.NewAccountRestService(store, time.Now))
	bindings.RuntimeStore().Update(func(snapshot *runtimeconfig.Snapshot) {
		snapshot.OutboundPaused = true
	})
	backupRuntime, err := newDesktopBackupRuntimeForDatabase(paths.root, store, secrets, db)
	if err != nil {
		if providerClose != nil {
			_ = providerClose()
		}
		return fail(err)
	}
	bindings.ConfigureBackups(backupRuntime)
	senderRegistry := telegramgotd.NewClientSenderRegistry(store.Catalogs(), senderReferences)
	outbound := telegramgotd.NewOutbound(bindings.RuntimeStore(), store.Accounts(), senderRegistry, time.Now)
	scheduledDMRepository := sqlite.NewScheduledDMRepository(db, time.Now)
	scheduledDMRunner := usecase.NewScheduledDMRunner(scheduledDMRepository, store.Accounts(), outbound, time.Now)
	scheduledDM := usecase.NewAutomationController(scheduledDMRunner)
	wailsbindings.ConfigureScheduledDM(
		bindings,
		usecase.NewScheduledDMService(scheduledDMRepository, store, time.Now),
		scheduledDMRepository,
		store.Accounts(),
		outbound,
		scheduledDM,
	)
	jobs := store.Jobs()
	liveSink := telegramgotd.NewLiveSink(collector, store.Catalogs(), bindings.RuntimeStore(), jobs, time.Now)
	updateActivator := telegramgotd.NewUpdateActivator(store.Catalogs(), liveSink, time.Now, senderReferences)
	updateActivator.ConfigureHistoryStore(analyticsSchedulerStore)
	analyticsScheduler := usecase.NewAnalyticsScheduler(analyticsSchedulerStore, updateActivator, canonicalAnalyzer, bindings.SyncCanonicalTriggers, time.Now)
	activator := telegramgotd.NewProductionActivator(updateActivator, senderRegistry)
	manager := telegramgotd.NewManager(store.Accounts(), factory, activator, func(event telegramgotd.AccountStatusEvent) {
		if err := persistAndLogAccountStatus(ctx, store.Accounts(), log, event); err != nil {
			log.Warn("gotd account status persistence failed", "account", event.AccountID, "error_code", "status_store")
		}
	})
	manager.ConfigureRouteFailures(routeRegistry)

	connectivityRunner := &productionAutomation{
		proxy:  proxySupervisor,
		routes: routeRegistry,
		gotd:   telegramgotd.NewRuntimeRunner(manager, bindings.RuntimeStore()),
	}
	connectivity := usecase.NewAutomationController(&resilientAutomation{
		runner: connectivityRunner,
		log:    log,
	})
	scheduler := usecase.NewScheduler(store.Accounts(), jobs, store, outbound, usecase.NewRoundRobinSelector(), nil, senderRegistry)
	scheduler.SetGroupRestEnabledResolver(func(ctx context.Context) (bool, error) {
		settings, err := store.LoadAppSettings(ctx)
		if err != nil {
			return false, err
		}
		return settings.Normalized().GroupRestEnabled, nil
	})
	messageRunner := &messageAutomation{
		config:   bindings.RuntimeStore(),
		delivery: usecase.NewDeliveryRunner(scheduler, time.Now),
	}
	controller = usecase.NewAutomationController(messageRunner)
	bindings.SetAutomationController(controller)
	bindings.ConfigureProduction(metrics, analyzer, importer, opener, updateActivator)
	bindings.ConfigureCanonicalAnalytics(canonicalAnalyzer)
	bindings.ConfigureAnalyticsCollection(analyticsScheduler, analyticsSchedulerStore)
	bindings.ConfigureProxyStatus(desktopProxyStatusProvider{supervisor: proxySupervisor})
	bindings.ConfigureProxyRouting(proxyProfiles, routeRegistry)
	bindings.ConfigureImportFileSelector(desktopImportFileSelector{})
	bindings.ConfigureImportCandidateExportSelector(desktopImportFileSelector{})
	bindings.ConfigureLiveStatisticsExportSelector(desktopImportFileSelector{})
	if err := bindings.SyncCanonicalTriggers(); err != nil {
		if providerClose != nil {
			_ = providerClose()
		}
		return fail(err)
	}
	if err := bindings.ProductionReady(); err != nil {
		if providerClose != nil {
			_ = providerClose()
		}
		return fail(err)
	}

	closers := []func() error{db.Close}
	if providerClose != nil {
		closers = append([]func() error{providerClose}, closers...)
	}
	return &DesktopApp{bindings: bindings, connectivity: connectivity, scheduledDM: scheduledDM, proxyProfiles: proxyProfiles, analyticsScheduler: analyticsScheduler, log: log, close: closers, retention: scouting.NewRetention(messageStore, clock), maximizeWindow: wailsruntime.WindowMaximise}, nil
}

func systemProxyRoute(address string) (domain.ProxyRoute, error) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return domain.ProxyRoute{}, fmt.Errorf("parse system proxy address: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return domain.ProxyRoute{}, errors.New("system proxy port is invalid")
	}
	if strings.TrimSpace(host) == "" {
		return domain.ProxyRoute{}, errors.New("system proxy host is required")
	}
	return domain.ProxyRoute{
		ID: domain.SystemProxyRouteID, Name: "Tor/Snowflake", Protocol: "socks5",
		Host: host, Port: port, Enabled: true,
	}, nil
}
func newProxyProfileStore(ctx context.Context, db *sql.DB, secrets secretKeyStore) (*sqlite.ProxyProfileStore, error) {
	key, err := secrets.GetOrCreate(ctx, "proxy-credentials-v1", 32)
	if err != nil {
		return nil, err
	}
	cipher, err := messagecrypto.NewProxyCredentialsCipher(key)
	clear(key)
	if err != nil {
		return nil, err
	}
	return sqlite.NewProxyProfileStore(db, cipher), nil
}

func newAccountCredentialStore(ctx context.Context, db *sql.DB, secrets secretKeyStore) (*sqlite.AccountCredentialStore, error) {
	key, err := secrets.GetOrCreate(ctx, "telegram-account-credentials-v1", 32)
	if err != nil {
		return nil, err
	}
	cipher, err := messagecrypto.NewAccountCredentialsCipher(key)
	clear(key)
	if err != nil {
		return nil, err
	}
	return sqlite.NewAccountCredentialStore(db, cipher), nil
}

func loadAccountCredentials(
	ctx context.Context,
	dataDir string,
	accounts []domain.Account,
	store *sqlite.AccountCredentialStore,
) (map[domain.ID]telegramgotd.AppCredentials, error) {
	if store == nil {
		return nil, errors.New("account credential store is required")
	}
	stored, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	missing := make([]domain.Account, 0)
	for _, account := range accounts {
		if _, ok := stored[account.ID]; !ok {
			missing = append(missing, account)
		}
	}
	if len(missing) > 0 {
		legacy, err := telegramCredentialsByAccount(dataDir, missing)
		if err != nil {
			return nil, err
		}
		for accountID, credentials := range legacy {
			converted := messagecrypto.AppCredentials{AppID: credentials.AppID, AppHash: credentials.AppHash}
			if err := store.Save(ctx, accountID, converted); err != nil {
				return nil, err
			}
			stored[accountID] = converted
		}
	}
	result := make(map[domain.ID]telegramgotd.AppCredentials, len(stored))
	for id, credentials := range stored {
		result[id] = credentials
	}
	for _, account := range accounts {
		credentials, ok := stored[account.ID]
		if !ok {
			return nil, errors.New("Telegram app credentials are unavailable for an account")
		}
		result[account.ID] = telegramgotd.AppCredentials{AppID: credentials.AppID, AppHash: credentials.AppHash}
	}
	return result, nil
}

type desktopImportFileSelector struct{}

func (desktopImportFileSelector) SelectImportFile(ctx context.Context, locale string) (string, error) {
	return wailsruntime.OpenFileDialog(ctx, wailsruntime.OpenDialogOptions{
		Title:   nativeDialogTitle(locale, "import"),
		Filters: []wailsruntime.FileFilter{{DisplayName: "Text, CSV, or JSON", Pattern: "*.txt;*.csv;*.json"}},
	})
}

func (desktopImportFileSelector) SelectExportFile(ctx context.Context, defaultName, locale string) (string, error) {
	return wailsruntime.SaveFileDialog(ctx, wailsruntime.SaveDialogOptions{
		Title:           nativeDialogTitle(locale, "export"),
		DefaultFilename: defaultName,
		Filters:         []wailsruntime.FileFilter{{DisplayName: "Text", Pattern: "*.txt"}},
	})
}

func (desktopImportFileSelector) SelectLiveStatisticsExportFile(ctx context.Context, defaultName, locale string) (string, error) {
	title := "Выгрузка Live-статистики"
	if strings.EqualFold(strings.TrimSpace(locale), "en") {
		title = "Export live statistics"
	}
	return wailsruntime.SaveFileDialog(ctx, wailsruntime.SaveDialogOptions{
		Title:           title,
		DefaultFilename: defaultName,
		Filters:         []wailsruntime.FileFilter{{DisplayName: "CSV", Pattern: "*.csv"}},
	})
}

func nativeDialogTitle(locale, operation string) string {
	if strings.EqualFold(strings.TrimSpace(locale), "en") {
		if operation == "export" {
			return "Export candidates"
		}
		return "Import AI data"
	}
	if operation == "export" {
		return "Экспорт кандидатов"
	}
	return "Импорт данных AI"
}

func resolveBuildDesktopPaths(info buildinfo.Info, override, configRoot, executablePath string) (desktopPaths, error) {
	// Public arm64 builds must share the stable, human-readable application
	// data location so updates retain activation/profile state. Internal builds
	// keep the explicit override used by tests and local staging.
	effectiveOverride := strings.TrimSpace(override)
	if info.Channel == buildinfo.ChannelPublicMacOSARM64 {
		effectiveOverride = filepath.Join(configRoot, "Telegram Companion")
	}
	return resolveDesktopPaths(effectiveOverride, configRoot, executablePath)
}

func resolveDesktopPaths(override, configRoot, executablePath string) (desktopPaths, error) {
	root := strings.TrimSpace(override)
	if root == "" {
		root = filepath.Join(configRoot, "telegram-companion")
	}
	if !filepath.IsAbs(root) {
		return desktopPaths{}, errors.New("desktop data root must be absolute")
	}
	if !filepath.IsAbs(executablePath) {
		return desktopPaths{}, errors.New("desktop executable path must be absolute")
	}
	cleanExecutable := filepath.Clean(executablePath)
	resources := filepath.Dir(cleanExecutable)
	if filepath.Base(resources) == "MacOS" {
		contents := filepath.Dir(resources)
		if filepath.Base(contents) == "Contents" && strings.HasSuffix(strings.ToLower(filepath.Base(filepath.Dir(contents))), ".app") {
			resources = filepath.Join(contents, "Resources")
		}
	}
	return desktopPaths{root: filepath.Clean(root), resources: resources}, nil
}

func modelDirectory(resources string) string {
	return filepath.Join(resources, "models", "multilingual-e5-small")
}

func migrateLegacyWorkingData(legacyRoot, stableRoot string) error {
	legacyData := filepath.Join(filepath.Clean(legacyRoot), "data")
	stableData := filepath.Join(filepath.Clean(stableRoot), "data")
	if legacyData == stableData {
		return nil
	}
	if info, err := os.Stat(stableData); err == nil {
		if !info.IsDir() {
			return errors.New("stable desktop data path is not a directory")
		}
		marked, markerErr := isCompanionLegacyData(legacyData)
		if markerErr != nil {
			return markerErr
		}
		if !marked {
			return nil
		}
		return copyMissingLegacySessionStaging(legacyData, stableData)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info, err := os.Stat(legacyData); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	} else if !info.IsDir() {
		return errors.New("legacy desktop data path is not a directory")
	}
	marked, err := isCompanionLegacyData(legacyData)
	if err != nil {
		return err
	}
	if !marked {
		return nil
	}
	if err := os.MkdirAll(stableRoot, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(stableRoot, 0o700); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(stableRoot, ".data-migration-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	if err := copyPrivateTree(legacyData, temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, stableData); err != nil {
		if _, statErr := os.Stat(stableData); statErr == nil {
			return nil
		}
		return err
	}
	return syncDirectory(stableRoot)
}

func copyMissingLegacySessionStaging(legacyData, stableData string) error {
	source := filepath.Join(legacyData, "gotd-import-staging")
	destination := filepath.Join(stableData, "gotd-import-staging")
	if info, err := os.Lstat(destination); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("stable gotd session staging path is not a directory")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	info, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("legacy gotd session staging path is not a directory")
	}
	temporary, err := os.MkdirTemp(stableData, ".gotd-import-staging-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	if err := copyPrivateTree(source, temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		if _, statErr := os.Stat(destination); statErr == nil {
			return nil
		}
		return err
	}
	return syncDirectory(stableData)
}

func migrateDiscoveredLegacyData(home, executablePath, stableRoot string) error {
	for _, candidate := range legacyRootCandidates(home, executablePath) {
		if err := migrateLegacyWorkingData(candidate, stableRoot); err != nil {
			return err
		}
		if info, err := os.Stat(filepath.Join(stableRoot, "data")); err == nil && !info.IsDir() {
			return errors.New("stable desktop data path is not a directory")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if info, err := os.Stat(filepath.Join(stableRoot, "data", "gotd-import-staging")); err == nil && info.IsDir() {
			return nil
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func legacyRootCandidates(home, executablePath string) []string {
	home = filepath.Clean(home)
	executableDir := filepath.Dir(filepath.Clean(executablePath))
	candidates := []string{
		filepath.Join(home, "projects", "telegram-companion"),
		filepath.Join(home, "Documents", "telegram-companion"),
		filepath.Join(home, "telegram-companion"),
		filepath.Join(home, ".telegram-companion"),
		home,
		executableDir,
	}
	if filepath.Base(executableDir) == "bin" {
		parent := filepath.Dir(executableDir)
		candidates = append(candidates, parent)
		if filepath.Base(parent) == "build" {
			candidates = append(candidates, filepath.Dir(parent))
		}
	}
	seen := make(map[string]struct{}, len(candidates))
	unique := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		unique = append(unique, candidate)
	}
	return unique
}

func isCompanionLegacyData(dataDir string) (bool, error) {
	for _, marker := range []string{"application-state.bolt"} {
		info, err := os.Lstat(filepath.Join(dataDir, marker))
		if err == nil {
			return info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	if sqliteCompanionMarker(filepath.Join(dataDir, "app.db")) {
		return true, nil
	}
	if boltCompanionMarker(filepath.Join(dataDir, "app.db")) {
		return true, nil
	}
	sessions, err := filepath.Glob(filepath.Join(dataDir, "gotd-import-staging", "*", "*", "account-*", "session.json"))
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		info, statErr := os.Lstat(session)
		if statErr == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return true, nil
		}
	}
	return false, nil
}

func sqliteCompanionMarker(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro&immutable=1"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return false
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name IN ('accounts','app_settings','scout_messages')`).Scan(&count); err != nil {
		return false
	}
	return count >= 2
}

func boltCompanionMarker(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	db, err := bolt.Open(path, 0o400, &bolt.Options{ReadOnly: true, Timeout: 100 * time.Millisecond})
	if err != nil {
		return false
	}
	defer db.Close()
	marked := false
	if err := db.View(func(tx *bolt.Tx) error {
		marked = tx.Bucket([]byte("settings")) != nil
		return nil
	}); err != nil {
		return false
	}
	return marked
}

func copyPrivateTree(source, destination string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative != "." {
			topLevel := strings.Split(relative, string(filepath.Separator))[0]
			switch topLevel {
			case "acceptance", "acceptance-venv", "tor-snowflake":
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			return os.Chmod(target, 0o700)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("legacy data contains unsupported file type: %s", relative)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := errors.Join(input.Close(), output.Sync(), output.Close())
		return errors.Join(copyErr, closeErr)
	})
}

type runtimeStatusRecorder interface {
	RecordRuntimeStatus(context.Context, domain.ID, string, string, time.Time) error
}

type floodWaitStatusRecorder interface {
	RecordFloodWait(context.Context, domain.ID, time.Time, time.Time) error
}

func persistAndLogAccountStatus(ctx context.Context, recorder runtimeStatusRecorder, log *slog.Logger, event telegramgotd.AccountStatusEvent) error {
	now := time.Now().UTC()
	var flood *telegramgotd.FloodWaitError
	if event.Status == telegramgotd.RuntimeBackoff && errors.As(event.Err, &flood) && flood.Duration > 0 {
		if floodRecorder, ok := recorder.(floodWaitStatusRecorder); ok {
			if err := floodRecorder.RecordFloodWait(ctx, event.AccountID, now.Add(flood.Duration), now); err != nil {
				return err
			}
			logAccountStatus(log, event)
			return nil
		}
	}
	status := "paused"
	switch event.Status {
	case telegramgotd.RuntimeConnecting:
		status = "joining"
	case telegramgotd.RuntimeConnected:
		status = "ready"
	case telegramgotd.RuntimeBackoff:
		status = "partial"
	case telegramgotd.RuntimeSessionInvalid:
		status = "error"
	case telegramgotd.RuntimeStopped:
		status = "stopped"
	}
	errorCode := accountStatusErrorCode(event.Err)
	if err := recorder.RecordRuntimeStatus(ctx, event.AccountID, status, errorCode, now); err != nil {
		return err
	}
	logAccountStatus(log, event)
	return nil
}

func accountStatusErrorCode(err error) string {
	if err == nil {
		return ""
	}
	code := "runtime_error"
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		code = "session_io"
	} else if errors.Is(err, proxyroutes.ErrRouteUnavailable) {
		code = "proxy_unavailable"
	} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, telegramgotd.ErrConnectionTimeout) {
		code = "connection_timeout"
	} else if errors.Is(err, context.Canceled) {
		code = "cancelled"
	} else if rpcErr, ok := tgerr.As(err); ok && rpcErr.Type != "" {
		code = "rpc_" + strings.ToLower(rpcErr.Type)
	}
	return code
}

func logAccountStatus(log *slog.Logger, event telegramgotd.AccountStatusEvent) {
	attrs := []any{"account", event.AccountID, "route", event.RouteID, "status", event.Status, "revision", event.Revision}
	if event.RetryIn > 0 {
		attrs = append(attrs, "retry_in", event.RetryIn)
	}
	if event.Err != nil {
		log.Warn("gotd account state", append(attrs, "error_code", accountStatusErrorCode(event.Err), "error_kind", accountStatusErrorKind(event.Err))...)
		return
	}
	log.Info("gotd account state", attrs...)
}

func accountStatusErrorKind(err error) string {
	if err == nil {
		return ""
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		return "network"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, telegramgotd.ErrConnectionTimeout) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "internal"
}

type proxyRuntime interface {
	Run(context.Context) error
	WaitReady(context.Context) error
	Generation() uint64
	WaitReadyAfter(context.Context, uint64) error
}

type automationRuntime interface {
	Run(context.Context) error
}

type resilientAutomation struct {
	runner     automationRuntime
	log        *slog.Logger
	retryDelay time.Duration
}

func (r *resilientAutomation) Run(ctx context.Context) error {
	if r == nil || r.runner == nil {
		return errors.New("resilient automation is not configured")
	}
	delay := r.retryDelay
	if delay <= 0 {
		delay = 5 * time.Second
	}
	for {
		err := r.runner.Run(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if r.log != nil {
			r.log.Warn("Telegram connectivity stopped unexpectedly", "error", err, "retry_in", delay)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type routeHealthRuntime interface {
	CheckAll(context.Context) error
	Run(context.Context, time.Duration) error
}

type productionAutomation struct {
	proxy  proxyRuntime
	routes routeHealthRuntime
	gotd   automationRuntime
}

func (r *productionAutomation) Run(ctx context.Context) error {
	if r == nil || r.proxy == nil || r.gotd == nil {
		return errors.New("production automation is not configured")
	}
	baseCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	group, runCtx := errgroup.WithContext(baseCtx)
	generation := r.proxy.Generation()
	group.Go(func() error { return r.proxy.Run(runCtx) })
	if err := r.proxy.WaitReadyAfter(runCtx, generation); err != nil {
		cancel()
		return errors.Join(err, group.Wait())
	}
	if r.routes != nil {
		if err := r.routes.CheckAll(runCtx); err != nil && runCtx.Err() != nil {
			cancel()
			return errors.Join(err, group.Wait())
		}
		group.Go(func() error { return r.routes.Run(runCtx, 30*time.Second) })
	}
	group.Go(func() error { return r.gotd.Run(runCtx) })
	return group.Wait()
}

type messageAutomation struct {
	config   *runtimeconfig.Store
	delivery automationRuntime
}

func (r *messageAutomation) Run(ctx context.Context) error {
	if r == nil || r.config == nil || r.delivery == nil {
		return errors.New("message automation is not configured")
	}
	r.config.Update(func(snapshot *runtimeconfig.Snapshot) {
		snapshot.OutboundPaused = false
	})
	defer r.config.Update(func(snapshot *runtimeconfig.Snapshot) {
		snapshot.OutboundPaused = true
	})
	return r.delivery.Run(ctx)
}

type desktopProxyStatusProvider struct{ supervisor *torproxy.Supervisor }

func (p desktopProxyStatusProvider) Status() wailsbindings.ProxyStatusDTO {
	status := p.supervisor.Status()
	return wailsbindings.ProxyStatusDTO{
		Mode: "tor_snowflake", State: string(status.State), Address: status.Address,
		Transport: status.Transport, LastError: status.LastError, RestartCount: status.RestartCount,
		UpdatedAt: status.UpdatedAt.Format(time.RFC3339Nano), AutoRestart: true,
	}
}

func (a *DesktopApp) startStorageMaintenance(root context.Context) {
	if a == nil || a.retention == nil {
		return
	}
	a.maintenanceMu.Lock()
	if a.maintenanceCancel != nil {
		a.maintenanceMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(root)
	a.maintenanceCancel = cancel
	a.maintenanceWG.Add(1)
	a.maintenanceMu.Unlock()
	go func() {
		defer a.maintenanceWG.Done()
		if _, err := a.retention.RunOnce(ctx, time.Now().UTC().Add(-scouting.RetentionPeriod)); err != nil && !errors.Is(err, context.Canceled) {
			a.log.Error("run storage retention", "error_code", "retention_failed")
		}
		ticker := time.NewTicker(scouting.RetentionInterval)
		defer ticker.Stop()
		if err := a.retention.Run(ctx, ticker.C); err != nil && !errors.Is(err, context.Canceled) {
			a.log.Error("run storage retention", "error_code", "retention_failed")
		}
	}()
}

func (a *DesktopApp) stopStorageMaintenance() {
	if a == nil {
		return
	}
	a.maintenanceMu.Lock()
	cancel := a.maintenanceCancel
	a.maintenanceCancel = nil
	a.maintenanceMu.Unlock()
	if cancel != nil {
		cancel()
		a.maintenanceWG.Wait()
	}
}

func (a *DesktopApp) Startup(ctx context.Context) {
	a.bindings.SetRootContext(ctx)
	if a.maximizeWindow != nil {
		a.maximizeWindow(ctx)
	}
	a.startStorageMaintenance(ctx)
	if err := a.bindings.StartBackgroundServices(); err != nil {
		a.log.Error("start backup background services", "error", err)
	}
	if a.connectivity != nil {
		if err := a.connectivity.Start(context.WithoutCancel(ctx)); err != nil {
			a.log.Error("start Telegram membership connectivity", "error", err)
		}
	}
	if a.analyticsScheduler != nil {
		if err := a.analyticsScheduler.Restore(ctx); err != nil {
			a.log.Error("start analytics scheduler", "error", err)
		}
	}
}

func (a *DesktopApp) RejectNewOperations(ctx context.Context) error {
	if a == nil || a.bindings == nil {
		return errors.New("desktop operations are unavailable")
	}
	return a.bindings.StopOperations(ctx)
}

func (a *DesktopApp) Shutdown(ctx context.Context) {
	if a == nil {
		return
	}
	a.shutdownOnce.Do(func() { a.shutdown(ctx) })
}

func (a *DesktopApp) shutdown(ctx context.Context) {
	timeout := a.shutdownTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	shutdownRoot := context.WithoutCancel(ctx)
	if a.analyticsScheduler != nil {
		shutdownCtx, cancel := context.WithTimeout(shutdownRoot, timeout)
		err := a.analyticsScheduler.Shutdown(shutdownCtx)
		cancel()
		if err != nil {
			a.log.Error("stop analytics scheduler", "error", err)
		}
	}
	if err := a.bindings.StopAutomation(); err != nil {
		a.log.Error("stop gotd automation", "error", err)
	}
	if a.connectivity != nil {
		connectivityCtx, cancelConnectivity := context.WithTimeout(shutdownRoot, timeout)
		err := a.connectivity.Stop(connectivityCtx)
		cancelConnectivity()
		if err != nil {
			a.log.Error("stop Telegram membership connectivity", "error", err)
		}
	}
	if a.scheduledDM != nil {
		scheduledDMCtx, cancelScheduledDM := context.WithTimeout(shutdownRoot, timeout)
		err := a.scheduledDM.Stop(scheduledDMCtx)
		cancelScheduledDM()
		if err != nil {
			a.log.Error("stop scheduled DM runner", "error", err)
		}
	}
	analysisCtx, cancelAnalysis := context.WithTimeout(shutdownRoot, timeout)
	err := a.bindings.StopAnalysis(analysisCtx)
	cancelAnalysis()
	if err != nil {
		a.log.Error("stop active analysis", "error", err)
		return
	}
	operationsCtx, cancelOperations := context.WithTimeout(shutdownRoot, timeout)
	err = a.bindings.StopOperations(operationsCtx)
	cancelOperations()
	if err != nil {
		a.log.Error("stop active desktop operations", "error", err)
		return
	}
	a.stopStorageMaintenance()
	shutdownCtx, cancelShutdown := context.WithTimeout(shutdownRoot, timeout)
	defer cancelShutdown()
	a.closeBackgroundServices(shutdownCtx)
	for _, closeResource := range a.close {
		if err := closeResource(); err != nil {
			a.log.Error("close desktop resource", "error", err)
		}
	}
}

func (a *DesktopApp) Bindings() *wailsbindings.Bindings { return a.bindings }

func runDesktop(app *DesktopApp, run func(*options.App) error) error {
	if app == nil || run == nil {
		return errors.New("desktop app and Wails runner are required")
	}
	return run(&options.App{
		Title: desktopWindowTitle, Width: 980, Height: 700, MinWidth: 860, MinHeight: 620, WindowStartState: options.Maximised,
		AssetServer: &assetserver.Options{Assets: assets},
		Linux:       &linux.Options{Icon: appIcon, ProgramName: "telegram-companion"},
		Mac: &mac.Options{
			DisableZoom:                  false,
			DisableEscapeExitsFullscreen: false,
		},
		OnStartup: app.Startup, OnShutdown: app.Shutdown,
		Bind: []interface{}{app.Bindings()},
	})
}

func desktopLogOutput(root string) (io.Writer, func()) {
	path := filepath.Join(root, "logs", "desktop.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return os.Stdout, func() {}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return os.Stdout, func() {}
	}
	return io.MultiWriter(os.Stdout, file), func() { _ = file.Close() }
}

func main() {
	if err := awaitDesktopRelaunchHandoff(); err != nil {
		fmt.Fprintln(os.Stderr, "desktop relaunch handoff invalid")
		os.Exit(1)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	info, err := buildinfo.Current()
	if err != nil {
		log.Error("desktop build metadata invalid", "error_code", "build_metadata")
		os.Exit(1)
	}
	configRoot, err := os.UserConfigDir()
	if err != nil {
		log.Error("desktop configuration directory unavailable", "error_code", "config_root")
		os.Exit(1)
	}
	executable, err := os.Executable()
	if err != nil {
		log.Error("desktop executable unavailable", "error_code", "executable")
		os.Exit(1)
	}
	override := os.Getenv("TELEGRAM_COMPANION_DATA_ROOT")
	paths, err := resolveBuildDesktopPaths(info, override, configRoot, executable)
	if err != nil {
		log.Error("desktop paths invalid", "error_code", "desktop_paths")
		os.Exit(1)
	}
	logOutput, closeLogOutput := desktopLogOutput(paths.root)
	defer closeLogOutput()
	log = slog.New(slog.NewJSONHandler(logOutput, nil))

	licenseStore := license.NewFileStore(paths.root)
	runtimeSecrets := secretservice.NewSecretStore(desktopSecretService)
	gate, revocationChecker, err := newProductionDesktopLicenseGate(info, licenseStore, runtimeSecrets)
	if err != nil {
		log.Error("desktop revocation initialization failed", "error_code", "revocation_initialization")
		os.Exit(1)
	}
	relauncher := newDesktopRelaunchCoordinator()
	program, err := newDesktopProgram(info, gate.Authorized(), func() (*DesktopApp, error) {
		imported, importErr := importBundledDesktopState(context.Background(), info, paths, gate, runtimeSecrets)
		if importErr != nil {
			return nil, importErr
		}
		if imported {
			log.Info("private seeded state imported", "event", "bootstrap_state_imported")
		}
		if info.Channel == buildinfo.ChannelInternal && strings.TrimSpace(override) == "" {
			home, homeErr := os.UserHomeDir()
			if homeErr != nil {
				return nil, homeErr
			}
			if migrateErr := migrateDiscoveredLegacyData(home, executable, paths.root); migrateErr != nil {
				return nil, migrateErr
			}
		}
		return newDesktopAppWithPaths(context.Background(), paths, log, runtimeSecrets)
	})
	if err != nil {
		log.Error("desktop runtime initialization failed", "error_code", "runtime_initialization")
		os.Exit(1)
	}
	if program.mode == desktopModeWorkspace && revocationChecker != nil {
		licenseID, ok := gate.AuthorizedLicenseID()
		if !ok {
			log.Error("desktop revocation identity unavailable", "error_code", "revocation_identity")
			os.Exit(1)
		}
		runtime, runtimeErr := newDesktopRevocationRuntime(
			gate,
			program.app,
			relauncher,
			wailsruntime.Quit,
			revocationChecker,
			licenseID,
			revocation.NewSystemSupervisorTimer,
			randomDesktopRevocationJitter,
		)
		if runtimeErr != nil {
			log.Error("desktop revocation supervisor unavailable", "error_code", "revocation_supervisor")
			os.Exit(1)
		}
		program.revocation = runtime
	}

	activation := wailsbindings.NewActivationBindingsWithRestartRequester(
		gate,
		selectDesktopLicenseFile,
		relauncher,
		wailsruntime.Quit,
		nil,
	)
	startup := wailsbindings.NewStartupBindings(
		string(program.mode),
		string(program.recoveryCode),
		gate,
		selectDesktopLicenseFile,
		relauncher,
		wailsruntime.Quit,
		nil,
	)
	updateService := newDesktopUpdateService(info, program)
	updates := wailsbindings.NewUpdateBindings(updateService, nil)
	run := withDesktopSingleInstance(wails.Run, wailsruntime.WindowUnminimise, wailsruntime.WindowShow)
	runErr := runDesktopProgram(program, startup, activation, updates, run)
	relauncher.Release()
	if runErr != nil {
		log.Error("desktop exited", "error_code", "wails_runtime")
		os.Exit(1)
	}
}

func promoteLegacyDatabase(databasePath, legacyPath string) error {
	file, err := os.Open(databasePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	header := make([]byte, 16)
	_, readErr := file.Read(header)
	closeErr := file.Close()
	if readErr == nil && string(header) == "SQLite format 3\x00" {
		return closeErr
	}
	if _, err := os.Stat(legacyPath); err == nil {
		return errors.New("legacy application-state.bolt already exists; refusing to overwrite it")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(databasePath, legacyPath); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(databasePath))
}

func telegramCredentialsByAccount(dataDir string, accounts []domain.Account) (map[domain.ID]telegramgotd.AppCredentials, error) {
	if len(accounts) == 0 {
		return map[domain.ID]telegramgotd.AppCredentials{}, nil
	}
	paths, err := filepath.Glob(filepath.Join(dataDir, "tdata", "*", "tdata", "*.json"))
	if err != nil {
		return nil, err
	}
	unique := make(map[string]telegramgotd.AppCredentials)
	for _, path := range paths {
		if strings.HasPrefix(filepath.Base(path), "shortcuts-") {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		var metadata struct {
			AppID   int    `json:"app_id"`
			AppHash string `json:"app_hash"`
		}
		decodeErr := json.NewDecoder(file).Decode(&metadata)
		closeErr := file.Close()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if metadata.AppID <= 0 || strings.TrimSpace(metadata.AppHash) == "" {
			continue
		}
		credentials := telegramgotd.AppCredentials{AppID: metadata.AppID, AppHash: metadata.AppHash}
		unique[fmt.Sprintf("%d:%s", credentials.AppID, credentials.AppHash)] = credentials
	}
	if len(unique) != 1 {
		return nil, errors.New("exactly one shared Telegram app credential set is required")
	}
	var shared telegramgotd.AppCredentials
	for _, credentials := range unique {
		shared = credentials
	}
	result := make(map[domain.ID]telegramgotd.AppCredentials, len(accounts))
	for _, account := range accounts {
		result[account.ID] = shared
	}
	return result, nil
}

func productionEmbeddingProvider(_ context.Context, modelDir string) (analyticsai.EmbeddingProvider, func() error) {
	provider := &lazyEmbeddingProvider{modelDir: modelDir}
	return provider, provider.Close
}

type lazyEmbeddingProvider struct {
	mu       sync.Mutex
	modelDir string
	provider *analyticsai.HugotProvider
	initErr  error
}

func (p *lazyEmbeddingProvider) Embed(ctx context.Context, texts []string) ([][]float32, analyticsai.ModelMetadata, error) {
	p.mu.Lock()
	if p.provider == nil && p.initErr == nil {
		p.provider, p.initErr = analyticsai.NewHugotProvider(ctx, p.modelDir, analyticsai.PinnedModelMetadata())
	}
	provider, err := p.provider, p.initErr
	p.mu.Unlock()
	if err != nil {
		return nil, analyticsai.ModelMetadata{}, err
	}
	return provider.Embed(ctx, texts)
}

func (p *lazyEmbeddingProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.provider == nil {
		return nil
	}
	return p.provider.Close()
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

type desktopFileOpener struct{ opener *exportservice.OSFileOpener }

func (o desktopFileOpener) Open(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return o.opener.Open(ctx, path)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
