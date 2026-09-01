package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	usecaseanalytics "telegram-companion/internal/usecase/analytics"
)

type MessageDecrypter interface {
	Decrypt(ctx context.Context, ciphertext, nonce []byte, chatID string, messageID int64, messageAt time.Time) ([]byte, error)
}

type EncryptedAnalyticsRowSource struct {
	db     *sql.DB
	cipher MessageDecrypter
}

func NewEncryptedAnalyticsRowSource(db *sql.DB, cipher MessageDecrypter) *EncryptedAnalyticsRowSource {
	return &EncryptedAnalyticsRowSource{db: db, cipher: cipher}
}

func (s *EncryptedAnalyticsRowSource) Rows(ctx context.Context, sourceScope string, topics []string) ([]usecaseanalytics.SourceRow, error) {
	if s == nil || s.db == nil || s.cipher == nil {
		return nil, errors.New("encrypted analytics row source is not configured")
	}
	wanted := make(map[string]struct{}, len(topics))
	for _, topic := range topics {
		wanted[strings.ToLower(strings.TrimSpace(topic))] = struct{}{}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT chat.id, chat.telegram_chat_id, chat.topic,
		message.telegram_message_id, message.encrypted_text, message.nonce, message.message_at
		FROM scout_messages message
		JOIN scout_chats chat ON chat.telegram_chat_id=message.telegram_chat_id
		WHERE chat.active=1 ORDER BY message.message_at, message.id`)
	if err != nil {
		return nil, fmt.Errorf("query encrypted analytics rows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]usecaseanalytics.SourceRow, 0)
	for rows.Next() {
		var sourceID, chatID, topic, messageAtRaw string
		var messageID int64
		var ciphertext, nonce []byte
		if err := rows.Scan(&sourceID, &chatID, &topic, &messageID, &ciphertext, &nonce, &messageAtRaw); err != nil {
			return nil, fmt.Errorf("scan encrypted analytics row: %w", err)
		}
		if sourceScope == usecaseanalytics.ScopeTopic {
			if _, ok := wanted[strings.ToLower(strings.TrimSpace(topic))]; !ok {
				continue
			}
		}
		messageAt, err := parseTime(messageAtRaw)
		if err != nil {
			return nil, err
		}
		plaintext, err := s.cipher.Decrypt(ctx, ciphertext, nonce, chatID, messageID, messageAt)
		if err != nil {
			return nil, fmt.Errorf("decrypt analytics row: %w", err)
		}
		result = append(result, usecaseanalytics.SourceRow{
			Text: string(plaintext), Topic: topic, SourceID: sourceID,
			MessageID: strconv.FormatInt(messageID, 10), MessageAt: messageAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate encrypted analytics rows: %w", err)
	}
	return result, nil
}

var _ AnalyticsRowSource = (*EncryptedAnalyticsRowSource)(nil)
