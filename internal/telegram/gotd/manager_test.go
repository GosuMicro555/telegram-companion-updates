package gotd

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
	proxyroutes "telegram-companion/internal/proxy/routes"
	"telegram-companion/internal/usecase/runtimeconfig"
)

type managerAccountRepo struct{ accounts []domain.Account }

func (r managerAccountRepo) ListActive(context.Context) ([]domain.Account, error) {
	return r.accounts, nil
}
func (r managerAccountRepo) List(context.Context) ([]domain.Account, error) { return r.accounts, nil }
func (r managerAccountRepo) Save(context.Context, domain.Account) error     { return nil }

type managerClient struct {
	mu        sync.Mutex
	runs      int
	runErrs   []error
	connected chan struct{}
}

type blockingManagerClient struct{}

func TestCloneRuntimeSnapshotDeepCopiesKeywordConfiguration(t *testing.T) {
	source := runtimeconfig.Snapshot{
		MinusKeywords: []string{"blocked phrase"},
		CanonicalTriggers: []runtimeconfig.CanonicalTrigger{{
			ID: "money", Canonical: "деньги", Forms: []string{"деньги", "денег"},
		}},
	}

	clone := cloneRuntimeSnapshot(source)
	clone.MinusKeywords[0] = "changed"
	clone.CanonicalTriggers[0].Forms[1] = "changed"

	require.Equal(t, []string{"blocked phrase"}, source.MinusKeywords)
	require.Equal(t, []string{"деньги", "денег"}, source.CanonicalTriggers[0].Forms)
}

func (blockingManagerClient) Run(ctx context.Context, _ func(context.Context) error) error {
	<-ctx.Done()
	return ctx.Err()
}
func (blockingManagerClient) API() *tg.Client                             { return nil }
func (blockingManagerClient) Validate(context.Context) (time.Time, error) { return time.Time{}, nil }

type blockingManagerFactory struct{}

func (blockingManagerFactory) New(domain.Account) (TelegramClient, error) {
	return blockingManagerClient{}, nil
}

func TestManagerBoundsConnectionBeforeValidatedCallback(t *testing.T) {
	status := make(chan AccountStatusEvent, 8)
	manager := newManager(
		managerAccountRepo{accounts: []domain.Account{{ID: "one", Role: domain.AccountRoleSpammer, Status: domain.AccountActive}}},
		blockingManagerFactory{}, &recordingActivator{}, func(event AccountStatusEvent) { status <- event },
		func(ctx context.Context, _ domain.ID, _ time.Duration) error { <-ctx.Done(); return ctx.Err() },
	)
	manager.connectionTimeout = 20 * time.Millisecond
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{Revision: 1, Roles: map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer}}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { _ = manager.Run(ctx) }()

	for {
		select {
		case event := <-status:
			if event.Status == RuntimeBackoff {
				require.ErrorIs(t, event.Err, ErrConnectionTimeout)
				return
			}
		case <-ctx.Done():
			t.Fatal("connection timeout status was not reported")
		}
	}
}

func (c *managerClient) Run(ctx context.Context, callback func(context.Context) error) error {
	c.mu.Lock()
	c.runs++
	var runErr error
	if len(c.runErrs) > 0 {
		runErr = c.runErrs[0]
		c.runErrs = c.runErrs[1:]
	}
	c.mu.Unlock()
	if runErr != nil {
		return runErr
	}
	select {
	case c.connected <- struct{}{}:
	default:
	}
	return callback(ctx)
}

func (c *managerClient) API() *tg.Client                             { return nil }
func (c *managerClient) Validate(context.Context) (time.Time, error) { return time.Time{}, nil }
func (c *managerClient) runCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runs
}

type managerFactory struct {
	mu      sync.Mutex
	clients map[domain.ID]*managerClient
	news    map[domain.ID]int
}

type routeFailureTrackerFake struct {
	mu       sync.Mutex
	routeID  domain.ID
	delay    time.Duration
	failures int
}

