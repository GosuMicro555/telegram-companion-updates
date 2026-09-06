package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"time"

	"telegram-companion/internal/revocation"
)

const (
	ErrUnknownGeneratorLicense            GeneratorError = "unknown license"
	ErrGeneratorRevocationUnavailable     GeneratorError = "revocation unavailable"
	ErrGeneratorRevocationRestoreRequired GeneratorError = "revocation restore required"
	ErrGeneratorRevocationPending         GeneratorError = "revocation publication pending"
	ErrGeneratorRevocationFailed          GeneratorError = "revocation publication failed"
)

const (
	revocationPublicationCredentialRequired    = "credential_required"
	revocationPublicationReady                 = "ready"
	revocationPublicationRestoreRequired       = "restore_required"
	revocationPublicationRemoteRestoreRequired = "remote_restore_required"
	revocationPublicationRestartRequired       = "restart_required"
	revocationPublicationUnavailable           = "unavailable"
)

type generatorRevocationPublisher interface {
	Initialize(context.Context) (revocation.PublicationResult, error)
	Revoke(context.Context, string) (revocation.PublicationResult, error)
	Close()
}

type generatorRevocationPublisherFactory interface {
	ProveRemoteAbsent(context.Context) (bool, error)
	InspectRemote(context.Context, ed25519.PublicKey) (revocationRestoreEvidence, error)
	New([]byte, ed25519.PublicKey, ed25519.PrivateKey, revocation.PublicationRegistry, func() time.Time) (generatorRevocationPublisher, error)
}

func (service *generatorService) AttachRevocationPublication(ctx context.Context, credentials *revocationCredentials, factory generatorRevocationPublisherFactory) error {
	if service == nil || ctx == nil || credentials == nil || factory == nil {
		return ErrGeneratorRevocationUnavailable
	}
	configured, err := credentials.Configured(ctx)
	if err != nil {
		service.setRevocationPublicationCodeIfInactive(revocationPublicationUnavailable)
		return ErrGeneratorRevocationUnavailable
	}
	if !configured {
		service.mu.Lock()
		if service.revocationCredentials != nil || service.revocationPublisher != nil {
			service.mu.Unlock()
			return ErrGeneratorRevocationUnavailable
		}
		service.revocationCredentials = credentials
		service.revocationFactory = factory
		service.revocationCredentialConfigured = false
		service.revocationPublicationCode = revocationPublicationCredentialRequired
		service.mu.Unlock()
		return nil
	}
	token, err := credentials.Load(ctx)
	if err != nil {
		service.setRevocationPublicationCodeIfInactive(revocationPublicationUnavailable)
		return ErrGeneratorRevocationUnavailable
	}
	defer wipeBytes(token)
	publisher, err := service.prepareRevocationPublisher(ctx, token, factory)
	if err != nil {
		if errors.Is(err, ErrGeneratorRevocationRestoreRequired) {
			service.mu.Lock()
			if service.revocationCredentials == nil && service.revocationFactory == nil && service.revocationPublisher == nil {
				service.revocationCredentials = credentials
				service.revocationFactory = factory
				service.revocationCredentialConfigured = true
			}
			service.mu.Unlock()
		}
		return err
	}
	previous := service.installRevocationPublication(credentials, factory, publisher, true)
	if previous != nil {
		previous.Close()
	}
	return nil
}

func (service *generatorService) ConfigureRevocationCredential(token string) error {
	if service == nil {
		return ErrGeneratorRevocationUnavailable
	}
	service.mu.Lock()
	credentials := service.revocationCredentials
	factory := service.revocationFactory
	service.mu.Unlock()
	if credentials == nil || factory == nil {
		return ErrGeneratorRevocationUnavailable
	}
	ctx := context.Background()
	secret := []byte(token)
	defer wipeBytes(secret)
	publisher, err := service.prepareRevocationPublisher(ctx, secret, factory)
	if err != nil {
		return err
	}
	if err := credentials.Configure(ctx, token); err != nil {
		publisher.Close()
		return ErrGeneratorRevocationUnavailable
	}
	previous := service.installRevocationPublication(credentials, factory, publisher, true)
	if previous != nil {
		previous.Close()
	}
	return nil
}

