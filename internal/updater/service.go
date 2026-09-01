// Package updater provides a platform-neutral update state machine.
package updater

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	initialCheckDelay   = 2 * time.Minute
	hourlyCheckInterval = time.Hour
)

var (
	ErrDisabled     = errors.New("updates are disabled")
	ErrStopped      = errors.New("updater is stopped")
	ErrBusy         = errors.New("update operation is already in progress")
	ErrInvalidState = errors.New("update operation is not available in the current state")
)

// State describes the current update workflow step.
type State string

const (
	StateDisabled    State = "disabled"
	StateIdle        State = "idle"
	StateChecking    State = "checking"
	StateAvailable   State = "available"
	StateDownloading State = "downloading"
	StateReady       State = "ready"
	StateError       State = "error"
)

// Snapshot is an immutable-by-value view of the updater. It contains no reference
// values, and every call to Service.Snapshot returns a separate value.
type Snapshot struct {
	State      State
	RetryState State
	Version    string
	Progress   int
	Error      string
	ErrorCode  string
}

// Timer is the small lifecycle surface required by the scheduler.
type Timer interface {
	Stop() bool
}

// Scheduler makes delayed checks deterministic in tests.
type Scheduler interface {
	AfterFunc(time.Duration, func()) Timer
}

// Config configures a Service. Zero delays use the production schedule.
type Config struct {
	Enabled      bool
	Driver       Driver
	Scheduler    Scheduler
	InitialDelay time.Duration
	Interval     time.Duration
}

// Service coordinates the platform-neutral update workflow.
type Service struct {
	mu sync.Mutex

	enabled   bool
	driver    Driver
	scheduler Scheduler
	delay     time.Duration
	interval  time.Duration

	snapshot Snapshot
	retry    State
	busy     bool
	started  bool
	stopped  bool
	op       uint64

	timers      map[uint64]Timer
	nextTimerID uint64
	operations  sync.WaitGroup
	stopDone    chan struct{}

	lifecycleCtx       context.Context
	cancelLifecycle    context.CancelFunc
	stopLifecycleWatch func() bool
}

// NewService constructs a service that is disabled when updates or its driver are
// not enabled. It does not start timers or contact the driver.
func NewService(config Config) *Service {
	delay := config.InitialDelay
	if delay == 0 {
		delay = initialCheckDelay
	}
	interval := config.Interval
	if interval == 0 {
		interval = hourlyCheckInterval
	}
	scheduler := config.Scheduler
	if scheduler == nil {
		scheduler = wallClockScheduler{}
	}
	lifecycleCtx, cancelLifecycle := context.WithCancel(context.Background())
	enabled := config.Enabled && config.Driver != nil
	state := StateDisabled
	if enabled {
		state = StateIdle
	}
	return &Service{
		enabled:         enabled,
		driver:          config.Driver,
		scheduler:       scheduler,
		delay:           delay,
		interval:        interval,
		snapshot:        Snapshot{State: state},
		timers:          make(map[uint64]Timer),
		stopDone:        make(chan struct{}),
		lifecycleCtx:    lifecycleCtx,
		cancelLifecycle: cancelLifecycle,
	}
}

// Snapshot returns a point-in-time, immutable-by-value view of the service.
func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot
}

// Start binds automatic work to ctx, schedules the first check two minutes after
// activation, and then schedules checks hourly. Repeated calls are harmless.
func (s *Service) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	lifecycle, cancelLifecycle := context.WithCancel(ctx)

	s.mu.Lock()
	if !s.enabled || s.started || s.stopped {
		s.mu.Unlock()
		cancelLifecycle()
		return
	}
	oldCancel := s.cancelLifecycle
	oldWatch := s.stopLifecycleWatch
	s.started = true
	s.lifecycleCtx = lifecycle
	s.cancelLifecycle = cancelLifecycle
	s.stopLifecycleWatch = context.AfterFunc(lifecycle, s.cancelTimers)
	s.mu.Unlock()

	if oldWatch != nil {
		oldWatch()
	}
	oldCancel()
	if lifecycle.Err() != nil {
		return
	}

	s.addTimer(lifecycle, s.delay, func() {
		_, _ = s.checkNow(lifecycle, automaticCheck)
		s.scheduleRepeat(lifecycle)
	})
}

// CheckNow runs an on-demand update check. A manual check may refresh an
// available update, but no check can replace an available or ready update with
// idle or error state. Ready checks are harmless no-ops.
func (s *Service) CheckNow(ctx context.Context) (Snapshot, error) {
	return s.checkNow(ctx, manualCheck)
}

