package gotd

import (
	"context"
	"errors"
	"hash/fnv"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/gotd/td/tgerr"

	"telegram-companion/internal/domain"
	proxyroutes "telegram-companion/internal/proxy/routes"
	"telegram-companion/internal/usecase/runtimeconfig"
)

var (
	ErrManagerAlreadyRunning = errors.New("gotd manager is already running")
	ErrStaleRevision         = errors.New("runtime configuration revision is stale")
	ErrConnectionTimeout     = errors.New("gotd connection timed out before identity validation")
)

const (
	defaultConnectionTimeout       = 30 * time.Second
	defaultMembershipRecheckPeriod = 15 * time.Minute
)

type ManagedClientFactory interface {
	New(domain.Account) (TelegramClient, error)
}

type ClientActivator interface {
	Apply(context.Context, TelegramClient, domain.Account, runtimeconfig.Snapshot) error
}

type ClientDeactivator interface {
	Deactivate(domain.ID)
}

type ExplicitLifecycleResetter interface {
	ResetExplicitLifecycle()
}

type SnapshotConsumer interface {
	ApplySnapshot(runtimeconfig.Snapshot)
}

type AccountRuntimeStatus string

const (
	RuntimeConnecting     AccountRuntimeStatus = "connecting"
	RuntimeConnected      AccountRuntimeStatus = "connected"
	RuntimeBackoff        AccountRuntimeStatus = "backoff"
	RuntimeStopped        AccountRuntimeStatus = "stopped"
	RuntimeSessionInvalid AccountRuntimeStatus = "session_invalid"
)

type AccountStatusEvent struct {
	AccountID domain.ID
	RouteID   domain.ID
	Status    AccountRuntimeStatus
	Revision  uint64
	RetryIn   time.Duration
	Err       error
}

type StatusCallback func(AccountStatusEvent)

type accountWait func(context.Context, domain.ID, time.Duration) error

type RouteFailureTracker interface {
	RecordFailure(domain.ID, error) time.Duration
	ReconnectDelay(int) time.Duration
}

type Manager struct {
	accounts  domain.AccountRepository
	factory   ManagedClientFactory
	activator ClientActivator
	status    StatusCallback
	wait      accountWait
	consumers []SnapshotConsumer
	routes    RouteFailureTracker

	mu                        sync.Mutex
	revision                  uint64
	latest                    runtimeconfig.Snapshot
	running                   bool
	runCancel                 context.CancelFunc
	updates                   chan runtimeconfig.Snapshot
	connectionTimeout         time.Duration
	membershipRecheckInterval time.Duration
}

func (m *Manager) ConfigureRouteFailures(routes RouteFailureTracker) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.routes = routes
	m.mu.Unlock()
}

func NewManager(accounts domain.AccountRepository, factory ManagedClientFactory, activator ClientActivator, status StatusCallback, consumers ...SnapshotConsumer) *Manager {
	return newManager(accounts, factory, activator, status, waitAccount, consumers...)
}

func newManager(accounts domain.AccountRepository, factory ManagedClientFactory, activator ClientActivator, status StatusCallback, wait accountWait, consumers ...SnapshotConsumer) *Manager {
	return &Manager{
		accounts: accounts, factory: factory, activator: activator, status: status, wait: wait,
		consumers:                 append([]SnapshotConsumer(nil), consumers...),
		updates:                   make(chan runtimeconfig.Snapshot, 1),
		connectionTimeout:         defaultConnectionTimeout,
		membershipRecheckInterval: defaultMembershipRecheckPeriod,
	}
}

func (m *Manager) Apply(snapshot runtimeconfig.Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if snapshot.Revision <= m.revision {
		return ErrStaleRevision
	}
	reconcile := m.revision == 0 || !sameTelegramActivationConfig(m.latest, snapshot)
	m.revision = snapshot.Revision
	m.latest = cloneRuntimeSnapshot(snapshot)
	for _, consumer := range m.consumers {
		if consumer != nil {
			consumer.ApplySnapshot(cloneRuntimeSnapshot(m.latest))
		}
	}
	if reconcile {
		replaceManagerSnapshot(m.updates, cloneRuntimeSnapshot(m.latest))
	}
	return nil
}

