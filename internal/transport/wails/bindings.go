package wails

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	coreanalytics "telegram-companion/internal/analytics"
	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase"
	analyticsusecase "telegram-companion/internal/usecase/analytics"
	importsusecase "telegram-companion/internal/usecase/imports"
	"telegram-companion/internal/usecase/runtimeconfig"
	"telegram-companion/internal/usecase/scouting"
)

var errDesktopOperationsClosed = errors.New("desktop licensed operations are unavailable")

type SettingsStore interface {
	usecase.AccountStore
	usecase.CatalogStore
	Load(context.Context) (domain.KeywordSettings, error)
	Save(context.Context, domain.KeywordSettings) error
	LoadChannels(context.Context) ([]domain.ManagedChannel, error)
	SaveChannels(context.Context, []domain.ManagedChannel) error
	LoadAppSettings(context.Context) (domain.AppSettings, error)
	SaveAppSettings(context.Context, domain.AppSettings) error
}

type groupRestDelayReleaser interface {
	ReleaseGroupRestDelays(context.Context) error
}

type groupRestCleaner interface {
	ClearGroupRests(context.Context) error
}

type joinIntervalClearer interface {
	ClearJoinIntervals(context.Context) error
}

type catalogMembershipLister interface {
	ListMemberships(context.Context, domain.SourceCatalog, domain.ID) ([]domain.ChannelMembership, error)
}

type StorageMetricsProvider interface {
	Get(ctx context.Context) (scouting.StorageMetrics, error)
}

type AnalyticsService interface {
	Analyze(context.Context, analyticsusecase.AnalysisRequest) (analyticsusecase.AnalysisResult, error)
	CompareTopics(context.Context, analyticsusecase.TopicComparisonRequest) ([]analyticsusecase.TopicSummary, error)
	ModerationStates(context.Context) (map[analyticsusecase.CandidateKey]string, error)
	Moderate(context.Context, string, string, string) error
	AddCandidatesToKeywords(context.Context, []analyticsusecase.CandidateRef) (int, error)
}

type CanonicalAnalyticsService interface {
	Analyze(context.Context) (analyticsusecase.CanonicalAnalysisResult, error)
	List(context.Context, coreanalytics.KeywordClass) ([]coreanalytics.CanonicalKeyword, error)
	Classify(context.Context, string, coreanalytics.KeywordClass) error
	SetTriggerActive(context.Context, string, bool) error
	RemoveForm(context.Context, string, string) (coreanalytics.CanonicalKeyword, error)
	MoveForm(context.Context, string, string) error
	AddForm(context.Context, string, string) error
	BulkImport(context.Context, []coreanalytics.CanonicalImportValue, coreanalytics.KeywordClass) (coreanalytics.CanonicalBulkImportResult, error)
	Delete(context.Context, string) error
	Clear(context.Context, coreanalytics.KeywordClass) error
}

type ScoutHistorySyncer interface {
	SyncHistory(context.Context) error
}

type AnalyticsCollectionScheduler interface {
	Start(context.Context, int) error
	Stop(context.Context) error
	RunNow(context.Context) (domain.AnalyticsSchedulerRun, error)
	Status(context.Context) (usecase.AnalyticsCollectionStatus, error)
	History(context.Context, int) ([]domain.AnalyticsSchedulerRun, error)
}

type AIImportService interface {
	Import(context.Context, importsusecase.ImportRequest) (domain.Import, error)
}

type ImportFileSelector interface {
	SelectImportFile(context.Context, string) (string, error)
}

type ImportCandidateExportSelector interface {
	SelectExportFile(context.Context, string, string) (string, error)
}

type LiveStatisticsExportSelector interface {
	SelectLiveStatisticsExportFile(context.Context, string, string) (string, error)
}

type persistedAIImportService interface {
	List(context.Context) ([]domain.Import, error)
	ListCandidates(context.Context, domain.ID) ([]domain.KeywordCandidate, error)
	ExportCandidates(context.Context, domain.ID, string) (string, error)
}

type FileOpener interface {
	Open(string) error
}

type ProxyStatusProvider interface {
	Status() ProxyStatusDTO
}

type Bindings struct {
	driveAccounts           driveAccountsRuntime
	rootMu                  sync.RWMutex
	root                    context.Context
	automation              *usecase.AutomationController
	accounts                *usecase.AccountService
	catalogs                *usecase.CatalogService
	settings                SettingsStore
	accountRests            AccountRestLister
	runtime                 *runtimeconfig.Store
	storageMetrics          StorageMetricsProvider
	backups                 *usecase.BackupService
	proxyStatus             ProxyStatusProvider
	proxyProfiles           domain.ProxyProfileRepository
	proxyRoutes             ProxyRouteRegistry
	backupMu                sync.RWMutex
	latestBackup            BackupDTO
	backupRunner            usecase.AutomationRunner
	backupLifecycleMu       sync.Mutex
	backupCancel            context.CancelFunc
	backupDone              chan error
	backupClose             func(context.Context) error
	initializationErr       error
	analytics               AnalyticsService
	canonicalAnalytics      CanonicalAnalyticsService
	channelModeration       *usecase.ChannelModerationService
	historySyncer           ScoutHistorySyncer
	analyticsScheduler      AnalyticsCollectionScheduler
	analyticsStore          domain.AnalyticsSchedulerStore
	importer                AIImportService
	importFileSelector      ImportFileSelector
	importExportSelector    ImportCandidateExportSelector
	liveStatsExportSelector LiveStatisticsExportSelector
	fileOpener              FileOpener
	scheduledDM             scheduledDMRuntime
	analysisMu              sync.RWMutex
	analysisStatus          AnalysisStatusDTO
	analysisResults         AnalysisResultsDTO
	comparisonResults       []TopicSummaryDTO
	analysisCancel          context.CancelFunc
	analysisDone            chan struct{}
	operationMu             sync.Mutex
	operationWG             sync.WaitGroup
	operationContext        context.Context
	operationCancel         context.CancelFunc
	operationActive         int
	operationsClosing       bool
}

type BackupRuntime struct {
	Service *usecase.BackupService
	Runner  usecase.AutomationRunner
	Close   func(context.Context) error
}

type DashboardDTO struct {
	Running      bool   `json:"running"`
	Locale       string `json:"locale"`
	ChannelCount int    `json:"channelCount"`
	AccountCount int    `json:"accountCount"`
	LastStatus   string `json:"lastStatus"`
}

type KeywordSettingsDTO struct {
	Keywords              []string `json:"keywords"`
	MinusKeywords         []string `json:"minusKeywords"`
	SharedReply           string   `json:"sharedReply"`
	PrivateReply          string   `json:"privateReply"`
	DeliveryMode          string   `json:"deliveryMode"`
	DirectMessageKeywords []string `json:"directMessageKeywords"`
	Revision              uint64   `json:"revision"`
}

type ManagedChannelDTO struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Link         string `json:"link"`
	Status       string `json:"status"`
	Members      string `json:"members"`
	Sent         int    `json:"sent"`
	LastActivity string `json:"lastActivity"`
	Active       bool   `json:"active"`
	Revision     uint64 `json:"revision"`
}

type AppSettingsDTO struct {
	RepliesPerMinute       int    `json:"repliesPerMinute"`
	MinIntervalSeconds     int    `json:"minIntervalSeconds"`
	JoinIntervalMinMinutes int    `json:"joinIntervalMinMinutes"`
	JoinIntervalMaxMinutes int    `json:"joinIntervalMaxMinutes"`
	JoinIntervalEnabled    bool   `json:"joinIntervalEnabled"`
	GroupRestHours         int    `json:"groupRestHours"`
	GroupRestEnabled       bool   `json:"groupRestEnabled"`
	DirectMessages         bool   `json:"directMessages"`
	Proxy                  string `json:"proxy"`
	Revision               uint64 `json:"revision"`
}

type ProxyStatusDTO struct {
	Mode         string `json:"mode"`
	State        string `json:"state"`
	Address      string `json:"address"`
	Transport    string `json:"transport"`
	LastError    string `json:"lastError"`
	RestartCount int    `json:"restartCount"`
	UpdatedAt    string `json:"updatedAt"`
	AutoRestart  bool   `json:"autoRestart"`
}

type AccountDTO struct {
	ID                        string `json:"id"`
	DisplayName               string `json:"displayName"`
	Username                  string `json:"username"`
	PhoneMasked               string `json:"phoneMasked"`
	Role                      string `json:"role"`
	Status                    string `json:"status"`
	ErrorCode                 string `json:"errorCode,omitempty"`
	Proxy                     string `json:"proxy"`
	ProxyWarning              string `json:"proxyWarning"`
	ProxyProfileID            string `json:"proxyProfileId"`
	ProxyRouteName            string `json:"proxyRouteName"`
	ProxyRouteState           string `json:"proxyRouteState"`
	ProxyRouteUsage           int    `json:"proxyRouteUsage"`
	ProxyRouteCapacity        int    `json:"proxyRouteCapacity"`
	PublicRepliesSent         int64  `json:"publicRepliesSent"`
	PrivateMessagesSent       int64  `json:"privateMessagesSent"`
	NextDelivery              string `json:"nextDelivery"`
	PrivateMessagesClosed     int64  `json:"privateMessagesClosed"`
	LastActivity              string `json:"lastActivity"`
	LegacyPauseReviewRequired bool   `json:"legacyPauseReviewRequired"`
	Revision                  uint64 `json:"revision"`
}

type CatalogEntryDTO struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Link            string `json:"link"`
	Topic           string `json:"topic"`
	Status          string `json:"status"`
	MessageCount    int64  `json:"messageCount"`
	SentCount       int64  `json:"sentCount"`
	LastActivity    string `json:"lastActivity"`
	Active          bool   `json:"active"`
	Member          int    `json:"member"`
	PendingApproval int    `json:"pendingApproval"`
	Joining         int    `json:"joining"`
	Leaving         int    `json:"leaving"`
	Failed          int    `json:"failed"`
	JoinNotBefore   string `json:"joinNotBefore"`
	Planned         bool   `json:"planned"`
	Revision        uint64 `json:"revision"`
}

type StorageMetricsDTO struct {
	RowCount         int64  `json:"rowCount"`
	DatabaseBytes    int64  `json:"databaseBytes"`
	OldestTimestamp  string `json:"oldestTimestamp"`
	NewestTimestamp  string `json:"newestTimestamp"`
	PausedForLowDisk bool   `json:"pausedForLowDisk"`
	LastRefresh      string `json:"lastRefresh"`
}

type BackupDTO struct {
	ID          string `json:"id"`
	ArchivePath string `json:"archivePath"`
	Kind        string `json:"kind"`
	SizeBytes   int64  `json:"sizeBytes"`
	SHA256      string `json:"sha256"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt"`
	VerifiedAt  string `json:"verifiedAt"`
	Error       string `json:"error"`
}

type AnalysisRequestDTO struct {
	SourceScope  string   `json:"sourceScope"`
	ProfileID    string   `json:"profileId"`
	SourceTopics []string `json:"sourceTopics"`
}

type AnalysisStatusDTO struct {
	Status      string `json:"status"`
	Operation   string `json:"operation"`
	Progress    int    `json:"progress"`
	Error       string `json:"error"`
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt"`
}

type AnalysisCandidateDTO struct {
	RunID           string  `json:"runId"`
	NormalizedValue string  `json:"normalizedValue"`
	DisplayValue    string  `json:"displayValue"`
	Kind            string  `json:"kind"`
	Frequency       int     `json:"frequency"`
	SourceDiversity int     `json:"sourceDiversity"`
	Score           float64 `json:"score"`
	Source          string  `json:"source"`
	ModerationState string  `json:"moderationState"`
}

type AnalysisResultsDTO struct {
	RunID             string                 `json:"runId"`
	Status            string                 `json:"status"`
	GeneralPhrases    []AnalysisCandidateDTO `json:"generalPhrases"`
	GeneralWords      []AnalysisCandidateDTO `json:"generalWords"`
	ProfileCandidates []AnalysisCandidateDTO `json:"profileCandidates"`
}

type CanonicalFormDTO struct {
	Value     string `json:"value"`
	Frequency int64  `json:"frequency"`
}

type CanonicalKeywordDTO struct {
	ID             string             `json:"id"`
	Canonical      string             `json:"canonical"`
	Language       string             `json:"language"`
	Class          string             `json:"class"`
	Frequency      int64              `json:"frequency"`
	FrequencyDelta int64              `json:"frequencyDelta"`
	MessageCount   int64              `json:"messageCount"`
	LastSeen       string             `json:"lastSeen"`
	Forms          []CanonicalFormDTO `json:"forms"`
	TriggerActive  bool               `json:"triggerActive"`
}

type CanonicalAnalysisResultDTO struct {
	MessageCount int64 `json:"messageCount"`
	KeywordCount int   `json:"keywordCount"`
}

type CanonicalImportEntryDTO struct {
	Canonical string   `json:"canonical"`
	Forms     []string `json:"forms"`
}

type CanonicalBulkImportResultDTO struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"`
}

type AnalyticsCollectionStatusDTO struct {
	Running          bool                   `json:"running"`
	Collecting       bool                   `json:"collecting"`
	State            string                 `json:"state"`
	IntervalMinutes  int                    `json:"intervalMinutes"`
	LastRunAt        string                 `json:"lastRunAt"`
	NextRunAt        string                 `json:"nextRunAt"`
	LastError        string                 `json:"lastError"`
	AllTimeMessages  int64                  `json:"allTimeMessages"`
	AllTimeKeywords  int64                  `json:"allTimeKeywords"`
	AllTimeGroups    int64                  `json:"allTimeGroups"`
	LatestCollection AnalyticsRunMetricsDTO `json:"latestCollection"`
	Totals           AnalyticsTotalsDTO     `json:"totals"`
}

