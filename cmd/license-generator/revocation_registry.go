package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"telegram-companion/internal/licenseissuer"
	"telegram-companion/internal/revocation"
)

type revocationBootstrapDecision uint8

const (
	revocationBootstrapRequireRestore revocationBootstrapDecision = iota
	revocationBootstrapRemoteAbsent
)

func newGeneratorServiceWithRevocation(repository stateRepository, revocationRepository revocationStateStore, decision revocationBootstrapDecision, now func() time.Time, random io.Reader) (*generatorService, error) {
	if revocationRepository == nil || (decision != revocationBootstrapRequireRestore && decision != revocationBootstrapRemoteAbsent) {
		return nil, ErrGeneratorInitialization
	}
	service, err := newGeneratorService(repository, now, random)
	if err != nil {
		return nil, err
	}
	state, err := revocationRepository.Load()
	if errors.Is(err, licenseissuer.ErrStateNotFound) {
		service.revocationRepository = revocationRepository
		if decision == revocationBootstrapRequireRestore {
			service.revocationRestore = true
			return service, nil
		}
		publicKey, privateKey, keyErr := ed25519.GenerateKey(random)
		if keyErr != nil {
			service.Close()
			return nil, ErrGeneratorInitialization
		}
		state = generatorRevocationState{
			PrivateKey: privateKey,
			PublicKey:  publicKey,
			Records:    []revocationPublicationRecord{},
		}
		if saveErr := revocationRepository.Save(state); saveErr != nil {
			wipeBytes(privateKey)
			service.Close()
			return nil, ErrGeneratorStorage
		}
	} else if err != nil {
		service.Close()
		return nil, ErrGeneratorStorage
	}
	if !validGeneratorRevocationState(state) {
		wipeBytes(state.PrivateKey)
		service.Close()
		return nil, ErrGeneratorInitialization
	}
	service.revocationRepository = revocationRepository
	service.revocationState = state
	return service, nil
}

func (service *generatorService) Prepare(ctx context.Context, handle revocation.Handle, requestedAt time.Time) (revocation.PreparedRevocation, error) {
	if service == nil || ctx == nil {
		return revocation.PreparedRevocation{}, ErrGeneratorInitialization
	}
	if err := ctx.Err(); err != nil {
		return revocation.PreparedRevocation{}, err
	}
	handleText := handle.String()
	if _, err := revocation.ParseHandle(handleText); err != nil || requestedAt.Location() != time.UTC || requestedAt.Nanosecond() != 0 || requestedAt.Format(time.RFC3339) != requestedAt.Format(time.RFC3339Nano) {
		return revocation.PreparedRevocation{}, ErrGeneratorInitialization
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return revocation.PreparedRevocation{}, err
	}
	if service.revocationRepository == nil || service.revocationRestore || !validGeneratorRevocationState(service.revocationState) {
		return revocation.PreparedRevocation{}, ErrGeneratorInitialization
	}
	if err := service.refreshRevocationStateLocked(); err != nil {
		return revocation.PreparedRevocation{}, ErrGeneratorStorage
	}
	for index := range service.revocationState.Records {
		if service.revocationState.Records[index].Handle == handleText {
			return preparedRevocation(service.revocationState, index)
		}
	}

	identifier := make([]byte, 16)
	if _, err := io.ReadFull(service.random, identifier); err != nil {
		return revocation.PreparedRevocation{}, ErrGeneratorInitialization
	}
	eventID := fmt.Sprintf("revocation-%s-%d", hex.EncodeToString(identifier), len(service.revocationState.Records)+1)
	next := service.revocationState
	next.Records = append(append([]revocationPublicationRecord(nil), service.revocationState.Records...), revocationPublicationRecord{
		EventID:     eventID,
		Handle:      handleText,
		RequestedAt: requestedAt.Format(time.RFC3339),
		State:       revocationPublicationPending,
	})
	if !validGeneratorRevocationState(next) {
		return revocation.PreparedRevocation{}, ErrGeneratorInitialization
	}
	if err := service.revocationRepository.Save(next); err != nil {
		return revocation.PreparedRevocation{}, ErrGeneratorStorage
	}
	service.revocationState.Records = next.Records
	return preparedRevocation(service.revocationState, len(service.revocationState.Records)-1)
}

