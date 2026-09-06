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
)

func TestProductionStorePersistsAccountsRolesSessionsValidationAndSettings(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	store := NewProductionStore(db)
	now := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	accounts := []domain.Account{
		{ID: "account-opaque-a", Role: domain.AccountRoleSpammer, Status: domain.AccountPaused, SessionPath: "/sessions/opaque-a.session", ProxyMode: domain.ProxyModeDirect, CreatedAt: now, UpdatedAt: now},
		{ID: "account-opaque-b", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountPaused, SessionPath: "/sessions/opaque-b.session", ProxyMode: domain.ProxyModeDirect, CreatedAt: now, UpdatedAt: now},
	}

	require.NoError(t, store.EnsureAccounts(ctx, accounts))
	require.NoError(t, store.MarkValidated(ctx, "account-opaque-b", now.Add(time.Minute)))
	listed, err := store.ListAccounts(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	require.Equal(t, domain.AccountRoleScoutAnalyst, listed[1].Role)
	require.Equal(t, domain.AccountActive, listed[1].Status)
	require.Equal(t, "/sessions/opaque-b.session", listed[1].SessionPath)
	require.Equal(t, domain.ProxyModeGlobal, listed[1].ProxyMode)

	wantSettings := domain.AppSettings{
		RepliesPerMinute: 19, MinIntervalSeconds: 2,
		JoinIntervalMinMinutes: 10, JoinIntervalMaxMinutes: 60, GroupRestHours: 36,
		DirectMessages: false,
	}
	require.NoError(t, store.SaveAppSettings(ctx, wantSettings))
	gotSettings, err := store.LoadAppSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, wantSettings, gotSettings)
}

func TestListActiveKeepsAccountDuringRuntimeBackoff(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	store := NewProductionStore(db)
	now := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	account := domain.Account{
		ID: "account-backoff", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/account-backoff", ProxyMode: domain.ProxyModeGlobal,
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, store.Accounts().Save(ctx, account))
	require.NoError(t, store.Accounts().RecordRuntimeStatus(ctx, account.ID, "partial", "proxy_unavailable", now.Add(time.Minute)))

	active, err := store.Accounts().ListActive(ctx)

	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, account.ID, active[0].ID)
	require.Equal(t, domain.AccountStatus("partial"), active[0].Status)
}

func TestPermanentSessionErrorOverridesActiveFloodWait(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	store := NewProductionStore(db)
	now := time.Date(2026, 9, 1, 4, 0, 0, 0, time.UTC)
	account := domain.Account{
		ID: "account-invalid-session", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/account-invalid-session", ProxyMode: domain.ProxyModeGlobal,
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, store.Accounts().Save(ctx, account))
	require.NoError(t, store.Accounts().RecordFloodWait(ctx, account.ID, now.Add(time.Hour), now))
	require.NoError(t, store.Accounts().RecordRuntimeStatus(
		ctx,
		account.ID,
		"partial",
		"proxy_unavailable",
		now.Add(30*time.Second),
	))
	listed, err := store.ListAccounts(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, domain.AccountFloodWait, listed[0].Status)
	require.Equal(t, "telegram flood wait", listed[0].LastError)
	require.NotNil(t, listed[0].FloodWaitUntil)

	require.NoError(t, store.Accounts().RecordRuntimeStatus(
		ctx,
		account.ID,
		string(domain.AccountError),
		"rpc_auth_key_duplicated",
		now.Add(time.Minute),
	))

	listed, err = store.ListAccounts(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, domain.AccountError, listed[0].Status)
	require.Equal(t, "rpc_auth_key_duplicated", listed[0].LastError)
	require.Nil(t, listed[0].FloodWaitUntil)
}

func TestJoinScheduleRoundTripPreservesInitialTime(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, Migrate(ctx, db))

	store := NewProductionStore(db)
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	require.NoError(t, store.Accounts().Save(ctx, domain.Account{
		ID: "account-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/account-1", CreatedAt: now, UpdatedAt: now,
	}))
	initial := now.Add(15 * time.Minute)
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, domain.ChannelMembership{
		AccountID: "account-1", ChannelID: "channel-1", Status: "joining", JoinNotBefore: &initial,
	}))

	updated := now.Add(30 * time.Minute)
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, domain.ChannelMembership{
		AccountID: "account-1", ChannelID: "channel-1", Status: "joining", JoinNotBefore: &updated,
	}))

	memberships, err := store.ListMemberships(ctx, domain.SourceCatalogOutbound, "channel-1")
	require.NoError(t, err)
	require.Len(t, memberships, 1)
	require.Equal(t, &initial, memberships[0].JoinNotBefore)
}

