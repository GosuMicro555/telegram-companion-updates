package wails

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"telegram-companion/internal/domain"
	proxyroutes "telegram-companion/internal/proxy/routes"
	"telegram-companion/internal/usecase"

	"github.com/stretchr/testify/require"
)

type proxyProfileRepositoryStub struct {
	mu            sync.Mutex
	profiles      map[domain.ID]domain.ProxyProfile
	passwords     map[domain.ID]string
	counts        map[domain.ID]int
	accounts      *settingsStoreStub
	savedPassword *string
	deleteErr     error
}

func newProxyProfileRepositoryStub(accounts *settingsStoreStub, profiles ...domain.ProxyProfile) *proxyProfileRepositoryStub {
	rows := make(map[domain.ID]domain.ProxyProfile, len(profiles))
	for _, profile := range profiles {
		rows[profile.ID] = profile
	}
	return &proxyProfileRepositoryStub{
		profiles: rows, passwords: make(map[domain.ID]string), counts: make(map[domain.ID]int), accounts: accounts,
	}
}

func (s *proxyProfileRepositoryStub) Save(_ context.Context, profile domain.ProxyProfile, password *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, exists := s.profiles[profile.ID]
	if password == nil && !exists {
		return errors.New("proxy profile not found")
	}
	if password != nil {
		value := *password
		s.savedPassword = &value
		s.passwords[profile.ID] = value
		profile.PasswordConfigured = value != ""
	} else {
		s.savedPassword = nil
		profile.PasswordConfigured = existing.PasswordConfigured
	}
	if profile.CreatedAt.IsZero() && exists {
		profile.CreatedAt = existing.CreatedAt
	}
	s.profiles[profile.ID] = profile
	return nil
}

