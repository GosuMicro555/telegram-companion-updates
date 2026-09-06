package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/telegram/fake"
)

type task8AccountRepoStub struct{ accounts []domain.Account }

func (r task8AccountRepoStub) ListActive(context.Context) ([]domain.Account, error) {
	return r.accounts, nil
}
func (r task8AccountRepoStub) List(context.Context) ([]domain.Account, error) { return r.accounts, nil }
func (r task8AccountRepoStub) Save(context.Context, domain.Account) error     { return nil }

type task8OneJobRepo struct {
	job             *domain.OutgoingMessageJob
	done            bool
	delays          int
	until           time.Time
	restReleases    int
	dispatchBlocked bool
	dispatchChecks  []schedulerDispatchCheck
}

type schedulerDispatchCheck struct {
	jobID     domain.ID
	accountID domain.ID
}

func (r *task8OneJobRepo) Enqueue(context.Context, domain.OutgoingMessageJob) error { return nil }
func (r *task8OneJobRepo) NextDue(context.Context) (*domain.OutgoingMessageJob, error) {
	return r.job, nil
}
func (r *task8OneJobRepo) MarkDone(_ context.Context, _ domain.ID, _ domain.OutgoingMessageEvent) error {
	r.done = true
	return nil
}
func (r *task8OneJobRepo) Claim(_ context.Context, _ domain.ID, accountID domain.ID) (domain.ID, error) {
	r.job.AccountID = &accountID
	return accountID, nil
}
func (r *task8OneJobRepo) Complete(_ context.Context, _ domain.ID, _ domain.OutgoingMessageEvent) (bool, error) {
	if r.done {
		return false, nil
	}
	r.done = true
	return true, nil
}
func (r *task8OneJobRepo) Delay(context.Context, domain.ID, string) error {
	r.delays++
	return nil
}
func (r *task8OneJobRepo) DelayUntil(_ context.Context, _ domain.ID, _ string, until time.Time) error {
	r.delays++
	r.until = until
	return nil
}
func (r *task8OneJobRepo) ReleaseGroupRestDelays(context.Context) error {
	r.restReleases++
	r.delays = 0
	r.until = time.Time{}
	return nil
}
func (r *task8OneJobRepo) CanDispatch(_ context.Context, jobID, accountID domain.ID) (bool, error) {
	r.dispatchChecks = append(r.dispatchChecks, schedulerDispatchCheck{jobID: jobID, accountID: accountID})
	return !r.dispatchBlocked, nil
}

type schedulerSenderStub struct {
	accounts []domain.ID
	err      error
}

func (s *schedulerSenderStub) SendPublicReply(_ context.Context, account domain.Account, _ domain.OutgoingMessageJob) error {
	s.accounts = append(s.accounts, account.ID)
	return s.err
}
func (s *schedulerSenderStub) SendPrivateMessage(_ context.Context, account domain.Account, _ domain.OutgoingMessageJob) error {
	s.accounts = append(s.accounts, account.ID)
	return s.err
}

type availabilityStub map[domain.ID]bool

func (a availabilityStub) Available(accountID domain.ID) bool { return a[accountID] }

type retryAfterError struct{ duration time.Duration }

func (e retryAfterError) Error() string             { return "transient" }
func (e retryAfterError) RetryAfter() time.Duration { return e.duration }

type schedulerGroupRestRepository struct {
	responses []map[domain.ID]time.Time
	calls     [][]domain.ID
}

func (r *schedulerGroupRestRepository) ActiveGroupRestUntil(_ context.Context, accountIDs []domain.ID, _ time.Time) (map[domain.ID]time.Time, error) {
	r.calls = append(r.calls, append([]domain.ID(nil), accountIDs...))
	index := len(r.calls) - 1
	if len(r.responses) == 0 {
		return map[domain.ID]time.Time{}, nil
	}
	if index >= len(r.responses) {
		index = len(r.responses) - 1
	}
	result := make(map[domain.ID]time.Time, len(r.responses[index]))
	for accountID, until := range r.responses[index] {
		result[accountID] = until
	}
	return result, nil
}

