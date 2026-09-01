package license

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"telegram-companion/internal/buildinfo"
	"telegram-companion/internal/revocation"
)

func TestGateStateValuesAreStable(t *testing.T) {
	want := map[GateState]string{
		StateChecking:        "checking",
		StateNeedsActivation: "needs_activation",
		StateActivated:       "activated",
		StateError:           "error",
		StateRevoked:         "revoked",
		StateCheckRequired:   "check_required",
	}
	for state, value := range want {
		if string(state) != value {
			t.Fatalf("state %q = %q, want %q", state, string(state), value)
		}
	}
}

func TestPublicGateChecksRevocationOnlyAfterLocalVerification(t *testing.T) {
	publicKey, _ := gateTestKey(t)
	store := &fakeGateStore{token: testGateToken}
	verified := false
	checker := &fakeRevocationChecker{decision: revocation.Active}
	gate := NewGateWithRevocationDependencies(
		publicGateInfo(publicKey),
		store,
		func() (string, error) { return testGateMachineID, nil },
		func(token string, _ VerifyOptions) (Payload, error) {
			if token != testGateToken {
				t.Fatalf("verified token = %q", token)
			}
			verified = true
			return gateTestPayload(), nil
		},
		checkerFuncForGate(func(_ context.Context, licenseID string) (revocation.Decision, error) {
			if !verified {
				t.Fatal("revocation checker ran before local verification")
			}
			if licenseID != gateTestPayload().LicenseID {
				t.Fatalf("checker LicenseID = %q", licenseID)
			}
			return revocation.Active, nil
		}),
	)
	if gate.Snapshot().State != StateActivated || !gate.Authorized() {
		t.Fatalf("snapshot=%#v authorized=%v", gate.Snapshot(), gate.Authorized())
	}

	checker.calls.Store(0)
	invalid := NewGateWithRevocationDependencies(
		publicGateInfo(publicKey),
		store,
		func() (string, error) { return testGateMachineID, nil },
		func(string, VerifyOptions) (Payload, error) { return Payload{}, ErrInvalidSignature },
		checker,
	)
	if invalid.Snapshot().State != StateError || checker.calls.Load() != 0 {
		t.Fatalf("invalid snapshot=%#v checker calls=%d", invalid.Snapshot(), checker.calls.Load())
	}
}

func TestPublicGateMapsRevocationDecisionsFailClosed(t *testing.T) {
	publicKey, _ := gateTestKey(t)
	tests := []struct {
		name       string
		decision   revocation.Decision
		checkerErr error
		wantState  GateState
		wantCode   string
		wantAuth   bool
	}{
		{name: "active", decision: revocation.Active, wantState: StateActivated, wantAuth: true},
		{name: "grace", decision: revocation.ActiveInGrace, wantState: StateActivated, wantAuth: true},
		{name: "revoked", decision: revocation.Revoked, wantState: StateRevoked, wantCode: GateErrorRevoked},
		{name: "check required", decision: revocation.CheckRequired, wantState: StateCheckRequired, wantCode: GateErrorCheckRequired},
		{name: "checker error", decision: revocation.Active, checkerErr: errors.New("private backend detail"), wantState: StateCheckRequired, wantCode: GateErrorCheckRequired},
		{name: "unknown decision", decision: revocation.Decision("unknown"), wantState: StateCheckRequired, wantCode: GateErrorCheckRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gate := NewGateWithRevocationDependencies(
				publicGateInfo(publicKey),
				&fakeGateStore{token: testGateToken},
				func() (string, error) { return testGateMachineID, nil },
				func(string, VerifyOptions) (Payload, error) { return gateTestPayload(), nil },
				&fakeRevocationChecker{decision: test.decision, err: test.checkerErr},
			)
			got := gate.Snapshot()
			if got.State != test.wantState || got.ErrorCode != test.wantCode || gate.Authorized() != test.wantAuth {
				t.Fatalf("snapshot=%#v authorized=%v", got, gate.Authorized())
			}
			if got.ErrorCode != "" {
				assertSafeGateErrorCode(t, got.ErrorCode)
			}
		})
	}
}

