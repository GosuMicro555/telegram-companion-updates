package domain

import (
	"encoding/json"
	"errors"
	"fmt"
)

const (
	JoinIntervalMinimumMinutes        = 0
	JoinIntervalMaximumMinutes        = 3000
	JoinIntervalDefaultMinimumMinutes = 10
	JoinIntervalDefaultMaximumMinutes = 60
	GroupRestDefaultHours             = 36
	GroupRestMinimumHours             = 1
	GroupRestMaximumHours             = 720
)

var (
	ErrInvalidJoinInterval   = errors.New("invalid join interval")
	ErrInvalidGroupRestHours = errors.New("invalid group rest hours")
)

const (
	ChannelStatusReady     = ChannelReady
	ChannelStatusJoining   = ChannelJoining
	ChannelStatusPartial   = ChannelPartial
	ChannelStatusError     = ChannelError
	ChannelStatusFloodWait = ChannelFloodWait
	ChannelStatusPaused    = ChannelPaused
)

type KeywordSettings struct {
	Keywords                         []string            `json:"keywords"`
	MinusKeywords                    []string            `json:"minusKeywords,omitempty"`
	SharedReply                      string              `json:"sharedReply"`
	PrivateReply                     string              `json:"privateReply"`
	DeliveryMode                     KeywordDeliveryMode `json:"deliveryMode"`
	PrivateReplyPresent              bool                `json:"-"`
	DirectMessageKeywords            []string            `json:"directMessageKeywords"`
	CanonicalTriggerIDs              []string            `json:"canonicalTriggerIds,omitempty"`
	CanonicalTriggerValues           []string            `json:"canonicalTriggerValues,omitempty"`
	DirectMessageCanonicalTriggerIDs []string            `json:"directMessageCanonicalTriggerIds,omitempty"`
	DirectMessageCanonicalValues     []string            `json:"directMessageCanonicalValues,omitempty"`
}

type KeywordDeliveryMode string

const (
	KeywordDeliveryModeComments KeywordDeliveryMode = "comments"
	KeywordDeliveryModePrivate  KeywordDeliveryMode = "private"
	KeywordDeliveryModeBoth     KeywordDeliveryMode = "both"
)

func (mode KeywordDeliveryMode) Normalized() KeywordDeliveryMode {
	switch mode {
	case KeywordDeliveryModeComments, KeywordDeliveryModePrivate, KeywordDeliveryModeBoth:
		return mode
	default:
		return KeywordDeliveryModeComments
	}
}

func (s *KeywordSettings) UnmarshalJSON(data []byte) error {
	type keywordSettingsAlias KeywordSettings
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var decoded keywordSettingsAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*s = KeywordSettings(decoded)
	_, s.PrivateReplyPresent = fields["privateReply"]
	if !s.PrivateReplyPresent {
		s.PrivateReply = s.SharedReply
	}
	s.DeliveryMode = s.DeliveryMode.Normalized()
	return nil
}

type ManagedChannel struct {
	ID           string        `json:"id"`
	Title        string        `json:"title"`
	Link         string        `json:"link"`
	Status       ChannelStatus `json:"status"`
	Members      string        `json:"members"`
	Sent         int           `json:"sent"`
	LastActivity string        `json:"lastActivity"`
	Active       bool          `json:"active"`
}

type AppSettings struct {
	RepliesPerMinute       int    `json:"repliesPerMinute"`
	MinIntervalSeconds     int    `json:"minIntervalSeconds"`
	JoinIntervalEnabled    bool   `json:"joinIntervalEnabled"`
	JoinIntervalMinMinutes int    `json:"joinIntervalMinMinutes"`
	JoinIntervalMaxMinutes int    `json:"joinIntervalMaxMinutes"`
	GroupRestHours         int    `json:"groupRestHours"`
	GroupRestEnabled       bool   `json:"groupRestEnabled"`
	DirectMessages         bool   `json:"directMessages"`
	Proxy                  string `json:"proxy"`
}

type JoinIntervalRange struct {
	MinMinutes int
	MaxMinutes int
}