func TestCatalogRepositoryNextJoiningDueSkipsPastAndTerminalMemberships(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, Migrate(ctx, db))
	store := NewProductionStore(db)
	now := time.Date(2026, 9, 7, 0, 43, 0, 0, time.UTC)
	account := domain.Account{ID: "account-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive, SessionPath: "/sessions/account-1", CreatedAt: now, UpdatedAt: now}
	require.NoError(t, store.Accounts().Save(ctx, account))

	// RFC3339Nano omits the fractional suffix for whole seconds. A textual SQL
	// comparison would incorrectly place this first deadline before `now`.
	first := now.Add(150 * time.Millisecond)
	second := now.Add(time.Second)
	for _, membership := range []domain.ChannelMembership{
		{AccountID: account.ID, ChannelID: "past", Status: "joining", JoinNotBefore: timePtr(now.Add(-time.Second))},
		{AccountID: account.ID, ChannelID: "first", Status: "joining", JoinNotBefore: timePtr(first)},
		{AccountID: account.ID, ChannelID: "second", Status: "joining", JoinNotBefore: timePtr(second)},
		{AccountID: account.ID, ChannelID: "member", IsMember: true, Status: "member", JoinNotBefore: timePtr(first)},
		{AccountID: account.ID, ChannelID: "pending", Status: "pending_approval", JoinNotBefore: timePtr(first)},
		{AccountID: account.ID, ChannelID: "leaving", Status: "leaving", JoinNotBefore: timePtr(first)},
	} {
		require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, membership))
	}

	due, err := store.Catalogs().NextJoiningDue(ctx, account.ID, now)
	require.NoError(t, err)
	require.Equal(t, &first, due)

	due, err = store.Catalogs().NextJoiningDue(ctx, account.ID, first)
	require.NoError(t, err)
	require.Equal(t, &second, due)

	due, err = store.Catalogs().NextJoiningDue(ctx, account.ID, second)
	require.NoError(t, err)
	require.Nil(t, due)
}

func TestActivateCatalogWithMembershipsRollsBackChannelAndMemberships(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, Migrate(ctx, db))
	_, err = db.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
	require.NoError(t, err)

	store := NewProductionStore(db)
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	channel := domain.Channel{ID: "channel-1", TelegramID: "telegram-1", Link: "https://t.me/room", Topic: "topic", Status: domain.ChannelPaused, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, store.SaveCatalog(ctx, domain.SourceCatalogOutbound, channel))
	require.NoError(t, store.Accounts().Save(ctx, domain.Account{ID: "account-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive, SessionPath: "/sessions/account-1", CreatedAt: now, UpdatedAt: now}))

	channel.Active = true
	channel.Status = domain.ChannelJoining
	channel.UpdatedAt = now.Add(time.Minute)
	err = store.ActivateCatalogWithMemberships(ctx, domain.SourceCatalogOutbound, channel, []domain.ChannelMembership{
		{AccountID: "account-1", ChannelID: channel.ID, Status: "joining", JoinNotBefore: timePtr(now)},
		{AccountID: "missing-account", ChannelID: channel.ID, Status: "joining", JoinNotBefore: timePtr(now.Add(10 * time.Minute))},
	})
	require.Error(t, err)

	channels, err := store.ListCatalog(ctx, domain.SourceCatalogOutbound)
	require.NoError(t, err)
	require.Len(t, channels, 1)
	require.False(t, channels[0].Active)
	require.Equal(t, domain.ChannelPaused, channels[0].Status)
	memberships, err := store.ListMemberships(ctx, domain.SourceCatalogOutbound, channel.ID)
	require.NoError(t, err)
	require.Empty(t, memberships)
}

func TestRequestCatalogLeaveRetainsChannelCancelsUnsubmittedJoinAndMarksRemainingMembershipsLeaving(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "catalog-leave.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	store := NewProductionStore(db)
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	channel := domain.Channel{
		ID: "channel-1", TelegramID: "77", Link: "https://t.me/room", Topic: "topic",
		Active: true, Status: domain.ChannelReady, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, store.SaveCatalog(ctx, domain.SourceCatalogOutbound, channel))
	for _, accountID := range []domain.ID{"joining", "pending", "member"} {
		require.NoError(t, store.Accounts().Save(ctx, domain.Account{
			ID: accountID, Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
			SessionPath: "/sessions/" + string(accountID), CreatedAt: now, UpdatedAt: now,
		}))
	}
	requestedAt := now.Add(-time.Hour)
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, domain.ChannelMembership{
		AccountID: "joining", ChannelID: channel.ID, Status: "joining",
	}))
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, domain.ChannelMembership{
		AccountID: "pending", ChannelID: channel.ID, Status: "pending_approval", RequestSubmittedAt: &requestedAt,
	}))
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, domain.ChannelMembership{
		AccountID: "member", ChannelID: channel.ID, IsMember: true, Status: "member",
	}))

	require.NoError(t, store.RequestCatalogLeave(ctx, domain.SourceCatalogOutbound, channel.ID))

	channels, err := store.ListCatalog(ctx, domain.SourceCatalogOutbound)
	require.NoError(t, err)
	require.Len(t, channels, 1)
	require.False(t, channels[0].Active)
	require.Equal(t, domain.ChannelPaused, channels[0].Status)
	memberships, err := store.ListMemberships(ctx, domain.SourceCatalogOutbound, channel.ID)
	require.NoError(t, err)
	require.Len(t, memberships, 2)
	require.Equal(t, []domain.ID{"member", "pending"}, []domain.ID{memberships[0].AccountID, memberships[1].AccountID})
	require.Equal(t, "leaving", memberships[0].Status)
	require.Equal(t, "leaving", memberships[1].Status)
	require.Equal(t, &requestedAt, memberships[1].RequestSubmittedAt)
}

