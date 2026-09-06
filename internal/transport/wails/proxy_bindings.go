package wails

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"telegram-companion/internal/domain"
	proxyroutes "telegram-companion/internal/proxy/routes"
	"telegram-companion/internal/usecase/runtimeconfig"
)

const managedProxyLoopbackAddress = "127.0.0.1:19050"

type ProxyRouteRegistry interface {
	Status(context.Context, domain.ID) (proxyroutes.Status, error)
	Check(context.Context, domain.ID) error
}

type ProxyProfileDTO struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Protocol           string `json:"protocol"`
	MaskedEndpoint     string `json:"maskedEndpoint"`
	Endpoint           string `json:"endpoint"`
	State              string `json:"state"`
	Usage              int    `json:"usage"`
	Capacity           int    `json:"capacity"`
	Error              string `json:"error"`
	LastError          string `json:"lastError"`
	Enabled            bool   `json:"enabled"`
	PasswordConfigured bool   `json:"passwordConfigured"`
}

type ProxyProfileInputDTO struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Protocol      string `json:"protocol"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	ClearPassword bool   `json:"clearPassword"`
	Enabled       bool   `json:"enabled"`
}

func (b *Bindings) ConfigureProxyRouting(repository domain.ProxyProfileRepository, registry ProxyRouteRegistry) {
	b.proxyProfiles = repository
	b.proxyRoutes = registry
}

func (b *Bindings) ListProxyProfiles() ([]ProxyProfileDTO, error) {
	repository, registry, err := b.proxyRouting()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 15*time.Second)
	defer cancel()
	profiles, err := repository.List(ctx)
	if err != nil {
		return nil, safeProxyError(err)
	}
	result := make([]ProxyProfileDTO, 0, len(profiles)+1)
	result = append(result, systemProxyProfileDTO(ctx, registry))
	for _, profile := range profiles {
		result = append(result, proxyProfileDTO(ctx, profile, registry))
	}
	return result, nil
}

func (b *Bindings) SaveProxyProfile(input ProxyProfileInputDTO) (ProxyProfileDTO, error) {
	repository, registry, err := b.proxyRouting()
	if err != nil {
		return ProxyProfileDTO{}, err
	}
	id := domain.ID(strings.TrimSpace(input.ID))
	creating := id == ""
	if creating {
		id, err = newProxyProfileID()
		if err != nil {
			return ProxyProfileDTO{}, errors.New("generate proxy profile id")
		}
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 15*time.Second)
	defer cancel()
	profile := domain.ProxyProfile{
		ID: id, Name: strings.TrimSpace(input.Name), Protocol: strings.ToLower(strings.TrimSpace(input.Protocol)),
		Host: strings.TrimSpace(input.Host), Port: input.Port, Username: strings.TrimSpace(input.Username), Enabled: input.Enabled,
		UpdatedAt: time.Now().UTC(),
	}
	if !creating && (profile.Host == "" || profile.Port == 0 || profile.Username == "") {
		existing, routeErr := repository.Route(ctx, id)
		if routeErr != nil {
			return ProxyProfileDTO{}, safeProxyError(routeErr)
		}
		if profile.Host == "" {
			profile.Host = existing.Host
			profile.Port = existing.Port
		} else if profile.Port == 0 {
			profile.Port = existing.Port
		}
		if profile.Username == "" {
			profile.Username = existing.Username
		}
	}
	if creating {
		profile.CreatedAt = profile.UpdatedAt
	}
	password := proxyProfilePassword(input, creating)
	if err := repository.Save(ctx, profile, password); err != nil {
		return ProxyProfileDTO{}, safeProxyError(err)
	}
	if err := b.publishProxyAssignments(ctx); err != nil {
		return ProxyProfileDTO{}, safeProxyError(err)
	}
	profiles, err := repository.List(ctx)
	if err != nil {
		return ProxyProfileDTO{}, safeProxyError(err)
	}
	for _, saved := range profiles {
		if saved.ID == id {
			return proxyProfileDTO(ctx, saved, registry), nil
		}
	}
	return ProxyProfileDTO{}, errors.New("proxy profile was not saved")
}

func (b *Bindings) DeleteProxyProfile(id string) error {
	repository, _, err := b.proxyRouting()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 15*time.Second)
	defer cancel()
	if err := repository.Delete(ctx, domain.ID(strings.TrimSpace(id))); err != nil {
		return safeProxyError(err)
	}
	return nil
}

func (b *Bindings) CheckProxyProfile(id string) (ProxyProfileDTO, error) {
	repository, registry, err := b.proxyRouting()
	if err != nil {
		return ProxyProfileDTO{}, err
	}
	profileID := domain.ID(strings.TrimSpace(id))
	if profileID == "" || profileID == domain.SystemProxyRouteID {
		return ProxyProfileDTO{}, errors.New("system proxy route cannot be checked here")
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 15*time.Second)
	defer cancel()
	checkErr := registry.Check(ctx, profileID)
	profiles, listErr := repository.List(ctx)
	if listErr != nil {
		return ProxyProfileDTO{}, safeProxyError(listErr)
	}
	for _, profile := range profiles {
		if profile.ID == profileID {
			return proxyProfileDTO(ctx, profile, registry), safeProxyError(checkErr)
		}
	}
	return ProxyProfileDTO{}, errors.New("proxy profile not found")
}

func (b *Bindings) AssignAccountProxy(accountID, profileID string) (AccountDTO, error) {
	repository, registry, err := b.proxyRouting()
	if err != nil {
		return AccountDTO{}, err
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 15*time.Second)
	defer cancel()
	accounts, err := b.settings.ListAccounts(ctx)
	if err != nil {
		return AccountDTO{}, safeProxyError(err)
	}
	account, found := findAccount(accounts, domain.ID(strings.TrimSpace(accountID)))
	if !found {
		return AccountDTO{}, errors.New("account not found")
	}

	mode, routeID := requestedProxyRoute(profileID)
	status, err := registry.Status(ctx, routeID)
	if err != nil {
		return AccountDTO{}, errors.New("proxy route unavailable")
	}
	currentRouteID := domain.ID(accountProxyRouteID(account))
	if !routeAcceptsAssignment(status, currentRouteID == routeID) {
		if status.State == proxyroutes.StateFull {
			return AccountDTO{}, domain.ErrProxyRouteFull
		}
		return AccountDTO{}, errors.New("proxy route unavailable")
	}
	var selected *domain.ID
	if mode == domain.ProxyModeAssigned {
		selected = &routeID
	}
	if err := repository.AssignAccount(ctx, account.ID, mode, selected); err != nil {
		return AccountDTO{}, safeProxyError(err)
	}
	if err := b.publishProxyAssignments(ctx); err != nil {
		return AccountDTO{}, safeProxyError(err)
	}
	accounts, err = b.settings.ListAccounts(ctx)
	if err != nil {
		return AccountDTO{}, safeProxyError(err)
	}
	account, found = findAccount(accounts, account.ID)
	if !found {
		return AccountDTO{}, errors.New("account not found")
	}
	return b.proxyAccountDTO(ctx, account), nil
}

func (b *Bindings) proxyRouting() (domain.ProxyProfileRepository, ProxyRouteRegistry, error) {
	if err := b.runtimeError(); err != nil {
		return nil, nil, err
	}
	if b.proxyProfiles == nil || b.proxyRoutes == nil {
		return nil, nil, errors.New("proxy routing is not configured")
	}
	return b.proxyProfiles, b.proxyRoutes, nil
}

func proxyProfilePassword(input ProxyProfileInputDTO, creating bool) *string {
	if creating || input.ClearPassword || input.Password != "" {
		password := input.Password
		return &password
	}
	return nil
}

func newProxyProfileID() (domain.ID, error) {
	var bytes [18]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return domain.ID("proxy-" + hex.EncodeToString(bytes[:])), nil
}

func proxyProfileDTO(ctx context.Context, profile domain.ProxyProfile, registry ProxyRouteRegistry) ProxyProfileDTO {
	state := strings.TrimSpace(profile.LastHealthStatus)
	if state == "" {
		state = string(proxyroutes.StateChecking)
	}
	result := ProxyProfileDTO{
		ID: string(profile.ID), Name: profile.Name, Protocol: profile.Protocol, MaskedEndpoint: maskedProxyEndpoint(profile.Protocol, profile.Port),
		Endpoint: maskedProxyEndpoint(profile.Protocol, profile.Port), State: state, Error: safeProxyErrorCode(profile.LastError),
		LastError: safeProxyErrorCode(profile.LastError), Enabled: profile.Enabled, PasswordConfigured: profile.PasswordConfigured, Capacity: proxyroutes.RouteCapacity,
	}
	status, err := registry.Status(ctx, profile.ID)
	if err != nil {
		result.State = "unavailable"
		return result
	}
	result.State = string(status.State)
	result.Usage = status.Usage
	result.Capacity = status.Capacity
	result.Error = safeProxyErrorCode(status.LastError)
	result.LastError = result.Error
	return result
}

func systemProxyProfileDTO(ctx context.Context, registry ProxyRouteRegistry) ProxyProfileDTO {
	result := ProxyProfileDTO{
		ID: string(domain.SystemProxyRouteID), Name: "Tor/Snowflake", Protocol: "socks5", MaskedEndpoint: "socks5://***",
		Endpoint: "socks5://***", State: string(proxyroutes.StateChecking), Enabled: true,
	}
	status, err := registry.Status(ctx, domain.SystemProxyRouteID)
	if err != nil {
		return result
	}
	result.Name = status.Name
	result.State = string(status.State)
	result.Usage = status.Usage
	result.Capacity = status.Capacity
	result.Error = safeProxyErrorCode(status.LastError)
	result.LastError = result.Error
	return result
}

func maskedProxyEndpoint(protocol string, port int) string {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		protocol = "proxy"
	}
	return fmt.Sprintf("%s://***:%d", protocol, port)
}

func requestedProxyRoute(profileID string) (domain.ProxyMode, domain.ID) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return domain.ProxyModeGlobal, domain.SystemProxyRouteID
	}
	return domain.ProxyModeAssigned, domain.ID(profileID)
}

func routeAcceptsAssignment(status proxyroutes.Status, alreadyAssigned bool) bool {
	if status.State == proxyroutes.StateFull {
		return alreadyAssigned
	}
	return status.State == proxyroutes.StateReady && (status.Capacity == 0 || status.Usage < status.Capacity)
}

func findAccount(accounts []domain.Account, id domain.ID) (domain.Account, bool) {
	for _, account := range accounts {
		if account.ID == id {
			return account, true
		}
	}
	return domain.Account{}, false
}

func safeProxyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, domain.ErrProxyRouteFull) {
		return domain.ErrProxyRouteFull
	}
	if errors.Is(err, proxyroutes.ErrRouteUnavailable) {
		return errors.New("proxy route unavailable")
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "not found"):
		return errors.New("proxy profile not found")
	case strings.Contains(message, "assigned to accounts"):
		return errors.New("proxy profile is assigned to accounts")
	case strings.Contains(message, "name already exists"):
		return errors.New("proxy profile name already exists")
	case strings.Contains(message, "endpoint already exists"):
		return errors.New("proxy profile endpoint already exists")
	case strings.Contains(message, "profile id and name are required"), strings.Contains(message, "protocol must be"), strings.Contains(message, "valid proxy host and port"):
		return errors.New("invalid proxy profile")
	case strings.Contains(message, "account not found"):
		return errors.New("account not found")
	default:
		return errors.New("proxy operation failed")
	}
}

func safeProxyErrorCode(code string) string {
	switch strings.TrimSpace(code) {
	case "transport_failure", "retry_required", "health_check_failed",
		"managed_proxy_unavailable", "bridge_config_invalid", "all_candidates_exhausted",
		"bootstrap_failed", "socks_probe_failed", "proxy_start_failed", "proxy_exited",
		"transport_alias_unavailable", "state_io":
		return strings.TrimSpace(code)
	default:
		return ""
	}
}

func sanitizeManagedProxyStatus(status ProxyStatusDTO) ProxyStatusDTO {
	status.Mode = "tor_snowflake"
	switch status.State {
	case "stopped", "starting", "ready", "degraded", "error":
	default:
		status.State = "error"
	}
	switch status.Transport {
	case "obfs4", "snowflake":
	default:
		status.Transport = "snowflake"
	}
	// The managed route is intentionally loopback-only. Never reflect an
	// untrusted or accidentally misconfigured address through the binding.
	status.Address = managedProxyLoopbackAddress
	status.LastError = safeProxyErrorCode(status.LastError)
	return status
}

func (b *Bindings) publishProxyAssignments(ctx context.Context) error {
	accounts, err := b.settings.ListAccounts(ctx)
	if err != nil {
		return err
	}
	b.runtime.Update(func(next *runtimeconfig.Snapshot) {
		next.ProxyAssignments = make(map[domain.ID]string, len(accounts))
		for _, account := range accounts {
			next.ProxyAssignments[account.ID] = accountProxyRouteID(account)
		}
	})
	return nil
}

func (b *Bindings) proxyAccountDTOs(ctx context.Context, accounts []domain.Account) []AccountDTO {
	result := make([]AccountDTO, len(accounts))
	for index, account := range accounts {
		result[index] = b.proxyAccountDTO(ctx, account)
	}
	return result
}

func (b *Bindings) proxyAccountDTO(ctx context.Context, account domain.Account) AccountDTO {
	result := accountDTO(account, b.runtime.Current().Revision, b.proxyStatus != nil, b.GetProxyStatus().State)
	routeID := domain.ID(accountProxyRouteID(account))
	if routeID == domain.ID(domain.ProxyModeUnassigned) {
		result.ProxyRouteState = string(domain.ProxyModeUnassigned)
		return result
	}
	if account.ProxyMode == domain.ProxyModeAssigned && account.ProxyProfileID != nil {
		result.ProxyProfileID = string(*account.ProxyProfileID)
	}
	if b.proxyRoutes == nil {
		return result
	}
	status, err := b.proxyRoutes.Status(ctx, routeID)
	if err != nil {
		result.ProxyWarning = "route_unavailable"
		result.ProxyRouteState = "unavailable"
		return result
	}
	result.Proxy = status.Name
	result.ProxyRouteName = status.Name
	result.ProxyRouteState = string(status.State)
	result.ProxyRouteUsage = status.Usage
	result.ProxyRouteCapacity = status.Capacity
	if status.State != proxyroutes.StateReady && status.State != proxyroutes.StateFull {
		result.ProxyWarning = "route_unavailable"
	}
	return result
}
