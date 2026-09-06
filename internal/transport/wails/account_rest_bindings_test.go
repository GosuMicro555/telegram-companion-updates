package wails

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase"
)

type accountRestListerStub struct {
	rows []usecase.AccountRest
	err  error
	ctx  context.Context
}

func (s *accountRestListerStub) List(ctx context.Context) ([]usecase.AccountRest, error) {
	s.ctx = ctx
	return append([]usecase.AccountRest(nil), s.rows...), s.err
}

func TestListAccountRestsReturnsSafeUTCWireDTO(t *testing.T) {
	type contextKey string
	const key contextKey = "root"
	location := time.FixedZone("UTC+3", 3*60*60)
	startedAt := time.Date(2026, 7, 23, 1, 2, 3, 456_000_000, location)
	until := time.Date(2026, 7, 24, 13, 2, 3, 456_000_000, location)
	lister := &accountRestListerStub{rows: []usecase.AccountRest{{
		AccountID: "account-a", AccountTitle: "Alpha", ChannelID: "channel-a", ChannelTitle: "Group",
		Catalog: domain.SourceCatalogOutbound, Status: usecase.AccountRestStatusResting,
		StartedAt: startedAt, Until: until, DurationHours: 36,
	}}}
	bindings := NewBindings(usecase.NewAutomationController(nil))
	ConfigureAccountRests(bindings, lister)
	root := context.WithValue(context.Background(), key, "desktop")
	bindings.SetRootContext(root)

	rows, err := bindings.ListAccountRests()

	require.NoError(t, err)
	require.Same(t, root, lister.ctx)
	require.Equal(t, []AccountRestDTO{{
		AccountID: "account-a", AccountTitle: "Alpha", ChannelID: "channel-a", ChannelTitle: "Group",
		Catalog: "outbound", Status: "resting",
		StartedAt: "2026-07-22T22:02:03.456Z", Until: "2026-07-24T10:02:03.456Z", DurationHours: 36,
	}}, rows)
	encoded, err := json.Marshal(rows)
	require.NoError(t, err)
	require.JSONEq(t, `[{
		"accountID":"account-a",
		"accountTitle":"Alpha",
		"channelID":"channel-a",
		"channelTitle":"Group",
		"catalog":"outbound",
		"status":"resting",
		"startedAt":"2026-07-22T22:02:03.456Z",
		"until":"2026-07-24T10:02:03.456Z",
		"durationHours":36
	}]`, string(encoded))
}

func TestListAccountRestsSanitizesDependencyErrors(t *testing.T) {
	lister := &accountRestListerStub{err: errors.New("sqlite /Users/private/account-rest.db failed")}
	bindings := NewBindings(usecase.NewAutomationController(nil))
	ConfigureAccountRests(bindings, lister)

	rows, err := bindings.ListAccountRests()

	require.Nil(t, rows)
	require.EqualError(t, err, "account rests are unavailable")
	require.NotContains(t, err.Error(), "Users")
}

func TestListAccountRestsRequiresConfiguredRuntime(t *testing.T) {
	bindings := NewBindings(usecase.NewAutomationController(nil))

	rows, err := bindings.ListAccountRests()

	require.Nil(t, rows)
	require.EqualError(t, err, "account rests are unavailable")
}