func TestRetryPendingCatalogMembershipsClearsPreviousAttemptWithoutTouchingMembers(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "catalog-retry.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	store := NewProductionStore(db)
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	channel := domain.Channel{
		ID: "channel-1", TelegramID: "77", Link: "https://t.me/room", Topic: "topic",
		Active: true, Status: domain.ChannelJoining, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, store.SaveCatalog(ctx, domain.SourceCatalogOutbound, channel))
	for _, accountID := range []domain.ID{"pending", "member"} {
		require.NoError(t, store.Accounts().Save(ctx, domain.Account{
			ID: accountID, Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
			SessionPath: "/sessions/" + string(accountID), CreatedAt: now, UpdatedAt: now,
		}))
	}
	requestedAt := now.Add(-24 * time.Hour)
	lastCheckAt := now.Add(-time.Hour)
	retryNotBefore := now.Add(-30 * time.Minute)
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, domain.ChannelMembership{
		AccountID: "pending", ChannelID: channel.ID, Status: "pending_approval",
		RequestSubmittedAt: &requestedAt, LastCheckAt: &lastCheckAt, JoinNotBefore: &retryNotBefore,
		LastError: "old error",
	}))
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, domain.ChannelMembership{
		AccountID: "member", ChannelID: channel.ID, IsMember: true, Status: "member", JoinedAt: &requestedAt,
	}))

	updated, err := store.RetryPendingCatalogMemberships(ctx, domain.SourceCatalogOutbound, channel.ID, now)

	require.NoError(t, err)
	require.Equal(t, 1, updated)
	memberships, err := store.ListMemberships(ctx, domain.SourceCatalogOutbound, channel.ID)
	require.NoError(t, err)
	require.Len(t, memberships, 2)
	member := memberships[0]
	pending := memberships[1]
	require.Equal(t, domain.ID("member"), member.AccountID)
	require.True(t, member.IsMember)
	require.Equal(t, "member", member.Status)
	require.Equal(t, domain.ID("pending"), pending.AccountID)
	require.False(t, pending.IsMember)
	require.Equal(t, "joining", pending.Status)
	require.Nil(t, pending.RequestSubmittedAt)
	require.Nil(t, pending.LastCheckAt)
	require.Nil(t, pending.JoinNotBefore)
	require.Empty(t, pending.LastError)
}

func TestJoinIntervalDefaultsWhenMissingAndPreservesExplicitZeroRange(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, Migrate(ctx, db))

	settings, err := NewProductionStore(db).LoadAppSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, 10, settings.JoinIntervalMinMinutes)
	require.Equal(t, 60, settings.JoinIntervalMaxMinutes)

	_, err = db.ExecContext(ctx, `INSERT INTO app_settings (key,value_json,revision,updated_at)
		VALUES ('app_settings','{"joinIntervalMinMinutes":0,"joinIntervalMaxMinutes":0}',1,?)`, time.Now().UTC().Format(time.RFC3339Nano))
	require.NoError(t, err)

	settings, err = NewProductionStore(db).LoadAppSettings(ctx)
	require.NoError(t, err)
	require.Zero(t, settings.JoinIntervalMinMinutes)
	require.Zero(t, settings.JoinIntervalMaxMinutes)
}

func TestListAccountGroupRestsIncludesExpiredRowsAndCatalog(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()

	store := NewProductionStore(db)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Accounts().Save(ctx, domain.Account{
		ID: "account-1", DisplayName: "Primary", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/account-1", CreatedAt: now, UpdatedAt: now,
	}))
	channels := []struct {
		catalog domain.SourceCatalog
		channel domain.Channel
		until   time.Time
	}{
		{
			catalog: domain.SourceCatalogOutbound,
			channel: domain.Channel{ID: "outbound-1", TelegramID: "outbound-1", Title: "Expired group", Link: "https://t.me/expired", Topic: "topic", Status: domain.ChannelReady, CreatedAt: now, UpdatedAt: now},
			until:   now.Add(-time.Hour),
		},
		{
			catalog: domain.SourceCatalogScout,
			channel: domain.Channel{ID: "scout-1", TelegramID: "scout-1", Title: "Active group", Link: "https://t.me/active", Topic: "topic", Status: domain.ChannelReady, CreatedAt: now, UpdatedAt: now},
			until:   now.Add(36 * time.Hour),
		},
	}
	for _, entry := range channels {
		require.NoError(t, store.SaveCatalog(ctx, entry.catalog, entry.channel))
		require.NoError(t, store.SaveMembership(ctx, entry.catalog, domain.ChannelMembership{
			AccountID: "account-1", ChannelID: entry.channel.ID, IsMember: true, Status: "member",
		}))
		_, err := db.ExecContext(ctx, `UPDATE account_channel_memberships
			SET rest_started_at=?, rest_until=?, rest_duration_hours=36
			WHERE account_id=? AND catalog=? AND channel_id=?`,
			formatTime(now), formatTime(entry.until), "account-1", entry.catalog, entry.channel.ID)
		require.NoError(t, err)
	}

	rests, err := store.ListAccountGroupRests(ctx)

	require.NoError(t, err)
	require.Len(t, rests, 2)
	byCatalog := make(map[domain.SourceCatalog]domain.AccountGroupRest, len(rests))
	for _, rest := range rests {
		byCatalog[rest.Catalog] = rest
	}
	require.Equal(t, "Primary", byCatalog[domain.SourceCatalogOutbound].AccountTitle)
	require.Equal(t, "Expired group", byCatalog[domain.SourceCatalogOutbound].ChannelTitle)
	require.Equal(t, now.Add(-time.Hour), byCatalog[domain.SourceCatalogOutbound].Until)
	require.Equal(t, "Active group", byCatalog[domain.SourceCatalogScout].ChannelTitle)
}

