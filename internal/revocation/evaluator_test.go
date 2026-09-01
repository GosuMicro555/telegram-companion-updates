package revocation

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

func TestEvaluateDecisionMatrix(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	activeHandle, _ := DeriveHandle("license-active")
	revokedHandle, _ := DeriveHandle("license-revoked")
	replacementHandle, _ := DeriveHandle("license-replacement")
	activeManifest := verifiedManifestForTest(t, publicKey, privateKey, testPayload(3, []Entry{}))
	revokedPayload := testPayload(4, sortedEntriesForTest(t, revokedHandle, now))
	revokedPayload.GeneratedAt = now.Format(manifestTimeLayout)
	revokedManifest := verifiedManifestForTest(t, publicKey, privateKey, revokedPayload)
	outage := FetchResult{Err: errors.New("offline")}

	tests := []struct {
		name            string
		handle          Handle
		state           SecureState
		fetch           FetchResult
		at              time.Time
		wantDecision    Decision
		wantSequence    uint64
		wantReceipts    int
		wantLastSuccess time.Time
	}{
		{name: "no prior online success", handle: activeHandle, fetch: outage, at: now, wantDecision: CheckRequired},
		{name: "verified nonmatch is active", handle: activeHandle, fetch: FetchResult{Manifest: &activeManifest}, at: now, wantDecision: Active, wantSequence: 3, wantLastSuccess: now},
		{name: "verified match is revoked", handle: revokedHandle, fetch: FetchResult{Manifest: &revokedManifest}, at: now, wantDecision: Revoked, wantSequence: 4, wantReceipts: 1, wantLastSuccess: now},
		{name: "outage inside grace", handle: activeHandle, state: stateWithSuccess(now.Add(-23*time.Hour-59*time.Minute), 3), fetch: outage, at: now, wantDecision: ActiveInGrace, wantSequence: 3, wantLastSuccess: now.Add(-23*time.Hour - 59*time.Minute)},
		{name: "outage at grace boundary", handle: activeHandle, state: stateWithSuccess(now.Add(-24*time.Hour), 3), fetch: outage, at: now, wantDecision: CheckRequired, wantSequence: 3, wantLastSuccess: now.Add(-24 * time.Hour)},
		{name: "replacement is not old receipt", handle: replacementHandle, state: stateWithReceipt(now.Add(-time.Hour), revokedHandle), fetch: FetchResult{Manifest: &activeManifest}, at: now, wantDecision: Active, wantSequence: 3, wantReceipts: 1, wantLastSuccess: now},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, next, err := Evaluate(test.handle, test.state, test.fetch, test.at)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if decision != test.wantDecision {
				t.Fatalf("decision = %q, want %q", decision, test.wantDecision)
			}
			if next.HighestSequence != test.wantSequence || len(next.RevokedHandles) != test.wantReceipts {
				t.Fatalf("next state = %#v", next)
			}
			if !next.LastSuccessUTC.Equal(test.wantLastSuccess) {
				t.Fatalf("last success = %v, want %v", next.LastSuccessUTC, test.wantLastSuccess)
			}
		})
	}
}

func TestEvaluateReceiptRollbackAndClockRollback(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	handle, _ := DeriveHandle("license-receipt")
	other, _ := DeriveHandle("license-other")
	activeSequence5 := verifiedManifestForTest(t, publicKey, privateKey, testPayload(5, []Entry{}))
	lowerSequence4 := verifiedManifestForTest(t, publicKey, privateKey, testPayload(4, []Entry{}))

	receiptState := stateWithReceipt(now.Add(-48*time.Hour), handle)
	decision, next, err := Evaluate(handle, receiptState, FetchResult{Err: errors.New("offline")}, now)
	if err != nil || decision != Revoked || len(next.RevokedHandles) != 1 {
		t.Fatalf("receipt decision = %q, state=%#v, err=%v", decision, next, err)
	}

	state := stateWithSuccess(now.Add(-time.Hour), 5)
	decision, next, err = Evaluate(other, state, FetchResult{Manifest: &lowerSequence4}, now)
	if err != nil || decision != ActiveInGrace || next.HighestSequence != 5 {
		t.Fatalf("lower-sequence decision = %q, state=%#v, err=%v", decision, next, err)
	}

	state = stateWithSuccess(now.Add(-time.Hour), 5)
	state.LastWallUTC = now
	state.ManifestDigest = activeSequence5.Digest()
	decision, _, err = Evaluate(other, state, FetchResult{Err: errors.New("offline")}, now.Add(-5*time.Minute-time.Second))
	if err != nil || decision != CheckRequired {
		t.Fatalf("large rollback decision = %q, err=%v", decision, err)
	}
	decision, next, err = Evaluate(other, state, FetchResult{Manifest: &activeSequence5}, now.Add(-5*time.Minute-time.Second))
	if err != nil || decision != Active || !next.LastSuccessUTC.Equal(now.Add(-5*time.Minute-time.Second)) {
		t.Fatalf("online rollback recovery = %q, state=%#v, err=%v", decision, next, err)
	}

	state = stateWithSuccess(now.Add(-time.Hour), 5)
	state.LastWallUTC = now
	decision, next, err = Evaluate(other, state, FetchResult{Err: errors.New("offline")}, now.Add(-5*time.Minute))
	if err != nil || decision != ActiveInGrace || !next.LastSuccessUTC.Equal(now.Add(-time.Hour)) {
		t.Fatalf("bounded rollback decision = %q, state=%#v, err=%v", decision, next, err)
	}
}

