package gotd

import (
	"errors"
	"sync"

	"github.com/gotd/td/telegram/dcs"

	"telegram-companion/internal/domain"
	appcrypto "telegram-companion/internal/service/crypto"
)

type AppCredentials = appcrypto.AppCredentials

type PerAccountClientFactory struct {
	credentials map[domain.ID]AppCredentials
	validations ValidationStore
	barrier     *SessionBarrier
	resolver    AccountResolver

	credentialsMu sync.RWMutex
	mu            sync.Mutex
	factories     map[domain.ID]*ClientFactory
}

func NewPerAccountClientFactory(credentials map[domain.ID]AppCredentials, validations ValidationStore, barrier *SessionBarrier) *PerAccountClientFactory {
	return NewPerAccountClientFactoryWithResolver(credentials, validations, barrier, nil)
}

type DCResolverFactory func() dcs.Resolver

type AccountResolver interface {
	ResolverFor(domain.Account) (dcs.Resolver, error)
}

type AccountResolverFunc func(domain.Account) (dcs.Resolver, error)

func (f AccountResolverFunc) ResolverFor(account domain.Account) (dcs.Resolver, error) {
	return f(account)
}

func NewPerAccountClientFactoryWithResolver(credentials map[domain.ID]AppCredentials, validations ValidationStore, barrier *SessionBarrier, resolverFactory DCResolverFactory) *PerAccountClientFactory {
	resolver := AccountResolverFunc(func(domain.Account) (dcs.Resolver, error) {
		if resolverFactory == nil {
			return nil, nil
		}
		return resolverFactory(), nil
	})
	return NewPerAccountClientFactoryWithAccountResolver(credentials, validations, barrier, resolver)
}

func NewPerAccountClientFactoryWithAccountResolver(credentials map[domain.ID]AppCredentials, validations ValidationStore, barrier *SessionBarrier, resolver AccountResolver) *PerAccountClientFactory {
	copyCredentials := make(map[domain.ID]AppCredentials, len(credentials))
	for id, value := range credentials {
		copyCredentials[id] = value
	}
	return &PerAccountClientFactory{
		credentials: copyCredentials,
		validations: validations,
		barrier:     barrier,
		resolver:    resolver,
		factories:   make(map[domain.ID]*ClientFactory, len(credentials)),
	}
}

func (f *PerAccountClientFactory) New(account domain.Account) (TelegramClient, error) {
	if f == nil || f.validations == nil || f.barrier == nil {
		return nil, errors.New("per-account client factory is not configured")
	}
	f.credentialsMu.RLock()
	credentials, ok := f.credentials[account.ID]
	f.credentialsMu.RUnlock()
	if !ok || credentials.AppID <= 0 || credentials.AppHash == "" {
		return nil, errors.New("telegram app credentials are unavailable for account")
	}
	var resolver dcs.Resolver
	var err error
	if f.resolver != nil {
		resolver, err = f.resolver.ResolverFor(account)
		if err != nil {
			return nil, err
		}
	}
	clientFactory := NewClientFactoryWithResolver(credentials.AppID, credentials.AppHash, f.validations, f.barrier, resolver)
	f.mu.Lock()
	f.factories[account.ID] = clientFactory
	f.mu.Unlock()
	return clientFactory.New(account)
}

func (f *PerAccountClientFactory) UpsertCredentials(id domain.ID, credentials AppCredentials) {
	if f == nil {
		return
	}
	f.credentialsMu.Lock()
	f.credentials[id] = credentials
	f.credentialsMu.Unlock()

	f.mu.Lock()
	previous := f.factories[id]
	delete(f.factories, id)
	f.mu.Unlock()
	if previous != nil {
		previous.ResetExplicitLifecycle()
	}
}

func (f *PerAccountClientFactory) ResetExplicitLifecycle() {
	if f == nil {
		return
	}
	f.credentialsMu.RLock()
	credentialCount := len(f.credentials)
	f.credentialsMu.RUnlock()
	f.mu.Lock()
	factories := make([]*ClientFactory, 0, len(f.factories))
	for _, factory := range f.factories {
		factories = append(factories, factory)
	}
	f.factories = make(map[domain.ID]*ClientFactory, credentialCount)
	f.mu.Unlock()
	for _, factory := range factories {
		factory.ResetExplicitLifecycle()
	}
}

var _ ManagedClientFactory = (*PerAccountClientFactory)(nil)
