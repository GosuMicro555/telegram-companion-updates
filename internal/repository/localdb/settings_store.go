package localdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase"

	bolt "go.etcd.io/bbolt"
)

var (
	settingsBucket = []byte("settings")
	keywordsKey    = []byte("keywords")
	channelsKey    = []byte("channels")
	appSettingsKey = []byte("app_settings")
	accountsKey    = []byte("accounts")
	outboundKey    = []byte("outbound_catalog")
	scoutKey       = []byte("scout_catalog")
)

type KeywordSettingsStore struct {
	db *bolt.DB
}

// Snapshot writes a transactionally consistent bbolt image without copying
// the live database file outside a read transaction.
func (s *KeywordSettingsStore) Snapshot(ctx context.Context, destination string) (retErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := output.Close(); closeErr != nil {
			retErr = errors.Join(retErr, closeErr)
		}
		if retErr != nil {
			_ = os.Remove(destination)
		}
	}()
	if err := s.db.View(func(tx *bolt.Tx) error {
		_, err := tx.WriteTo(contextWriter{ctx: ctx, writer: output})
		return err
	}); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	return ctx.Err()
}

type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (w contextWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.writer.Write(data)
}

func (s *KeywordSettingsStore) ListAccounts(ctx context.Context) ([]domain.Account, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	accounts := defaultAccounts()
	err := s.db.View(func(tx *bolt.Tx) error {
		value := tx.Bucket(settingsBucket).Get(accountsKey)
		if len(value) == 0 {
			return nil
		}
		return json.Unmarshal(value, &accounts)
	})
	return accounts, err
}

func (s *KeywordSettingsStore) SaveAccount(ctx context.Context, account domain.Account) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(settingsBucket)
		accounts, err := accountsFromBucket(bucket)
		if err != nil {
			return err
		}
		for index := range accounts {
			if accounts[index].ID != account.ID {
				continue
			}
			accounts[index] = account
			return putJSON(bucket, accountsKey, accounts)
		}
		return errors.New("account not found")
	})
}

func (s *KeywordSettingsStore) ListCatalog(ctx context.Context, catalog domain.SourceCatalog) ([]domain.Channel, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	key, err := catalogKey(catalog)
	if err != nil {
		return nil, err
	}
	rows := make([]domain.Channel, 0)
	err = s.db.View(func(tx *bolt.Tx) error {
		var err error
		rows, err = catalogFromBucket(tx.Bucket(settingsBucket), catalog, key)
		return err
	})
	return rows, err
}

func (s *KeywordSettingsStore) SaveCatalog(ctx context.Context, catalog domain.SourceCatalog, channel domain.Channel) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	key, err := catalogKey(catalog)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(settingsBucket)
		rows, err := catalogFromBucket(bucket, catalog, key)
		if err != nil {
			return err
		}
		for index := range rows {
			if rows[index].ID == channel.ID {
				rows[index] = channel
				return putJSON(bucket, key, rows)
			}
		}
		rows = append(rows, channel)
		return putJSON(bucket, key, rows)
	})
}

func (s *KeywordSettingsStore) SetCatalogTopics(ctx context.Context, catalog domain.SourceCatalog, ids []domain.ID, topic string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	key, err := catalogKey(catalog)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(settingsBucket)
		rows, err := catalogFromBucket(bucket, catalog, key)
		if err != nil {
			return err
		}
		positions := make(map[domain.ID]int, len(rows))
		for index := range rows {
			positions[rows[index].ID] = index
		}
		for _, id := range ids {
			if _, exists := positions[id]; !exists {
				return fmt.Errorf("%w: %s", usecase.ErrChannelNotFound, id)
			}
		}
		for _, id := range ids {
			rows[positions[id]].Topic = topic
		}
		return putJSON(bucket, key, rows)
	})
}

func OpenKeywordSettingsStore(path string) (*KeywordSettingsStore, error) {
	return openKeywordSettingsStore(path, nil)
}