func (s *proxyProfileRepositoryStub) List(context.Context) ([]domain.ProxyProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profiles := make([]domain.ProxyProfile, 0, len(s.profiles))
	for _, profile := range s.profiles {
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func (s *proxyProfileRepositoryStub) Delete(_ context.Context, profileID domain.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if _, ok := s.profiles[profileID]; !ok {
		return errors.New("proxy profile not found")
	}
	delete(s.profiles, profileID)
	return nil
}

func (s *proxyProfileRepositoryStub) Route(_ context.Context, profileID domain.ID) (domain.ProxyRoute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.profiles[profileID]
	if !ok {
		return domain.ProxyRoute{}, errors.New("proxy profile not found")
	}
	return domain.ProxyRoute{
		ID: profile.ID, Name: profile.Name, Protocol: profile.Protocol, Host: profile.Host, Port: profile.Port,
		Username: profile.Username, Password: s.passwords[profileID], Enabled: profile.Enabled,
	}, nil
}

func (s *proxyProfileRepositoryStub) AssignmentCounts(context.Context) (map[domain.ID]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	counts := make(map[domain.ID]int, len(s.counts))
	for id, count := range s.counts {
		counts[id] = count
	}
	return counts, nil
}

func (s *proxyProfileRepositoryStub) AssignAccount(_ context.Context, accountID domain.ID, mode domain.ProxyMode, profileID *domain.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accounts == nil {
		return errors.New("account store is not configured")
	}
	s.accounts.mu.Lock()
	defer s.accounts.mu.Unlock()
	for index := range s.accounts.accounts {
		account := &s.accounts.accounts[index]
		if account.ID != accountID {
			continue
		}
		currentID := proxyRouteIDForTest(*account)
		nextID := domain.ID(domain.ProxyModeUnassigned)
		if mode == domain.ProxyModeGlobal {
			nextID = domain.SystemProxyRouteID
		} else if mode == domain.ProxyModeAssigned && profileID != nil {
			nextID = *profileID
		}
		if nextID != currentID && nextID != domain.SystemProxyRouteID && nextID != domain.ID(domain.ProxyModeUnassigned) && s.counts[nextID] >= proxyroutes.RouteCapacity {
			return domain.ErrProxyRouteFull
		}
		if currentID != domain.ID(domain.ProxyModeUnassigned) && currentID != nextID {
			s.counts[currentID]--
		}
		if nextID != domain.ID(domain.ProxyModeUnassigned) && currentID != nextID {
			s.counts[nextID]++
		}
		account.ProxyMode = mode
		if profileID == nil {
			account.ProxyProfileID = nil
		} else {
			copy := *profileID
			account.ProxyProfileID = &copy
		}
		if mode == domain.ProxyModeUnassigned {
			account.Status = domain.AccountStopped
		}
		return nil
	}
	return errors.New("account not found")
}

func (s *proxyProfileRepositoryStub) UpdateHealth(_ context.Context, profileID domain.ID, status, errorCode string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.profiles[profileID]
	if !ok {
		return errors.New("proxy profile not found")
	}
	profile.LastHealthStatus = status
	profile.LastError = errorCode
	profile.LastHealthAt = &at
	s.profiles[profileID] = profile
	return nil
}

func proxyRouteIDForTest(account domain.Account) domain.ID {
	if account.ProxyMode == domain.ProxyModeAssigned && account.ProxyProfileID != nil {
		return *account.ProxyProfileID
	}
	if account.ProxyMode == domain.ProxyModeGlobal || account.ProxyMode == "" {
		return domain.SystemProxyRouteID
	}
	return domain.ID(domain.ProxyModeUnassigned)
}

func testProxyRoutingBindings(t *testing.T, store *settingsStoreStub, repository *proxyProfileRepositoryStub, probe proxyroutes.Probe) (*Bindings, *proxyroutes.Registry) {
	t.Helper()
	registry := proxyroutes.NewRegistry(repository, domain.ProxyRoute{ID: domain.SystemProxyRouteID, Name: "Tor/Snowflake", Enabled: true}, proxyroutes.WithProbe(probe))
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureProxyRouting(repository, registry)
	return bindings, registry
}

func TestProxyProfileBindingsReturnOnlySafeDTOsAndPreservePasswordOnEmptyUpdate(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()}
	repository := newProxyProfileRepositoryStub(store, domain.ProxyProfile{
		ID: "profile-a", Name: "Private route", Protocol: "socks5", Host: "203.0.113.45", Port: 1080,
		Username: "alice", PasswordConfigured: true, Enabled: true,
	})
	repository.passwords["profile-a"] = "correct-horse-battery-staple"
	bindings, _ := testProxyRoutingBindings(t, store, repository, func(context.Context, domain.ProxyRoute, string) error { return nil })
	checked, err := bindings.CheckProxyProfile("profile-a")
	require.NoError(t, err)
	require.Equal(t, "ready", checked.State)

	profiles, err := bindings.ListProxyProfiles()
	require.NoError(t, err)
	require.Len(t, profiles, 2)
	require.Equal(t, string(domain.SystemProxyRouteID), profiles[0].ID)
	profile := profiles[1]
	require.Equal(t, "profile-a", profile.ID)
	require.True(t, profile.PasswordConfigured)
	require.Equal(t, "ready", profile.State)
	require.NotContains(t, profile.MaskedEndpoint, "203.0.113.45")
	require.Equal(t, profile.MaskedEndpoint, profile.Endpoint)

	payload, err := json.Marshal(profile)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "alice")
	require.NotContains(t, string(payload), "correct-horse-battery-staple")
	require.NotContains(t, string(payload), "ciphertext")

	saved, err := bindings.SaveProxyProfile(ProxyProfileInputDTO{
		Name: "New route", Protocol: "http", Host: "198.51.100.99", Port: 8080, Username: "bob", Password: "new-secret", Enabled: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, saved.ID)
	require.NotEqual(t, "New route", saved.ID)
	require.NotContains(t, saved.MaskedEndpoint, "198.51.100.99")
	require.NotNil(t, repository.savedPassword)
	require.Equal(t, "new-secret", *repository.savedPassword)

	_, err = bindings.SaveProxyProfile(ProxyProfileInputDTO{ID: saved.ID, Name: "Renamed route", Protocol: "http", Host: "198.51.100.99", Port: 8080, Enabled: true})
	require.NoError(t, err)
	require.Nil(t, repository.savedPassword)

	_, err = bindings.SaveProxyProfile(ProxyProfileInputDTO{ID: saved.ID, Name: "Renamed route", Protocol: "http", Host: "198.51.100.99", Port: 8080, Enabled: true, ClearPassword: true})
	require.NoError(t, err)
	require.NotNil(t, repository.savedPassword)
	require.Empty(t, *repository.savedPassword)
	require.NoError(t, bindings.DeleteProxyProfile(saved.ID))
}

func TestGetProxyStatusSanitizesManagedTransportAndError(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureProxyStatus(proxyStatusProviderStub{status: ProxyStatusDTO{
		Mode:      "direct",
		State:     "degraded",
		Address:   "198.51.100.9:443",
		Transport: "meek",
		LastError: "bridge_config_invalid cert=private-material endpoint=198.51.100.9",
	}})

	status := bindings.GetProxyStatus()
	require.Equal(t, "tor_snowflake", status.Mode)
	require.Equal(t, "snowflake", status.Transport)
	require.Equal(t, "127.0.0.1:19050", status.Address)
	require.Empty(t, status.LastError)

	payload, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "private-material")
	require.NotContains(t, string(payload), "198.51.100.9")
}

