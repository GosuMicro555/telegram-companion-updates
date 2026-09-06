package wails

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/buildinfo"
	"telegram-companion/internal/license"
)

type startupGateStub struct {
	mu       sync.Mutex
	snapshot license.Snapshot
	tokens   []string
	activate func(string) license.Snapshot
}

func (g *startupGateStub) Snapshot() license.Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.snapshot
}

func (g *startupGateStub) Activate(token string) license.Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tokens = append(g.tokens, token)
	if g.activate != nil {
		g.snapshot = g.activate(token)
	}
	return g.snapshot
}

func (g *startupGateStub) activatedTokens() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.tokens...)
}

func recoveryGate() *startupGateStub {
	return &startupGateStub{snapshot: license.Snapshot{
		Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateActivated, MachineID: "ABC123",
	}}
}

func TestStartupBindingsExposeOnlyClosedSafeStatus(t *testing.T) {
	bindings := NewStartupBindings("recovery", "/Users/private/license raw-account-secret", nil, nil, nil, nil, nil)

	got := bindings.GetDesktopStartupStatus()
	encoded, err := json.Marshal(got)

	require.NoError(t, err)
	require.Equal(t, DesktopStartupStatusDTO{Mode: "recovery", ErrorCode: "runtime_unavailable"}, got)
	for _, forbidden := range []string{"Users", "license", "account", "secret", "raw"} {
		require.NotContains(t, string(encoded), forbidden)
	}
}

func TestStartupBindingsRetryFailureIsSafeAndRetryable(t *testing.T) {
	requester := &activationRestartRequesterStub{request: func(_ context.Context, call int) error {
		if call == 1 {
			return errors.New("spawn /Users/private/Telegram Companion failed with secret")
		}
		return nil
	}}
	quitCalls := 0
	bindings := NewStartupBindings(
		"recovery", "keychain_unavailable", recoveryGate(), nil, requester,
		func(context.Context) { quitCalls++ }, func(callback func()) { callback() },
	)

	first := bindings.RetryStartup()
	persistedFailure := bindings.GetDesktopStartupStatus()
	second := bindings.RetryStartup()
	third := bindings.RetryStartup()

	require.Equal(t, DesktopStartupStatusDTO{Mode: "recovery", ErrorCode: "relaunch_failed"}, first)
	require.Equal(t, first, persistedFailure)
	require.NotContains(t, first.ErrorCode, "Users")
	require.Equal(t, DesktopStartupStatusDTO{Mode: "recovery", ErrorCode: "relaunch_failed", Restarting: true}, second)
	require.Equal(t, second, third)
	require.Equal(t, 2, requester.CallCount())
	require.Equal(t, 1, quitCalls)
}

func TestStartupBindingsChooseAnotherLicenseVerifiesAndStoresBeforeOneRestart(t *testing.T) {
	stored := false
	gate := recoveryGate()
	gate.activate = func(token string) license.Snapshot {
		require.Equal(t, "TCPLIC1.schema2.signature", token)
		stored = true
		return license.Snapshot{Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateActivated, MachineID: "ABC123"}
	}
	requester := &activationRestartRequesterStub{request: func(context.Context, int) error {
		require.True(t, stored, "replacement must be stored before restart")
		return nil
	}}
	quitCalls := 0
	bindings := NewStartupBindings(
		"recovery", "seed_incompatible", gate,
		func(context.Context) (string, error) { return " TCPLIC1.schema2.signature\n", nil },
		requester, func(context.Context) { quitCalls++ }, func(callback func()) { callback() },
	)

	first := bindings.ChooseAnotherLicense()
	second := bindings.ChooseAnotherLicense()

	require.Equal(t, DesktopStartupStatusDTO{Mode: "recovery", ErrorCode: "seed_incompatible", Restarting: true}, first)
	require.Equal(t, first, second)
	require.Equal(t, []string{"TCPLIC1.schema2.signature"}, gate.activatedTokens())
	require.Equal(t, 1, requester.CallCount())
	require.Equal(t, 1, quitCalls)
}

func TestStartupBindingsChooseAnotherLicenseCancelPreservesState(t *testing.T) {
	gate := recoveryGate()
	requester := &activationRestartRequesterStub{}
	bindings := NewStartupBindings(
		"recovery", "seed_license_required", gate,
		func(context.Context) (string, error) { return "", nil }, requester, func(context.Context) {},
		func(callback func()) { callback() },
	)

	got := bindings.ChooseAnotherLicense()

	require.Equal(t, DesktopStartupStatusDTO{Mode: "recovery", ErrorCode: "seed_license_required"}, got)
	require.Empty(t, gate.activatedTokens())
	require.Zero(t, requester.CallCount())
}

func TestStartupBindingsChooseAnotherLicenseInvalidTokenPreservesState(t *testing.T) {
	gate := recoveryGate()
	gate.activate = func(string) license.Snapshot {
		return license.Snapshot{Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateError, ErrorCode: "wrong_machine"}
	}
	requester := &activationRestartRequesterStub{}
	bindings := NewStartupBindings(
		"recovery", "seed_incompatible", gate,
		func(context.Context) (string, error) { return "invalid-secret-token", nil }, requester,
		func(context.Context) {}, func(callback func()) { callback() },
	)

	got := bindings.ChooseAnotherLicense()
	encoded, err := json.Marshal(got)

	require.NoError(t, err)
	require.Equal(t, DesktopStartupStatusDTO{Mode: "recovery", ErrorCode: "seed_incompatible"}, got)
	require.Zero(t, requester.CallCount())
	require.NotContains(t, string(encoded), "invalid-secret-token")
}

func TestStartupBindingsConcurrentActionsShareOneRestartRequest(t *testing.T) {
	gate := recoveryGate()
	gate.activate = func(string) license.Snapshot {
		return license.Snapshot{Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateActivated}
	}
	requester := &activationRestartRequesterStub{}
	var quitMu sync.Mutex
	quitCalls := 0
	bindings := NewStartupBindings(
		"recovery", "storage_unavailable", gate,
		func(context.Context) (string, error) { return "TCPLIC1.schema2.signature", nil }, requester,
		func(context.Context) { quitMu.Lock(); quitCalls++; quitMu.Unlock() }, func(callback func()) { callback() },
	)

	const actions = 16
	var wait sync.WaitGroup
	wait.Add(actions)
	for index := range actions {
		go func(index int) {
			defer wait.Done()
			if index%2 == 0 {
				bindings.RetryStartup()
				return
			}
			bindings.ChooseAnotherLicense()
		}(index)
	}
	wait.Wait()

	require.Equal(t, 1, requester.CallCount())
	quitMu.Lock()
	require.Equal(t, 1, quitCalls)
	quitMu.Unlock()
}
