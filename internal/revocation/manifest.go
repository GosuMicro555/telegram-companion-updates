package revocation

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	EnvelopePrefix                  = "TCREV1"
	ManifestSchema           uint64 = 1
	EntryKindLicenseIDSHA256        = "license_id_sha256"
	MaxEntries                      = 10_000
	MaxEnvelopeBytes                = 1_048_576
	MaxPayloadBytes                 = 786_432
	MaxKeyIDBytes                   = 64
	manifestTimeLayout              = "2006-01-02T15:04:05Z"
)

var errInvalidManifest = errors.New("revocation: invalid manifest")

type Entry struct {
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	RevokedAt string `json:"revoked_at"`
}

type Payload struct {
	Schema      uint64  `json:"schema"`
	KeyID       string  `json:"key_id"`
	Sequence    uint64  `json:"sequence"`
	GeneratedAt string  `json:"generated_at"`
	Entries     []Entry `json:"entries"`
}

type VerifiedManifest struct {
	payload Payload
	digest  [sha256.Size]byte
}

// PayloadCopy returns a detached copy of the authenticated payload. Mutating the
// result cannot change the manifest accepted by Verify.
func (manifest VerifiedManifest) PayloadCopy() Payload {
	return clonePayload(manifest.payload)
}

func (manifest VerifiedManifest) Sequence() uint64 {
	return manifest.payload.Sequence
}

func (manifest VerifiedManifest) KeyID() string {
	return manifest.payload.KeyID
}

func (manifest VerifiedManifest) Digest() [sha256.Size]byte {
	return manifest.digest
}

func Sign(payload Payload, key ed25519.PrivateKey) (string, error) {
	if len(key) != ed25519.PrivateKeySize {
		return "", errInvalidManifest
	}
	encoded, err := canonicalPayload(payload)
	if err != nil || len(encoded) > MaxPayloadBytes {
		return "", errInvalidManifest
	}
	signature := ed25519.Sign(key, encoded)
	envelope := EnvelopePrefix + "." + base64.RawURLEncoding.EncodeToString(encoded) + "." + base64.RawURLEncoding.EncodeToString(signature)
	if len(envelope) > MaxEnvelopeBytes {
		return "", errInvalidManifest
	}
	return envelope, nil
}

func Verify(envelope, keyID string, key ed25519.PublicKey) (VerifiedManifest, error) {
	if envelope == "" || len(envelope) > MaxEnvelopeBytes || len(key) != ed25519.PublicKeySize || !validKeyID(keyID) {
		return VerifiedManifest{}, errInvalidManifest
	}
	parts := strings.Split(envelope, ".")
	if len(parts) != 3 || parts[0] != EnvelopePrefix {
		return VerifiedManifest{}, errInvalidManifest
	}
	payloadBytes, ok := decodeCanonicalBase64URL(parts[1], MaxPayloadBytes)
	if !ok {
		return VerifiedManifest{}, errInvalidManifest
	}
	signature, ok := decodeCanonicalBase64URL(parts[2], ed25519.SignatureSize)
	if !ok || len(signature) != ed25519.SignatureSize || !ed25519.Verify(key, payloadBytes, signature) {
		return VerifiedManifest{}, errInvalidManifest
	}

	decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
	decoder.DisallowUnknownFields()
	var payload Payload
	if err := decoder.Decode(&payload); err != nil {
		return VerifiedManifest{}, errInvalidManifest
	}
	if err := requireJSONEOF(decoder); err != nil {
		return VerifiedManifest{}, errInvalidManifest
	}
	canonical, err := canonicalPayload(payload)
	if err != nil || !bytes.Equal(canonical, payloadBytes) || payload.KeyID != keyID {
		return VerifiedManifest{}, errInvalidManifest
	}

	return VerifiedManifest{
		payload: clonePayload(payload),
		digest:  sha256.Sum256([]byte(envelope)),
	}, nil
}

func BuildNext(current VerifiedManifest, handle Handle, revokedAt time.Time, key ed25519.PrivateKey) (string, error) {
	return buildNextAt(current, handle, revokedAt, revokedAt, key)
}

