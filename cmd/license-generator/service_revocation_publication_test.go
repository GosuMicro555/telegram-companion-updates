package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	keyring "github.com/zalando/go-keyring"

	"telegram-companion/internal/licenseissuer"
	"telegram-companion/internal/revocation"
)

func TestGeneratorServiceConfiguresCredentialWithoutExposingIt(t *testing.T) {
	service, _, _ := revocationReadyService(t)
	defer service.Close()
	store := &memoryGeneratorSecretStore{getErr: keyring.ErrNotFound}
	credentials, _ := newRevocationCredentials(store)
	factory := &recordingGeneratorPublisherFactory{}
	if err := service.AttachRevocationPublication(context.Background(), credentials, factory); err != nil {
		t.Fatalf("AttachRevocationPublication() error = %v", err)
	}
	if service.Status().RevocationCredentialConfigured {
		t.Fatal("missing credential reported configured")
	}
	store.getErr = nil
	token := "github_pat_" + strings.Repeat("k", 82)
	if err := service.ConfigureRevocationCredential(token); err != nil {
		t.Fatalf("ConfigureRevocationCredential() error = %v", err)
	}
	if !service.Status().RevocationCredentialConfigured || factory.newCalls != 1 || string(store.secret) != token {
		t.Fatalf("credential status = %#v, factory calls = %d", service.Status(), factory.newCalls)
	}
	encoded, _ := json.Marshal(service.Status())
	if bytes.Contains(encoded, []byte(token)) || bytes.Contains(encoded, store.secret) {
		t.Fatal("Generator status exposes the GitHub credential")
	}
}

func TestGeneratorServiceCredentialAndPublisherReplacementIsTransactional(t *testing.T) {
	service, _, _ := revocationReadyService(t)
	defer service.Close()
	oldToken := "github_pat_" + strings.Repeat("o", 82)
	newToken := "github_pat_" + strings.Repeat("x", 82)
	store := &memoryGeneratorSecretStore{secret: []byte(oldToken)}
	credentials, _ := newRevocationCredentials(store)
	factory := &recordingGeneratorPublisherFactory{}
	if err := service.AttachRevocationPublication(context.Background(), credentials, factory); err != nil {
		t.Fatalf("AttachRevocationPublication() error = %v", err)
	}
	previous := service.revocationPublisher
	factory.newErr = errors.New("publisher creation failed with secret context")
	if err := service.ConfigureRevocationCredential(newToken); !errors.Is(err, ErrGeneratorRevocationUnavailable) {
		t.Fatalf("ConfigureRevocationCredential() error = %v", err)
	}
	if string(store.secret) != oldToken || service.revocationPublisher != previous || !service.Status().RevocationCredentialConfigured {
		t.Fatalf("failed replacement state: stored=%t publisher-preserved=%t status=%#v", string(store.secret) == oldToken, service.revocationPublisher == previous, service.Status())
	}
}

func TestGeneratorServiceFailedFirstCredentialDoesNotLeaveConfiguredState(t *testing.T) {
	service, _, _ := revocationReadyService(t)
	defer service.Close()
	store := &memoryGeneratorSecretStore{getErr: keyring.ErrNotFound}
	credentials, _ := newRevocationCredentials(store)
	factory := &recordingGeneratorPublisherFactory{newErr: errors.New("publisher unavailable")}
	if err := service.AttachRevocationPublication(context.Background(), credentials, factory); err != nil {
		t.Fatalf("AttachRevocationPublication() error = %v", err)
	}
	store.getErr = nil
	if err := service.ConfigureRevocationCredential("github_pat_" + strings.Repeat("y", 82)); !errors.Is(err, ErrGeneratorRevocationUnavailable) {
		t.Fatalf("ConfigureRevocationCredential() error = %v", err)
	}
	if len(store.secret) != 0 || service.revocationPublisher != nil || service.Status().RevocationCredentialConfigured {
		t.Fatalf("failed first configuration left partial state: secret=%d status=%#v", len(store.secret), service.Status())
	}
}

