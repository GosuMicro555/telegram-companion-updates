package scouting

import (
	"context"
	"errors"
	"time"

	"telegram-companion/internal/domain"
)

type IncomingUpdate struct {
	ChatID         string
	MessageID      int64
	Text           string
	SenderID       string
	SenderUsername string
	MessageAt      time.Time
	EditedAt       *time.Time
}

type EncryptedMessage struct {
	ChatID     string
	MessageID  int64
	Ciphertext []byte
	Nonce      []byte
	MessageAt  time.Time
	ReceivedAt time.Time
	EditedAt   *time.Time
}

type MessageStorage interface {
	CanonicalMessageAt(ctx context.Context, chatID string, messageID int64, incoming time.Time) (canonical time.Time, accept bool, err error)
	SaveEncrypted(ctx context.Context, message EncryptedMessage) error
	Delete(ctx context.Context, chatID string, messageID int64, deletedAt time.Time) error
}

type messageExistenceStorage interface {
	Exists(ctx context.Context, chatID string, messageID int64) (bool, error)
}

type MessageCipher interface {
	Encrypt(ctx context.Context, plaintext []byte, chatID string, messageID int64, messageAt time.Time) (ciphertext, nonce []byte, err error)
}

type Collector struct {
	storage MessageStorage
	cipher  MessageCipher
	clock   domain.Clock
}

func NewCollector(storage MessageStorage, cipher MessageCipher, clock domain.Clock) *Collector {
	return &Collector{storage: storage, cipher: cipher, clock: clock}
}

func (c *Collector) Ingest(ctx context.Context, update IncomingUpdate) error {
	if err := c.ready(); err != nil {
		return err
	}
	messageAt := update.MessageAt
	if messageAt.IsZero() {
		messageAt = c.clock.Now()
	}
	canonical, accept, err := c.storage.CanonicalMessageAt(ctx, update.ChatID, update.MessageID, messageAt)
	if err != nil {
		return err
	}
	if !accept {
		return nil
	}
	messageAt = canonical
	return c.save(ctx, update.ChatID, update.MessageID, update.Text, messageAt, c.clock.Now(), update.EditedAt)
}

func (c *Collector) Delete(ctx context.Context, chatID string, messageID int64) error {
	if err := c.ready(); err != nil {
		return err
	}
	if err := validateIdentity(chatID, messageID); err != nil {
		return err
	}
	return c.storage.Delete(ctx, chatID, messageID, c.clock.Now())
}

func (c *Collector) Exists(ctx context.Context, chatID string, messageID int64) (bool, error) {
	if err := c.ready(); err != nil {
		return false, err
	}
	if err := validateIdentity(chatID, messageID); err != nil {
		return false, err
	}
	storage, ok := c.storage.(messageExistenceStorage)
	if !ok {
		return false, nil
	}
	return storage.Exists(ctx, chatID, messageID)
}

func (c *Collector) save(ctx context.Context, chatID string, messageID int64, text string, messageAt, receivedAt time.Time, editedAt *time.Time) error {
	if err := validateIdentity(chatID, messageID); err != nil {
		return err
	}
	ciphertext, nonce, err := c.cipher.Encrypt(ctx, []byte(text), chatID, messageID, messageAt)
	if err != nil {
		return err
	}
	return c.storage.SaveEncrypted(ctx, EncryptedMessage{
		ChatID: chatID, MessageID: messageID, Ciphertext: ciphertext, Nonce: nonce,
		MessageAt: messageAt, ReceivedAt: receivedAt, EditedAt: editedAt,
	})
}

func (c *Collector) ready() error {
	if c == nil || c.storage == nil || c.cipher == nil || c.clock == nil {
		return errors.New("collector dependencies are required")
	}
	return nil
}

func validateIdentity(chatID string, messageID int64) error {
	if chatID == "" {
		return errors.New("chat ID is required")
	}
	if messageID <= 0 {
		return errors.New("message ID must be positive")
	}
	return nil
}

type ScoutMessageRepositoryAdapter struct {
	collector *Collector
}

func NewScoutMessageRepositoryAdapter(collector *Collector) *ScoutMessageRepositoryAdapter {
	return &ScoutMessageRepositoryAdapter{collector: collector}
}

func (a *ScoutMessageRepositoryAdapter) Save(ctx context.Context, message domain.ScoutMessage) error {
	if a == nil || a.collector == nil {
		return errors.New("collector adapter is not initialized")
	}
	receivedAt := message.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = a.collector.clock.Now()
	}
	return a.collector.save(ctx, message.ChatTelegramID, message.MessageID, message.Text, message.MessageAt, receivedAt, message.EditedAt)
}

func (a *ScoutMessageRepositoryAdapter) Delete(ctx context.Context, chatID string, messageID int64) error {
	return a.collector.Delete(ctx, chatID, messageID)
}

var _ domain.ScoutMessageRepository = (*ScoutMessageRepositoryAdapter)(nil)