func TestGetProxyStatusKeepsStableManagedErrorCode(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureProxyStatus(proxyStatusProviderStub{status: ProxyStatusDTO{
		Mode: "tor_snowflake", State: "error", Address: "127.0.0.1:19050",
		Transport: "obfs4", LastError: "all_candidates_exhausted",
	}})

	status := bindings.GetProxyStatus()
	require.Equal(t, "tor_snowflake", status.Mode)
	require.Equal(t, "obfs4", status.Transport)
	require.Equal(t, "all_candidates_exhausted", status.LastError)
}

type unavailableProxyRouteRegistry struct{}

func (unavailableProxyRouteRegistry) Status(context.Context, domain.ID) (proxyroutes.Status, error) {
	return proxyroutes.Status{}, errors.New("registry unavailable")
}

func (unavailableProxyRouteRegistry) Check(context.Context, domain.ID) error {
	return errors.New("registry unavailable")
}

func TestManagedProxyStatusBindingSanitizesDegradedSecrets(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings()}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	bindings.ConfigureProxyStatus(proxyStatusProviderStub{status: ProxyStatusDTO{
		Mode:      "direct",
		State:     "degraded",
		Address:   "198.51.100.44:443",
		Transport: "obfs4",
		LastError: "managed_proxy_unavailable",
	}})

	got := bindings.GetProxyStatus()
	payload, err := json.Marshal(got)
	require.NoError(t, err)
	require.Equal(t, "tor_snowflake", got.Mode)
	require.Equal(t, "degraded", got.State)
	require.Equal(t, managedProxyLoopbackAddress, got.Address)
	require.Equal(t, "obfs4", got.Transport)
	require.Equal(t, "managed_proxy_unavailable", got.LastError)
	require.NotContains(t, string(payload), "198.51.100.44")
	require.NotContains(t, string(payload), "cert=")

	bindings.ConfigureProxyStatus(proxyStatusProviderStub{status: ProxyStatusDTO{
		State:     "error",
		Transport: "obfs4",
		LastError: "bridge_config_invalid cert=private-material endpoint=198.51.100.44:443",
	}})
	got = bindings.GetProxyStatus()
	require.Empty(t, got.LastError)
}

func TestProxyProfileDTOKeepsCustomRouteUnavailableWhenStatusFails(t *testing.T) {
	dto := proxyProfileDTO(context.Background(), domain.ProxyProfile{
		ID: "custom", Name: "Custom", Protocol: "http", Port: 8080, Enabled: true, LastHealthStatus: "ready",
	}, unavailableProxyRouteRegistry{})

	require.Equal(t, "unavailable", dto.State)
	require.Equal(t, proxyroutes.RouteCapacity, dto.Capacity)
}

func TestSaveProxyProfilePreservesHiddenEndpointFieldsWhenEditingMetadata(t *testing.T) {
	store := &settingsStoreStub{settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings()}
	repository := newProxyProfileRepositoryStub(store, domain.ProxyProfile{
		ID: "profile-a", Name: "Private route", Protocol: "http", Host: "203.0.113.45", Port: 8080,
		Username: "alice", PasswordConfigured: true, Enabled: true,
	})
	repository.passwords["profile-a"] = "secret"
	bindings, _ := testProxyRoutingBindings(t, store, repository, func(context.Context, domain.ProxyRoute, string) error { return nil })

	_, err := bindings.SaveProxyProfile(ProxyProfileInputDTO{ID: "profile-a", Name: "Renamed", Protocol: "http", Port: 1080, Enabled: true})
	require.NoError(t, err)
	route, err := repository.Route(context.Background(), "profile-a")
	require.NoError(t, err)
	require.Equal(t, "203.0.113.45", route.Host)
	require.Equal(t, 8080, route.Port)
	require.Equal(t, "alice", route.Username)
}