func TestSchedulerSkipsRestingAccountForPublicReply(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	jobs := &task8OneJobRepo{job: &domain.OutgoingMessageJob{ID: "job-1", Type: domain.JobPublicReply}}
	sender := &schedulerSenderStub{}
	rests := &schedulerGroupRestRepository{responses: []map[domain.ID]time.Time{{"resting": now.Add(time.Hour)}}}
	scheduler := NewScheduler(
		task8AccountRepoStub{accounts: []domain.Account{
			{ID: "resting", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
			{ID: "ready", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
		}},
		jobs, rests, sender, NewRoundRobinSelector(), nil,
	)

	require.NoError(t, scheduler.RunOnce(context.Background(), now))

	require.Equal(t, []domain.ID{"ready"}, sender.accounts)
	require.Equal(t, domain.ID("ready"), *jobs.job.AccountID)
}

func TestSchedulerAllowsPrivateMessageDuringRest(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	jobs := &task8OneJobRepo{job: &domain.OutgoingMessageJob{ID: "job-1", Type: domain.JobPrivateMessage}}
	sender := &keywordSchedulerSender{}
	rests := &schedulerGroupRestRepository{responses: []map[domain.ID]time.Time{{"resting": now.Add(time.Hour)}}}
	scheduler := NewScheduler(
		task8AccountRepoStub{accounts: []domain.Account{{ID: "resting", Role: domain.AccountRoleSpammer, Status: domain.AccountActive}}},
		jobs, rests, sender, NewRoundRobinSelector(), nil,
	)

	require.NoError(t, scheduler.RunOnce(context.Background(), now))

	require.Equal(t, []schedulerSendCall{{kind: domain.JobPrivateMessage, accountID: "resting"}}, sender.calls)
}

func TestKeywordPrivateTurnIsAllowedDuringRest(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{keywordSchedulerAccount("resting", domain.DeliveryTargetPrivate)}}
	jobs := newKeywordSchedulerJobRepo(accounts, keywordSchedulerJob("job-1", true))
	sender := &keywordSchedulerSender{}
	rests := &schedulerGroupRestRepository{responses: []map[domain.ID]time.Time{{"resting": now.Add(time.Hour)}}}
	scheduler := NewScheduler(accounts, jobs, rests, sender, NewRoundRobinSelector(), nil)

	require.NoError(t, scheduler.RunOnce(context.Background(), now))

	require.Equal(t, []schedulerSendCall{{kind: domain.JobPrivateMessage, accountID: "resting"}}, sender.calls)
	require.Empty(t, jobs.transientDelays)
}

func TestKeywordDeliveryModePrivateIgnoresLegacyAlternationCursor(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{keywordSchedulerAccount("account-1", domain.DeliveryTargetPublic)}}
	job := keywordSchedulerJob("job-1", false)
	job.KeywordDeliveryMode = domain.KeywordDeliveryModePrivate
	job.TargetTelegramID = "recipient-1"
	jobs := newKeywordSchedulerJobRepo(accounts, job)
	sender := &keywordSchedulerSender{}
	scheduler := NewScheduler(accounts, jobs, nil, sender, NewRoundRobinSelector(), nil)

	require.NoError(t, scheduler.RunOnce(context.Background(), now))
	require.Equal(t, []schedulerSendCall{{kind: domain.JobPrivateMessage, accountID: "account-1"}}, sender.calls)
}

func TestKeywordDeliveryModeCommentsIgnoresLegacyAlternationCursor(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{keywordSchedulerAccount("account-1", domain.DeliveryTargetPrivate)}}
	job := keywordSchedulerJob("job-1", true)
	job.KeywordDeliveryMode = domain.KeywordDeliveryModeComments
	jobs := newKeywordSchedulerJobRepo(accounts, job)
	sender := &keywordSchedulerSender{}
	scheduler := NewScheduler(accounts, jobs, nil, sender, NewRoundRobinSelector(), nil)

	require.NoError(t, scheduler.RunOnce(context.Background(), now))
	require.Equal(t, []schedulerSendCall{{kind: domain.JobPublicReply, accountID: "account-1"}}, sender.calls)
}

func TestKeywordPublicTurnWaitsUntilRestExpires(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	until := now.Add(90 * time.Minute)
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{keywordSchedulerAccount("resting", domain.DeliveryTargetPublic)}}
	jobs := newKeywordSchedulerJobRepo(accounts, keywordSchedulerJob("job-1", true))
	sender := &keywordSchedulerSender{}
	rests := &schedulerGroupRestRepository{responses: []map[domain.ID]time.Time{{"resting": until}}}
	scheduler := NewScheduler(accounts, jobs, rests, sender, NewRoundRobinSelector(), nil)

	require.NoError(t, scheduler.RunOnce(context.Background(), now))

	require.Empty(t, sender.calls)
	require.Nil(t, jobs.jobs[0].AccountID)
	require.Equal(t, []schedulerDelay{{jobID: "job-1", reason: "account_group_rest", until: until}}, jobs.transientDelays)
	require.Empty(t, jobs.keywordFailures)
}

func TestPublicJobUsesAnotherNonRestingAccount(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{
		keywordSchedulerAccount("resting", domain.DeliveryTargetPublic),
		keywordSchedulerAccount("ready", domain.DeliveryTargetPublic),
	}}
	jobs := newKeywordSchedulerJobRepo(accounts, keywordSchedulerJob("job-1", true))
	sender := &keywordSchedulerSender{}
	rests := &schedulerGroupRestRepository{responses: []map[domain.ID]time.Time{{"resting": now.Add(time.Hour)}}}
	scheduler := NewScheduler(accounts, jobs, rests, sender, NewRoundRobinSelector(), nil)

	require.NoError(t, scheduler.RunOnce(context.Background(), now))

	require.Equal(t, []schedulerSendCall{{kind: domain.JobPublicReply, accountID: "ready"}}, sender.calls)
	require.Equal(t, domain.ID("ready"), *jobs.jobs[0].AccountID)
}

func TestPublicJobBypassesRestWhenAccountRestIsDisabled(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	jobs := &task8OneJobRepo{job: &domain.OutgoingMessageJob{ID: "job-1", Type: domain.JobPublicReply}}
	sender := &schedulerSenderStub{}
	rests := &schedulerGroupRestRepository{responses: []map[domain.ID]time.Time{{"resting": now.Add(time.Hour)}}}
	scheduler := NewScheduler(
		task8AccountRepoStub{accounts: []domain.Account{{ID: "resting", Role: domain.AccountRoleSpammer, Status: domain.AccountActive}}},
		jobs, rests, sender, NewRoundRobinSelector(), nil,
	)
	scheduler.SetGroupRestEnabledResolver(func(context.Context) (bool, error) { return false, nil })

	require.NoError(t, scheduler.RunOnce(context.Background(), now))
	require.Equal(t, []domain.ID{"resting"}, sender.accounts)
	require.Empty(t, rests.calls)
}

