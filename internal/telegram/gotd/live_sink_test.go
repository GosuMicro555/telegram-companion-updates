package gotd

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/repository/sqlite"
	"telegram-companion/internal/usecase/runtimeconfig"
	"telegram-companion/internal/usecase/scouting"
)

type liveCatalogRepo struct {
	rows map[domain.SourceCatalog][]domain.Channel
}

func TestLiveSinkRetryDoesNotDuplicateDurableWorkflow(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	sink := NewLiveSink(
		&scoutCollectorFake{},
		liveCatalogRepo{rows: map[domain.SourceCatalog][]domain.Channel{domain.SourceCatalogOutbound: {{ID: "out", TelegramID: "100", Active: true}}}},
		&outboundConfigFake{snapshot: runtimeconfig.Snapshot{Keywords: []string{"neutral"}, DirectMessages: true, DirectMessageKeywords: []string{"neutral"}}},
		sqlite.NewJobRepository(db, func() time.Time { return now }),
		func() time.Time { return now },
	)
	update := scouting.IncomingUpdate{ChatID: "100", MessageID: 7, Text: "neutral", SenderID: "opaque-reference", MessageAt: now}

	require.NoError(t, sink.Ingest(ctx, update))
	require.NoError(t, sink.Ingest(ctx, update))

	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outgoing_message_jobs`).Scan(&count))
	require.Equal(t, 1, count)
}

func (r liveCatalogRepo) List(_ context.Context, catalog domain.SourceCatalog) ([]domain.Channel, error) {
	return append([]domain.Channel(nil), r.rows[catalog]...), nil
}
func (r liveCatalogRepo) Save(context.Context, domain.SourceCatalog, domain.Channel) error {
	return nil
}

type liveJobRepo struct {
	jobs   []domain.OutgoingMessageJob
	drafts []domain.LiveDeliveryDraft
	err    error
}

func (r *liveJobRepo) Enqueue(_ context.Context, job domain.OutgoingMessageJob) error {
	if r.err != nil {
		return r.err
	}
	r.jobs = append(r.jobs, job)
	return nil
}
func (r *liveJobRepo) NextDue(context.Context) (*domain.OutgoingMessageJob, error) { return nil, nil }
func (r *liveJobRepo) MarkDone(context.Context, domain.ID, domain.OutgoingMessageEvent) error {
	return nil
}
func (r *liveJobRepo) Delay(context.Context, domain.ID, string) error { return nil }
func (r *liveJobRepo) EnqueueKeywordResponse(_ context.Context, job domain.OutgoingMessageJob, draft domain.LiveDeliveryDraft) error {
	if r.err != nil {
		return r.err
	}
	r.jobs = append(r.jobs, job)
	r.drafts = append(r.drafts, draft)
	return nil
}

func TestLiveSinkPersistsSanitizedRowsAndDurablyQueuesOneKeywordResponse(t *testing.T) {
	collector := &scoutCollectorFake{}
	catalogs := liveCatalogRepo{rows: map[domain.SourceCatalog][]domain.Channel{domain.SourceCatalogOutbound: {{ID: "out", TelegramID: "100", Active: true}}}}
	config := &outboundConfigFake{snapshot: runtimeconfig.Snapshot{Keywords: []string{"neutral"}, DirectMessages: true, DirectMessageKeywords: []string{"neutral"}, SharedReply: "reply"}}
	jobs := &liveJobRepo{}
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	sink := NewLiveSink(collector, catalogs, config, jobs, func() time.Time { return now })

	require.NoError(t, sink.Ingest(context.Background(), scouting.IncomingUpdate{ChatID: "100", MessageID: 1, Text: "neutral one", SenderID: "opaque-reference", MessageAt: now}))

	require.Len(t, jobs.jobs, 1)
	require.Equal(t, domain.JobKeywordResponse, jobs.jobs[0].Type)
	require.Equal(t, "out", string(jobs.jobs[0].ChannelID))
	require.Equal(t, "opaque-reference", jobs.jobs[0].TargetTelegramID)
	require.Equal(t, "1", jobs.jobs[0].ReplyToMessageID)
	require.True(t, jobs.jobs[0].AllowPrivate)
	require.Equal(t, "queued", jobs.jobs[0].Status)
	require.Equal(t, now, jobs.jobs[0].NextAttemptAt)
	require.Equal(t, now, jobs.jobs[0].CreatedAt)
	require.NotEmpty(t, jobs.jobs[0].ID)
	require.Len(t, collector.ingested, 1)
	require.Empty(t, collector.ingested[0].SenderID)
	require.Empty(t, collector.ingested[0].SenderUsername)
}

func TestLiveSinkMinusKeywordSuppressesEveryPositiveTrigger(t *testing.T) {
	jobs := &liveJobRepo{}
	sink := NewLiveSink(
		&scoutCollectorFake{},
		liveCatalogRepo{rows: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "out", TelegramID: "100", Active: true}},
		}},
		&outboundConfigFake{snapshot: runtimeconfig.Snapshot{
			Keywords:      []string{"машина едет"},
			MinusKeywords: []string{"ремонт"},
		}},
		jobs,
		time.Now,
	)

	require.NoError(t, sink.Trigger(context.Background(), scouting.IncomingUpdate{
		ChatID: "100", MessageID: 11, Text: "Машина после ремонта тихо едет",
	}))
	require.Empty(t, jobs.jobs)
}

func TestLiveSinkStandaloneMinusKeywordSuppressesPositiveTrigger(t *testing.T) {
	jobs := &liveJobRepo{}
	sink := NewLiveSink(
		&scoutCollectorFake{},
		liveCatalogRepo{rows: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "out", TelegramID: "100", Active: true}},
		}},
		&outboundConfigFake{snapshot: runtimeconfig.Snapshot{
			Keywords:      []string{"деньги"},
			MinusKeywords: []string{"-деньги"},
		}},
		jobs,
		time.Now,
	)

	require.NoError(t, sink.Trigger(context.Background(), scouting.IncomingUpdate{
		ChatID: "100", MessageID: 111, Text: "Где взять денег?",
	}))
	require.Empty(t, jobs.jobs)
}

func TestLiveSinkMatchesConfiguredKeywordOperatorExpression(t *testing.T) {
	jobs := &liveJobRepo{}
	sink := NewLiveSink(
		&scoutCollectorFake{},
		liveCatalogRepo{rows: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "out", TelegramID: "100", Active: true}},
		}},
		&outboundConfigFake{snapshot: runtimeconfig.Snapshot{
			Keywords: []string{"машина едет -(ремонт|сломалась)"},
		}},
		jobs,
		time.Now,
	)

	require.NoError(t, sink.Trigger(context.Background(), scouting.IncomingUpdate{
		ChatID: "100", MessageID: 12, Text: "Машина очень тихо едет",
	}))
	require.Len(t, jobs.jobs, 1)
	require.Equal(t, "машина едет -(ремонт|сломалась)", jobs.drafts[0].TriggerSnapshot)
}

func TestLiveSinkDirectMessageOperatorExpressionUsesSourceText(t *testing.T) {
	jobs := &liveJobRepo{}
	sink := NewLiveSink(
		&scoutCollectorFake{},
		liveCatalogRepo{rows: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "out", TelegramID: "100", Active: true}},
		}},
		&outboundConfigFake{snapshot: runtimeconfig.Snapshot{
			Keywords:              []string{"(машина|авто) едет"},
			DirectMessages:        true,
			DirectMessageKeywords: []string{"(машина|авто) едет"},
		}},
		jobs,
		time.Now,
	)

	require.NoError(t, sink.Trigger(context.Background(), scouting.IncomingUpdate{
		ChatID: "100", MessageID: 13, Text: "Авто сегодня едет", SenderID: "sender",
	}))
	require.Len(t, jobs.jobs, 1)
	require.True(t, jobs.jobs[0].AllowPrivate)
}

func TestLiveSinkDMPolicyControlsAllowPrivateOnSingleKeywordResponse(t *testing.T) {
	tests := []struct {
		name             string
		directMessages   bool
		directKeywords   []string
		senderID         string
		wantAllowPrivate bool
		wantTarget       string
	}{
		{name: "global DM off", directKeywords: []string{"neutral"}, senderID: " sender-1 ", wantTarget: "sender-1"},
		{name: "sender absent", directMessages: true, directKeywords: []string{"neutral"}},
		{name: "matched keyword DM off", directMessages: true, senderID: "sender-3", wantTarget: "sender-3"},
		{name: "matched keyword DM on", directMessages: true, directKeywords: []string{"neutral"}, senderID: " sender-4 ", wantAllowPrivate: true, wantTarget: "sender-4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := &liveJobRepo{}
			sink := NewLiveSink(
				&scoutCollectorFake{},
				liveCatalogRepo{rows: map[domain.SourceCatalog][]domain.Channel{domain.SourceCatalogOutbound: {{ID: "out", TelegramID: "100", Active: true}}}},
				&outboundConfigFake{snapshot: runtimeconfig.Snapshot{Keywords: []string{"neutral"}, DirectMessages: tt.directMessages, DirectMessageKeywords: tt.directKeywords}},
				jobs,
				time.Now,
			)

			require.NoError(t, sink.Trigger(context.Background(), scouting.IncomingUpdate{ChatID: "100", MessageID: 10, Text: "neutral", SenderID: tt.senderID}))
			require.Len(t, jobs.jobs, 1)
			require.Equal(t, domain.JobKeywordResponse, jobs.jobs[0].Type)
			require.Equal(t, tt.wantTarget, jobs.jobs[0].TargetTelegramID)
			require.Equal(t, tt.wantAllowPrivate, jobs.jobs[0].AllowPrivate)
		})
	}
}

func TestLiveSinkEnqueuesPublicAndPrivateJobsForBothKeywordDeliveryMode(t *testing.T) {
	jobs := &liveJobRepo{}
	sink := NewLiveSink(
		&scoutCollectorFake{},
		liveCatalogRepo{rows: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "out", TelegramID: "100", Active: true}},
		}},
		&outboundConfigFake{snapshot: runtimeconfig.Snapshot{
			Keywords:     []string{"neutral"},
			DeliveryMode: domain.KeywordDeliveryModeBoth,
		}},
		jobs,
		time.Now,
	)

	require.NoError(t, sink.Trigger(context.Background(), scouting.IncomingUpdate{
		ChatID: "100", MessageID: 88, Text: "neutral", SenderID: "sender-88",
	}))

	require.Len(t, jobs.jobs, 2)
	require.NotEqual(t, jobs.jobs[0].ID, jobs.jobs[1].ID)
	require.ElementsMatch(t, []domain.KeywordDeliveryMode{
		domain.KeywordDeliveryModeComments,
		domain.KeywordDeliveryModePrivate,
	}, []domain.KeywordDeliveryMode{
		jobs.jobs[0].KeywordDeliveryMode,
		jobs.jobs[1].KeywordDeliveryMode,
	})
}

func TestLiveSinkSuppressesEditedUpdate(t *testing.T) {
	editedAt := time.Now().UTC()
	jobs := &liveJobRepo{}
	sink := NewLiveSink(
		&scoutCollectorFake{},
		liveCatalogRepo{rows: map[domain.SourceCatalog][]domain.Channel{domain.SourceCatalogOutbound: {{ID: "out", TelegramID: "100", Active: true}}}},
		&outboundConfigFake{snapshot: runtimeconfig.Snapshot{Keywords: []string{"neutral"}}},
		jobs,
		time.Now,
	)

	require.NoError(t, sink.Trigger(context.Background(), scouting.IncomingUpdate{ChatID: "100", MessageID: 11, Text: "neutral", EditedAt: &editedAt}))
	require.Empty(t, jobs.jobs)
}

func TestLiveSinkDoesNotQueueTriggersWhileOutboundAutomationIsPaused(t *testing.T) {
	jobs := &liveJobRepo{}
	sink := NewLiveSink(
		&scoutCollectorFake{},
		liveCatalogRepo{rows: map[domain.SourceCatalog][]domain.Channel{domain.SourceCatalogOutbound: {{ID: "out", TelegramID: "100", Active: true}}}},
		&outboundConfigFake{snapshot: runtimeconfig.Snapshot{OutboundPaused: true, Keywords: []string{"neutral"}}},
		jobs,
		time.Now,
	)

	require.NoError(t, sink.Trigger(context.Background(), scouting.IncomingUpdate{ChatID: "100", MessageID: 12, Text: "neutral"}))
	require.Empty(t, jobs.jobs)
}

func TestLiveSinkStoresNamespacedTelegramPeerUnderRawCatalogChatID(t *testing.T) {
	collector := &scoutCollectorFake{}
	jobs := &liveJobRepo{}
	sink := NewLiveSink(collector, liveCatalogRepo{}, &outboundConfigFake{}, jobs, time.Now)

	require.NoError(t, sink.CollectHistory(context.Background(), scouting.IncomingUpdate{
		ChatID: "channel:100", MessageID: 7, Text: "history", MessageAt: time.Now().UTC(),
	}))

	require.Len(t, collector.ingested, 1)
	require.Equal(t, "100", collector.ingested[0].ChatID)
	require.Empty(t, jobs.jobs)
}

func TestLiveSinkPropagatesQueueFailureInsteadOfDroppingTrigger(t *testing.T) {
	want := errors.New("queue unavailable")
	sink := NewLiveSink(
		&scoutCollectorFake{},
		liveCatalogRepo{rows: map[domain.SourceCatalog][]domain.Channel{domain.SourceCatalogOutbound: {{ID: "out", TelegramID: "100", Active: true}}}},
		&outboundConfigFake{snapshot: runtimeconfig.Snapshot{Keywords: []string{"neutral"}}},
		&liveJobRepo{err: want},
		time.Now,
	)

	err := sink.Ingest(context.Background(), scouting.IncomingUpdate{ChatID: "100", MessageID: 1, Text: "neutral", MessageAt: time.Now().UTC()})
	require.ErrorIs(t, err, want)
}

func TestContainsKeywordMatchesWholeWordsAndSameSentencePhrases(t *testing.T) {
	require.True(t, containsKeyword("Денег сегодня нет!", []string{"денег"}))
	require.True(t, containsKeyword("BUY now", []string{"buy"}))
	require.False(t, containsKeyword("который", []string{"кот"}))
	require.False(t, containsKeyword("покупатель", []string{"куп"}))
	require.True(t, containsKeyword("где взять денег", []string{"взять денег"}))
	require.False(t, containsKeyword("где взять", []string{"взять денег"}))
	require.False(t, containsKeyword("деньги нужны. Где взять?", []string{"взять денег"}))
}

func TestMatchCanonicalTriggerReturnsOwnerForInflectedForm(t *testing.T) {
	trigger, ok := matchCanonicalTrigger("сегодня нет денег", []runtimeconfig.CanonicalTrigger{{
		ID: "money", Canonical: "деньги", Forms: []string{"деньги", "денег"},
	}})

	require.True(t, ok)
	require.Equal(t, "money", trigger.ID)
}

func TestMatchCanonicalTriggerMatchesPhrasesWithinOneSentence(t *testing.T) {
	trigger := runtimeconfig.CanonicalTrigger{ID: "vehicle", Canonical: "\u043c\u0430\u0448\u0438\u043d\u0430 \u0435\u0434\u0435\u0442"}

	for _, tt := range []struct {
		name string
		text string
		want bool
	}{
		{name: "Russian words with intervening text", text: "\u0421\u0435\u0439\u0447\u0430\u0441 \u043c\u0430\u0448\u0438\u043d\u0430 \u043e\u0447\u0435\u043d\u044c \u0442\u0438\u0445\u043e \u0435\u0434\u0435\u0442", want: true},
		{name: "Russian words in reverse order", text: "\u0435\u0434\u0435\u0442 \u043c\u0430\u0448\u0438\u043d\u0430", want: true},
		{name: "missing second phrase word", text: "\u0421\u0435\u0439\u0447\u0430\u0441 \u043c\u0430\u0448\u0438\u043d\u0430", want: false},
		{name: "missing first phrase word", text: "\u0421\u0435\u0439\u0447\u0430\u0441 \u0435\u0434\u0435\u0442", want: false},
		{name: "words in separate sentences", text: "\u041c\u0430\u0448\u0438\u043d\u0430 \u0441\u0442\u043e\u0438\u0442. \u041f\u043e\u0435\u0437\u0434 \u0435\u0434\u0435\u0442.", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := matchCanonicalTrigger(tt.text, []runtimeconfig.CanonicalTrigger{trigger})
			require.Equal(t, tt.want, ok)
			if ok {
				require.Equal(t, "vehicle", got.ID)
			}
		})
	}
}

func TestMatchCanonicalTriggerMatchesEnglishPhrase(t *testing.T) {
	trigger, ok := matchCanonicalTrigger("The car quietly moves", []runtimeconfig.CanonicalTrigger{{
		ID: "car-moves", Canonical: "car moves",
	}})

	require.True(t, ok)
	require.Equal(t, "car-moves", trigger.ID)
}

func TestMatchCanonicalTriggerKeepsAdditionalFormsAsExactSingleWords(t *testing.T) {
	trigger := runtimeconfig.CanonicalTrigger{ID: "vehicle", Canonical: "\u043c\u0430\u0448\u0438\u043d\u0430 \u0435\u0434\u0435\u0442", Forms: []string{"\u043a\u043e\u0442"}}

	_, ok := matchCanonicalTrigger("\u043a\u043e\u0442\u043e\u0440\u044b\u0439", []runtimeconfig.CanonicalTrigger{trigger})
	require.False(t, ok)

	matched, ok := matchCanonicalTrigger("\u043a\u043e\u0442 \u0441\u043f\u0438\u0442", []runtimeconfig.CanonicalTrigger{trigger})
	require.True(t, ok)
	require.Equal(t, "vehicle", matched.ID)
}

func TestMatchCanonicalTriggerSegmentsBeforeNormalizingSentenceBoundaries(t *testing.T) {
	trigger := runtimeconfig.CanonicalTrigger{ID: "vehicle", Canonical: "\u043c\u0430\u0448\u0438\u043d\u0430 \u0435\u0434\u0435\u0442"}

	for _, text := range []string{
		"\u043c\u0430\u0448\u0438\u043d\u0430\r\n\u0435\u0434\u0435\u0442",
		"\u043c\u0430\u0448\u0438\u043d\u0430... \u0435\u0434\u0435\u0442",
		"\u043c\u0430\u0448\u0438\u043d\u0430!?!\u0435\u0434\u0435\u0442",
	} {
		t.Run(text, func(t *testing.T) {
			_, ok := matchCanonicalTrigger(text, []runtimeconfig.CanonicalTrigger{trigger})
			require.False(t, ok)
		})
	}
}

func TestMatchCanonicalTriggerReturnsCompoundIdentityForCanonicalAndForms(t *testing.T) {
	configured := runtimeconfig.CanonicalTrigger{
		ID: "car-moves", Canonical: "car\u00a0moves", Forms: []string{"vehicle"},
	}

	for _, text := range []string{"The car quietly moves", "vehicle parked"} {
		trigger, ok := matchCanonicalTrigger(text, []runtimeconfig.CanonicalTrigger{configured})
		require.True(t, ok)
		require.Equal(t, "car-moves", trigger.ID)
		require.Equal(t, "car\u00a0moves", trigger.Canonical)
	}
}

func TestLiveSinkCompoundTriggerUsesMatchedCanonicalForAllowPrivate(t *testing.T) {
	jobs := &liveJobRepo{}
	sink := NewLiveSink(
		&scoutCollectorFake{},
		liveCatalogRepo{rows: map[domain.SourceCatalog][]domain.Channel{
			domain.SourceCatalogOutbound: {{ID: "out", TelegramID: "100", Active: true}},
		}},
		&outboundConfigFake{snapshot: runtimeconfig.Snapshot{
			CanonicalTriggers: []runtimeconfig.CanonicalTrigger{{
				ID: "car-moves", Canonical: "car moves", Forms: []string{"car moves"},
			}},
			DirectMessages:        true,
			DirectMessageKeywords: []string{"car moves"},
		}},
		jobs,
		time.Now,
	)

	require.NoError(t, sink.Trigger(context.Background(), scouting.IncomingUpdate{
		ChatID: "100", MessageID: 91, Text: "the car quietly moves", SenderID: "sender",
	}))
	require.Len(t, jobs.jobs, 1)
	require.Equal(t, domain.ID("car-moves"), jobs.jobs[0].RuleID)
	require.True(t, jobs.jobs[0].AllowPrivate)
}

func TestMatchCanonicalTriggerPrefersMostSpecificMatch(t *testing.T) {
	trigger, ok := matchCanonicalTrigger("the car quietly moves", []runtimeconfig.CanonicalTrigger{
		{ID: "car", Canonical: "car", Forms: []string{"car"}},
		{ID: "car-moves", Canonical: "car moves", Forms: []string{"car moves"}},
	})

	require.True(t, ok)
	require.Equal(t, "car-moves", trigger.ID)
}

func TestMatchCanonicalTriggerUsesFormsForEveryCompoundToken(t *testing.T) {
	configured := runtimeconfig.CanonicalTrigger{
		ID:        "vehicle-moves",
		Canonical: "car moves",
		Forms: []string{
			"automobile moves",
			"car drives",
		},
	}

	for _, tt := range []struct {
		name string
		text string
		want bool
	}{
		{name: "all token forms", text: "automobile quietly drives", want: true},
		{name: "first token form only", text: "automobile parked", want: false},
		{name: "second token form only", text: "train drives", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := matchCanonicalTrigger(tt.text, []runtimeconfig.CanonicalTrigger{configured})
			require.Equal(t, tt.want, ok)
		})
	}
}

func TestMatchCanonicalTriggerPreservesRepeatedTokenMultiplicity(t *testing.T) {
	configured := runtimeconfig.CanonicalTrigger{
		ID: "very-very-good", Canonical: "very very good", Forms: []string{"very very good"},
	}

	_, ok := matchCanonicalTrigger("very good", []runtimeconfig.CanonicalTrigger{configured})
	require.False(t, ok)

	trigger, ok := matchCanonicalTrigger("very very good", []runtimeconfig.CanonicalTrigger{configured})
	require.True(t, ok)
	require.Equal(t, "very-very-good", trigger.ID)
}

func TestLiveSinkEnqueuesCanonicalAuditDraft(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	jobs := &liveJobRepo{}
	sink := NewLiveSink(
		&scoutCollectorFake{},
		liveCatalogRepo{rows: map[domain.SourceCatalog][]domain.Channel{domain.SourceCatalogOutbound: {{ID: "out", TelegramID: "100", Active: true}}}},
		&outboundConfigFake{snapshot: runtimeconfig.Snapshot{
			CanonicalTriggers: []runtimeconfig.CanonicalTrigger{{ID: "money", Canonical: "деньги", Forms: []string{"деньги", "денег"}}},
			DirectMessages:    true, DirectMessageKeywords: []string{"денег"},
		}},
		jobs,
		func() time.Time { return now },
	)

	require.NoError(t, sink.Trigger(context.Background(), scouting.IncomingUpdate{ChatID: "100", MessageID: 1, Text: "  Сегодня нет денег  ", SenderID: "sender"}))
	require.Len(t, jobs.jobs, 1)
	require.Equal(t, domain.ID("money"), jobs.jobs[0].RuleID)
	require.Equal(t, domain.LiveDeliveryDraft{
		SourceMessage: "Сегодня нет денег", TriggerCanonicalID: "money", TriggerSnapshot: "деньги", TriggeredAt: now,
	}, jobs.drafts[0])
	require.True(t, jobs.jobs[0].AllowPrivate)
}

func TestLiveSinkEnqueuesCanonicalTriggerForNamespacedCatalogChannel(t *testing.T) {
	for _, chatID := range []string{"channel:4291488698", "chat:4291488698"} {
		for _, message := range []string{"тест", "тестик"} {
			t.Run(chatID+"/"+message, func(t *testing.T) {
				now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
				jobs := &liveJobRepo{}
				sink := NewLiveSink(
					&scoutCollectorFake{},
					liveCatalogRepo{rows: map[domain.SourceCatalog][]domain.Channel{
						domain.SourceCatalogOutbound: {{ID: "out", TelegramID: "4291488698", Active: true}},
					}},
					&outboundConfigFake{snapshot: runtimeconfig.Snapshot{CanonicalTriggers: []runtimeconfig.CanonicalTrigger{{
						ID: "test", Canonical: "тест", Forms: []string{"тест", "тестик"},
					}}}},
					jobs,
					func() time.Time { return now },
				)

				require.NoError(t, sink.Trigger(context.Background(), scouting.IncomingUpdate{
					ChatID: chatID, MessageID: 73, Text: message,
				}))
				require.Len(t, jobs.jobs, 1)
				require.Equal(t, domain.ID("out"), jobs.jobs[0].ChannelID)
				require.Equal(t, domain.ID("test"), jobs.jobs[0].RuleID)
				require.Equal(t, "73", jobs.jobs[0].ReplyToMessageID)
				require.Len(t, jobs.drafts, 1)
				require.Equal(t, "тест", jobs.drafts[0].TriggerSnapshot)
			})
		}
	}
}
