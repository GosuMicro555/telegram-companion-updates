package domain

import (
	"errors"
	"time"
)

var ErrProxyRouteFull = errors.New("proxy route capacity reached")

type AccountRole string

const (
	AccountRoleSpammer      AccountRole = "spammer"
	AccountRoleScoutAnalyst AccountRole = "scout_analyst"
)

type Account struct {
	ID                        ID
	DisplayName               string
	Username                  string
	PhoneMasked               string
	Role                      AccountRole
	SessionPath               string
	Status                    AccountStatus
	ProxyProfileID            *ID
	ProxyMode                 ProxyMode
	PublicRepliesSent         int64
	PrivateMessagesSent       int64
	NextDelivery              DeliveryTarget
	PrivateMessagesClosed     int64
	LastActivityAt            *time.Time
	FloodWaitUntil            *time.Time
	LastError                 string
	LegacyPauseReviewRequired bool
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

func (a Account) EffectiveNextDelivery() DeliveryTarget {
	if a.NextDelivery == DeliveryTargetPublic {
		return DeliveryTargetPublic
	}
	return DeliveryTargetPrivate
}

func (a Account) Eligible() bool {
	if a.ProxyMode == ProxyModeUnassigned {
		return false
	}
	switch a.Status {
	case AccountActive, AccountStopped, "joining", "ready", "partial":
		return true
	default:
		return false
	}
}

type ProxyProfile struct {
	ID                 ID
	Name               string
	Protocol           string
	Host               string
	Port               int
	Username           string
	PasswordConfigured bool
	Enabled            bool
	LastHealthStatus   string
	LastHealthAt       *time.Time
	LastError          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type ProxyRoute struct {
	ID       ID
	Name     string
	Protocol string
	Host     string
	Port     int
	Username string
	Password string
	Enabled  bool
}