func OpenKeywordSettingsStoreWithTimeout(path string, timeout time.Duration) (*KeywordSettingsStore, error) {
	return openKeywordSettingsStore(path, &bolt.Options{Timeout: timeout})
}

func openKeywordSettingsStore(path string, options *bolt.Options) (*KeywordSettingsStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, options)
	if err != nil {
		return nil, err
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(settingsBucket)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &KeywordSettingsStore{db: db}, nil
}

func (s *KeywordSettingsStore) Close() error {
	return s.db.Close()
}

func (s *KeywordSettingsStore) Load(ctx context.Context) (domain.KeywordSettings, error) {
	select {
	case <-ctx.Done():
		return domain.KeywordSettings{}, ctx.Err()
	default:
	}

	settings := domain.DefaultKeywordSettings()
	err := s.db.View(func(tx *bolt.Tx) error {
		value := tx.Bucket(settingsBucket).Get(keywordsKey)
		if len(value) == 0 {
			return nil
		}
		return json.Unmarshal(value, &settings)
	})
	if err != nil {
		return domain.KeywordSettings{}, err
	}
	settings.Keywords = normalizeKeywords(settings.Keywords)
	settings.MinusKeywords = normalizeKeywords(settings.MinusKeywords)
	settings.DirectMessageKeywords = normalizeDirectMessageKeywords(settings.DirectMessageKeywords, settings.Keywords)
	return settings, nil
}

func (s *KeywordSettingsStore) Save(ctx context.Context, settings domain.KeywordSettings) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	settings.Keywords = normalizeKeywords(settings.Keywords)
	settings.MinusKeywords = normalizeKeywords(settings.MinusKeywords)
	settings.DirectMessageKeywords = normalizeDirectMessageKeywords(settings.DirectMessageKeywords, settings.Keywords)
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(settingsBucket).Put(keywordsKey, data)
	})
}

func (s *KeywordSettingsStore) LoadChannels(ctx context.Context) ([]domain.ManagedChannel, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	channels := domain.DefaultChannels()
	err := s.db.View(func(tx *bolt.Tx) error {
		value := tx.Bucket(settingsBucket).Get(channelsKey)
		if len(value) == 0 {
			return nil
		}
		return json.Unmarshal(value, &channels)
	})
	if err != nil {
		return nil, err
	}
	return normalizeChannels(channels), nil
}

func (s *KeywordSettingsStore) SaveChannels(ctx context.Context, channels []domain.ManagedChannel) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	data, err := json.Marshal(normalizeChannels(channels))
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(settingsBucket).Put(channelsKey, data)
	})
}

func (s *KeywordSettingsStore) LoadAppSettings(ctx context.Context) (domain.AppSettings, error) {
	select {
	case <-ctx.Done():
		return domain.AppSettings{}, ctx.Err()
	default:
	}

	settings := domain.DefaultAppSettings()
	err := s.db.View(func(tx *bolt.Tx) error {
		value := tx.Bucket(settingsBucket).Get(appSettingsKey)
		if len(value) == 0 {
			return nil
		}
		return json.Unmarshal(value, &settings)
	})
	if err != nil {
		return domain.AppSettings{}, err
	}
	return normalizeAppSettings(settings), nil
}

func (s *KeywordSettingsStore) SaveAppSettings(ctx context.Context, settings domain.AppSettings) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	data, err := json.Marshal(normalizeAppSettings(settings))
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(settingsBucket).Put(appSettingsKey, data)
	})
}

func normalizeKeywords(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		keyword := strings.TrimSpace(value)
		if keyword == "" {
			continue
		}
		normalized := strings.ToLower(keyword)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, keyword)
	}
	return out
}