func (f *routeFailureTrackerFake) RecordFailure(routeID domain.ID, _ error) time.Duration {
	f.mu.Lock()
	f.routeID = routeID
	f.failures++
	f.mu.Unlock()
	return f.delay
}

func (*routeFailureTrackerFake) ReconnectDelay(position int) time.Duration {
	return time.Duration(position) * 300 * time.Millisecond
}

func TestManagerUsesRouteScopedBackoffAndReconnectStagger(t *testing.T) {
	tracker := &routeFailureTrackerFake{delay: 2 * time.Second}
	manager := &Manager{routes: tracker}

	require.Equal(t, 2600*time.Millisecond, manager.recordRouteFailure("route-a", 2, errors.New("transport")))
	require.Equal(t, 600*time.Millisecond, manager.routeReconnectDelay(2))
	tracker.mu.Lock()
	require.Equal(t, domain.ID("route-a"), tracker.routeID)
	tracker.mu.Unlock()
}

func TestManagerDoesNotExtendBackoffWhenRouteIsAlreadyUnavailable(t *testing.T) {
	tracker := &routeFailureTrackerFake{delay: 2 * time.Second}
	manager := &Manager{routes: tracker}

	delay := manager.recordRouteFailure(
		"route-a",
		2,
		errors.Join(errors.New("client creation failed"), proxyroutes.ErrRouteUnavailable),
	)

	require.Equal(t, 600*time.Millisecond, delay)
	tracker.mu.Lock()
	require.Zero(t, tracker.failures)
	tracker.mu.Unlock()
}

func TestManagerDoesNotDegradeRouteForAccountFloodWait(t *testing.T) {
	tracker := &routeFailureTrackerFake{delay: 2 * time.Second}
	manager := &Manager{routes: tracker}

	delay := manager.recordRouteFailure(
		"route-a",
		2,
		&FloodWaitError{AccountID: "account-a", Duration: 3 * time.Second, Err: errors.New("FLOOD_WAIT_3")},
	)

	require.Zero(t, delay)
	tracker.mu.Lock()
	require.Zero(t, tracker.failures)
	tracker.mu.Unlock()
}

func TestManagerStopsPermanentDuplicatedSessionWithoutDegradingRoute(t *testing.T) {
	duplicated := tgerr.New(406, "AUTH_KEY_DUPLICATED")
	client := &managerClient{runErrs: []error{duplicated}, connected: make(chan struct{}, 1)}
	tracker := &routeFailureTrackerFake{delay: 2 * time.Second}
	events := make(chan AccountStatusEvent, 8)
	var waitCalls atomic.Int32
	manager := newManager(
		managerAccountRepo{accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}}},
		&managerFactory{clients: map[domain.ID]*managerClient{"one": client}, news: map[domain.ID]int{}},
		&recordingActivator{}, func(event AccountStatusEvent) { events <- event },
		func(context.Context, domain.ID, time.Duration) error {
			waitCalls.Add(1)
			return nil
		},
	)
	manager.ConfigureRouteFailures(tracker)
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{
		Revision: 1,
		Roles:    map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer},
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	eventually(t, func() bool {
		select {
		case event := <-events:
			return event.Status == RuntimeSessionInvalid && errors.Is(event.Err, duplicated)
		default:
			return false
		}
	})
	require.Equal(t, 1, client.runCount())
	require.Zero(t, waitCalls.Load())
	tracker.mu.Lock()
	require.Zero(t, tracker.failures)
	tracker.mu.Unlock()

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

type lifecycleFactory struct {
	inner  *managerFactory
	mu     sync.Mutex
	resets int
}

func (f *lifecycleFactory) New(account domain.Account) (TelegramClient, error) {
	return f.inner.New(account)
}

func (f *lifecycleFactory) ResetExplicitLifecycle() {
	f.mu.Lock()
	f.resets++
	f.mu.Unlock()
}

func (f *lifecycleFactory) resetCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resets
}

