package domain

import (
	"errors"
	"strings"

	"telegram-companion/internal/keywordmatch"
)

type KeywordRule struct {
	ID                 ID
	Keyword            string
	Enabled            bool
	ActionMode         ActionMode
	PublicReplyText    string
	PrivateMessageText string
}

func (r KeywordRule) Validate() error {
	if strings.TrimSpace(r.Keyword) == "" {
		return errors.New("keyword is required")
	}
	if !r.Enabled {
		return nil
	}
	switch r.ActionMode {
	case ActionPublicReply:
		if strings.TrimSpace(r.PublicReplyText) == "" {
			return errors.New("public reply text is required")
		}
	case ActionPrivateMessage:
		if strings.TrimSpace(r.PrivateMessageText) == "" {
			return errors.New("private message text is required")
		}
	case ActionBoth:
		if strings.TrimSpace(r.PublicReplyText) == "" {
			return errors.New("public reply text is required")
		}
		if strings.TrimSpace(r.PrivateMessageText) == "" {
			return errors.New("private message text is required")
		}
	default:
		return errors.New("unsupported action mode")
	}
	return nil
}

func (r KeywordRule) Matches(text string) bool {
	if !r.Enabled {
		return false
	}
	return keywordmatch.Match(r.Keyword, text)
}