func (service *generatorService) RecordOutcome(ctx context.Context, eventID string, outcome revocation.PublicationResult) error {
	if service == nil || ctx == nil {
		return ErrGeneratorInitialization
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if service.revocationRepository == nil || service.revocationRestore || !validGeneratorRevocationState(service.revocationState) {
		return ErrGeneratorInitialization
	}
	if err := service.refreshRevocationStateLocked(); err != nil {
		return ErrGeneratorStorage
	}
	index := -1
	for candidate := range service.revocationState.Records {
		if service.revocationState.Records[candidate].EventID == eventID {
			index = candidate
			break
		}
	}
	if index < 0 {
		return ErrGeneratorInitialization
	}
	current := service.revocationState.Records[index]
	nextRecord, ok := publicationRecordWithOutcome(current, outcome)
	if !ok {
		return ErrGeneratorInitialization
	}
	if current == nextRecord {
		return nil
	}
	if current.State == revocationPublicationPublished || outcome.Sequence < current.PublishedSequence {
		return ErrGeneratorInitialization
	}
	next := service.revocationState
	next.Records = append([]revocationPublicationRecord(nil), service.revocationState.Records...)
	next.Records[index] = nextRecord
	if !validGeneratorRevocationState(next) {
		return ErrGeneratorInitialization
	}
	if err := service.revocationRepository.Save(next); err != nil {
		return ErrGeneratorStorage
	}
	service.revocationState.Records = next.Records
	return nil
}

func (service *generatorService) refreshRevocationStateLocked() error {
	if service.revocationRepository == nil || service.revocationRestore || !validGeneratorRevocationState(service.revocationState) {
		return ErrGeneratorInitialization
	}
	loaded, err := service.revocationRepository.Load()
	if err != nil || !validGeneratorRevocationState(loaded) {
		wipeBytes(loaded.PrivateKey)
		return ErrGeneratorStorage
	}
	if !bytes.Equal(loaded.PublicKey, service.revocationState.PublicKey) {
		wipeBytes(loaded.PrivateKey)
		return ErrGeneratorKeyMismatch
	}
	wipeBytes(loaded.PrivateKey)
	service.revocationState.BackupConfirmed = loaded.BackupConfirmed
	service.revocationState.Records = make([]revocationPublicationRecord, len(loaded.Records))
	copy(service.revocationState.Records, loaded.Records)
	return nil
}

func preparedRevocation(state generatorRevocationState, target int) (revocation.PreparedRevocation, error) {
	if !validGeneratorRevocationState(state) || target < 0 || target >= len(state.Records) {
		return revocation.PreparedRevocation{}, ErrGeneratorInitialization
	}
	result := revocation.PreparedRevocation{PublishedHandles: []revocation.Handle{}}
	for index, record := range state.Records {
		handle, err := revocation.ParseHandle(record.Handle)
		if err != nil {
			return revocation.PreparedRevocation{}, ErrGeneratorInitialization
		}
		if record.PublishedSequence > result.MinimumSequence {
			result.MinimumSequence = record.PublishedSequence
		}
		if record.State == revocationPublicationPublished {
			result.PublishedHandles = append(result.PublishedHandles, handle)
		}
		if index != target {
			continue
		}
		requestedAt, err := time.Parse(time.RFC3339, record.RequestedAt)
		if err != nil {
			return revocation.PreparedRevocation{}, ErrGeneratorInitialization
		}
		result.EventID = record.EventID
		result.Handle = handle
		result.RequestedAt = requestedAt
		result.State = revocation.PublicationState(record.State)
		result.PublishedSequence = record.PublishedSequence
		result.RemoteBlobID = record.RemoteBlobID
	}
	return result, nil
}

func publicationRecordWithOutcome(record revocationPublicationRecord, outcome revocation.PublicationResult) (revocationPublicationRecord, bool) {
	switch outcome.State {
	case revocation.PublicationPending:
		record.State = revocationPublicationPending
	case revocation.PublicationFailed:
		record.State = revocationPublicationFailed
	case revocation.PublicationPublished:
		record.State = revocationPublicationPublished
	default:
		return revocationPublicationRecord{}, false
	}
	record.PublishedSequence = outcome.Sequence
	record.RemoteBlobID = outcome.BlobID
	return record, validRevocationPublicationRecord(record)
}

var _ revocation.PublicationRegistry = (*generatorService)(nil)
