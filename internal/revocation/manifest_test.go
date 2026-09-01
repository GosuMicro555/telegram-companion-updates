package revocation

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

const testKeyID = "revocation-2026-01"

func testManifestKey() (ed25519.PublicKey, ed25519.PrivateKey) {
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	return privateKey.Public().(ed25519.PublicKey), privateKey
}

func testPayload(sequence uint64, entries []Entry) Payload {
	return Payload{
		Schema:      ManifestSchema,
		KeyID:       testKeyID,
		Sequence:    sequence,
		GeneratedAt: "2026-08-25T00:00:00Z",
		Entries:     entries,
	}
}

func TestManifestSignVerifyAndFixedVectors(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	sequence0, err := Sign(testPayload(0, []Entry{}), privateKey)
	if err != nil {
		t.Fatalf("Sign sequence 0: %v", err)
	}
	verified0, err := Verify(sequence0, testKeyID, publicKey)
	if err != nil {
		t.Fatalf("Verify sequence 0: %v", err)
	}
	verified0Payload := verified0.PayloadCopy()
	if verified0.Sequence() != 0 || len(verified0Payload.Entries) != 0 {
		t.Fatalf("unexpected sequence-0 payload: %#v", verified0Payload)
	}
	if got, want := verified0.Digest(), sha256.Sum256([]byte(sequence0)); got != want {
		t.Fatal("manifest digest does not bind exact envelope bytes")
	}

	handle, err := DeriveHandle("license-sequence-1")
	if err != nil {
		t.Fatal(err)
	}
	sequence1, err := BuildNext(verified0, handle, time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC), privateKey)
	if err != nil {
		t.Fatalf("BuildNext: %v", err)
	}
	verified1, err := Verify(sequence1, testKeyID, publicKey)
	if err != nil {
		t.Fatalf("Verify sequence 1: %v", err)
	}
	if verified1.Sequence() != 1 || !verified1.Contains(handle) {
		t.Fatalf("sequence 1 does not contain handle: %#v", verified1.PayloadCopy())
	}

	for name, want := range map[string]string{
		"manifest-sequence-0.tcrev": sequence0,
		"manifest-sequence-1.tcrev": sequence1,
	} {
		bytes, readErr := os.ReadFile("testdata/" + name)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		if got := strings.TrimSuffix(string(bytes), "\n"); got != want {
			t.Fatalf("%s differs from deterministic signed vector", name)
		}
	}
}

func TestVerifiedManifestAccessorsAreImmutableCopies(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	handle, _ := DeriveHandle("license-opaque")
	payload := testPayload(9, []Entry{{Kind: EntryKindLicenseIDSHA256, Value: handle.String(), RevokedAt: "2026-08-25T00:00:00Z"}})
	manifest := verifiedManifestForTest(t, publicKey, privateKey, payload)

	copyPayload := manifest.PayloadCopy()
	copyPayload.Sequence = 99
	copyPayload.Entries[0].Value = strings.Repeat("a", 43)
	if manifest.Sequence() != 9 || !manifest.Contains(handle) {
		t.Fatal("caller mutation changed verified manifest")
	}
	digest := manifest.Digest()
	digest[0] ^= 0xff
	if digest == manifest.Digest() {
		t.Fatal("digest accessor did not return a value copy")
	}
}

func TestManifestRejectsNonCanonicalAndUntrustedEnvelopes(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	valid, err := Sign(testPayload(0, []Entry{}), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(valid, ".")
	if len(parts) != 3 {
		t.Fatal("test envelope shape")
	}
	wrongPublic, _ := testAlternateManifestKey()

	unknownJSON := []byte(`{"schema":1,"key_id":"revocation-2026-01","sequence":0,"generated_at":"2026-08-25T00:00:00Z","entries":[],"extra":true}`)
	duplicateJSON := []byte(`{"schema":1,"schema":1,"key_id":"revocation-2026-01","sequence":0,"generated_at":"2026-08-25T00:00:00Z","entries":[]}`)
	noncanonicalJSON := []byte("{\n\"schema\":1,\"key_id\":\"revocation-2026-01\",\"sequence\":0,\"generated_at\":\"2026-08-25T00:00:00Z\",\"entries\":[]}")
	invalidUTF8JSON := append([]byte(`{"schema":1,"key_id":"revocation-`), 0xff)
	invalidUTF8JSON = append(invalidUTF8JSON, []byte(`","sequence":0,"generated_at":"2026-08-25T00:00:00Z","entries":[]}`)...)
	nonUTCJSON := []byte(`{"schema":1,"key_id":"revocation-2026-01","sequence":0,"generated_at":"2026-08-25T00:00:00+00:00","entries":[]}`)

	cases := map[string]struct {
		envelope string
		keyID    string
		key      ed25519.PublicKey
	}{
		"bad prefix":        {"TCREV2." + parts[1] + "." + parts[2], testKeyID, publicKey},
		"padded payload":    {parts[0] + "." + parts[1] + "=." + parts[2], testKeyID, publicKey},
		"padded signature":  {parts[0] + "." + parts[1] + "." + parts[2] + "=", testKeyID, publicKey},
		"mutated signature": {parts[0] + "." + parts[1] + "." + mutateBase64URL(parts[2]), testKeyID, publicKey},
		"wrong key id":      {valid, "revocation-other", publicKey},
		"wrong key":         {valid, testKeyID, wrongPublic},
		"unknown field":     {signRawPayload(unknownJSON, privateKey), testKeyID, publicKey},
		"duplicate field":   {signRawPayload(duplicateJSON, privateKey), testKeyID, publicKey},
		"noncanonical json": {signRawPayload(noncanonicalJSON, privateKey), testKeyID, publicKey},
		"invalid UTF-8":     {signRawPayload(invalidUTF8JSON, privateKey), testKeyID, publicKey},
		"non-UTC time":      {signRawPayload(nonUTCJSON, privateKey), testKeyID, publicKey},
		"trailing bytes":    {signRawPayload(append(mustCanonicalPayload(t, testPayload(0, []Entry{})), '\n'), privateKey), testKeyID, publicKey},
		"oversized":         {strings.Repeat("x", MaxEnvelopeBytes+1), testKeyID, publicKey},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if _, verifyErr := Verify(test.envelope, test.keyID, test.key); verifyErr == nil {
				t.Fatal("Verify succeeded")
			}
		})
	}
}

