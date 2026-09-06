package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"telegram-companion/internal/domain"
	proxycrypto "telegram-companion/internal/service/crypto"
)

type ProxyProfileStore struct {
	db     *sql.DB
	cipher *proxycrypto.ProxyCredentialsCipher
}

func NewProxyProfileStore(db *sql.DB, cipher *proxycrypto.ProxyCredentialsCipher) *ProxyProfileStore {
	return &ProxyProfileStore{db: db, cipher: cipher}
}

func (s *ProxyProfileStore) Save(ctx context.Context, profile domain.ProxyProfile, password *string) error {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Protocol = strings.ToLower(strings.TrimSpace(profile.Protocol))
	profile.Host = strings.ToLower(strings.TrimSpace(profile.Host))
	profile.Username = strings.TrimSpace(profile.Username)
	if strings.TrimSpace(string(profile.ID)) == "" || profile.Name == "" {
		return errors.New("proxy profile id and name are required")
	}
	if profile.Protocol != "socks5" && profile.Protocol != "http" {
		return errors.New("proxy protocol must be socks5 or http")
	}
	if profile.Host == "" || profile.Port < 1 || profile.Port > 65535 {
		return errors.New("valid proxy host and port are required")
	}
	normalizedName := strings.ToLower(profile.Name)
	normalizedEndpoint := fmt.Sprintf("%s://%s:%d", profile.Protocol, profile.Host, profile.Port)
	var duplicateID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM proxy_profiles WHERE normalized_name=? AND id<>?`, normalizedName, profile.ID).Scan(&duplicateID); err == nil {
		return errors.New("proxy profile name already exists")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM proxy_profiles WHERE normalized_endpoint=? AND id<>?`, normalizedEndpoint, profile.ID).Scan(&duplicateID); err == nil {
		return errors.New("proxy profile endpoint already exists")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var encrypted, nonce []byte
	if password == nil {
		if err := s.db.QueryRowContext(ctx, `SELECT encrypted_password,password_nonce FROM proxy_profiles WHERE id=?`, profile.ID).Scan(&encrypted, &nonce); err != nil {
			return fmt.Errorf("load existing proxy credentials: %w", err)
		}
	} else {
		var err error
		encrypted, nonce, err = s.cipher.Encrypt(ctx, *password)
		if err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	if profile.UpdatedAt.IsZero() {
		profile.UpdatedAt = now
	}
	if profile.LastHealthStatus == "" {
		profile.LastHealthStatus = "checking"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO proxy_profiles
		(id,name,normalized_name,protocol,host,port,normalized_endpoint,username,encrypted_password,password_nonce,enabled,last_health_status,last_health_at,last_error,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name,normalized_name=excluded.normalized_name,protocol=excluded.protocol,
		host=excluded.host,port=excluded.port,normalized_endpoint=excluded.normalized_endpoint,username=excluded.username,
		encrypted_password=excluded.encrypted_password,password_nonce=excluded.password_nonce,enabled=excluded.enabled,
		last_health_status=excluded.last_health_status,last_health_at=excluded.last_health_at,last_error=excluded.last_error,updated_at=excluded.updated_at`,
		profile.ID, profile.Name, normalizedName, profile.Protocol, profile.Host, profile.Port, normalizedEndpoint, profile.Username,
		encrypted, nonce, boolInt(profile.Enabled), profile.LastHealthStatus, formatTimePtr(profile.LastHealthAt), profile.LastError,
		formatTime(profile.CreatedAt), formatTime(profile.UpdatedAt))
	if err != nil {
		return fmt.Errorf("save proxy profile: %w", err)
	}
	return nil
}

func (s *ProxyProfileStore) List(ctx context.Context) ([]domain.ProxyProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,protocol,host,port,username,length(encrypted_password)>0,enabled,last_health_status,last_health_at,last_error,created_at,updated_at FROM proxy_profiles ORDER BY normalized_name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ProxyProfile
	for rows.Next() {
		var profile domain.ProxyProfile
		var id, createdAt, updatedAt string
		var configured, enabled int
		var healthAt sql.NullString
		if err := rows.Scan(&id, &profile.Name, &profile.Protocol, &profile.Host, &profile.Port, &profile.Username, &configured, &enabled,
			&profile.LastHealthStatus, &healthAt, &profile.LastError, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		profile.ID = domain.ID(id)
		profile.PasswordConfigured = configured != 0
		profile.Enabled = enabled != 0
		var parseErr error
		if profile.CreatedAt, parseErr = parseTime(createdAt); parseErr != nil {
			return nil, parseErr
		}
		if profile.UpdatedAt, parseErr = parseTime(updatedAt); parseErr != nil {
			return nil, parseErr
		}
		if healthAt.Valid {
			value, parseErr := parseTime(healthAt.String)
			if parseErr != nil {
				return nil, parseErr
			}
			profile.LastHealthAt = &value
		}
		result = append(result, profile)
	}
	return result, rows.Err()
}

func (s *ProxyProfileStore) Route(ctx context.Context, profileID domain.ID) (domain.ProxyRoute, error) {
	var route domain.ProxyRoute
	var id string
	var encrypted, nonce []byte
	var enabled int
	err := s.db.QueryRowContext(ctx, `SELECT id,name,protocol,host,port,username,encrypted_password,password_nonce,enabled FROM proxy_profiles WHERE id=?`, profileID).
		Scan(&id, &route.Name, &route.Protocol, &route.Host, &route.Port, &route.Username, &encrypted, &nonce, &enabled)
	if err != nil {
		return domain.ProxyRoute{}, err
	}
	password, err := s.cipher.Decrypt(ctx, encrypted, nonce)
	if err != nil {
		return domain.ProxyRoute{}, err
	}
	route.ID = domain.ID(id)
	route.Password = password
	route.Enabled = enabled != 0
	return route, nil
}

func (s *ProxyProfileStore) Delete(ctx context.Context, profileID domain.ID) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM proxy_profiles WHERE id=?`, profileID)
	if err != nil {
		if strings.Contains(err.Error(), "proxy profile is assigned to accounts") {
			return errors.New("proxy profile is assigned to accounts")
		}
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return errors.New("proxy profile not found")
	}
	return nil
}

func (s *ProxyProfileStore) AssignmentCounts(ctx context.Context) (map[domain.ID]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT CASE WHEN proxy_mode='global' THEN ? ELSE proxy_profile_id END,COUNT(*) FROM accounts WHERE proxy_mode IN ('global','assigned') GROUP BY proxy_mode,proxy_profile_id`, domain.SystemProxyRouteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[domain.ID]int)
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		result[domain.ID(id)] += count
	}
	return result, rows.Err()
}

func (s *ProxyProfileStore) AssignAccount(ctx context.Context, accountID domain.ID, mode domain.ProxyMode, profileID *domain.ID) error {
	switch mode {
	case domain.ProxyModeAssigned:
		if profileID == nil {
			return errors.New("assigned proxy mode requires a profile")
		}
	case domain.ProxyModeGlobal, domain.ProxyModeUnassigned:
		if profileID != nil {
			return errors.New("selected proxy mode does not accept a profile")
		}
	default:
		return errors.New("invalid proxy mode")
	}

	var result sql.Result
	var err error
	if mode == domain.ProxyModeUnassigned {
		result, err = s.db.ExecContext(ctx, `UPDATE accounts
			SET proxy_mode=?,proxy_profile_id=?,status='stopped',updated_at=? WHERE id=?`,
			mode, profileID, formatTime(time.Now().UTC()), accountID)
	} else {
		result, err = s.db.ExecContext(ctx, `UPDATE accounts
			SET proxy_mode=?,proxy_profile_id=?,updated_at=? WHERE id=?`,
			mode, profileID, formatTime(time.Now().UTC()), accountID)
	}
	if err != nil {
		if strings.Contains(err.Error(), "proxy route capacity reached") {
			return domain.ErrProxyRouteFull
		}
		return fmt.Errorf("assign account proxy: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return errors.New("account not found")
	}
	return nil
}

func (s *ProxyProfileStore) UpdateHealth(ctx context.Context, profileID domain.ID, status, errorCode string, at time.Time) error {
	switch status {
	case "ready", "checking", "degraded", "disabled", "full":
	default:
		return errors.New("invalid proxy health status")
	}
	errorCode = strings.TrimSpace(errorCode)
	switch errorCode {
	case "", "transport_failure", "retry_required", "health_check_failed":
	default:
		return errors.New("invalid proxy health error code")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE proxy_profiles SET last_health_status=?,last_health_at=?,last_error=?,updated_at=? WHERE id=?`,
		status, formatTime(at), errorCode, formatTime(at), profileID)
	if err != nil {
		return fmt.Errorf("update proxy health: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("proxy profile not found")
	}
	return nil
}

var _ domain.ProxyProfileRepository = (*ProxyProfileStore)(nil)