func TestSchedulerReleasesInFlightRestDelayWhenRestIsDisabled(t *testing.T) {
	now := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	jobs := &task8OneJobRepo{job: &domain.OutgoingMessageJob{ID: "job-1", Type: domain.JobPublicReply}}
	rests := &schedulerGroupRestRepository{responses: []map[domain.ID]time.Time{{
		"resting": now.Add(time.Hour),
	}}}
	scheduler := NewScheduler(
		task8AccountRepoStub{accounts: []domain.Account{{ID: "resting", Role: domain.AccountRoleSpammer, Status: domain.AccountActive}}},
		jobs, rests, &schedulerSenderStub{}, NewRoundRobinSelector(), nil,
	)
	resolverCalls := 0
	scheduler.SetGroupRestEnabledResolver(func(context.Context) (bool, error) {
		resolverCalls++
		return resolverCalls < 3, nil
	})

	require.NoError(t, scheduler.RunOnce(context.Background(), now))
	require.Equal(t, 3, resolverCalls)
	require.Equal(t, 1, jobs.restReleases)
	require.Zero(t, jobs.delays)
	require.True(t, jobs.until.IsZero())
}

func TestAllRestingAccountsDelayUntilEarliestExpiry(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	earliest := now.Add(30 * time.Minute)
	jobs := &task8OneJobRepo{job: &domain.OutgoingMessageJob{ID: "job-1", Type: domain.JobPublicReply}}
	sender := &schedulerSenderStub{}
	rests := &schedulerGroupRestRepository{responses: []map[domain.ID]time.Time{{
		"first": now.Add(time.Hour), "second": earliest,
	}}}
	scheduler := NewScheduler(
		task8AccountRepoStub{accounts: []domain.Account{
			{ID: "first", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
			{ID: "second", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
		}},
		jobs, rests, sender, NewRoundRobinSelector(), nil,
	)

	require.NoError(t, scheduler.RunOnce(context.Background(), now))

	require.Empty(t, sender.accounts)
	require.Nil(t, jobs.job.AccountID)
	require.Equal(t, earliest, jobs.until)
	require.Equal(t, 1, jobs.delays)
}

func TestClaimedKeywordPublicTurnWaitsForItsAccountRest(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	until := now.Add(45 * time.Minute)
	accountID := domain.ID("resting")
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{keywordSchedulerAccount(accountID, domain.DeliveryTargetPublic)}}
	job := keywordSchedulerJob("job-1", true)
	job.AccountID = &accountID
	job.Attempts = 3
	jobs := newKeywordSchedulerJobRepo(accounts, job)
	sender := &keywordSchedulerSender{}
	rests := &schedulerGroupRestRepository{responses: []map[domain.ID]time.Time{{accountID: until}}}
	scheduler := NewScheduler(accounts, jobs, rests, sender, NewRoundRobinSelector(), nil)

	require.NoError(t, scheduler.RunOnce(context.Background(), now))

	require.Empty(t, sender.calls)
	require.Equal(t, accountID, *jobs.jobs[0].AccountID)
	require.Equal(t, 3, jobs.jobs[0].Attempts)
	require.Equal(t, []schedulerDelay{{jobID: "job-1", reason: "account_group_rest", until: until}}, jobs.transientDelays)
}

func TestKeywordCandidatesApplyRestPerDeliveryCursor(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{
		keywordSchedulerAccount("public", domain.DeliveryTargetPublic),
		keywordSchedulerAccount("private", domain.DeliveryTargetPrivate),
	}}
	jobs := newKeywordSchedulerJobRepo(accounts, keywordSchedulerJob("job-1", true))
	sender := &keywordSchedulerSender{}
	rests := &schedulerGroupRestRepository{responses: []map[domain.ID]time.Time{{
		"public": now.Add(time.Hour), "private": now.Add(2 * time.Hour),
	}}}
	scheduler := NewScheduler(accounts, jobs, rests, sender, NewRoundRobinSelector(), nil)

	require.NoError(t, scheduler.RunOnce(context.Background(), now))

	require.Equal(t, []schedulerSendCall{{kind: domain.JobPrivateMessage, accountID: "private"}}, sender.calls)
	require.Equal(t, domain.ID("private"), *jobs.jobs[0].AccountID)
}

func TestSchedulerRechecksRestImmediatelyBeforePublicSend(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	until := now.Add(3 * time.Hour)
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{keywordSchedulerAccount("account-1", domain.DeliveryTargetPublic)}}
	job := keywordSchedulerJob("job-1", true)
	job.Attempts = 2
	jobs := newKeywordSchedulerJobRepo(accounts, job)
	sender := &keywordSchedulerSender{}
	rests := &schedulerGroupRestRepository{responses: []map[domain.ID]time.Time{
		{},
		{"account-1": until},
	}}
	scheduler := NewScheduler(accounts, jobs, rests, sender, NewRoundRobinSelector(), nil)

	require.NoError(t, scheduler.RunOnce(context.Background(), now))

	require.Len(t, rests.calls, 2)
	require.Empty(t, sender.calls)
	require.NotNil(t, jobs.jobs[0].AccountID)
	require.Equal(t, domain.ID("account-1"), *jobs.jobs[0].AccountID)
	require.Equal(t, 2, jobs.jobs[0].Attempts)
	require.Equal(t, []schedulerDelay{{jobID: "job-1", reason: "account_group_rest", until: until}}, jobs.transientDelays)
}

