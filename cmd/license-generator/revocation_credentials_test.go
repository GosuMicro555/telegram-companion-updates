package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	keyring "github.com/zalando/go-keyring"
)

func TestRevocationCredentialsStoresFineGrainedPATUnderDedicatedName(t *testing.T) {
	store := &memoryGeneratorSecretStore{}
	credentials, err := newRevocationCredentials(store)
	if err != nil {
		t.Fatalf("newRevocationCredentials() error = %v", err)
	}
	token := "github_pat_" + string(bytes.Repeat([]byte{'a'}, 82))
	if err := credentials.Configure(context.Background(), token); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if store.setName != generatorRevocationCredentialName || string(store.secret) != token {
		t.Fatalf("stored credential name = %q, value length = %d", store.setName, len(store.secret))
	}
	configured, err := credentials.Configured(context.Background())
	if err != nil || !configured {
		t.Fatalf("Configured() = %v, %v", configured, err)
	}
	loaded, err := credentials.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	defer wipeBytes(loaded)
	if string(loaded) != token {
		t.Fatal("Load() changed the credential")
	}
	loaded[0] = 'X'
	if string(store.secret) != token {
		t.Fatal("Load() returned an alias to credential storage")
	}
}

func TestRevocationCredentialsRejectsUnsafePATWithoutStorage(t *testing.T) {
	store := &memoryGeneratorSecretStore{}
	credentials, err := newRevocationCredentials(store)
	if err != nil {
		t.Fatalf("newRevocationCredentials() error = %v", err)
	}
	for name, token := range map[string]string{
		"empty":         "",
		"classic":       "ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"whitespace":    "github_pat_abcdefghijklmnopqrstuvwxyz 0123456789",
		"control":       "github_pat_abcdefghijklmnopqrstuvwxyz\n0123456789",
		"too long":      "github_pat_" + string(bytes.Repeat([]byte{'x'}, maxGeneratorRevocationCredentialSize)),
		"non printable": "github_pat_abcdefghijklmnopqrstuvwxyz\u007f0123456789",
	} {
		t.Run(name, func(t *testing.T) {
			if err := credentials.Configure(context.Background(), token); !errors.Is(err, ErrRevocationCredential) {
				t.Fatalf("Configure() error = %v, want ErrRevocationCredential", err)
			}
		})
	}
	if store.setCalls != 0 {
		t.Fatalf("invalid PAT reached storage %d times", store.setCalls)
	}
}

func TestRevocationCredentialsReportsMissingWithoutExposingBackendErrors(t *testing.T) {
	store := &memoryGeneratorSecretStore{getErr: keyring.ErrNotFound}
	credentials, err := newRevocationCredentials(store)
	if err != nil {
		t.Fatalf("newRevocationCredentials() error = %v", err)
	}
	configured, err := credentials.Configured(context.Background())
	if err != nil || configured {
		t.Fatalf("Configured(missing) = %v, %v", configured, err)
	}
	store.getErr = errors.New("backend contains secret github_pat_should_never_escape")
	if _, err := credentials.Configured(context.Background()); !errors.Is(err, ErrRevocationCredential) || err.Error() != ErrRevocationCredential.Error() {
		t.Fatalf("Configured(backend failure) error = %q", err)
	}
}

type memoryGeneratorSecretStore struct {
	secret   []byte
	getErr   error
	setErr   error
	setName  string
	setCalls int
}

func (store *memoryGeneratorSecretStore) Get(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store.getErr != nil {
		return nil, store.getErr
	}
	return append([]byte(nil), store.secret...), nil
}

func (store *memoryGeneratorSecretStore) Set(ctx context.Context, name string, secret []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.setCalls++
	if store.setErr != nil {
		return store.setErr
	}
	store.setName = name
	store.secret = append([]byte(nil), secret...)
	return nil
}