func sameTelegramActivationConfig(current, next runtimeconfig.Snapshot) bool {
	if current.OutboundPaused != next.OutboundPaused ||
		!maps.Equal(current.Roles, next.Roles) ||
		!maps.Equal(current.ProxyAssignments, next.ProxyAssignments) ||
		len(current.CatalogAssignments) != len(next.CatalogAssignments) {
		return false
	}
	for catalog, currentIDs := range current.CatalogAssignments {
		if !slices.Equal(currentIDs, next.CatalogAssignments[catalog]) {
			return false
		}
	}
	return true
}

func (m *Manager) Run(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return ErrManagerAlreadyRunning
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.running = true
	m.runCancel = cancel
	if m.revision > 0 {
		replaceManagerSnapshot(m.updates, cloneRuntimeSnapshot(m.latest))
	}
	m.mu.Unlock()
	defer func() {
		cancel()
		m.resetExplicitLifecycle()
		m.mu.Lock()
		m.running = false
		m.runCancel = nil
		m.mu.Unlock()
	}()

	workers := make(map[domain.ID]*managedAccount)
	var workerGroup sync.WaitGroup
	for {
		select {
		case <-runCtx.Done():
			for _, worker := range workers {
				worker.cancel()
			}
			workerGroup.Wait()
			return runCtx.Err()
		case snapshot := <-m.updates:
			if err := m.reconcile(runCtx, snapshot, workers, &workerGroup); err != nil {
				for _, worker := range workers {
					worker.cancel()
				}
				workerGroup.Wait()
				return err
			}
		}
	}
}

func (m *Manager) resetExplicitLifecycle() {
	if resetter, ok := m.activator.(ExplicitLifecycleResetter); ok {
		resetter.ResetExplicitLifecycle()
	}
	if resetter, ok := m.factory.(ExplicitLifecycleResetter); ok {
		resetter.ResetExplicitLifecycle()
	}
}

func (m *Manager) Close() error {
	m.mu.Lock()
	cancel := m.runCancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

type managedAccount struct {
	account  domain.Account
	routeID  domain.ID
	position int
	cancel   context.CancelFunc
	done     chan struct{}

	mu      sync.RWMutex
	latest  runtimeconfig.Snapshot
	updates chan struct{}
}

func (a *managedAccount) publish(snapshot runtimeconfig.Snapshot) {
	a.mu.Lock()
	a.latest = cloneRuntimeSnapshot(snapshot)
	a.mu.Unlock()
	replaceManagerSignal(a.updates)
}

func (a *managedAccount) snapshot() runtimeconfig.Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneRuntimeSnapshot(a.latest)
}

