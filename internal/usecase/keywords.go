package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"telegram-companion/internal/domain"
)

type KeywordProcessor struct {
	rules domain.RuleRepository
	jobs  domain.JobRepository
}

func NewKeywordProcessor(ruleRepo domain.RuleRepository, jobRepo domain.JobRepository) *KeywordProcessor {
	return &KeywordProcessor{rules: ruleRepo, jobs: jobRepo}
}

func (p *KeywordProcessor) Process(ctx context.Context, event domain.IncomingMessageEvent) error {
	rules, err := p.rules.ListEnabled(ctx)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		if !rule.Matches(event.Text) {
			continue
		}
		if rule.ActionMode == domain.ActionPublicReply || rule.ActionMode == domain.ActionBoth {
			if err := p.jobs.Enqueue(ctx, newJob(domain.JobPublicReply, event, rule, rule.PublicReplyText)); err != nil {
				return err
			}
		}
		if rule.ActionMode == domain.ActionPrivateMessage || rule.ActionMode == domain.ActionBoth {
			if err := p.jobs.Enqueue(ctx, newJob(domain.JobPrivateMessage, event, rule, rule.PrivateMessageText)); err != nil {
				return err
			}
		}
	}
	return nil
}

func newJob(jobType domain.JobType, event domain.IncomingMessageEvent, rule domain.KeywordRule, text string) domain.OutgoingMessageJob {
	return domain.OutgoingMessageJob{
		ID:               domain.ID(randomID()),
		Type:             jobType,
		ChannelID:        event.ChannelID,
		RuleID:           rule.ID,
		TargetTelegramID: event.SenderTelegramID,
		ReplyToMessageID: event.TelegramMessageID,
		Text:             text,
		Status:           "queued",
		NextAttemptAt:    time.Now().UTC(),
		CreatedAt:        time.Now().UTC(),
	}
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}