func TestSchedulerSendsDueJob(t *testing.T) {
	jobRepo := &task8OneJobRepo{job: &domain.OutgoingMessageJob{ID: "job-1", Type: domain.JobPublicReply, ChannelID: "c1", Text: "hello"}}
	scheduler := NewScheduler(
		task8AccountRepoStub{accounts: []domain.Account{{ID: "a1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive}}},
		jobRepo,
		nil,
		fake.NewGateway(),
		NewRoundRobinSelector(),
		NewMemoryLimiter(2*time.Second, 19),
	)

	if err := scheduler.RunOnce(context.Background(), time.Now()); err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if !jobRepo.done {
		t.Fatal("job was not marked done")
	}
}

func TestSchedulerDoesNotSendClaimedJobAfterChannelDeactivation(t *testing.T) {
	accountID := domain.ID("assigned-account")
	jobs := &task8OneJobRepo{
		job:             &domain.OutgoingMessageJob{ID: "job-1", Type: domain.JobPublicReply, AccountID: &accountID, ChannelID: "channel-1", Text: "hello"},
		dispatchBlocked: true,
	}
	sender := &schedulerSenderStub{}
	scheduler := NewScheduler(
		task8AccountRepoStub{accounts: []domain.Account{{ID: accountID, Role: domain.AccountRoleSpammer, Status: domain.AccountActive}}},
		jobs,
		nil,
		sender,
		NewRoundRobinSelector(),
		nil,
	)

	require.NoError(t, scheduler.RunOnce(context.Background(), time.Now()))
	require.Empty(t, sender.accounts)
	require.Equal(t, []schedulerDispatchCheck{{jobID: "job-1", accountID: accountID}}, jobs.dispatchChecks)
}

func TestSchedulerDelaysWhenNoAccountEligible(t *testing.T) {
	jobRepo := &task8OneJobRepo{job: &domain.OutgoingMessageJob{ID: "job-1", Type: domain.JobPublicReply, ChannelID: "c1", Text: "hello"}}
	scheduler := NewScheduler(
		task8AccountRepoStub{accounts: []domain.Account{{ID: "a1", Role: domain.AccountRoleSpammer, Status: domain.AccountPaused}}},
		jobRepo,
		nil,
		fake.NewGateway(),
		NewRoundRobinSelector(),
		NewMemoryLimiter(2*time.Second, 19),
	)

	if err := scheduler.RunOnce(context.Background(), time.Now()); err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if jobRepo.done {
		t.Fatal("job was marked done")
	}
	if jobRepo.delays != 1 {
		t.Fatalf("delays = %d, want 1", jobRepo.delays)
	}
}

func TestSchedulerDelaysWhenRateLimited(t *testing.T) {
	jobRepo := &task8OneJobRepo{job: &domain.OutgoingMessageJob{ID: "job-1", Type: domain.JobPublicReply, ChannelID: "c1", Text: "hello"}}
	limiter := NewMemoryLimiter(2*time.Second, 19)
	scheduler := NewScheduler(
		task8AccountRepoStub{accounts: []domain.Account{{ID: "a1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive}}},
		jobRepo,
		nil,
		fake.NewGateway(),
		NewRoundRobinSelector(),
		limiter,
	)
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	if err := scheduler.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("first RunOnce returned error: %v", err)
	}
	jobRepo.done = false
	if err := scheduler.RunOnce(context.Background(), now.Add(time.Second)); err != nil {
		t.Fatalf("second RunOnce returned error: %v", err)
	}
	if jobRepo.done {
		t.Fatal("rate-limited job was marked done")
	}
	if jobRepo.delays != 1 {
		t.Fatalf("delays = %d, want 1", jobRepo.delays)
	}
}

func TestSchedulerSelectionExcludesUnavailableAccounts(t *testing.T) {
	jobRepo := &task8OneJobRepo{job: &domain.OutgoingMessageJob{ID: "job-1", Type: domain.JobPublicReply}}
	sender := &schedulerSenderStub{}
	scheduler := NewScheduler(
		task8AccountRepoStub{accounts: []domain.Account{
			{ID: "disconnected", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
			{ID: "connected", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
		}},
		jobRepo, nil, sender, NewRoundRobinSelector(), nil,
		availabilityStub{"connected": true},
	)

	require.NoError(t, scheduler.RunOnce(context.Background(), time.Now()))
	require.Equal(t, []domain.ID{"connected"}, sender.accounts)
	require.True(t, jobRepo.done)
}

func TestSchedulerUsesTransientRetryDurationWithoutDroppingJob(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	jobRepo := &task8OneJobRepo{job: &domain.OutgoingMessageJob{ID: "job-1", Type: domain.JobPublicReply}}
	sender := &schedulerSenderStub{err: retryAfterError{duration: 37 * time.Second}}
	scheduler := NewScheduler(
		task8AccountRepoStub{accounts: []domain.Account{{ID: "connected", Role: domain.AccountRoleSpammer, Status: domain.AccountActive}}},
		jobRepo, nil, sender, NewRoundRobinSelector(), nil,
	)

	require.NoError(t, scheduler.RunOnce(context.Background(), now))
	require.False(t, jobRepo.done)
	require.Equal(t, 1, jobRepo.delays)
	require.Equal(t, now.Add(37*time.Second), jobRepo.until)
}

type retryingJobRepository struct {
	calls   int
	retried chan struct{}
}

func (*retryingJobRepository) Enqueue(context.Context, domain.OutgoingMessageJob) error { return nil }
func (r *retryingJobRepository) NextDue(context.Context) (*domain.OutgoingMessageJob, error) {
	r.calls++
	if r.calls == 1 {
		return nil, errors.New("temporary database error")
	}
	if r.calls == 2 {
		close(r.retried)
	}
	return nil, nil
}
func (*retryingJobRepository) MarkDone(context.Context, domain.ID, domain.OutgoingMessageEvent) error {
	return nil
}
func (*retryingJobRepository) Delay(context.Context, domain.ID, string) error { return nil }

func TestDeliveryRunnerRetriesBoundedErrorsWithoutStoppingAutomation(t *testing.T) {
	jobs := &retryingJobRepository{retried: make(chan struct{})}
	scheduler := NewScheduler(task8AccountRepoStub{}, jobs, nil, &schedulerSenderStub{}, NewRoundRobinSelector(), nil)
	runner := NewDeliveryRunner(scheduler, time.Now)
	runner.interval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	select {
	case err := <-done:
		t.Fatalf("delivery runner stopped on retryable error: %v", err)
	case <-jobs.retried:
	}
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

type keywordSchedulerAccountRepo struct{ accounts []domain.Account }

func (r *keywordSchedulerAccountRepo) ListActive(context.Context) ([]domain.Account, error) {
	return append([]domain.Account(nil), r.accounts...), nil
}
func (r *keywordSchedulerAccountRepo) List(context.Context) ([]domain.Account, error) {
	return append([]domain.Account(nil), r.accounts...), nil
}
func (r *keywordSchedulerAccountRepo) Save(_ context.Context, account domain.Account) error {
	for i := range r.accounts {
		if r.accounts[i].ID == account.ID {
			r.accounts[i] = account
			return nil
		}
	}
	r.accounts = append(r.accounts, account)
	return nil
}

func (r *keywordSchedulerAccountRepo) update(accountID domain.ID, update func(*domain.Account)) {
	for i := range r.accounts {
		if r.accounts[i].ID == accountID {
			update(&r.accounts[i])
			return
		}
	}
}

type schedulerDelay struct {
	jobID  domain.ID
	reason string
	until  time.Time
}

type keywordSchedulerJobRepo struct {
	accounts         *keywordSchedulerAccountRepo
	jobs             []domain.OutgoingMessageJob
	done             map[domain.ID]bool
	closed           map[domain.ID]bool
	closedCalls      int
	legacyEvents     []domain.OutgoingMessageEvent
	keywordOutcomes  []domain.KeywordDeliveryOutcome
	completionErrors []error
	completionCalls  int
	delays           []schedulerDelay
	transientDelays  []schedulerDelay
	keywordFailures  []domain.KeywordDeliveryFailure
}

func newKeywordSchedulerJobRepo(accounts *keywordSchedulerAccountRepo, jobs ...domain.OutgoingMessageJob) *keywordSchedulerJobRepo {
	return &keywordSchedulerJobRepo{
		accounts: accounts,
		jobs:     append([]domain.OutgoingMessageJob(nil), jobs...),
		done:     make(map[domain.ID]bool),
		closed:   make(map[domain.ID]bool),
	}
}

func (r *keywordSchedulerJobRepo) Enqueue(_ context.Context, job domain.OutgoingMessageJob) error {
	r.jobs = append(r.jobs, job)
	return nil
}

func (r *keywordSchedulerJobRepo) NextDue(context.Context) (*domain.OutgoingMessageJob, error) {
	for i := range r.jobs {
		if !r.done[r.jobs[i].ID] {
			return &r.jobs[i], nil
		}
	}
	return nil, nil
}

func (r *keywordSchedulerJobRepo) MarkDone(ctx context.Context, jobID domain.ID, event domain.OutgoingMessageEvent) error {
	_, err := r.Complete(ctx, jobID, event)
	return err
}

func (r *keywordSchedulerJobRepo) Delay(_ context.Context, jobID domain.ID, reason string) error {
	r.delays = append(r.delays, schedulerDelay{jobID: jobID, reason: reason})
	return nil
}

func (r *keywordSchedulerJobRepo) DelayUntil(_ context.Context, jobID domain.ID, reason string, until time.Time) error {
	r.delays = append(r.delays, schedulerDelay{jobID: jobID, reason: reason, until: until})
	return nil
}

func (*keywordSchedulerJobRepo) CanDispatch(context.Context, domain.ID, domain.ID) (bool, error) {
	return true, nil
}

func (r *keywordSchedulerJobRepo) Claim(_ context.Context, jobID, accountID domain.ID) (domain.ID, error) {
	for i := range r.jobs {
		if r.jobs[i].ID == jobID {
			r.jobs[i].AccountID = &accountID
			return accountID, nil
		}
	}
	return "", errors.New("job not found")
}

func (r *keywordSchedulerJobRepo) Complete(_ context.Context, jobID domain.ID, event domain.OutgoingMessageEvent) (bool, error) {
	if r.done[jobID] {
		return false, nil
	}
	r.done[jobID] = true
	r.legacyEvents = append(r.legacyEvents, event)
	return true, nil
}

func (r *keywordSchedulerJobRepo) RecordPrivateClosed(_ context.Context, jobID, accountID domain.ID, _ time.Time) error {
	if r.closed[jobID] {
		return nil
	}
	r.closed[jobID] = true
	r.closedCalls++
	for i := range r.jobs {
		if r.jobs[i].ID == jobID {
			r.jobs[i].AllowPrivate = false
		}
	}
	r.accounts.update(accountID, func(account *domain.Account) {
		account.NextDelivery = domain.DeliveryTargetPublic
		account.PrivateMessagesClosed++
	})
	return nil
}

func (r *keywordSchedulerJobRepo) CompleteKeywordResponse(_ context.Context, outcome domain.KeywordDeliveryOutcome) (bool, error) {
	if r.completionCalls < len(r.completionErrors) {
		err := r.completionErrors[r.completionCalls]
		r.completionCalls++
		return false, err
	}
	r.completionCalls++
	if r.done[outcome.JobID] {
		return false, nil
	}
	r.done[outcome.JobID] = true
	r.keywordOutcomes = append(r.keywordOutcomes, outcome)
	r.accounts.update(outcome.AccountID, func(account *domain.Account) {
		if outcome.Type == domain.JobPrivateMessage {
			account.PrivateMessagesSent++
		} else {
			account.PublicRepliesSent++
		}
		if outcome.AdvanceTo != nil {
			account.NextDelivery = *outcome.AdvanceTo
		}
	})
	return true, nil
}

func (r *keywordSchedulerJobRepo) DelayTransientUntil(_ context.Context, jobID domain.ID, reason string, until time.Time) error {
	r.transientDelays = append(r.transientDelays, schedulerDelay{jobID: jobID, reason: reason, until: until})
	return nil
}

func (r *keywordSchedulerJobRepo) DelayKeywordFailure(_ context.Context, failure domain.KeywordDeliveryFailure) error {
	r.keywordFailures = append(r.keywordFailures, failure)
	r.delays = append(r.delays, schedulerDelay{jobID: failure.JobID, reason: failure.ErrorCode, until: failure.NextAttemptAt})
	return nil
}

type schedulerSendCall struct {
	kind      domain.JobType
	accountID domain.ID
}

type keywordSchedulerSender struct {
	calls        []schedulerSendCall
	publicErrs   []error
	privateErrs  []error
	publicCalls  int
	privateCalls int
}

func (s *keywordSchedulerSender) SendPublicReply(_ context.Context, account domain.Account, _ domain.OutgoingMessageJob) error {
	s.calls = append(s.calls, schedulerSendCall{kind: domain.JobPublicReply, accountID: account.ID})
	var err error
	if s.publicCalls < len(s.publicErrs) {
		err = s.publicErrs[s.publicCalls]
	}
	s.publicCalls++
	return err
}

func (s *keywordSchedulerSender) SendPrivateMessage(_ context.Context, account domain.Account, _ domain.OutgoingMessageJob) error {
	s.calls = append(s.calls, schedulerSendCall{kind: domain.JobPrivateMessage, accountID: account.ID})
	var err error
	if s.privateCalls < len(s.privateErrs) {
		err = s.privateErrs[s.privateCalls]
	}
	s.privateCalls++
	return err
}

func keywordSchedulerAccount(id domain.ID, next domain.DeliveryTarget) domain.Account {
	return domain.Account{ID: id, Role: domain.AccountRoleSpammer, Status: domain.AccountActive, NextDelivery: next}
}

func keywordSchedulerJob(id domain.ID, allowPrivate bool) domain.OutgoingMessageJob {
	return domain.OutgoingMessageJob{
		ID: id, Type: domain.JobKeywordResponse, ChannelID: "channel-1",
		TargetTelegramID: "target-1", AllowPrivate: allowPrivate,
	}
}

func TestSchedulerAlternatesOneAccountPrivatePublicPrivate(t *testing.T) {
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{keywordSchedulerAccount("account-1", "")}}
	jobs := newKeywordSchedulerJobRepo(accounts,
		keywordSchedulerJob("job-1", true),
		keywordSchedulerJob("job-2", true),
		keywordSchedulerJob("job-3", true),
	)
	sender := &keywordSchedulerSender{}
	scheduler := NewScheduler(accounts, jobs, nil, sender, NewRoundRobinSelector(), nil)

	for i := 0; i < 3; i++ {
		require.NoError(t, scheduler.RunOnce(context.Background(), time.Date(2026, 7, 16, 12, 0, i, 0, time.UTC)))
	}

	require.Equal(t, []schedulerSendCall{
		{kind: domain.JobPrivateMessage, accountID: "account-1"},
		{kind: domain.JobPublicReply, accountID: "account-1"},
		{kind: domain.JobPrivateMessage, accountID: "account-1"},
	}, sender.calls)
	require.Equal(t, domain.DeliveryTargetPublic, accounts.accounts[0].NextDelivery)
	require.Equal(t, int64(2), accounts.accounts[0].PrivateMessagesSent)
	require.Equal(t, int64(1), accounts.accounts[0].PublicRepliesSent)
	require.Equal(t, accounts.accounts[0].DisplayName, jobs.keywordOutcomes[0].AccountTitleSnapshot)
}

func TestSchedulerMaintainsIndependentCursorsAcrossRoundRobinAccounts(t *testing.T) {
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{
		keywordSchedulerAccount("account-a", ""),
		keywordSchedulerAccount("account-b", ""),
	}}
	jobs := newKeywordSchedulerJobRepo(accounts,
		keywordSchedulerJob("job-1", true),
		keywordSchedulerJob("job-2", true),
		keywordSchedulerJob("job-3", true),
		keywordSchedulerJob("job-4", true),
	)
	sender := &keywordSchedulerSender{}
	scheduler := NewScheduler(accounts, jobs, nil, sender, NewRoundRobinSelector(), nil)

	for i := 0; i < 4; i++ {
		require.NoError(t, scheduler.RunOnce(context.Background(), time.Date(2026, 7, 16, 12, 0, i, 0, time.UTC)))
	}

	require.Equal(t, []schedulerSendCall{
		{kind: domain.JobPrivateMessage, accountID: "account-a"},
		{kind: domain.JobPrivateMessage, accountID: "account-b"},
		{kind: domain.JobPublicReply, accountID: "account-a"},
		{kind: domain.JobPublicReply, accountID: "account-b"},
	}, sender.calls)
	require.Equal(t, domain.DeliveryTargetPrivate, accounts.accounts[0].NextDelivery)
	require.Equal(t, domain.DeliveryTargetPrivate, accounts.accounts[1].NextDelivery)
}

func TestSchedulerUsesRestartLoadedAccountCursor(t *testing.T) {
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{keywordSchedulerAccount("account-1", domain.DeliveryTargetPublic)}}
	jobs := newKeywordSchedulerJobRepo(accounts, keywordSchedulerJob("job-1", true))
	sender := &keywordSchedulerSender{}

	restarted := NewScheduler(accounts, jobs, nil, sender, NewRoundRobinSelector(), nil)
	require.NoError(t, restarted.RunOnce(context.Background(), time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)))

	require.Equal(t, []schedulerSendCall{{kind: domain.JobPublicReply, accountID: "account-1"}}, sender.calls)
	require.Equal(t, domain.DeliveryTargetPrivate, accounts.accounts[0].NextDelivery)
}

func TestSchedulerPublicOnlyKeywordResponseDoesNotAdvanceCursor(t *testing.T) {
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{keywordSchedulerAccount("account-1", "")}}
	jobs := newKeywordSchedulerJobRepo(accounts, keywordSchedulerJob("job-1", false))
	sender := &keywordSchedulerSender{}
	scheduler := NewScheduler(accounts, jobs, nil, sender, NewRoundRobinSelector(), nil)

	require.NoError(t, scheduler.RunOnce(context.Background(), time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)))

	require.Equal(t, []schedulerSendCall{{kind: domain.JobPublicReply, accountID: "account-1"}}, sender.calls)
	require.Equal(t, domain.DeliveryTargetPrivate, accounts.accounts[0].EffectiveNextDelivery())
	require.Nil(t, jobs.keywordOutcomes[0].AdvanceTo)
}

func TestSchedulerClosedPrivateDelaysSameAccountPublicFallback(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{keywordSchedulerAccount("account-1", "")}}
	jobs := newKeywordSchedulerJobRepo(accounts, keywordSchedulerJob("job-1", true))
	sender := &keywordSchedulerSender{privateErrs: []error{domain.PrivateMessageClosed(errors.New("privacy restricted"))}}
	scheduler := NewScheduler(accounts, jobs, nil, sender, NewRoundRobinSelector(), nil)

	require.NoError(t, scheduler.RunOnce(context.Background(), now))
	require.Equal(t, []schedulerSendCall{{kind: domain.JobPrivateMessage, accountID: "account-1"}}, sender.calls)
	require.Equal(t, []schedulerDelay{{jobID: "job-1", reason: "private_message_closed", until: now.Add(2 * time.Second)}}, jobs.transientDelays)
	require.Empty(t, jobs.keywordFailures)
	require.Equal(t, domain.DeliveryTargetPublic, accounts.accounts[0].NextDelivery)
	require.Equal(t, int64(1), accounts.accounts[0].PrivateMessagesClosed)

	require.NoError(t, scheduler.RunOnce(context.Background(), now.Add(2*time.Second)))
	require.Equal(t, []schedulerSendCall{
		{kind: domain.JobPrivateMessage, accountID: "account-1"},
		{kind: domain.JobPublicReply, accountID: "account-1"},
	}, sender.calls)
	require.Equal(t, domain.DeliveryTargetPublic, accounts.accounts[0].NextDelivery)
	require.Equal(t, int64(1), accounts.accounts[0].PublicRepliesSent)
	require.Nil(t, jobs.keywordOutcomes[0].AdvanceTo)
}

func TestSchedulerFailedClosedPrivateFallbackRetainsPublicCursor(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{keywordSchedulerAccount("account-1", "")}}
	jobs := newKeywordSchedulerJobRepo(accounts, keywordSchedulerJob("job-1", true))
	sender := &keywordSchedulerSender{
		privateErrs: []error{domain.PrivateMessageClosed(errors.New("privacy restricted"))},
		publicErrs:  []error{errors.New("network unavailable")},
	}
	scheduler := NewScheduler(accounts, jobs, nil, sender, NewRoundRobinSelector(), nil)
	fallbackAt := now.Add(2 * time.Second)

	require.NoError(t, scheduler.RunOnce(context.Background(), now))
	require.NoError(t, scheduler.RunOnce(context.Background(), fallbackAt))

	require.Equal(t, domain.DeliveryTargetPublic, accounts.accounts[0].NextDelivery)
	require.Equal(t, 1, jobs.closedCalls)
	require.Empty(t, jobs.keywordOutcomes)
	require.Equal(t, []schedulerDelay{{jobID: "job-1", reason: "send_failed", until: fallbackAt.Add(2 * time.Second)}}, jobs.delays)
}