func TestManifestBoundaryLimits(t *testing.T) {
	validTime := "2026-08-25T00:00:00Z"
	entries := make([]Entry, MaxEntries+1)
	for index := range entries {
		var handle Handle
		handle[0] = byte(index >> 8)
		handle[1] = byte(index)
		entries[index] = Entry{Kind: EntryKindLicenseIDSHA256, Value: handle.String(), RevokedAt: validTime}
	}
	atLimit := testPayload(1, entries[:MaxEntries])
	if err := validatePayload(atLimit); err != nil {
		t.Fatalf("payload at entry limit rejected: %v", err)
	}
	overLimit := testPayload(1, entries)
	if err := validatePayload(overLimit); err == nil {
		t.Fatal("payload above entry limit accepted")
	}

	maxPayload := bytes.Repeat([]byte{'x'}, MaxPayloadBytes)
	encodedMaxPayload := base64.RawURLEncoding.EncodeToString(maxPayload)
	if decoded, ok := decodeCanonicalBase64URL(encodedMaxPayload, MaxPayloadBytes); !ok || len(decoded) != MaxPayloadBytes {
		t.Fatal("payload at decoded byte limit rejected")
	}
	overPayload := append(maxPayload, 'x')
	if _, ok := decodeCanonicalBase64URL(base64.RawURLEncoding.EncodeToString(overPayload), MaxPayloadBytes); ok {
		t.Fatal("payload above decoded byte limit accepted")
	}

	if !validKeyID(strings.Repeat("k", MaxKeyIDBytes)) {
		t.Fatal("key ID at byte limit rejected")
	}
	if validKeyID(strings.Repeat("k", MaxKeyIDBytes+1)) {
		t.Fatal("key ID above byte limit accepted")
	}

	publicKey, _ := testManifestKey()
	if _, err := Verify(strings.Repeat("x", MaxEnvelopeBytes+1), testKeyID, publicKey); err == nil {
		t.Fatal("envelope above byte limit accepted")
	}
}

func TestManifestRejectsInvalidPayloadRules(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	handleA, _ := DeriveHandle("license-a")
	handleB, _ := DeriveHandle("license-b")
	first, second := handleA, handleB
	if bytes.Compare(first[:], second[:]) > 0 {
		first, second = second, first
	}
	validTime := "2026-08-25T00:00:00Z"
	validEntry := Entry{Kind: EntryKindLicenseIDSHA256, Value: first.String(), RevokedAt: validTime}

	cases := map[string]Payload{
		"unsupported schema": {Schema: 2, KeyID: testKeyID, GeneratedAt: validTime, Entries: []Entry{}},
		"empty key id":       {Schema: 1, KeyID: "", GeneratedAt: validTime, Entries: []Entry{}},
		"long key id":        {Schema: 1, KeyID: strings.Repeat("a", MaxKeyIDBytes+1), GeneratedAt: validTime, Entries: []Entry{}},
		"non-ascii key id":   {Schema: 1, KeyID: "revocation-ключ", GeneratedAt: validTime, Entries: []Entry{}},
		"fractional time":    {Schema: 1, KeyID: testKeyID, GeneratedAt: "2026-08-25T00:00:00.1Z", Entries: []Entry{}},
		"nil entries":        {Schema: 1, KeyID: testKeyID, GeneratedAt: validTime, Entries: nil},
		"unsupported kind":   {Schema: 1, KeyID: testKeyID, GeneratedAt: validTime, Entries: []Entry{{Kind: "other", Value: first.String(), RevokedAt: validTime}}},
		"short handle":       {Schema: 1, KeyID: testKeyID, GeneratedAt: validTime, Entries: []Entry{{Kind: EntryKindLicenseIDSHA256, Value: "abcd", RevokedAt: validTime}}},
		"entry after manifest": {Schema: 1, KeyID: testKeyID, GeneratedAt: validTime, Entries: []Entry{{
			Kind: EntryKindLicenseIDSHA256, Value: first.String(), RevokedAt: "2026-08-25T00:00:01Z",
		}}},
		"duplicate": {Schema: 1, KeyID: testKeyID, GeneratedAt: validTime, Entries: []Entry{validEntry, validEntry}},
		"out of order": {Schema: 1, KeyID: testKeyID, GeneratedAt: validTime, Entries: []Entry{
			{Kind: EntryKindLicenseIDSHA256, Value: second.String(), RevokedAt: validTime}, validEntry,
		}},
	}

	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			envelope := signRawPayload(mustJSON(t, payload), privateKey)
			if _, err := Verify(envelope, testKeyID, publicKey); err == nil {
				t.Fatal("Verify succeeded")
			}
		})
	}
}

