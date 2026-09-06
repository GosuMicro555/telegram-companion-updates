package usecase

import (
	"context"
	"testing"
	"time"

	"telegram-companion/internal/domain"
)

type ruleRepoStub struct{ rules []domain.KeywordRule }

func (r ruleRepoStub) ListEnabled(context.Context) ([]domain.KeywordRule, error) { return r.rules, nil }
func (r ruleRepoStub) Save(context.Context, domain.KeywordRule) error { return nil }

type jobRepoStub struct{ jobs []domain.OutgoingMessageJob }

func (j *jobRepoStub) Enqueue(_ context.Context, job domain.OutgoingMessageJob) error {
	j.jobs = append(j.jobs, job)
	return nil
}
func (j *jobRepoStub) NextDue(context.Context) (*domain.OutgoingMessageJob, error) { return nil, nil }
func (j *jobRepoStub) MarkDone(context.Context, domain.ID, domain.OutgoingMessageEvent) error { return nil }
func (j *jobRepoStub) Delay(context.Context, domain.ID, string) error { return nil }

func TestKeywordProcessorCreatesPublicAndPrivateJobs(t *testing.T) {
	jobs := &jobRepoStub{}
	processor := NewKeywordProcessor(ruleRepoStub{rules: []domain.KeywordRule{{
		ID:                 "rule-1",
		Keyword:            "hello",
		Enabled:            true,
		ActionMode:         domain.ActionBoth,
		PublicReplyText:    "Public reply text",
		PrivateMessageText: "Private reply text",
	}}}, jobs)

	err := processor.Process(context.Background(), domain.IncomingMessageEvent{
		ID:                "event-1",
		ChannelID:         "channel-1",
		SenderTelegramID:  "user-1",
		TelegramMessageID:  "msg-1",
		Text:              "say hello now",
		ReceivedAt:        time.Now(),
	})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if len(jobs.jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(jobs.jobs))
	}
	if jobs.jobs[0].Type != domain.JobPublicReply || jobs.jobs[1].Type != domain.JobPrivateMessage {
		t.Fatalf("job types = %s,%s", jobs.jobs[0].Type, jobs.jobs[1].Type)
	}
}