func TestGeneratorServiceRevokeResolvesLocalLicenseIDAndReturnsPublishedRow(t *testing.T) {
	service, _, revocationRepository := revocationReadyService(t)
	defer service.Close()
	store := &memoryGeneratorSecretStore{secret: []byte("github_pat_" + strings.Repeat("m", 82))}
	credentials, _ := newRevocationCredentials(store)
	factory := &recordingGeneratorPublisherFactory{outcome: revocation.PublicationResult{State: revocation.PublicationPublished, Sequence: 1, BlobID: strings.Repeat("a", 40)}}
	if err := service.AttachRevocationPublication(context.Background(), credentials, factory); err != nil {
		t.Fatalf("AttachRevocationPublication() error = %v", err)
	}
	licenseID := service.state.History[0].LicenseID
	row, err := service.RevokeLicense(licenseID)
	if err != nil {
		t.Fatalf("RevokeLicense() error = %v", err)
	}
	if row.LicenseID != licenseID || row.RevocationState != licenseRevocationPublished || row.RevokedAt == "" || factory.publisher.calls != 1 || factory.publisher.licenseID != licenseID {
		t.Fatalf("RevokeLicense() row = %#v, publisher = %#v", row, factory.publisher)
	}
	if len(revocationRepository.state.Records) != 1 || revocationRepository.state.Records[0].State != revocationPublicationPublished {
		t.Fatalf("durable publication record = %#v", revocationRepository.state.Records)
	}
	if _, err := service.RevokeLicense("unknown-license"); !errors.Is(err, ErrUnknownGeneratorLicense) {
		t.Fatalf("RevokeLicense(unknown) error = %v, want ErrUnknownGeneratorLicense", err)
	}
	if factory.publisher.calls != 1 {
		t.Fatal("unknown LicenseID reached publisher")
	}
}

func TestGeneratorServiceReturnsTruthfulPendingAndFailedRows(t *testing.T) {
	for name, test := range map[string]struct {
		outcome revocation.PublicationResult
		err     error
		state   string
		wantErr error
	}{
		"pending": {
			outcome: revocation.PublicationResult{State: revocation.PublicationPending, Sequence: 2, BlobID: strings.Repeat("b", 40)},
			err:     revocation.ErrPublicationPending,
			state:   licenseRevocationPending,
			wantErr: ErrGeneratorRevocationPending,
		},
		"failed": {
			outcome: revocation.PublicationResult{State: revocation.PublicationFailed},
			err:     revocation.ErrPublicationFailed,
			state:   licenseRevocationFailed,
			wantErr: ErrGeneratorRevocationFailed,
		},
	} {
		t.Run(name, func(t *testing.T) {
			service, _, _ := revocationReadyService(t)
			defer service.Close()
			credentials, _ := newRevocationCredentials(&memoryGeneratorSecretStore{secret: []byte("github_pat_" + strings.Repeat("n", 82))})
			factory := &recordingGeneratorPublisherFactory{outcome: test.outcome, err: test.err}
			if err := service.AttachRevocationPublication(context.Background(), credentials, factory); err != nil {
				t.Fatalf("AttachRevocationPublication() error = %v", err)
			}
			row, err := service.RevokeLicense(service.state.History[0].LicenseID)
			if !errors.Is(err, test.wantErr) || row.RevocationState != test.state {
				t.Fatalf("RevokeLicense() = %#v, %v", row, err)
			}
		})
	}
}

func TestGeneratorServiceBlocksPublicationUntilBothBackupsConfirmed(t *testing.T) {
	service, _, _ := revocationReadyService(t)
	defer service.Close()
	service.revocationState.BackupConfirmed = false
	credentials, _ := newRevocationCredentials(&memoryGeneratorSecretStore{secret: []byte("github_pat_" + strings.Repeat("p", 82))})
	factory := &recordingGeneratorPublisherFactory{}
	if err := service.AttachRevocationPublication(context.Background(), credentials, factory); err != nil {
		t.Fatalf("AttachRevocationPublication() error = %v", err)
	}
	if _, err := service.RevokeLicense(service.state.History[0].LicenseID); !errors.Is(err, ErrGeneratorRevocationUnavailable) {
		t.Fatalf("RevokeLicense() error = %v, want ErrGeneratorRevocationUnavailable", err)
	}
	if factory.publisher.calls != 0 {
		t.Fatal("unconfirmed revocation key reached publisher")
	}
}