func TestActivateRequiresFreshActiveRevocationDecisionBeforeSaving(t *testing.T) {
	publicKey, _ := gateTestKey(t)
	for _, decision := range []revocation.Decision{revocation.ActiveInGrace, revocation.Revoked, revocation.CheckRequired} {
		t.Run(string(decision), func(t *testing.T) {
			store := &fakeGateStore{loadErr: ErrNotFound}
			gate := NewGateWithRevocationDependencies(
				publicGateInfo(publicKey),
				store,
				func() (string, error) { return testGateMachineID, nil },
				func(string, VerifyOptions) (Payload, error) { return gateTestPayload(), nil },
				&fakeRevocationChecker{decision: decision},
			)
			got := gate.Activate(testGateToken)
			if got.State == StateActivated || gate.Authorized() || store.saveCalls() != 0 {
				t.Fatalf("decision=%q snapshot=%#v saves=%d", decision, got, store.saveCalls())
			}
		})
	}

	store := &fakeGateStore{loadErr: ErrNotFound}
	gate := NewGateWithRevocationDependencies(
		publicGateInfo(publicKey),
		store,
		func() (string, error) { return testGateMachineID, nil },
		func(string, VerifyOptions) (Payload, error) { return gateTestPayload(), nil },
		&fakeRevocationChecker{decision: revocation.Active},
	)
	if got := gate.Activate(testGateToken); got.State != StateActivated || store.saveCalls() != 1 {
		t.Fatalf("active replacement snapshot=%#v saves=%d", got, store.saveCalls())
	}
}

func TestGatePublishesTerminalRuntimeDecisionAndHidesLicenseID(t *testing.T) {
	publicKey, _ := gateTestKey(t)
	for _, test := range []struct {
		decision revocation.Decision
		state    GateState
		code     string
	}{
		{decision: revocation.Revoked, state: StateRevoked, code: GateErrorRevoked},
		{decision: revocation.CheckRequired, state: StateCheckRequired, code: GateErrorCheckRequired},
	} {
		t.Run(string(test.decision), func(t *testing.T) {
			gate := NewGateWithRevocationDependencies(
				publicGateInfo(publicKey),
				&fakeGateStore{token: testGateToken},
				func() (string, error) { return testGateMachineID, nil },
				func(string, VerifyOptions) (Payload, error) { return gateTestPayload(), nil },
				&fakeRevocationChecker{decision: revocation.Active},
			)
			licenseID, ok := gate.AuthorizedLicenseID()
			if !ok || licenseID != gateTestPayload().LicenseID {
				t.Fatalf("AuthorizedLicenseID() = %q, %v", licenseID, ok)
			}

			got := gate.ApplyRevocationDecision(test.decision)
			if got.State != test.state || got.ErrorCode != test.code || gate.Authorized() {
				t.Fatalf("terminal snapshot=%#v authorized=%v", got, gate.Authorized())
			}
			if licenseID, ok := gate.AuthorizedLicenseID(); ok || licenseID != "" {
				t.Fatalf("terminal AuthorizedLicenseID() = %q, %v", licenseID, ok)
			}
		})
	}
}

func TestGateIgnoresNonterminalRuntimeDecision(t *testing.T) {
	publicKey, _ := gateTestKey(t)
	gate := NewGateWithRevocationDependencies(
		publicGateInfo(publicKey),
		&fakeGateStore{token: testGateToken},
		func() (string, error) { return testGateMachineID, nil },
		func(string, VerifyOptions) (Payload, error) { return gateTestPayload(), nil },
		&fakeRevocationChecker{decision: revocation.Active},
	)
	before := gate.Snapshot()
	if got := gate.ApplyRevocationDecision(revocation.Active); got != before || !gate.Authorized() {
		t.Fatalf("nonterminal decision changed snapshot: before=%#v after=%#v", before, got)
	}
}

