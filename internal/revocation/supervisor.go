package revocation

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

const (
	NormalCheckMinimum = 13 * time.Minute
	NormalCheckJitter  = time.Minute
	FirstFailureRetry  = time.Minute
	SecondFailureRetry = 5 * time.Minute
)

var errInvalidSupervisor = errors.New("revocation: invalid supervisor")

type DecisionChecker interface {
	Check(context.Context, string) (Decision, error)
}

type SupervisorTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type TimerFactory func(time.Duration) SupervisorTimer

type Supervisor struct {
	licenseID string
	checker   DecisionChecker
	newTimer  TimerFactory
	jitter    func() time.Duration
	terminal  func(Decision)
	manual    chan struct{}
	started   atomic.Bool
}

func NewSupervisor(licenseID string, checker DecisionChecker, newTimer TimerFactory, jitter func() time.Duration, terminal func(Decision)) (*Supervisor, error) {
	if _, err := DeriveHandle(licenseID); err != nil || checker == nil || newTimer == nil || jitter == nil || terminal == nil {
		return nil, errInvalidSupervisor
	}
	return &Supervisor{
		licenseID: licenseID,
		checker:   checker,
		newTimer:  newTimer,
		jitter:    jitter,
		terminal:  terminal,
		manual:    make(chan struct{}, 1),
	}, nil
}

func (supervisor *Supervisor) CheckNow() {
	if supervisor == nil {
		return
	}
	select {
	case supervisor.manual <- struct{}{}:
	default:
	}
}

func (supervisor *Supervisor) Run(ctx context.Context) {
	if supervisor == nil || !supervisor.started.CompareAndSwap(false, true) {
		return
	}
	failures := 0
	// Startup authorization may have used a nearly-expired cached validation.
	// Refresh immediately, then schedule every later check from its completion.
	timer := supervisor.newTimer(0)
	if timer == nil {
		return
	}
	defer func() { timer.Stop() }()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C():
		case <-supervisor.manual:
			drainManual(supervisor.manual)
		}
		timer.Stop()

		decision, err := supervisor.checker.Check(ctx, supervisor.licenseID)
		if ctx.Err() != nil {
			return
		}
		if decision == CheckRequired || (err == nil && decision == Revoked) {
			supervisor.terminal(decision)
			return
		}

		var delay time.Duration
		if err != nil || decision != Active {
			failures++
			delay = supervisor.failureDelay(failures)
		} else {
			failures = 0
			delay = supervisor.normalDelay()
		}
		timer = supervisor.newTimer(delay)
		if timer == nil {
			return
		}
	}
}

func (supervisor *Supervisor) normalDelay() time.Duration {
	jitter := supervisor.jitter()
	if jitter < 0 || jitter > NormalCheckJitter {
		jitter = 0
	}
	return NormalCheckMinimum + jitter
}

func (supervisor *Supervisor) failureDelay(failures int) time.Duration {
	switch failures {
	case 1:
		return FirstFailureRetry
	case 2:
		return SecondFailureRetry
	default:
		return supervisor.normalDelay()
	}
}

func drainManual(channel <-chan struct{}) {
	for {
		select {
		case <-channel:
		default:
			return
		}
	}
}

type systemSupervisorTimer struct {
	timer *time.Timer
}

func NewSystemSupervisorTimer(delay time.Duration) SupervisorTimer {
	return &systemSupervisorTimer{timer: time.NewTimer(delay)}
}

func (timer *systemSupervisorTimer) C() <-chan time.Time { return timer.timer.C }
func (timer *systemSupervisorTimer) Stop() bool          { return timer.timer.Stop() }
