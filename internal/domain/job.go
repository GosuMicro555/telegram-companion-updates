package domain

import "time"

type IncomingMessageEvent struct {
	ID                ID
	ChannelID         ID
	TelegramMessageID string
	SenderTelegramID  string
	Text              string
	ReceivedAt        time.Time
}

type OutgoingMessageJob struct {
	ID                  ID
	Type                JobType
	AccountID           *ID
	ChannelID           ID
	RuleID              ID
	TargetTelegramID    string
	ReplyToMessageID    string
	Text                string
	AllowPrivate        bool
	KeywordDeliveryMode KeywordDeliveryMode
	FallbackToPublic    bool
	Status              string
	Attempts            int
	NextAttemptAt       time.Time
	CreatedAt           time.Time
}

type KeywordDeliveryOutcome struct {
	JobID                ID
	AccountID            ID
	ChannelID            ID
	AccountTitleSnapshot string
	Type                 JobType
	AdvanceTo            *DeliveryTarget
	CompletedAt          time.Time
}

type KeywordDeliveryFailure struct {
	JobID                ID
	AccountID            *ID
	AccountTitleSnapshot string
	Type                 *JobType
	ErrorCode            string
	FailedAt             time.Time
	NextAttemptAt        time.Time
}

type OutgoingMessageEvent struct {
	ID        ID
	JobID     ID
	AccountID ID
	ChannelID ID
	Type      JobType
	Success   bool
	ErrorCode string
	CreatedAt time.Time
}