func TestSchedulerFloodWaitDoesNotFallbackOrAdvanceCursor(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{keywordSchedulerAccount("account-1", "")}}
	jobs := newKeywordSchedulerJobRepo(accounts, keywordSchedulerJob("job-1", true))
	sender := &keywordSchedulerSender{privateErrs: []error{retryAfterError{duration: 37 * time.Second}}}
	scheduler := NewScheduler(accounts, jobs, nil, sender, NewRoundRobinSelector(), nil)

	require.NoError(t, scheduler.RunOnce(context.Background(), now))

	require.Equal(t, []schedulerSendCall{{kind: domain.JobPrivateMessage, accountID: "account-1"}}, sender.calls)
	require.Equal(t, domain.DeliveryTargetPrivate, accounts.accounts[0].EffectiveNextDelivery())
	require.Zero(t, jobs.closedCalls)
	require.Empty(t, jobs.keywordOutcomes)
	require.Equal(t, []schedulerDelay{{jobID: "job-1", reason: "send_failed", until: now.Add(37 * time.Second)}}, jobs.delays)
}

func TestSchedulerRecordsKeywordDelaysWithoutAccountForTerminalFinalization(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	accounts := &keywordSchedulerAccountRepo{}
	jobs := newKeywordSchedulerJobRepo(accounts, keywordSchedulerJob("job-1", true))
	scheduler := NewScheduler(accounts, jobs, nil, &keywordSchedulerSender{}, NewRoundRobinSelector(), nil)

	for attempt := 0; attempt < 8; attempt++ {
		require.NoError(t, scheduler.RunOnce(context.Background(), now.Add(time.Duration(attempt)*time.Second)))
	}

	require.Len(t, jobs.keywordFailures, 8)
	for _, failure := range jobs.keywordFailures {
		require.Equal(t, domain.ID("job-1"), failure.JobID)
		require.Equal(t, "no_eligible_account", failure.ErrorCode)
		require.Nil(t, failure.AccountID)
		require.Nil(t, failure.Type)
	}
}