func (s *Service) checkNow(ctx context.Context, mode checkMode) (Snapshot, error) {
	operation, snapshot, err := s.begin(ctx, StateChecking, func(state State, retry State) beginDecision {
		return checkBeginDecision(state, retry, mode)
	})
	if err != nil || operation == nil {
		return snapshot, err
	}
	defer operation.done()

	update, driverErr := s.driver.Check(operation.ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if operation.id != s.op {
		return s.snapshot, nil
	}
	if cancelErr := s.restoreIfInterruptedLocked(operation); cancelErr != nil {
		return s.snapshot, cancelErr
	}
	s.busy = false
	if driverErr != nil {
		if operation.previous.State == StateAvailable || operation.previous.State == StateReady {
			s.restoreLocked(operation)
		} else {
			s.setErrorLocked(driverErr, StateIdle)
		}
		return s.snapshot, nil
	}
	if update.Available {
		s.snapshot = Snapshot{State: StateAvailable, Version: update.Version}
		s.retry = ""
		return s.snapshot, nil
	}
	if operation.previous.State == StateAvailable || operation.previous.State == StateReady {
		s.restoreLocked(operation)
		return s.snapshot, nil
	}
	s.snapshot = Snapshot{State: StateIdle}
	s.retry = ""
	return s.snapshot, nil
}

// Download fetches the available update. Progress callbacks are clamped to the
// inclusive range 0 through 100.
func (s *Service) Download(ctx context.Context) (Snapshot, error) {
	operation, snapshot, err := s.begin(ctx, StateDownloading, func(state State, retry State) beginDecision {
		if state == StateAvailable || (state == StateError && retry == StateAvailable) {
			return beginOperation
		}
		return rejectOperation
	})
	if err != nil || operation == nil {
		return snapshot, err
	}
	defer operation.done()

	driverErr := s.driver.Download(operation.ctx, func(progress int) {
		s.setProgress(operation.id, progress)
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	if operation.id != s.op {
		return s.snapshot, nil
	}
	if cancelErr := s.restoreIfInterruptedLocked(operation); cancelErr != nil {
		return s.snapshot, cancelErr
	}
	s.busy = false
	if driverErr != nil {
		s.setErrorLocked(driverErr, StateAvailable)
		return s.snapshot, nil
	}
	s.snapshot.State = StateReady
	s.snapshot.Progress = 100
	s.snapshot.Error = ""
	s.retry = ""
	return s.snapshot, nil
}

// Install asks the driver to restart and install a ready update.
func (s *Service) Install(ctx context.Context) (Snapshot, error) {
	operation, snapshot, err := s.begin(ctx, StateReady, func(state State, retry State) beginDecision {
		if state == StateReady || (state == StateError && retry == StateReady) {
			return beginOperation
		}
		return rejectOperation
	})
	if err != nil || operation == nil {
		return snapshot, err
	}
	defer operation.done()

	driverErr := s.driver.Install(operation.ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if operation.id != s.op {
		return s.snapshot, nil
	}
	if cancelErr := s.restoreIfInterruptedLocked(operation); cancelErr != nil {
		return s.snapshot, cancelErr
	}
	s.busy = false
	if driverErr != nil {
		s.setErrorLocked(driverErr, StateReady)
		return s.snapshot, nil
	}
	s.snapshot.State = StateReady
	s.snapshot.Error = ""
	s.retry = ""
	return s.snapshot, nil
}

// Stop cancels active work and pending checks, waits for every driver operation,
// and only then stops the driver. It is safe to call repeatedly.
func (s *Service) Stop() {
	s.mu.Lock()
	if s.stopped {
		stopDone := s.stopDone
		s.mu.Unlock()
		<-stopDone
		return
	}
	s.stopped = true
	stopDone := s.stopDone
	timers := s.takeTimersLocked()
	driver := s.driver
	if !s.enabled {
		driver = nil
	}
	cancel := s.cancelLifecycle
	stopWatch := s.stopLifecycleWatch
	s.mu.Unlock()

	if stopWatch != nil {
		stopWatch()
	}
	cancel()
	stopTimers(timers)
	s.operations.Wait()
	if driver != nil {
		_ = driver.Stop()
	}
	close(stopDone)
}

type checkMode uint8

const (
	manualCheck checkMode = iota
	automaticCheck
)

type beginDecision uint8

const (
	rejectOperation beginDecision = iota
	beginOperation
	skipOperation
)

func checkBeginDecision(state State, retry State, mode checkMode) beginDecision {
	switch state {
	case StateIdle:
		return beginOperation
	case StateAvailable:
		if mode == manualCheck {
			return beginOperation
		}
		return skipOperation
	case StateReady:
		return skipOperation
	case StateError:
		if retry == StateIdle {
			return beginOperation
		}
		return skipOperation
	default:
		return rejectOperation
	}
}

type serviceOperation struct {
	service       *Service
	id            uint64
	ctx           context.Context
	caller        context.Context
	lifecycle     context.Context
	previous      Snapshot
	previousRetry State
	cancel        context.CancelFunc
	stopLifecycle func() bool
}

func (o *serviceOperation) done() {
	o.stopLifecycle()
	o.cancel()
	o.service.operations.Done()
}

func (s *Service) begin(
	ctx context.Context,
	next State,
	decide func(State, State) beginDecision,
) (*serviceOperation, Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, s.Snapshot(), err
	}

	s.mu.Lock()
	if !s.enabled {
		snapshot := s.snapshot
		s.mu.Unlock()
		return nil, snapshot, ErrDisabled
	}
	if s.stopped {
		snapshot := s.snapshot
		s.mu.Unlock()
		return nil, snapshot, ErrStopped
	}
	if s.busy {
		snapshot := s.snapshot
		s.mu.Unlock()
		return nil, snapshot, ErrBusy
	}
	lifecycle := s.lifecycleCtx
	if err := lifecycle.Err(); err != nil {
		snapshot := s.snapshot
		s.mu.Unlock()
		return nil, snapshot, err
	}
	switch decide(s.snapshot.State, s.retry) {
	case skipOperation:
		snapshot := s.snapshot
		s.mu.Unlock()
		return nil, snapshot, nil
	case rejectOperation:
		snapshot := s.snapshot
		s.mu.Unlock()
		return nil, snapshot, ErrInvalidState
	}

	previous := s.snapshot
	previousRetry := s.retry
	s.busy = true
	s.op++
	id := s.op
	s.snapshot.State = next
	s.snapshot.RetryState = ""
	s.snapshot.Error = ""
	s.snapshot.ErrorCode = ""
	if next == StateDownloading {
		s.snapshot.Progress = 0
	}
	operationCtx, cancel := context.WithCancel(ctx)
	stopLifecycle := context.AfterFunc(lifecycle, cancel)
	s.operations.Add(1)
	s.mu.Unlock()

	if lifecycle.Err() != nil {
		cancel()
	}
	return &serviceOperation{
		service:       s,
		id:            id,
		ctx:           operationCtx,
		caller:        ctx,
		lifecycle:     lifecycle,
		previous:      previous,
		previousRetry: previousRetry,
		cancel:        cancel,
		stopLifecycle: stopLifecycle,
	}, s.Snapshot(), nil
}

func (s *Service) restoreIfInterruptedLocked(operation *serviceOperation) error {
	var err error
	if callerErr := operation.caller.Err(); callerErr != nil {
		err = callerErr
	} else if lifecycleErr := operation.lifecycle.Err(); lifecycleErr != nil {
		err = lifecycleErr
	} else if s.stopped {
		err = ErrStopped
	}
	if err != nil {
		s.restoreLocked(operation)
	}
	return err
}

func (s *Service) restoreLocked(operation *serviceOperation) {
	s.snapshot = operation.previous
	s.retry = operation.previousRetry
	s.busy = false
}

func (s *Service) setProgress(operationID uint64, progress int) {
	if progress < 0 {
		progress = 0
	} else if progress > 100 {
		progress = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped && s.busy && s.op == operationID && s.snapshot.State == StateDownloading {
		s.snapshot.Progress = progress
	}
}

func (s *Service) setErrorLocked(err error, retry State) {
	s.snapshot.State = StateError
	s.snapshot.RetryState = retry
	s.snapshot.Error = err.Error()
	s.snapshot.ErrorCode = string(ErrorCodeOf(err))
	s.retry = retry
}

func (s *Service) addTimer(ctx context.Context, delay time.Duration, callback func()) {
	s.mu.Lock()
	if s.stopped || !s.started || ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	s.nextTimerID++
	timerID := s.nextTimerID
	s.timers[timerID] = nil
	scheduler := s.scheduler
	s.mu.Unlock()

	timer := scheduler.AfterFunc(delay, func() {
		s.fireTimer(timerID, ctx, callback)
	})

	s.mu.Lock()
	_, pending := s.timers[timerID]
	keep := pending && !s.stopped && ctx.Err() == nil
	if keep {
		s.timers[timerID] = timer
	} else {
		delete(s.timers, timerID)
	}
	s.mu.Unlock()
	if !keep {
		timer.Stop()
	}
}

func (s *Service) fireTimer(timerID uint64, ctx context.Context, callback func()) {
	s.mu.Lock()
	if _, pending := s.timers[timerID]; !pending {
		s.mu.Unlock()
		return
	}
	delete(s.timers, timerID)
	run := !s.stopped && s.started && ctx.Err() == nil
	s.mu.Unlock()
	if run {
		callback()
	}
}

func (s *Service) scheduleRepeat(ctx context.Context) {
	s.mu.Lock()
	run := !s.stopped && s.started && ctx.Err() == nil
	s.mu.Unlock()
	if !run {
		return
	}
	s.addTimer(ctx, s.interval, func() {
		_, _ = s.checkNow(ctx, automaticCheck)
		s.scheduleRepeat(ctx)
	})
}

func (s *Service) cancelTimers() {
	s.mu.Lock()
	timers := s.takeTimersLocked()
	s.mu.Unlock()
	stopTimers(timers)
}

func (s *Service) takeTimersLocked() []Timer {
	timers := make([]Timer, 0, len(s.timers))
	for timerID, timer := range s.timers {
		if timer != nil {
			timers = append(timers, timer)
		}
		delete(s.timers, timerID)
	}
	return timers
}

func stopTimers(timers []Timer) {
	for _, timer := range timers {
		timer.Stop()
	}
}

type wallClockScheduler struct{}

func (wallClockScheduler) AfterFunc(delay time.Duration, callback func()) Timer {
	return time.AfterFunc(delay, callback)
}
