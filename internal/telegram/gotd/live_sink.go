package gotd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	core "telegram-companion/internal/analytics"
	analytictext "telegram-companion/internal/analytics/text"
	"telegram-companion/internal/domain"
	"telegram-companion/internal/keywordmatch"
	"telegram-companion/internal/usecase/runtimeconfig"
	"telegram-companion/internal/usecase/scouting"
)

// LiveSink persists a sanitized copy first, then evaluates the current hot
// trigger configuration entirely in memory for spammer-only outbound work.
type LiveSink struct {
	collector UpdateCollector
	catalogs  domain.CatalogRepository
	config    RuntimeSnapshotSource
	jobs      domain.JobRepository
	now       func() time.Time
}

func NewLiveSink(collector UpdateCollector, catalogs domain.CatalogRepository, config RuntimeSnapshotSource, jobs domain.JobRepository, now func() time.Time) *LiveSink {
	return &LiveSink{collector: collector, catalogs: catalogs, config: config, jobs: jobs, now: now}
}

func (s *LiveSink) Ingest(ctx context.Context, update scouting.IncomingUpdate) error {
	if err := s.CollectHistory(ctx, update); err != nil {
		return err
	}
	return s.Trigger(ctx, update)
}

// CollectHistory persists an anonymized historical message without evaluating
// live reply triggers. History synchronization must never answer old messages.
func (s *LiveSink) CollectHistory(ctx context.Context, update scouting.IncomingUpdate) error {
	update.ChatID = storageChatID(update.ChatID)
	return s.collector.Ingest(ctx, scouting.IncomingUpdate{
		ChatID: update.ChatID, MessageID: update.MessageID, Text: update.Text,
		MessageAt: update.MessageAt, EditedAt: update.EditedAt,
	})
}

func (s *LiveSink) Trigger(ctx context.Context, update scouting.IncomingUpdate) error {
	if update.EditedAt != nil {
		return nil
	}
	snapshot := s.config.Current()
	if snapshot.OutboundPaused {
		return nil
	}
	text := strings.ToLower(strings.TrimSpace(update.Text))
	if text == "" || text == strings.ToLower(strings.TrimSpace(snapshot.SharedReply)) {
		return nil
	}
	if keywordmatch.MatchesAnyExclusion(snapshot.MinusKeywords, update.Text) {
		return nil
	}
	trigger, matched := matchCanonicalTrigger(text, snapshot.CanonicalTriggers)
	if !matched {
		legacy, ok := matchLegacyKeyword(text, snapshot.Keywords)
		if !ok {
			return nil
		}
		trigger = runtimeconfig.CanonicalTrigger{Canonical: legacy, Forms: []string{legacy}}
	}
	channelID, err := s.outboundChannel(ctx, update.ChatID)
	if err != nil || channelID == "" {
		return err
	}
	now := s.now().UTC()
	enqueuer, ok := s.jobs.(domain.KeywordResponseEnqueuer)
	if !ok {
		return errors.New("outgoing job repository does not support keyword response enqueue")
	}
	draft := domain.LiveDeliveryDraft{
		SourceMessage: strings.TrimSpace(update.Text), TriggerCanonicalID: domain.ID(trigger.ID),
		TriggerSnapshot: trigger.Canonical, TriggeredAt: now,
	}
	for _, job := range keywordDeliveryJobs(snapshot, update, channelID, trigger, now) {
		if err := enqueuer.EnqueueKeywordResponse(ctx, job, draft); err != nil {
			return err
		}
	}
	return nil
}