func TestBuildNextSortsAndRejectsDuplicateOrOverflow(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	baseEnvelope, err := Sign(testPayload(0, []Entry{}), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	base, err := Verify(baseEnvelope, testKeyID, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	handleB, _ := DeriveHandle("license-b")
	handleA, _ := DeriveHandle("license-a")
	if err := validatePayload(base.PayloadCopy()); err != nil {
		t.Fatalf("verified base payload is invalid: %v", err)
	}
	if base.Contains(handleB) {
		t.Fatal("empty base unexpectedly contains handle")
	}
	revokedAt := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	if revokedAt.Location() != time.UTC || revokedAt.Nanosecond() != 0 {
		t.Fatalf("test timestamp is not canonical UTC: %v", revokedAt)
	}
	oneEnvelope, err := BuildNext(base, handleB, revokedAt, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := Verify(oneEnvelope, testKeyID, publicKey)
	twoEnvelope, err := BuildNext(one, handleA, time.Date(2026, 8, 25, 0, 0, 1, 0, time.UTC), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	two, err := Verify(twoEnvelope, testKeyID, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	twoPayload := two.PayloadCopy()
	if len(twoPayload.Entries) != 2 {
		t.Fatalf("entry count = %d", len(twoPayload.Entries))
	}
	firstParsed, _ := ParseHandle(twoPayload.Entries[0].Value)
	secondParsed, _ := ParseHandle(twoPayload.Entries[1].Value)
	if bytes.Compare(firstParsed[:], secondParsed[:]) >= 0 {
		t.Fatalf("entries are not sorted: %#v", twoPayload.Entries)
	}
	if _, err := BuildNext(two, handleA, time.Now().UTC(), privateKey); err == nil {
		t.Fatal("duplicate BuildNext succeeded")
	}
	overflow := verifiedManifestForTest(t, publicKey, privateKey, testPayload(^uint64(0), []Entry{}))
	if _, err := BuildNext(overflow, handleB, time.Now().UTC(), privateKey); err == nil {
		t.Fatal("overflow BuildNext succeeded")
	}
}

func TestBuildNextAtKeepsRequestedAndGeneratedTimesDistinct(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	baseEnvelope, err := Sign(testPayload(0, []Entry{}), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	base, err := Verify(baseEnvelope, testKeyID, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	handle, _ := DeriveHandle("license-distinct-publication-time")
	requestedAt := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	generatedAt := requestedAt.Add(time.Hour)
	nextEnvelope, err := buildNextAt(base, handle, requestedAt, generatedAt, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	next, err := Verify(nextEnvelope, testKeyID, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	payload := next.PayloadCopy()
	if payload.GeneratedAt != generatedAt.Format(manifestTimeLayout) || len(payload.Entries) != 1 || payload.Entries[0].RevokedAt != requestedAt.Format(manifestTimeLayout) {
		t.Fatalf("payload times = %#v", payload)
	}
	other, _ := DeriveHandle("license-generated-time-rollback")
	if _, err := buildNextAt(next, other, requestedAt, requestedAt, privateKey); err == nil {
		t.Fatal("generated time before current manifest accepted")
	}
}

func testAlternateManifestKey() (ed25519.PublicKey, ed25519.PrivateKey) {
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	return privateKey.Public().(ed25519.PublicKey), privateKey
}

func signRawPayload(payload []byte, privateKey ed25519.PrivateKey) string {
	signature := ed25519.Sign(privateKey, payload)
	return EnvelopePrefix + "." + base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func mustCanonicalPayload(t *testing.T, payload Payload) []byte {
	t.Helper()
	encoded, err := canonicalPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func mutateBase64URL(value string) string {
	if value[0] == 'A' {
		return "B" + value[1:]
	}
	return "A" + value[1:]
}