func TestActiveGroupRestUntilReturnsLatestFutureExpiryPerAccount(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()

	store := NewProductionStore(db)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	for _, accountID := range []domain.ID{"account-1", "account-2"} {
		require.NoError(t, store.Accounts().Save(ctx, domain.Account{
			ID: accountID, DisplayName: string(accountID), Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
			SessionPath: "/sessions/" + string(accountID), CreatedAt: now, UpdatedAt: now,
		}))
	}
	expiries := []struct {
		accountID domain.ID
		channelID domain.ID
		until     time.Time
	}{
		{accountID: "account-1", channelID: "expired", until: now.Add(-time.Minute)},
		{accountID: "account-1", channelID: "soon", until: now.Add(time.Hour)},
		{accountID: "account-1", channelID: "latest", until: now.Add(2 * time.Hour)},
		{accountID: "account-1", channelID: "fractional-latest", until: now.Add(2*time.Hour + 500*time.Millisecond)},
		{accountID: "account-2", channelID: "other", until: now.Add(90 * time.Minute)},
	}
	for _, entry := range expiries {
		channel := domain.Channel{
			ID: entry.channelID, TelegramID: string(entry.channelID), Title: string(entry.channelID),
			Link: "https://t.me/" + string(entry.channelID), Topic: "topic", Status: domain.ChannelReady, CreatedAt: now, UpdatedAt: now,
		}
		require.NoError(t, store.SaveCatalog(ctx, domain.SourceCatalogOutbound, channel))
		require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, domain.ChannelMembership{
			AccountID: entry.accountID, ChannelID: entry.channelID, IsMember: true, Status: "member",
		}))
		_, err := db.ExecContext(ctx, `UPDATE account_channel_memberships
			SET rest_started_at=?, rest_until=?, rest_duration_hours=36
			WHERE account_id=? AND catalog='outbound' AND channel_id=?`,
			formatTime(now), formatTime(entry.until), entry.accountID, entry.channelID)
		require.NoError(t, err)
	}

	active, err := store.ActiveGroupRestUntil(ctx, []domain.ID{"account-1", "account-2", "missing"}, now)

	require.NoError(t, err)
	require.Equal(t, map[domain.ID]time.Time{
		"account-1": now.Add(2*time.Hour + 500*time.Millisecond),
		"account-2": now.Add(90 * time.Minute),
	}, active)
}

