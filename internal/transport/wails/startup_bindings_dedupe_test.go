package wails

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"telegram-companion/internal/buildinfo"
	"telegram-companion/internal/license"
)

func TestStartupBindingsDeduplicatesConcurrentPickerAfterCancelOrError(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "cancel"},
		{name: "picker error", err: errors.New("native picker failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			var importerCalls atomic.Int32
			bindings := NewStartupBindings(
				"recovery", "seed_license_required", recoveryGate(),
				func(context.Context) (string, error) {
					if importerCalls.Add(1) == 1 {
						entered <- struct{}{}
						<-release
					}
					return "", test.err
				},
				nil, func(context.Context) {}, func(callback func()) { callback() },
			)
			firstDone := make(chan DesktopStartupStatusDTO, 1)
			go func() { firstDone <- bindings.ChooseAnotherLicense() }()
			awaitStartupSignal(t, entered, "first picker")

			statusDone := make(chan DesktopStartupStatusDTO, 1)
			go func() { statusDone <- bindings.GetDesktopStartupStatus() }()
			secondDone := make(chan DesktopStartupStatusDTO, 1)
			go func() { secondDone <- bindings.ChooseAnotherLicense() }()
			statusReturned := startupStatusReturnsPromptly(statusDone)
			secondReturned := startupStatusReturnsPromptly(secondDone)
			close(release)
			awaitStartupStatus(t, firstDone, "first picker result")
			if !statusReturned {
				awaitStartupStatus(t, statusDone, "status after picker release")
				t.Error("GetDesktopStartupStatus blocked behind the native picker")
			}
			if !secondReturned {
				awaitStartupStatus(t, secondDone, "second choose after picker release")
				t.Error("concurrent ChooseAnotherLicense queued behind the first picker")
			}
			if got := importerCalls.Load(); got != 1 {
				t.Fatalf("picker calls after two concurrent actions = %d, want 1", got)
			}

			bindings.ChooseAnotherLicense()
			if got := importerCalls.Load(); got != 2 {
				t.Fatalf("picker calls after retryable failure = %d, want 2", got)
			}
		})
	}
}

func TestStartupBindingsRetryAndChooseShareDedupeWithoutBlockingStatus(t *testing.T) {
	requester := &blockingStartupRestartRequester{
		entered: make(chan struct{}, 1), release: make(chan struct{}),
		firstErr: errors.New("spawn failed"),
	}
	var importerCalls atomic.Int32
	var quitCalls atomic.Int32
	bindings := NewStartupBindings(
		"recovery", "storage_unavailable", recoveryGate(),
		func(context.Context) (string, error) {
			importerCalls.Add(1)
			return "", nil
		},
		requester, func(context.Context) { quitCalls.Add(1) }, func(callback func()) { callback() },
	)
	firstDone := make(chan DesktopStartupStatusDTO, 1)
	go func() { firstDone <- bindings.RetryStartup() }()
	awaitStartupSignal(t, requester.entered, "restart request")

	statusDone := make(chan DesktopStartupStatusDTO, 1)
	go func() { statusDone <- bindings.GetDesktopStartupStatus() }()
	chooseDone := make(chan DesktopStartupStatusDTO, 1)
	go func() { chooseDone <- bindings.ChooseAnotherLicense() }()
	statusReturned := startupStatusReturnsPromptly(statusDone)
	chooseReturned := startupStatusReturnsPromptly(chooseDone)
	close(requester.release)
	failed := awaitStartupStatus(t, firstDone, "failed restart result")
	if !statusReturned {
		awaitStartupStatus(t, statusDone, "status after restart release")
		t.Error("GetDesktopStartupStatus blocked behind restart.Request")
	}
	if !chooseReturned {
		awaitStartupStatus(t, chooseDone, "choose after restart release")
		t.Error("ChooseAnotherLicense queued behind RetryStartup")
	}
	if importerCalls.Load() != 0 {
		t.Fatalf("picker calls during in-progress retry = %d, want zero", importerCalls.Load())
	}
	if failed.ErrorCode != "relaunch_failed" || failed.Restarting {
		t.Fatalf("failed retry status = %+v", failed)
	}

	succeeded := bindings.RetryStartup()
	if !succeeded.Restarting || succeeded.ErrorCode != "relaunch_failed" {
		t.Fatalf("retry after failure status = %+v, want restarting relaunch_failed", succeeded)
	}
	if requester.calls.Load() != 2 {
		t.Fatalf("restart requests = %d, want 2", requester.calls.Load())
	}
	if quitCalls.Load() != 1 {
		t.Fatalf("quit calls = %d, want 1", quitCalls.Load())
	}
}

