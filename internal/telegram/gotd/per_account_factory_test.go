package gotd

import (
	"os"
	"sync"
	"testing"

	"github.com/gotd/td/telegram/dcs"
	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

type accountResolverRecorder struct {
	mu    sync.Mutex
	calls []domain.ID
}

func (r *accountResolverRecorder) ResolverFor(account domain.Account) (dcs.Resolver, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, account.ID)
	return dcs.Plain(dcs.PlainOptions{}), nil
}

func (r *accountResolverRecorder) callCount(id domain.ID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, candidate := range r.calls {
		if candidate == id {
			count++
		}
	}
	return count
}

func TestPerAccountFactoryResolvesAtNewTimeForEachAccount(t *testing.T) {
	resolver := &accountResolverRecorder{}
	factory := NewPerAccountClientFactoryWithAccountResolver(
		map[domain.ID]AppCredentials{
			"one": {AppID: 1, AppHash: "hash-one"},
			"two": {AppID: 2, AppHash: "hash-two"},
		},
		proxyResolverValidationStore{}, ProductionSessionBarrier(), resolver,
	)
	accountOne := domain.Account{ID: "one", SessionPath: privateSessionFile(t, "one")}
	accountTwo := domain.Account{ID: "two", SessionPath: privateSessionFile(t, "two")}

	_, err := factory.New(accountOne)
	require.NoError(t, err)
	_, err = factory.New(accountTwo)
	require.NoError(t, err)
	_, err = factory.New(accountOne)
	require.NoError(t, err)
	require.Equal(t, 2, resolver.callCount("one"))
	require.Equal(t, 1, resolver.callCount("two"))
}

func TestPerAccountClientFactoryAcceptsCredentialsAddedAfterConstruction(t *testing.T) {
	factory := NewPerAccountClientFactoryWithAccountResolver(
		map[domain.ID]AppCredentials{},
		proxyResolverValidationStore{}, ProductionSessionBarrier(), nil,
	)
	account := domain.Account{ID: "account-new", SessionPath: privateSessionFile(t, "account-new")}

	_, err := factory.New(account)
	require.ErrorContains(t, err, "unavailable")

	factory.UpsertCredentials(account.ID, AppCredentials{AppID: 123, AppHash: "hash"})
	_, err = factory.New(account)
	require.NoError(t, err)
}

func TestPerAccountClientFactoryUpsertCredentialsIsSafeWithConcurrentNew(t *testing.T) {
	account := domain.Account{ID: "account-race", SessionPath: privateSessionFile(t, "account-race")}
	factory := NewPerAccountClientFactoryWithAccountResolver(
		map[domain.ID]AppCredentials{account.ID: {AppID: 1, AppHash: "initial"}},
		proxyResolverValidationStore{}, ProductionSessionBarrier(), nil,
	)

	var wg sync.WaitGroup
	for i := 1; i <= 20; i++ {
		wg.Add(2)
		go func(appID int) {
			defer wg.Done()
			factory.UpsertCredentials(account.ID, AppCredentials{AppID: appID, AppHash: "updated"})
		}(i)
		go func() {
			defer wg.Done()
			_, _ = factory.New(account)
		}()
	}
	wg.Wait()
}

func privateSessionFile(t *testing.T, name string) string {
	t.Helper()
	path := t.TempDir() + "/" + name + ".session"
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	return path
}
