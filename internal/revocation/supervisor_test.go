package revocation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSupervisorSchedulesNormalCadenceAndFailureRetries(t *testing.T) {
	timers := newFakeTimerFactory()
	checker := &scriptedChecker{results: []checkResult{
		{decision: Active},
		{err: errors.New("offline")},
		{err: errors.New("offline")},
		{err: errors.New("offline")},
	}}
	supervisor, err := NewSupervisor("license-schedule", checker, timers.New, func() time.Duration { return 30 * time.Second }, func(Decision) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); supervisor.Run(ctx) }()

	for index, want := range []time.Duration{0, 13*time.Minute + 30*time.Second, time.Minute, 5 * time.Minute, 13*time.Minute + 30*time.Second} {
		timer := timers.Wait(t, index)
		if timer.delay != want {
			t.Fatalf("timer %d delay = %v, want %v", index, timer.delay, want)
		}
		if index < 4 {
			timer.Fire()
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not stop")
	}
}

func TestSupervisorGraceUsesFailureRetriesAndDoesNotCallTerminal(t *testing.T) {
	timers := newFakeTimerFactory()
	checker := &scriptedChecker{results: []checkResult{
		{decision: ActiveInGrace},
		{decision: ActiveInGrace},
		{decision: Active},
	}}
	var terminalCalls atomic.Int32
	supervisor, err := NewSupervisor("license-grace", checker, timers.New, func() time.Duration { return 15 * time.Second }, func(Decision) { terminalCalls.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); supervisor.Run(ctx) }()

	for index, want := range []time.Duration{0, time.Minute, 5 * time.Minute, 13*time.Minute + 15*time.Second} {
		timer := timers.Wait(t, index)
		if timer.delay != want {
			t.Fatalf("timer %d delay = %v, want %v", index, timer.delay, want)
		}
		if index < 3 {
			timer.Fire()
		}
	}
	if terminalCalls.Load() != 0 {
		t.Fatalf("terminal callback called %d times", terminalCalls.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not stop")
	}
}

func TestSupervisorCoalescesManualChecksAndStopsOnTerminalDecision(t *testing.T) {
	timers := newFakeTimerFactory()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	checker := checkerFunc(func(ctx context.Context, licenseID string) (Decision, error) {
		if licenseID != "license-terminal" {
			t.Errorf("license ID = %q", licenseID)
		}
		started <- struct{}{}
		select {
		case <-release:
			return Revoked, nil
		case <-ctx.Done():
			return CheckRequired, ctx.Err()
		}
	})
	terminal := make(chan Decision, 2)
	supervisor, err := NewSupervisor("license-terminal", checker, timers.New, func() time.Duration { return 0 }, func(decision Decision) { terminal <- decision })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); supervisor.Run(ctx) }()

	_ = timers.Wait(t, 0)
	supervisor.CheckNow()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("manual check did not start")
	}
	for index := 0; index < 20; index++ {
		supervisor.CheckNow()
	}
	close(release)
	select {
	case decision := <-terminal:
		if decision != Revoked {
			t.Fatalf("terminal decision = %q", decision)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal callback not called")
	}
	select {
	case extra := <-terminal:
		t.Fatalf("extra terminal callback: %q", extra)
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not stop after terminal decision")
	}
	select {
	case <-started:
		t.Fatal("manual checks were not coalesced")
	default:
	}
}

func TestSupervisorRunsWakeCheckQueuedDuringAnActiveCheck(t *testing.T) {
	timers := newFakeTimerFactory()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	checker := checkerFunc(func(context.Context, string) (Decision, error) {
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		return Active, nil
	})
	supervisor, err := NewSupervisor("license-wake-during-check", checker, timers.New, func() time.Duration { return 0 }, func(Decision) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); supervisor.Run(ctx) }()

	timers.Wait(t, 0).Fire()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("initial check did not start")
	}
	supervisor.CheckNow()
	close(releaseFirst)

	pendingNormalTimer := timers.Wait(t, 1)
	if pendingNormalTimer.delay != NormalCheckMinimum {
		t.Fatalf("normal timer delay = %v, want %v", pendingNormalTimer.delay, NormalCheckMinimum)
	}
	deadline := time.After(time.Second)
	for calls.Load() < 2 {
		select {
		case <-deadline:
			t.Fatal("wake queued during active check did not start a follow-up check")
		case <-timers.created:
		}
	}
	if !pendingNormalTimer.stopped.Load() {
		t.Fatal("wake follow-up did not interrupt the pending normal timer")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not stop")
	}
}

func TestSupervisorCancellationStopsTimerAndChecker(t *testing.T) {
	timers := newFakeTimerFactory()
	checkerStarted := make(chan struct{})
	checkerStopped := make(chan struct{})
	checker := checkerFunc(func(ctx context.Context, _ string) (Decision, error) {
		close(checkerStarted)
		<-ctx.Done()
		close(checkerStopped)
		return CheckRequired, ctx.Err()
	})
	supervisor, err := NewSupervisor("license-cancel", checker, timers.New, func() time.Duration { return 0 }, func(Decision) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); supervisor.Run(ctx) }()
	timer := timers.Wait(t, 0)
	timer.Fire()
	select {
	case <-checkerStarted:
	case <-time.After(time.Second):
		t.Fatal("checker did not start")
	}
	cancel()
	select {
	case <-checkerStopped:
	case <-time.After(time.Second):
		t.Fatal("checker context was not cancelled")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not stop")
	}
	if !timer.stopped.Load() {
		t.Fatal("timer was not stopped")
	}
}

