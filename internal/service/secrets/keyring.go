package secrets

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	keyring "github.com/zalando/go-keyring"
)

// ErrKeyringUnavailable classifies failures reported by the platform keyring.
// Callers must use errors.Is/As; its rendered form deliberately omits backend
// error text and the service and secret identifiers involved.
var ErrKeyringUnavailable = errors.New("keyring unavailable")

type KeyringUnavailableError struct {
	cause error
}

func (e *KeyringUnavailableError) Error() string { return ErrKeyringUnavailable.Error() }

func (e *KeyringUnavailableError) Unwrap() error { return e.cause }

func (e *KeyringUnavailableError) Is(target error) bool { return target == ErrKeyringUnavailable }

func newKeyringUnavailableError(cause error) error {
	return &KeyringUnavailableError{cause: cause}
}

type keyringBackend interface {
	Get(service, name string) (string, error)
	Set(service, name, value string) error
	Delete(service, name string) error
}

type SecretStore struct {
	service string
	backend keyringBackend
	random  io.Reader
	mu      *sync.Mutex
}

var secretStoreLocks sync.Map

func NewSecretStore(service string) *SecretStore {
	return newSecretStore(service, systemKeyring{}, rand.Reader)
}

func newSecretStore(service string, backend keyringBackend, random io.Reader) *SecretStore {
	lock, _ := secretStoreLocks.LoadOrStore(strings.TrimSpace(service), &sync.Mutex{})
	return &SecretStore{service: service, backend: backend, random: random, mu: lock.(*sync.Mutex)}
}

func (s *SecretStore) Get(ctx context.Context, name string) ([]byte, error) {
	if err := s.validate(name); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	encoded, err := s.backend.Get(s.service, name)
	if err != nil {
		return nil, err
	}
	secret, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("decode keyring secret")
	}
	return secret, nil
}

func (s *SecretStore) Set(ctx context.Context, name string, secret []byte) error {
	if err := s.validate(name); err != nil {
		return err
	}
	if len(secret) == 0 {
		return errors.New("secret value is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded := base64.StdEncoding.EncodeToString(secret)
	if err := s.backend.Set(s.service, name, encoded); err != nil {
		return err
	}
	return nil
}

// SetBatch applies all values and runs commit while updates for this keyring
// service are serialized. Any write or commit failure restores prior values.
func (s *SecretStore) SetBatch(ctx context.Context, values map[string][]byte, commit func() error) error {
	if s == nil || s.backend == nil || s.mu == nil || strings.TrimSpace(s.service) == "" {
		return errors.New("secret store is not configured")
	}
	if commit == nil {
		return errors.New("secret batch commit is required")
	}
	names := make([]string, 0, len(values))
	encoded := make(map[string]string, len(values))
	for name, value := range values {
		if err := s.validate(name); err != nil {
			return err
		}
		if len(value) == 0 {
			return errors.New("secret value is required")
		}
		names = append(names, name)
		encoded[name] = base64.StdEncoding.EncodeToString(value)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sort.Strings(names)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	type priorSecret struct {
		value  string
		exists bool
	}
	prior := make(map[string]priorSecret, len(names))
	for _, name := range names {
		value, err := s.backend.Get(s.service, name)
		switch {
		case err == nil:
			prior[name] = priorSecret{value: value, exists: true}
		case errors.Is(err, keyring.ErrNotFound):
			prior[name] = priorSecret{}
		default:
			return err
		}
	}
	rollback := func() error {
		var rollbackErr error
		for index := len(names) - 1; index >= 0; index-- {
			name := names[index]
			previous := prior[name]
			if previous.exists {
				if err := s.backend.Set(s.service, name, previous.value); err != nil {
					rollbackErr = errors.Join(rollbackErr, err)
				}
				continue
			}
			if err := s.backend.Delete(s.service, name); err != nil && !errors.Is(err, keyring.ErrNotFound) {
				rollbackErr = errors.Join(rollbackErr, err)
			}
		}
		return rollbackErr
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, rollback())
		}
		if err := s.backend.Set(s.service, name, encoded[name]); err != nil {
			return errors.Join(err, rollback())
		}
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, rollback())
	}
	if err := commit(); err != nil {
		return errors.Join(err, rollback())
	}
	return nil
}

func (s *SecretStore) GetOrCreate(ctx context.Context, name string, bytes int) ([]byte, error) {
	if err := s.validate(name); err != nil {
		return nil, err
	}
	if s.random == nil {
		return nil, errors.New("secret store is not configured")
	}
	if bytes <= 0 {
		return nil, errors.New("secret byte count must be positive")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	encoded, err := s.backend.Get(s.service, name)
	if err == nil {
		secret, decodeErr := base64.StdEncoding.DecodeString(encoded)
		if decodeErr != nil {
			return nil, errors.New("decode keyring secret")
		}
		if len(secret) != bytes {
			return nil, fmt.Errorf("keyring secret length is %d bytes, expected %d", len(secret), bytes)
		}
		return secret, nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	secret := make([]byte, bytes)
	if _, err := io.ReadFull(s.random, secret); err != nil {
		return nil, fmt.Errorf("generate secret: %w", err)
	}
	encoded = base64.StdEncoding.EncodeToString(secret)
	if err := s.backend.Set(s.service, name, encoded); err != nil {
		clear(secret)
		return nil, err
	}
	return secret, nil
}

func (s *SecretStore) validate(name string) error {
	if s == nil || s.backend == nil || s.mu == nil || strings.TrimSpace(s.service) == "" {
		return errors.New("secret store is not configured")
	}
	if strings.TrimSpace(name) == "" {
		return errors.New("secret name is required")
	}
	return nil
}

type systemKeyring struct{}

func (systemKeyring) Get(service, name string) (string, error) {
	value, err := readSystemKeyring(service, name)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return value, err
	}
	return "", newKeyringUnavailableError(err)
}

func (systemKeyring) Set(service, name, value string) error {
	if err := keyring.Set(service, name, value); err != nil {
		return newKeyringUnavailableError(err)
	}
	return nil
}

func (systemKeyring) Delete(service, name string) error {
	err := keyring.Delete(service, name)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return newKeyringUnavailableError(err)
}