func keywordDeliveryJobs(snapshot runtimeconfig.Snapshot, update scouting.IncomingUpdate, channelID domain.ID, trigger runtimeconfig.CanonicalTrigger, now time.Time) []domain.OutgoingMessageJob {
	target := strings.TrimSpace(update.SenderID)
	if snapshot.DeliveryMode == "" {
		allowPrivate := snapshot.DirectMessages && target != "" && triggerAllowsPrivate(trigger, snapshot.DirectMessageKeywords, update.Text)
		return []domain.OutgoingMessageJob{{
			ID: outgoingJobID(update, domain.JobKeywordResponse), Type: domain.JobKeywordResponse, ChannelID: channelID,
			RuleID: domain.ID(trigger.ID), TargetTelegramID: target, ReplyToMessageID: strconv.FormatInt(update.MessageID, 10),
			AllowPrivate: allowPrivate, Status: "queued", NextAttemptAt: now, CreatedAt: now,
		}}
	}
	mode := snapshot.DeliveryMode.Normalized()
	base := func(deliveryMode domain.KeywordDeliveryMode, fallbackToPublic bool) domain.OutgoingMessageJob {
		return domain.OutgoingMessageJob{
			ID: outgoingJobID(update, domain.JobKeywordResponse, string(deliveryMode)), Type: domain.JobKeywordResponse, ChannelID: channelID,
			RuleID: domain.ID(trigger.ID), TargetTelegramID: target, ReplyToMessageID: strconv.FormatInt(update.MessageID, 10),
			KeywordDeliveryMode: deliveryMode, FallbackToPublic: fallbackToPublic,
			Status: "queued", NextAttemptAt: now, CreatedAt: now,
		}
	}
	switch mode {
	case domain.KeywordDeliveryModePrivate:
		if target == "" {
			return nil
		}
		return []domain.OutgoingMessageJob{base(domain.KeywordDeliveryModePrivate, true)}
	case domain.KeywordDeliveryModeBoth:
		jobs := []domain.OutgoingMessageJob{base(domain.KeywordDeliveryModeComments, false)}
		if target != "" {
			jobs = append(jobs, base(domain.KeywordDeliveryModePrivate, false))
		}
		return jobs
	default:
		return []domain.OutgoingMessageJob{base(domain.KeywordDeliveryModeComments, false)}
	}
}

func outgoingJobID(update scouting.IncomingUpdate, jobType domain.JobType, identity ...string) domain.ID {
	value := update.ChatID + "\x00" + strconv.FormatInt(update.MessageID, 10) + "\x00" + string(jobType)
	if len(identity) > 0 {
		value += "\x00" + strings.Join(identity, "\x00")
	}
	sum := sha256.Sum256([]byte(value))
	return domain.ID(hex.EncodeToString(sum[:]))
}

func (s *LiveSink) Delete(ctx context.Context, chatID string, messageID int64) error {
	return s.collector.Delete(ctx, storageChatID(chatID), messageID)
}

func (s *LiveSink) Exists(ctx context.Context, chatID string, messageID int64) (bool, error) {
	return s.collector.Exists(ctx, storageChatID(chatID), messageID)
}

func storageChatID(chatID string) string {
	chatID = strings.TrimSpace(chatID)
	for _, namespace := range []string{"channel:", "chat:", "user:"} {
		if strings.HasPrefix(chatID, namespace) {
			return strings.TrimPrefix(chatID, namespace)
		}
	}
	return chatID
}

func (s *LiveSink) outboundChannel(ctx context.Context, telegramID string) (domain.ID, error) {
	rows, err := s.catalogs.List(ctx, domain.SourceCatalogOutbound)
	if err != nil {
		return "", err
	}
	telegramID = storageChatID(telegramID)
	for _, row := range rows {
		if row.Active && storageChatID(row.TelegramID) == telegramID {
			return row.ID, nil
		}
	}
	return "", nil
}

func containsKeyword(text string, keywords []string) bool {
	_, ok := matchLegacyKeyword(text, keywords)
	return ok
}

func matchCanonicalTrigger(text string, triggers []runtimeconfig.CanonicalTrigger) (runtimeconfig.CanonicalTrigger, bool) {
	sentences := normalizedPhraseSentences(text)
	bestScore := 0
	var best runtimeconfig.CanonicalTrigger
	for _, trigger := range triggers {
		score := canonicalTriggerMatchScore(sentences, text, trigger)
		if score > bestScore {
			bestScore = score
			best = trigger
		}
	}
	return best, bestScore > 0
}

