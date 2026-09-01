//go:build windows

package licenseissuer

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	generatorStateSchema = 1
	generatorStateFile   = "generator-state.json"
	maxGeneratorState    = 8 << 20
)

const dpapiEntropy = "Telegram Companion License Generator/DPAPI/v1"

// StoreError is a stable, non-sensitive state-store error classification.
type StoreError string

func (e StoreError) Error() string { return "licenseissuer: " + string(e) }

func (e StoreError) Is(target error) bool {
	other, ok := target.(StoreError)
	return ok && e == other
}

const (
	ErrStoreUnsupported StoreError = "state store is unsupported on this platform"
	ErrStateNotFound    StoreError = "generator state not found"
	ErrInvalidState     StoreError = "invalid generator state"
	ErrStateStorage     StoreError = "generator state storage failed"
)

// GeneratorState is the decrypted in-memory state used by the generator.
type GeneratorState struct {
	PrivateKey      ed25519.PrivateKey
	PublicKey       ed25519.PublicKey
	BackupConfirmed bool
	History         []HistoryEntry
}

type persistedGeneratorState struct {
	Schema          int             `json:"schema"`
	PrivateKeyDPAPI string          `json:"private_key_dpapi"`
	PublicKeySPKI   string          `json:"public_key_spki"`
	BackupConfirmed bool            `json:"backup_confirmed"`
	IssuanceHistory json.RawMessage `json:"history"`
}

// StateStore atomically persists the generator state below a caller-owned root.
type StateStore struct {
	path string
	mu   sync.Mutex
}

func DefaultStateRoot() (string, error) {
	configRoot, err := os.UserConfigDir()
	if err != nil || configRoot == "" {
		return "", ErrStateStorage
	}
	return filepath.Join(configRoot, "Telegram Companion License Generator"), nil
}

func NewStateStore(root string) (*StateStore, error) {
	if root == "" {
		return nil, ErrInvalidState
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, ErrInvalidState
	}
	return &StateStore{path: filepath.Join(absolute, generatorStateFile)}, nil
}

func (s *StateStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *StateStore) Load() (GeneratorState, error) {
	if s == nil || s.path == "" {
		return GeneratorState{}, ErrInvalidState
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	encoded, err := readStateFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return GeneratorState{}, ErrStateNotFound
	}
	if err != nil {
		return GeneratorState{}, ErrStateStorage
	}
	return decodeState(encoded)
}

func (s *StateStore) Save(state GeneratorState) error {
	if s == nil || s.path == "" {
		return ErrInvalidState
	}
	encoded, err := encodeState(state)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return ErrStateStorage
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".generator-state-*.tmp")
	if err != nil {
		return ErrStateStorage
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	writeErr := temporary.Chmod(0o600)
	if writeErr == nil {
		_, writeErr = temporary.Write(encoded)
	}
	if writeErr == nil {
		writeErr = temporary.Sync()
	}
	closeErr := temporary.Close()
	if writeErr != nil || closeErr != nil {
		return ErrStateStorage
	}
	if err := replaceFile(temporaryPath, s.path); err != nil {
		return ErrStateStorage
	}
	return nil
}

func encodeState(state GeneratorState) ([]byte, error) {
	if len(state.PrivateKey) != ed25519.PrivateKeySize || len(state.PublicKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidState
	}
	derivedPublic, ok := state.PrivateKey.Public().(ed25519.PublicKey)
	if !ok || !bytes.Equal(derivedPublic, state.PublicKey) {
		return nil, ErrInvalidState
	}
	publicKeySPKI, err := x509.MarshalPKIXPublicKey(state.PublicKey)
	if err != nil {
		return nil, ErrInvalidState
	}
	history, err := MarshalHistory(state.History)
	if err != nil {
		return nil, ErrInvalidState
	}
	protectedKey, err := protectWithDPAPI(state.PrivateKey)
	if err != nil {
		return nil, ErrInvalidState
	}
	document := persistedGeneratorState{
		Schema:          generatorStateSchema,
		PrivateKeyDPAPI: base64.RawStdEncoding.EncodeToString(protectedKey),
		PublicKeySPKI:   base64.RawStdEncoding.EncodeToString(publicKeySPKI),
		BackupConfirmed: state.BackupConfirmed,
		IssuanceHistory: history,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, ErrInvalidState
	}
	return encoded, nil
}