func TestSchedulerKeepsRealAttemptMetadataBeforeTerminalAccountLoss(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	account := keywordSchedulerAccount("account-1", domain.DeliveryTargetPublic)
	account.DisplayName = "Operator"
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{account}}
	jobs := newKeywordSchedulerJobRepo(accounts, keywordSchedulerJob("job-1", false))
	sender := &keywordSchedulerSender{publicErrs: []error{
		errors.New("send 1"), errors.New("send 2"), errors.New("send 3"), errors.New("send 4"),
		errors.New("send 5"), errors.New("send 6"), errors.New("send 7"),
	}}
	scheduler := NewScheduler(accounts, jobs, nil, sender, NewRoundRobinSelector(), nil)

	for attempt := 0; attempt < 7; attempt++ {
		require.NoError(t, scheduler.RunOnce(context.Background(), now.Add(time.Duration(attempt)*time.Second)))
	}
	accounts.accounts = nil
	require.NoError(t, scheduler.RunOnce(context.Background(), now.Add(7*time.Second)))

	require.Len(t, jobs.keywordFailures, 8)
	for _, failure := range jobs.keywordFailures[:7] {
		require.NotNil(t, failure.AccountID)
		require.Equal(t, domain.ID("account-1"), *failure.AccountID)
		require.Equal(t, "Operator", failure.AccountTitleSnapshot)
		require.NotNil(t, failure.Type)
		require.Equal(t, domain.JobPublicReply, *failure.Type)
	}
	terminal := jobs.keywordFailures[7]
	require.Equal(t, "claimed_account_unavailable", terminal.ErrorCode)
	require.Nil(t, terminal.AccountID)
	require.Nil(t, terminal.Type)
}