func TestClearGroupRestsClearsMembershipsAndReleasesOnlyRestDelayedJobs(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "clear-group-rests.db"))
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	store := NewProductionStore(db)
	accountID := domain.ID("account-1")
	channel := domain.Channel{
		ID: "channel-1", TelegramID: "channel-1", Title: "Test group",
		Link: "https://t.me/test_group", Topic: "topic", Active: true,
		Status: domain.ChannelReady, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, store.Accounts().Save(ctx, domain.Account{
		ID: accountID, Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/account-1", CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, store.SaveCatalog(ctx, domain.SourceCatalogOutbound, channel))
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, domain.ChannelMembership{
		AccountID: accountID, ChannelID: channel.ID, IsMember: true, Status: "member",
		JoinedAt: timePtr(now), RestDurationHours: 36,
	}))

	jobs := NewJobRepository(db, func() time.Time { return now })
	for index, jobID := range []domain.ID{"rest-delayed", "flood-delayed"} {
		require.NoError(t, jobs.Enqueue(ctx, domain.OutgoingMessageJob{
			ID: jobID, Type: domain.JobPublicReply, AccountID: &accountID, ChannelID: channel.ID,
			ReplyToMessageID: fmt.Sprintf("%d", index+1), Text: "reply", Status: "queued",
			NextAttemptAt: now, CreatedAt: now,
		}))
	}
	require.NoError(t, jobs.DelayTransientUntil(ctx, "rest-delayed", "account_group_rest", now.Add(36*time.Hour)))
	require.NoError(t, jobs.DelayTransientUntil(ctx, "flood-delayed", "flood_wait", now.Add(time.Hour)))

	rests, err := store.ListAccountGroupRests(ctx)
	require.NoError(t, err)
	require.Len(t, rests, 1)

	require.NoError(t, store.ClearGroupRests(ctx))

	rests, err = store.ListAccountGroupRests(ctx)
	require.NoError(t, err)
	require.Empty(t, rests)
	var restStartedAt, restUntil sql.NullString
	var restDurationHours int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT rest_started_at,rest_until,rest_duration_hours
		FROM account_channel_memberships WHERE account_id=? AND catalog=? AND channel_id=?`,
		accountID, domain.SourceCatalogOutbound, channel.ID,
	).Scan(&restStartedAt, &restUntil, &restDurationHours))
	require.False(t, restStartedAt.Valid)
	require.False(t, restUntil.Valid)
	require.Zero(t, restDurationHours)

	var status, lastError string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status,last_error FROM outgoing_message_jobs WHERE id='rest-delayed'`).
		Scan(&status, &lastError))
	require.Equal(t, "queued", status)
	require.Empty(t, lastError)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status,last_error FROM outgoing_message_jobs WHERE id='flood-delayed'`).
		Scan(&status, &lastError))
	require.Equal(t, "delayed", status)
	require.Equal(t, "flood_wait", lastError)
}

func TestClearGroupRestsRollsBackMembershipClearWhenJobReleaseFails(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "clear-group-rests-rollback.db"))
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	store := NewProductionStore(db)
	accountID := domain.ID("account-1")
	channel := domain.Channel{
		ID: "channel-1", TelegramID: "channel-1", Title: "Test group",
		Link: "https://t.me/test_group", Topic: "topic", Active: true,
		Status: domain.ChannelReady, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, store.Accounts().Save(ctx, domain.Account{
		ID: accountID, Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/account-1", CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, store.SaveCatalog(ctx, domain.SourceCatalogOutbound, channel))
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, domain.ChannelMembership{
		AccountID: accountID, ChannelID: channel.ID, IsMember: true, Status: "member",
		JoinedAt: timePtr(now), RestDurationHours: 36,
	}))

	jobs := NewJobRepository(db, func() time.Time { return now })
	require.NoError(t, jobs.Enqueue(ctx, domain.OutgoingMessageJob{
		ID: "rest-delayed", Type: domain.JobPublicReply, AccountID: &accountID, ChannelID: channel.ID,
		ReplyToMessageID: "1", Text: "reply", Status: "queued", NextAttemptAt: now, CreatedAt: now,
	}))
	require.NoError(t, jobs.DelayTransientUntil(ctx, "rest-delayed", "account_group_rest", now.Add(36*time.Hour)))
	_, err = db.ExecContext(ctx, `CREATE TRIGGER reject_group_rest_release
		BEFORE UPDATE OF status ON outgoing_message_jobs
		WHEN OLD.last_error='account_group_rest' AND NEW.status='queued'
		BEGIN
			SELECT RAISE(ABORT,'forced group rest release failure');
		END`)
	require.NoError(t, err)

	err = store.ClearGroupRests(ctx)

	require.ErrorContains(t, err, "release group rest delays")
	var restStartedAt, restUntil sql.NullString
	var restDurationHours int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT rest_started_at,rest_until,rest_duration_hours
		FROM account_channel_memberships WHERE account_id=? AND catalog=? AND channel_id=?`,
		accountID, domain.SourceCatalogOutbound, channel.ID,
	).Scan(&restStartedAt, &restUntil, &restDurationHours))
	require.True(t, restStartedAt.Valid)
	require.True(t, restUntil.Valid)
	require.Equal(t, 36, restDurationHours)
	var status, lastError string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status,last_error FROM outgoing_message_jobs WHERE id='rest-delayed'`).
		Scan(&status, &lastError))
	require.Equal(t, "delayed", status)
	require.Equal(t, "account_group_rest", lastError)
}

func TestClearJoinIntervalsMakesEveryExistingMembershipImmediatelyDue(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewProductionStore(db)
	now := time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)
	require.NoError(t, store.EnsureAccounts(ctx, []domain.Account{{
		ID: "account-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		CreatedAt: now, UpdatedAt: now,
	}}))
	future := now.Add(24 * time.Hour)
	for index, status := range []string{"joining", "pending_approval", "member"} {
		channelID := domain.ID(fmt.Sprintf("channel-%d", index+1))
		require.NoError(t, store.SaveCatalog(ctx, domain.SourceCatalogOutbound, domain.Channel{
			ID: channelID, Link: "https://t.me/" + string(channelID), Active: true, CreatedAt: now, UpdatedAt: now,
		}))
		require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, domain.ChannelMembership{
			AccountID: "account-1", ChannelID: channelID, Status: status, JoinNotBefore: &future,
		}))
	}

	require.NoError(t, store.ClearJoinIntervals(ctx))

	var remaining int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM account_channel_memberships WHERE join_not_before IS NOT NULL`).Scan(&remaining))
	require.Zero(t, remaining)
}

func TestSaveMembershipInitializesRestOnlyOnce(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "membership-rest.db"))
	require.NoError(t, err)
	defer db.Close()
	store := NewProductionStore(db)
	now := time.Date(2026, 7, 20, 12, 0, 0, 123, time.UTC)
	require.NoError(t, store.Accounts().Save(ctx, domain.Account{
		ID: "account-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/account-1", CreatedAt: now, UpdatedAt: now,
	}))
	intent := domain.ChannelMembership{
		AccountID: "account-1", ChannelID: "channel-1", Status: "joining", RestDurationHours: 36,
	}
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, intent))

	firstJoinedAt := now.Add(time.Minute)
	first := intent
	first.IsMember = true
	first.Status = "member"
	first.JoinedAt = timePtr(firstJoinedAt)
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, first))

	second := first
	second.JoinedAt = timePtr(firstJoinedAt.Add(time.Hour))
	second.RestDurationHours = 72
	second.RestStartedAt = second.JoinedAt
	second.RestUntil = timePtr(second.JoinedAt.Add(72 * time.Hour))
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, second))

	membership, found, err := store.Catalogs().LoadMembership(ctx, "account-1", domain.SourceCatalogOutbound, "channel-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, timePtr(firstJoinedAt), membership.JoinedAt)
	require.Equal(t, timePtr(firstJoinedAt), membership.RestStartedAt)
	require.Equal(t, timePtr(firstJoinedAt.Add(36*time.Hour)), membership.RestUntil)
	require.Equal(t, 36, membership.RestDurationHours)
}