func (f *managerFactory) New(account domain.Account) (TelegramClient, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.news[account.ID]++
	return f.clients[account.ID], nil
}

func (f *managerFactory) newCount(accountID domain.ID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.news[accountID]
}

type activationRecord struct {
	account  domain.ID
	revision uint64
	role     domain.AccountRole
	at       time.Time
}

type recordingActivator struct {
	mu      sync.Mutex
	records []activationRecord
}

type deadlineRecordingActivator struct {
	recordingActivator
	next  time.Time
	calls atomic.Int32
}

func (a *deadlineRecordingActivator) NextMembershipCheck(context.Context, domain.ID, runtimeconfig.Snapshot, time.Time) (*time.Time, error) {
	if a.calls.Add(1) != 1 {
		return nil, nil
	}
	next := a.next
	return &next, nil
}

type deadlineCrossingActivator struct {
	recordingActivator
	next  time.Time
	calls atomic.Int32
}

func (a *deadlineCrossingActivator) Apply(ctx context.Context, client TelegramClient, account domain.Account, snapshot runtimeconfig.Snapshot) error {
	a.recordingActivator.Apply(ctx, client, account, snapshot)
	if a.calls.Add(1) == 1 {
		a.next = time.Now().Add(10 * time.Millisecond)
		time.Sleep(25 * time.Millisecond)
	}
	return nil
}

func (a *deadlineCrossingActivator) NextMembershipCheck(_ context.Context, _ domain.ID, _ runtimeconfig.Snapshot, after time.Time) (*time.Time, error) {
	if !a.next.After(after) {
		return nil, nil
	}
	next := a.next
	return &next, nil
}

type blockingActivator struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (a *blockingActivator) Apply(ctx context.Context, _ TelegramClient, _ domain.Account, _ runtimeconfig.Snapshot) error {
	a.once.Do(func() { close(a.started) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-a.release:
		return nil
	}
}

type deactivationRecordingActivator struct {
	recordingActivator
	mu          sync.Mutex
	deactivated []domain.ID
}

type lifecycleActivator struct {
	recordingActivator
	mu     sync.Mutex
	resets int
}

func (a *lifecycleActivator) ResetExplicitLifecycle() {
	a.mu.Lock()
	a.resets++
	a.mu.Unlock()
}

func (a *lifecycleActivator) resetCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.resets
}

func (a *deactivationRecordingActivator) Deactivate(accountID domain.ID) {
	a.mu.Lock()
	a.deactivated = append(a.deactivated, accountID)
	a.mu.Unlock()
}

func (a *deactivationRecordingActivator) wasDeactivated(accountID domain.ID) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, candidate := range a.deactivated {
		if candidate == accountID {
			return true
		}
	}
	return false
}

type failThenActivate struct {
	mu       sync.Mutex
	failures int
	calls    int
}

func (a *failThenActivate) Apply(context.Context, TelegramClient, domain.Account, runtimeconfig.Snapshot) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.calls <= a.failures {
		return errors.New("activation failed")
	}
	return nil
}

func (a *failThenActivate) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func (a *recordingActivator) Apply(_ context.Context, _ TelegramClient, account domain.Account, snapshot runtimeconfig.Snapshot) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, activationRecord{account: account.ID, revision: snapshot.Revision, role: snapshot.Roles[account.ID], at: time.Now()})
	return nil
}

func (a *recordingActivator) has(account domain.ID, revision uint64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, record := range a.records {
		if record.account == account && record.revision == revision {
			return true
		}
	}
	return false
}

func (a *recordingActivator) count(account domain.ID, revision uint64) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	count := 0
	for _, record := range a.records {
		if record.account == account && record.revision == revision {
			count++
		}
	}
	return count
}

func (a *recordingActivator) hasAtOrAfter(account domain.ID, revision uint64, at time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, record := range a.records {
		if record.account == account && record.revision == revision && !record.at.Before(at) {
			return true
		}
	}
	return false
}