func (service *generatorService) prepareRevocationPublisher(ctx context.Context, token []byte, factory generatorRevocationPublisherFactory) (generatorRevocationPublisher, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	service.mu.Lock()
	if service.revocationRepository == nil || factory == nil {
		service.mu.Unlock()
		return nil, ErrGeneratorRevocationUnavailable
	}
	restoreRequired := service.revocationRestore
	service.mu.Unlock()

	if restoreRequired {
		absent, err := factory.ProveRemoteAbsent(ctx)
		if err != nil {
			service.setRevocationPublicationCodeIfInactive(revocationPublicationUnavailable)
			return nil, ErrGeneratorRevocationUnavailable
		}
		if !absent {
			service.mu.Lock()
			service.revocationPublicationCode = revocationPublicationRemoteRestoreRequired
			service.mu.Unlock()
			return nil, ErrGeneratorRevocationRestoreRequired
		}
		service.mu.Lock()
		if service.revocationRestore {
			publicKey, privateKey, err := ed25519.GenerateKey(service.random)
			if err != nil {
				service.mu.Unlock()
				return nil, ErrGeneratorRevocationUnavailable
			}
			state := generatorRevocationState{
				PrivateKey: privateKey,
				PublicKey:  publicKey,
				Records:    []revocationPublicationRecord{},
			}
			if err := service.revocationRepository.Save(state); err != nil {
				wipeBytes(privateKey)
				service.mu.Unlock()
				return nil, ErrGeneratorStorage
			}
			service.revocationState = state
			service.revocationRestore = false
		}
		service.mu.Unlock()
	}

	service.mu.Lock()
	if !validGeneratorRevocationState(service.revocationState) {
		service.mu.Unlock()
		return nil, ErrGeneratorRevocationUnavailable
	}
	publisher, err := factory.New(token, service.revocationState.PublicKey, service.revocationState.PrivateKey, service, service.now)
	if err != nil || publisher == nil {
		service.mu.Unlock()
		service.setRevocationPublicationCodeIfInactive(revocationPublicationUnavailable)
		return nil, ErrGeneratorRevocationUnavailable
	}
	service.mu.Unlock()
	return publisher, nil
}

func (service *generatorService) installRevocationPublication(credentials *revocationCredentials, factory generatorRevocationPublisherFactory, publisher generatorRevocationPublisher, configured bool) generatorRevocationPublisher {
	service.mu.Lock()
	defer service.mu.Unlock()
	previous := service.revocationPublisher
	service.revocationCredentials = credentials
	service.revocationFactory = factory
	service.revocationPublisher = publisher
	service.revocationCredentialConfigured = configured
	service.revocationPublicationCode = revocationPublicationReady
	return previous
}

func (service *generatorService) setRevocationPublicationCodeIfInactive(code string) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.revocationPublisher == nil {
		service.revocationPublicationCode = code
	}
}

func (service *generatorService) RevokeLicense(licenseID string) (LicenseRow, error) {
	return service.revokeLicense(context.Background(), licenseID)
}

func (service *generatorService) RetryRevocation(licenseID string) (LicenseRow, error) {
	return service.revokeLicense(context.Background(), licenseID)
}

func (service *generatorService) InitializeRevocationManifest(ctx context.Context) error {
	if service == nil || ctx == nil {
		return ErrGeneratorRevocationUnavailable
	}
	service.mu.Lock()
	if !service.state.BackupConfirmed || !validGeneratorRevocationState(service.revocationState) || !service.revocationState.BackupConfirmed || !service.revocationCredentialConfigured || service.revocationPublisher == nil {
		service.mu.Unlock()
		return ErrGeneratorRevocationUnavailable
	}
	publisher := service.revocationPublisher
	service.mu.Unlock()
	result, err := publisher.Initialize(ctx)
	switch {
	case err == nil && result.State == revocation.PublicationPublished && validGeneratorGitHubBlobID(result.BlobID):
		return nil
	case errors.Is(err, revocation.ErrPublicationPending) || result.State == revocation.PublicationPending:
		return ErrGeneratorRevocationPending
	default:
		return ErrGeneratorRevocationFailed
	}
}

func (service *generatorService) revokeLicense(ctx context.Context, licenseID string) (LicenseRow, error) {
	if service == nil || ctx == nil {
		return LicenseRow{}, ErrGeneratorRevocationUnavailable
	}
	service.mu.Lock()
	var entryFound bool
	for _, entry := range service.state.History {
		if entry.LicenseID == licenseID {
			entryFound = true
			break
		}
	}
	if !entryFound {
		service.mu.Unlock()
		return LicenseRow{}, ErrUnknownGeneratorLicense
	}
	if !service.state.BackupConfirmed || !validGeneratorRevocationState(service.revocationState) || !service.revocationState.BackupConfirmed || !service.revocationCredentialConfigured || service.revocationPublisher == nil {
		row := service.licenseRowByIDLocked(licenseID)
		service.mu.Unlock()
		return row, ErrGeneratorRevocationUnavailable
	}
	publisher := service.revocationPublisher
	service.mu.Unlock()

	result, publishErr := publisher.Revoke(ctx, licenseID)
	service.mu.Lock()
	row := service.licenseRowByIDLocked(licenseID)
	service.mu.Unlock()
	switch {
	case publishErr == nil && result.State == revocation.PublicationPublished:
		return row, nil
	case errors.Is(publishErr, revocation.ErrPublicationPending) || result.State == revocation.PublicationPending:
		return row, ErrGeneratorRevocationPending
	default:
		return row, ErrGeneratorRevocationFailed
	}
}

func (service *generatorService) licenseRowByIDLocked(licenseID string) LicenseRow {
	for _, entry := range service.state.History {
		if entry.LicenseID == licenseID {
			return service.licenseRowForEntryLocked(entry)
		}
	}
	return LicenseRow{}
}
