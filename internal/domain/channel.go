package domain

import (
	"context"
	"time"
)

type ChannelType string

const (
	ChannelTypeChannel    ChannelType = "channel"
	ChannelTypeGroup      ChannelType = "group"
	ChannelTypeSupergroup ChannelType = "supergroup"
	ChannelTypeDiscussion ChannelType = "discussion"
)

type Channel struct {
	ID                 ID
	TelegramID         string
	Title              string
	Link               string
	Topic              string
	Username           string
	Type               ChannelType
	Status             ChannelStatus
	Active             bool
	SentCount          int64
	MessageCount       int64
	LastActivityAt     *time.Time
	RemovalRequestedAt *time.Time
	LastError          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type ChannelMembership struct {
	AccountID          ID
	ChannelID          ID
	IsMember           bool
	Status             string
	LastCheckAt        *time.Time
	RequestSubmittedAt *time.Time
	JoinedAt           *time.Time
	JoinNotBefore      *time.Time
	RestStartedAt      *time.Time
	RestUntil          *time.Time
	RestDurationHours  int
	LastError          string
}

type AccountGroupRest struct {
	AccountID     ID
	AccountTitle  string
	ChannelID     ID
	ChannelTitle  string
	Catalog       SourceCatalog
	StartedAt     time.Time
	Until         time.Time
	DurationHours int
}

type GroupRestRepository interface {
	ActiveGroupRestUntil(context.Context, []ID, time.Time) (map[ID]time.Time, error)
}