func TestManagerPeriodicallyReconcilesMembershipWithoutSnapshotChange(t *testing.T) {
	client := &managerClient{connected: make(chan struct{}, 1)}
	activator := &recordingActivator{}
	manager := NewManager(
		managerAccountRepo{accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}}},
		&managerFactory{clients: map[domain.ID]*managerClient{"one": client}, news: map[domain.ID]int{}},
		activator,
		nil,
	)
	manager.membershipRecheckInterval = 10 * time.Millisecond
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{
		Revision: 1,
		Roles:    map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer},
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	eventually(t, func() bool { return activator.count("one", 1) >= 2 })

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestManagerReconcilesAtFutureMembershipDeadlineBeforeFallbackPeriod(t *testing.T) {
	client := &managerClient{connected: make(chan struct{}, 1)}
	activator := &deadlineRecordingActivator{next: time.Now().Add(25 * time.Millisecond)}
	manager := NewManager(
		managerAccountRepo{accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}}},
		&managerFactory{clients: map[domain.ID]*managerClient{"one": client}, news: map[domain.ID]int{}},
		activator,
		nil,
	)
	manager.membershipRecheckInterval = 15 * time.Minute
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{
		Revision: 1, MembershipRevision: 1,
		Roles: map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer},
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	eventually(t, func() bool { return activator.hasAtOrAfter("one", 1, activator.next) })

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestManagerRetainsDeadlineThatPassesDuringMembershipApply(t *testing.T) {
	client := &managerClient{connected: make(chan struct{}, 1)}
	activator := &deadlineCrossingActivator{}
	manager := NewManager(
		managerAccountRepo{accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}}},
		&managerFactory{clients: map[domain.ID]*managerClient{"one": client}, news: map[domain.ID]int{}},
		activator,
		nil,
	)
	manager.membershipRecheckInterval = 15 * time.Minute
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{
		Revision: 1, MembershipRevision: 1,
		Roles: map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer},
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	eventually(t, func() bool { return activator.count("one", 1) >= 2 })

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestManagerReconcilesMembershipRevisionWithUnchangedCatalogAssignments(t *testing.T) {
	client := &managerClient{connected: make(chan struct{}, 1)}
	activator := &recordingActivator{}
	manager := NewManager(
		managerAccountRepo{accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}}},
		&managerFactory{clients: map[domain.ID]*managerClient{"one": client}, news: map[domain.ID]int{}},
		activator,
		nil,
	)
	first := runtimeconfig.Snapshot{
		Revision: 1, MembershipRevision: 1,
		Roles:              map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer},
		CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogOutbound: {"channel"}},
	}
	require.NoError(t, manager.Apply(first))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	eventually(t, func() bool { return activator.has("one", 1) })

	second := first
	second.Revision = 2
	second.MembershipRevision = 2
	require.NoError(t, manager.Apply(second))
	eventually(t, func() bool { return activator.has("one", 2) })

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestManagerReportsConnectedOnlyAfterActivatorCompletes(t *testing.T) {
	client := &managerClient{connected: make(chan struct{}, 1)}
	activator := &blockingActivator{started: make(chan struct{}), release: make(chan struct{})}
	events := make(chan AccountStatusEvent, 8)
	manager := NewManager(
		managerAccountRepo{accounts: []domain.Account{{ID: "scout", Status: domain.AccountActive}}},
		&managerFactory{clients: map[domain.ID]*managerClient{"scout": client}, news: map[domain.ID]int{}},
		activator,
		func(event AccountStatusEvent) { events <- event },
	)
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{
		Revision: 1,
		Roles:    map[domain.ID]domain.AccountRole{"scout": domain.AccountRoleScoutAnalyst},
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	<-activator.started

	for {
		select {
		case event := <-events:
			require.NotEqual(t, RuntimeConnected, event.Status)
		default:
			goto activated
		}
	}

activated:
	close(activator.release)
	eventually(t, func() bool {
		select {
		case event := <-events:
			return event.Status == RuntimeConnected
		default:
			return false
		}
	})

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestManagerDiffAppliesWithoutReconnectingUnaffectedClients(t *testing.T) {
	accounts := []domain.Account{
		{ID: "one", Status: domain.AccountActive},
		{ID: "two", Status: domain.AccountActive},
	}
	clients := map[domain.ID]*managerClient{
		"one": {connected: make(chan struct{}, 1)},
		"two": {connected: make(chan struct{}, 1)},
	}
	factory := &managerFactory{clients: clients, news: map[domain.ID]int{}}
	activator := &recordingActivator{}
	manager := NewManager(managerAccountRepo{accounts: accounts}, factory, activator, nil)
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{Revision: 1, Roles: map[domain.ID]domain.AccountRole{
		"one": domain.AccountRoleSpammer,
		"two": domain.AccountRoleSpammer,
	}}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	eventually(t, func() bool { return activator.has("one", 1) && activator.has("two", 1) })

	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{Revision: 2, Roles: map[domain.ID]domain.AccountRole{
		"one": domain.AccountRoleScoutAnalyst,
		"two": domain.AccountRoleSpammer,
	}}))
	eventually(t, func() bool { return activator.has("one", 2) && activator.has("two", 2) })
	require.Equal(t, 1, clients["one"].runCount())
	require.Equal(t, 1, clients["two"].runCount())
	require.Equal(t, 1, factory.news[domain.ID("one")])
	require.Equal(t, 1, factory.news[domain.ID("two")])

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestManagerRestartsOnlyAccountWhoseProxyRouteChanged(t *testing.T) {
	accounts := []domain.Account{
		{ID: "one", Status: domain.AccountActive, ProxyMode: domain.ProxyModeGlobal},
		{ID: "two", Status: domain.AccountActive, ProxyMode: domain.ProxyModeGlobal},
	}
	clients := map[domain.ID]*managerClient{
		"one": {connected: make(chan struct{}, 2)},
		"two": {connected: make(chan struct{}, 2)},
	}
	factory := &managerFactory{clients: clients, news: map[domain.ID]int{}}
	activator := &recordingActivator{}
	manager := NewManager(managerAccountRepo{accounts: accounts}, factory, activator, nil)
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{
		Revision:         1,
		Roles:            map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer, "two": domain.AccountRoleSpammer},
		ProxyAssignments: map[domain.ID]string{"one": "system", "two": "system"},
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	eventually(t, func() bool { return factory.newCount("one") == 1 && factory.newCount("two") == 1 })

	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{
		Revision:         2,
		Roles:            map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer, "two": domain.AccountRoleSpammer},
		ProxyAssignments: map[domain.ID]string{"one": "route-custom", "two": "system"},
	}))
	eventually(t, func() bool { return factory.newCount("one") == 2 })
	require.Equal(t, 1, factory.newCount("two"))

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestManagerApplyRefreshesGatewayAssignmentsWithoutReconnect(t *testing.T) {
	accounts := managerAccountRepo{accounts: []domain.Account{
		{ID: "first", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
		{ID: "second", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
	}}
	clients := map[domain.ID]*managerClient{
		"first":  {connected: make(chan struct{}, 1)},
		"second": {connected: make(chan struct{}, 1)},
	}
	factory := &managerFactory{clients: clients, news: map[domain.ID]int{}}
	activator := &recordingActivator{}
	rpc := &catalogRPCFake{channel: domain.Channel{ID: "entry-one", TelegramID: "-1001", Active: true}}
	gateway := NewCatalogGateway(
		accounts, &catalogRepoFake{}, &channelRepoFake{}, rpc,
		catalogAssignmentResolverFake{assigned: map[assignmentKey]bool{
			{account: "first", catalog: domain.SourceCatalogOutbound, channel: "entry-one"}:  true,
			{account: "second", catalog: domain.SourceCatalogOutbound, channel: "entry-two"}: true,
		}},
	)
	manager := NewManager(accounts, factory, activator, nil, gateway)
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{
		Revision: 1,
		Roles: map[domain.ID]domain.AccountRole{
			"first": domain.AccountRoleSpammer, "second": domain.AccountRoleSpammer,
		},
		CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogOutbound: {"entry-one"}},
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	eventually(t, func() bool { return activator.has("first", 1) && activator.has("second", 1) })
	_, err := gateway.ResolveAndJoin(context.Background(), domain.SourceCatalogOutbound, "@entry_one")
	require.NoError(t, err)
	require.Equal(t, []domain.ID{"first"}, rpc.joined)

	rpc.channel = domain.Channel{ID: "entry-two", TelegramID: "-1002", Active: true}
	rpc.checked = nil
	rpc.joined = nil
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{
		Revision: 2,
		Roles: map[domain.ID]domain.AccountRole{
			"first": domain.AccountRoleSpammer, "second": domain.AccountRoleSpammer,
		},
		CatalogAssignments: map[domain.SourceCatalog][]domain.ID{domain.SourceCatalogOutbound: {"entry-two"}},
	}))
	eventually(t, func() bool { return activator.has("first", 2) && activator.has("second", 2) })
	_, err = gateway.ResolveAndJoin(context.Background(), domain.SourceCatalogOutbound, "@entry_two")
	require.NoError(t, err)
	require.Equal(t, []domain.ID{"second"}, rpc.joined)
	require.Equal(t, 1, clients["first"].runCount())
	require.Equal(t, 1, clients["second"].runCount())

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestManagerRunsOnlyOneSupervisedClientPerAccountAcrossRepeatedApply(t *testing.T) {
	client := &managerClient{connected: make(chan struct{}, 1)}
	factory := &managerFactory{clients: map[domain.ID]*managerClient{"one": client}, news: map[domain.ID]int{}}
	activator := &recordingActivator{}
	manager := NewManager(managerAccountRepo{accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}}}, factory, activator, nil)
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{Revision: 1, Roles: map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer}}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	for revision := uint64(2); revision <= 10; revision++ {
		require.NoError(t, manager.Apply(runtimeconfig.Snapshot{
			Revision:       revision,
			OutboundPaused: revision%2 == 0,
			Roles:          map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer},
		}))
	}
	eventually(t, func() bool { return activator.has("one", 10) })
	require.Equal(t, 1, client.runCount())
	require.Equal(t, 1, factory.news[domain.ID("one")])

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestManagerDoesNotReactivateTelegramForReplyOnlyUpdate(t *testing.T) {
	client := &managerClient{connected: make(chan struct{}, 1)}
	factory := &managerFactory{clients: map[domain.ID]*managerClient{"one": client}, news: map[domain.ID]int{}}
	activator := &recordingActivator{}
	manager := NewManager(
		managerAccountRepo{accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}}},
		factory,
		activator,
		nil,
	)
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{
		Revision:    1,
		Roles:       map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer},
		SharedReply: "old reply",
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	eventually(t, func() bool { return activator.has("one", 1) })

	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{
		Revision:    2,
		Roles:       map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer},
		SharedReply: "new reply",
	}))
	require.Never(t, func() bool { return activator.has("one", 2) }, 100*time.Millisecond, time.Millisecond)
	require.Equal(t, 1, client.runCount())
	require.Equal(t, 1, factory.newCount("one"))

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