func TestGeneratorServiceInitializesSequenceZeroOnlyAfterBothBackupsConfirmed(t *testing.T) {
	service, _, _ := revocationReadyService(t)
	defer service.Close()
	credentials, _ := newRevocationCredentials(&memoryGeneratorSecretStore{secret: []byte("github_pat_" + strings.Repeat("r", 82))})
	factory := &recordingGeneratorPublisherFactory{}
	if err := service.AttachRevocationPublication(context.Background(), credentials, factory); err != nil {
		t.Fatalf("AttachRevocationPublication() error = %v", err)
	}
	service.revocationState.BackupConfirmed = false
	if err := service.InitializeRevocationManifest(context.Background()); !errors.Is(err, ErrGeneratorRevocationUnavailable) || factory.publisher.initializeCalls != 0 {
		t.Fatalf("InitializeRevocationManifest(unconfirmed) = %v, calls=%d", err, factory.publisher.initializeCalls)
	}
	service.revocationState.BackupConfirmed = true
	if err := service.InitializeRevocationManifest(context.Background()); err != nil || factory.publisher.initializeCalls != 1 {
		t.Fatalf("InitializeRevocationManifest(confirmed) = %v, calls=%d", err, factory.publisher.initializeCalls)
	}
}

func TestGeneratorServiceCloseDoesNotInvertPublisherAndRegistryLocks(t *testing.T) {
	service, _, _ := revocationReadyService(t)
	publisher := &lockingGeneratorPublisher{
		registry:    service,
		entered:     make(chan struct{}),
		proceed:     make(chan struct{}),
		closeCalled: make(chan struct{}),
	}
	service.revocationPublisher = publisher
	revokeDone := make(chan struct{})
	go func() {
		defer close(revokeDone)
		_, _ = publisher.Revoke(context.Background(), "license-lock-order")
	}()
	<-publisher.entered
	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		service.Close()
	}()
	<-publisher.closeCalled
	close(publisher.proceed)
	select {
	case <-revokeDone:
	case <-time.After(time.Second):
		t.Fatal("publisher remained blocked acquiring the service registry lock")
	}
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("service Close remained blocked acquiring the publisher lock")
	}
}

func TestGeneratorServiceInitializesMissingRevocationKeyOnlyAfterFactoryProvesRemoteAbsent(t *testing.T) {
	for name, remoteExists := range map[string]bool{"remote absent": false, "remote exists": true} {
		t.Run(name, func(t *testing.T) {
			repository := repositoryWithGeneratedState(t)
			repository.state.BackupConfirmed = true
			entry, err := licenseissuer.NewHistoryEntry(validPayload())
			if err != nil {
				t.Fatalf("NewHistoryEntry() error = %v", err)
			}
			repository.state.History = []licenseissuer.HistoryEntry{entry}
			revocationRepository := &memoryRevocationStateStore{loadErr: licenseissuer.ErrStateNotFound}
			service, err := newGeneratorServiceWithRevocation(repository, revocationRepository, revocationBootstrapRequireRestore, fixedNow, deterministicRandom())
			if err != nil {
				t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
			}
			defer service.Close()
			credentials, _ := newRevocationCredentials(&memoryGeneratorSecretStore{secret: []byte("github_pat_" + strings.Repeat("q", 82))})
			factory := &recordingGeneratorPublisherFactory{remoteExists: remoteExists}
			attachErr := service.AttachRevocationPublication(context.Background(), credentials, factory)
			if remoteExists {
				if !errors.Is(attachErr, ErrGeneratorRevocationRestoreRequired) || revocationRepository.saveCalls != 0 || !service.Status().RevocationRestoreRequired || service.Status().RevocationPublicationCode != revocationPublicationRemoteRestoreRequired || factory.newCalls != 0 || factory.absenceCalls != 1 || service.Status().LicensePublicKey == "" {
					t.Fatalf("remote-present state = %#v, saves = %d", service.Status(), revocationRepository.saveCalls)
				}
				return
			}
			if attachErr != nil || revocationRepository.saveCalls != 1 || service.Status().RevocationRestoreRequired || factory.newCalls != 1 || factory.absenceCalls != 1 || !validGeneratorRevocationState(revocationRepository.state) {
				t.Fatalf("remote-absent state = %#v, saves = %d, factories = %d", service.Status(), revocationRepository.saveCalls, factory.newCalls)
			}
		})
	}
}