func TestLegacyJoinedMembershipIsNotBackfilledWithRest(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "legacy-membership-rest.db"))
	require.NoError(t, err)
	defer db.Close()
	store := NewProductionStore(db)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Accounts().Save(ctx, domain.Account{
		ID: "legacy", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/legacy", CreatedAt: now, UpdatedAt: now,
	}))
	legacy := domain.ChannelMembership{
		AccountID: "legacy", ChannelID: "channel-1", IsMember: true, Status: "member", JoinedAt: timePtr(now),
	}
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, legacy))

	rediscovered := legacy
	rediscovered.JoinedAt = timePtr(now.Add(time.Hour))
	rediscovered.RestDurationHours = 36
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, rediscovered))

	membership, found, err := store.Catalogs().LoadMembership(ctx, "legacy", domain.SourceCatalogOutbound, "channel-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, timePtr(now), membership.JoinedAt)
	require.Nil(t, membership.RestStartedAt)
	require.Nil(t, membership.RestUntil)
	require.Zero(t, membership.RestDurationHours)
}

func TestConcurrentMembershipConfirmationsInitializeRestOnce(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "concurrent-membership-rest.db"))
	require.NoError(t, err)
	defer db.Close()
	store := NewProductionStore(db)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Accounts().Save(ctx, domain.Account{
		ID: "account-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/account-1", CreatedAt: now, UpdatedAt: now,
	}))
	intent := domain.ChannelMembership{
		AccountID: "account-1", ChannelID: "channel-1", Status: "joining", RestDurationHours: 36,
	}
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, intent))

	const confirmations = 12
	start := make(chan struct{})
	errs := make(chan error, confirmations)
	var wg sync.WaitGroup
	for index := 0; index < confirmations; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			joinedAt := now.Add(time.Duration(index+1) * time.Second)
			confirmed := intent
			confirmed.IsMember = true
			confirmed.Status = "member"
			confirmed.JoinedAt = timePtr(joinedAt)
			errs <- store.SaveMembership(ctx, domain.SourceCatalogOutbound, confirmed)
		}(index)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	membership, found, err := store.Catalogs().LoadMembership(ctx, "account-1", domain.SourceCatalogOutbound, "channel-1")
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, membership.JoinedAt)
	require.Equal(t, membership.JoinedAt, membership.RestStartedAt)
	require.Equal(t, timePtr(membership.JoinedAt.Add(36*time.Hour)), membership.RestUntil)
	require.Equal(t, 36, membership.RestDurationHours)
	initialized := membership

	for index := 0; index < confirmations; index++ {
		confirmed := intent
		confirmed.IsMember = true
		confirmed.Status = "member"
		confirmed.JoinedAt = timePtr(now.Add(time.Duration(index+20) * time.Second))
		require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, confirmed))
	}
	membership, found, err = store.Catalogs().LoadMembership(ctx, "account-1", domain.SourceCatalogOutbound, "channel-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, initialized.JoinedAt, membership.JoinedAt)
	require.Equal(t, initialized.RestStartedAt, membership.RestStartedAt)
	require.Equal(t, initialized.RestUntil, membership.RestUntil)
	require.Equal(t, initialized.RestDurationHours, membership.RestDurationHours)
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func TestProductionStorePersistsAccountDeliveryState(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	store := NewProductionStore(db)
	account := domain.Account{
		ID: "account-delivery", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/delivery", NextDelivery: domain.DeliveryTargetPublic,
		PrivateMessagesClosed: 4, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, store.Accounts().Save(ctx, account))

	accounts, err := store.Accounts().List(ctx)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, domain.DeliveryTargetPublic, accounts[0].NextDelivery)
	require.Equal(t, int64(4), accounts[0].PrivateMessagesClosed)
}