type floodOnceClient struct {
	managerClient
	wait time.Duration
}

func (c *floodOnceClient) Run(ctx context.Context, callback func(context.Context) error) error {
	c.mu.Lock()
	c.runs++
	run := c.runs
	c.mu.Unlock()
	if run == 1 {
		return &FloodWaitError{Duration: c.wait, Err: errors.New("FLOOD_WAIT")}
	}
	return callback(ctx)
}

type mixedFactory struct {
	flood *floodOnceClient
	other *managerClient
}

func (f mixedFactory) New(account domain.Account) (TelegramClient, error) {
	if account.ID == "flood" {
		return f.flood, nil
	}
	return f.other, nil
}

func TestManagerFloodWaitBackoffIsAccountScoped(t *testing.T) {
	flood := &floodOnceClient{managerClient: managerClient{connected: make(chan struct{}, 1)}, wait: 37 * time.Second}
	other := &managerClient{connected: make(chan struct{}, 1)}
	activator := &recordingActivator{}
	manager := newManager(
		managerAccountRepo{accounts: []domain.Account{{ID: "flood", Status: domain.AccountActive}, {ID: "other", Status: domain.AccountActive}}},
		mixedFactory{flood: flood, other: other}, activator, nil,
		func(_ context.Context, accountID domain.ID, duration time.Duration) error {
			require.Equal(t, domain.ID("flood"), accountID)
			require.Equal(t, 37*time.Second, duration)
			return nil
		},
	)
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{Revision: 1, Roles: map[domain.ID]domain.AccountRole{
		"flood": domain.AccountRoleSpammer,
		"other": domain.AccountRoleSpammer,
	}}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	eventually(t, func() bool { return flood.runCount() == 2 && activator.has("other", 1) })
	require.Equal(t, 1, other.runCount())

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestManagerReappliesLatestSnapshotAndIncreasesBackoffAfterActivationFailure(t *testing.T) {
	client := &managerClient{connected: make(chan struct{}, 1)}
	activator := &failThenActivate{failures: 2}
	var waitMu sync.Mutex
	var waits []time.Duration
	manager := newManager(
		managerAccountRepo{accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}}},
		&managerFactory{clients: map[domain.ID]*managerClient{"one": client}, news: map[domain.ID]int{}},
		activator, nil,
		func(_ context.Context, _ domain.ID, duration time.Duration) error {
			waitMu.Lock()
			waits = append(waits, duration)
			waitMu.Unlock()
			return nil
		},
	)
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{Revision: 1, Roles: map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer}}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	eventually(t, func() bool { return activator.callCount() == 3 })
	waitMu.Lock()
	require.Len(t, waits, 2)
	require.Greater(t, waits[1], waits[0])
	waitMu.Unlock()

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