func TestStartupBindingsDoesNotHoldStateLockAcrossActivationGate(t *testing.T) {
	gate := &blockingStartupGate{entered: make(chan struct{}, 1), release: make(chan struct{})}
	requester := &blockingStartupRestartRequester{}
	bindings := NewStartupBindings(
		"recovery", "seed_incompatible", gate,
		func(context.Context) (string, error) { return "TCPLIC1.schema2.signature", nil },
		requester, func(context.Context) {}, func(callback func()) { callback() },
	)
	firstDone := make(chan DesktopStartupStatusDTO, 1)
	go func() { firstDone <- bindings.ChooseAnotherLicense() }()
	awaitStartupSignal(t, gate.entered, "activation gate")

	statusDone := make(chan DesktopStartupStatusDTO, 1)
	go func() { statusDone <- bindings.GetDesktopStartupStatus() }()
	retryDone := make(chan DesktopStartupStatusDTO, 1)
	go func() { retryDone <- bindings.RetryStartup() }()
	statusReturned := startupStatusReturnsPromptly(statusDone)
	retryReturned := startupStatusReturnsPromptly(retryDone)
	close(gate.release)
	got := awaitStartupStatus(t, firstDone, "activation result")
	if !statusReturned {
		awaitStartupStatus(t, statusDone, "status after gate release")
		t.Error("GetDesktopStartupStatus blocked behind activation Gate")
	}
	if !retryReturned {
		awaitStartupStatus(t, retryDone, "retry after gate release")
		t.Error("RetryStartup queued behind ChooseAnotherLicense")
	}
	if !got.Restarting {
		t.Fatalf("activation result = %+v, want restarting", got)
	}
	if requester.calls.Load() != 1 {
		t.Fatalf("restart requests = %d, want 1", requester.calls.Load())
	}
}

type blockingStartupRestartRequester struct {
	entered  chan struct{}
	release  chan struct{}
	firstErr error
	calls    atomic.Int32
}

func (r *blockingStartupRestartRequester) Request(context.Context) error {
	call := r.calls.Add(1)
	if call == 1 && r.entered != nil {
		r.entered <- struct{}{}
	}
	if call == 1 && r.release != nil {
		<-r.release
	}
	if call == 1 {
		return r.firstErr
	}
	return nil
}

type blockingStartupGate struct {
	entered chan struct{}
	release chan struct{}
}

func (g *blockingStartupGate) Snapshot() license.Snapshot {
	return license.Snapshot{Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateActivated}
}

func (g *blockingStartupGate) Activate(string) license.Snapshot {
	g.entered <- struct{}{}
	<-g.release
	return license.Snapshot{Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateActivated}
}

func startupStatusReturnsPromptly(status <-chan DesktopStartupStatusDTO) bool {
	select {
	case <-status:
		return true
	case <-time.After(250 * time.Millisecond):
		return false
	}
}

func awaitStartupSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func awaitStartupStatus(t *testing.T, status <-chan DesktopStartupStatusDTO, label string) DesktopStartupStatusDTO {
	t.Helper()
	select {
	case got := <-status:
		return got
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return DesktopStartupStatusDTO{}
	}
}