func TestProductionStorePreservesIndependentDeliveryCursorsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := Open(ctx, path)
	require.NoError(t, err)

	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	store := NewProductionStore(db)
	require.NoError(t, store.Accounts().Save(ctx, domain.Account{
		ID: "account-public", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/public", NextDelivery: domain.DeliveryTargetPublic,
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, store.Accounts().Save(ctx, domain.Account{
		ID: "account-private", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/private", NextDelivery: domain.DeliveryTargetPrivate,
		CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	}))
	require.NoError(t, db.Close())

	reopened, err := Open(ctx, path)
	require.NoError(t, err)
	defer reopened.Close()

	accounts, err := NewProductionStore(reopened).Accounts().List(ctx)
	require.NoError(t, err)
	require.Len(t, accounts, 2)
	byID := make(map[domain.ID]domain.Account, len(accounts))
	for _, account := range accounts {
		byID[account.ID] = account
	}
	require.Equal(t, domain.DeliveryTargetPublic, byID["account-public"].NextDelivery)
	require.Equal(t, domain.DeliveryTargetPrivate, byID["account-private"].NextDelivery)
}

func TestProductionStoreRoleRoundTripPreservesDeliveryCursorAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := Open(ctx, path)
	require.NoError(t, err)

	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	store := NewProductionStore(db)
	account := domain.Account{
		ID: "account-role-round-trip", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/role-round-trip", NextDelivery: domain.DeliveryTargetPublic,
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, store.Accounts().Save(ctx, account))

	_, err = store.SetAccountRole(ctx, account.ID, domain.AccountRoleScoutAnalyst, now.Add(time.Minute))
	require.NoError(t, err)
	_, err = store.SetAccountRole(ctx, account.ID, domain.AccountRoleSpammer, now.Add(2*time.Minute))
	require.NoError(t, err)
	require.NoError(t, db.Close())

	reopened, err := Open(ctx, path)
	require.NoError(t, err)
	defer reopened.Close()

	accounts, err := NewProductionStore(reopened).Accounts().List(ctx)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, domain.AccountRoleSpammer, accounts[0].Role)
	require.Equal(t, domain.DeliveryTargetPublic, accounts[0].NextDelivery)
}

func TestProductionStoreSatisfiesRuntimeRepositoryPorts(t *testing.T) {
	store := NewProductionStore(nil)
	require.Implements(t, (*domain.AccountRepository)(nil), store.Accounts())
	require.Implements(t, (*domain.CatalogRepository)(nil), store.Catalogs())
	require.Implements(t, (*domain.ChannelRepository)(nil), store.Channels())
}

func TestEnsureAccountsPreservesRuntimeStateOnRestart(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	store := NewProductionStore(db)
	now := time.Now().UTC()
	require.NoError(t, store.EnsureAccounts(ctx, []domain.Account{{ID: "account-1", PhoneMasked: "***01", Role: domain.AccountRoleSpammer, Status: domain.AccountActive, SessionPath: "/old", CreatedAt: now, UpdatedAt: now}}))
	account := (mustAccounts(t, store, ctx))[0]
	account.Role = domain.AccountRoleScoutAnalyst
	account.Status = domain.AccountFloodWait
	account.PublicRepliesSent = 9
	require.NoError(t, store.SaveAccount(ctx, account))

	require.NoError(t, store.EnsureAccounts(ctx, []domain.Account{{ID: "account-1", PhoneMasked: "***01", Role: domain.AccountRoleSpammer, Status: domain.AccountActive, SessionPath: "/new", CreatedAt: now, UpdatedAt: now}}))
	restarted := (mustAccounts(t, store, ctx))[0]
	require.Equal(t, domain.AccountRoleScoutAnalyst, restarted.Role)
	require.Equal(t, domain.AccountFloodWait, restarted.Status)
	require.Equal(t, int64(9), restarted.PublicRepliesSent)
	require.Equal(t, "/new", restarted.SessionPath)
}

func TestAccountRepositoryAutomaticallyRecoversExpiredFloodWait(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	store := NewProductionStore(db)
	now := time.Now().UTC()
	expired := now.Add(-time.Minute)
	future := now.Add(time.Hour)
	require.NoError(t, store.EnsureAccounts(ctx, []domain.Account{
		{ID: "expired", Role: domain.AccountRoleSpammer, Status: domain.AccountFloodWait, FloodWaitUntil: &expired, SessionPath: "/expired", CreatedAt: now, UpdatedAt: now},
		{ID: "waiting", Role: domain.AccountRoleSpammer, Status: domain.AccountFloodWait, FloodWaitUntil: &future, SessionPath: "/waiting", CreatedAt: now, UpdatedAt: now},
	}))
	for _, account := range mustAccounts(t, store, ctx) {
		require.NoError(t, store.SaveAccount(ctx, account))
	}

	active, err := store.Accounts().ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, domain.ID("expired"), active[0].ID)
	require.Equal(t, domain.AccountActive, active[0].Status)
	require.Nil(t, active[0].FloodWaitUntil)
}

func TestAccountRepositoryExcludesUnassignedAccounts(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	store := NewProductionStore(db)
	now := time.Now().UTC()
	require.NoError(t, store.Accounts().Save(ctx, domain.Account{
		ID: "overflow", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/overflow", ProxyMode: domain.ProxyModeUnassigned, CreatedAt: now, UpdatedAt: now,
	}))

	active, err := store.Accounts().ListActive(ctx)
	require.NoError(t, err)
	require.Empty(t, active)
}

func TestReconcileDiscoveredAccountAtFullSystemRoute(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	store := NewProductionStore(db)
	now := time.Now().UTC()
	for i := 0; i < 9; i++ {
		require.NoError(t, store.Accounts().Save(ctx, domain.Account{
			ID: domain.ID(fmt.Sprintf("stable-%02d", i)), Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
			SessionPath: fmt.Sprintf("/stable/%d", i), ProxyMode: domain.ProxyModeGlobal, CreatedAt: now, UpdatedAt: now,
		}))
	}
	require.NoError(t, store.Accounts().Save(ctx, domain.Account{
		ID: "account-0001", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountActive,
		SessionPath: "/legacy", ProxyMode: domain.ProxyModeGlobal, CreatedAt: now, UpdatedAt: now,
	}))

	require.NoError(t, store.ReconcileDiscoveredAccounts(ctx, []domain.Account{{ID: "discovered-opaque", SessionPath: "/legacy"}}))
	accounts := mustAccounts(t, store, ctx)
	require.Len(t, accounts, 10)
	var found bool
	for _, account := range accounts {
		if account.ID == "discovered-opaque" {
			found = true
			require.Equal(t, domain.ProxyModeGlobal, account.ProxyMode)
		}
		require.NotEqual(t, domain.ID("account-0001"), account.ID)
	}
	require.True(t, found)
}