type overlapTrackingClient struct {
	mu        sync.Mutex
	runs      int
	active    int
	maxActive int
	canceled  chan struct{}
}

func (c *overlapTrackingClient) Run(ctx context.Context, callback func(context.Context) error) error {
	c.mu.Lock()
	c.runs++
	c.active++
	if c.active > c.maxActive {
		c.maxActive = c.active
	}
	c.mu.Unlock()
	err := callback(ctx)
	select {
	case c.canceled <- struct{}{}:
	default:
	}
	time.Sleep(25 * time.Millisecond)
	c.mu.Lock()
	c.active--
	c.mu.Unlock()
	return err
}

func (c *overlapTrackingClient) API() *tg.Client                             { return nil }
func (c *overlapTrackingClient) Validate(context.Context) (time.Time, error) { return time.Time{}, nil }
func (c *overlapTrackingClient) counts() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runs, c.maxActive
}

type overlapFactory struct{ client *overlapTrackingClient }

func (f overlapFactory) New(domain.Account) (TelegramClient, error) { return f.client, nil }

func TestManagerNeverOverlapsClientsWhenAccountIsRemovedAndReadded(t *testing.T) {
	client := &overlapTrackingClient{canceled: make(chan struct{}, 1)}
	manager := NewManager(
		managerAccountRepo{accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}}},
		overlapFactory{client: client}, &recordingActivator{}, nil,
	)
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{Revision: 1, Roles: map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer}}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	eventually(t, func() bool { runs, _ := client.counts(); return runs == 1 })

	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{Revision: 2, Roles: map[domain.ID]domain.AccountRole{}}))
	<-client.canceled
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{Revision: 3, Roles: map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer}}))
	eventually(t, func() bool { runs, _ := client.counts(); return runs == 2 })
	_, maxActive := client.counts()
	require.Equal(t, 1, maxActive)

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestManagerRejectsStaleRevision(t *testing.T) {
	manager := NewManager(managerAccountRepo{}, &managerFactory{}, &recordingActivator{}, nil)
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{Revision: 4}))
	require.ErrorIs(t, manager.Apply(runtimeconfig.Snapshot{Revision: 4}), ErrStaleRevision)
	require.ErrorIs(t, manager.Apply(runtimeconfig.Snapshot{Revision: 3}), ErrStaleRevision)
}