func normalizedPhraseSentences(value string) [][]string {
	var result [][]string
	var sentence strings.Builder
	flush := func() {
		if tokens := strings.Fields(analytictext.Normalize(sentence.String())); len(tokens) > 0 {
			result = append(result, tokens)
		}
		sentence.Reset()
	}
	for _, r := range value {
		switch r {
		case '.', '!', '?', '\n', '\r':
			flush()
		default:
			sentence.WriteRune(r)
		}
	}
	flush()
	return result
}

func canonicalTriggerMatchScore(sentences [][]string, text string, trigger runtimeconfig.CanonicalTrigger) int {
	required := compoundTokenVariants(trigger)
	if len(required) >= 2 {
		for _, sentence := range sentences {
			if sentenceMatchesCompound(sentence, required) {
				return len(required)
			}
		}
	}

	if _, ok := matchLegacyKeyword(text, trigger.Forms); ok {
		return 1
	}
	return 0
}

func compoundTokenVariants(trigger runtimeconfig.CanonicalTrigger) []map[string]struct{} {
	canonicalTokens := strings.Fields(analytictext.Normalize(trigger.Canonical))
	if len(canonicalTokens) < 2 {
		return nil
	}
	required := make([]map[string]struct{}, len(canonicalTokens))
	for index, token := range canonicalTokens {
		required[index] = map[string]struct{}{canonicalTokenKey(token): {}}
	}
	for _, form := range trigger.Forms {
		tokens := strings.Fields(analytictext.Normalize(form))
		if len(tokens) != len(required) {
			continue
		}
		for index, token := range tokens {
			required[index][canonicalTokenKey(token)] = struct{}{}
		}
	}
	return required
}

func sentenceMatchesCompound(sentence []string, required []map[string]struct{}) bool {
	if len(sentence) < len(required) {
		return false
	}
	available := make([]string, len(sentence))
	for index, token := range sentence {
		available[index] = canonicalTokenKey(token)
	}
	matchedSlot := make([]int, len(available))
	for index := range matchedSlot {
		matchedSlot[index] = -1
	}
	var assign func(int, []bool) bool
	assign = func(slot int, seen []bool) bool {
		for tokenIndex, token := range available {
			if seen[tokenIndex] {
				continue
			}
			if _, ok := required[slot][token]; !ok {
				continue
			}
			seen[tokenIndex] = true
			if matchedSlot[tokenIndex] == -1 || assign(matchedSlot[tokenIndex], seen) {
				matchedSlot[tokenIndex] = slot
				return true
			}
		}
		return false
	}
	for slot := range required {
		if !assign(slot, make([]bool, len(available))) {
			return false
		}
	}
	return true
}

func canonicalTokenKey(token string) string {
	for value := range core.CanonicalTokenSet([]string{token}) {
		return value
	}
	return token
}

func triggerAllowsPrivate(trigger runtimeconfig.CanonicalTrigger, keywords []string, sourceText string) bool {
	if keywordmatch.MatchesAny(keywords, sourceText) {
		return true
	}
	allowed := make(map[string]struct{}, len(keywords))
	for _, keyword := range keywords {
		if normalized := analytictext.Normalize(keyword); normalized != "" {
			allowed[normalized] = struct{}{}
		}
	}
	values := append([]string{trigger.Canonical}, trigger.Forms...)
	for _, value := range values {
		if _, ok := allowed[analytictext.Normalize(value)]; ok {
			return true
		}
	}
	return false
}

func matchLegacyKeyword(text string, keywords []string) (string, bool) {
	for _, keyword := range keywords {
		if keywordmatch.Match(keyword, text) {
			return strings.TrimSpace(keyword), true
		}
	}
	return "", false
}

var _ UpdateCollector = (*LiveSink)(nil)