func TestReconcileDiscoveredAccountAtFullCustomRoute(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	store := NewProductionStore(db)
	profiles := newTestProxyProfileStore(t, db)
	now := time.Now().UTC()
	empty := ""
	profileID := domain.ID("custom-full")
	require.NoError(t, profiles.Save(ctx, domain.ProxyProfile{
		ID: profileID, Name: "Custom Full", Protocol: "socks5", Host: "127.0.0.1", Port: 1080,
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}, &empty))
	for i := 0; i < 9; i++ {
		require.NoError(t, store.Accounts().Save(ctx, domain.Account{
			ID: domain.ID(fmt.Sprintf("custom-%02d", i)), Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
			SessionPath: fmt.Sprintf("/custom/%d", i), ProxyMode: domain.ProxyModeAssigned, ProxyProfileID: &profileID,
			CreatedAt: now, UpdatedAt: now,
		}))
	}
	require.NoError(t, store.Accounts().Save(ctx, domain.Account{
		ID: "account-0002", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountActive,
		SessionPath: "/legacy-custom", ProxyMode: domain.ProxyModeAssigned, ProxyProfileID: &profileID,
		CreatedAt: now, UpdatedAt: now,
	}))

	require.NoError(t, store.ReconcileDiscoveredAccounts(ctx, []domain.Account{{ID: "discovered-custom", SessionPath: "/legacy-custom"}}))
	accounts := mustAccounts(t, store, ctx)
	require.Len(t, accounts, 10)
	for _, account := range accounts {
		if account.ID == "discovered-custom" {
			require.Equal(t, domain.ProxyModeAssigned, account.ProxyMode)
			require.NotNil(t, account.ProxyProfileID)
			require.Equal(t, profileID, *account.ProxyProfileID)
			return
		}
	}
	t.Fatal("reconciled custom-route account not found")
}

func TestMembershipPersistenceCannotRestoreDisabledJoinIntervalOrGroupRest(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "disabled-membership-settings.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	store := NewProductionStore(db)
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	accountID := domain.ID("account-disabled-settings")
	channel := domain.Channel{
		ID: "channel-disabled-settings", TelegramID: "channel-disabled-settings",
		Link: "https://t.me/disabled_settings", Topic: "test", Active: true,
		Status: domain.ChannelJoining, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, store.Accounts().Save(ctx, domain.Account{
		ID: accountID, Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/disabled-settings", CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, store.SaveCatalog(ctx, domain.SourceCatalogOutbound, channel))
	require.NoError(t, store.SaveAppSettings(ctx, domain.AppSettings{
		RepliesPerMinute: 19, MinIntervalSeconds: 2,
		JoinIntervalEnabled: true, JoinIntervalMinMinutes: 10, JoinIntervalMaxMinutes: 60,
		GroupRestHours: 36, GroupRestEnabled: true,
	}))
	staleJoinNotBefore := now.Add(24 * time.Hour)
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, domain.ChannelMembership{
		AccountID: accountID, ChannelID: channel.ID, Status: "joining",
		JoinNotBefore: &staleJoinNotBefore, RestDurationHours: 36,
	}))

	require.NoError(t, store.SaveAppSettings(ctx, domain.AppSettings{
		RepliesPerMinute: 19, MinIntervalSeconds: 2,
		JoinIntervalEnabled: false, JoinIntervalMinMinutes: 0, JoinIntervalMaxMinutes: 3000,
		GroupRestHours: 36, GroupRestEnabled: false,
	}))
	require.NoError(t, store.ClearJoinIntervals(ctx))
	require.NoError(t, store.ClearGroupRests(ctx))

	joinedAt := now.Add(time.Minute)
	joinNotBefore := now.Add(24 * time.Hour)
	require.NoError(t, store.ActivateCatalogWithMemberships(ctx, domain.SourceCatalogOutbound, channel, []domain.ChannelMembership{{
		AccountID: accountID, ChannelID: channel.ID, IsMember: true, Status: "member",
		JoinedAt: &joinedAt, JoinNotBefore: &joinNotBefore, RestDurationHours: 36,
	}}))

	memberships, err := store.ListMemberships(ctx, domain.SourceCatalogOutbound, channel.ID)
	require.NoError(t, err)
	require.Len(t, memberships, 1)
	require.Nil(t, memberships[0].JoinNotBefore)
	require.Nil(t, memberships[0].RestStartedAt)
	require.Nil(t, memberships[0].RestUntil)
	require.Zero(t, memberships[0].RestDurationHours)
}

func mustAccounts(t *testing.T, store *ProductionStore, ctx context.Context) []domain.Account {
	t.Helper()
	accounts, err := store.ListAccounts(ctx)
	require.NoError(t, err)
	return accounts
}
