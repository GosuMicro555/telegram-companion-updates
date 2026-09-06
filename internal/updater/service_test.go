package updater

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestServiceStartsIdleAndChecksAfterDelayThenHourly(t *testing.T) {
	driver := &fakeDriver{checkResult: Update{Available: true, Version: "1.2.3"}}
	scheduler := &fakeScheduler{}
	service := NewService(Config{Enabled: true, Driver: driver, Scheduler: scheduler})

	if got := service.Snapshot(); got.State != StateIdle {
		t.Fatalf("initial state = %q, want %q", got.State, StateIdle)
	}
	service.Start(context.Background())
	if got := scheduler.callCount(); got != 1 {
		t.Fatalf("startup timer count = %d, want 1", got)
	}
	if got := scheduler.delay(0); got != initialCheckDelay {
		t.Fatalf("startup delay = %s, want %s", got, initialCheckDelay)
	}
	if got := driver.checkCallCount(); got != 0 {
		t.Fatalf("check calls before delay = %d, want 0", got)
	}

	scheduler.fire(0)
	if got := driver.checkCallCount(); got != 1 {
		t.Fatalf("check calls after delay = %d, want 1", got)
	}
	if got := service.Snapshot(); got.State != StateAvailable || got.Version != "1.2.3" {
		t.Fatalf("snapshot after delayed check = %#v", got)
	}
	if got := scheduler.callCount(); got != 2 {
		t.Fatalf("scheduled calls after initial check = %d, want 2", got)
	}
	if got := scheduler.delay(1); got != hourlyCheckInterval {
		t.Fatalf("repeat delay = %s, want %s", got, hourlyCheckInterval)
	}

	scheduler.fire(1)
	if got := driver.checkCallCount(); got != 1 {
		t.Fatalf("automatic check calls while update available = %d, want 1", got)
	}
}

func TestAutomaticChecksUseStartLifecycleAndStopAfterCancellation(t *testing.T) {
	type contextKey string
	const key contextKey = "source"
	seenValue := make(chan any, 1)
	driver := &fakeDriver{check: func(ctx context.Context) (Update, error) {
		seenValue <- ctx.Value(key)
		return Update{}, nil
	}}
	scheduler := &fakeScheduler{}
	service := NewService(Config{Enabled: true, Driver: driver, Scheduler: scheduler})
	lifecycle, cancel := context.WithCancel(context.WithValue(context.Background(), key, "activation"))
	service.Start(lifecycle)

	scheduler.fire(0)
	if got := <-seenValue; got != "activation" {
		t.Fatalf("automatic check context value = %#v, want activation", got)
	}
	if got := pendingTimerCount(service); got != 1 {
		t.Fatalf("pending timers after first check = %d, want 1", got)
	}

	cancel()
	scheduler.fire(1)
	if got := driver.checkCallCount(); got != 1 {
		t.Fatalf("check calls after lifecycle cancellation = %d, want 1", got)
	}
	if got := pendingTimerCount(service); got != 0 {
		t.Fatalf("pending timers after lifecycle cancellation = %d, want 0", got)
	}
}

func TestStartWithCanceledLifecycleSchedulesNothing(t *testing.T) {
	driver := &fakeDriver{}
	scheduler := &fakeScheduler{}
	service := NewService(Config{Enabled: true, Driver: driver, Scheduler: scheduler})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	service.Start(ctx)

	if got := scheduler.callCount(); got != 0 {
		t.Fatalf("scheduled calls = %d, want 0", got)
	}
	if got := driver.totalCalls(); got != 0 {
		t.Fatalf("driver calls = %d, want 0", got)
	}
}

func TestFiredTimersAreRemovedAcrossRepeatedScheduling(t *testing.T) {
	driver := &fakeDriver{}
	scheduler := &fakeScheduler{}
	service := NewService(Config{Enabled: true, Driver: driver, Scheduler: scheduler})
	service.Start(context.Background())

	for i := 0; i < 5; i++ {
		if got := pendingTimerCount(service); got != 1 {
			t.Fatalf("pending timers before fire %d = %d, want 1", i, got)
		}
		scheduler.fire(i)
		if got := pendingTimerCount(service); got != 1 {
			t.Fatalf("pending timers after fire %d = %d, want 1", i, got)
		}
		if got := scheduler.activeTimerCount(); got != 1 {
			t.Fatalf("active scheduler timers after fire %d = %d, want 1", i, got)
		}
	}
	if got := driver.checkCallCount(); got != 5 {
		t.Fatalf("check calls = %d, want 5", got)
	}
}

