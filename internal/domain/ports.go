package domain

import (
	"context"
	"time"
)

type AccountRepository interface {
	ListActive(ctx context.Context) ([]Account, error)
	List(ctx context.Context) ([]Account, error)
	Save(ctx context.Context, account Account) error
}

type ProxyProfileRepository interface {
	Save(ctx context.Context, profile ProxyProfile, password *string) error
	List(ctx context.Context) ([]ProxyProfile, error)
	Delete(ctx context.Context, profileID ID) error
	Route(ctx context.Context, profileID ID) (ProxyRoute, error)
	AssignmentCounts(ctx context.Context) (map[ID]int, error)
	AssignAccount(ctx context.Context, accountID ID, mode ProxyMode, profileID *ID) error
	UpdateHealth(ctx context.Context, profileID ID, status, errorCode string, at time.Time) error
}

type ChannelRepository interface {
	List(ctx context.Context) ([]Channel, error)
	ListActive(ctx context.Context) ([]Channel, error)
	Save(ctx context.Context, channel Channel) error
	SaveMembership(ctx context.Context, membership ChannelMembership) error
}

type CatalogRepository interface {
	List(ctx context.Context, catalog SourceCatalog) ([]Channel, error)
	Save(ctx context.Context, catalog SourceCatalog, channel Channel) error
}

type ScoutMessageRepository interface {
	Save(ctx context.Context, message ScoutMessage) error
	Delete(ctx context.Context, chatTelegramID string, messageID int64) error
}

type AnalyticsRepository interface {
	SaveProfile(ctx context.Context, profile AnalysisProfile) error
	ListProfiles(ctx context.Context) ([]AnalysisProfile, error)
	SaveRun(ctx context.Context, run AnalysisRun) error
	ListCandidates(ctx context.Context, runID ID) ([]KeywordCandidate, error)
	SaveModerationDecision(ctx context.Context, decision ModerationDecision) error
}

type BackupRepository interface {
	Save(ctx context.Context, backup BackupRecord) error
	List(ctx context.Context) ([]BackupRecord, error)
}

type RuleRepository interface {
	ListEnabled(ctx context.Context) ([]KeywordRule, error)
	Save(ctx context.Context, rule KeywordRule) error
}

type JobRepository interface {
	Enqueue(ctx context.Context, job OutgoingMessageJob) error
	NextDue(ctx context.Context) (*OutgoingMessageJob, error)
	MarkDone(ctx context.Context, jobID ID, event OutgoingMessageEvent) error
	Delay(ctx context.Context, jobID ID, reason string) error
}

type KeywordResponseEnqueuer interface {
	EnqueueKeywordResponse(context.Context, OutgoingMessageJob, LiveDeliveryDraft) error
}

type DelayedJobRepository interface {
	DelayUntil(ctx context.Context, jobID ID, reason string, nextAttemptAt time.Time) error
}

type StatsRepository interface {
	AccountStats(ctx context.Context) ([]Account, error)
}

type TelegramGateway interface {
	ResolveChannel(ctx context.Context, link string) (Channel, error)
	CheckMembership(ctx context.Context, account Account, channel Channel) (ChannelMembership, error)
	JoinChannel(ctx context.Context, account Account, channel Channel) (ChannelMembership, error)
	SendPublicReply(ctx context.Context, account Account, job OutgoingMessageJob) error
	SendPrivateMessage(ctx context.Context, account Account, job OutgoingMessageJob) error
}
