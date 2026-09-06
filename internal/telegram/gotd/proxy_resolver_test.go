package gotd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

type proxyResolverValidationStore struct{}

func (proxyResolverValidationStore) MarkValidated(_ context.Context, _ domain.ID, _ time.Time) error {
	return nil
}

func TestPerAccountFactoryBuildsClientsWithConfiguredResolver(t *testing.T) {
	resolverFactory := NewTorSOCKSResolverFactory("127.0.0.1:19050")
	factory := NewPerAccountClientFactoryWithResolver(
		map[domain.ID]AppCredentials{"account-1": {AppID: 1, AppHash: "hash"}},
		proxyResolverValidationStore{},
		ProductionSessionBarrier(),
		resolverFactory,
	)
	_, err := factory.New(domain.Account{ID: "account-1", SessionPath: privateSessionFile(t, "resolver")})
	require.NoError(t, err)

	clientFactory := factory.factories["account-1"]
	builder, ok := clientFactory.builder.(gotdRuntimeBuilder)
	require.True(t, ok)
	require.NotNil(t, builder.resolver)
}

func TestNewProxyResolverRejectsInvalidRoute(t *testing.T) {
	_, err := NewProxyResolver(domain.ProxyRoute{Protocol: "ftp", Host: "127.0.0.1", Port: 8080, Enabled: true})
	require.Error(t, err)
}

type routeRegistryFake struct {
	route domain.ProxyRoute
	err   error
}

func (f routeRegistryFake) RouteFor(context.Context, domain.Account) (domain.ProxyRoute, error) {
	return f.route, f.err
}

func TestRegistryResolverNeverFallsBackWhenRouteUnavailable(t *testing.T) {
	resolver := NewRegistryResolver(routeRegistryFake{err: errors.New("route unavailable")})
	resolved, err := resolver.ResolverFor(domain.Account{ID: "account"})
	require.Nil(t, resolved)
	require.Error(t, err)
}