func TestManualAndAutomaticCheckPoliciesPreserveAvailableAndReady(t *testing.T) {
	driver := &fakeDriver{checkResult: Update{Available: true, Version: "1.2.3"}}
	scheduler := &fakeScheduler{}
	service := NewService(Config{Enabled: true, Driver: driver, Scheduler: scheduler})

	if _, err := service.CheckNow(context.Background()); err != nil {
		t.Fatalf("initial CheckNow() error = %v", err)
	}
	driver.setCheckResult(Update{})
	if _, err := service.CheckNow(context.Background()); err != nil {
		t.Fatalf("refresh CheckNow() error = %v", err)
	}
	if got := service.Snapshot(); got.State != StateAvailable || got.Version != "1.2.3" {
		t.Fatalf("available snapshot after no-update refresh = %#v", got)
	}
	driver.setCheckError(errors.New("offline"))
	if _, err := service.CheckNow(context.Background()); err != nil {
		t.Fatalf("failed refresh CheckNow() error = %v", err)
	}
	if got := service.Snapshot(); got.State != StateAvailable || got.Version != "1.2.3" || got.Error != "" {
		t.Fatalf("available snapshot after failed refresh = %#v", got)
	}
	driver.setCheckError(nil)

	service.Start(context.Background())
	scheduler.fire(0)
	if got := driver.checkCallCount(); got != 3 {
		t.Fatalf("check calls after automatic available-state check = %d, want 3", got)
	}

	if _, err := service.Download(context.Background()); err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	ready := service.Snapshot()
	if ready.State != StateReady || ready.Version != "1.2.3" {
		t.Fatalf("ready snapshot = %#v", ready)
	}
	if got, err := service.CheckNow(context.Background()); err != nil || got != ready {
		t.Fatalf("manual ready-state check = (%#v, %v), want (%#v, nil)", got, err, ready)
	}
	scheduler.fire(1)
	if got := driver.checkCallCount(); got != 3 {
		t.Fatalf("check calls after ready-state checks = %d, want 3", got)
	}
	if got := service.Snapshot(); got != ready {
		t.Fatalf("snapshot after ready-state checks = %#v, want %#v", got, ready)
	}
}

func TestManualDownloadClampsProgressAndInstalls(t *testing.T) {
	var service *Service
	driver := &fakeDriver{
		checkResult: Update{Available: true, Version: "1.2.3"},
		download: func(_ context.Context, progress func(int)) error {
			progress(-20)
			if got := service.Snapshot(); got.Progress != 0 || got.State != StateDownloading {
				t.Fatalf("low progress snapshot = %#v", got)
			}
			progress(145)
			if got := service.Snapshot(); got.Progress != 100 || got.State != StateDownloading {
				t.Fatalf("high progress snapshot = %#v", got)
			}
			return nil
		},
	}
	service = NewService(Config{Enabled: true, Driver: driver, Scheduler: &fakeScheduler{}})

	if _, err := service.CheckNow(context.Background()); err != nil {
		t.Fatalf("CheckNow() error = %v", err)
	}
	if _, err := service.Download(context.Background()); err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if got := service.Snapshot(); got.State != StateReady || got.Progress != 100 {
		t.Fatalf("snapshot after download = %#v", got)
	}
	if _, err := service.Install(context.Background()); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if got := driver.installCallCount(); got != 1 {
		t.Fatalf("install calls = %d, want 1", got)
	}
}

func TestAlreadyCanceledCheckReturnsContextErrorWithoutCallingDriver(t *testing.T) {
	driver := &fakeDriver{}
	service := NewService(Config{Enabled: true, Driver: driver, Scheduler: &fakeScheduler{}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := service.CheckNow(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CheckNow() error = %v, want context.Canceled", err)
	}
	if got.State != StateIdle || service.Snapshot().State != StateIdle {
		t.Fatalf("snapshot after canceled check = %#v", got)
	}
	if got := driver.checkCallCount(); got != 0 {
		t.Fatalf("check calls = %d, want 0", got)
	}
}

func TestManualCheckCancellationRestoresIdle(t *testing.T) {
	started := make(chan struct{})
	driver := &fakeDriver{check: func(ctx context.Context) (Update, error) {
		close(started)
		<-ctx.Done()
		return Update{}, ctx.Err()
	}}
	service := NewService(Config{Enabled: true, Driver: driver, Scheduler: &fakeScheduler{}})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan operationResult, 1)
	go func() {
		snapshot, err := service.CheckNow(ctx)
		result <- operationResult{snapshot: snapshot, err: err}
	}()

	<-started
	cancel()
	got := <-result
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("CheckNow() error = %v, want context.Canceled", got.err)
	}
	if got.snapshot.State != StateIdle || got.snapshot.Error != "" {
		t.Fatalf("snapshot after canceled check = %#v", got.snapshot)
	}
}