func TestInternalGateIsImmediatelyAuthorizedWithoutLicenseDependencies(t *testing.T) {
	gate := NewGateWithDependencies(
		buildinfo.Info{Channel: buildinfo.ChannelInternal, ProductID: buildinfo.ProductID},
		nil,
		func() (string, error) { panic("internal gate requested a Machine ID") },
		func(string, VerifyOptions) (Payload, error) { panic("internal gate verified a license") },
	)

	want := Snapshot{Mode: buildinfo.ChannelInternal, State: StateActivated}
	if got := gate.Snapshot(); got != want {
		t.Fatalf("Snapshot() = %#v, want %#v", got, want)
	}
	if !gate.Authorized() {
		t.Fatal("internal gate is not authorized")
	}
}

func TestPublicGateNeedsActivationWhenNoLicenseIsStored(t *testing.T) {
	store := &fakeGateStore{loadErr: ErrNotFound}
	gate := newPublicTestGate(t, store, func(string, VerifyOptions) (Payload, error) {
		t.Fatal("verifier called without a stored license")
		return Payload{}, nil
	})

	want := Snapshot{
		Mode:      buildinfo.ChannelPublicMacOSARM64,
		State:     StateNeedsActivation,
		MachineID: testGateMachineID,
	}
	if got := gate.Snapshot(); got != want {
		t.Fatalf("Snapshot() = %#v, want %#v", got, want)
	}
	if gate.Authorized() {
		t.Fatal("unlicensed public gate is authorized")
	}
}

func TestPublicGateVerifiesStoredLicenseWithBuildAndMachineOptions(t *testing.T) {
	publicKey, privateKey := gateTestKey(t)
	payload := gateTestPayload()
	token := gateSignToken(t, privateKey, payload)
	store := &fakeGateStore{token: token}
	info := publicGateInfo(publicKey)

	gate := NewGateWithDependencies(
		info,
		store,
		func() (string, error) { return payload.MachineID, nil },
		ParseAndVerify,
	)

	if got := gate.Snapshot(); got != (Snapshot{
		Mode:      buildinfo.ChannelPublicMacOSARM64,
		State:     StateActivated,
		MachineID: payload.MachineID,
	}) {
		t.Fatalf("Snapshot() = %#v", got)
	}
	if !gate.Authorized() {
		t.Fatal("verified public gate is not authorized")
	}
	if store.saveCalls() != 0 {
		t.Fatalf("startup check saved the existing token %d times", store.saveCalls())
	}
}

func TestPublicGateReturnsVerifiedUniversalSeedGrant(t *testing.T) {
	publicKey, privateKey := gateTestKey(t)
	payload := gateTestPayload()
	payload.Schema = currentSchema
	payload.SeedID = "public-seed-082"
	payload.SeedKey = base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	store := &fakeGateStore{token: gateSignToken(t, privateKey, payload)}
	gate := NewGateWithDependencies(
		publicGateInfo(publicKey),
		store,
		func() (string, error) { return payload.MachineID, nil },
		ParseAndVerify,
	)

	grant, err := gate.SeedGrant()
	if err != nil {
		t.Fatalf("SeedGrant() error = %v", err)
	}
	if grant.ID != payload.SeedID || string(grant.Key) != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("SeedGrant() = %#v", grant)
	}
	grant.Key[0] = 'X'
	again, err := gate.SeedGrant()
	if err != nil || string(again.Key) != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("second SeedGrant() = %#v, %v", again, err)
	}
}

func TestInternalGateHasNoUniversalSeedGrant(t *testing.T) {
	gate := NewGateWithDependencies(
		buildinfo.Info{Channel: buildinfo.ChannelInternal, ProductID: buildinfo.ProductID},
		nil,
		nil,
		nil,
	)
	if _, err := gate.SeedGrant(); !errors.Is(err, ErrNoSeedGrant) {
		t.Fatalf("SeedGrant() error = %v, want ErrNoSeedGrant", err)
	}
}

