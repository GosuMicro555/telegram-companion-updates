//go:build windows

package main

import (
	"bytes"
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

	"telegram-companion/internal/licenseissuer"
)

const (
	seedStateFile    = "generator-seed-state.json"
	seedStateSchema  = 1
	maxSeedStateSize = 8 << 10
	seedStateEntropy = "Telegram Companion License Generator/Seed/DPAPI/v1"
)

type protectedStateRepository struct {
	stateStore signingStateStore
	seedPath   string
	mu         sync.Mutex
}

type signingStateStore interface {
	Load() (licenseissuer.GeneratorState, error)
	Save(licenseissuer.GeneratorState) error
}

type persistedSeedState struct {
	Schema       int    `json:"schema"`
	SeedID       string `json:"seed_id"`
	SeedKeyDPAPI string `json:"seed_key_dpapi"`
}

func newProtectedStateRepository(root string, stateStore *licenseissuer.StateStore) (*protectedStateRepository, error) {
	if root == "" || stateStore == nil {
		return nil, licenseissuer.ErrInvalidState
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, licenseissuer.ErrInvalidState
	}
	return &protectedStateRepository{stateStore: stateStore, seedPath: filepath.Join(absRoot, seedStateFile)}, nil
}

func (r *protectedStateRepository) Load() (generatorState, error) {
	if r == nil || r.stateStore == nil || r.seedPath == "" {
		return generatorState{}, licenseissuer.ErrInvalidState
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	signingState, err := r.stateStore.Load()
	if err != nil {
		return generatorState{}, err
	}
	state := generatorState{
		PrivateKey:      signingState.PrivateKey,
		PublicKey:       signingState.PublicKey,
		BackupConfirmed: signingState.BackupConfirmed,
		History:         signingState.History,
	}
	seedID, seedKey, err := loadProtectedSeed(r.seedPath)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return generatorState{}, licenseissuer.ErrInvalidState
	}
	state.SeedID = seedID
	state.SeedKey = seedKey
	return state, nil
}

func (r *protectedStateRepository) Save(state generatorState) error {
	if r == nil || r.stateStore == nil || r.seedPath == "" || !validSeedGrant(state.SeedID, state.SeedKey) {
		return licenseissuer.ErrInvalidState
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	previousSeedID, previousSeedKey, err := loadProtectedSeed(r.seedPath)
	hadPreviousSeed := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return licenseissuer.ErrStateStorage
	}
	defer wipeBytes(previousSeedKey)

	if err := saveProtectedSeed(r.seedPath, state.SeedID, state.SeedKey); err != nil {
		return licenseissuer.ErrStateStorage
	}
	err = r.stateStore.Save(licenseissuer.GeneratorState{
		PrivateKey:      state.PrivateKey,
		PublicKey:       state.PublicKey,
		BackupConfirmed: state.BackupConfirmed,
		History:         state.History,
	})
	if err == nil {
		return nil
	}

	if hadPreviousSeed {
		if rollbackErr := saveProtectedSeed(r.seedPath, previousSeedID, previousSeedKey); rollbackErr != nil {
			return licenseissuer.ErrStateStorage
		}
	} else if rollbackErr := os.Remove(r.seedPath); rollbackErr != nil && !errors.Is(rollbackErr, os.ErrNotExist) {
		return licenseissuer.ErrStateStorage
	}
	return err
}

func loadProtectedSeed(path string) (string, []byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() < 1 || info.Size() > maxSeedStateSize {
		return "", nil, licenseissuer.ErrInvalidState
	}
	encoded, err := io.ReadAll(io.LimitReader(file, maxSeedStateSize+1))
	if err != nil || len(encoded) > maxSeedStateSize {
		return "", nil, licenseissuer.ErrInvalidState
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var document persistedSeedState
	if err := decoder.Decode(&document); err != nil {
		return "", nil, licenseissuer.ErrInvalidState
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || document.Schema != seedStateSchema {
		return "", nil, licenseissuer.ErrInvalidState
	}
	protected, ok := decodeCanonicalBase64(document.SeedKeyDPAPI)
	if !ok {
		return "", nil, licenseissuer.ErrInvalidState
	}
	seedKey, err := unprotectSeed(protected)
	if err != nil || !validSeedGrant(document.SeedID, seedKey) {
		wipeBytes(seedKey)
		return "", nil, licenseissuer.ErrInvalidState
	}
	return document.SeedID, seedKey, nil
}

func saveProtectedSeed(path, seedID string, seedKey []byte) error {
	protected, err := protectSeed(seedKey)
	if err != nil {
		return err
	}
	document := persistedSeedState{
		Schema:       seedStateSchema,
		SeedID:       seedID,
		SeedKeyDPAPI: base64.RawStdEncoding.EncodeToString(protected),
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".generator-seed-*.tmp")
	if err != nil {
		return err
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
		return licenseissuer.ErrStateStorage
	}
	from, err := windows.UTF16PtrFromString(temporaryPath)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func protectSeed(plaintext []byte) ([]byte, error) {
	input := seedDataBlob(plaintext)
	entropyBytes := []byte(seedStateEntropy)
	entropy := seedDataBlob(entropyBytes)
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	runtime.KeepAlive(plaintext)
	runtime.KeepAlive(entropyBytes)
	return copyAndFreeSeedBlob(&output)
}

func unprotectSeed(ciphertext []byte) ([]byte, error) {
	input := seedDataBlob(ciphertext)
	entropyBytes := []byte(seedStateEntropy)
	entropy := seedDataBlob(entropyBytes)
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
	return copyAndFreeSeedBlob(&output)
}

func seedDataBlob(value []byte) windows.DataBlob {
	if len(value) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{Size: uint32(len(value)), Data: &value[0]}
}

func copyAndFreeSeedBlob(blob *windows.DataBlob) ([]byte, error) {
	if blob == nil || blob.Data == nil || blob.Size == 0 {
		return nil, licenseissuer.ErrInvalidState
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(blob.Data)))
	return append([]byte(nil), unsafe.Slice(blob.Data, int(blob.Size))...), nil
}

func decodeCanonicalBase64(value string) ([]byte, bool) {
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	return decoded, err == nil && base64.RawStdEncoding.EncodeToString(decoded) == value
}
