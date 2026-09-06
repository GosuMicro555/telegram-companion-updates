package scouting_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"telegram-companion/internal/usecase/scouting"

	"github.com/stretchr/testify/require"
)

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

type encryptedStoreSpy struct {
	saved   []scouting.EncryptedMessage
	deleted [][2]any
}

func (r *encryptedStoreSpy) CanonicalMessageAt(_ context.Context, _ string, _ int64, incoming time.Time) (time.Time, bool, error) {
	return incoming, true, nil
}

func (r *encryptedStoreSpy) SaveEncrypted(_ context.Context, message scouting.EncryptedMessage) error {
	r.saved = append(r.saved, message)
	return nil
}

func (r *encryptedStoreSpy) Delete(_ context.Context, chatID string, messageID int64, _ time.Time) error {
	r.deleted = append(r.deleted, [2]any{chatID, messageID})
	return nil
}

type cipherSpy struct {
	plaintext []byte
}

func (c *cipherSpy) Encrypt(_ context.Context, plaintext []byte, _ string, _ int64, _ time.Time) ([]byte, []byte, error) {
	c.plaintext = append([]byte(nil), plaintext...)
	return []byte("ciphertext"), []byte("nonce"), nil
}

func TestCollectorStripsAuthorFieldsBeforeRepository(t *testing.T) {
	now := time.Date(2026, 7, 11, 8, 30, 0, 0, time.UTC)
	store := &encryptedStoreSpy{}
	cipher := &cipherSpy{}
	collector := scouting.NewCollector(store, cipher, &fixedClock{now: now})

	err := collector.Ingest(context.Background(), scouting.IncomingUpdate{
		ChatID: "1001", MessageID: 77, Text: "пример текста",
		SenderID: "must-not-persist", SenderUsername: "private-author-username",
		MessageAt: now.Add(-time.Minute),
	})
	require.NoError(t, err)
	require.Equal(t, []byte("пример текста"), cipher.plaintext)
	require.Equal(t, []scouting.EncryptedMessage{{
		ChatID: "1001", MessageID: 77, Ciphertext: []byte("ciphertext"), Nonce: []byte("nonce"),
		MessageAt: now.Add(-time.Minute), ReceivedAt: now,
	}}, store.saved)

	messageType := reflect.TypeOf(store.saved[0])
	for _, field := range []string{"Text", "SenderID", "SenderUsername", "AuthorID", "AuthorUsername"} {
		_, exists := messageType.FieldByName(field)
		require.False(t, exists, field)
	}
}

func TestCollectorDeleteUsesPrivacySafeIdentity(t *testing.T) {
	store := &encryptedStoreSpy{}
	collector := scouting.NewCollector(store, &cipherSpy{}, &fixedClock{})

	require.NoError(t, collector.Delete(context.Background(), "1001", 77))
	require.Equal(t, [][2]any{{"1001", int64(77)}}, store.deleted)
}

func TestCollectorRejectsInvalidIdentity(t *testing.T) {
	collector := scouting.NewCollector(&encryptedStoreSpy{}, &cipherSpy{}, &fixedClock{})
	require.Error(t, collector.Ingest(context.Background(), scouting.IncomingUpdate{MessageID: 77}))
	require.Error(t, collector.Delete(context.Background(), "1001", 0))
}