func TestPublicGateBuildsExactVerifyOptions(t *testing.T) {
	publicKey, _ := gateTestKey(t)
	store := &fakeGateStore{token: "opaque-license-token"}
	var gotToken string
	var gotOptions VerifyOptions

	gate := NewGateWithDependencies(
		publicGateInfo(publicKey),
		store,
		func() (string, error) { return testGateMachineID, nil },
		func(token string, options VerifyOptions) (Payload, error) {
			gotToken = token
			gotOptions = options
			return Payload{}, nil
		},
	)

	if gate.Snapshot().State != StateActivated {
		t.Fatalf("Snapshot() = %#v", gate.Snapshot())
	}
	if gotToken != store.token {
		t.Fatalf("verifier token = %q, want stored token", gotToken)
	}
	if !gotOptions.PublicKey.Equal(publicKey) {
		t.Fatalf("verifier public key = %x, want %x", gotOptions.PublicKey, publicKey)
	}
	if gotOptions.Product != buildinfo.ProductID {
		t.Fatalf("verifier product = %q, want %q", gotOptions.Product, buildinfo.ProductID)
	}
	if gotOptions.Channel != string(buildinfo.ChannelPublicMacOSARM64) {
		t.Fatalf("verifier channel = %q, want %q", gotOptions.Channel, buildinfo.ChannelPublicMacOSARM64)
	}
	if gotOptions.MachineID != testGateMachineID {
		t.Fatalf("verifier Machine ID = %q, want %q", gotOptions.MachineID, testGateMachineID)
	}
	if !gotOptions.Now.IsZero() {
		t.Fatalf("verifier time = %v, want zero for ParseAndVerify clock", gotOptions.Now)
	}
}

func TestActivateVerifiesBeforeSaving(t *testing.T) {
	store := &fakeGateStore{loadErr: ErrNotFound}
	verified := false
	gate := newPublicTestGate(t, store, func(token string, _ VerifyOptions) (Payload, error) {
		if token != testGateToken {
			t.Fatalf("verifier token = %q", token)
		}
		verified = true
		return Payload{}, nil
	})
	store.beforeSave = func(token string) {
		if !verified {
			t.Fatal("Save() ran before verification")
		}
		if token != testGateToken {
			t.Fatalf("Save() token = %q", token)
		}
	}

	got := gate.Activate(testGateToken)

	if got.State != StateActivated || !gate.Authorized() {
		t.Fatalf("Activate() = %#v, authorized=%v", got, gate.Authorized())
	}
	if store.saveCalls() != 1 {
		t.Fatalf("Save() calls = %d, want 1", store.saveCalls())
	}
}

func TestActivateDoesNotSaveRejectedLicense(t *testing.T) {
	store := &fakeGateStore{loadErr: ErrNotFound}
	gate := newPublicTestGate(t, store, func(string, VerifyOptions) (Payload, error) {
		return Payload{}, ErrWrongMachine
	})

	got := gate.Activate(testGateToken)

	if got.State != StateError || got.ErrorCode != string(CodeWrongMachine) {
		t.Fatalf("Activate() = %#v", got)
	}
	if store.saveCalls() != 0 {
		t.Fatalf("rejected activation saved %d times", store.saveCalls())
	}
	if gate.Authorized() {
		t.Fatal("rejected activation authorized the gate")
	}
}

func TestGateMapsLicenseFailuresToSafeCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "malformed", err: ErrMalformed, want: GateErrorMalformedLicense},
		{name: "unsupported schema", err: ErrUnsupportedSchema, want: string(CodeUnsupportedSchema)},
		{name: "invalid signature", err: ErrInvalidSignature, want: string(CodeInvalidSignature)},
		{name: "invalid public key", err: ErrInvalidPublicKey, want: string(CodeInvalidPublicKey)},
		{name: "wrong product", err: ErrWrongProduct, want: string(CodeWrongProduct)},
		{name: "wrong channel", err: ErrWrongChannel, want: string(CodeWrongChannel)},
		{name: "wrong machine", err: ErrWrongMachine, want: string(CodeWrongMachine)},
		{name: "expired", err: ErrExpired, want: string(CodeExpired)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeGateStore{token: testGateToken}
			gate := newPublicTestGate(t, store, func(string, VerifyOptions) (Payload, error) {
				return Payload{}, fmt.Errorf("wrapped verifier failure: %w", tt.err)
			})

			got := gate.Snapshot()
			if got.State != StateError || got.ErrorCode != tt.want {
				t.Fatalf("Snapshot() = %#v, want error code %q", got, tt.want)
			}
			assertSafeGateErrorCode(t, got.ErrorCode, testGateToken, testGateMachineID)
		})
	}
}

func TestGateRedactsUnknownDependencyErrors(t *testing.T) {
	tests := []struct {
		name  string
		store *fakeGateStore
		id    MachineIDProvider
		check Verifier
		want  string
	}{
		{
			name:  "machine ID",
			store: &fakeGateStore{token: testGateToken},
			id: func() (string, error) {
				return "", errors.New("raw-platform-id: " + testRawMachineID)
			},
			check: func(string, VerifyOptions) (Payload, error) { return Payload{}, nil },
			want:  string(CodeMachineUnavailable),
		},
		{
			name:  "load",
			store: &fakeGateStore{loadErr: errors.New("read failed for " + testGateToken)},
			id:    func() (string, error) { return testGateMachineID, nil },
			check: func(string, VerifyOptions) (Payload, error) { return Payload{}, nil },
			want:  GateErrorStorage,
		},
		{
			name:  "verify",
			store: &fakeGateStore{token: testGateToken},
			id:    func() (string, error) { return testGateMachineID, nil },
			check: func(string, VerifyOptions) (Payload, error) {
				return Payload{}, errors.New("verification failed for " + testGateToken + " on " + testGateMachineID)
			},
			want: GateErrorVerification,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publicKey, _ := gateTestKey(t)
			gate := NewGateWithDependencies(publicGateInfo(publicKey), tt.store, tt.id, tt.check)

			got := gate.Snapshot()
			if got.State != StateError || got.ErrorCode != tt.want {
				t.Fatalf("Snapshot() = %#v, want error code %q", got, tt.want)
			}
			assertSafeGateErrorCode(t, got.ErrorCode, testRawMachineID, testGateToken, testGateMachineID)
		})
	}
}

func TestActivateRedactsSaveFailure(t *testing.T) {
	store := &fakeGateStore{
		loadErr: ErrNotFound,
		saveErr: errors.New("could not persist " + testGateToken + " for " + testGateMachineID),
	}
	gate := newPublicTestGate(t, store, func(string, VerifyOptions) (Payload, error) {
		return Payload{}, nil
	})

	got := gate.Activate(testGateToken)

	if got.State != StateError || got.ErrorCode != GateErrorStorage {
		t.Fatalf("Activate() = %#v", got)
	}
	assertSafeGateErrorCode(t, got.ErrorCode, testGateToken, testGateMachineID)
}

func TestGateRejectsInvalidPublicMetadataWithoutCallingStore(t *testing.T) {
	store := &fakeGateStore{token: testGateToken}
	info := buildinfo.Info{
		Channel:          buildinfo.ChannelPublicMacOSARM64,
		ProductID:        buildinfo.ProductID,
		LicensePublicKey: "not-base64",
	}
	gate := NewGateWithDependencies(
		info,
		store,
		func() (string, error) { return testGateMachineID, nil },
		func(string, VerifyOptions) (Payload, error) {
			t.Fatal("verifier called with invalid public metadata")
			return Payload{}, nil
		},
	)

	got := gate.Snapshot()
	if got.State != StateError || got.ErrorCode != string(CodeInvalidPublicKey) {
		t.Fatalf("Snapshot() = %#v", got)
	}
	if store.loadCalls() != 0 {
		t.Fatalf("Load() calls = %d, want 0", store.loadCalls())
	}
}