func TestManualDownloadCancellationRestoresAvailable(t *testing.T) {
	started := make(chan struct{})
	driver := &fakeDriver{
		checkResult: Update{Available: true, Version: "1.2.3"},
		download: func(ctx context.Context, _ func(int)) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	}
	service := NewService(Config{Enabled: true, Driver: driver, Scheduler: &fakeScheduler{}})
	if _, err := service.CheckNow(context.Background()); err != nil {
		t.Fatalf("CheckNow() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan operationResult, 1)
	go func() {
		snapshot, err := service.Download(ctx)
		result <- operationResult{snapshot: snapshot, err: err}
	}()

	<-started
	cancel()
	got := <-result
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("Download() error = %v, want context.Canceled", got.err)
	}
	if got.snapshot.State != StateAvailable || got.snapshot.Version != "1.2.3" || got.snapshot.Error != "" {
		t.Fatalf("snapshot after canceled download = %#v", got.snapshot)
	}
}

func TestManualInstallCancellationRestoresReady(t *testing.T) {
	started := make(chan struct{})
	driver := &fakeDriver{
		checkResult: Update{Available: true, Version: "1.2.3"},
		install: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	}
	service := NewService(Config{Enabled: true, Driver: driver, Scheduler: &fakeScheduler{}})
	if _, err := service.CheckNow(context.Background()); err != nil {
		t.Fatalf("CheckNow() error = %v", err)
	}
	if _, err := service.Download(context.Background()); err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan operationResult, 1)
	go func() {
		snapshot, err := service.Install(ctx)
		result <- operationResult{snapshot: snapshot, err: err}
	}()

	<-started
	cancel()
	got := <-result
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("Install() error = %v, want context.Canceled", got.err)
	}
	if got.snapshot.State != StateReady || got.snapshot.Version != "1.2.3" || got.snapshot.Error != "" {
		t.Fatalf("snapshot after canceled install = %#v", got.snapshot)
	}
}

func TestStopWaitsForActiveDriverCallBeforeDriverStop(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)

	checkStarted := make(chan struct{})
	cancelObserved := make(chan struct{})
	releaseCheck := make(chan struct{})
	checkReturned := make(chan struct{})
	driverStopCalled := make(chan struct{})
	var overlapMu sync.Mutex
	overlapped := false
	driver := &fakeDriver{
		check: func(ctx context.Context) (Update, error) {
			close(checkStarted)
			<-ctx.Done()
			close(cancelObserved)
			<-releaseCheck
			close(checkReturned)
			return Update{}, ctx.Err()
		},
		stop: func() error {
			select {
			case <-checkReturned:
			default:
				overlapMu.Lock()
				overlapped = true
				overlapMu.Unlock()
			}
			close(driverStopCalled)
			return nil
		},
	}
	service := NewService(Config{Enabled: true, Driver: driver, Scheduler: &fakeScheduler{}})
	operationDone := make(chan error, 1)
	go func() {
		_, err := service.CheckNow(context.Background())
		operationDone <- err
	}()
	<-checkStarted

	stopDone := make(chan struct{})
	go func() {
		service.Stop()
		close(stopDone)
	}()
	<-cancelObserved
	select {
	case <-driverStopCalled:
		t.Fatal("Driver.Stop ran while Driver.Check was active")
	case <-stopDone:
		t.Fatal("Service.Stop returned while Driver.Check was active")
	default:
	}

	close(releaseCheck)
	if err := <-operationDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("active CheckNow() error = %v, want context.Canceled", err)
	}
	<-stopDone
	<-driverStopCalled
	overlapMu.Lock()
	defer overlapMu.Unlock()
	if overlapped {
		t.Fatal("Driver.Stop overlapped an active driver call")
	}
}