func TestAssignAccountProxyCustomRouteCapacityAndUnlimitedSystemRoute(t *testing.T) {
	customID := domain.ID("custom")
	store := &settingsStoreStub{
		settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings(),
		accounts: []domain.Account{
			{ID: "existing", ProxyMode: domain.ProxyModeAssigned, ProxyProfileID: &customID},
			{ID: "new", ProxyMode: domain.ProxyModeUnassigned},
		},
	}
	repository := newProxyProfileRepositoryStub(store,
		domain.ProxyProfile{ID: customID, Name: "Full route", Protocol: "socks5", Host: "203.0.113.2", Port: 1080, Enabled: true},
		domain.ProxyProfile{ID: "disabled", Name: "Disabled route", Protocol: "http", Host: "203.0.113.3", Port: 8080, Enabled: false},
		domain.ProxyProfile{ID: "degraded", Name: "Degraded route", Protocol: "http", Host: "203.0.113.4", Port: 8080, Enabled: true},
	)
	repository.counts[customID] = proxyroutes.RouteCapacity
	repository.counts[domain.SystemProxyRouteID] = proxyroutes.RouteCapacity
	bindings, registry := testProxyRoutingBindings(t, store, repository, func(_ context.Context, route domain.ProxyRoute, _ string) error {
		if route.ID == "degraded" {
			return errors.New("dial failed for credential-that-must-not-escape")
		}
		return nil
	})
	require.NoError(t, registry.Check(context.Background(), customID))
	require.Error(t, registry.Check(context.Background(), "degraded"))

	_, err := bindings.AssignAccountProxy("new", string(customID))
	require.ErrorContains(t, err, "capacity")
	require.NotContains(t, err.Error(), "203.0.113.2")

	updated, err := bindings.AssignAccountProxy("existing", string(customID))
	require.NoError(t, err)
	require.Equal(t, string(customID), updated.ProxyProfileID)

	_, err = bindings.AssignAccountProxy("new", "disabled")
	require.ErrorContains(t, err, "unavailable")
	_, err = bindings.AssignAccountProxy("new", "degraded")
	require.ErrorContains(t, err, "unavailable")
	updated, err = bindings.AssignAccountProxy("new", "")
	require.NoError(t, err)
	require.Equal(t, "Tor/Snowflake", updated.ProxyRouteName)
	require.Equal(t, proxyroutes.StateReady, proxyroutes.State(updated.ProxyRouteState))
	require.Zero(t, updated.ProxyRouteCapacity)
}

func TestProxyRoutingBindingsPublishAssignmentsAndExposeAssignedRouteOnAccounts(t *testing.T) {
	profileID := domain.ID("custom")
	store := &settingsStoreStub{
		settings: domain.DefaultKeywordSettings(), appSettings: domain.DefaultAppSettings(),
		accounts: []domain.Account{{ID: "account-a", ProxyMode: domain.ProxyModeAssigned, ProxyProfileID: &profileID}},
	}
	repository := newProxyProfileRepositoryStub(store, domain.ProxyProfile{ID: profileID, Name: "Custom route", Protocol: "http", Host: "198.51.100.5", Port: 8080, Enabled: true})
	repository.counts[profileID] = 1
	bindings, registry := testProxyRoutingBindings(t, store, repository, func(context.Context, domain.ProxyRoute, string) error { return nil })
	require.NoError(t, registry.Check(context.Background(), profileID))

	accounts, err := bindings.GetAccounts()
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, string(profileID), accounts[0].ProxyProfileID)
	require.Equal(t, "Custom route", accounts[0].ProxyRouteName)
	require.Equal(t, "ready", accounts[0].ProxyRouteState)
	require.Equal(t, 1, accounts[0].ProxyRouteUsage)
	require.Equal(t, proxyroutes.RouteCapacity, accounts[0].ProxyRouteCapacity)
	require.NotEqual(t, "Tor/Snowflake", accounts[0].Proxy)

	before := bindings.RuntimeStore().Current().Revision
	updated, err := bindings.AssignAccountProxy("account-a", "")
	require.NoError(t, err)
	require.Empty(t, updated.ProxyProfileID)
	require.Equal(t, "Tor/Snowflake", updated.ProxyRouteName)
	snapshot := bindings.RuntimeStore().Current()
	require.Equal(t, before+1, snapshot.Revision)
	require.Equal(t, string(domain.SystemProxyRouteID), snapshot.ProxyAssignments["account-a"])
}