func TestManagerCloseStopsWorkersAndRunCanReapplyLatestRevision(t *testing.T) {
	client := &managerClient{connected: make(chan struct{}, 1)}
	factory := &managerFactory{clients: map[domain.ID]*managerClient{"one": client}, news: map[domain.ID]int{}}
	activator := &recordingActivator{}
	manager := NewManager(managerAccountRepo{accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}}}, factory, activator, nil)
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{Revision: 1, Roles: map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer}}))

	firstDone := make(chan error, 1)
	go func() { firstDone <- manager.Run(context.Background()) }()
	eventually(t, func() bool { return client.runCount() == 1 })
	require.NoError(t, manager.Close())
	require.ErrorIs(t, <-firstDone, context.Canceled)

	secondDone := make(chan error, 1)
	go func() { secondDone <- manager.Run(context.Background()) }()
	eventually(t, func() bool { return client.runCount() == 2 })
	require.NoError(t, manager.Close())
	require.ErrorIs(t, <-secondDone, context.Canceled)
}

func TestManagerUnregistersClientWhenManagedRunStops(t *testing.T) {
	client := &managerClient{connected: make(chan struct{}, 1)}
	activator := &deactivationRecordingActivator{}
	manager := NewManager(
		managerAccountRepo{accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}}},
		&managerFactory{clients: map[domain.ID]*managerClient{"one": client}, news: map[domain.ID]int{}},
		activator,
		nil,
	)
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{Revision: 1, Roles: map[domain.ID]domain.AccountRole{"one": domain.AccountRoleSpammer}}))

	done := make(chan error, 1)
	go func() { done <- manager.Run(context.Background()) }()
	eventually(t, func() bool { return activator.has("one", 1) })
	require.NoError(t, manager.Close())
	require.ErrorIs(t, <-done, context.Canceled)
	require.True(t, activator.wasDeactivated("one"))
}