func TestGateFailsClosedForUnknownBuildChannel(t *testing.T) {
	gate := NewGateWithDependencies(
		buildinfo.Info{Channel: buildinfo.Channel("preview"), ProductID: buildinfo.ProductID},
		nil,
		func() (string, error) { panic("unknown channel requested a Machine ID") },
		func(string, VerifyOptions) (Payload, error) { panic("unknown channel verified a license") },
	)

	got := gate.Snapshot()
	if got.State != StateError || got.ErrorCode != GateErrorConfiguration || gate.Authorized() {
		t.Fatalf("Snapshot() = %#v, authorized=%v", got, gate.Authorized())
	}
}

func TestSnapshotIsAnIndependentValue(t *testing.T) {
	store := &fakeGateStore{loadErr: ErrNotFound}
	gate := newPublicTestGate(t, store, func(string, VerifyOptions) (Payload, error) {
		return Payload{}, nil
	})

	copy := gate.Snapshot()
	copy.State = StateActivated
	copy.MachineID = "mutated"
	copy.ErrorCode = "mutated"

	if got := gate.Snapshot(); got.State != StateNeedsActivation || got.MachineID != testGateMachineID || got.ErrorCode != "" {
		t.Fatalf("mutating returned snapshot changed gate state: %#v", got)
	}
}

func TestCheckPublishesCheckingWhileStoreIsBusy(t *testing.T) {
	store := &fakeGateStore{loadErr: ErrNotFound}
	gate := newPublicTestGate(t, store, func(string, VerifyOptions) (Payload, error) {
		return Payload{}, nil
	})
	started := make(chan struct{})
	release := make(chan struct{})
	store.loadHook = func(call int) {
		if call != 2 {
			return
		}
		close(started)
		<-release
	}
	done := make(chan Snapshot, 1)
	go func() { done <- gate.Check() }()
	<-started

	if got := gate.Snapshot(); got.State != StateChecking || got.ErrorCode != "" {
		t.Fatalf("Snapshot() while checking = %#v", got)
	}
	close(release)
	if got := <-done; got.State != StateNeedsActivation {
		t.Fatalf("Check() = %#v", got)
	}
}

func TestGateSerializesConcurrentActivation(t *testing.T) {
	store := &fakeGateStore{loadErr: ErrNotFound}
	var active atomic.Int32
	var maximum atomic.Int32
	gate := newPublicTestGate(t, store, func(string, VerifyOptions) (Payload, error) {
		current := active.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		active.Add(-1)
		return Payload{}, nil
	})

	const workers = 24
	var ready sync.WaitGroup
	ready.Add(workers)
	start := make(chan struct{})
	var complete sync.WaitGroup
	complete.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer complete.Done()
			ready.Done()
			<-start
			gate.Activate(testGateToken)
		}()
	}
	ready.Wait()
	close(start)
	complete.Wait()

	if got := maximum.Load(); got != 1 {
		t.Fatalf("maximum concurrent verifications = %d, want 1", got)
	}
	if got := store.saveCalls(); got != workers {
		t.Fatalf("Save() calls = %d, want %d", got, workers)
	}
	if got := gate.Snapshot(); got.State != StateActivated || !gate.Authorized() {
		t.Fatalf("final snapshot = %#v, authorized=%v", got, gate.Authorized())
	}
}

const (
	testGateMachineID = "A1B2C3D4"
	testGateToken     = "secret-license-token"
	testRawMachineID  = "raw-platform-uuid"
)

