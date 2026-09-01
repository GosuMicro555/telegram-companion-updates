package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
	proxycrypto "telegram-companion/internal/service/crypto"
)

func TestProxyProfileStoreEncryptsCredentialsAndSupportsCRUD(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	store := newTestProxyProfileStore(t, db)
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	password := "secret-proxy-password"
	profile := domain.ProxyProfile{
		ID: "proxy-a", Name: "  Primary Route  ", Protocol: "SOCKS5",
		Host: " Proxy.Example.COM ", Port: 1080, Username: "alice", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}

	require.NoError(t, store.Save(ctx, profile, &password))

	var encrypted, nonce []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT encrypted_password,password_nonce FROM proxy_profiles WHERE id=?`, profile.ID).Scan(&encrypted, &nonce))
	require.NotEmpty(t, encrypted)
	require.NotEmpty(t, nonce)
	require.NotContains(t, string(encrypted), password)

	profiles, err := store.List(ctx)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	require.Equal(t, "Primary Route", profiles[0].Name)
	require.Equal(t, "socks5", profiles[0].Protocol)
	require.Equal(t, "proxy.example.com", profiles[0].Host)
	require.True(t, profiles[0].PasswordConfigured)

	route, err := store.Route(ctx, profile.ID)
	require.NoError(t, err)
	require.Equal(t, password, route.Password)
	require.Equal(t, "proxy.example.com", route.Host)

	profiles[0].Username = "updated"
	profiles[0].UpdatedAt = now.Add(time.Minute)
	require.NoError(t, store.Save(ctx, profiles[0], nil))
	route, err = store.Route(ctx, profile.ID)
	require.NoError(t, err)
	require.Equal(t, "updated", route.Username)
	require.Equal(t, password, route.Password)

	require.NoError(t, store.Delete(ctx, profile.ID))
	profiles, err = store.List(ctx)
	require.NoError(t, err)
	require.Empty(t, profiles)
}

func TestProxyProfileStoreRejectsDuplicateNormalizedNameAndEndpoint(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	store := newTestProxyProfileStore(t, db)
	empty := ""
	now := time.Now().UTC()
	require.NoError(t, store.Save(ctx, domain.ProxyProfile{ID: "one", Name: "Office", Protocol: "http", Host: "Proxy.Example", Port: 8080, Enabled: true, CreatedAt: now, UpdatedAt: now}, &empty))

	err = store.Save(ctx, domain.ProxyProfile{ID: "two", Name: " office ", Protocol: "socks5", Host: "other.example", Port: 1080, Enabled: true, CreatedAt: now, UpdatedAt: now}, &empty)
	require.ErrorContains(t, err, "name")
	err = store.Save(ctx, domain.ProxyProfile{ID: "three", Name: "Other", Protocol: "HTTP", Host: " proxy.example ", Port: 8080, Enabled: true, CreatedAt: now, UpdatedAt: now}, &empty)
	require.ErrorContains(t, err, "endpoint")
}

func TestProxyProfileStoreUpdatesSanitizedHealth(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	store := newTestProxyProfileStore(t, db)
	now := time.Date(2026, 7, 16, 11, 0, 0, 0, time.UTC)
	empty := ""
	require.NoError(t, store.Save(ctx, domain.ProxyProfile{ID: "health", Name: "Health", Protocol: "http", Host: "127.0.0.1", Port: 8080, Enabled: true, CreatedAt: now, UpdatedAt: now}, &empty))

	require.NoError(t, store.UpdateHealth(ctx, "health", "degraded", "health_check_failed", now.Add(time.Minute)))
	profiles, err := store.List(ctx)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	require.Equal(t, "degraded", profiles[0].LastHealthStatus)
	require.Equal(t, "health_check_failed", profiles[0].LastError)
	require.Equal(t, now.Add(time.Minute), *profiles[0].LastHealthAt)
}

func TestProxyProfileStoreRejectsUnsafeHealthErrors(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	store := newTestProxyProfileStore(t, db)
	now := time.Date(2026, 7, 16, 11, 0, 0, 0, time.UTC)
	empty := ""
	require.NoError(t, store.Save(ctx, domain.ProxyProfile{ID: "health", Name: "Health", Protocol: "http", Host: "127.0.0.1", Port: 8080, Enabled: true, CreatedAt: now, UpdatedAt: now}, &empty))

	for _, errorCode := range []string{
		"unexpected proxy handshake failure",
		"http://alice:secret@example.com:8080",
		"socks5://bob:password@127.0.0.1:1080",
	} {
		require.Error(t, store.UpdateHealth(ctx, "health", "degraded", errorCode, now.Add(time.Minute)))
	}

	var lastError string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT last_error FROM proxy_profiles WHERE id=?`, "health").Scan(&lastError))
	require.Empty(t, lastError)
}

