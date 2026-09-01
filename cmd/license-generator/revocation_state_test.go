package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"telegram-companion/internal/licenseissuer"
	"telegram-companion/internal/revocation"
)

func TestRevocationStateCodecStrictRoundTrip(t *testing.T) {
	state := testGeneratorRevocationState(t)
	encoded, err := encodeRevocationState(state, xorRevocationStateSecret)
	if err != nil {
		t.Fatalf("encodeRevocationState() error = %v", err)
	}
	for _, secret := range []string{
		string(state.PrivateKey),
		base64.StdEncoding.EncodeToString(state.PrivateKey),
		base64.RawStdEncoding.EncodeToString(state.PrivateKey),
	} {
		if secret != "" && bytes.Contains(encoded, []byte(secret)) {
			t.Fatal("encoded revocation state exposes the private key")
		}
	}
	for _, field := range []string{`"schema"`, `"public_key"`, `"private_key_dpapi"`, `"backup_confirmed"`, `"records"`} {
		if !bytes.Contains(encoded, []byte(field)) {
			t.Fatalf("encoded revocation state omits %s: %s", field, encoded)
		}
	}

	decoded, err := decodeRevocationState(encoded, xorRevocationStateSecret)
	if err != nil {
		t.Fatalf("decodeRevocationState() error = %v", err)
	}
	if !bytes.Equal(decoded.PrivateKey, state.PrivateKey) || !bytes.Equal(decoded.PublicKey, state.PublicKey) || !decoded.BackupConfirmed {
		t.Fatalf("decoded key state = %#v", decoded)
	}
	if len(decoded.Records) != 1 || decoded.Records[0] != state.Records[0] {
		t.Fatalf("decoded records = %#v, want %#v", decoded.Records, state.Records)
	}

	decoded.PrivateKey[0] ^= 0xff
	decoded.PublicKey[0] ^= 0xff
	decoded.Records[0].EventID = "mutated"
	if bytes.Equal(decoded.PrivateKey, state.PrivateKey) || bytes.Equal(decoded.PublicKey, state.PublicKey) || state.Records[0].EventID != "event-001" {
		t.Fatal("decoded state aliases caller-owned key or record memory")
	}
}

func TestRevocationStateCodecPreservesCanonicalEmptyRecords(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfirmed", true: "confirmed"}[confirmed], func(t *testing.T) {
			state := testGeneratorRevocationState(t)
			state.BackupConfirmed = confirmed
			state.Records = []revocationPublicationRecord{}
			encoded, err := encodeRevocationState(state, xorRevocationStateSecret)
			if err != nil {
				t.Fatalf("encodeRevocationState(empty records) error = %v", err)
			}
			if !bytes.Contains(encoded, []byte(`"records":[]`)) || bytes.Contains(encoded, []byte(`"records":null`)) {
				t.Fatalf("encoded empty records are not canonical: %s", encoded)
			}
			decoded, err := decodeRevocationState(encoded, xorRevocationStateSecret)
			if err != nil {
				t.Fatalf("decodeRevocationState(empty records) error = %v", err)
			}
			defer wipeBytes(decoded.PrivateKey)
			if decoded.Records == nil || len(decoded.Records) != 0 || decoded.BackupConfirmed != confirmed {
				t.Fatalf("decoded empty records = %#v, confirmed=%v, want=%v", decoded.Records, decoded.BackupConfirmed, confirmed)
			}
		})
	}
}

func TestRevocationStateCodecMigratesOnlyUnconfirmedLegacyNullRecords(t *testing.T) {
	state := testGeneratorRevocationState(t)
	state.BackupConfirmed = false
	state.Records = []revocationPublicationRecord{}
	encoded, err := encodeRevocationState(state, xorRevocationStateSecret)
	if err != nil {
		t.Fatalf("encodeRevocationState() error = %v", err)
	}
	legacy := bytes.Replace(encoded, []byte(`"records":[]`), []byte(`"records":null`), 1)
	if bytes.Equal(legacy, encoded) {
		t.Fatal("legacy null-record fixture was not formed")
	}
	decoded, err := decodeRevocationState(legacy, xorRevocationStateSecret)
	if err != nil {
		t.Fatalf("decodeRevocationState(unconfirmed legacy null) error = %v", err)
	}
	defer wipeBytes(decoded.PrivateKey)
	if decoded.Records == nil || len(decoded.Records) != 0 || decoded.BackupConfirmed {
		t.Fatalf("migrated legacy state = %#v", decoded)
	}

	var document map[string]any
	if err := json.Unmarshal(legacy, &document); err != nil {
		t.Fatal(err)
	}
	document["backup_confirmed"] = true
	confirmedLegacy, _ := json.Marshal(document)
	if _, err := decodeRevocationState(confirmedLegacy, xorRevocationStateSecret); !errors.Is(err, licenseissuer.ErrInvalidState) {
		t.Fatalf("decode confirmed null records error = %v, want ErrInvalidState", err)
	}
	delete(document, "records")
	missingRecords, _ := json.Marshal(document)
	if _, err := decodeRevocationState(missingRecords, xorRevocationStateSecret); !errors.Is(err, licenseissuer.ErrInvalidState) {
		t.Fatalf("decode missing records error = %v, want ErrInvalidState", err)
	}
}