func normalizeDirectMessageKeywords(values, keywords []string) []string {
	if values == nil {
		return nil
	}
	enabled := make(map[string]struct{}, len(values))
	for _, value := range values {
		keyword := strings.TrimSpace(value)
		if keyword == "" {
			continue
		}
		enabled[strings.ToLower(keyword)] = struct{}{}
	}
	out := make([]string, 0, len(enabled))
	for _, keyword := range keywords {
		if _, ok := enabled[strings.ToLower(keyword)]; ok {
			out = append(out, keyword)
		}
	}
	return out
}

func normalizeChannels(values []domain.ManagedChannel) []domain.ManagedChannel {
	out := make([]domain.ManagedChannel, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		channel := value
		channel.ID = strings.TrimSpace(channel.ID)
		channel.Title = strings.TrimSpace(channel.Title)
		channel.Link = strings.TrimSpace(channel.Link)
		channel.Members = strings.TrimSpace(channel.Members)
		channel.LastActivity = strings.TrimSpace(channel.LastActivity)
		if channel.Link == "" {
			continue
		}
		normalizedLink := strings.ToLower(channel.Link)
		if _, ok := seen[normalizedLink]; ok {
			continue
		}
		seen[normalizedLink] = struct{}{}
		if channel.ID == "" {
			channel.ID = normalizedLink
		}
		if channel.Title == "" {
			channel.Title = channel.Link
		}
		if channel.Status == "" {
			channel.Status = domain.ChannelStatusReady
		}
		if channel.Members == "" {
			channel.Members = "0/3"
		}
		if channel.LastActivity == "" {
			channel.LastActivity = "—"
		}
		out = append(out, channel)
	}
	return out
}

func normalizeAppSettings(settings domain.AppSettings) domain.AppSettings {
	settings.Proxy = strings.TrimSpace(settings.Proxy)
	if settings.RepliesPerMinute < 1 {
		settings.RepliesPerMinute = domain.DefaultAppSettings().RepliesPerMinute
	}
	if settings.RepliesPerMinute > 19 {
		settings.RepliesPerMinute = 19
	}
	if settings.MinIntervalSeconds < 2 {
		settings.MinIntervalSeconds = 2
	}
	return settings
}

func defaultAccounts() []domain.Account {
	return nil
}

func accountsFromBucket(bucket *bolt.Bucket) ([]domain.Account, error) {
	value := bucket.Get(accountsKey)
	if len(value) == 0 {
		return defaultAccounts(), nil
	}
	var accounts []domain.Account
	if err := json.Unmarshal(value, &accounts); err != nil {
		return nil, err
	}
	return accounts, nil
}

func catalogKey(catalog domain.SourceCatalog) ([]byte, error) {
	switch catalog {
	case domain.SourceCatalogOutbound:
		return outboundKey, nil
	case domain.SourceCatalogScout:
		return scoutKey, nil
	default:
		return nil, errors.New("unsupported catalog")
	}
}

func catalogFromBucket(bucket *bolt.Bucket, catalog domain.SourceCatalog, key []byte) ([]domain.Channel, error) {
	value := bucket.Get(key)
	if len(value) != 0 {
		var rows []domain.Channel
		if err := json.Unmarshal(value, &rows); err != nil {
			return nil, err
		}
		return rows, nil
	}
	if catalog != domain.SourceCatalogOutbound {
		return []domain.Channel{}, nil
	}
	legacyValue := bucket.Get(channelsKey)
	channels := domain.DefaultChannels()
	if len(legacyValue) != 0 {
		if err := json.Unmarshal(legacyValue, &channels); err != nil {
			return nil, err
		}
	}
	rows := make([]domain.Channel, 0, len(channels))
	for _, channel := range normalizeChannels(channels) {
		rows = append(rows, domain.Channel{
			ID: domain.ID(channel.ID), TelegramID: channel.ID, Title: channel.Title, Link: channel.Link,
			Topic: "Без тематики", Status: channel.Status, Active: channel.Active, SentCount: int64(channel.Sent),
		})
	}
	return rows, nil
}

func putJSON(bucket *bolt.Bucket, key []byte, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return bucket.Put(key, data)
}