func TestProxyProfileStoreCustomRouteCapacityEnforcesTenAssignmentsAtomically(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	store := newTestProxyProfileStore(t, db)
	now := time.Now().UTC()
	empty := ""
	require.NoError(t, store.Save(ctx, domain.ProxyProfile{ID: "custom", Name: "Custom", Protocol: "socks5", Host: "127.0.0.1", Port: 1080, Enabled: true, CreatedAt: now, UpdatedAt: now}, &empty))
	for i := 0; i < 11; i++ {
		account := domain.Account{ID: domain.ID("account-" + string(rune('a'+i))), Role: domain.AccountRoleSpammer, Status: domain.AccountPaused, SessionPath: "/session", ProxyMode: domain.ProxyModeUnassigned, CreatedAt: now, UpdatedAt: now}
		require.NoError(t, NewProductionStore(db).Accounts().Save(ctx, account))
	}

	profileID := domain.ID("custom")
	var wg sync.WaitGroup
	results := make(chan error, 11)
	for i := 0; i < 11; i++ {
		wg.Add(1)
		go func(accountID domain.ID) {
			defer wg.Done()
			results <- store.AssignAccount(ctx, accountID, domain.ProxyModeAssigned, &profileID)
		}(domain.ID("account-" + string(rune('a'+i))))
	}
	wg.Wait()
	close(results)
	var successes, full int
	for err := range results {
		switch err {
		case nil:
			successes++
		case domain.ErrProxyRouteFull:
			full++
		default:
			require.NoError(t, err)
		}
	}
	require.Equal(t, 10, successes)
	require.Equal(t, 1, full)

	counts, err := store.AssignmentCounts(ctx)
	require.NoError(t, err)
	require.Equal(t, 10, counts[profileID])

	err = store.AssignAccount(ctx, "account-a", domain.ProxyModeAssigned, nil)
	require.Error(t, err)
	err = store.AssignAccount(ctx, "account-a", domain.ProxyModeGlobal, &profileID)
	require.Error(t, err)
	err = store.AssignAccount(ctx, "account-a", domain.ProxyModeUnassigned, &profileID)
	require.Error(t, err)
}

func TestProxyProfileStoreUnlimitedSystemAssignments(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	store := newTestProxyProfileStore(t, db)
	accounts := NewProductionStore(db).Accounts()
	now := time.Now().UTC()

	for i := 1; i <= 500; i++ {
		accountID := domain.ID(fmt.Sprintf("system-%03d", i))
		require.NoError(t, accounts.Save(ctx, domain.Account{
			ID: accountID, Role: domain.AccountRoleSpammer, Status: domain.AccountPaused,
			SessionPath: fmt.Sprintf("/session/%d", i), ProxyMode: domain.ProxyModeUnassigned,
			CreatedAt: now, UpdatedAt: now,
		}))
		require.NoErrorf(t, store.AssignAccount(ctx, accountID, domain.ProxyModeGlobal, nil), "system assignment %d", i)
	}

	counts, err := store.AssignmentCounts(ctx)
	require.NoError(t, err)
	require.Equal(t, 500, counts[domain.SystemProxyRouteID])
}

func TestMigrationV16PreservesAccountsOnSystemRoute(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "v15.db"))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, migrateDeliveryThrough(ctx, db, 15))
	require.NoError(t, Configure(ctx, db))
	const now = "2026-07-16T08:00:00.000000000Z"
	for i := 0; i < 11; i++ {
		_, err = db.ExecContext(ctx, `INSERT INTO accounts (id,phone_masked,display_name,role,status,session_path,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`,
			fmt.Sprintf("existing-%02d", i), "", "", "spammer", "ready", fmt.Sprintf("/session/%d", i), now, now)
		require.NoError(t, err)
	}

	require.NoError(t, Migrate(ctx, db))
	var mode string
	var profileID sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `SELECT proxy_mode,proxy_profile_id FROM accounts WHERE id='existing-00'`).Scan(&mode, &profileID))
	require.Equal(t, string(domain.ProxyModeGlobal), mode)
	require.False(t, profileID.Valid)
	var globalCount, unassignedCount, stoppedCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE proxy_mode='global'`).Scan(&globalCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE proxy_mode='unassigned'`).Scan(&unassignedCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE proxy_mode='unassigned' AND status='stopped'`).Scan(&stoppedCount))
	require.Equal(t, 10, globalCount)
	require.Equal(t, 1, unassignedCount)
	require.Equal(t, 1, stoppedCount)
}

func TestProxyProfileDeleteAndAssignmentCannotLeaveDanglingRoute(t *testing.T) {
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
		require.NoError(t, err)
		store := newTestProxyProfileStore(t, db)
		now := time.Now().UTC()
		empty := ""
		profileID := domain.ID("race")
		require.NoError(t, store.Save(ctx, domain.ProxyProfile{ID: profileID, Name: "Race", Protocol: "http", Host: "127.0.0.1", Port: 8080, Enabled: true, CreatedAt: now, UpdatedAt: now}, &empty))
		require.NoError(t, NewProductionStore(db).Accounts().Save(ctx, domain.Account{ID: "account", Role: domain.AccountRoleSpammer, Status: domain.AccountStopped, SessionPath: "/account", ProxyMode: domain.ProxyModeUnassigned, CreatedAt: now, UpdatedAt: now}))

		start := make(chan struct{})
		results := make(chan error, 2)
		go func() { <-start; results <- store.AssignAccount(ctx, "account", domain.ProxyModeAssigned, &profileID) }()
		go func() { <-start; results <- store.Delete(ctx, profileID) }()
		close(start)
		<-results
		<-results

		var dangling int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts a LEFT JOIN proxy_profiles p ON p.id=a.proxy_profile_id WHERE a.proxy_mode='assigned' AND p.id IS NULL`).Scan(&dangling))
		require.Zero(t, dangling)
		require.NoError(t, db.Close())
	}
}

func newTestProxyProfileStore(t *testing.T, db *sql.DB) *ProxyProfileStore {
	t.Helper()
	cipher, err := proxycrypto.NewProxyCredentialsCipher(make([]byte, 32))
	require.NoError(t, err)
	return NewProxyProfileStore(db, cipher)
}