func decodeState(encoded []byte) (GeneratorState, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var document persistedGeneratorState
	if err := decoder.Decode(&document); err != nil {
		return GeneratorState{}, ErrInvalidState
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return GeneratorState{}, ErrInvalidState
	}
	if document.Schema != generatorStateSchema || len(document.IssuanceHistory) == 0 {
		return GeneratorState{}, ErrInvalidState
	}
	protectedKey, ok := decodeCanonicalBase64(document.PrivateKeyDPAPI)
	if !ok {
		return GeneratorState{}, ErrInvalidState
	}
	publicKeyDER, ok := decodeCanonicalBase64(document.PublicKeySPKI)
	if !ok {
		return GeneratorState{}, ErrInvalidState
	}
	parsedPublic, err := x509.ParsePKIXPublicKey(publicKeyDER)
	if err != nil {
		return GeneratorState{}, ErrInvalidState
	}
	publicKey, ok := parsedPublic.(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return GeneratorState{}, ErrInvalidState
	}
	privateKey, err := unprotectWithDPAPI(protectedKey)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		wipe(privateKey)
		return GeneratorState{}, ErrInvalidState
	}
	derivedPublic, ok := ed25519.PrivateKey(privateKey).Public().(ed25519.PublicKey)
	if !ok || !bytes.Equal(derivedPublic, publicKey) {
		wipe(privateKey)
		return GeneratorState{}, ErrInvalidState
	}
	history, err := UnmarshalHistory(document.IssuanceHistory)
	if err != nil {
		wipe(privateKey)
		return GeneratorState{}, ErrInvalidState
	}
	return GeneratorState{
		PrivateKey:      ed25519.PrivateKey(privateKey),
		PublicKey:       append(ed25519.PublicKey(nil), publicKey...),
		BackupConfirmed: document.BackupConfirmed,
		History:         history,
	}, nil
}

func protectWithDPAPI(plaintext []byte) ([]byte, error) {
	input := dataBlob(plaintext)
	entropyBytes := []byte(dpapiEntropy)
	entropy := dataBlob(entropyBytes)
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	runtime.KeepAlive(plaintext)
	runtime.KeepAlive(entropyBytes)
	return copyAndFreeBlob(&output)
}

func unprotectWithDPAPI(ciphertext []byte) ([]byte, error) {
	input := dataBlob(ciphertext)
	entropyBytes := []byte(dpapiEntropy)
	entropy := dataBlob(entropyBytes)
	var output windows.DataBlob
	var description *uint16
	if err := windows.CryptUnprotectData(&input, &description, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	if description != nil {
		_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(description)))
	}
	runtime.KeepAlive(ciphertext)
	runtime.KeepAlive(entropyBytes)
	return copyAndFreeBlob(&output)
}

func dataBlob(value []byte) windows.DataBlob {
	if len(value) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{Size: uint32(len(value)), Data: &value[0]}
}

func copyAndFreeBlob(blob *windows.DataBlob) ([]byte, error) {
	if blob == nil || blob.Data == nil || blob.Size == 0 {
		return nil, ErrInvalidState
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(blob.Data)))
	return append([]byte(nil), unsafe.Slice(blob.Data, int(blob.Size))...), nil
}

func decodeCanonicalBase64(value string) ([]byte, bool) {
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil || base64.RawStdEncoding.EncodeToString(decoded) != value {
		return nil, false
	}
	return decoded, true
}

func readStateFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < 1 || info.Size() > maxGeneratorState {
		return nil, ErrInvalidState
	}
	return io.ReadAll(io.LimitReader(file, maxGeneratorState+1))
}

func replaceFile(source, destination string) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(sourcePointer, destinationPointer, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