func TestDownloadAndInstallErrorsCanBeRetried(t *testing.T) {
	driver := &fakeDriver{
		checkResult: Update{Available: true, Version: "1.2.3"},
		downloadErr: errors.New("download failed"),
	}
	service := NewService(Config{Enabled: true, Driver: driver, Scheduler: &fakeScheduler{}})
	if _, err := service.CheckNow(context.Background()); err != nil {
		t.Fatalf("CheckNow() error = %v", err)
	}

	if _, err := service.Download(context.Background()); err != nil {
		t.Fatalf("failed Download() returned API error = %v", err)
	}
	if got := service.Snapshot(); got.State != StateError || got.RetryState != StateAvailable || got.Version != "1.2.3" || got.Error == "" {
		t.Fatalf("snapshot after download error = %#v", got)
	}
	driver.setDownloadError(nil)
	if _, err := service.Download(context.Background()); err != nil {
		t.Fatalf("retry Download() error = %v", err)
	}
	if got := service.Snapshot(); got.State != StateReady || got.RetryState != "" || got.Error != "" {
		t.Fatalf("snapshot after download retry = %#v", got)
	}

	driver.setInstallError(errors.New("install failed"))
	if _, err := service.Install(context.Background()); err != nil {
		t.Fatalf("failed Install() returned API error = %v", err)
	}
	if got := service.Snapshot(); got.State != StateError || got.RetryState != StateReady || got.Version != "1.2.3" || got.Error == "" {
		t.Fatalf("snapshot after install error = %#v", got)
	}
	driver.setInstallError(nil)
	if _, err := service.Install(context.Background()); err != nil {
		t.Fatalf("retry Install() error = %v", err)
	}
	if got := service.Snapshot(); got.State != StateReady || got.RetryState != "" || got.Error != "" {
		t.Fatalf("snapshot after install retry = %#v", got)
	}
	if got := driver.downloadCallCount(); got != 2 {
		t.Fatalf("download calls = %d, want 2", got)
	}
	if got := driver.installCallCount(); got != 2 {
		t.Fatalf("install calls = %d, want 2", got)
	}
}

func TestServiceCarriesCodedDownloadFailureToItsSnapshot(t *testing.T) {
	driver := &fakeDriver{
		checkResult: Update{Available: true, Version: "1.2.3"},
		downloadErr: NewCodedError(CodeSignatureInvalid, errors.New("native signature details")),
	}
	service := NewService(Config{Enabled: true, Driver: driver, Scheduler: &fakeScheduler{}})
	if _, err := service.CheckNow(context.Background()); err != nil {
		t.Fatalf("CheckNow() error = %v", err)
	}
	if _, err := service.Download(context.Background()); err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	got := service.Snapshot()
	if got.State != StateError || got.RetryState != StateAvailable {
		t.Fatalf("snapshot state = %#v, want retryable error", got)
	}
	if got.ErrorCode != string(CodeSignatureInvalid) {
		t.Fatalf("snapshot error code = %q, want %q", got.ErrorCode, CodeSignatureInvalid)
	}
}

func TestLateProgressFromCompletedAndRetriedDownloadsIsIgnored(t *testing.T) {
	var service *Service
	var lateProgress func(int)
	attempt := 0
	driver := &fakeDriver{
		checkResult: Update{Available: true, Version: "1.2.3"},
		download: func(_ context.Context, progress func(int)) error {
			attempt++
			if attempt == 1 {
				lateProgress = progress
				return errors.New("temporary failure")
			}
			lateProgress(91)
			if got := service.Snapshot(); got.State != StateDownloading || got.Progress != 0 {
				t.Fatalf("snapshot after stale retry progress = %#v", got)
			}
			progress(35)
			return nil
		},
	}
	service = NewService(Config{Enabled: true, Driver: driver, Scheduler: &fakeScheduler{}})
	if _, err := service.CheckNow(context.Background()); err != nil {
		t.Fatalf("CheckNow() error = %v", err)
	}
	if _, err := service.Download(context.Background()); err != nil {
		t.Fatalf("first Download() error = %v", err)
	}
	if _, err := service.Download(context.Background()); err != nil {
		t.Fatalf("retry Download() error = %v", err)
	}

	lateProgress(4)
	if got := service.Snapshot(); got.State != StateReady || got.Progress != 100 {
		t.Fatalf("snapshot after late completed progress = %#v", got)
	}
}

func TestDisabledServiceSchedulesAndCallsNothing(t *testing.T) {
	driver := &fakeDriver{}
	scheduler := &fakeScheduler{}
	service := NewService(Config{Enabled: false, Driver: driver, Scheduler: scheduler})
	service.Start(context.Background())

	if got := service.Snapshot(); got.State != StateDisabled {
		t.Fatalf("disabled snapshot = %#v", got)
	}
	if _, err := service.CheckNow(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled CheckNow() error = %v, want ErrDisabled", err)
	}
	if _, err := service.Download(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled Download() error = %v, want ErrDisabled", err)
	}
	if _, err := service.Install(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled Install() error = %v, want ErrDisabled", err)
	}
	service.Stop()
	service.Stop()

	if got := scheduler.callCount(); got != 0 {
		t.Fatalf("disabled scheduler calls = %d, want 0", got)
	}
	if got := driver.totalCalls(); got != 0 {
		t.Fatalf("disabled driver calls = %d, want 0", got)
	}
}

