package revocation

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCheckerPersistsAuthenticatedDecisionsBeforeReturning(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	revokedHandle, _ := DeriveHandle("license-revoked-by-checker")
	payload := testPayload(7, sortedEntriesForTest(t, revokedHandle, now))
	payload.GeneratedAt = now.Format(manifestTimeLayout)
	envelope, err := Sign(payload, privateKey)
	if err != nil {
		t.Fatal(err)
	}

	store := &checkerStateStore{}
	checker, err := NewChecker(store, staticFetcher{body: []byte(envelope)}, testKeyID, publicKey, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	decision, err := checker.Check(context.Background(), "license-revoked-by-checker")
	if err != nil || decision != Revoked {
		t.Fatalf("Check() = %q, %v", decision, err)
	}
	saved := store.savedState()
	if store.saves != 1 || saved.HighestSequence != 7 || !containsReceipt(saved.RevokedHandles, revokedHandle) {
		t.Fatalf("persisted state before return = %#v (saves=%d)", saved, store.saves)
	}
}

func TestCheckerFailsClosedAcrossDependencyFailures(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	activeEnvelope, _ := Sign(testPayload(3, []Entry{}), privateKey)
	prior := stateWithSuccess(now.Add(-time.Hour), 2)

	tests := []struct {
		name      string
		store     *checkerStateStore
		fetcher   Fetcher
		wantSaves int
		wantError bool
	}{
		{name: "load", store: &checkerStateStore{loadErr: errors.New("keychain unavailable")}, fetcher: staticFetcher{body: []byte(activeEnvelope)}, wantError: true},
		{name: "fetch without cache", store: &checkerStateStore{}, fetcher: staticFetcher{err: errors.New("offline")}, wantSaves: 1},
		{name: "invalid signature without cache", store: &checkerStateStore{}, fetcher: staticFetcher{body: []byte("not-a-manifest")}, wantSaves: 1},
		{name: "save after active", store: &checkerStateStore{saveErr: errors.New("disk full")}, fetcher: staticFetcher{body: []byte(activeEnvelope)}, wantSaves: 1, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checker, err := NewChecker(test.store, test.fetcher, testKeyID, publicKey, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			decision, checkErr := checker.Check(context.Background(), "license-active-checker")
			if decision != CheckRequired || (checkErr != nil) != test.wantError {
				t.Fatalf("Check() = %q, %v", decision, checkErr)
			}
			if test.store.saves != test.wantSaves {
				t.Fatalf("Save() calls = %d, want %d", test.store.saves, test.wantSaves)
			}
		})
	}

	graceStore := &checkerStateStore{state: prior}
	checker, _ := NewChecker(graceStore, staticFetcher{err: errors.New("offline")}, testKeyID, publicKey, func() time.Time { return now })
	decision, err := checker.Check(context.Background(), "license-active-checker")
	if err != nil || decision != ActiveInGrace || graceStore.saves != 1 || !graceStore.savedState().LastWallUTC.Equal(now) {
		t.Fatalf("cached outage = %q, %v, state=%#v", decision, err, graceStore.savedState())
	}
}

func TestCheckerRejectsInvalidConstructionAndCancellation(t *testing.T) {
	publicKey, _ := testManifestKey()
	now := func() time.Time { return time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC) }
	validStore := &checkerStateStore{}
	validFetcher := staticFetcher{err: errors.New("offline")}
	for name, build := range map[string]func() (*Checker, error){
		"nil store":   func() (*Checker, error) { return NewChecker(nil, validFetcher, testKeyID, publicKey, now) },
		"nil fetcher": func() (*Checker, error) { return NewChecker(validStore, nil, testKeyID, publicKey, now) },
		"bad key id":  func() (*Checker, error) { return NewChecker(validStore, validFetcher, "", publicKey, now) },
		"bad key": func() (*Checker, error) {
			return NewChecker(validStore, validFetcher, testKeyID, ed25519.PublicKey{1}, now)
		},
		"nil clock": func() (*Checker, error) { return NewChecker(validStore, validFetcher, testKeyID, publicKey, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			if checker, err := build(); err == nil || checker != nil {
				t.Fatalf("NewChecker() = %#v, %v", checker, err)
			}
		})
	}

	counting := &countingFetcher{}
	checker, err := NewChecker(validStore, counting, testKeyID, publicKey, now)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	decision, checkErr := checker.Check(ctx, "license-cancelled")
	if decision != CheckRequired || !errors.Is(checkErr, context.Canceled) || counting.calls != 0 {
		t.Fatalf("cancelled Check() = %q, %v, fetch calls=%d", decision, checkErr, counting.calls)
	}
}

func TestCheckerSerializesStateTransitions(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	envelope, _ := Sign(testPayload(1, []Entry{}), privateKey)
	store := &checkerStateStore{operationDelay: time.Millisecond}
	checker, err := NewChecker(store, staticFetcher{body: []byte(envelope)}, testKeyID, publicKey, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	const workers = 12
	var wait sync.WaitGroup
	wait.Add(workers)
	for index := 0; index < workers; index++ {
		go func() {
			defer wait.Done()
			decision, checkErr := checker.Check(context.Background(), "license-concurrent")
			if checkErr != nil || decision != Active {
				t.Errorf("Check() = %q, %v", decision, checkErr)
			}
		}()
	}
	wait.Wait()
	if store.maximumConcurrent != 1 {
		t.Fatalf("maximum concurrent store operations = %d, want 1", store.maximumConcurrent)
	}
}

type staticFetcher struct {
	body []byte
	err  error
}

func (fetcher staticFetcher) Fetch(context.Context) ([]byte, error) {
	return append([]byte(nil), fetcher.body...), fetcher.err
}

type countingFetcher struct{ calls int }

func (fetcher *countingFetcher) Fetch(context.Context) ([]byte, error) {
	fetcher.calls++
	return nil, errors.New("offline")
}

type checkerStateStore struct {
	mu                sync.Mutex
	state             SecureState
	loadErr           error
	saveErr           error
	saves             int
	active            int
	maximumConcurrent int
	operationDelay    time.Duration
}

func (store *checkerStateStore) Load(context.Context) (SecureState, error) {
	store.enter()
	defer store.leave()
	time.Sleep(store.operationDelay)
	return store.state, store.loadErr
}

func (store *checkerStateStore) Save(_ context.Context, state SecureState) error {
	store.enter()
	defer store.leave()
	time.Sleep(store.operationDelay)
	store.saves++
	if store.saveErr == nil {
		store.state = state
	}
	return store.saveErr
}

func (store *checkerStateStore) enter() {
	store.mu.Lock()
	store.active++
	if store.active > store.maximumConcurrent {
		store.maximumConcurrent = store.active
	}
	store.mu.Unlock()
}

func (store *checkerStateStore) leave() {
	store.mu.Lock()
	store.active--
	store.mu.Unlock()
}

func (store *checkerStateStore) savedState() SecureState {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.state
}
