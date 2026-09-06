package main

import (
	"bytes"
	"context"
	"errors"

	keyring "github.com/zalando/go-keyring"
)

const (
	generatorRevocationCredentialService = "telegram-companion-license-generator"
	generatorRevocationCredentialName    = "github-revocation-publisher-v1"
	maxGeneratorRevocationCredentialSize = 512
)

const ErrRevocationCredential GeneratorError = "revocation credential failed"

type generatorSecretStore interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte) error
}

type revocationCredentials struct {
	store generatorSecretStore
}

func newRevocationCredentials(store generatorSecretStore) (*revocationCredentials, error) {
	if store == nil {
		return nil, ErrRevocationCredential
	}
	return &revocationCredentials{store: store}, nil
}

func (credentials *revocationCredentials) Configure(ctx context.Context, token string) error {
	if credentials == nil || credentials.store == nil || ctx == nil {
		return ErrRevocationCredential
	}
	secret := []byte(token)
	defer wipeBytes(secret)
	if !validGeneratorRevocationCredential(secret) {
		return ErrRevocationCredential
	}
	if err := credentials.store.Set(ctx, generatorRevocationCredentialName, secret); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return ErrRevocationCredential
	}
	return nil
}

func (credentials *revocationCredentials) Configured(ctx context.Context) (bool, error) {
	if credentials == nil || credentials.store == nil || ctx == nil {
		return false, ErrRevocationCredential
	}
	secret, err := credentials.store.Get(ctx, generatorRevocationCredentialName)
	defer wipeBytes(secret)
	if errors.Is(err, keyring.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		return false, ErrRevocationCredential
	}
	if !validGeneratorRevocationCredential(secret) {
		return false, ErrRevocationCredential
	}
	return true, nil
}

func (credentials *revocationCredentials) Load(ctx context.Context) ([]byte, error) {
	if credentials == nil || credentials.store == nil || ctx == nil {
		return nil, ErrRevocationCredential
	}
	secret, err := credentials.store.Get(ctx, generatorRevocationCredentialName)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, ErrRevocationCredential
	}
	if !validGeneratorRevocationCredential(secret) {
		wipeBytes(secret)
		return nil, ErrRevocationCredential
	}
	copy := append([]byte(nil), secret...)
	wipeBytes(secret)
	return copy, nil
}

func validGeneratorRevocationCredential(token []byte) bool {
	if len(token) < len("github_pat_")+20 || len(token) > maxGeneratorRevocationCredentialSize || !bytes.HasPrefix(token, []byte("github_pat_")) {
		return false
	}
	for _, character := range token {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}
