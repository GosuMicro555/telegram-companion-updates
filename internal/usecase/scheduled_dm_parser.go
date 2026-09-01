package usecase

import (
	"errors"
	"net/url"
	"strings"
)

var (
	ErrScheduledDMRecipientsRequired = errors.New("at least one scheduled DM recipient is required")
	ErrScheduledDMRecipientInvalid   = errors.New("scheduled DM recipient must be a Telegram username or https://t.me/username")
	ErrScheduledDMRecipientsLimit    = errors.New("scheduled DM supports at most three unique recipients")
)

func ParseScheduledDMRecipients(input string) ([]string, error) {
	lines := strings.Split(input, "\n")
	recipients := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		value := strings.TrimSpace(line)
		if value == "" {
			continue
		}
		if len(strings.Fields(value)) != 1 {
			return nil, ErrScheduledDMRecipientInvalid
		}
		username, err := normalizeScheduledDMUsername(value)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[username]; exists {
			continue
		}
		seen[username] = struct{}{}
		recipients = append(recipients, username)
	}
	if len(recipients) == 0 {
		return nil, ErrScheduledDMRecipientsRequired
	}
	if len(recipients) > 3 {
		return nil, ErrScheduledDMRecipientsLimit
	}
	return recipients, nil
}

func normalizeScheduledDMUsername(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Hostname(), "t.me") || parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" {
			return "", ErrScheduledDMRecipientInvalid
		}
		if !strings.HasPrefix(parsed.Path, "/") || strings.Count(parsed.Path, "/") != 1 {
			return "", ErrScheduledDMRecipientInvalid
		}
		value = strings.TrimPrefix(parsed.Path, "/")
	} else {
		value = strings.TrimPrefix(value, "@")
	}

	if !isScheduledDMUsername(value) {
		return "", ErrScheduledDMRecipientInvalid
	}
	return strings.ToLower(value), nil
}

func isScheduledDMUsername(value string) bool {
	if len(value) < 5 || len(value) > 32 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			continue
		}
		return false
	}
	return true
}
