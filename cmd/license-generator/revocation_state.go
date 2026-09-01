package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"

	"telegram-companion/internal/licenseissuer"
	"telegram-companion/internal/revocation"
)

const (
	revocationStateFile    = "generator-revocation-state.json"
	revocationStateSchema  = 1
	maxRevocationStateSize = 8 << 20
	revocationStateEntropy = "Telegram Companion License Generator/Revocation/DPAPI/v1"
)

type revocationPublicationState string

const (
	revocationPublicationPending   revocationPublicationState = "pending"
	revocationPublicationFailed    revocationPublicationState = "failed"
	revocationPublicationPublished revocationPublicationState = "published"
)

type revocationPublicationRecord struct {
	EventID           string                     `json:"event_id"`
	Handle            string                     `json:"handle"`
	RequestedAt       string                     `json:"requested_at"`
	PublishedSequence uint64                     `json:"published_sequence,omitempty"`
	RemoteBlobID      string                     `json:"remote_blob_id,omitempty"`
	State             revocationPublicationState `json:"state"`
}

type generatorRevocationState struct {
	PrivateKey      ed25519.PrivateKey
	PublicKey       ed25519.PublicKey
	BackupConfirmed bool
	Records         []revocationPublicationRecord
}

type revocationStateStore interface {
	Load() (generatorRevocationState, error)
	Save(generatorRevocationState) error
}

type persistedRevocationState struct {
	Schema          int                           `json:"schema"`
	PublicKey       string                        `json:"public_key"`
	PrivateKeyDPAPI string                        `json:"private_key_dpapi"`
	BackupConfirmed bool                          `json:"backup_confirmed"`
	Records         []revocationPublicationRecord `json:"records"`
}

type decodedPersistedRevocationState struct {
	Schema          int             `json:"schema"`
	PublicKey       string          `json:"public_key"`
	PrivateKeyDPAPI string          `json:"private_key_dpapi"`
	BackupConfirmed bool            `json:"backup_confirmed"`
	Records         json.RawMessage `json:"records"`
}

type revocationSecretCodec func([]byte) ([]byte, error)

func encodeRevocationState(state generatorRevocationState, protect revocationSecretCodec) ([]byte, error) {
	if protect == nil || !validGeneratorRevocationState(state) {
		return nil, licenseissuer.ErrInvalidState
	}
	privateKey := append([]byte(nil), state.PrivateKey...)
	defer wipeBytes(privateKey)
	protected, err := protect(privateKey)
	if err != nil || len(protected) == 0 {
		return nil, licenseissuer.ErrStateStorage
	}
	records := make([]revocationPublicationRecord, len(state.Records))
	copy(records, state.Records)
	document := persistedRevocationState{
		Schema:          revocationStateSchema,
		PublicKey:       base64.RawStdEncoding.EncodeToString(state.PublicKey),
		PrivateKeyDPAPI: base64.RawStdEncoding.EncodeToString(protected),
		BackupConfirmed: state.BackupConfirmed,
		Records:         records,
	}
	encoded, err := json.Marshal(document)
	if err != nil || len(encoded) == 0 || len(encoded) > maxRevocationStateSize {
		return nil, licenseissuer.ErrInvalidState
	}
	return encoded, nil
}

func decodeRevocationState(encoded []byte, unprotect revocationSecretCodec) (generatorRevocationState, error) {
	if unprotect == nil || len(encoded) == 0 || len(encoded) > maxRevocationStateSize {
		return generatorRevocationState{}, licenseissuer.ErrInvalidState
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var document decodedPersistedRevocationState
	if err := decoder.Decode(&document); err != nil {
		return generatorRevocationState{}, licenseissuer.ErrInvalidState
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || document.Schema != revocationStateSchema || len(document.Records) == 0 {
		return generatorRevocationState{}, licenseissuer.ErrInvalidState
	}
	var records []revocationPublicationRecord
	if bytes.Equal(document.Records, []byte("null")) {
		if document.BackupConfirmed {
			return generatorRevocationState{}, licenseissuer.ErrInvalidState
		}
		records = make([]revocationPublicationRecord, 0)
	} else {
		recordsDecoder := json.NewDecoder(bytes.NewReader(document.Records))
		recordsDecoder.DisallowUnknownFields()
		if err := recordsDecoder.Decode(&records); err != nil || records == nil {
			return generatorRevocationState{}, licenseissuer.ErrInvalidState
		}
		if err := recordsDecoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return generatorRevocationState{}, licenseissuer.ErrInvalidState
		}
	}
	publicKey, ok := decodeCanonicalRevocationStateBase64(document.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return generatorRevocationState{}, licenseissuer.ErrInvalidState
	}
	protected, ok := decodeCanonicalRevocationStateBase64(document.PrivateKeyDPAPI)
	if !ok || len(protected) == 0 {
		return generatorRevocationState{}, licenseissuer.ErrInvalidState
	}
	privateKey, err := unprotect(protected)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		wipeBytes(privateKey)
		return generatorRevocationState{}, licenseissuer.ErrInvalidState
	}
	state := generatorRevocationState{
		PrivateKey:      ed25519.PrivateKey(privateKey),
		PublicKey:       append(ed25519.PublicKey(nil), publicKey...),
		BackupConfirmed: document.BackupConfirmed,
		Records:         make([]revocationPublicationRecord, len(records)),
	}
	copy(state.Records, records)
	if !validGeneratorRevocationState(state) {
		wipeBytes(privateKey)
		return generatorRevocationState{}, licenseissuer.ErrInvalidState
	}
	return state, nil
}