func TestKeywordSchedulerJobRepoRecordsKeywordFailureAsRetryDelay(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	jobs := newKeywordSchedulerJobRepo(&keywordSchedulerAccountRepo{})

	require.NoError(t, jobs.DelayKeywordFailure(context.Background(), domain.KeywordDeliveryFailure{
		JobID: "job-1", ErrorCode: "send_failed", NextAttemptAt: now.Add(37 * time.Second),
	}))

	require.Equal(t, []schedulerDelay{{jobID: "job-1", reason: "send_failed", until: now.Add(37 * time.Second)}}, jobs.delays)
}

func TestSchedulerDuplicateRunOnceCompletesKeywordResponseOnce(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	accounts := &keywordSchedulerAccountRepo{accounts: []domain.Account{keywordSchedulerAccount("account-1", "")}}
	jobs := newKeywordSchedulerJobRepo(accounts, keywordSchedulerJob("job-1", true))
	jobs.completionErrors = []error{errors.New("local commit failed")}
	sender := &keywordSchedulerSender{}
	scheduler := NewScheduler(accounts, jobs, nil, sender, NewRoundRobinSelector(), nil)

	require.ErrorContains(t, scheduler.RunOnce(context.Background(), now), "local commit failed")
	require.NoError(t, scheduler.RunOnce(context.Background(), now.Add(time.Second)))

	require.Equal(t, []schedulerSendCall{
		{kind: domain.JobPrivateMessage, accountID: "account-1"},
		{kind: domain.JobPrivateMessage, accountID: "account-1"},
	}, sender.calls)
	require.Equal(t, 2, jobs.completionCalls)
	require.Len(t, jobs.keywordOutcomes, 1)
	require.Equal(t, int64(1), accounts.accounts[0].PrivateMessagesSent)
	require.Zero(t, accounts.accounts[0].PublicRepliesSent)
}
