package wails

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/buildinfo"
	"telegram-companion/internal/license"
)

type activationRestartRequesterStub struct {
	mu      sync.Mutex
	calls   int
	request func(context.Context, int) error
}

func (r *activationRestartRequesterStub) Request(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.request == nil {
		return nil
	}
	return r.request(ctx, r.calls)
}

func (r *activationRestartRequesterStub) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type activationGateStub struct {
	snapshot  license.Snapshot
	activated []string
	activate  func(string) license.Snapshot
}

func (g *activationGateStub) Snapshot() license.Snapshot { return g.snapshot }
func (g *activationGateStub) Activate(token string) license.Snapshot {
	g.activated = append(g.activated, token)
	if g.activate != nil {
		g.snapshot = g.activate(token)
	}
	return g.snapshot
}

func TestActivationBindingsExposeSafeSnapshot(t *testing.T) {
	gate := &activationGateStub{snapshot: license.Snapshot{
		Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateNeedsActivation, MachineID: "ABC123",
	}}
	bindings := NewActivationBindings(gate, nil, nil, nil)

	got := bindings.GetActivationStatus()

	require.Equal(t, ActivationStatusDTO{
		Mode: "public-macos-arm64", State: "needs_activation", MachineID: "ABC123",
	}, got)
}

func TestActivationBindingsExposeOnlySafeTerminalRevocationStates(t *testing.T) {
	for _, test := range []struct {
		state license.GateState
		code  string
	}{
		{state: license.StateRevoked, code: license.GateErrorRevoked},
		{state: license.StateCheckRequired, code: license.GateErrorCheckRequired},
	} {
		gate := &activationGateStub{snapshot: license.Snapshot{
			Mode: buildinfo.ChannelPublicMacOSARM64, State: test.state, MachineID: "ABC123", ErrorCode: test.code,
		}}
		got := NewActivationBindings(gate, nil, nil, nil).GetActivationStatus()
		require.Equal(t, string(test.state), got.State)
		require.Equal(t, test.code, got.ErrorCode)
		require.NotContains(t, got.ErrorCode, "license-id")
	}
}

func TestActivateLicenseKeyTrimsPasteAndSchedulesOneRestart(t *testing.T) {
	gate := &activationGateStub{snapshot: license.Snapshot{
		Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateNeedsActivation, MachineID: "ABC123",
	}}
	gate.activate = func(string) license.Snapshot {
		return license.Snapshot{Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateActivated, MachineID: "ABC123"}
	}
	restarts := 0
	scheduled := 0
	bindings := NewActivationBindings(gate, nil, func() { restarts++ }, func(callback func()) {
		scheduled++
		callback()
	})

	first := bindings.ActivateLicenseKey("  TCPLIC1.payload.signature\n")
	second := bindings.ActivateLicenseKey("TCPLIC1.payload.signature")

	require.Equal(t, "activated", first.State)
	require.Equal(t, "activated", second.State)
	require.Equal(t, []string{"TCPLIC1.payload.signature", "TCPLIC1.payload.signature"}, gate.activated)
	require.Equal(t, 1, scheduled)
	require.Equal(t, 1, restarts)
}

func TestRejectedActivationDoesNotScheduleRestart(t *testing.T) {
	gate := &activationGateStub{snapshot: license.Snapshot{
		Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateNeedsActivation, MachineID: "ABC123",
	}}
	gate.activate = func(string) license.Snapshot {
		return license.Snapshot{Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateError, MachineID: "ABC123", ErrorCode: "wrong_machine"}
	}
	restarts := 0
	bindings := NewActivationBindings(gate, nil, func() { restarts++ }, func(callback func()) { callback() })

	got := bindings.ActivateLicenseKey("wrong")

	require.Equal(t, "error", got.State)
	require.Equal(t, "wrong_machine", got.ErrorCode)
	require.Zero(t, restarts)
}

func TestImportLicenseFileUsesRootContextAndRedactsSelectorFailure(t *testing.T) {
	type contextKey string
	const key contextKey = "root"
	gate := &activationGateStub{snapshot: license.Snapshot{
		Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateNeedsActivation, MachineID: "ABC123",
	}}
	binding := NewActivationBindings(gate, func(ctx context.Context) (string, error) {
		require.Equal(t, "activation", ctx.Value(key))
		return "", errors.New("/Users/private/license.tcomplicense")
	}, nil, nil)
	binding.Startup(context.WithValue(context.Background(), key, "activation"))

	got := binding.ImportLicenseFile()

	require.Equal(t, "error", got.State)
	require.Equal(t, "import_failed", got.ErrorCode)
	require.NotContains(t, got.ErrorCode, "Users")
}