func TestSupervisorCheckRequiredCallsTerminalOnce(t *testing.T) {
	timers := newFakeTimerFactory()
	checker := &scriptedChecker{results: []checkResult{{decision: CheckRequired}}}
	terminal := make(chan Decision, 2)
	supervisor, err := NewSupervisor("license-check-required", checker, timers.New, func() time.Duration { return 0 }, func(decision Decision) { terminal <- decision })
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); supervisor.Run(context.Background()) }()
	timers.Wait(t, 0).Fire()
	select {
	case decision := <-terminal:
		if decision != CheckRequired {
			t.Fatalf("terminal decision = %q", decision)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal callback not called")
	}
	<-done
	select {
	case decision := <-terminal:
		t.Fatalf("extra terminal callback: %q", decision)
	default:
	}
}

func TestSupervisorReceiptCapEvaluationFailsClosedOnce(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	handle, _ := DeriveHandle("license-supervisor-cap-target")
	payload := testPayload(2, sortedEntriesForTest(t, handle, now))
	payload.GeneratedAt = now.Format(manifestTimeLayout)
	manifest := verifiedManifestForTest(t, publicKey, privateKey, payload)
	state := stateWithSuccess(now.Add(-time.Hour), 1)
	state.RevokedHandles = receiptSetForTest(MaxRevokedReceipts)
	checker := checkerFunc(func(context.Context, string) (Decision, error) {
		decision, _, evaluationErr := Evaluate(handle, state, FetchResult{Manifest: &manifest}, now)
		return decision, evaluationErr
	})

	timers := newFakeTimerFactory()
	terminal := make(chan Decision, 2)
	supervisor, err := NewSupervisor("license-supervisor-cap-target", checker, timers.New, func() time.Duration { return 0 }, func(decision Decision) { terminal <- decision })
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); supervisor.Run(context.Background()) }()
	timers.Wait(t, 0).Fire()
	select {
	case decision := <-terminal:
		if decision != CheckRequired {
			t.Fatalf("terminal decision = %q", decision)
		}
	case <-time.After(time.Second):
		t.Fatal("receipt-cap CheckRequired did not reach terminal callback")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not stop after receipt-cap CheckRequired")
	}
	select {
	case decision := <-terminal:
		t.Fatalf("extra terminal callback: %q", decision)
	default:
	}
}

func TestSupervisorRejectsNilTerminalCallbackAndIsSingleUse(t *testing.T) {
	timers := newFakeTimerFactory()
	checker := checkerFunc(func(context.Context, string) (Decision, error) { return Active, nil })
	if _, err := NewSupervisor("license-no-terminal", checker, timers.New, func() time.Duration { return 0 }, nil); err == nil {
		t.Fatal("nil terminal callback accepted")
	}
	supervisor, err := NewSupervisor("license-single-use", checker, timers.New, func() time.Duration { return 0 }, func(Decision) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstDone := make(chan struct{})
	go func() { defer close(firstDone); supervisor.Run(ctx) }()
	_ = timers.Wait(t, 0)
	secondDone := make(chan struct{})
	go func() { defer close(secondDone); supervisor.Run(ctx) }()
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second Run did not fail closed")
	}
	cancel()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first Run did not stop")
	}
}

type checkerFunc func(context.Context, string) (Decision, error)

func (function checkerFunc) Check(ctx context.Context, licenseID string) (Decision, error) {
	return function(ctx, licenseID)
}

type checkResult struct {
	decision Decision
	err      error
}

type scriptedChecker struct {
	mu      sync.Mutex
	results []checkResult
	calls   int
}

func (checker *scriptedChecker) Check(context.Context, string) (Decision, error) {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	if checker.calls >= len(checker.results) {
		return Active, nil
	}
	result := checker.results[checker.calls]
	checker.calls++
	return result.decision, result.err
}

type fakeTimerFactory struct {
	mu      sync.Mutex
	timers  []*fakeSupervisorTimer
	created chan struct{}
}

func newFakeTimerFactory() *fakeTimerFactory {
	return &fakeTimerFactory{created: make(chan struct{}, 32)}
}

func (factory *fakeTimerFactory) New(delay time.Duration) SupervisorTimer {
	timer := &fakeSupervisorTimer{delay: delay, channel: make(chan time.Time, 1)}
	factory.mu.Lock()
	factory.timers = append(factory.timers, timer)
	factory.mu.Unlock()
	factory.created <- struct{}{}
	return timer
}

func (factory *fakeTimerFactory) Wait(t *testing.T, index int) *fakeSupervisorTimer {
	t.Helper()
	for {
		factory.mu.Lock()
		if index < len(factory.timers) {
			timer := factory.timers[index]
			factory.mu.Unlock()
			return timer
		}
		factory.mu.Unlock()
		select {
		case <-factory.created:
		case <-time.After(time.Second):
			t.Fatalf("timer %d was not created", index)
		}
	}
}

type fakeSupervisorTimer struct {
	delay   time.Duration
	channel chan time.Time
	stopped atomic.Bool
}

func (timer *fakeSupervisorTimer) C() <-chan time.Time { return timer.channel }

func (timer *fakeSupervisorTimer) Stop() bool {
	return !timer.stopped.Swap(true)
}

func (timer *fakeSupervisorTimer) Fire() {
	timer.channel <- time.Now()
}