func NewJoinIntervalRange(minMinutes, maxMinutes int) (JoinIntervalRange, error) {
	if minMinutes < JoinIntervalMinimumMinutes || minMinutes > JoinIntervalMaximumMinutes {
		return JoinIntervalRange{}, fmt.Errorf("%w: minimum must be between %d and %d minutes (got %d)", ErrInvalidJoinInterval, JoinIntervalMinimumMinutes, JoinIntervalMaximumMinutes, minMinutes)
	}
	if maxMinutes < JoinIntervalMinimumMinutes || maxMinutes > JoinIntervalMaximumMinutes {
		return JoinIntervalRange{}, fmt.Errorf("%w: maximum must be between %d and %d minutes (got %d)", ErrInvalidJoinInterval, JoinIntervalMinimumMinutes, JoinIntervalMaximumMinutes, maxMinutes)
	}
	if minMinutes > maxMinutes {
		return JoinIntervalRange{}, fmt.Errorf("%w: minimum must not exceed maximum (%d > %d)", ErrInvalidJoinInterval, minMinutes, maxMinutes)
	}
	return JoinIntervalRange{MinMinutes: minMinutes, MaxMinutes: maxMinutes}, nil
}

func ValidateGroupRestHours(hours int) error {
	if hours < GroupRestMinimumHours || hours > GroupRestMaximumHours {
		return fmt.Errorf("%w: must be between %d and %d hours (got %d)", ErrInvalidGroupRestHours, GroupRestMinimumHours, GroupRestMaximumHours, hours)
	}
	return nil
}

func DefaultJoinIntervalRange() JoinIntervalRange {
	return JoinIntervalRange{
		MinMinutes: JoinIntervalDefaultMinimumMinutes,
		MaxMinutes: JoinIntervalDefaultMaximumMinutes,
	}
}

func (s AppSettings) JoinIntervalRange() (JoinIntervalRange, error) {
	normalized := s.Normalized()
	return NewJoinIntervalRange(normalized.JoinIntervalMinMinutes, normalized.JoinIntervalMaxMinutes)
}

func (s *AppSettings) UnmarshalJSON(data []byte) error {
	type appSettingsAlias AppSettings
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	base := *s
	if base == (AppSettings{}) {
		base = DefaultAppSettings()
	}
	decoded := appSettingsAlias(base)
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	defaults := DefaultAppSettings()
	if _, present := fields["joinIntervalEnabled"]; !present {
		decoded.JoinIntervalEnabled = true
	}
	if _, present := fields["joinIntervalMinMinutes"]; !present {
		decoded.JoinIntervalMinMinutes = defaults.JoinIntervalMinMinutes
	}
	if _, present := fields["joinIntervalMaxMinutes"]; !present {
		decoded.JoinIntervalMaxMinutes = defaults.JoinIntervalMaxMinutes
	}
	if _, present := fields["groupRestEnabled"]; !present {
		decoded.GroupRestEnabled = true
	}
	normalized := AppSettings(decoded).Normalized()
	if _, err := normalized.JoinIntervalRange(); err != nil {
		return err
	}
	if err := ValidateGroupRestHours(normalized.GroupRestHours); err != nil {
		return err
	}
	*s = normalized
	return nil
}

func (s AppSettings) Normalized() AppSettings {
	defaults := DefaultAppSettings()
	if s == (AppSettings{}) {
		return defaults
	}
	if s.GroupRestHours == 0 {
		s.GroupRestHours = defaults.GroupRestHours
	}
	return s
}

func DefaultKeywordSettings() KeywordSettings {
	return KeywordSettings{DeliveryMode: KeywordDeliveryModeComments}
}

func DefaultChannels() []ManagedChannel {
	return nil
}

func DefaultAppSettings() AppSettings {
	return AppSettings{
		RepliesPerMinute:       19,
		MinIntervalSeconds:     2,
		JoinIntervalEnabled:    true,
		JoinIntervalMinMinutes: JoinIntervalDefaultMinimumMinutes,
		JoinIntervalMaxMinutes: JoinIntervalDefaultMaximumMinutes,
		GroupRestHours:         GroupRestDefaultHours,
		GroupRestEnabled:       true,
		DirectMessages:         false,
	}
}
