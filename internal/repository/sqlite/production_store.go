package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"telegram-companion/internal/domain"
)

const appSettingsKey = "app_settings"

// ProductionStore exposes the single SQLite database through the repository
// ports used by Wails, gotd, analytics, and backup composition.
type ProductionStore struct {
	db       *sql.DB
	settings *SettingsStore
	catalogs *CatalogStore
	gateMu   sync.Mutex
	gates    map[domain.ID]*sync.Mutex
}

func NewProductionStore(db *sql.DB) *ProductionStore {
	return &ProductionStore{db: db, settings: NewSettingsStore(db), catalogs: NewCatalogStore(db), gates: make(map[domain.ID]*sync.Mutex)}
}

func (s *ProductionStore) lockAccount(accountID domain.ID) func() {
	s.gateMu.Lock()
	gate := s.gates[accountID]
	if gate == nil {
		gate = &sync.Mutex{}
		s.gates[accountID] = gate
	}
	s.gateMu.Unlock()
	gate.Lock()
	return gate.Unlock
}

func (s *ProductionStore) ReconcileDiscoveredAccounts(ctx context.Context, discovered []domain.Account) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, account := range discovered {
		rows, err := tx.QueryContext(ctx, `SELECT id,proxy_mode,proxy_profile_id FROM accounts
			WHERE session_path=? AND id<>? AND length(id)=12 AND id GLOB 'account-[0-9][0-9][0-9][0-9]'
			ORDER BY created_at,id`, account.SessionPath, account.ID)
		if err != nil {
			return err
		}
		type legacyAssignment struct {
			id        string
			mode      string
			profileID sql.NullString
		}
		var legacyAccounts []legacyAssignment
		for rows.Next() {
			var legacy legacyAssignment
			if err := rows.Scan(&legacy.id, &legacy.mode, &legacy.profileID); err != nil {
				_ = rows.Close()
				return err
			}
			legacyAccounts = append(legacyAccounts, legacy)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(legacyAccounts) == 0 {
			continue
		}
		legacy := legacyAccounts[0]
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET proxy_mode='unassigned',proxy_profile_id=NULL WHERE id=?`, legacy.id); err != nil {
			return fmt.Errorf("release legacy proxy assignment: %w", err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO accounts
			(id,phone_masked,display_name,role,status,flood_wait_until,session_path,proxy_profile_id,
			 proxy_mode,
			 public_replies_sent,private_messages_sent,next_delivery,private_messages_closed,
			 last_activity_at,last_error,created_at,updated_at,legacy_pause_review_required)
			SELECT ?, '', display_name, role, status, flood_wait_until, ?, ?, ?,
				 public_replies_sent,private_messages_sent,next_delivery,private_messages_closed,
				 last_activity_at,last_error,created_at,?,legacy_pause_review_required
			FROM accounts WHERE id=?
			ON CONFLICT(id) DO UPDATE SET phone_masked='', role=excluded.role,status=excluded.status,
			 flood_wait_until=excluded.flood_wait_until,session_path=excluded.session_path,
			 proxy_profile_id=excluded.proxy_profile_id,proxy_mode=excluded.proxy_mode,public_replies_sent=excluded.public_replies_sent,
			 private_messages_sent=excluded.private_messages_sent,next_delivery=excluded.next_delivery,
			 private_messages_closed=excluded.private_messages_closed,last_activity_at=excluded.last_activity_at,
				 last_error=excluded.last_error,updated_at=excluded.updated_at,
				 legacy_pause_review_required=excluded.legacy_pause_review_required`,
			account.ID, account.SessionPath, legacy.profileID, legacy.mode, formatTime(time.Now().UTC()), legacy.id)
		if err != nil {
			return fmt.Errorf("migrate discovered account: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE account_channel_memberships SET account_id=? WHERE account_id=?`, account.ID, legacy.id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE outgoing_message_jobs SET account_id=? WHERE account_id=?`, account.ID, legacy.id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE outgoing_message_events SET account_id=? WHERE account_id=?`, account.ID, legacy.id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM accounts WHERE id=?`, legacy.id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM accounts
		WHERE length(id)=12 AND id GLOB 'account-[0-9][0-9][0-9][0-9]'`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *ProductionStore) EnsureAccounts(ctx context.Context, accounts []domain.Account) error {
	for _, account := range accounts {
		now := time.Now().UTC()
		if account.CreatedAt.IsZero() {
			account.CreatedAt = now
		}
		if account.UpdatedAt.IsZero() {
			account.UpdatedAt = now
		}
		mode, profileID, err := persistedProxyAssignment(account)
		if err != nil {
			return err
		}
		if mode == domain.ProxyModeUnassigned {
			account.Status = domain.AccountStopped
		}
		_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO accounts
			(id, phone_masked, display_name, role, status, flood_wait_until, session_path, proxy_profile_id,
			 proxy_mode,
			 public_replies_sent, private_messages_sent, last_activity_at, last_error, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, NULL, '', ?, ?)`, account.ID, account.PhoneMasked,
			account.DisplayName, account.Role, encodeAccountStatus(account.Status), formatTimePtr(account.FloodWaitUntil), account.SessionPath,
			profileID, mode, formatTime(account.CreatedAt), formatTime(account.UpdatedAt))
		if err != nil {
			return fmt.Errorf("insert production account: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE accounts SET phone_masked=?, session_path=?, updated_at=? WHERE id=?`,
			account.PhoneMasked, account.SessionPath, formatTime(now), account.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *ProductionStore) listAccounts(ctx context.Context) ([]domain.Account, error) {
	proxyModeColumn := `'global'`
	var proxyModeColumnCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('accounts') WHERE name='proxy_mode'`).Scan(&proxyModeColumnCount); err != nil {
		return nil, fmt.Errorf("inspect account proxy columns: %w", err)
	}
	if proxyModeColumnCount == 1 {
		proxyModeColumn = `proxy_mode`
	}
	deliveryColumns := `'private', 0`
	var deliveryColumnCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('accounts')
		WHERE name IN ('next_delivery','private_messages_closed')`).Scan(&deliveryColumnCount); err != nil {
		return nil, fmt.Errorf("inspect account delivery columns: %w", err)
	}
	if deliveryColumnCount == 2 {
		deliveryColumns = `next_delivery, private_messages_closed`
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, phone_masked, display_name, role, status, flood_wait_until, session_path,
		proxy_profile_id, `+proxyModeColumn+`, public_replies_sent, private_messages_sent, `+deliveryColumns+`,
		last_activity_at, last_error,
		legacy_pause_review_required, created_at, updated_at
		FROM accounts ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Account, 0)
	for rows.Next() {
		var account domain.Account
		var id, role, status, proxyMode, nextDelivery, createdAt, updatedAt string
		var proxyID, lastActivity, floodWaitUntil sql.NullString
		if err := rows.Scan(&id, &account.PhoneMasked, &account.DisplayName, &role, &status, &floodWaitUntil, &account.SessionPath,
			&proxyID, &proxyMode, &account.PublicRepliesSent, &account.PrivateMessagesSent, &nextDelivery, &account.PrivateMessagesClosed,
			&lastActivity, &account.LastError,
			&account.LegacyPauseReviewRequired, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		account.ID = domain.ID(id)
		account.Role = domain.AccountRole(role)
		account.Status = decodeAccountStatus(status)
		account.NextDelivery = domain.DeliveryTarget(nextDelivery)
		account.ProxyMode = domain.ProxyMode(proxyMode)
		if proxyID.Valid {
			value := domain.ID(proxyID.String)
			account.ProxyProfileID = &value
		}
		if lastActivity.Valid {
			value, err := parseTime(lastActivity.String)
			if err != nil {
				return nil, err
			}
			account.LastActivityAt = &value
		}
		if floodWaitUntil.Valid {
			value, err := parseTime(floodWaitUntil.String)
			if err != nil {
				return nil, err
			}
			account.FloodWaitUntil = &value
		}
		if account.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		if account.UpdatedAt, err = parseTime(updatedAt); err != nil {
			return nil, err
		}
		result = append(result, account)
	}
	return result, rows.Err()
}

func (s *ProductionStore) listActiveAccounts(ctx context.Context) ([]domain.Account, error) {
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `UPDATE accounts SET status='ready', flood_wait_until=NULL, last_error='', updated_at=?
		WHERE status='flood_wait' AND flood_wait_until IS NOT NULL AND flood_wait_until<=?`, formatTime(now), formatTime(now)); err != nil {
		return nil, fmt.Errorf("recover expired account flood waits: %w", err)
	}
	accounts, err := s.listAccounts(ctx)
	if err != nil {
		return nil, err
	}
	active := accounts[:0]
	for _, account := range accounts {
		if account.Eligible() {
			active = append(active, account)
		}
	}
	return active, nil
}

func (s *ProductionStore) saveAccount(ctx context.Context, account domain.Account) error {
	now := time.Now().UTC()
	if account.CreatedAt.IsZero() {
		account.CreatedAt = now
	}
	if account.UpdatedAt.IsZero() {
		account.UpdatedAt = now
	}
	mode, profileID, err := persistedProxyAssignment(account)
	if err != nil {
		return err
	}
	if mode == domain.ProxyModeUnassigned {
		account.Status = domain.AccountStopped
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO accounts
		(id, phone_masked, display_name, role, status, flood_wait_until, session_path, proxy_profile_id,
		 proxy_mode,
		 public_replies_sent, private_messages_sent, next_delivery, private_messages_closed,
		 last_activity_at, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET phone_masked=excluded.phone_masked, display_name=excluded.display_name,
		role=excluded.role, status=excluded.status, flood_wait_until=excluded.flood_wait_until, session_path=excluded.session_path,
		proxy_profile_id=excluded.proxy_profile_id, proxy_mode=excluded.proxy_mode, public_replies_sent=excluded.public_replies_sent,
		private_messages_sent=excluded.private_messages_sent, next_delivery=excluded.next_delivery,
		private_messages_closed=excluded.private_messages_closed, last_activity_at=excluded.last_activity_at,
		last_error=excluded.last_error, updated_at=excluded.updated_at`,
		account.ID, account.PhoneMasked, account.DisplayName, account.Role, encodeAccountStatus(account.Status), formatTimePtr(account.FloodWaitUntil), account.SessionPath,
		profileID, mode, account.PublicRepliesSent, account.PrivateMessagesSent, account.EffectiveNextDelivery(), account.PrivateMessagesClosed,
		formatTimePtr(account.LastActivityAt),
		account.LastError, formatTime(account.CreatedAt), formatTime(account.UpdatedAt))
	if err != nil {
		return fmt.Errorf("save account: %w", err)
	}
	return nil
}

func persistedProxyAssignment(account domain.Account) (domain.ProxyMode, *domain.ID, error) {
	switch account.ProxyMode {
	case "", domain.ProxyModeDirect, domain.ProxyModeGlobal:
		return domain.ProxyModeGlobal, nil, nil
	case domain.ProxyModeUnassigned:
		return domain.ProxyModeUnassigned, nil, nil
	case domain.ProxyModeAssigned:
		if account.ProxyProfileID == nil {
			return "", nil, errors.New("assigned proxy mode requires a profile")
		}
		return domain.ProxyModeAssigned, account.ProxyProfileID, nil
	default:
		return "", nil, errors.New("invalid proxy mode")
	}
}

func (s *ProductionStore) MarkValidated(ctx context.Context, accountID domain.ID, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE accounts SET status='ready', status_source='runtime', legacy_pause_review_required=0,
		flood_wait_until=NULL, last_error='', last_activity_at=?, updated_at=? WHERE id=?`, formatTime(at), formatTime(at), accountID)
	if err != nil {
		return fmt.Errorf("mark account validated: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("validated account not found")
	}
	return nil
}

func (s *ProductionStore) ListAccounts(ctx context.Context) ([]domain.Account, error) {
	return s.listAccounts(ctx)
}
func (s *ProductionStore) SaveAccount(ctx context.Context, account domain.Account) error {
	return s.saveAccount(ctx, account)
}

func (s *ProductionStore) SetAccountRole(ctx context.Context, accountID domain.ID, role domain.AccountRole, at time.Time) (domain.Account, error) {
	unlock := s.lockAccount(accountID)
	defer unlock()
	result, err := s.db.ExecContext(ctx, `UPDATE accounts SET role=?, updated_at=? WHERE id=?`, role, formatTime(at), accountID)
	if err != nil {
		return domain.Account{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return domain.Account{}, err
	}
	if affected != 1 {
		return domain.Account{}, errors.New("account not found")
	}
	accounts, err := s.listAccounts(ctx)
	if err != nil {
		return domain.Account{}, err
	}
	for _, account := range accounts {
		if account.ID == accountID {
			return account, nil
		}
	}
	return domain.Account{}, errors.New("account not found")
}
func (s *ProductionStore) Load(ctx context.Context) (domain.KeywordSettings, error) {
	return s.settings.Load(ctx)
}
func (s *ProductionStore) Save(ctx context.Context, settings domain.KeywordSettings) error {
	return s.settings.Save(ctx, settings)
}

func (s *ProductionStore) LoadAppSettings(ctx context.Context) (domain.AppSettings, error) {
	settings := domain.DefaultAppSettings()
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key=?`, appSettingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return domain.AppSettings{}, err
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return domain.AppSettings{}, err
	}
	return settings.Normalized(), nil
}

func (s *ProductionStore) SaveAppSettings(ctx context.Context, settings domain.AppSettings) error {
	settings = settings.Normalized()
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO app_settings (key,value_json,revision,updated_at) VALUES (?, ?, 1, ?)
		ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json, revision=app_settings.revision+1, updated_at=excluded.updated_at`,
		appSettingsKey, string(raw), formatTime(time.Now().UTC()))
	return err
}

func (s *ProductionStore) SaveCatalog(ctx context.Context, catalog domain.SourceCatalog, channel domain.Channel) error {
	return s.catalogs.Save(ctx, catalog, channel)
}

func (s *ProductionStore) ActivateCatalogWithMemberships(ctx context.Context, catalog domain.SourceCatalog, channel domain.Channel, memberships []domain.ChannelMembership) error {
	for _, membership := range memberships {
		if membership.ChannelID != channel.ID {
			return fmt.Errorf("membership channel %q does not match activated channel %q", membership.ChannelID, channel.ID)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.catalogs.save(ctx, tx, catalog, channel); err != nil {
		return err
	}
	for _, membership := range memberships {
		if err := s.saveCatalogMembership(ctx, tx, catalog, membership); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *ProductionStore) ListCatalog(ctx context.Context, catalog domain.SourceCatalog) ([]domain.Channel, error) {
	return s.catalogs.List(ctx, catalog)
}

func (s *ProductionStore) SetCatalogTopics(ctx context.Context, catalog domain.SourceCatalog, ids []domain.ID, topic string) error {
	table, _, err := catalogTable(catalog)
	if err != nil {
		return err
	}
	for _, id := range ids {
		result, err := s.db.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET topic=?, updated_at=? WHERE id=?`, table), topic, formatTime(time.Now().UTC()), id)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return fmt.Errorf("catalog channel %s not found", id)
		}
	}
	return nil
}

func (s *ProductionStore) RequestCatalogRemoval(ctx context.Context, catalog domain.SourceCatalog, ids []domain.ID) error {
	table, _, err := catalogTable(catalog)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := formatTime(time.Now().UTC())
	for _, id := range ids {
		result, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s
			SET active=0,status='paused',removal_requested_at=COALESCE(removal_requested_at,?),updated_at=? WHERE id=?`, table), now, now, id)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return fmt.Errorf("catalog channel %s not found", id)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE outgoing_message_jobs
			SET status='done',last_error='channel_removal_requested',lease_token='',lease_until=NULL
			WHERE channel_id=? AND status IN ('queued','delayed')`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RequestCatalogLeave keeps the catalog row while turning every remaining
// membership into an explicit exit intent. A join that was never submitted is
// removed locally because Telegram has nothing to cancel.
func (s *ProductionStore) RequestCatalogLeave(ctx context.Context, catalog domain.SourceCatalog, channelID domain.ID) error {
	table, _, err := catalogTable(catalog)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s
		SET active=0,status=?,updated_at=? WHERE id=?`, table),
		domain.ChannelPaused, formatTime(time.Now().UTC()), channelID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New("catalog channel not found")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_channel_memberships
		WHERE catalog=? AND channel_id=? AND status='joining' AND is_member=0 AND request_submitted_at IS NULL`, catalog, channelID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_channel_memberships
		SET status='leaving',join_not_before=NULL,last_error=''
		WHERE catalog=? AND channel_id=?`, catalog, channelID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *ProductionStore) RetryPendingCatalogMemberships(ctx context.Context, catalog domain.SourceCatalog, channelID domain.ID, retryAt time.Time) (int, error) {
	table, _, err := catalogTable(catalog)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `UPDATE account_channel_memberships
		SET is_member=0,status='joining',last_check_at=NULL,request_submitted_at=NULL,
			join_not_before=NULL,last_error=''
		WHERE catalog=? AND channel_id=? AND is_member=0 AND status='pending_approval'`, catalog, channelID)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if affected == 0 {
		return 0, tx.Commit()
	}
	channelResult, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s
		SET status=?,updated_at=? WHERE id=? AND removal_requested_at IS NULL`, table),
		domain.ChannelJoining, formatTime(retryAt.UTC()), channelID)
	if err != nil {
		return 0, err
	}
	channelAffected, err := channelResult.RowsAffected()
	if err != nil {
		return 0, err
	}
	if channelAffected != 1 {
		return 0, errors.New("catalog channel not found or being removed")
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(affected), nil
}

func (s *ProductionStore) DeleteMembership(ctx context.Context, catalog domain.SourceCatalog, accountID, channelID domain.ID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM account_channel_memberships WHERE account_id=? AND catalog=? AND channel_id=?`, accountID, catalog, channelID)
	return err
}

func (s *ProductionStore) CompleteCatalogRemoval(ctx context.Context, catalog domain.SourceCatalog, channelID domain.ID) error {
	table, _, err := catalogTable(catalog)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var result sql.Result
	if catalog == domain.SourceCatalogScout {
		now := formatTime(time.Now().UTC())
		result, err = tx.ExecContext(ctx, `UPDATE scout_chats
			SET active=1,status='paused',archived_at=?,updated_at=?
			WHERE id=? AND removal_requested_at IS NOT NULL AND archived_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM account_channel_memberships WHERE catalog=? AND channel_id=?)`,
			now, now, channelID, catalog, channelID)
	} else {
		result, err = tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s
			WHERE id=? AND removal_requested_at IS NOT NULL
			AND NOT EXISTS (SELECT 1 FROM account_channel_memberships WHERE catalog=? AND channel_id=?)`, table), channelID, catalog, channelID)
	}
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected > 1 {
		if err != nil {
			return err
		}
		return errors.New("unexpected catalog removal count")
	}
	if affected == 0 {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_channel_memberships WHERE catalog=? AND channel_id=?`, catalog, channelID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *ProductionStore) LoadChannels(ctx context.Context) ([]domain.ManagedChannel, error) {
	rows, err := s.catalogs.List(ctx, domain.SourceCatalogOutbound)
	if err != nil {
		return nil, err
	}
	result := make([]domain.ManagedChannel, 0, len(rows))
	for _, row := range rows {
		result = append(result, domain.ManagedChannel{ID: string(row.ID), Title: row.Title, Link: row.Link, Status: row.Status, Sent: int(row.SentCount), Active: row.Active})
	}
	return result, nil
}

func (s *ProductionStore) SaveChannels(ctx context.Context, rows []domain.ManagedChannel) error {
	for _, row := range rows {
		now := time.Now().UTC()
		if err := s.catalogs.Save(ctx, domain.SourceCatalogOutbound, domain.Channel{ID: domain.ID(row.ID), TelegramID: row.ID, Title: row.Title, Link: row.Link, Topic: "Uncategorized", Status: row.Status, Active: row.Active, SentCount: int64(row.Sent), CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
	}
	return nil
}

func (s *ProductionStore) saveMembership(ctx context.Context, membership domain.ChannelMembership) error {
	catalog := domain.SourceCatalogOutbound
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scout_chats WHERE id=?)`, membership.ChannelID).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		catalog = domain.SourceCatalogScout
	}
	return s.SaveMembership(ctx, catalog, membership)
}

func (s *ProductionStore) ListMemberships(ctx context.Context, catalog domain.SourceCatalog, channelID domain.ID) ([]domain.ChannelMembership, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT account_id,is_member,status,last_check_at,request_submitted_at,joined_at,join_not_before,
			rest_started_at,rest_until,rest_duration_hours,last_error
		FROM account_channel_memberships WHERE catalog=? AND channel_id=? ORDER BY account_id`, catalog, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	memberships := make([]domain.ChannelMembership, 0)
	for rows.Next() {
		var membership domain.ChannelMembership
		var isMember int
		var lastCheck, requestSubmitted, joinedAt, joinNotBefore, restStartedAt, restUntil sql.NullString
		if err := rows.Scan(
			&membership.AccountID, &isMember, &membership.Status, &lastCheck, &requestSubmitted, &joinedAt, &joinNotBefore,
			&restStartedAt, &restUntil, &membership.RestDurationHours, &membership.LastError,
		); err != nil {
			return nil, err
		}
		membership.ChannelID = channelID
		membership.IsMember = isMember != 0
		if lastCheck.Valid {
			value, err := parseTime(lastCheck.String)
			if err != nil {
				return nil, err
			}
			membership.LastCheckAt = &value
		}
		if requestSubmitted.Valid {
			value, err := parseTime(requestSubmitted.String)
			if err != nil {
				return nil, err
			}
			membership.RequestSubmittedAt = &value
		}
		if joinedAt.Valid {
			value, err := parseTime(joinedAt.String)
			if err != nil {
				return nil, err
			}
			membership.JoinedAt = &value
		}
		if joinNotBefore.Valid {
			value, err := parseTime(joinNotBefore.String)
			if err != nil {
				return nil, err
			}
			membership.JoinNotBefore = &value
		}
		if restStartedAt.Valid {
			value, err := parseTime(restStartedAt.String)
			if err != nil {
				return nil, err
			}
			membership.RestStartedAt = &value
		}
		if restUntil.Valid {
			value, err := parseTime(restUntil.String)
			if err != nil {
				return nil, err
			}
			membership.RestUntil = &value
		}
		memberships = append(memberships, membership)
	}
	return memberships, rows.Err()
}

func (s *ProductionStore) SaveMembership(ctx context.Context, catalog domain.SourceCatalog, membership domain.ChannelMembership) error {
	return s.saveCatalogMembership(ctx, nil, catalog, membership)
}

func (s *ProductionStore) saveCatalogMembership(ctx context.Context, tx *sql.Tx, catalog domain.SourceCatalog, membership domain.ChannelMembership) error {
	var restStartedAt, restUntil *time.Time
	if membership.JoinedAt != nil && domain.ValidateGroupRestHours(membership.RestDurationHours) == nil {
		startedAt := membership.JoinedAt.UTC()
		until := startedAt.Add(time.Duration(membership.RestDurationHours) * time.Hour)
		restStartedAt = &startedAt
		restUntil = &until
	}
	query := `INSERT INTO account_channel_memberships
		(account_id,catalog,channel_id,is_member,status,last_check_at,request_submitted_at,joined_at,join_not_before,
		 rest_started_at,rest_until,rest_duration_hours,last_error) VALUES (?,?,?,?,?,?,?,?,CASE
			WHEN COALESCE((SELECT json_extract(value_json,'$.joinIntervalEnabled') FROM app_settings WHERE key='app_settings'),1)
			THEN ? ELSE NULL END,CASE
			WHEN COALESCE((SELECT json_extract(value_json,'$.groupRestEnabled') FROM app_settings WHERE key='app_settings'),1)
			THEN ? ELSE NULL END,CASE
			WHEN COALESCE((SELECT json_extract(value_json,'$.groupRestEnabled') FROM app_settings WHERE key='app_settings'),1)
			THEN ? ELSE NULL END,CASE
			WHEN COALESCE((SELECT json_extract(value_json,'$.groupRestEnabled') FROM app_settings WHERE key='app_settings'),1)
			THEN ? ELSE 0 END,?)
		ON CONFLICT(account_id,catalog,channel_id) DO UPDATE SET
			is_member=excluded.is_member,
			status=excluded.status,
			last_check_at=excluded.last_check_at,
			request_submitted_at=COALESCE(account_channel_memberships.request_submitted_at,excluded.request_submitted_at),
			joined_at=COALESCE(account_channel_memberships.joined_at,excluded.joined_at),
			join_not_before=CASE
				WHEN COALESCE((SELECT json_extract(value_json,'$.joinIntervalEnabled') FROM app_settings WHERE key='app_settings'),1)
				THEN COALESCE(account_channel_memberships.join_not_before,excluded.join_not_before)
				ELSE NULL
			END,
			rest_started_at=CASE
				WHEN NOT COALESCE((SELECT json_extract(value_json,'$.groupRestEnabled') FROM app_settings WHERE key='app_settings'),1)
				THEN NULL
				WHEN account_channel_memberships.joined_at IS NULL
					AND excluded.joined_at IS NOT NULL
					AND account_channel_memberships.rest_duration_hours BETWEEN 1 AND 720
					AND excluded.rest_duration_hours=account_channel_memberships.rest_duration_hours
				THEN excluded.rest_started_at
				ELSE account_channel_memberships.rest_started_at
			END,
			rest_until=CASE
				WHEN NOT COALESCE((SELECT json_extract(value_json,'$.groupRestEnabled') FROM app_settings WHERE key='app_settings'),1)
				THEN NULL
				WHEN account_channel_memberships.joined_at IS NULL
					AND excluded.joined_at IS NOT NULL
					AND account_channel_memberships.rest_duration_hours BETWEEN 1 AND 720
					AND excluded.rest_duration_hours=account_channel_memberships.rest_duration_hours
				THEN excluded.rest_until
				ELSE account_channel_memberships.rest_until
			END,
			rest_duration_hours=CASE
				WHEN COALESCE((SELECT json_extract(value_json,'$.groupRestEnabled') FROM app_settings WHERE key='app_settings'),1)
				THEN account_channel_memberships.rest_duration_hours
				ELSE 0
			END,
			last_error=excluded.last_error`
	args := []any{
		membership.AccountID, catalog, membership.ChannelID, boolInt(membership.IsMember), membership.Status,
		formatTimePtr(membership.LastCheckAt), formatTimePtr(membership.RequestSubmittedAt), formatTimePtr(membership.JoinedAt),
		formatTimePtr(membership.JoinNotBefore), formatTimePtr(restStartedAt), formatTimePtr(restUntil),
		membership.RestDurationHours, membership.LastError,
	}
	var err error
	if tx == nil {
		_, err = s.db.ExecContext(ctx, query, args...)
	} else {
		_, err = tx.ExecContext(ctx, query, args...)
	}
	return err
}

func (s *ProductionStore) ListAccountGroupRests(ctx context.Context) ([]domain.AccountGroupRest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
			m.account_id,
			COALESCE(NULLIF(a.display_name,''),NULLIF(a.phone_masked,''),a.id),
			m.channel_id,
			COALESCE(CASE m.catalog
				WHEN 'outbound' THEN COALESCE(NULLIF(o.title,''),o.link,o.id)
				ELSE COALESCE(NULLIF(sc.title,''),sc.link,sc.id)
			END,m.channel_id),
			m.catalog,m.rest_started_at,m.rest_until,m.rest_duration_hours
		FROM account_channel_memberships m
		JOIN accounts a ON a.id=m.account_id
		LEFT JOIN outbound_channels o ON m.catalog='outbound' AND o.id=m.channel_id
		LEFT JOIN scout_chats sc ON m.catalog='scout' AND sc.id=m.channel_id
		WHERE m.rest_started_at IS NOT NULL AND m.rest_until IS NOT NULL
		ORDER BY m.rest_until DESC,m.account_id,m.catalog,m.channel_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rests := make([]domain.AccountGroupRest, 0)
	for rows.Next() {
		var rest domain.AccountGroupRest
		var catalog, startedAt, until string
		if err := rows.Scan(
			&rest.AccountID, &rest.AccountTitle, &rest.ChannelID, &rest.ChannelTitle,
			&catalog, &startedAt, &until, &rest.DurationHours,
		); err != nil {
			return nil, err
		}
		rest.Catalog = domain.SourceCatalog(catalog)
		rest.StartedAt, err = parseTime(startedAt)
		if err != nil {
			return nil, err
		}
		rest.Until, err = parseTime(until)
		if err != nil {
			return nil, err
		}
		rests = append(rests, rest)
	}
	return rests, rows.Err()
}

func (s *ProductionStore) ActiveGroupRestUntil(ctx context.Context, accountIDs []domain.ID, now time.Time) (map[domain.ID]time.Time, error) {
	active := make(map[domain.ID]time.Time)
	if len(accountIDs) == 0 {
		return active, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(accountIDs)), ",")
	args := make([]any, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		args = append(args, accountID)
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT account_id,rest_until
		FROM account_channel_memberships
		WHERE account_id IN (%s) AND rest_until IS NOT NULL`, placeholders), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var accountID domain.ID
		var until string
		if err := rows.Scan(&accountID, &until); err != nil {
			return nil, err
		}
		value, err := parseTime(until)
		if err != nil {
			return nil, err
		}
		if !value.After(now) {
			continue
		}
		if latest, found := active[accountID]; !found || value.After(latest) {
			active[accountID] = value
		}
	}
	return active, rows.Err()
}

// ClearGroupRests atomically removes all membership rest state and makes jobs
// delayed only by account group rest eligible for delivery again.
func (s *ProductionStore) ClearGroupRests(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin clear group rests: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `UPDATE account_channel_memberships
		SET rest_started_at=NULL,rest_until=NULL,rest_duration_hours=0
		WHERE rest_started_at IS NOT NULL OR rest_until IS NOT NULL OR rest_duration_hours<>0`); err != nil {
		return fmt.Errorf("clear membership group rests: %w", err)
	}
	if err := releaseGroupRestDelays(ctx, tx, time.Now().UTC()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit clear group rests: %w", err)
	}
	return nil
}

// ClearJoinIntervals makes every queued join immediately eligible while
// preserving membership and moderation history.
func (s *ProductionStore) ClearJoinIntervals(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE account_channel_memberships
		SET join_not_before=NULL
		WHERE join_not_before IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("clear join intervals: %w", err)
	}
	return nil
}

type AccountRepository struct{ store *ProductionStore }

func (s *ProductionStore) Accounts() *AccountRepository { return &AccountRepository{store: s} }
func (s *ProductionStore) ResumeLegacyPaused(ctx context.Context, accountID domain.ID, at time.Time) (domain.Account, error) {
	return s.Accounts().ResumeLegacyPaused(ctx, accountID, at)
}
func (r *AccountRepository) List(ctx context.Context) ([]domain.Account, error) {
	return r.store.listAccounts(ctx)
}
func (r *AccountRepository) ListActive(ctx context.Context) ([]domain.Account, error) {
	return r.store.listActiveAccounts(ctx)
}
func (r *AccountRepository) Save(ctx context.Context, account domain.Account) error {
	return r.store.saveAccount(ctx, account)
}
func (r *AccountRepository) ResumeLegacyPaused(ctx context.Context, accountID domain.ID, at time.Time) (domain.Account, error) {
	unlock := r.store.lockAccount(accountID)
	defer unlock()
	result, err := r.store.db.ExecContext(ctx, `UPDATE accounts SET
		status=CASE WHEN flood_wait_until IS NOT NULL AND flood_wait_until>? THEN 'flood_wait' ELSE 'stopped' END,
		status_source='runtime', legacy_pause_review_required=0,
		last_error=CASE WHEN flood_wait_until IS NOT NULL AND flood_wait_until>? THEN 'telegram flood wait' ELSE '' END,
		updated_at=? WHERE id=? AND status='paused' AND legacy_pause_review_required=1`,
		formatTime(at), formatTime(at), formatTime(at), accountID)
	if err != nil {
		return domain.Account{}, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return domain.Account{}, err
		}
		return domain.Account{}, errors.New("legacy paused account is not awaiting review")
	}
	accounts, err := r.store.listAccounts(ctx)
	if err != nil {
		return domain.Account{}, err
	}
	for _, account := range accounts {
		if account.ID == accountID {
			return account, nil
		}
	}
	return domain.Account{}, errors.New("account not found")
}
func (r *AccountRepository) WithAccountLease(ctx context.Context, accountID domain.ID, action func(domain.Account) error) error {
	unlock := r.store.lockAccount(accountID)
	defer unlock()
	accounts, err := r.store.listAccounts(ctx)
	if err != nil {
		return err
	}
	for _, account := range accounts {
		if account.ID == accountID {
			return action(account)
		}
	}
	return errors.New("outbound account not found")
}
func (r *AccountRepository) RecordFloodWait(ctx context.Context, accountID domain.ID, until, at time.Time) error {
	result, err := r.store.db.ExecContext(ctx, `UPDATE accounts SET status='flood_wait', status_source='runtime', legacy_pause_review_required=0, flood_wait_until=?,
		last_error='telegram flood wait', updated_at=? WHERE id=?`, formatTime(until), formatTime(at), accountID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return errors.New("outbound account not found")
	}
	return nil
}
func (r *AccountRepository) RecordRuntimeStatus(ctx context.Context, accountID domain.ID, status, errorCode string, at time.Time) error {
	overrideActiveFloodWait := 0
	if status == string(domain.AccountError) && errorCode == "rpc_auth_key_duplicated" {
		overrideActiveFloodWait = 1
	}
	result, err := r.store.db.ExecContext(ctx, `UPDATE accounts SET
		status=CASE WHEN ?=0 AND status='flood_wait' AND flood_wait_until>? THEN status ELSE ? END,
		status_source='runtime',
		legacy_pause_review_required=0,
		last_error=CASE WHEN ?=0 AND status='flood_wait' AND flood_wait_until>? THEN last_error ELSE ? END,
		flood_wait_until=CASE WHEN ?=1 THEN NULL ELSE flood_wait_until END,
		updated_at=? WHERE id=?`,
		overrideActiveFloodWait, formatTime(at), status,
		overrideActiveFloodWait, formatTime(at), errorCode,
		overrideActiveFloodWait, formatTime(at), accountID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return errors.New("runtime account not found")
	}
	return nil
}

type CatalogRepository struct{ store *ProductionStore }

func (s *ProductionStore) Catalogs() *CatalogRepository { return &CatalogRepository{store: s} }
func (r *CatalogRepository) List(ctx context.Context, catalog domain.SourceCatalog) ([]domain.Channel, error) {
	return r.store.catalogs.List(ctx, catalog)
}
func (r *CatalogRepository) Save(ctx context.Context, catalog domain.SourceCatalog, channel domain.Channel) error {
	return r.store.catalogs.Save(ctx, catalog, channel)
}
func (r *CatalogRepository) LoadMembership(ctx context.Context, accountID domain.ID, catalog domain.SourceCatalog, channelID domain.ID) (domain.ChannelMembership, bool, error) {
	var membership domain.ChannelMembership
	var isMember int
	var lastCheck, requestSubmitted, joinedAt, joinNotBefore, restStartedAt, restUntil sql.NullString
	err := r.store.db.QueryRowContext(ctx, `SELECT is_member,status,last_check_at,request_submitted_at,joined_at,join_not_before,
			rest_started_at,rest_until,rest_duration_hours,last_error
		FROM account_channel_memberships WHERE account_id=? AND catalog=? AND channel_id=?`,
		accountID, catalog, channelID).Scan(
		&isMember, &membership.Status, &lastCheck, &requestSubmitted, &joinedAt, &joinNotBefore,
		&restStartedAt, &restUntil, &membership.RestDurationHours, &membership.LastError,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChannelMembership{}, false, nil
	}
	if err != nil {
		return domain.ChannelMembership{}, false, err
	}
	membership.AccountID = accountID
	membership.ChannelID = channelID
	membership.IsMember = isMember != 0
	if lastCheck.Valid {
		value, err := parseTime(lastCheck.String)
		if err != nil {
			return domain.ChannelMembership{}, false, err
		}
		membership.LastCheckAt = &value
	}
	if requestSubmitted.Valid {
		value, err := parseTime(requestSubmitted.String)
		if err != nil {
			return domain.ChannelMembership{}, false, err
		}
		membership.RequestSubmittedAt = &value
	}
	if joinedAt.Valid {
		value, err := parseTime(joinedAt.String)
		if err != nil {
			return domain.ChannelMembership{}, false, err
		}
		membership.JoinedAt = &value
	}
	if joinNotBefore.Valid {
		value, err := parseTime(joinNotBefore.String)
		if err != nil {
			return domain.ChannelMembership{}, false, err
		}
		membership.JoinNotBefore = &value
	}
	if restStartedAt.Valid {
		value, err := parseTime(restStartedAt.String)
		if err != nil {
			return domain.ChannelMembership{}, false, err
		}
		membership.RestStartedAt = &value
	}
	if restUntil.Valid {
		value, err := parseTime(restUntil.String)
		if err != nil {
			return domain.ChannelMembership{}, false, err
		}
		membership.RestUntil = &value
	}
	return membership, true, nil
}
func (r *CatalogRepository) SaveMembership(ctx context.Context, catalog domain.SourceCatalog, membership domain.ChannelMembership) error {
	return r.store.SaveMembership(ctx, catalog, membership)
}
func (r *CatalogRepository) DeleteMembership(ctx context.Context, catalog domain.SourceCatalog, accountID, channelID domain.ID) error {
	return r.store.DeleteMembership(ctx, catalog, accountID, channelID)
}
func (r *CatalogRepository) CompleteCatalogRemoval(ctx context.Context, catalog domain.SourceCatalog, channelID domain.ID) error {
	return r.store.CompleteCatalogRemoval(ctx, catalog, channelID)
}
func (r *CatalogRepository) RequestCatalogRemoval(ctx context.Context, catalog domain.SourceCatalog, ids []domain.ID) error {
	return r.store.RequestCatalogRemoval(ctx, catalog, ids)
}

type ChannelRepository struct{ store *ProductionStore }

func (s *ProductionStore) Channels() *ChannelRepository { return &ChannelRepository{store: s} }
func (r *ChannelRepository) List(ctx context.Context) ([]domain.Channel, error) {
	return r.store.catalogs.List(ctx, domain.SourceCatalogOutbound)
}
func (r *ChannelRepository) ListActive(ctx context.Context) ([]domain.Channel, error) {
	rows, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	active := rows[:0]
	for _, row := range rows {
		if row.Active {
			active = append(active, row)
		}
	}
	return active, nil
}
func (r *ChannelRepository) Save(ctx context.Context, channel domain.Channel) error {
	return r.store.catalogs.Save(ctx, domain.SourceCatalogOutbound, channel)
}
func (r *ChannelRepository) SaveMembership(ctx context.Context, membership domain.ChannelMembership) error {
	return r.store.saveMembership(ctx, membership)
}

func encodeAccountStatus(status domain.AccountStatus) string {
	switch status {
	case domain.AccountActive:
		return "ready"
	case domain.AccountLimited:
		return "partial"
	default:
		return string(status)
	}
}

func decodeAccountStatus(status string) domain.AccountStatus {
	switch status {
	case "ready", "collecting":
		return domain.AccountActive
	default:
		return domain.AccountStatus(status)
	}
}
