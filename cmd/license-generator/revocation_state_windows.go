//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"telegram-companion/internal/licenseissuer"
)

type revocationStateRepository struct {
	path        string
	lockName    string
	replaceFile func(string, string) error
	baseline    revocationStateBaseline
	mu          sync.Mutex
}

func newRevocationStateRepository(root string) (*revocationStateRepository, error) {
	if root == "" {
		return nil, licenseissuer.ErrInvalidState
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, licenseissuer.ErrInvalidState
	}
	return &revocationStateRepository{
		path:        filepath.Join(absolute, revocationStateFile),
		lockName:    revocationStateProcessLockName(filepath.Join(absolute, revocationStateFile)),
		replaceFile: replaceRevocationStateFile,
	}, nil
}

func (repository *revocationStateRepository) Path() string {
	if repository == nil {
		return ""
	}
	return repository.path
}

func (repository *revocationStateRepository) Load() (generatorRevocationState, error) {
	if repository == nil || repository.path == "" || repository.replaceFile == nil {
		return generatorRevocationState{}, licenseissuer.ErrInvalidState
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	var state generatorRevocationState
	err := withRevocationStateProcessLock(repository.lockName, func() error {
		encoded, readErr := readRevocationStateFile(repository.path)
		if errors.Is(readErr, os.ErrNotExist) {
			repository.baseline.Observe(nil, false)
			return licenseissuer.ErrStateNotFound
		}
		if readErr != nil {
			return licenseissuer.ErrStateStorage
		}
		repository.baseline.Observe(encoded, true)
		decoded, decodeErr := decodeRevocationState(encoded, unprotectRevocationPrivateKey)
		if decodeErr != nil {
			return decodeErr
		}
		state = decoded
		return nil
	})
	return state, err
}

func (repository *revocationStateRepository) Save(state generatorRevocationState) error {
	if repository == nil || repository.path == "" {
		return licenseissuer.ErrInvalidState
	}
	encoded, err := encodeRevocationState(state, protectRevocationPrivateKey)
	if err != nil {
		return err
	}
	defer wipeBytes(encoded)
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return withRevocationStateProcessLock(repository.lockName, func() error {
		current, readErr := readRevocationStateFile(repository.path)
		exists := true
		if errors.Is(readErr, os.ErrNotExist) {
			exists = false
			current = nil
		} else if readErr != nil {
			return licenseissuer.ErrStateStorage
		}
		if !repository.baseline.initialized {
			if exists {
				return licenseissuer.ErrStateStorage
			}
			repository.baseline.Observe(nil, false)
		}
		if !repository.baseline.Matches(current, exists) {
			return licenseissuer.ErrStateStorage
		}
		if err := os.MkdirAll(filepath.Dir(repository.path), 0o700); err != nil {
			return licenseissuer.ErrStateStorage
		}
		temporary, err := os.CreateTemp(filepath.Dir(repository.path), ".generator-revocation-*.tmp")
		if err != nil {
			return licenseissuer.ErrStateStorage
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
		if err := repository.replaceFile(temporaryPath, repository.path); err != nil {
			return licenseissuer.ErrStateStorage
		}
		repository.baseline.Observe(encoded, true)
		return nil
	})
}

func revocationStateProcessLockName(path string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(path))))
	return `Local\TelegramCompanionLicenseGeneratorRevocation-` + hex.EncodeToString(digest[:])
}

func withRevocationStateProcessLock(name string, action func() error) error {
	if name == "" || action == nil {
		return licenseissuer.ErrStateStorage
	}
	// A Windows mutex is owned by the calling OS thread. Keep acquisition,
	// persistence, and release on that same thread even if the goroutine blocks.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	namePointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return licenseissuer.ErrStateStorage
	}
	handle, err := windows.CreateMutex(nil, false, namePointer)
	if handle == 0 || (err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS)) {
		if handle != 0 {
			windows.CloseHandle(handle)
		}
		return licenseissuer.ErrStateStorage
	}
	defer windows.CloseHandle(handle)
	result, err := windows.WaitForSingleObject(handle, uint32((30*time.Second)/time.Millisecond))
	if err != nil || (result != windows.WAIT_OBJECT_0 && result != windows.WAIT_ABANDONED) {
		return licenseissuer.ErrStateStorage
	}
	defer windows.ReleaseMutex(handle)
	return action()
}

func readRevocationStateFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < 1 || info.Size() > maxRevocationStateSize {
		return nil, licenseissuer.ErrInvalidState
	}
	encoded, err := io.ReadAll(io.LimitReader(file, maxRevocationStateSize+1))
	if err != nil || len(encoded) > maxRevocationStateSize {
		return nil, licenseissuer.ErrInvalidState
	}
	return encoded, nil
}

func replaceRevocationStateFile(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func protectRevocationPrivateKey(plaintext []byte) ([]byte, error) {
	return protectRevocationPrivateKeyWithEntropy(plaintext, revocationStateEntropy)
}

func protectRevocationPrivateKeyWithEntropy(plaintext []byte, entropyLabel string) ([]byte, error) {
	input := revocationDataBlob(plaintext)
	entropyBytes := []byte(entropyLabel)
	entropy := revocationDataBlob(entropyBytes)
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	runtime.KeepAlive(plaintext)
	runtime.KeepAlive(entropyBytes)
	return copyAndFreeRevocationBlob(&output)
}

func unprotectRevocationPrivateKey(ciphertext []byte) ([]byte, error) {
	return unprotectRevocationPrivateKeyWithEntropy(ciphertext, revocationStateEntropy)
}

func unprotectRevocationPrivateKeyWithEntropy(ciphertext []byte, entropyLabel string) ([]byte, error) {
	input := revocationDataBlob(ciphertext)
	entropyBytes := []byte(entropyLabel)
	entropy := revocationDataBlob(entropyBytes)
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
	return copyAndFreeRevocationBlob(&output)
}

func revocationDataBlob(value []byte) windows.DataBlob {
	if len(value) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{Size: uint32(len(value)), Data: &value[0]}
}

func copyAndFreeRevocationBlob(blob *windows.DataBlob) ([]byte, error) {
	return copyWipeAndReleaseRevocationBlob(blob, func(handle windows.Handle) {
		_, _ = windows.LocalFree(handle)
	})
}

func copyWipeAndReleaseRevocationBlob(blob *windows.DataBlob, release func(windows.Handle)) ([]byte, error) {
	if blob == nil || blob.Data == nil || release == nil {
		return nil, licenseissuer.ErrInvalidState
	}
	handle := windows.Handle(unsafe.Pointer(blob.Data))
	defer func() {
		release(handle)
		blob.Data = nil
		blob.Size = 0
	}()
	if blob.Size == 0 || blob.Size > maxRevocationStateSize {
		return nil, licenseissuer.ErrInvalidState
	}
	secret := unsafe.Slice(blob.Data, int(blob.Size))
	copy := copyAndWipeRevocationSecret(secret)
	runtime.KeepAlive(secret)
	return copy, nil
}

var _ revocationStateStore = (*revocationStateRepository)(nil)