type fakeGateStore struct {
	mu         sync.Mutex
	token      string
	loadErr    error
	saveErr    error
	loads      int
	saves      int
	loadHook   func(int)
	beforeSave func(string)
}

type fakeRevocationChecker struct {
	decision revocation.Decision
	err      error
	calls    atomic.Int32
}

func (checker *fakeRevocationChecker) Check(context.Context, string) (revocation.Decision, error) {
	checker.calls.Add(1)
	return checker.decision, checker.err
}

type checkerFuncForGate func(context.Context, string) (revocation.Decision, error)

func (checker checkerFuncForGate) Check(ctx context.Context, licenseID string) (revocation.Decision, error) {
	return checker(ctx, licenseID)
}

func (s *fakeGateStore) Load() (string, error) {
	s.mu.Lock()
	s.loads++
	call := s.loads
	hook := s.loadHook
	token := s.token
	err := s.loadErr
	s.mu.Unlock()
	if hook != nil {
		hook(call)
	}
	return token, err
}

func (s *fakeGateStore) Save(token string) error {
	s.mu.Lock()
	hook := s.beforeSave
	err := s.saveErr
	s.mu.Unlock()
	if hook != nil {
		hook(token)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	if err == nil {
		s.token = token
		s.loadErr = nil
	}
	return err
}

func (s *fakeGateStore) loadCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loads
}

func (s *fakeGateStore) saveCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves
}

func newPublicTestGate(t *testing.T, store TokenStore, verifier Verifier) *Gate {
	t.Helper()
	publicKey, _ := gateTestKey(t)
	return NewGateWithDependencies(
		publicGateInfo(publicKey),
		store,
		func() (string, error) { return testGateMachineID, nil },
		verifier,
	)
}

func publicGateInfo(publicKey ed25519.PublicKey) buildinfo.Info {
	return buildinfo.Info{
		Channel:          buildinfo.ChannelPublicMacOSARM64,
		ProductID:        buildinfo.ProductID,
		LicensePublicKey: base64.StdEncoding.EncodeToString(publicKey),
	}
}

func gateTestKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	return publicKey, privateKey
}

func gateTestPayload() Payload {
	return Payload{
		Schema:    1,
		LicenseID: "gate-license-123",
		Product:   buildinfo.ProductID,
		Channel:   string(buildinfo.ChannelPublicMacOSARM64),
		MachineID: testGateMachineID,
		Owner:     "Gate Test",
		IssuedAt:  "2026-07-01T00:00:00Z",
	}
}

func gateSignToken(t *testing.T, privateKey ed25519.PrivateKey, payload Payload) string {
	t.Helper()
	canonical, err := CanonicalPayload(payload)
	if err != nil {
		t.Fatalf("CanonicalPayload() error = %v", err)
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(canonical)
	signature := ed25519.Sign(privateKey, []byte(TokenPrefix+"."+payloadPart))
	return TokenPrefix + "." + payloadPart + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func assertSafeGateErrorCode(t *testing.T, code string, secrets ...string) {
	t.Helper()
	allowed := map[string]bool{
		GateErrorConfiguration:         true,
		GateErrorMalformedLicense:      true,
		GateErrorStorage:               true,
		GateErrorVerification:          true,
		GateErrorRevoked:               true,
		GateErrorCheckRequired:         true,
		string(CodeUnsupportedSchema):  true,
		string(CodeInvalidSignature):   true,
		string(CodeInvalidPublicKey):   true,
		string(CodeWrongProduct):       true,
		string(CodeWrongChannel):       true,
		string(CodeWrongMachine):       true,
		string(CodeExpired):            true,
		string(CodeMachineUnavailable): true,
	}
	if !allowed[code] {
		t.Fatalf("unsafe or unknown gate error code %q", code)
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(code, secret) {
			t.Fatalf("gate error code %q contains secret %q", code, secret)
		}
	}
}
