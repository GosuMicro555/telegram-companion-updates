package domain

import "time"

type ID string

type ChannelStatus string

const (
	ChannelReady     ChannelStatus = "ready"
	ChannelJoining   ChannelStatus = "joining"
	ChannelPartial   ChannelStatus = "partial"
	ChannelError     ChannelStatus = "error"
	ChannelFloodWait ChannelStatus = "flood_wait"
	ChannelPaused    ChannelStatus = "paused"
)

type AccountStatus string

const (
	AccountActive    AccountStatus = "active"
	AccountStopped   AccountStatus = "stopped"
	AccountPaused    AccountStatus = "paused"
	AccountError     AccountStatus = "error"
	AccountLimited   AccountStatus = "limited"
	AccountFloodWait AccountStatus = "flood_wait"
)

type JobType string

const (
	JobPublicReply     JobType = "public_reply"
	JobPrivateMessage  JobType = "private_message"
	JobKeywordResponse JobType = "keyword_response"
)

type DeliveryTarget string

const (
	DeliveryTargetPrivate DeliveryTarget = "private"
	DeliveryTargetPublic  DeliveryTarget = "public"
)

type ActionMode string

const (
	ActionPublicReply    ActionMode = "public_reply"
	ActionPrivateMessage ActionMode = "private_message"
	ActionBoth           ActionMode = "both"
)

type ProxyMode string

const (
	ProxyModeAssigned   ProxyMode = "assigned"
	ProxyModeGlobal     ProxyMode = "global"
	ProxyModeDirect     ProxyMode = "direct"
	ProxyModeUnassigned ProxyMode = "unassigned"
	SystemProxyRouteID  ID        = "system"
)

type Clock interface {
	Now() time.Time
}