func revocationReadyService(t *testing.T) (*generatorService, *memoryStateRepository, *memoryRevocationStateStore) {
	t.Helper()
	repository := repositoryWithGeneratedState(t)
	repository.state.BackupConfirmed = true
	entry, err := licenseissuer.NewHistoryEntry(validPayload())
	if err != nil {
		t.Fatalf("NewHistoryEntry() error = %v", err)
	}
	repository.state.History = []licenseissuer.HistoryEntry{entry}
	revocationRepository := &memoryRevocationStateStore{state: testGeneratorRevocationState(t)}
	revocationRepository.state.Records = []revocationPublicationRecord{}
	service, err := newGeneratorServiceWithRevocation(repository, revocationRepository, revocationBootstrapRequireRestore, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
	}
	return service, repository, revocationRepository
}

type recordingGeneratorPublisherFactory struct {
	remoteExists    bool
	remoteErr       error
	absenceCalls    int
	newErr          error
	outcome         revocation.PublicationResult
	err             error
	newCalls        int
	publisher       *recordingGeneratorPublisher
	restoreEvidence revocationRestoreEvidence
	restoreErr      error
	inspectCalls    int
}

func (factory *recordingGeneratorPublisherFactory) ProveRemoteAbsent(context.Context) (bool, error) {
	factory.absenceCalls++
	return !factory.remoteExists, factory.remoteErr
}

func (factory *recordingGeneratorPublisherFactory) InspectRemote(context.Context, ed25519.PublicKey) (revocationRestoreEvidence, error) {
	factory.inspectCalls++
	return factory.restoreEvidence, factory.restoreErr
}

func (factory *recordingGeneratorPublisherFactory) New(token []byte, publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey, registry revocation.PublicationRegistry, now func() time.Time) (generatorRevocationPublisher, error) {
	factory.newCalls++
	if factory.newErr != nil {
		return nil, factory.newErr
	}
	factory.publisher = &recordingGeneratorPublisher{registry: registry, now: now, outcome: factory.outcome, err: factory.err}
	return factory.publisher, nil
}

type recordingGeneratorPublisher struct {
	registry        revocation.PublicationRegistry
	now             func() time.Time
	outcome         revocation.PublicationResult
	err             error
	calls           int
	licenseID       string
	closed          bool
	initializeCalls int
}

func (publisher *recordingGeneratorPublisher) Initialize(context.Context) (revocation.PublicationResult, error) {
	publisher.initializeCalls++
	return revocation.PublicationResult{State: revocation.PublicationPublished, Sequence: 0, BlobID: strings.Repeat("d", 40)}, nil
}

func (publisher *recordingGeneratorPublisher) Revoke(ctx context.Context, licenseID string) (revocation.PublicationResult, error) {
	publisher.calls++
	publisher.licenseID = licenseID
	handle, err := revocation.DeriveHandle(licenseID)
	if err != nil {
		return revocation.PublicationResult{}, err
	}
	prepared, err := publisher.registry.Prepare(ctx, handle, publisher.now())
	if err != nil {
		return revocation.PublicationResult{}, err
	}
	outcome := publisher.outcome
	if outcome.State == "" {
		outcome = revocation.PublicationResult{State: revocation.PublicationPublished, Sequence: 1, BlobID: strings.Repeat("c", 40)}
	}
	if err := publisher.registry.RecordOutcome(ctx, prepared.EventID, outcome); err != nil {
		return outcome, err
	}
	return outcome, publisher.err
}

func (publisher *recordingGeneratorPublisher) Close() {
	publisher.closed = true
}

type lockingGeneratorPublisher struct {
	mu          sync.Mutex
	registry    revocation.PublicationRegistry
	entered     chan struct{}
	proceed     chan struct{}
	closeCalled chan struct{}
}

func (publisher *lockingGeneratorPublisher) Initialize(context.Context) (revocation.PublicationResult, error) {
	return revocation.PublicationResult{}, revocation.ErrPublicationFailed
}

func (publisher *lockingGeneratorPublisher) Revoke(ctx context.Context, licenseID string) (revocation.PublicationResult, error) {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	close(publisher.entered)
	<-publisher.proceed
	handle, _ := revocation.DeriveHandle(licenseID)
	_, err := publisher.registry.Prepare(ctx, handle, fixedNow())
	return revocation.PublicationResult{}, err
}

func (publisher *lockingGeneratorPublisher) Close() {
	close(publisher.closeCalled)
	publisher.mu.Lock()
	publisher.mu.Unlock()
}