func TestManagerResetsLifecycleOnlyWhenExplicitRunEndsNotOnReconnect(t *testing.T) {
	client := &managerClient{connected: make(chan struct{}, 1), runErrs: []error{errors.New("transient disconnect")}}
	factory := &lifecycleFactory{inner: &managerFactory{clients: map[domain.ID]*managerClient{"one": client}, news: map[domain.ID]int{}}}
	activator := &lifecycleActivator{}
	manager := newManager(
		managerAccountRepo{accounts: []domain.Account{{ID: "one", Status: domain.AccountActive}}},
		factory,
		activator,
		nil,
		func(context.Context, domain.ID, time.Duration) error { return nil },
	)
	require.NoError(t, manager.Apply(runtimeconfig.Snapshot{Revision: 1, Roles: map[domain.ID]domain.AccountRole{"one": domain.AccountRoleScoutAnalyst}}))
	done := make(chan error, 1)
	go func() { done <- manager.Run(context.Background()) }()
	eventually(t, func() bool { return client.runCount() >= 2 && activator.has("one", 1) })
	require.Zero(t, factory.resetCount())
	require.Zero(t, activator.resetCount())

	require.NoError(t, manager.Close())
	require.ErrorIs(t, <-done, context.Canceled)
	require.Equal(t, 1, factory.resetCount())
	require.Equal(t, 1, activator.resetCount())
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met")
}