func (m *Manager) reconcile(ctx context.Context, snapshot runtimeconfig.Snapshot, workers map[domain.ID]*managedAccount, group *sync.WaitGroup) error {
	accounts, err := m.accounts.ListActive(ctx)
	if err != nil {
		return err
	}
	type desiredAccount struct {
		account  domain.Account
		routeID  domain.ID
		position int
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	desired := make(map[domain.ID]desiredAccount, len(accounts))
	routePositions := make(map[domain.ID]int)
	for _, account := range accounts {
		role, assigned := snapshot.Roles[account.ID]
		if !assigned || !account.Eligible() || !managedRole(role) {
			continue
		}
		account.Role = role
		account, routeID, routable := accountWithRuntimeRoute(account, snapshot.ProxyAssignments)
		if !routable {
			continue
		}
		position := routePositions[routeID]
		routePositions[routeID] = position + 1
		desired[account.ID] = desiredAccount{account: account, routeID: routeID, position: position}
	}
	for accountID, worker := range workers {
		candidate, keep := desired[accountID]
		if keep && candidate.routeID == worker.routeID {
			worker.publish(snapshot)
			continue
		}
		worker.cancel()
		select {
		case <-worker.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		delete(workers, accountID)
	}
	for accountID, candidate := range desired {
		if _, exists := workers[accountID]; exists {
			continue
		}
		workerCtx, cancel := context.WithCancel(ctx) //nolint:gosec // cancel is retained by the managed worker and called on removal/shutdown.
		worker := &managedAccount{
			account:  candidate.account,
			routeID:  candidate.routeID,
			position: candidate.position,
			cancel:   cancel,
			done:     make(chan struct{}),
			updates:  make(chan struct{}, 1),
		}
		worker.publish(snapshot)
		workers[accountID] = worker
		group.Add(1)
		go func() {
			defer group.Done()
			defer close(worker.done)
			m.supervise(workerCtx, worker)
		}()
	}
	return nil
}

func accountWithRuntimeRoute(account domain.Account, assignments map[domain.ID]string) (domain.Account, domain.ID, bool) {
	if routeID := domain.ID(assignments[account.ID]); routeID != "" {
		switch routeID {
		case domain.SystemProxyRouteID:
			account.ProxyMode = domain.ProxyModeGlobal
			account.ProxyProfileID = nil
			return account, routeID, true
		case domain.ID(domain.ProxyModeUnassigned):
			account.ProxyMode = domain.ProxyModeUnassigned
			account.ProxyProfileID = nil
			return account, routeID, false
		default:
			account.ProxyMode = domain.ProxyModeAssigned
			account.ProxyProfileID = &routeID
			return account, routeID, true
		}
	}
	switch account.ProxyMode {
	case "", domain.ProxyModeGlobal:
		return account, domain.SystemProxyRouteID, true
	case domain.ProxyModeAssigned:
		if account.ProxyProfileID != nil && *account.ProxyProfileID != "" {
			return account, *account.ProxyProfileID, true
		}
	}
	return account, "", false
}

func (m *Manager) supervise(ctx context.Context, worker *managedAccount) {
	attempt := 0
	if delay := m.routeReconnectDelay(worker.position); delay > 0 {
		if err := m.wait(ctx, worker.account.ID, delay); err != nil {
			m.report(AccountStatusEvent{AccountID: worker.account.ID, RouteID: worker.routeID, Status: RuntimeStopped, Err: err})
			return
		}
	}
	for {
		if ctx.Err() != nil {
			m.report(AccountStatusEvent{AccountID: worker.account.ID, RouteID: worker.routeID, Status: RuntimeStopped})
			return
		}
		m.report(AccountStatusEvent{AccountID: worker.account.ID, RouteID: worker.routeID, Status: RuntimeConnecting})
		client, err := m.factory.New(worker.account)
		if err == nil {
			attemptCtx, attemptCancel := context.WithCancel(ctx)
			connected := make(chan struct{})
			timedOut := make(chan struct{})
			timer := time.AfterFunc(m.connectionTimeout, func() {
				close(timedOut)
				attemptCancel()
			})
			err = client.Run(attemptCtx, func(runCtx context.Context) error {
				timer.Stop()
				close(connected)
				recheckTicker := time.NewTicker(m.membershipRecheckInterval)
				defer recheckTicker.Stop()
				var appliedRevision uint64
				recheckMembership := false
				connectedReported := false
				for {
					snapshot := worker.snapshot()
					if snapshot.Revision > appliedRevision || recheckMembership {
						account := worker.account
						account.Role = snapshot.Roles[account.ID]
						if applyErr := m.activator.Apply(runCtx, client, account, snapshot); applyErr != nil {
							return applyErr
						}
						appliedRevision = snapshot.Revision
						recheckMembership = false
						if !connectedReported {
							m.report(AccountStatusEvent{AccountID: worker.account.ID, RouteID: worker.routeID, Status: RuntimeConnected})
							connectedReported = true
						}
					}
					select {
					case <-runCtx.Done():
						return runCtx.Err()
					case <-worker.updates:
					case <-recheckTicker.C:
						recheckMembership = true
					}
				}
			})
			if deactivator, ok := m.activator.(ClientDeactivator); ok {
				deactivator.Deactivate(worker.account.ID)
			}
			timer.Stop()
			attemptCancel()
			select {
			case <-timedOut:
				select {
				case <-connected:
				default:
					err = ErrConnectionTimeout
				}
			default:
			}
		}
		if ctx.Err() != nil {
			m.report(AccountStatusEvent{AccountID: worker.account.ID, RouteID: worker.routeID, Status: RuntimeStopped})
			return
		}
		if isPermanentSessionError(err) {
			m.report(AccountStatusEvent{AccountID: worker.account.ID, RouteID: worker.routeID, Status: RuntimeSessionInvalid, Err: err})
			return
		}
		delay := accountBackoff(worker.account.ID, attempt, err)
		if routeDelay := m.recordRouteFailure(worker.routeID, worker.position, err); routeDelay > delay {
			delay = routeDelay
		}
		m.report(AccountStatusEvent{AccountID: worker.account.ID, RouteID: worker.routeID, Status: RuntimeBackoff, RetryIn: delay, Err: err})
		if waitErr := m.wait(ctx, worker.account.ID, delay); waitErr != nil {
			m.report(AccountStatusEvent{AccountID: worker.account.ID, RouteID: worker.routeID, Status: RuntimeStopped, Err: waitErr})
			return
		}
		attempt++
	}
}

func (m *Manager) routeReconnectDelay(position int) time.Duration {
	m.mu.Lock()
	routes := m.routes
	m.mu.Unlock()
	if routes == nil {
		return 0
	}
	return routes.ReconnectDelay(position)
}

func (m *Manager) recordRouteFailure(routeID domain.ID, position int, err error) time.Duration {
	m.mu.Lock()
	routes := m.routes
	m.mu.Unlock()
	if routes == nil || routeID == "" || err == nil || isPermanentSessionError(err) {
		return 0
	}
	if errors.Is(err, proxyroutes.ErrRouteUnavailable) {
		return routes.ReconnectDelay(position)
	}
	var flood *FloodWaitError
	if errors.As(err, &flood) {
		return 0
	}
	return routes.RecordFailure(routeID, err) + routes.ReconnectDelay(position)
}

func isPermanentSessionError(err error) bool {
	return tgerr.Is(err, "AUTH_KEY_DUPLICATED")
}

func managedRole(role domain.AccountRole) bool {
	return role == domain.AccountRoleSpammer || role == domain.AccountRoleScoutAnalyst
}

func (m *Manager) report(event AccountStatusEvent) {
	if m.status != nil {
		m.status(event)
	}
}

func waitAccount(ctx context.Context, _ domain.ID, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func accountBackoff(accountID domain.ID, attempt int, err error) time.Duration {
	var flood *FloodWaitError
	if errors.As(err, &flood) && flood.Duration > 0 {
		return flood.Duration
	}
	if attempt > 6 {
		attempt = 6
	}
	base := 500 * time.Millisecond * time.Duration(1<<attempt)
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(accountID))
	jitter := time.Duration(hash.Sum32()%251) * base / 1000
	return base + jitter
}

func replaceManagerSnapshot(target chan runtimeconfig.Snapshot, snapshot runtimeconfig.Snapshot) {
	select {
	case target <- snapshot:
		return
	default:
	}
	select {
	case <-target:
	default:
	}
	select {
	case target <- snapshot:
	default:
	}
}

func replaceManagerSignal(target chan struct{}) {
	select {
	case target <- struct{}{}:
	default:
	}
}

func cloneRuntimeSnapshot(snapshot runtimeconfig.Snapshot) runtimeconfig.Snapshot {
	clone := snapshot
	clone.Roles = make(map[domain.ID]domain.AccountRole, len(snapshot.Roles))
	for accountID, role := range snapshot.Roles {
		clone.Roles[accountID] = role
	}
	clone.CatalogAssignments = make(map[domain.SourceCatalog][]domain.ID, len(snapshot.CatalogAssignments))
	for catalog, assignments := range snapshot.CatalogAssignments {
		clone.CatalogAssignments[catalog] = append([]domain.ID(nil), assignments...)
	}
	clone.Keywords = append([]string(nil), snapshot.Keywords...)
	clone.MinusKeywords = append([]string(nil), snapshot.MinusKeywords...)
	clone.CanonicalTriggers = make([]runtimeconfig.CanonicalTrigger, len(snapshot.CanonicalTriggers))
	for index, trigger := range snapshot.CanonicalTriggers {
		clone.CanonicalTriggers[index] = trigger
		clone.CanonicalTriggers[index].Forms = append([]string(nil), trigger.Forms...)
	}
	clone.DirectMessageKeywords = append([]string(nil), snapshot.DirectMessageKeywords...)
	clone.ProxyAssignments = make(map[domain.ID]string, len(snapshot.ProxyAssignments))
	for accountID, proxy := range snapshot.ProxyAssignments {
		clone.ProxyAssignments[accountID] = proxy
	}
	return clone
}