func TestStopCleansUpOnce(t *testing.T) {
	driver := &fakeDriver{}
	scheduler := &fakeScheduler{}
	service := NewService(Config{Enabled: true, Driver: driver, Scheduler: scheduler})
	service.Start(context.Background())

	service.Stop()
	service.Stop()

	if got := driver.stopCallCount(); got != 1 {
		t.Fatalf("driver Stop calls = %d, want 1", got)
	}
	if !scheduler.timerStopped(0) {
		t.Fatal("startup timer was not stopped")
	}
	if got := pendingTimerCount(service); got != 0 {
		t.Fatalf("pending timers after Stop = %d, want 0", got)
	}
}

type operationResult struct {
	snapshot Snapshot
	err      error
}

type fakeDriver struct {
	mu sync.Mutex

	checkResult Update
	checkErr    error
	downloadErr error
	installErr  error
	check       func(context.Context) (Update, error)
	download    func(context.Context, func(int)) error
	install     func(context.Context) error
	stop        func() error

	checkCalls    int
	downloadCalls int
	installCalls  int
	stopCalls     int
}

func (f *fakeDriver) Check(ctx context.Context) (Update, error) {
	f.mu.Lock()
	f.checkCalls++
	check := f.check
	result := f.checkResult
	err := f.checkErr
	f.mu.Unlock()
	if check != nil {
		return check(ctx)
	}
	return result, err
}

func (f *fakeDriver) Download(ctx context.Context, progress func(int)) error {
	f.mu.Lock()
	f.downloadCalls++
	download := f.download
	err := f.downloadErr
	f.mu.Unlock()
	if download != nil {
		return download(ctx, progress)
	}
	return err
}

func (f *fakeDriver) Install(ctx context.Context) error {
	f.mu.Lock()
	f.installCalls++
	install := f.install
	err := f.installErr
	f.mu.Unlock()
	if install != nil {
		return install(ctx)
	}
	return err
}

func (f *fakeDriver) Stop() error {
	f.mu.Lock()
	f.stopCalls++
	stop := f.stop
	f.mu.Unlock()
	if stop != nil {
		return stop()
	}
	return nil
}

func (f *fakeDriver) setCheckResult(update Update) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkResult = update
}

func (f *fakeDriver) setCheckError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkErr = err
}

func (f *fakeDriver) setDownloadError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.downloadErr = err
}

func (f *fakeDriver) setInstallError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installErr = err
}

func (f *fakeDriver) checkCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checkCalls
}

func (f *fakeDriver) downloadCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.downloadCalls
}

func (f *fakeDriver) installCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.installCalls
}

func (f *fakeDriver) stopCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopCalls
}

func (f *fakeDriver) totalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checkCalls + f.downloadCalls + f.installCalls + f.stopCalls
}

type fakeScheduler struct {
	mu    sync.Mutex
	calls []scheduledCall
}

type scheduledCall struct {
	delay time.Duration
	fn    func()
	timer *fakeTimer
}

func (s *fakeScheduler) AfterFunc(delay time.Duration, fn func()) Timer {
	timer := &fakeTimer{}
	s.mu.Lock()
	s.calls = append(s.calls, scheduledCall{delay: delay, fn: fn, timer: timer})
	s.mu.Unlock()
	return timer
}

func (s *fakeScheduler) fire(index int) {
	s.mu.Lock()
	call := s.calls[index]
	s.mu.Unlock()
	call.timer.fire(call.fn)
}

func (s *fakeScheduler) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *fakeScheduler) delay(index int) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[index].delay
}

func (s *fakeScheduler) activeTimerCount() int {
	s.mu.Lock()
	calls := append([]scheduledCall(nil), s.calls...)
	s.mu.Unlock()
	active := 0
	for _, call := range calls {
		if call.timer.active() {
			active++
		}
	}
	return active
}

func (s *fakeScheduler) timerStopped(index int) bool {
	s.mu.Lock()
	timer := s.calls[index].timer
	s.mu.Unlock()
	return timer.isStopped()
}

type fakeTimer struct {
	mu      sync.Mutex
	stopped bool
	fired   bool
}

func (t *fakeTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	return true
}

func (t *fakeTimer) fire(fn func()) {
	t.mu.Lock()
	if t.stopped || t.fired {
		t.mu.Unlock()
		return
	}
	t.fired = true
	t.mu.Unlock()
	fn()
}

func (t *fakeTimer) active() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.stopped && !t.fired
}

func (t *fakeTimer) isStopped() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopped
}

func pendingTimerCount(service *Service) int {
	service.mu.Lock()
	defer service.mu.Unlock()
	return len(service.timers)
}