func buildNextAt(current VerifiedManifest, handle Handle, revokedAt, generatedAt time.Time, key ed25519.PrivateKey) (string, error) {
	currentGeneratedAt, currentTimeOK := parseManifestTime(current.payload.GeneratedAt)
	if !current.valid() || current.payload.Sequence == math.MaxUint64 || len(current.payload.Entries) >= MaxEntries || revokedAt.Location() != time.UTC || revokedAt.Nanosecond() != 0 || generatedAt.Location() != time.UTC || generatedAt.Nanosecond() != 0 || revokedAt.After(generatedAt) || !currentTimeOK || generatedAt.Before(currentGeneratedAt) {
		return "", errInvalidManifest
	}
	if current.Contains(handle) {
		return "", errInvalidManifest
	}

	entries := append([]Entry(nil), current.payload.Entries...)
	entries = append(entries, Entry{
		Kind:      EntryKindLicenseIDSHA256,
		Value:     handle.String(),
		RevokedAt: revokedAt.Format(manifestTimeLayout),
	})
	sort.Slice(entries, func(left, right int) bool {
		leftHandle, _ := ParseHandle(entries[left].Value)
		rightHandle, _ := ParseHandle(entries[right].Value)
		return bytes.Compare(leftHandle[:], rightHandle[:]) < 0
	})

	next := Payload{
		Schema:      ManifestSchema,
		KeyID:       current.payload.KeyID,
		Sequence:    current.payload.Sequence + 1,
		GeneratedAt: generatedAt.Format(manifestTimeLayout),
		Entries:     entries,
	}
	return Sign(next, key)
}

func (manifest VerifiedManifest) Contains(handle Handle) bool {
	if !manifest.valid() {
		return false
	}
	index := sort.Search(len(manifest.payload.Entries), func(index int) bool {
		candidate, err := ParseHandle(manifest.payload.Entries[index].Value)
		return err != nil || bytes.Compare(candidate[:], handle[:]) >= 0
	})
	if index >= len(manifest.payload.Entries) {
		return false
	}
	candidate, err := ParseHandle(manifest.payload.Entries[index].Value)
	return err == nil && candidate == handle
}

func (manifest VerifiedManifest) valid() bool {
	return manifest.digest != ([sha256.Size]byte{}) && validatePayload(manifest.payload) == nil
}

func canonicalPayload(payload Payload) ([]byte, error) {
	if err := validatePayload(payload); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(payload)
	if err != nil || len(encoded) > MaxPayloadBytes {
		return nil, errInvalidManifest
	}
	return encoded, nil
}

func validatePayload(payload Payload) error {
	if payload.Schema != ManifestSchema || !validKeyID(payload.KeyID) || payload.Entries == nil || len(payload.Entries) > MaxEntries {
		return errInvalidManifest
	}
	generatedAt, ok := parseManifestTime(payload.GeneratedAt)
	if !ok {
		return errInvalidManifest
	}
	var prior Handle
	for index, entry := range payload.Entries {
		if entry.Kind != EntryKindLicenseIDSHA256 {
			return errInvalidManifest
		}
		handle, err := ParseHandle(entry.Value)
		if err != nil {
			return errInvalidManifest
		}
		revokedAt, valid := parseManifestTime(entry.RevokedAt)
		if !valid || revokedAt.After(generatedAt) {
			return errInvalidManifest
		}
		if index > 0 && bytes.Compare(prior[:], handle[:]) >= 0 {
			return errInvalidManifest
		}
		prior = handle
	}
	return nil
}

func parseManifestTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(manifestTimeLayout, value)
	return parsed, err == nil && parsed.Location() == time.UTC && parsed.Format(manifestTimeLayout) == value
}

func validKeyID(value string) bool {
	if value == "" || len(value) > MaxKeyIDBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}

func decodeCanonicalBase64URL(value string, maximum int) ([]byte, bool) {
	if value == "" || len(value) > base64.RawURLEncoding.EncodedLen(maximum) {
		return nil, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) > maximum || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, false
	}
	return decoded, true
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errInvalidManifest
	}
	return nil
}

func clonePayload(payload Payload) Payload {
	cloned := payload
	cloned.Entries = make([]Entry, len(payload.Entries))
	copy(cloned.Entries, payload.Entries)
	return cloned
}