func validGeneratorRevocationState(state generatorRevocationState) bool {
	if len(state.PrivateKey) != ed25519.PrivateKeySize || len(state.PublicKey) != ed25519.PublicKeySize || state.Records == nil {
		return false
	}
	derived := derivePrivateKeyFromCopiedSeed(state.PrivateKey, ed25519.NewKeyFromSeed)
	defer wipeBytes(derived)
	if !bytes.Equal(derived, state.PrivateKey) || !bytes.Equal(derived[ed25519.SeedSize:], state.PublicKey) {
		return false
	}
	eventIDs := make(map[string]struct{}, len(state.Records))
	handles := make(map[string]struct{}, len(state.Records))
	for _, record := range state.Records {
		if !validRevocationPublicationRecord(record) {
			return false
		}
		if _, duplicate := eventIDs[record.EventID]; duplicate {
			return false
		}
		if _, duplicate := handles[record.Handle]; duplicate {
			return false
		}
		eventIDs[record.EventID] = struct{}{}
		handles[record.Handle] = struct{}{}
	}
	return true
}

func validRevocationPublicationRecord(record revocationPublicationRecord) bool {
	if record.EventID == "" || len(record.EventID) > 128 || strings.TrimSpace(record.EventID) != record.EventID || strings.IndexFunc(record.EventID, unicode.IsControl) >= 0 {
		return false
	}
	if _, err := revocation.ParseHandle(record.Handle); err != nil {
		return false
	}
	requestedAt, err := time.Parse(time.RFC3339, record.RequestedAt)
	if err != nil || requestedAt.Nanosecond() != 0 || requestedAt.Location() != time.UTC || requestedAt.Format(time.RFC3339) != record.RequestedAt {
		return false
	}
	switch record.State {
	case revocationPublicationPending:
		if record.PublishedSequence == 0 {
			return record.RemoteBlobID == ""
		}
		return validRemoteBlobID(record.RemoteBlobID)
	case revocationPublicationFailed:
		return record.PublishedSequence == 0 && record.RemoteBlobID == ""
	case revocationPublicationPublished:
		return record.PublishedSequence > 0 && validRemoteBlobID(record.RemoteBlobID)
	default:
		return false
	}
}

func validRemoteBlobID(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

func decodeCanonicalRevocationStateBase64(value string) ([]byte, bool) {
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	return decoded, err == nil && base64.RawStdEncoding.EncodeToString(decoded) == value
}

func cloneGeneratorRevocationState(state generatorRevocationState) generatorRevocationState {
	var records []revocationPublicationRecord
	if state.Records != nil {
		records = make([]revocationPublicationRecord, len(state.Records))
		copy(records, state.Records)
	}
	return generatorRevocationState{
		PrivateKey:      append(ed25519.PrivateKey(nil), state.PrivateKey...),
		PublicKey:       append(ed25519.PublicKey(nil), state.PublicKey...),
		BackupConfirmed: state.BackupConfirmed,
		Records:         records,
	}
}

func derivePrivateKeyFromCopiedSeed(privateKey ed25519.PrivateKey, derive func([]byte) ed25519.PrivateKey) ed25519.PrivateKey {
	seed := privateKey.Seed()
	defer wipeBytes(seed)
	return derive(seed)
}

func copyAndWipeRevocationSecret(secret []byte) []byte {
	copy := append([]byte(nil), secret...)
	wipeBytes(secret)
	return copy
}