func TestRevocationStateCodecRejectsMalformedState(t *testing.T) {
	valid := testGeneratorRevocationState(t)
	validEncoded, err := encodeRevocationState(valid, xorRevocationStateSecret)
	if err != nil {
		t.Fatalf("encodeRevocationState() error = %v", err)
	}

	other := testGeneratorRevocationStateWithByte(t, 0x42)
	tests := []struct {
		name  string
		state generatorRevocationState
	}{
		{name: "missing private key", state: generatorRevocationState{PublicKey: valid.PublicKey, Records: []revocationPublicationRecord{}}},
		{name: "mismatched key pair", state: generatorRevocationState{PrivateKey: valid.PrivateKey, PublicKey: other.PublicKey, Records: []revocationPublicationRecord{}}},
		{name: "nil records", state: generatorRevocationState{PrivateKey: valid.PrivateKey, PublicKey: valid.PublicKey}},
		{name: "invalid event", state: generatorRevocationState{PrivateKey: valid.PrivateKey, PublicKey: valid.PublicKey, Records: []revocationPublicationRecord{{EventID: " ", Handle: valid.Records[0].Handle, RequestedAt: valid.Records[0].RequestedAt, State: revocationPublicationPending}}}},
		{name: "duplicate handle", state: generatorRevocationState{PrivateKey: valid.PrivateKey, PublicKey: valid.PublicKey, Records: []revocationPublicationRecord{valid.Records[0], {EventID: "event-002", Handle: valid.Records[0].Handle, RequestedAt: valid.Records[0].RequestedAt, State: revocationPublicationPending}}}},
		{name: "pending blob without sequence", state: generatorRevocationState{PrivateKey: valid.PrivateKey, PublicKey: valid.PublicKey, Records: []revocationPublicationRecord{{EventID: "event-002", Handle: valid.Records[0].Handle, RequestedAt: valid.Records[0].RequestedAt, State: revocationPublicationPending, RemoteBlobID: "blob-without-sequence"}}}},
		{name: "pending sequence without blob", state: generatorRevocationState{PrivateKey: valid.PrivateKey, PublicKey: valid.PublicKey, Records: []revocationPublicationRecord{{EventID: "event-002", Handle: valid.Records[0].Handle, RequestedAt: valid.Records[0].RequestedAt, State: revocationPublicationPending, PublishedSequence: 1}}}},
		{name: "failed with remote evidence", state: generatorRevocationState{PrivateKey: valid.PrivateKey, PublicKey: valid.PublicKey, Records: []revocationPublicationRecord{{EventID: "event-002", Handle: valid.Records[0].Handle, RequestedAt: valid.Records[0].RequestedAt, State: revocationPublicationFailed, PublishedSequence: 1, RemoteBlobID: "blob-failed"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := encodeRevocationState(test.state, xorRevocationStateSecret); !errors.Is(err, licenseissuer.ErrInvalidState) {
				t.Fatalf("encodeRevocationState() error = %v, want ErrInvalidState", err)
			}
		})
	}

	var document map[string]any
	if err := json.Unmarshal(validEncoded, &document); err != nil {
		t.Fatalf("Unmarshal(valid state) error = %v", err)
	}
	document["unknown"] = true
	unknown, _ := json.Marshal(document)
	if _, err := decodeRevocationState(unknown, xorRevocationStateSecret); !errors.Is(err, licenseissuer.ErrInvalidState) {
		t.Fatalf("decode unknown field error = %v, want ErrInvalidState", err)
	}
	delete(document, "unknown")
	records := document["records"].([]any)
	record := records[0].(map[string]any)
	record["unknown_nested"] = true
	nestedUnknown, _ := json.Marshal(document)
	unprotectCalled := false
	if _, err := decodeRevocationState(nestedUnknown, func([]byte) ([]byte, error) {
		unprotectCalled = true
		return nil, errors.New("must not unprotect malformed records")
	}); !errors.Is(err, licenseissuer.ErrInvalidState) {
		t.Fatalf("decode nested unknown field error = %v, want ErrInvalidState", err)
	}
	if unprotectCalled {
		t.Fatal("nested unknown field reached private-key unprotect")
	}
	if _, err := decodeRevocationState(append(validEncoded, []byte(` {}`)...), xorRevocationStateSecret); !errors.Is(err, licenseissuer.ErrInvalidState) {
		t.Fatalf("decode trailing JSON error = %v, want ErrInvalidState", err)
	}

	if err := json.Unmarshal(validEncoded, &document); err != nil {
		t.Fatalf("Unmarshal(valid state) error = %v", err)
	}
	document["public_key"] = document["public_key"].(string) + "="
	nonCanonical, _ := json.Marshal(document)
	if _, err := decodeRevocationState(nonCanonical, xorRevocationStateSecret); !errors.Is(err, licenseissuer.ErrInvalidState) {
		t.Fatalf("decode noncanonical base64 error = %v, want ErrInvalidState", err)
	}

	oversized := bytes.Repeat([]byte{' '}, maxRevocationStateSize+1)
	if _, err := decodeRevocationState(oversized, xorRevocationStateSecret); !errors.Is(err, licenseissuer.ErrInvalidState) {
		t.Fatalf("decode oversized state error = %v, want ErrInvalidState", err)
	}
}

func TestRevocationStateCodecKeepsAmbiguousPendingRemoteEvidence(t *testing.T) {
	state := testGeneratorRevocationState(t)
	state.Records[0].PublishedSequence = 7
	state.Records[0].RemoteBlobID = "blob-ambiguous-7"
	encoded, err := encodeRevocationState(state, xorRevocationStateSecret)
	if err != nil {
		t.Fatalf("encodeRevocationState() error = %v", err)
	}
	decoded, err := decodeRevocationState(encoded, xorRevocationStateSecret)
	if err != nil {
		t.Fatalf("decodeRevocationState() error = %v", err)
	}
	defer wipeBytes(decoded.PrivateKey)
	if len(decoded.Records) != 1 || decoded.Records[0].PublishedSequence != 7 || decoded.Records[0].RemoteBlobID != "blob-ambiguous-7" || decoded.Records[0].State != revocationPublicationPending {
		t.Fatalf("decoded ambiguous outcome = %#v", decoded.Records)
	}
}

func TestRevocationStateCodecWipesRejectedPrivateBytes(t *testing.T) {
	state := testGeneratorRevocationState(t)
	encoded, err := encodeRevocationState(state, xorRevocationStateSecret)
	if err != nil {
		t.Fatalf("encodeRevocationState() error = %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	other := testGeneratorRevocationStateWithByte(t, 0x55)
	document["public_key"] = base64.RawStdEncoding.EncodeToString(other.PublicKey)
	encoded, _ = json.Marshal(document)

	unprotected := append([]byte(nil), state.PrivateKey...)
	_, err = decodeRevocationState(encoded, func([]byte) ([]byte, error) {
		return unprotected, nil
	})
	if !errors.Is(err, licenseissuer.ErrInvalidState) {
		t.Fatalf("decodeRevocationState() error = %v, want ErrInvalidState", err)
	}
	if !bytes.Equal(unprotected, make([]byte, len(unprotected))) {
		t.Fatal("decodeRevocationState() retained rejected private bytes")
	}
}

func TestDerivePrivateKeyFromCopiedSeedWipesSeedCopy(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x67}, ed25519.SeedSize))
	defer wipeBytes(privateKey)
	var copiedSeed []byte
	derived := derivePrivateKeyFromCopiedSeed(privateKey, func(seed []byte) ed25519.PrivateKey {
		copiedSeed = seed
		return ed25519.NewKeyFromSeed(seed)
	})
	defer wipeBytes(derived)
	if !bytes.Equal(derived, privateKey) {
		t.Fatal("derivePrivateKeyFromCopiedSeed() changed the private key")
	}
	if len(copiedSeed) != ed25519.SeedSize || !bytes.Equal(copiedSeed, make([]byte, len(copiedSeed))) {
		t.Fatal("derivePrivateKeyFromCopiedSeed() retained the copied seed")
	}
}

func TestCopyAndWipeRevocationSecretReturnsIndependentCopy(t *testing.T) {
	secret := bytes.Repeat([]byte{0x58}, ed25519.PrivateKeySize)
	want := append([]byte(nil), secret...)
	copy := copyAndWipeRevocationSecret(secret)
	defer wipeBytes(copy)
	if !bytes.Equal(copy, want) {
		t.Fatal("copyAndWipeRevocationSecret() changed the returned copy")
	}
	if !bytes.Equal(secret, make([]byte, len(secret))) {
		t.Fatal("copyAndWipeRevocationSecret() retained the source secret")
	}
}

func testGeneratorRevocationState(t *testing.T) generatorRevocationState {
	t.Helper()
	return testGeneratorRevocationStateWithByte(t, 0x31)
}

func testGeneratorRevocationStateWithByte(t *testing.T, value byte) generatorRevocationState {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{value}, ed25519.SeedSize))
	publicKey := append(ed25519.PublicKey(nil), privateKey.Public().(ed25519.PublicKey)...)
	handle, err := revocation.DeriveHandle("license-state-test-" + strings.Repeat(string([]byte{value}), 2))
	if err != nil {
		t.Fatalf("DeriveHandle() error = %v", err)
	}
	return generatorRevocationState{
		PrivateKey:      privateKey,
		PublicKey:       publicKey,
		BackupConfirmed: true,
		Records: []revocationPublicationRecord{{
			EventID:     "event-001",
			Handle:      handle.String(),
			RequestedAt: "2026-08-25T12:00:00Z",
			State:       revocationPublicationPending,
		}},
	}
}

func xorRevocationStateSecret(value []byte) ([]byte, error) {
	result := append([]byte(nil), value...)
	for index := range result {
		result[index] ^= 0xa5
	}
	return result, nil
}
