package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"telegram-companion/internal/licenseissuer"
	"telegram-companion/internal/revocation"
)

func TestGeneratorPublicationRegistryPersistsBeforeReturningAndReusesEvent(t *testing.T) {
	service, repository := serviceWithRevocationState(t)
	defer service.Close()
	handle, err := revocation.DeriveHandle("license-registry-one")
	if err != nil {
		t.Fatalf("DeriveHandle() error = %v", err)
	}

	prepared, err := service.Prepare(context.Background(), handle, fixedNow())
	if err != nil {
		t.Fatalf("Prepare(first) error = %v", err)
	}
	if repository.saveCalls != 1 || len(repository.state.Records) != 1 {
		t.Fatalf("durable records = %#v, saves = %d", repository.state.Records, repository.saveCalls)
	}
	if prepared.EventID == "" || prepared.Handle != handle || !prepared.RequestedAt.Equal(fixedNow()) || prepared.State != revocation.PublicationPending || prepared.MinimumSequence != 0 || len(prepared.PublishedHandles) != 0 {
		t.Fatalf("Prepare(first) = %#v", prepared)
	}
	if repository.state.Records[0].EventID != prepared.EventID || repository.state.Records[0].Handle != handle.String() {
		t.Fatalf("persisted event = %#v", repository.state.Records[0])
	}

	again, err := service.Prepare(context.Background(), handle, fixedNow().Add(time.Hour))
	if err != nil {
		t.Fatalf("Prepare(again) error = %v", err)
	}
	if again.EventID != prepared.EventID || !again.RequestedAt.Equal(prepared.RequestedAt) || repository.saveCalls != 1 {
		t.Fatalf("Prepare(again) = %#v, saves = %d", again, repository.saveCalls)
	}
}

func TestGeneratorPublicationRegistryRetainsPendingEvidenceAndPublishedHistory(t *testing.T) {
	service, repository := serviceWithRevocationState(t)
	defer service.Close()
	handle, err := revocation.DeriveHandle("license-registry-outcome")
	if err != nil {
		t.Fatalf("DeriveHandle() error = %v", err)
	}
	prepared, err := service.Prepare(context.Background(), handle, fixedNow())
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	pending := revocation.PublicationResult{State: revocation.PublicationPending, Sequence: 7, BlobID: "blob-pending-7"}
	if err := service.RecordOutcome(context.Background(), prepared.EventID, pending); err != nil {
		t.Fatalf("RecordOutcome(pending) error = %v", err)
	}
	retry, err := service.Prepare(context.Background(), handle, fixedNow().Add(time.Hour))
	if err != nil {
		t.Fatalf("Prepare(retry) error = %v", err)
	}
	if retry.State != revocation.PublicationPending || retry.PublishedSequence != 7 || retry.RemoteBlobID != "blob-pending-7" || retry.MinimumSequence != 7 || len(retry.PublishedHandles) != 0 {
		t.Fatalf("Prepare(retry) = %#v", retry)
	}

	published := revocation.PublicationResult{State: revocation.PublicationPublished, Sequence: 7, BlobID: "blob-published-7"}
	if err := service.RecordOutcome(context.Background(), prepared.EventID, published); err != nil {
		t.Fatalf("RecordOutcome(published) error = %v", err)
	}
	completed, err := service.Prepare(context.Background(), handle, fixedNow())
	if err != nil {
		t.Fatalf("Prepare(completed) error = %v", err)
	}
	if completed.State != revocation.PublicationPublished || completed.PublishedSequence != 7 || completed.RemoteBlobID != "blob-published-7" || completed.MinimumSequence != 7 || len(completed.PublishedHandles) != 1 || completed.PublishedHandles[0] != handle {
		t.Fatalf("Prepare(completed) = %#v", completed)
	}
	savesBeforeDowngrade := repository.saveCalls
	if err := service.RecordOutcome(context.Background(), prepared.EventID, revocation.PublicationResult{State: revocation.PublicationFailed}); err == nil {
		t.Fatal("RecordOutcome() downgraded a published event")
	}
	if repository.saveCalls != savesBeforeDowngrade || repository.state.Records[0].State != revocationPublicationPublished {
		t.Fatal("rejected downgrade mutated durable publication state")
	}
}

func TestGeneratorPublicationRegistryDoesNotMutateOnCancellationOrSaveFailure(t *testing.T) {
	service, repository := serviceWithRevocationState(t)
	defer service.Close()
	handle, err := revocation.DeriveHandle("license-registry-failure")
	if err != nil {
		t.Fatalf("DeriveHandle() error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Prepare(cancelled, handle, fixedNow()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Prepare(cancelled) error = %v, want context.Canceled", err)
	}
	if repository.saveCalls != 0 || len(repository.state.Records) != 0 {
		t.Fatal("cancelled Prepare() mutated durable state")
	}

	repository.saveErr = licenseissuer.ErrStateStorage
	if _, err := service.Prepare(context.Background(), handle, fixedNow()); !errors.Is(err, ErrGeneratorStorage) {
		t.Fatalf("Prepare(save failure) error = %v, want ErrGeneratorStorage", err)
	}
	if len(repository.state.Records) != 0 {
		t.Fatal("failed Prepare() mutated durable state")
	}
	repository.saveErr = nil
	if _, err := service.Prepare(context.Background(), handle, fixedNow()); err != nil {
		t.Fatalf("Prepare(after failure) error = %v", err)
	}
	if len(repository.state.Records) != 1 {
		t.Fatal("Prepare(after failure) did not persist exactly one event")
	}
}

func serviceWithRevocationState(t *testing.T) (*generatorService, *memoryRevocationStateStore) {
	t.Helper()
	revocationRepository := &memoryRevocationStateStore{state: testGeneratorRevocationState(t)}
	revocationRepository.state.BackupConfirmed = false
	revocationRepository.state.Records = []revocationPublicationRecord{}
	service, err := newGeneratorServiceWithRevocation(
		repositoryWithGeneratedState(t),
		revocationRepository,
		revocationBootstrapRequireRestore,
		fixedNow,
		deterministicRandom(),
	)
	if err != nil {
		t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
	}
	return service, revocationRepository
}

type memoryRevocationStateStore struct {
	state     generatorRevocationState
	loadErr   error
	saveErr   error
	saveCalls int
}

func (repository *memoryRevocationStateStore) Load() (generatorRevocationState, error) {
	if repository.loadErr != nil {
		return generatorRevocationState{}, repository.loadErr
	}
	return cloneGeneratorRevocationState(repository.state), nil
}

func (repository *memoryRevocationStateStore) Save(state generatorRevocationState) error {
	repository.saveCalls++
	if repository.saveErr != nil {
		return repository.saveErr
	}
	wipeBytes(repository.state.PrivateKey)
	repository.state = cloneGeneratorRevocationState(state)
	repository.loadErr = nil
	return nil
}