func TestEvaluateRejectsZeroAndEquivocatingVerifiedManifests(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	handle, _ := DeriveHandle("license-untrusted")
	state := stateWithSuccess(now.Add(-time.Hour), 5)
	valid := verifiedManifestForTest(t, publicKey, privateKey, testPayload(5, []Entry{}))
	state.ManifestDigest = valid.Digest()

	decision, _, err := Evaluate(handle, state, FetchResult{Manifest: &VerifiedManifest{}}, now)
	if err != nil || decision != ActiveInGrace {
		t.Fatalf("zero manifest decision = %q, err=%v", decision, err)
	}

	other := verifiedManifestForTest(t, publicKey, privateKey, testPayload(5, []Entry{}))
	otherDigest := other.Digest()
	otherDigest[0] ^= 0xff
	state.ManifestDigest = otherDigest
	decision, next, err := Evaluate(handle, state, FetchResult{Manifest: &other}, now)
	if err != nil || decision != ActiveInGrace || next.ManifestDigest != otherDigest {
		t.Fatalf("equivocation decision = %q, state=%#v, err=%v", decision, next, err)
	}

	decision, _, err = Evaluate(handle, state, FetchResult{Manifest: &valid, Err: errors.New("transport failed after response")}, now)
	if err != nil || decision != ActiveInGrace {
		t.Fatalf("ambiguous fetch result decision = %q, err=%v", decision, err)
	}
}

func TestEvaluateReceiptCapFailsClosedBeforeTerminalDecision(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	handle, _ := DeriveHandle("license-cap-target")
	payload := testPayload(2, sortedEntriesForTest(t, handle, now))
	payload.GeneratedAt = now.Format(manifestTimeLayout)
	manifest := verifiedManifestForTest(t, publicKey, privateKey, payload)
	state := stateWithSuccess(now.Add(-time.Hour), 1)
	state.RevokedHandles = receiptSetForTest(MaxRevokedReceipts)

	decision, next, err := Evaluate(handle, state, FetchResult{Manifest: &manifest}, now)
	if !errors.Is(err, errInvalidEvaluation) || decision != CheckRequired {
		t.Fatalf("decision = %q, err=%v", decision, err)
	}
	if len(next.RevokedHandles) != MaxRevokedReceipts || containsReceipt(next.RevokedHandles, handle) {
		t.Fatalf("receipt cap changed: count=%d contains-target=%v", len(next.RevokedHandles), containsReceipt(next.RevokedHandles, handle))
	}
	if _, marshalErr := MarshalSecureState(next); marshalErr != nil {
		t.Fatalf("returned state cannot be persisted: %v", marshalErr)
	}
}

func verifiedManifestForTest(t *testing.T, publicKey, privateKey []byte, payload Payload) VerifiedManifest {
	t.Helper()
	envelope, err := Sign(payload, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Verify(envelope, payload.KeyID, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func sortedEntriesForTest(t *testing.T, handle Handle, revokedAt time.Time) []Entry {
	t.Helper()
	return []Entry{{Kind: EntryKindLicenseIDSHA256, Value: handle.String(), RevokedAt: revokedAt.Format(manifestTimeLayout)}}
}

func stateWithSuccess(at time.Time, sequence uint64) SecureState {
	return SecureState{Schema: SecureStateSchema, HighestSequence: sequence, LastSuccessUTC: at, LastWallUTC: at, ManifestDigest: [32]byte{1}, RevokedHandles: []Handle{}}
}

func stateWithReceipt(at time.Time, handle Handle) SecureState {
	state := stateWithSuccess(at, 1)
	state.RevokedHandles = []Handle{handle}
	return state
}

func receiptSetForTest(count int) []Handle {
	receipts := make([]Handle, count)
	for index := range receipts {
		binary.BigEndian.PutUint64(receipts[index][24:], uint64(index+1))
	}
	return receipts
}