type AnalyticsSettingsDTO struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"intervalMinutes"`
}

type AnalyticsRunDTO struct {
	ID              string   `json:"id"`
	StartedAt       string   `json:"startedAt"`
	FinishedAt      string   `json:"finishedAt"`
	NewMessages     int64    `json:"newMessages"`
	ExtractedWords  int64    `json:"extractedWords"`
	NewCanonicals   int64    `json:"newCanonicals"`
	ProcessedGroups int64    `json:"processedGroups"`
	DurationMillis  int64    `json:"durationMillis"`
	Errors          []string `json:"errors"`
}

type AnalyticsRunMetricsDTO struct {
	NewMessages       int64    `json:"newMessages"`
	ExtractedWords    int64    `json:"extractedWords"`
	NewCanonicalWords int64    `json:"newCanonicalWords"`
	ProcessedGroups   int64    `json:"processedGroups"`
	DurationMillis    int64    `json:"durationMillis"`
	Errors            []string `json:"errors"`
}

type AnalyticsTotalsDTO struct {
	Messages       int64 `json:"messages"`
	CanonicalWords int64 `json:"canonicalWords"`
	Forms          int64 `json:"forms"`
	Groups         int64 `json:"groups"`
	KeywordDBBytes int64 `json:"keywordDbBytes"`
}

type AnalyticsMetricsDTO struct {
	CanonicalCount      int64 `json:"canonicalCount"`
	FormCount           int64 `json:"formCount"`
	MessageCount        int64 `json:"messageCount"`
	GroupCount          int64 `json:"groupCount"`
	LogicalKeywordBytes int64 `json:"logicalKeywordBytes"`
}

type AnalyticsTableColumnPreferenceDTO struct {
	Key     string `json:"key"`
	Width   int    `json:"width"`
	Visible bool   `json:"visible"`
}

type AnalyticsTablePreferenceDTO struct {
	Tab           string                              `json:"tab"`
	Columns       []AnalyticsTableColumnPreferenceDTO `json:"columns"`
	SortBy        string                              `json:"sortBy"`
	SortDirection string                              `json:"sortDirection"`
	PageSize      int                                 `json:"pageSize"`
}

type AnalysisCandidateRefDTO struct {
	RunID           string `json:"runId"`
	NormalizedValue string `json:"normalizedValue"`
	Kind            string `json:"kind"`
}

type TopicComparisonRequestDTO struct {
	SourceScope string   `json:"sourceScope"`
	ProfileID   string   `json:"profileId"`
	Topics      []string `json:"topics"`
}

type TopicSummaryDTO struct {
	Topic                  string                 `json:"topic"`
	Count                  int                    `json:"count"`
	DistinctCandidateCount int                    `json:"distinctCandidateCount"`
	TopPhrases             []AnalysisCandidateDTO `json:"topPhrases"`
	RelativeShare          float64                `json:"relativeShare"`
}

type AIImportRequestDTO struct {
	Path            string `json:"path"`
	SHA256          string `json:"sha256"`
	RightsConfirmed bool   `json:"rightsConfirmed"`
	ProfileID       string `json:"profileId"`
}

type AIImportDTO struct {
	ID          string `json:"id"`
	FileName    string `json:"fileName"`
	FilePath    string `json:"filePath"`
	Status      string `json:"status"`
	RecordCount int    `json:"recordCount"`
	ImportedAt  string `json:"importedAt"`
	Error       string `json:"error"`
}

type ChannelModerationAccountDTO struct {
	Title              string `json:"title"`
	Role               string `json:"role"`
	RequestSubmittedAt string `json:"requestSubmittedAt"`
	JoinedAt           string `json:"joinedAt"`
	DurationSeconds    int64  `json:"durationSeconds"`
	Status             string `json:"status"`
}

type ChannelModerationDTO struct {
	Catalog         string                        `json:"catalog"`
	ChannelID       string                        `json:"channelID"`
	Title           string                        `json:"title"`
	Link            string                        `json:"link"`
	Topic           string                        `json:"topic"`
	Applications    int                           `json:"applications"`
	Joined          int                           `json:"joined"`
	Pending         int                           `json:"pending"`
	Status          string                        `json:"status"`
	FirstRequestAt  string                        `json:"firstRequestAt"`
	DurationSeconds int64                         `json:"durationSeconds"`
	Accounts        []ChannelModerationAccountDTO `json:"accounts"`
}

var (
	channelInputSplitter      = regexp.MustCompile(`[\s,;]+`)
	discussionFragmentPattern = regexp.MustCompile(`^tc-discussion=([0-9]+)$`)
)

func NewBindings(automation *usecase.AutomationController, stores ...SettingsStore) *Bindings {
	if len(stores) > 1 {
		return &Bindings{automation: automation, runtime: runtimeconfig.NewStore(runtimeconfig.Snapshot{}), initializationErr: errors.New("only one settings store is supported")}
	}
	var settings SettingsStore
	if len(stores) == 1 {
		settings = stores[0]
	}
	if settings == nil {
		return &Bindings{automation: automation, runtime: runtimeconfig.NewStore(runtimeconfig.Snapshot{})}
	}
	bindings, err := NewBindingsWithError(automation, settings)
	if err == nil {
		return bindings
	}
	bindings = &Bindings{
		automation:        automation,
		accounts:          usecase.NewAccountService(settings),
		catalogs:          usecase.NewCatalogService(settings),
		settings:          settings,
		runtime:           runtimeconfig.NewStore(runtimeconfig.Snapshot{}),
		initializationErr: err,
	}
	if store, ok := settings.(usecase.ChannelModerationStore); ok {
		bindings.channelModeration = usecase.NewChannelModerationService(store)
	}
	return bindings
}

func NewBindingsWithStorageMetrics(automation *usecase.AutomationController, settings SettingsStore, metrics StorageMetricsProvider) *Bindings {
	bindings := NewBindings(automation, settings)
	bindings.storageMetrics = metrics
	return bindings
}

func NewBindingsWithBackups(automation *usecase.AutomationController, settings SettingsStore, backups *usecase.BackupService) *Bindings {
	bindings := NewBindings(automation, settings)
	bindings.ConfigureBackups(BackupRuntime{Service: backups})
	return bindings
}

func NewBindingsWithAnalytics(
	automation *usecase.AutomationController,
	settings SettingsStore,
	analytics AnalyticsService,
	importer AIImportService,
	fileOpener FileOpener,
) *Bindings {
	bindings := NewBindings(automation, settings)
	bindings.analytics = analytics
	bindings.importer = importer
	bindings.fileOpener = fileOpener
	bindings.analysisStatus = AnalysisStatusDTO{Status: "idle"}
	return bindings
}

// ConfigureProduction injects the Task 12 services that are optional only in
// unit tests. Desktop composition calls this before Wails starts.
func (b *Bindings) ConfigureProduction(metrics StorageMetricsProvider, analytics AnalyticsService, importer AIImportService, opener FileOpener, history ...ScoutHistorySyncer) {
	b.storageMetrics = metrics
	b.analytics = analytics
	b.importer = importer
	b.fileOpener = opener
	if len(history) > 0 {
		b.historySyncer = history[0]
	}
	b.analysisStatus = AnalysisStatusDTO{Status: "idle"}
}

func (b *Bindings) ConfigureProxyStatus(provider ProxyStatusProvider) {
	b.proxyStatus = provider
}

func (b *Bindings) ConfigureCanonicalAnalytics(service CanonicalAnalyticsService) {
	b.canonicalAnalytics = service
}

func (b *Bindings) ConfigureAnalyticsCollection(scheduler AnalyticsCollectionScheduler, store domain.AnalyticsSchedulerStore) {
	b.analyticsScheduler = scheduler
	b.analyticsStore = store
}

func (b *Bindings) ConfigureImportFileSelector(selector ImportFileSelector) {
	b.importFileSelector = selector
}

func (b *Bindings) ConfigureImportCandidateExportSelector(selector ImportCandidateExportSelector) {
	b.importExportSelector = selector
}

func (b *Bindings) ConfigureLiveStatisticsExportSelector(selector LiveStatisticsExportSelector) {
	b.liveStatsExportSelector = selector
}

func (b *Bindings) ConfigureBackups(runtime BackupRuntime) {
	b.backups = runtime.Service
	b.backupRunner = runtime.Runner
	b.backupClose = runtime.Close
}

func (b *Bindings) RuntimeStore() *runtimeconfig.Store { return b.runtime }

func (b *Bindings) SetRootContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	b.rootMu.Lock()
	b.root = ctx
	b.rootMu.Unlock()
	b.operationMu.Lock()
	if b.operationActive == 0 && !b.operationsClosing {
		if b.operationCancel != nil {
			b.operationCancel()
		}
		b.operationContext = nil
		b.operationCancel = nil
	}
	b.operationMu.Unlock()
}

func (b *Bindings) rootContext() context.Context {
	b.rootMu.RLock()
	ctx := b.root
	b.rootMu.RUnlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (b *Bindings) beginOperation(timeout time.Duration) (context.Context, func(), error) {
	b.operationMu.Lock()
	if b.operationsClosing {
		b.operationMu.Unlock()
		return nil, nil, errDesktopOperationsClosed
	}
	if b.operationContext == nil {
		b.operationContext, b.operationCancel = context.WithCancel(b.rootContext())
	}
	operationContext := b.operationContext
	b.operationActive++
	b.operationWG.Add(1)
	b.operationMu.Unlock()
	ctx, cancel := context.WithTimeout(operationContext, timeout)
	var once sync.Once
	done := func() {
		once.Do(func() {
			cancel()
			b.operationMu.Lock()
			b.operationActive--
			b.operationMu.Unlock()
			b.operationWG.Done()
		})
	}
	return ctx, done, nil
}

func (b *Bindings) StopOperations(ctx context.Context) error {
	b.operationMu.Lock()
	b.operationsClosing = true
	cancel := b.operationCancel
	b.operationMu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		b.operationWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *Bindings) SetAutomationController(controller *usecase.AutomationController) {
	b.automation = controller
}

func (b *Bindings) ProductionReady() error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	missing := make([]string, 0, 8)
	if b.accountRests == nil {
		missing = append(missing, "account rests")
	}
	if b.storageMetrics == nil {
		missing = append(missing, "storage metrics")
	}
	if b.analytics == nil {
		missing = append(missing, "analytics")
	}
	if b.analyticsScheduler == nil || b.analyticsStore == nil {
		missing = append(missing, "analytics collection")
	}
	if b.importer == nil {
		missing = append(missing, "AI import")
	}
	if b.fileOpener == nil {
		missing = append(missing, "file opener")
	}
	if b.backups == nil || b.backupRunner == nil {
		missing = append(missing, "backup")
	}
	if len(missing) != 0 {
		return fmt.Errorf("production services missing: %s", strings.Join(missing, ", "))
	}
	return nil
}

func NewBindingsWithError(automation *usecase.AutomationController, settings SettingsStore) (*Bindings, error) {
	snapshot, err := loadRuntimeSnapshot(settings)
	if err != nil {
		return nil, err
	}
	appSettings, err := settings.LoadAppSettings(context.Background())
	if err != nil {
		return nil, fmt.Errorf("load app settings for join interval: %w", err)
	}
	catalogs := usecase.NewCatalogService(settings)
	appSettings = appSettings.Normalized()
	interval, err := appSettings.JoinIntervalRange()
	if err != nil {
		return nil, err
	}
	if err := catalogs.SetJoinIntervalRange(interval.MinMinutes, interval.MaxMinutes); err != nil {
		return nil, err
	}
	catalogs.SetJoinIntervalEnabled(appSettings.JoinIntervalEnabled)
	if !appSettings.JoinIntervalEnabled {
		if clearer, ok := settings.(joinIntervalClearer); ok {
			if err := clearer.ClearJoinIntervals(context.Background()); err != nil {
				return nil, fmt.Errorf("clear disabled join intervals: %w", err)
			}
		}
	}
	if err := catalogs.SetGroupRestHours(appSettings.GroupRestHours); err != nil {
		return nil, err
	}
	catalogs.SetGroupRestEnabled(appSettings.GroupRestEnabled)
	if !appSettings.GroupRestEnabled {
		if cleaner, ok := settings.(groupRestCleaner); ok {
			if err := cleaner.ClearGroupRests(context.Background()); err != nil {
				return nil, fmt.Errorf("clear disabled account rests: %w", err)
			}
		} else if releaser, ok := settings.(groupRestDelayReleaser); ok {
			if err := releaser.ReleaseGroupRestDelays(context.Background()); err != nil {
				return nil, fmt.Errorf("release disabled account rests: %w", err)
			}
		}
	}
	bindings := &Bindings{
		automation: automation,
		accounts:   usecase.NewAccountService(settings),
		catalogs:   catalogs,
		settings:   settings,
		runtime:    runtimeconfig.NewStore(snapshot),
	}
	if store, ok := settings.(usecase.ChannelModerationStore); ok {
		bindings.channelModeration = usecase.NewChannelModerationService(store)
	}
	return bindings, nil
}

func (b *Bindings) StartAutomation() error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	backgroundErr := b.StartBackgroundServices()
	return errors.Join(backgroundErr, b.automation.Start(b.rootContext()))
}

func (b *Bindings) StopAutomation() error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(b.rootContext()), 10*time.Second)
	defer cancel()
	return b.automation.Stop(ctx)
}

func (b *Bindings) StartBackgroundServices() error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	if b.backupRunner == nil {
		return nil
	}
	b.backupLifecycleMu.Lock()
	defer b.backupLifecycleMu.Unlock()
	if b.backupCancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(b.rootContext())
	done := make(chan error, 1)
	b.backupCancel = cancel
	b.backupDone = done
	go func() { done <- b.backupRunner.Run(ctx) }()
	return nil
}

func (b *Bindings) CloseBackgroundServices(ctx context.Context) error {
	b.backupLifecycleMu.Lock()
	cancel := b.backupCancel
	done := b.backupDone
	closeRuntime := b.backupClose
	b.backupCancel = nil
	b.backupDone = nil
	b.backupClose = nil
	b.backupLifecycleMu.Unlock()
	var runErr error
	if cancel != nil {
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				runErr = err
			}
		case <-ctx.Done():
			runErr = ctx.Err()
		}
	}
	var closeErr error
	if closeRuntime != nil {
		closeErr = closeRuntime(context.WithoutCancel(ctx))
	}
	return errors.Join(runErr, closeErr)
}

func (b *Bindings) GetDashboard() (DashboardDTO, error) {
	lastStatus := "ready"
	if err := b.runtimeError(); err != nil {
		lastStatus = "error: " + err.Error()
	}
	return DashboardDTO{
		Running:    b.automation.Running(),
		Locale:     "ru",
		LastStatus: lastStatus,
	}, nil
}

func (b *Bindings) StartAnalysis(request AnalysisRequestDTO) (AnalysisStatusDTO, error) {
	if err := b.runtimeError(); err != nil {
		return AnalysisStatusDTO{}, err
	}
	if b.analytics == nil {
		return AnalysisStatusDTO{}, errors.New("analytics service is not configured")
	}
	b.analysisMu.Lock()
	if b.analysisCancel != nil {
		status := b.analysisStatus
		b.analysisMu.Unlock()
		return status, errors.New("analysis is already running")
	}
	ctx, cancel := context.WithCancel(b.rootContext())
	done := make(chan struct{})
	started := time.Now().UTC()
	b.analysisCancel = cancel
	b.analysisDone = done
	b.analysisStatus = AnalysisStatusDTO{Status: "running", Operation: "analysis", Progress: 0, StartedAt: started.Format(time.RFC3339Nano)}
	b.analysisResults = AnalysisResultsDTO{}
	b.comparisonResults = nil
	status := b.analysisStatus
	b.analysisMu.Unlock()

	go b.runAnalysis(ctx, done, analyticsusecase.AnalysisRequest{
		SourceScope: strings.TrimSpace(request.SourceScope), ProfileID: strings.TrimSpace(request.ProfileID),
		SourceTopics: append([]string(nil), request.SourceTopics...),
	})
	return status, nil
}

func (b *Bindings) runAnalysis(ctx context.Context, done chan struct{}, request analyticsusecase.AnalysisRequest) {
	defer b.finishAnalysis(done)
	if b.historySyncer != nil {
		if err := b.historySyncer.SyncHistory(ctx); err != nil {
			b.publishAnalysisError(err)
			return
		}
	}
	result, err := b.analytics.Analyze(ctx, request)
	var states map[analyticsusecase.CandidateKey]string
	if err == nil {
		states, err = b.analytics.ModerationStates(context.WithoutCancel(ctx))
	}
	completed := time.Now().UTC().Format(time.RFC3339Nano)
	b.analysisMu.Lock()
	defer b.analysisMu.Unlock()
	if result.RunID != "" || result.Status != "" {
		b.analysisResults = analysisResultsDTO(result, states)
	}
	if errors.Is(err, context.Canceled) {
		b.analysisStatus.Status = "cancelled"
		b.analysisStatus.Progress = 0
		b.analysisStatus.Error = ""
	} else if err != nil {
		b.analysisStatus.Status = "error"
		b.analysisStatus.Error = err.Error()
	} else {
		b.analysisStatus.Status = result.Status
		if b.analysisStatus.Status == "" {
			b.analysisStatus.Status = "complete"
		}
		b.analysisStatus.Progress = 100
		b.analysisStatus.Error = ""
	}
	b.analysisStatus.CompletedAt = completed
}

func (b *Bindings) publishAnalysisError(err error) {
	b.analysisMu.Lock()
	defer b.analysisMu.Unlock()
	b.analysisStatus.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if errors.Is(err, context.Canceled) {
		b.analysisStatus.Status = "cancelled"
		b.analysisStatus.Progress = 0
		b.analysisStatus.Error = ""
		return
	}
	b.analysisStatus.Status = "error"
	b.analysisStatus.Error = err.Error()
}

func (b *Bindings) StartTopicComparison(request TopicComparisonRequestDTO) (AnalysisStatusDTO, error) {
	if err := b.runtimeError(); err != nil {
		return AnalysisStatusDTO{}, err
	}
	if b.analytics == nil {
		return AnalysisStatusDTO{}, errors.New("analytics service is not configured")
	}
	b.analysisMu.Lock()
	if b.analysisCancel != nil {
		status := b.analysisStatus
		b.analysisMu.Unlock()
		return status, errors.New("analytics operation is already running")
	}
	ctx, cancel := context.WithCancel(b.rootContext())
	done := make(chan struct{})
	b.analysisCancel = cancel
	b.analysisDone = done
	b.analysisStatus = AnalysisStatusDTO{Status: "running", Operation: "comparison", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	b.analysisResults = AnalysisResultsDTO{}
	b.comparisonResults = nil
	status := b.analysisStatus
	b.analysisMu.Unlock()

	go b.runTopicComparison(ctx, done, analyticsusecase.TopicComparisonRequest{
		SourceScope: strings.TrimSpace(request.SourceScope), ProfileID: strings.TrimSpace(request.ProfileID),
		Topics: append([]string(nil), request.Topics...),
	})
	return status, nil
}

func (b *Bindings) runTopicComparison(ctx context.Context, done chan struct{}, request analyticsusecase.TopicComparisonRequest) {
	defer b.finishAnalysis(done)
	summaries, err := b.analytics.CompareTopics(ctx, analyticsusecase.TopicComparisonRequest{
		SourceScope: request.SourceScope, ProfileID: request.ProfileID, Topics: append([]string(nil), request.Topics...),
	})
	if err == nil {
		err = ctx.Err()
	}
	var states map[analyticsusecase.CandidateKey]string
	if err == nil {
		states, err = b.analytics.ModerationStates(ctx)
	}
	if err == nil {
		err = ctx.Err()
	}
	var rows []TopicSummaryDTO
	if err == nil {
		rows = make([]TopicSummaryDTO, len(summaries))
		for index, summary := range summaries {
			rows[index] = TopicSummaryDTO{
				Topic: summary.Topic, Count: summary.Count, DistinctCandidateCount: summary.DistinctCandidateCount,
				TopPhrases: analysisCandidateDTOs("", summary.Topic, summary.TopPhrases, states), RelativeShare: summary.RelativeShare,
			}
		}
	}

	b.analysisMu.Lock()
	defer b.analysisMu.Unlock()
	b.analysisStatus.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		b.analysisStatus.Status = "cancelled"
		b.analysisStatus.Progress = 0
		b.analysisStatus.Error = ""
		return
	}
	if err != nil {
		b.analysisStatus.Status = "error"
		b.analysisStatus.Error = err.Error()
		return
	}
	b.comparisonResults = rows
	b.analysisStatus.Status = "complete"
	b.analysisStatus.Progress = 100
	b.analysisStatus.Error = ""
}

func (b *Bindings) finishAnalysis(done chan struct{}) {
	close(done)
	b.analysisMu.Lock()
	if b.analysisDone == done {
		b.analysisCancel = nil
		b.analysisDone = nil
	}
	b.analysisMu.Unlock()
}

func (b *Bindings) CancelAnalysis() error {
	b.analysisMu.RLock()
	cancel := b.analysisCancel
	b.analysisMu.RUnlock()
	if cancel == nil {
		return nil
	}
	cancel()
	return nil
}

func (b *Bindings) StopAnalysis(ctx context.Context) error {
	b.analysisMu.RLock()
	cancel := b.analysisCancel
	done := b.analysisDone
	b.analysisMu.RUnlock()
	if cancel == nil || done == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *Bindings) GetAnalysisStatus() AnalysisStatusDTO {
	b.analysisMu.RLock()
	defer b.analysisMu.RUnlock()
	if b.analysisStatus.Status == "" {
		return AnalysisStatusDTO{Status: "idle"}
	}
	return b.analysisStatus
}

func (b *Bindings) GetAnalysisResults() AnalysisResultsDTO {
	b.analysisMu.RLock()
	defer b.analysisMu.RUnlock()
	return cloneAnalysisResults(b.analysisResults)
}

func (b *Bindings) GetTopicComparisonResults() []TopicSummaryDTO {
	b.analysisMu.RLock()
	defer b.analysisMu.RUnlock()
	rows := make([]TopicSummaryDTO, len(b.comparisonResults))
	for index, row := range b.comparisonResults {
		rows[index] = row
		rows[index].TopPhrases = append([]AnalysisCandidateDTO(nil), row.TopPhrases...)
	}
	return rows
}

func (b *Bindings) ModerateAnalysisCandidate(normalizedValue, kind, state string) error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	if b.analytics == nil {
		return errors.New("analytics service is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return b.analytics.Moderate(ctx, normalizedValue, kind, state)
}

func (b *Bindings) AddAnalysisCandidates(refs []AnalysisCandidateRefDTO) (int, error) {
	if err := b.runtimeError(); err != nil {
		return 0, err
	}
	if b.analytics == nil {
		return 0, errors.New("analytics service is not configured")
	}
	input := make([]analyticsusecase.CandidateRef, len(refs))
	for index, ref := range refs {
		input[index] = analyticsusecase.CandidateRef{RunID: ref.RunID, NormalizedValue: ref.NormalizedValue, Kind: ref.Kind}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	added, err := b.analytics.AddCandidatesToKeywords(ctx, input)
	if err != nil {
		return added, err
	}
	settings, err := b.settings.Load(ctx)
	if err != nil {
		return added, err
	}
	b.runtime.Update(func(next *runtimeconfig.Snapshot) {
		next.Keywords = append([]string(nil), settings.Keywords...)
		next.MinusKeywords = append([]string(nil), settings.MinusKeywords...)
		next.SharedReply = settings.SharedReply
		next.PrivateReply = settings.PrivateReply
		next.PrivateReplyPresent = settings.PrivateReplyPresent
		next.DeliveryMode = settings.DeliveryMode.Normalized()
		next.DirectMessageKeywords = append([]string(nil), settings.DirectMessageKeywords...)
	})
	return added, nil
}

func (b *Bindings) AnalyzeCanonicalKeywords() (CanonicalAnalysisResultDTO, error) {
	if b.analyticsScheduler != nil {
		run, err := b.analyticsScheduler.RunNow(b.rootContext())
		return CanonicalAnalysisResultDTO{MessageCount: run.Metrics.NewMessages, KeywordCount: int(run.Metrics.ExtractedWords)}, err
	}
	if b.canonicalAnalytics == nil {
		return CanonicalAnalysisResultDTO{}, errors.New("canonical analytics service is not configured")
	}
	ctx, done, err := b.beginOperation(30 * time.Minute)
	if err != nil {
		return CanonicalAnalysisResultDTO{}, err
	}
	defer done()
	if b.historySyncer != nil {
		if err := b.historySyncer.SyncHistory(ctx); err != nil {
			return CanonicalAnalysisResultDTO{}, err
		}
	}
	result, err := b.canonicalAnalytics.Analyze(ctx)
	if err != nil {
		return CanonicalAnalysisResultDTO{}, err
	}
	return CanonicalAnalysisResultDTO{MessageCount: result.MessageCount, KeywordCount: result.KeywordCount}, nil
}

func (b *Bindings) StartCanonicalAnalytics(intervalMinutes int) error {
	return b.StartAnalyticsCollection(intervalMinutes)
}

func (b *Bindings) StopCanonicalAnalytics() error {
	return b.StopAnalyticsCollection()
}

func (b *Bindings) GetCanonicalAnalyticsStatus() (AnalyticsCollectionStatusDTO, error) {
	return b.GetAnalyticsCollectionStatus()
}

func (b *Bindings) StartAnalyticsCollection(intervalMinutes int) error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	if !validAnalyticsInterval(intervalMinutes) {
		return errors.New("analytics interval must be one of 1, 5, 10, 30, 60, or 120 minutes")
	}
	if b.analyticsScheduler == nil {
		return errors.New("analytics collection scheduler is not configured")
	}
	return b.analyticsScheduler.Start(b.rootContext(), intervalMinutes)
}

func (b *Bindings) StopAnalyticsCollection() error {
	if b.analyticsScheduler == nil {
		return errors.New("analytics collection scheduler is not configured")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(b.rootContext()), 30*time.Second)
	defer cancel()
	return b.analyticsScheduler.Stop(ctx)
}

func (b *Bindings) RunAnalyticsCollectionNow() (AnalyticsRunDTO, error) {
	if err := b.runtimeError(); err != nil {
		return AnalyticsRunDTO{}, err
	}
	if b.analyticsScheduler == nil {
		return AnalyticsRunDTO{}, errors.New("analytics collection scheduler is not configured")
	}
	run, err := b.analyticsScheduler.RunNow(b.rootContext())
	return analyticsRunDTO(run), err
}

func (b *Bindings) GetAnalyticsCollectionStatus() (AnalyticsCollectionStatusDTO, error) {
	if b.analyticsScheduler == nil {
		return AnalyticsCollectionStatusDTO{}, errors.New("analytics collection scheduler is not configured")
	}
	status, err := b.analyticsScheduler.Status(b.rootContext())
	if err != nil {
		return AnalyticsCollectionStatusDTO{}, err
	}
	return analyticsCollectionStatusDTO(status), nil
}

func (b *Bindings) GetAnalyticsCollectionHistory(limit int) ([]AnalyticsRunDTO, error) {
	if b.analyticsScheduler == nil {
		return nil, errors.New("analytics collection scheduler is not configured")
	}
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("analytics history limit must be between 1 and 1000")
	}
	runs, err := b.analyticsScheduler.History(b.rootContext(), limit)
	if err != nil {
		return nil, err
	}
	result := make([]AnalyticsRunDTO, len(runs))
	for index, run := range runs {
		result[index] = analyticsRunDTO(run)
	}
	return result, nil
}

func (b *Bindings) GetChannelModeration() ([]ChannelModerationDTO, error) {
	if b.channelModeration == nil {
		return nil, errors.New("channel moderation service is not configured")
	}
	ctx, done, err := b.beginOperation(15 * time.Second)
	if err != nil {
		return nil, err
	}
	defer done()
	rows, err := b.channelModeration.List(ctx, time.Now())
	if err != nil {
		return nil, err
	}
	return channelModerationDTOs(rows), nil
}

func (b *Bindings) RetryCatalogJoin(catalog string, channelID string) ([]ChannelModerationDTO, error) {
	if err := b.runtimeError(); err != nil {
		return nil, err
	}
	if b.channelModeration == nil {
		return nil, errors.New("channel moderation service is not configured")
	}
	ctx, done, err := b.beginOperation(15 * time.Second)
	if err != nil {
		return nil, err
	}
	defer done()
	source := domain.SourceCatalog(catalog)
	if _, err := b.catalogs.RetryJoin(ctx, source, domain.ID(channelID)); err != nil {
		return nil, err
	}
	rows, err := b.channelModeration.List(ctx, time.Now())
	if err != nil {
		return nil, err
	}
	return channelModerationDTOs(rows), nil
}

func (b *Bindings) GetAnalyticsCollectionSettings() (AnalyticsSettingsDTO, error) {
	if b.analyticsStore == nil {
		return AnalyticsSettingsDTO{}, errors.New("analytics collection store is not configured")
	}
	settings, err := b.analyticsStore.AnalyticsSettings(b.rootContext())
	return AnalyticsSettingsDTO{Enabled: settings.Enabled, IntervalMinutes: settings.IntervalMinutes}, err
}

func (b *Bindings) SetAnalyticsCollectionInterval(intervalMinutes int) (AnalyticsSettingsDTO, error) {
	if err := b.runtimeError(); err != nil {
		return AnalyticsSettingsDTO{}, err
	}
	if !validAnalyticsInterval(intervalMinutes) {
		return AnalyticsSettingsDTO{}, errors.New("analytics interval must be one of 1, 5, 10, 30, 60, or 120 minutes")
	}
	if b.analyticsStore == nil {
		return AnalyticsSettingsDTO{}, errors.New("analytics collection store is not configured")
	}
	settings, err := b.analyticsStore.AnalyticsSettings(b.rootContext())
	if err != nil {
		return AnalyticsSettingsDTO{}, err
	}
	settings.IntervalMinutes = intervalMinutes
	if settings.Enabled {
		err = b.StartAnalyticsCollection(intervalMinutes)
	} else {
		err = b.analyticsStore.SaveAnalyticsSettings(b.rootContext(), settings)
	}
	return AnalyticsSettingsDTO{Enabled: settings.Enabled, IntervalMinutes: settings.IntervalMinutes}, err
}

func (b *Bindings) GetAnalyticsCollectionMetrics() (AnalyticsMetricsDTO, error) {
	if b.analyticsStore == nil {
		return AnalyticsMetricsDTO{}, errors.New("analytics collection store is not configured")
	}
	metrics, err := b.analyticsStore.AnalyticsMetrics(b.rootContext())
	if err != nil {
		return AnalyticsMetricsDTO{}, err
	}
	return AnalyticsMetricsDTO{
		CanonicalCount: metrics.CanonicalCount, FormCount: metrics.FormCount, MessageCount: metrics.MessageCount,
		GroupCount: metrics.GroupCount, LogicalKeywordBytes: metrics.LogicalKeywordBytes,
	}, nil
}

func (b *Bindings) GetAnalyticsServiceWords(language string) ([]string, error) {
	parsed, err := analyticsLanguage(language)
	if err != nil {
		return nil, err
	}
	if b.analyticsStore == nil {
		return nil, errors.New("analytics collection store is not configured")
	}
	return b.analyticsStore.ServiceWords(b.rootContext(), parsed)
}

func (b *Bindings) AddAnalyticsServiceWord(language, value string) error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	parsed, err := analyticsLanguage(language)
	if err != nil {
		return err
	}
	value, err = validatedServiceWord(value)
	if err != nil {
		return err
	}
	if b.analyticsStore == nil {
		return errors.New("analytics collection store is not configured")
	}
	return b.analyticsStore.UpsertServiceWord(b.rootContext(), domain.AnalyticsServiceWord{Language: parsed, Value: value})
}

func (b *Bindings) DeleteAnalyticsServiceWord(language, value string) error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	parsed, err := analyticsLanguage(language)
	if err != nil {
		return err
	}
	value, err = validatedServiceWord(value)
	if err != nil {
		return err
	}
	if b.analyticsStore == nil {
		return errors.New("analytics collection store is not configured")
	}
	return b.analyticsStore.DeleteServiceWord(b.rootContext(), parsed, value)
}

func (b *Bindings) ListServiceCanonicalKeywords() ([]CanonicalKeywordDTO, error) {
	result := make([]CanonicalKeywordDTO, 0)
	for _, language := range []domain.AnalyticsLanguage{domain.AnalyticsLanguageRU, domain.AnalyticsLanguageEN} {
		words, err := b.GetAnalyticsServiceWords(string(language))
		if err != nil {
			return nil, err
		}
		for _, word := range words {
			result = append(result, CanonicalKeywordDTO{ID: serviceWordID(language, word), Canonical: word, Language: string(language), Class: "service", Forms: []CanonicalFormDTO{}})
		}
	}
	return result, nil
}

func (b *Bindings) ClearServiceCanonicalKeywords() error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	for _, language := range []domain.AnalyticsLanguage{domain.AnalyticsLanguageRU, domain.AnalyticsLanguageEN} {
		words, err := b.GetAnalyticsServiceWords(string(language))
		if err != nil {
			return err
		}
		for _, word := range words {
			if err := b.analyticsStore.DeleteServiceWord(b.rootContext(), language, word); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *Bindings) GetAnalyticsTablePreferences(tab string) (AnalyticsTablePreferenceDTO, error) {
	if err := validateAnalyticsTab(tab); err != nil {
		return AnalyticsTablePreferenceDTO{}, err
	}
	if b.analyticsStore == nil {
		return AnalyticsTablePreferenceDTO{}, errors.New("analytics collection store is not configured")
	}
	preference, err := b.analyticsStore.TablePreference(b.rootContext(), tab)
	if err != nil {
		return AnalyticsTablePreferenceDTO{}, err
	}
	result := AnalyticsTablePreferenceDTO{
		Tab: preference.Tab, SortBy: preference.SortBy, SortDirection: string(preference.SortDirection), PageSize: preference.PageSize,
		Columns: make([]AnalyticsTableColumnPreferenceDTO, len(preference.Columns)),
	}
	for index, column := range preference.Columns {
		result.Columns[index] = AnalyticsTableColumnPreferenceDTO{Key: column.Key, Width: column.Width, Visible: column.Visible}
	}
	return result, nil
}

func (b *Bindings) SaveAnalyticsTablePreferences(preference AnalyticsTablePreferenceDTO) error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	if err := validateAnalyticsTab(preference.Tab); err != nil {
		return err
	}
	persisted, err := validateAnalyticsTablePreference(preference)
	if err != nil {
		return err
	}
	if b.analyticsStore == nil {
		return errors.New("analytics collection store is not configured")
	}
	return b.analyticsStore.SaveTablePreference(b.rootContext(), persisted)
}

func (b *Bindings) ListCanonicalKeywords(class string) ([]CanonicalKeywordDTO, error) {
	keywordClass, err := canonicalClass(class)
	if err != nil {
		return nil, err
	}
	if b.canonicalAnalytics == nil {
		return nil, errors.New("canonical analytics service is not configured")
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 30*time.Second)
	defer cancel()
	rows, err := b.canonicalAnalytics.List(ctx, keywordClass)
	if err != nil {
		return nil, err
	}
	result := make([]CanonicalKeywordDTO, len(rows))
	for index, row := range rows {
		result[index] = canonicalKeywordDTO(row)
	}
	return result, nil
}

func (b *Bindings) ClassifyCanonicalKeyword(id, class string) error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	keywordClass, err := canonicalClass(class)
	if err != nil {
		return err
	}
	if b.canonicalAnalytics == nil {
		return errors.New("canonical analytics service is not configured")
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 30*time.Second)
	defer cancel()
	if err := b.canonicalAnalytics.Classify(ctx, strings.TrimSpace(id), keywordClass); err != nil {
		return err
	}
	return b.syncCanonicalTriggers(ctx)
}

func (b *Bindings) SetCanonicalKeywordTrigger(id string, active bool) error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	if b.canonicalAnalytics == nil {
		return errors.New("canonical analytics service is not configured")
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 30*time.Second)
	defer cancel()
	if err := b.canonicalAnalytics.SetTriggerActive(ctx, strings.TrimSpace(id), active); err != nil {
		return err
	}
	return b.syncCanonicalTriggers(ctx)
}

func (b *Bindings) DeleteCanonicalKeyword(id string) error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	if language, word, serviceWord := parseServiceWordID(id); serviceWord {
		return b.DeleteAnalyticsServiceWord(string(language), word)
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 30*time.Second)
	defer cancel()
	return b.deleteCanonicalKeyword(ctx, id, false)
}

func (b *Bindings) DeleteLiveTrigger(canonicalID string) error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 30*time.Second)
	defer cancel()
	return b.deleteCanonicalKeyword(ctx, canonicalID, true)
}

func (b *Bindings) deleteCanonicalKeyword(ctx context.Context, id string, forceDrop bool) error {
	if b.canonicalAnalytics == nil {
		return errors.New("canonical analytics service is not configured")
	}
	rowsBefore, err := b.listCanonicalKeywords(ctx)
	if err != nil {
		return err
	}
	settings, err := b.settings.Load(ctx)
	if err != nil {
		return err
	}
	droppedRows := canonicalOwnedRows(rowsBefore, settings)
	if forceDrop {
		droppedRows = rowsBefore
	}
	dropped := canonicalValuesForID(droppedRows, strings.TrimSpace(id))
	if err := b.canonicalAnalytics.Delete(ctx, strings.TrimSpace(id)); err != nil {
		return err
	}
	return b.syncCanonicalTriggers(ctx, dropped...)
}

func (b *Bindings) ClearCanonicalKeywords(class string) error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	keywordClass, err := canonicalClass(class)
	if err != nil {
		return err
	}
	if b.canonicalAnalytics == nil {
		return errors.New("canonical analytics service is not configured")
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 30*time.Second)
	defer cancel()
	rowsBefore, err := b.canonicalAnalytics.List(ctx, keywordClass)
	if err != nil {
		return err
	}
	settings, err := b.settings.Load(ctx)
	if err != nil {
		return err
	}
	dropped := canonicalValues(canonicalOwnedRows(rowsBefore, settings))
	if err := b.canonicalAnalytics.Clear(ctx, keywordClass); err != nil {
		return err
	}
	return b.syncCanonicalTriggers(ctx, dropped...)
}

func (b *Bindings) RemoveCanonicalForm(id, form string) (CanonicalKeywordDTO, error) {
	if err := b.runtimeError(); err != nil {
		return CanonicalKeywordDTO{}, err
	}
	if b.canonicalAnalytics == nil {
		return CanonicalKeywordDTO{}, errors.New("canonical analytics service is not configured")
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 30*time.Second)
	defer cancel()
	row, err := b.canonicalAnalytics.RemoveForm(ctx, strings.TrimSpace(id), strings.TrimSpace(form))
	if err != nil {
		return CanonicalKeywordDTO{}, err
	}
	if err := b.syncCanonicalTriggers(ctx); err != nil {
		return CanonicalKeywordDTO{}, err
	}
	return canonicalKeywordDTO(row), nil
}

func (b *Bindings) MoveCanonicalForm(targetID, form string) error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	if b.canonicalAnalytics == nil {
		return errors.New("canonical analytics service is not configured")
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 30*time.Second)
	defer cancel()
	if err := b.canonicalAnalytics.MoveForm(ctx, strings.TrimSpace(targetID), strings.TrimSpace(form)); err != nil {
		return err
	}
	return b.syncCanonicalTriggers(ctx)
}

func (b *Bindings) AddCanonicalForm(targetID, form string) error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	if b.canonicalAnalytics == nil {
		return errors.New("canonical analytics service is not configured")
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 30*time.Second)
	defer cancel()
	if err := b.canonicalAnalytics.AddForm(ctx, strings.TrimSpace(targetID), strings.TrimSpace(form)); err != nil {
		return err
	}
	return b.syncCanonicalTriggers(ctx)
}

func (b *Bindings) BulkImportCanonicalKeywords(values []CanonicalImportEntryDTO, class string) (CanonicalBulkImportResultDTO, error) {
	if err := b.runtimeError(); err != nil {
		return CanonicalBulkImportResultDTO{}, err
	}
	keywordClass, err := bulkCanonicalClass(class)
	if err != nil {
		return CanonicalBulkImportResultDTO{}, err
	}
	if b.canonicalAnalytics == nil {
		return CanonicalBulkImportResultDTO{}, errors.New("canonical analytics service is not configured")
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 30*time.Second)
	defer cancel()
	imports := make([]coreanalytics.CanonicalImportValue, 0, len(values))
	for _, value := range values {
		imports = append(imports, coreanalytics.CanonicalImportValue{
			Value: value.Canonical,
			Forms: append([]string(nil), value.Forms...),
		})
	}
	result, err := b.canonicalAnalytics.BulkImport(ctx, imports, keywordClass)
	if err != nil {
		return CanonicalBulkImportResultDTO{}, err
	}
	return CanonicalBulkImportResultDTO{Added: result.Added, Updated: result.Updated, Skipped: result.Skipped}, nil
}

func (b *Bindings) SyncCanonicalTriggers() error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 30*time.Second)
	defer cancel()
	return b.syncCanonicalTriggers(ctx)
}

func (b *Bindings) syncCanonicalTriggers(ctx context.Context, dropped ...string) error {
	if b.canonicalAnalytics == nil {
		return errors.New("canonical analytics service is not configured")
	}
	rows, err := b.listCanonicalKeywords(ctx)
	if err != nil {
		return err
	}
	settings, err := b.settings.Load(ctx)
	if err != nil {
		return err
	}
	activeRows := make([]coreanalytics.CanonicalKeyword, 0, len(rows))
	activeIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Class == coreanalytics.ClassPositive && row.TriggerActive {
			activeRows = append(activeRows, row)
			activeIDs = appendUniqueString(activeIDs, row.ID)
		}
	}
	publicOwnedIDs := appendUniqueStrings(settings.CanonicalTriggerIDs, activeIDs...)
	publicOwned := canonicalValues(rowsWithCanonicalIDs(rows, publicOwnedIDs))
	publicOwned = append(publicOwned, settings.CanonicalTriggerValues...)
	publicOwned = append(publicOwned, dropped...)
	manual := make([]string, 0, len(settings.Keywords))
	for _, keyword := range settings.Keywords {
		if !containsNormalized(publicOwned, keyword) {
			manual = appendUniqueDisplayKeyword(manual, keyword)
		}
	}
	canons := canonicalValuesOnly(activeRows)
	forms := canonicalValues(activeRows)
	triggers := canonicalTriggerDescriptors(activeRows)

	directIDs := make([]string, 0, len(settings.DirectMessageCanonicalTriggerIDs))
	for _, row := range activeRows {
		if containsString(settings.DirectMessageCanonicalTriggerIDs, row.ID) ||
			(len(settings.DirectMessageCanonicalTriggerIDs) == 0 && containsNormalized(settings.DirectMessageKeywords, row.Canonical)) {
			directIDs = appendUniqueString(directIDs, row.ID)
		}
	}
	directOwned := canonicalValues(rowsWithCanonicalIDs(rows, settings.DirectMessageCanonicalTriggerIDs))
	directOwned = append(directOwned, settings.DirectMessageCanonicalValues...)
	directManual := make([]string, 0, len(settings.DirectMessageKeywords))
	for _, keyword := range settings.DirectMessageKeywords {
		if !containsNormalized(directOwned, keyword) && !containsNormalized(canonicalValues(rowsWithCanonicalIDs(activeRows, directIDs)), keyword) {
			directManual = appendUniqueDisplayKeyword(directManual, keyword)
		}
	}
	directCanons := canonicalValuesOnly(rowsWithCanonicalIDs(activeRows, directIDs))
	directForms := canonicalValues(rowsWithCanonicalIDs(activeRows, directIDs))

	settings.Keywords = append(append([]string(nil), manual...), canons...)
	settings.CanonicalTriggerIDs = activeIDs
	settings.CanonicalTriggerValues = canons
	settings.DirectMessageKeywords = append(append([]string(nil), directManual...), directCanons...)
	settings.DirectMessageCanonicalTriggerIDs = directIDs
	settings.DirectMessageCanonicalValues = directCanons
	if err := b.settings.Save(ctx, settings); err != nil {
		return err
	}
	b.runtime.Update(func(next *runtimeconfig.Snapshot) {
		next.Keywords = append(append([]string(nil), manual...), forms...)
		next.MinusKeywords = append([]string(nil), settings.MinusKeywords...)
		next.CanonicalTriggers = triggers
		next.SharedReply = settings.SharedReply
		next.PrivateReply = settings.PrivateReply
		next.PrivateReplyPresent = settings.PrivateReplyPresent
		next.DeliveryMode = settings.DeliveryMode.Normalized()
		next.DirectMessageKeywords = append(append([]string(nil), directManual...), directForms...)
	})
	return nil
}

func canonicalTriggerDescriptors(rows []coreanalytics.CanonicalKeyword) []runtimeconfig.CanonicalTrigger {
	triggers := make([]runtimeconfig.CanonicalTrigger, 0, len(rows))
	for _, row := range rows {
		canonical := strings.TrimSpace(row.Canonical)
		if row.ID == "" || canonical == "" {
			continue
		}
		forms := []string{normalizeKeyword(canonical)}
		for _, form := range row.Forms {
			if normalized := normalizeKeyword(form.Value); normalized != "" && !containsString(forms, normalized) {
				forms = append(forms, normalized)
			}
		}
		triggers = append(triggers, runtimeconfig.CanonicalTrigger{ID: row.ID, Canonical: canonical, Forms: forms})
	}
	return triggers
}

func (b *Bindings) reconcileCanonicalTriggerSelection(ctx context.Context, requested []string) error {
	rows, err := b.canonicalAnalytics.List(ctx, coreanalytics.ClassPositive)
	if err != nil {
		return err
	}
	selected := make(map[string]struct{}, len(requested))
	for _, keyword := range requested {
		if normalized := normalizeKeyword(keyword); normalized != "" {
			selected[normalized] = struct{}{}
		}
	}
	for _, row := range rows {
		_, active := selected[normalizeKeyword(row.Canonical)]
		if active == row.TriggerActive {
			continue
		}
		if err := b.canonicalAnalytics.SetTriggerActive(ctx, row.ID, active); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bindings) canonicalTriggerSelection(ctx context.Context, public, direct []string) ([]string, []string, []string, []string, error) {
	rows, err := b.canonicalAnalytics.List(ctx, coreanalytics.ClassPositive)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	publicIDs := make([]string, 0, len(rows))
	directIDs := make([]string, 0, len(rows))
	publicValues := make([]string, 0, len(rows))
	directValues := make([]string, 0, len(rows))
	for _, row := range rows {
		if !containsNormalized(public, row.Canonical) {
			continue
		}
		publicIDs = appendUniqueString(publicIDs, row.ID)
		publicValues = appendUniqueKeyword(publicValues, row.Canonical)
		if containsNormalized(direct, row.Canonical) {
			directIDs = appendUniqueString(directIDs, row.ID)
			directValues = appendUniqueKeyword(directValues, row.Canonical)
		}
	}
	return publicIDs, directIDs, publicValues, directValues, nil
}

func (b *Bindings) listCanonicalKeywords(ctx context.Context) ([]coreanalytics.CanonicalKeyword, error) {
	classes := []coreanalytics.KeywordClass{
		coreanalytics.ClassNeutral,
		coreanalytics.ClassPositive,
		coreanalytics.ClassNegative,
	}
	var result []coreanalytics.CanonicalKeyword
	for _, class := range classes {
		rows, err := b.canonicalAnalytics.List(ctx, class)
		if err != nil {
			return nil, err
		}
		result = append(result, rows...)
	}
	return result, nil
}

func canonicalValues(rows []coreanalytics.CanonicalKeyword) []string {
	values := make([]string, 0, len(rows)*2)
	for _, row := range rows {
		values = appendUniqueKeyword(values, row.Canonical)
		for _, form := range row.Forms {
			values = appendUniqueKeyword(values, form.Value)
		}
	}
	return values
}

func canonicalValuesOnly(rows []coreanalytics.CanonicalKeyword) []string {
	values := make([]string, 0, len(rows))
	for _, row := range rows {
		values = appendUniqueKeyword(values, row.Canonical)
	}
	return values
}

func rowsWithCanonicalIDs(rows []coreanalytics.CanonicalKeyword, ids []string) []coreanalytics.CanonicalKeyword {
	result := make([]coreanalytics.CanonicalKeyword, 0, len(ids))
	for _, row := range rows {
		if containsString(ids, row.ID) {
			result = append(result, row)
		}
	}
	return result
}

func canonicalOwnedRows(rows []coreanalytics.CanonicalKeyword, settings domain.KeywordSettings) []coreanalytics.CanonicalKeyword {
	result := make([]coreanalytics.CanonicalKeyword, 0, len(rows))
	for _, row := range rows {
		if row.TriggerActive || containsString(settings.CanonicalTriggerIDs, row.ID) ||
			containsString(settings.DirectMessageCanonicalTriggerIDs, row.ID) {
			result = append(result, row)
		}
	}
	return result
}

func containsNormalized(values []string, value string) bool {
	normalized := normalizeKeyword(value)
	for _, existing := range values {
		if normalizeKeyword(existing) == normalized {
			return true
		}
	}
	return false
}

func containsString(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func appendUniqueStrings(values []string, additions ...string) []string {
	result := append([]string(nil), values...)
	for _, value := range additions {
		result = appendUniqueString(result, value)
	}
	return result
}

func appendUniqueString(values []string, value string) []string {
	if value == "" || containsString(values, value) {
		return values
	}
	return append(values, value)
}

func canonicalValuesForID(rows []coreanalytics.CanonicalKeyword, id string) []string {
	for _, row := range rows {
		if row.ID == id {
			return canonicalValues([]coreanalytics.CanonicalKeyword{row})
		}
	}
	return nil
}

func normalizeKeyword(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func appendUniqueKeyword(values []string, value string) []string {
	value = normalizeKeyword(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueDisplayKeyword(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || containsNormalized(values, value) {
		return values
	}
	return append(values, value)
}

func canonicalClass(value string) (coreanalytics.KeywordClass, error) {
	class := coreanalytics.KeywordClass(strings.ToLower(strings.TrimSpace(value)))
	if class != coreanalytics.ClassNeutral && class != coreanalytics.ClassPositive && class != coreanalytics.ClassNegative {
		return "", errors.New("keyword class must be neutral, positive, or negative")
	}
	return class, nil
}

func bulkCanonicalClass(value string) (coreanalytics.KeywordClass, error) {
	class := coreanalytics.KeywordClass(strings.ToLower(strings.TrimSpace(value)))
	if class != coreanalytics.ClassPositive && class != coreanalytics.ClassNegative {
		return "", errors.New("bulk keyword class must be positive or negative")
	}
	return class, nil
}

func canonicalKeywordDTO(row coreanalytics.CanonicalKeyword) CanonicalKeywordDTO {
	forms := make([]CanonicalFormDTO, len(row.Forms))
	for index, form := range row.Forms {
		forms[index] = CanonicalFormDTO{Value: form.Value, Frequency: form.Frequency}
	}
	lastSeen := ""
	if !row.LastSeen.IsZero() {
		lastSeen = row.LastSeen.UTC().Format(time.RFC3339Nano)
	}
	return CanonicalKeywordDTO{
		ID: row.ID, Canonical: row.Canonical, Language: string(row.Language), Class: string(row.Class),
		Frequency: row.TotalFrequency, FrequencyDelta: row.FrequencyDelta, MessageCount: row.MessageCount, LastSeen: lastSeen,
		Forms: forms, TriggerActive: row.TriggerActive,
	}
}

func (b *Bindings) ImportAIFile(request AIImportRequestDTO) (AIImportDTO, error) {
	if b.importer == nil {
		return AIImportDTO{}, errors.New("AI import service is not configured")
	}
	ctx, done, err := b.beginOperation(30 * time.Minute)
	if err != nil {
		return AIImportDTO{}, err
	}
	defer done()
	imported, err := b.importer.Import(ctx, importsusecase.ImportRequest{
		Path: request.Path, SHA256: request.SHA256, RightsConfirmed: request.RightsConfirmed,
		UseAI: true, SourceKind: domain.SourceKindLocalImport, ProfileID: request.ProfileID,
	})
	return aiImportDTO(imported), err
}

func (b *Bindings) SelectAndImportAIFile(rightsConfirmed bool, profileID, locale string) (AIImportDTO, error) {
	if b.importer == nil || b.importFileSelector == nil {
		return AIImportDTO{}, errors.New("AI import and native file selector are required")
	}
	ctx, done, err := b.beginOperation(30 * time.Minute)
	if err != nil {
		return AIImportDTO{}, err
	}
	defer done()
	path, err := b.importFileSelector.SelectImportFile(ctx, normalizeDesktopLocale(locale))
	if err != nil {
		return AIImportDTO{}, err
	}
	if strings.TrimSpace(path) == "" {
		return AIImportDTO{}, errors.New("import file selection was cancelled")
	}
	imported, err := b.importer.Import(ctx, importsusecase.ImportRequest{
		Path: path, RightsConfirmed: rightsConfirmed, UseAI: true,
		SourceKind: domain.SourceKindLocalImport, ProfileID: profileID,
	})
	return aiImportDTO(imported), err
}

func (b *Bindings) GetAIImports() ([]AIImportDTO, error) {
	reader, ok := b.importer.(persistedAIImportService)
	if !ok {
		return nil, errors.New("AI import service does not support persisted reads")
	}
	imports, err := reader.List(b.rootContext())
	if err != nil {
		return nil, err
	}
	rows := make([]AIImportDTO, len(imports))
	for index, imported := range imports {
		rows[index] = aiImportDTO(imported)
	}
	return rows, nil
}

func (b *Bindings) GetAIImportCandidates(runID string) ([]AnalysisCandidateDTO, error) {
	reader, ok := b.importer.(persistedAIImportService)
	if !ok {
		return nil, errors.New("AI import service does not support persisted reads")
	}
	candidates, err := reader.ListCandidates(b.rootContext(), domain.ID(strings.TrimSpace(runID)))
	if err != nil {
		return nil, err
	}
	rows := make([]AnalysisCandidateDTO, len(candidates))
	for index, candidate := range candidates {
		rows[index] = AnalysisCandidateDTO{
			RunID: string(candidate.RunID), NormalizedValue: candidate.NormalizedValue,
			DisplayValue: candidate.DisplayValue, Kind: candidate.Kind, Frequency: candidate.Frequency,
			SourceDiversity: candidate.SourceDiversity, Score: candidate.Score, Source: candidate.Source,
			ModerationState: candidate.ModerationState,
		}
	}
	return rows, nil
}

func (b *Bindings) ExportAIImportCandidates(runID, locale string) (string, error) {
	reader, ok := b.importer.(persistedAIImportService)
	if !ok || b.importExportSelector == nil {
		return "", errors.New("AI import export and native file selector are required")
	}
	ctx, done, err := b.beginOperation(10 * time.Minute)
	if err != nil {
		return "", err
	}
	defer done()
	path, err := b.importExportSelector.SelectExportFile(ctx, "import-candidates.txt", normalizeDesktopLocale(locale))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", errors.New("import candidate export was cancelled")
	}
	return reader.ExportCandidates(ctx, domain.ID(strings.TrimSpace(runID)), path)
}

func normalizeDesktopLocale(locale string) string {
	if strings.EqualFold(strings.TrimSpace(locale), "en") {
		return "en"
	}
	return "ru"
}

func (b *Bindings) OpenFile(path string) error {
	if err := b.runtimeError(); err != nil {
		return err
	}
	if b.fileOpener == nil {
		return errors.New("file opener is not configured")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("file path is required")
	}
	return b.fileOpener.Open(path)
}

func analysisResultsDTO(result analyticsusecase.AnalysisResult, states map[analyticsusecase.CandidateKey]string) AnalysisResultsDTO {
	return AnalysisResultsDTO{
		RunID: result.RunID, Status: result.Status,
		GeneralPhrases:    analysisCandidateDTOs(result.RunID, "general_phrase", result.GeneralPhrases, states),
		GeneralWords:      analysisCandidateDTOs(result.RunID, "general_word", result.GeneralWords, states),
		ProfileCandidates: analysisCandidateDTOs(result.RunID, "profile", result.ProfileCandidates, states),
	}
}

func analysisCandidateDTOs(runID, source string, values []coreanalytics.Candidate, states map[analyticsusecase.CandidateKey]string) []AnalysisCandidateDTO {
	rows := make([]AnalysisCandidateDTO, len(values))
	for index, candidate := range values {
		state := states[analyticsusecase.CandidateKey{NormalizedValue: candidate.NormalizedValue, Kind: candidate.Kind}]
		if state == "" {
			state = "new"
		}
		rows[index] = AnalysisCandidateDTO{
			RunID: runID, NormalizedValue: candidate.NormalizedValue, DisplayValue: candidate.DisplayValue,
			Kind: candidate.Kind, Frequency: candidate.Frequency, SourceDiversity: candidate.SourceDiversity,
			Score: candidate.Score, Source: source, ModerationState: state,
		}
	}
	return rows
}

func cloneAnalysisResults(result AnalysisResultsDTO) AnalysisResultsDTO {
	result.GeneralPhrases = append([]AnalysisCandidateDTO(nil), result.GeneralPhrases...)
	result.GeneralWords = append([]AnalysisCandidateDTO(nil), result.GeneralWords...)
	result.ProfileCandidates = append([]AnalysisCandidateDTO(nil), result.ProfileCandidates...)
	return result
}

func aiImportDTO(imported domain.Import) AIImportDTO {
	dto := AIImportDTO{
		ID: imported.ID, FileName: imported.FileName, FilePath: "", Status: imported.Status,
		RecordCount: imported.RecordCount, Error: imported.LastError,
	}
	if !imported.ImportedAt.IsZero() {
		dto.ImportedAt = imported.ImportedAt.UTC().Format(time.RFC3339Nano)
	}
	return dto
}

func (b *Bindings) CreateBackup(kind string) (BackupDTO, error) {
	if b.backups == nil {
		return BackupDTO{}, errors.New("backup service is not configured")
	}
	backupKind := domain.BackupKind(strings.TrimSpace(kind))
	if backupKind != domain.BackupDaily && backupKind != domain.BackupMonthly && backupKind != domain.BackupPreMigration {
		return BackupDTO{}, errors.New("unsupported backup kind")
	}
	ctx, done, err := b.beginOperation(30 * time.Minute)
	if err != nil {
		return BackupDTO{}, err
	}
	defer done()
	record, err := b.backups.Create(ctx, backupKind)
	dto := backupDTO(record)
	b.backupMu.Lock()
	b.latestBackup = dto
	b.backupMu.Unlock()
	return dto, err
}

func (b *Bindings) GetBackupStatus() BackupDTO {
	if b.backups != nil {
		latest := b.backups.Latest()
		if latest.ID != "" || latest.Status != "" {
			return backupDTO(latest)
		}
	}
	b.backupMu.RLock()
	defer b.backupMu.RUnlock()
	return b.latestBackup
}

func (b *Bindings) RestoreBackup(archive, destination string) error {
	if b.backups == nil {
		return errors.New("backup service is not configured")
	}
	ctx, done, err := b.beginOperation(30 * time.Minute)
	if err != nil {
		return err
	}
	defer done()
	return b.backups.Restore(ctx, archive, destination)
}

func (b *Bindings) ExportBackupRecoveryKey(selectedPath string) error {
	if b.backups == nil {
		return errors.New("backup service is not configured")
	}
	ctx, done, err := b.beginOperation(30 * time.Second)
	if err != nil {
		return err
	}
	defer done()
	return b.backups.ExportRecoveryKey(ctx, selectedPath)
}

func backupDTO(record domain.BackupRecord) BackupDTO {
	dto := BackupDTO{
		ID: string(record.ID), ArchivePath: record.ArchivePath, Kind: string(record.Kind),
		SizeBytes: record.SizeBytes, SHA256: record.SHA256, Status: record.Status,
		Error: domain.SafeBackupError(record.Error),
	}
	if !record.CreatedAt.IsZero() {
		dto.CreatedAt = record.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if record.VerifiedAt != nil {
		dto.VerifiedAt = record.VerifiedAt.UTC().Format(time.RFC3339Nano)
	}
	return dto
}

func (b *Bindings) GetStorageMetrics() StorageMetricsDTO {
	if b.storageMetrics == nil {
		return StorageMetricsDTO{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	metrics, err := b.storageMetrics.Get(ctx)
	if err != nil {
		return StorageMetricsDTO{}
	}
	return StorageMetricsDTO{
		RowCount: metrics.RowCount, DatabaseBytes: metrics.DatabaseBytes,
		OldestTimestamp:  metricTime(metrics.OldestTimestamp),
		NewestTimestamp:  metricTime(metrics.NewestTimestamp),
		PausedForLowDisk: metrics.PausedForLowDisk,
		LastRefresh:      metrics.LastRefresh.UTC().Format(time.RFC3339Nano),
	}
}

func (b *Bindings) GetKeywordSettings() (KeywordSettingsDTO, error) {
	if err := b.runtimeError(); err != nil {
		return KeywordSettingsDTO{}, err
	}
	settings, err := b.settings.Load(context.Background())
	if err != nil {
		return KeywordSettingsDTO{}, err
	}
	revision := b.runtime.Current().Revision
	return keywordSettingsDTO(settings, revision), nil
}

func keywordSettingsDTO(settings domain.KeywordSettings, revision uint64) KeywordSettingsDTO {
	directMessageKeywords := settings.DirectMessageKeywords
	if directMessageKeywords == nil {
		directMessageKeywords = settings.Keywords
	}
	return KeywordSettingsDTO{
		Keywords:              append([]string{}, settings.Keywords...),
		MinusKeywords:         append([]string{}, settings.MinusKeywords...),
		SharedReply:           settings.SharedReply,
		PrivateReply:          settings.PrivateReply,
		DeliveryMode:          string(settings.DeliveryMode.Normalized()),
		DirectMessageKeywords: append([]string{}, directMessageKeywords...),
		Revision:              revision,
	}
}

func (b *Bindings) SaveKeywordSettings(settings KeywordSettingsDTO) (KeywordSettingsDTO, error) {
	if err := b.runtimeError(); err != nil {
		return KeywordSettingsDTO{}, err
	}
	nextSettings := domain.KeywordSettings{
		Keywords:              settings.Keywords,
		MinusKeywords:         settings.MinusKeywords,
		SharedReply:           settings.SharedReply,
		PrivateReply:          settings.PrivateReply,
		DeliveryMode:          domain.KeywordDeliveryMode(settings.DeliveryMode).Normalized(),
		PrivateReplyPresent:   true,
		DirectMessageKeywords: settings.DirectMessageKeywords,
	}
	if b.canonicalAnalytics != nil {
		publicIDs, directIDs, publicValues, directValues, err := b.canonicalTriggerSelection(context.Background(), settings.Keywords, settings.DirectMessageKeywords)
		if err != nil {
			return KeywordSettingsDTO{}, err
		}
		nextSettings.CanonicalTriggerIDs = publicIDs
		nextSettings.CanonicalTriggerValues = publicValues
		nextSettings.DirectMessageCanonicalTriggerIDs = directIDs
		nextSettings.DirectMessageCanonicalValues = directValues
	}
	if err := b.settings.Save(context.Background(), nextSettings); err != nil {
		return KeywordSettingsDTO{}, err
	}
	if b.canonicalAnalytics != nil {
		ctx := context.Background()
		if err := b.reconcileCanonicalTriggerSelection(ctx, nextSettings.Keywords); err != nil {
			return KeywordSettingsDTO{}, err
		}
		if err := b.syncCanonicalTriggers(ctx); err != nil {
			return KeywordSettingsDTO{}, err
		}
		persisted, err := b.settings.Load(context.Background())
		if err != nil {
			return KeywordSettingsDTO{}, err
		}
		return keywordSettingsDTO(persisted, b.runtime.Current().Revision), nil
	}
	revision := b.runtime.Update(func(next *runtimeconfig.Snapshot) {
		next.Keywords = append([]string(nil), nextSettings.Keywords...)
		next.MinusKeywords = append([]string(nil), nextSettings.MinusKeywords...)
		next.SharedReply = nextSettings.SharedReply
		next.PrivateReply = nextSettings.PrivateReply
		next.PrivateReplyPresent = nextSettings.PrivateReplyPresent
		next.DeliveryMode = nextSettings.DeliveryMode
		next.DirectMessageKeywords = append([]string(nil), nextSettings.DirectMessageKeywords...)
	})
	return keywordSettingsDTO(nextSettings, revision), nil
}

func (b *Bindings) GetChannels() ([]ManagedChannelDTO, error) {
	if err := b.runtimeError(); err != nil {
		return nil, err
	}
	channels, err := b.settings.LoadChannels(context.Background())
	if err != nil {
		return nil, err
	}
	return channelDTOs(channels, b.runtime.Current().Revision), nil
}

func (b *Bindings) AddChannelLinks(input string) ([]ManagedChannelDTO, error) {
	if err := b.runtimeError(); err != nil {
		return nil, err
	}
	channels, err := b.settings.LoadChannels(context.Background())
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		seen[strings.ToLower(strings.TrimSpace(channel.Link))] = struct{}{}
	}
	for _, link := range parseChannelLinks(input) {
		normalized := strings.ToLower(link)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		status := domain.ChannelStatusReady
		if strings.Contains(strings.ToLower(link), "/+") {
			status = domain.ChannelStatusJoining
		}
		channels = append(channels, domain.ManagedChannel{
			ID:           channelID(link),
			Title:        channelTitle(link),
			Link:         link,
			Status:       status,
			Members:      "0/3",
			Sent:         0,
			LastActivity: "—",
			Active:       true,
		})
	}
	if err := b.settings.SaveChannels(context.Background(), channels); err != nil {
		return nil, err
	}
	revision := b.publishChannels(channels)
	return channelDTOs(channels, revision), nil
}

func (b *Bindings) ToggleChannelActive(id string, active bool) ([]ManagedChannelDTO, error) {
	if err := b.runtimeError(); err != nil {
		return nil, err
	}
	channels, err := b.settings.LoadChannels(context.Background())
	if err != nil {
		return nil, err
	}
	for i := range channels {
		if channels[i].ID != id {
			continue
		}
		channels[i].Active = active
		if active && channels[i].Status == domain.ChannelStatusPaused {
			channels[i].Status = domain.ChannelStatusReady
		}
		if !active {
			channels[i].Status = domain.ChannelStatusPaused
		}
	}
	if err := b.settings.SaveChannels(context.Background(), channels); err != nil {
		return nil, err
	}
	revision := b.publishChannels(channels)
	return channelDTOs(channels, revision), nil
}

func (b *Bindings) GetProxyStatus() ProxyStatusDTO {
	if b.proxyStatus == nil {
		return sanitizeManagedProxyStatus(ProxyStatusDTO{Mode: "tor_snowflake", State: "stopped", Address: managedProxyLoopbackAddress, Transport: "snowflake", AutoRestart: true})
	}
	return sanitizeManagedProxyStatus(b.proxyStatus.Status())
}

func (b *Bindings) GetAccounts() ([]AccountDTO, error) {
	if err := b.runtimeError(); err != nil {
		return nil, err
	}
	accounts, err := b.accounts.List(context.Background())
	if err != nil {
		return nil, err
	}
	return b.proxyAccountDTOs(context.Background(), accounts), nil
}

func (b *Bindings) SetAccountRole(id string, role string) (AccountDTO, error) {
	if err := b.runtimeError(); err != nil {
		return AccountDTO{}, err
	}
	account, err := b.accounts.SetRole(context.Background(), domain.ID(id), domain.AccountRole(role))
	if err != nil {
		return AccountDTO{}, err
	}
	revision := b.runtime.Update(func(next *runtimeconfig.Snapshot) {
		if next.Roles == nil {
			next.Roles = make(map[domain.ID]domain.AccountRole)
		}
		next.Roles[account.ID] = account.Role
	})
	return accountDTO(account, revision, b.proxyStatus != nil, b.GetProxyStatus().State), nil
}

func (b *Bindings) ResumeLegacyPausedAccount(id string) (AccountDTO, error) {
	if err := b.runtimeError(); err != nil {
		return AccountDTO{}, err
	}
	account, err := b.accounts.ResumeLegacyPaused(context.Background(), domain.ID(id))
	if err != nil {
		return AccountDTO{}, err
	}
	revision := b.runtime.Update(nil)
	return accountDTO(account, revision, b.proxyStatus != nil, b.GetProxyStatus().State), nil
}

func (b *Bindings) GetCatalog(catalog string) ([]CatalogEntryDTO, error) {
	if err := b.runtimeError(); err != nil {
		return nil, err
	}
	ctx := context.Background()
	source := domain.SourceCatalog(catalog)
	rows, err := b.catalogs.List(ctx, source)
	if err != nil {
		return nil, err
	}
	return b.catalogDTOs(ctx, source, rows, b.runtime.Current().Revision)
}

func (b *Bindings) AddCatalogLinks(input string, catalog string, topic string) ([]CatalogEntryDTO, error) {
	if err := b.runtimeError(); err != nil {
		return nil, err
	}
	ctx := context.Background()
	source := domain.SourceCatalog(catalog)
	rows, err := b.catalogs.AddLinks(ctx, source, parseChannelLinks(input), topic)
	if err != nil {
		return nil, err
	}
	revision := b.publishCatalog(source, rows)
	return b.catalogDTOs(ctx, source, rows, revision)
}

func (b *Bindings) SetChannelTopic(catalog string, channelID string, topic string) ([]CatalogEntryDTO, error) {
	return b.SetChannelTopics(catalog, []string{channelID}, topic)
}

func (b *Bindings) SetChannelTopics(catalog string, channelIDs []string, topic string) ([]CatalogEntryDTO, error) {
	if err := b.runtimeError(); err != nil {
		return nil, err
	}
	ctx := context.Background()
	source := domain.SourceCatalog(catalog)
	ids := make([]domain.ID, len(channelIDs))
	for index := range channelIDs {
		ids[index] = domain.ID(channelIDs[index])
	}
	rows, err := b.catalogs.SetTopics(ctx, source, ids, topic)
	if err != nil {
		return nil, err
	}
	revision := b.publishCatalog(source, rows)
	return b.catalogDTOs(ctx, source, rows, revision)
}

func (b *Bindings) ToggleCatalogEntry(catalog string, channelID string, active bool) ([]CatalogEntryDTO, error) {
	if err := b.runtimeError(); err != nil {
		return nil, err
	}
	ctx := context.Background()
	source := domain.SourceCatalog(catalog)
	rows, err := b.catalogs.Toggle(ctx, source, domain.ID(channelID), active)
	if err != nil {
		return nil, err
	}
	revision := b.publishCatalog(source, rows)
	return b.catalogDTOs(ctx, source, rows, revision)
}

func (b *Bindings) JoinCatalogEntry(catalog string, channelID string) ([]CatalogEntryDTO, error) {
	if err := b.runtimeError(); err != nil {
		return nil, err
	}
	ctx := context.Background()
	source := domain.SourceCatalog(catalog)
	rows, err := b.catalogs.Join(ctx, source, domain.ID(channelID))
	if err != nil {
		return nil, err
	}
	revision := b.publishCatalog(source, rows)
	return b.catalogDTOs(ctx, source, rows, revision)
}

func (b *Bindings) LeaveCatalogEntry(catalog string, channelID string) ([]CatalogEntryDTO, error) {
	if err := b.runtimeError(); err != nil {
		return nil, err
	}
	ctx := context.Background()
	source := domain.SourceCatalog(catalog)
	rows, err := b.catalogs.Leave(ctx, source, domain.ID(channelID))
	if err != nil {
		return nil, err
	}
	revision := b.publishCatalog(source, rows)
	return b.catalogDTOs(ctx, source, rows, revision)
}

func (b *Bindings) DeleteCatalogEntries(catalog string, channelIDs []string) ([]CatalogEntryDTO, error) {
	if err := b.runtimeError(); err != nil {
		return nil, err
	}
	ctx := context.Background()
	source := domain.SourceCatalog(catalog)
	ids := make([]domain.ID, len(channelIDs))
	for index := range channelIDs {
		ids[index] = domain.ID(channelIDs[index])
	}
	rows, err := b.catalogs.Delete(ctx, source, ids)
	if err != nil {
		return nil, err
	}
	revision := b.publishCatalog(source, rows)
	return b.catalogDTOs(ctx, source, rows, revision)
}

func (b *Bindings) GetAppSettings() (AppSettingsDTO, error) {
	if err := b.runtimeError(); err != nil {
		return AppSettingsDTO{}, err
	}
	settings, err := b.settings.LoadAppSettings(context.Background())
	if err != nil {
		return AppSettingsDTO{}, err
	}
	return appSettingsDTO(settings, b.runtime.Current().Revision), nil
}

func (b *Bindings) SaveAppSettings(settings AppSettingsDTO) (AppSettingsDTO, error) {
	if err := b.runtimeError(); err != nil {
		return AppSettingsDTO{}, err
	}
	nextSettings := domain.AppSettings{
		RepliesPerMinute:       settings.RepliesPerMinute,
		MinIntervalSeconds:     settings.MinIntervalSeconds,
		JoinIntervalMinMinutes: settings.JoinIntervalMinMinutes,
		JoinIntervalMaxMinutes: settings.JoinIntervalMaxMinutes,
		JoinIntervalEnabled:    settings.JoinIntervalEnabled,
		GroupRestHours:         settings.GroupRestHours,
		GroupRestEnabled:       settings.GroupRestEnabled,
		DirectMessages:         settings.DirectMessages,
		Proxy:                  settings.Proxy,
	}.Normalized()
	if _, err := nextSettings.JoinIntervalRange(); err != nil {
		return AppSettingsDTO{}, err
	}
	if err := domain.ValidateGroupRestHours(nextSettings.GroupRestHours); err != nil {
		return AppSettingsDTO{}, err
	}
	if err := b.settings.SaveAppSettings(context.Background(), nextSettings); err != nil {
		return AppSettingsDTO{}, err
	}
	// Close the runtime gates before cleanup so concurrent catalog activation
	// cannot recreate state that the cleanup has just removed.
	b.catalogs.SetJoinIntervalEnabled(nextSettings.JoinIntervalEnabled)
	b.catalogs.SetGroupRestEnabled(nextSettings.GroupRestEnabled)
	if !nextSettings.JoinIntervalEnabled {
		if clearer, ok := b.settings.(joinIntervalClearer); ok {
			if err := clearer.ClearJoinIntervals(context.Background()); err != nil {
				return AppSettingsDTO{}, err
			}
		}
	}
	if !nextSettings.GroupRestEnabled {
		if cleaner, ok := b.settings.(groupRestCleaner); ok {
			if err := cleaner.ClearGroupRests(context.Background()); err != nil {
				return AppSettingsDTO{}, err
			}
		} else if releaser, ok := b.settings.(groupRestDelayReleaser); ok {
			if err := releaser.ReleaseGroupRestDelays(context.Background()); err != nil {
				return AppSettingsDTO{}, err
			}
		}
	}
	committed, err := b.settings.LoadAppSettings(context.Background())
	if err != nil {
		return AppSettingsDTO{}, err
	}
	interval, err := committed.JoinIntervalRange()
	if err != nil {
		return AppSettingsDTO{}, err
	}
	if err := b.catalogs.SetJoinIntervalRange(interval.MinMinutes, interval.MaxMinutes); err != nil {
		return AppSettingsDTO{}, err
	}
	b.catalogs.SetJoinIntervalEnabled(committed.JoinIntervalEnabled)
	if err := b.catalogs.SetGroupRestHours(committed.Normalized().GroupRestHours); err != nil {
		return AppSettingsDTO{}, err
	}
	b.catalogs.SetGroupRestEnabled(committed.GroupRestEnabled)
	revision := b.runtime.Update(func(next *runtimeconfig.Snapshot) {
		next.RateLimits = runtimeconfig.RateLimits{RepliesPerMinute: committed.RepliesPerMinute, MinIntervalSeconds: committed.MinIntervalSeconds}
		next.DirectMessages = committed.DirectMessages
	})
	return appSettingsDTO(committed, revision), nil
}

func parseChannelLinks(input string) []string {
	parts := channelInputSplitter.Split(input, -1)
	links := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		link := normalizeChannelLink(part)
		if link == "" {
			continue
		}
		normalized := strings.ToLower(link)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		links = append(links, link)
	}
	return links
}

func normalizeChannelLink(input string) string {
	value := strings.TrimSpace(input)
	value = strings.Trim(value, "\"'")
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "@") {
		value = "https://t.me/" + strings.TrimPrefix(value, "@")
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimPrefix(parsed.Host, "www."))
	if host != "t.me" && host != "telegram.me" {
		return ""
	}
	if parsed.Path == "" || parsed.Path == "/" {
		return ""
	}
	parsed.Scheme = "https"
	parsed.Host = "t.me"
	parsed.RawQuery = ""
	if match := discussionFragmentPattern.FindStringSubmatch(parsed.Fragment); len(match) == 2 {
		parsed.Fragment = "tc-discussion=" + match[1]
	} else {
		parsed.Fragment = ""
	}
	return parsed.String()
}

func channelID(link string) string {
	sum := sha1.Sum([]byte(strings.ToLower(link)))
	return hex.EncodeToString(sum[:])[:12]
}

func channelTitle(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		return link
	}
	slug := strings.Trim(parsed.Path, "/")
	if slug == "" {
		return link
	}
	if strings.HasPrefix(slug, "+") {
		if len(slug) > 9 {
			slug = slug[:9]
		}
		return "Приватная ссылка " + slug
	}
	return "@" + slug
}

func channelDTOs(channels []domain.ManagedChannel, revision uint64) []ManagedChannelDTO {
	out := make([]ManagedChannelDTO, 0, len(channels))
	for _, channel := range channels {
		out = append(out, ManagedChannelDTO{
			ID:           channel.ID,
			Title:        channel.Title,
			Link:         channel.Link,
			Status:       string(channel.Status),
			Members:      channel.Members,
			Sent:         channel.Sent,
			LastActivity: channel.LastActivity,
			Active:       channel.Active,
			Revision:     revision,
		})
	}
	return out
}

func appSettingsDTO(settings domain.AppSettings, revision uint64) AppSettingsDTO {
	settings = settings.Normalized()
	return AppSettingsDTO{
		RepliesPerMinute:       settings.RepliesPerMinute,
		MinIntervalSeconds:     settings.MinIntervalSeconds,
		JoinIntervalMinMinutes: settings.JoinIntervalMinMinutes,
		JoinIntervalMaxMinutes: settings.JoinIntervalMaxMinutes,
		JoinIntervalEnabled:    settings.JoinIntervalEnabled,
		GroupRestHours:         settings.GroupRestHours,
		GroupRestEnabled:       settings.GroupRestEnabled,
		DirectMessages:         settings.DirectMessages,
		Proxy:                  settings.Proxy,
		Revision:               revision,
	}
}

func loadRuntimeSnapshot(settings SettingsStore) (runtimeconfig.Snapshot, error) {
	snapshot := runtimeconfig.Snapshot{}
	if settings == nil {
		return snapshot, nil
	}
	ctx := context.Background()
	keywordSettings, err := settings.Load(ctx)
	if err != nil {
		return runtimeconfig.Snapshot{}, fmt.Errorf("load keyword settings: %w", err)
	}
	snapshot.Keywords = append([]string(nil), keywordSettings.Keywords...)
	snapshot.MinusKeywords = append([]string(nil), keywordSettings.MinusKeywords...)
	snapshot.SharedReply = keywordSettings.SharedReply
	snapshot.PrivateReply = keywordSettings.PrivateReply
	snapshot.PrivateReplyPresent = keywordSettings.PrivateReplyPresent
	snapshot.DeliveryMode = keywordSettings.DeliveryMode.Normalized()
	snapshot.DirectMessageKeywords = append([]string(nil), keywordSettings.DirectMessageKeywords...)

	appSettings, err := settings.LoadAppSettings(ctx)
	if err != nil {
		return runtimeconfig.Snapshot{}, fmt.Errorf("load app settings: %w", err)
	}
	snapshot.RateLimits = runtimeconfig.RateLimits{RepliesPerMinute: appSettings.RepliesPerMinute, MinIntervalSeconds: appSettings.MinIntervalSeconds}
	snapshot.DirectMessages = appSettings.DirectMessages
	channels, err := settings.LoadChannels(ctx)
	if err != nil {
		return runtimeconfig.Snapshot{}, fmt.Errorf("load channels: %w", err)
	}
	snapshot.CatalogAssignments = managedChannelAssignments(channels)

	accounts, err := settings.ListAccounts(ctx)
	if err != nil {
		return runtimeconfig.Snapshot{}, fmt.Errorf("load accounts: %w", err)
	}
	snapshot.Roles = make(map[domain.ID]domain.AccountRole, len(accounts))
	snapshot.ProxyAssignments = make(map[domain.ID]string, len(accounts))
	for _, account := range accounts {
		snapshot.Roles[account.ID] = account.Role
		snapshot.ProxyAssignments[account.ID] = accountProxyRouteID(account)
	}
	for _, catalog := range []domain.SourceCatalog{domain.SourceCatalogOutbound, domain.SourceCatalogScout} {
		rows, err := settings.ListCatalog(ctx, catalog)
		if err != nil {
			return runtimeconfig.Snapshot{}, fmt.Errorf("load %s catalog: %w", catalog, err)
		}
		snapshot.CatalogAssignments[catalog] = catalogAssignments(rows)
	}
	return snapshot, nil
}

func accountProxyRouteID(account domain.Account) string {
	switch account.ProxyMode {
	case domain.ProxyModeAssigned:
		if account.ProxyProfileID != nil {
			return string(*account.ProxyProfileID)
		}
		return string(domain.ProxyModeUnassigned)
	case domain.ProxyModeUnassigned:
		return string(domain.ProxyModeUnassigned)
	case domain.ProxyModeDirect:
		return string(domain.ProxyModeUnassigned)
	default:
		return string(domain.SystemProxyRouteID)
	}
}

func (b *Bindings) runtimeError() error {
	if b == nil {
		return errDesktopOperationsClosed
	}
	b.operationMu.Lock()
	closing := b.operationsClosing
	b.operationMu.Unlock()
	if closing {
		return errDesktopOperationsClosed
	}
	return b.initializationErr
}

func (b *Bindings) publishChannels(channels []domain.ManagedChannel) uint64 {
	return b.runtime.Update(func(next *runtimeconfig.Snapshot) {
		next.CatalogAssignments = managedChannelAssignments(channels)
	})
}

func (b *Bindings) publishCatalog(catalog domain.SourceCatalog, rows []domain.Channel) uint64 {
	return b.runtime.Update(func(next *runtimeconfig.Snapshot) {
		if next.CatalogAssignments == nil {
			next.CatalogAssignments = make(map[domain.SourceCatalog][]domain.ID)
		}
		next.CatalogAssignments[catalog] = catalogAssignments(rows)
	})
}

func catalogAssignments(rows []domain.Channel) []domain.ID {
	assignments := make([]domain.ID, 0, len(rows))
	for index := range rows {
		if rows[index].RemovalRequestedAt != nil {
			continue
		}
		assignments = append(assignments, rows[index].ID)
	}
	return assignments
}

func accountDTO(account domain.Account, revision uint64, managedProxy bool, proxyState string) AccountDTO {
	proxy := string(account.ProxyMode)
	proxyWarning := ""
	if account.ProxyMode == domain.ProxyModeUnassigned || account.ProxyMode == domain.ProxyModeDirect {
		proxy = "unassigned"
		proxyWarning = "route_capacity"
	} else if managedProxy && (account.ProxyMode == domain.ProxyModeGlobal || account.ProxyMode == "") {
		proxy = "Tor/Snowflake"
	}
	status := string(account.Status)
	if account.ProxyMode != domain.ProxyModeUnassigned && managedProxy && account.Status == domain.AccountStopped && (proxyState == "starting" || proxyState == "degraded") {
		status = "connecting"
	}
	return AccountDTO{
		ID: string(account.ID), DisplayName: account.DisplayName, Username: account.Username,
		PhoneMasked: account.PhoneMasked, Role: string(account.Role), Status: status,
		ErrorCode: safeAccountErrorCode(account),
		Proxy:     proxy, ProxyWarning: proxyWarning, PublicRepliesSent: account.PublicRepliesSent,
		PrivateMessagesSent: account.PrivateMessagesSent, NextDelivery: string(account.EffectiveNextDelivery()),
		PrivateMessagesClosed: account.PrivateMessagesClosed, LastActivity: displayTime(account.LastActivityAt),
		LegacyPauseReviewRequired: account.LegacyPauseReviewRequired, Revision: revision,
	}
}

func safeAccountErrorCode(account domain.Account) string {
	if account.Status == domain.AccountError && account.LastError == "rpc_auth_key_duplicated" {
		return account.LastError
	}
	return ""
}

func channelModerationDTOs(rows []usecase.ChannelModerationChannel) []ChannelModerationDTO {
	result := make([]ChannelModerationDTO, len(rows))
	for index, row := range rows {
		accounts := make([]ChannelModerationAccountDTO, len(row.Accounts))
		for accountIndex, account := range row.Accounts {
			requestSubmittedAt := ""
			if !account.RequestSubmittedAt.IsZero() {
				requestSubmittedAt = account.RequestSubmittedAt.UTC().Format(time.RFC3339)
			}
			joinedAt := ""
			if account.JoinedAt != nil {
				joinedAt = account.JoinedAt.UTC().Format(time.RFC3339)
			}
			accounts[accountIndex] = ChannelModerationAccountDTO{
				Title:              account.Title,
				Role:               string(account.Role),
				RequestSubmittedAt: requestSubmittedAt,
				JoinedAt:           joinedAt,
				DurationSeconds:    int64(account.Duration / time.Second),
				Status:             account.Status,
			}
		}
		firstRequestAt := ""
		if !row.FirstRequestAt.IsZero() {
			firstRequestAt = row.FirstRequestAt.UTC().Format(time.RFC3339)
		}
		result[index] = ChannelModerationDTO{
			Catalog:         string(row.Catalog),
			ChannelID:       string(row.ChannelID),
			Title:           row.Title,
			Link:            row.Link,
			Topic:           row.Topic,
			Applications:    row.Applications,
			Joined:          row.Joined,
			Pending:         row.Pending,
			Status:          row.Status,
			FirstRequestAt:  firstRequestAt,
			DurationSeconds: int64(row.Duration / time.Second),
			Accounts:        accounts,
		}
	}
	return result
}

func (b *Bindings) catalogDTOs(ctx context.Context, catalog domain.SourceCatalog, channels []domain.Channel, revision uint64) ([]CatalogEntryDTO, error) {
	rows := make([]CatalogEntryDTO, len(channels))
	lister, listsMemberships := b.settings.(catalogMembershipLister)
	for index, channel := range channels {
		title := strings.TrimSpace(channel.Title)
		if title == "" || title == channel.Link {
			title = channelTitle(channel.Link)
		}
		rows[index] = CatalogEntryDTO{
			ID: string(channel.ID), Title: title, Link: channel.Link, Topic: channel.Topic,
			Status: string(channel.Status), MessageCount: channel.MessageCount, SentCount: channel.SentCount,
			LastActivity: displayTime(channel.LastActivityAt), Active: channel.Active, Revision: revision,
		}
		if !channel.Active {
			rows[index].Status = string(domain.ChannelPaused)
		}
		if channel.RemovalRequestedAt != nil {
			rows[index].Status = "removing"
		}
		if !listsMemberships {
			continue
		}
		memberships, err := lister.ListMemberships(ctx, catalog, channel.ID)
		if err != nil {
			return nil, err
		}
		aggregateCatalogMemberships(&rows[index], memberships)
		if channel.RemovalRequestedAt != nil {
			rows[index].Status = "removing"
		}
	}
	return rows, nil
}

func aggregateCatalogMemberships(row *CatalogEntryDTO, memberships []domain.ChannelMembership) {
	now := time.Now().UTC()
	var plannedAt *time.Time
	for _, membership := range memberships {
		switch {
		case membership.IsMember || membership.Status == "member":
			row.Member++
		case membership.Status == "pending_approval":
			row.PendingApproval++
		case membership.Status == "joining":
			row.Joining++
			if row.Active && membership.JoinNotBefore != nil && membership.JoinNotBefore.After(now) && (plannedAt == nil || membership.JoinNotBefore.Before(*plannedAt)) {
				value := membership.JoinNotBefore.UTC()
				plannedAt = &value
			}
		case membership.Status == "leaving":
			row.Leaving++
		default:
			row.Failed++
		}
	}
	if !row.Active {
		row.Status = string(domain.ChannelPaused)
		return
	}
	if plannedAt != nil {
		row.Planned = true
		row.JoinNotBefore = metricTime(plannedAt)
	}
	total := row.Member + row.PendingApproval + row.Joining + row.Leaving + row.Failed
	if total == 0 {
		return
	}
	switch {
	case row.Member == total:
		row.Status = string(domain.ChannelReady)
	case row.PendingApproval == total:
		row.Status = "moderation"
	case row.Joining == total:
		row.Status = string(domain.ChannelJoining)
	case row.Leaving == total:
		row.Status = "leaving"
	case row.Failed == total:
		row.Status = string(domain.ChannelError)
	default:
		row.Status = string(domain.ChannelPartial)
	}
}

func displayTime(value *time.Time) string {
	if value == nil {
		return "—"
	}
	return value.Local().Format("02.01.2006 15:04")
}

func metricTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func managedChannelAssignments(channels []domain.ManagedChannel) map[domain.SourceCatalog][]domain.ID {
	assignments := make([]domain.ID, 0, len(channels))
	for _, channel := range channels {
		assignments = append(assignments, domain.ID(channel.ID))
	}
	return map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogOutbound: assignments}
}

var analyticsTableColumns = []string{"canonical", "class", "language", "frequency", "trend", "messages", "lastSeen", "forms", "actions"}

func validAnalyticsInterval(interval int) bool {
	switch interval {
	case 1, 5, 10, 30, 60, 120:
		return true
	default:
		return false
	}
}

func analyticsCollectionStatusDTO(status usecase.AnalyticsCollectionStatus) AnalyticsCollectionStatusDTO {
	state := "idle"
	if status.Enabled {
		state = "running"
	}
	if status.Running {
		state = "collecting"
	}
	result := AnalyticsCollectionStatusDTO{
		Running: status.Enabled, Collecting: status.Running, State: state, IntervalMinutes: status.IntervalMinutes,
		LastError: status.LastError, AllTimeMessages: status.AllTimeMessages,
		AllTimeKeywords: status.AllTimeKeywords, AllTimeGroups: status.AllTimeGroups,
		LatestCollection: analyticsRunMetricsDTO(status.LatestCollection),
		Totals: AnalyticsTotalsDTO{
			Messages: status.Metrics.MessageCount, CanonicalWords: status.Metrics.CanonicalCount,
			Forms: status.Metrics.FormCount, Groups: status.Metrics.GroupCount,
			KeywordDBBytes: status.Metrics.LogicalKeywordBytes,
		},
	}
	if status.LastRunAt != nil {
		result.LastRunAt = status.LastRunAt.UTC().Format(time.RFC3339Nano)
	}
	if status.NextRunAt != nil {
		result.NextRunAt = status.NextRunAt.UTC().Format(time.RFC3339Nano)
	}
	return result
}

func analyticsRunMetricsDTO(metrics domain.AnalyticsRunMetrics) AnalyticsRunMetricsDTO {
	return AnalyticsRunMetricsDTO{
		NewMessages: metrics.NewMessages, ExtractedWords: metrics.ExtractedWords,
		NewCanonicalWords: metrics.NewCanonicals, ProcessedGroups: metrics.ProcessedGroups,
		DurationMillis: metrics.Duration.Milliseconds(), Errors: append([]string(nil), metrics.Errors...),
	}
}

func analyticsRunDTO(run domain.AnalyticsSchedulerRun) AnalyticsRunDTO {
	result := AnalyticsRunDTO{
		ID: run.ID, StartedAt: run.StartedAt.UTC().Format(time.RFC3339Nano),
		NewMessages: run.Metrics.NewMessages, ExtractedWords: run.Metrics.ExtractedWords,
		NewCanonicals: run.Metrics.NewCanonicals, ProcessedGroups: run.Metrics.ProcessedGroups,
		DurationMillis: run.Metrics.Duration.Milliseconds(), Errors: append([]string(nil), run.Metrics.Errors...),
	}
	if run.FinishedAt != nil {
		result.FinishedAt = run.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	return result
}

func analyticsLanguage(value string) (domain.AnalyticsLanguage, error) {
	language := domain.AnalyticsLanguage(strings.ToLower(strings.TrimSpace(value)))
	if language != domain.AnalyticsLanguageRU && language != domain.AnalyticsLanguageEN {
		return "", errors.New("analytics service-word language must be ru or en")
	}
	return language, nil
}

func validatedServiceWord(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(strings.Fields(value)) != 1 {
		return "", errors.New("analytics service word must be one word")
	}
	return value, nil
}

func serviceWordID(language domain.AnalyticsLanguage, value string) string {
	return "service:" + string(language) + ":" + hex.EncodeToString([]byte(value))
}

func parseServiceWordID(id string) (domain.AnalyticsLanguage, string, bool) {
	parts := strings.Split(strings.TrimSpace(id), ":")
	if len(parts) != 3 || parts[0] != "service" {
		return "", "", false
	}
	language, err := analyticsLanguage(parts[1])
	if err != nil {
		return "", "", false
	}
	decoded, err := hex.DecodeString(parts[2])
	if err != nil {
		return "", "", false
	}
	word, err := validatedServiceWord(string(decoded))
	if err != nil {
		return "", "", false
	}
	return language, word, true
}

func validateAnalyticsTab(tab string) error {
	switch strings.TrimSpace(tab) {
	case "neutral", "positive", "negative", "service":
		return nil
	default:
		return errors.New("analytics table tab must be neutral, positive, negative, or service")
	}
}

func validateAnalyticsTablePreference(preference AnalyticsTablePreferenceDTO) (domain.AnalyticsTablePreference, error) {
	if len(preference.Columns) != len(analyticsTableColumns) {
		return domain.AnalyticsTablePreference{}, errors.New("analytics table column order must contain every column exactly once")
	}
	known := make(map[string]struct{}, len(analyticsTableColumns))
	for _, column := range analyticsTableColumns {
		known[column] = struct{}{}
	}
	seen := make(map[string]struct{}, len(preference.Columns))
	columns := make([]domain.AnalyticsTableColumnPreference, 0, len(preference.Columns))
	visible := 0
	for _, column := range preference.Columns {
		key := strings.TrimSpace(column.Key)
		if _, ok := known[key]; !ok {
			return domain.AnalyticsTablePreference{}, fmt.Errorf("analytics table column %q is not supported", key)
		}
		if _, duplicate := seen[key]; duplicate {
			return domain.AnalyticsTablePreference{}, errors.New("analytics table columns must be unique")
		}
		if column.Width < 48 || column.Width > 900 {
			return domain.AnalyticsTablePreference{}, fmt.Errorf("analytics column %q width must be between 48 and 900", key)
		}
		seen[key] = struct{}{}
		if column.Visible {
			visible++
		}
		columns = append(columns, domain.AnalyticsTableColumnPreference{Key: key, Width: column.Width, Visible: column.Visible})
	}
	if visible == 0 {
		return domain.AnalyticsTablePreference{}, errors.New("analytics table must have at least one visible column")
	}
	sortBy := strings.TrimSpace(preference.SortBy)
	if _, ok := known[sortBy]; !ok {
		return domain.AnalyticsTablePreference{}, fmt.Errorf("analytics sort column %q is not supported", sortBy)
	}
	direction := domain.AnalyticsSortDirection(strings.ToLower(strings.TrimSpace(preference.SortDirection)))
	if direction != domain.AnalyticsSortNone && direction != domain.AnalyticsSortAscending && direction != domain.AnalyticsSortDescending {
		return domain.AnalyticsTablePreference{}, errors.New("analytics sort direction must be none, ascending, or descending")
	}
	if preference.PageSize <= 0 || preference.PageSize > 1000 {
		return domain.AnalyticsTablePreference{}, errors.New("analytics page size must be between 1 and 1000")
	}
	return domain.AnalyticsTablePreference{
		Tab: strings.TrimSpace(preference.Tab), Columns: columns, SortBy: sortBy,
		SortDirection: direction, PageSize: preference.PageSize,
	}, nil
}