func TestImportLicenseFileActivatesSelectedToken(t *testing.T) {
	gate := &activationGateStub{snapshot: license.Snapshot{
		Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateNeedsActivation, MachineID: "ABC123",
	}}
	gate.activate = func(string) license.Snapshot {
		return license.Snapshot{Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateActivated, MachineID: "ABC123"}
	}
	binding := NewActivationBindings(gate, func(context.Context) (string, error) {
		return "TCPLIC1.file.signature", nil
	}, nil, func(callback func()) { callback() })

	got := binding.ImportLicenseFile()

	require.Equal(t, "activated", got.State)
	require.Equal(t, []string{"TCPLIC1.file.signature"}, gate.activated)
}

func TestContextRestartReceivesWailsRootContext(t *testing.T) {
	type contextKey string
	const key contextKey = "root"
	gate := &activationGateStub{snapshot: license.Snapshot{
		Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateNeedsActivation, MachineID: "ABC123",
	}}
	gate.activate = func(string) license.Snapshot {
		return license.Snapshot{Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateActivated, MachineID: "ABC123"}
	}
	restartedWith := ""
	binding := NewActivationBindingsWithContextRestart(gate, nil, func(ctx context.Context) {
		restartedWith, _ = ctx.Value(key).(string)
	}, func(callback func()) { callback() })
	binding.Startup(context.WithValue(context.Background(), key, "wails"))

	binding.ActivateLicenseKey("TCPLIC1.payload.signature")

	require.Equal(t, "wails", restartedWith)
}

func TestActivationRelaunchFailureIsSafeDoesNotQuitAndCanRetry(t *testing.T) {
	gate := &activationGateStub{snapshot: license.Snapshot{
		Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateNeedsActivation, MachineID: "ABC123",
	}}
	gate.activate = func(string) license.Snapshot {
		return license.Snapshot{Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateActivated, MachineID: "ABC123"}
	}
	requester := &activationRestartRequesterStub{request: func(_ context.Context, call int) error {
		if call == 1 {
			return errors.New("start /Users/private/Telegram Companion: permission denied")
		}
		return nil
	}}
	quitCalls := 0
	scheduled := 0
	binding := NewActivationBindingsWithRestartRequester(
		gate,
		nil,
		requester,
		func(context.Context) { quitCalls++ },
		func(callback func()) { scheduled++; callback() },
	)

	first := binding.ActivateLicenseKey("TCPLIC1.payload.signature")
	second := binding.ActivateLicenseKey("TCPLIC1.payload.signature")
	third := binding.ActivateLicenseKey("TCPLIC1.payload.signature")

	require.Equal(t, "error", first.State)
	require.Equal(t, "relaunch_failed", first.ErrorCode)
	require.NotContains(t, first.ErrorCode, "Users")
	require.Equal(t, "activated", second.State)
	require.Equal(t, "activated", third.State)
	require.Equal(t, 2, requester.CallCount())
	require.Equal(t, 1, scheduled)
	require.Equal(t, 1, quitCalls)
}

func TestActivationConcurrentRequestsPrepareAndQuitOnce(t *testing.T) {
	gate := &concurrentActivatedGate{snapshot: license.Snapshot{
		Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateActivated, MachineID: "ABC123",
	}}
	requester := &activationRestartRequesterStub{}
	var quitMu sync.Mutex
	quitCalls := 0
	binding := NewActivationBindingsWithRestartRequester(
		gate,
		nil,
		requester,
		func(context.Context) {
			quitMu.Lock()
			quitCalls++
			quitMu.Unlock()
		},
		func(callback func()) { callback() },
	)

	const requests = 16
	var wait sync.WaitGroup
	results := make(chan ActivationStatusDTO, requests)
	wait.Add(requests)
	for range requests {
		go func() {
			defer wait.Done()
			results <- binding.ActivateLicenseKey("TCPLIC1.payload.signature")
		}()
	}
	wait.Wait()
	close(results)
	for got := range results {
		require.Equal(t, "activated", got.State)
	}

	quitMu.Lock()
	defer quitMu.Unlock()
	require.Equal(t, 1, requester.CallCount())
	require.Equal(t, 1, quitCalls)
}

type concurrentActivatedGate struct {
	mu       sync.Mutex
	snapshot license.Snapshot
}

func (g *concurrentActivatedGate) Snapshot() license.Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.snapshot
}

func (g *concurrentActivatedGate) Activate(string) license.Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.snapshot
}
