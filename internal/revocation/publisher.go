package revocation

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	MaximumPublishAttempts = 8
	AnonymousReadAttempts  = 3
)

var (
	ErrPublicationPending     = errors.New("revocation: publication pending")
	ErrPublicationFailed      = errors.New("revocation: publication failed")
	ErrCompareAndSwapConflict = errors.New("revocation: compare-and-swap conflict")
)

type PublicationState string

const (
	PublicationPending   PublicationState = "pending"
	PublicationPublished PublicationState = "published"
	PublicationFailed    PublicationState = "failed"
)

type PublicationResult struct {
	State    PublicationState
	Sequence uint64
	BlobID   string
}

// PreparedRevocation is durable registry state returned only after the local
// event has been persisted. PublishedHandles is the append-only history that
// every acceptable remote baseline must contain.
type PreparedRevocation struct {
	EventID           string
	Handle            Handle
	RequestedAt       time.Time
	State             PublicationState
	PublishedSequence uint64
	RemoteBlobID      string
	MinimumSequence   uint64
	PublishedHandles  []Handle
}

type PublicationRegistry interface {
	Prepare(context.Context, Handle, time.Time) (PreparedRevocation, error)
	RecordOutcome(context.Context, string, PublicationResult) error
}

type RemoteFile struct {
	Bytes  []byte
	BlobID string
}

type PublishTransport interface {
	LoadAuthenticated(context.Context) (RemoteFile, error)
	CompareAndSwap(context.Context, string, []byte) (RemoteFile, error)
	LoadAnonymous(context.Context, string) ([]byte, error)
}

// Publisher owns initialization, signature verification, append-only checks,
// CAS publication, conflict reconciliation, and authenticated/anonymous
// read-back. Callers provide only a License ID.
type Publisher struct {
	mu         sync.Mutex
	keyID      string
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
	registry   PublicationRegistry
	transport  PublishTransport
	now        func() time.Time
	closed     bool
}

func NewPublisher(keyID string, publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey, registry PublicationRegistry, transport PublishTransport, now func() time.Time) (*Publisher, error) {
	if !validKeyID(keyID) || len(publicKey) != ed25519.PublicKeySize || len(privateKey) != ed25519.PrivateKeySize || registry == nil || transport == nil || now == nil {
		return nil, ErrPublicationFailed
	}
	derivedPublic := privateKey.Public().(ed25519.PublicKey)
	if !bytes.Equal(publicKey, derivedPublic) {
		return nil, ErrPublicationFailed
	}
	return &Publisher{
		keyID:      keyID,
		publicKey:  bytes.Clone(publicKey),
		privateKey: bytes.Clone(privateKey),
		registry:   registry,
		transport:  transport,
		now:        now,
	}, nil
}

func (publisher *Publisher) Close() {
	if publisher == nil {
		return
	}
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	for index := range publisher.privateKey {
		publisher.privateKey[index] = 0
	}
	publisher.closed = true
}

func (publisher *Publisher) Initialize(ctx context.Context) (PublicationResult, error) {
	if publisher == nil {
		return failedPublication(), ErrPublicationFailed
	}
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if !publisher.usable() {
		return failedPublication(), ErrPublicationFailed
	}
	return publisher.initializeLocked(ctx)
}

func (publisher *Publisher) Revoke(ctx context.Context, licenseID string) (PublicationResult, error) {
	if publisher == nil {
		return failedPublication(), ErrPublicationFailed
	}
	handle, err := DeriveHandle(licenseID)
	if err != nil {
		return failedPublication(), ErrPublicationFailed
	}
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if !publisher.usable() || ctx == nil {
		return failedPublication(), ErrPublicationFailed
	}
	requestedAt := canonicalPublisherTime(publisher.now())
	prepared, err := publisher.registry.Prepare(ctx, handle, requestedAt)
	if err != nil || !validPreparedRevocation(prepared, handle) {
		return failedPublication(), ErrPublicationFailed
	}
	if prepared.State == PublicationPublished {
		return PublicationResult{State: PublicationPublished, Sequence: prepared.PublishedSequence, BlobID: prepared.RemoteBlobID}, nil
	}

	result, publishErr := publisher.publishPreparedLocked(ctx, prepared)
	if recordErr := publisher.registry.RecordOutcome(ctx, prepared.EventID, result); recordErr != nil {
		return PublicationResult{State: PublicationPending, Sequence: result.Sequence, BlobID: result.BlobID}, ErrPublicationPending
	}
	return result, publishErr
}

func (publisher *Publisher) initializeLocked(ctx context.Context) (PublicationResult, error) {
	if ctx == nil {
		return failedPublication(), ErrPublicationFailed
	}
	for attempt := 0; attempt < MaximumPublishAttempts; attempt++ {
		remote, err := publisher.transport.LoadAuthenticated(ctx)
		if err != nil {
			return pendingPublication(0, ""), ErrPublicationPending
		}
		if !missingRemote(remote) {
			manifest, verifyErr := publisher.verifyRemote(remote)
			if verifyErr != nil {
				return failedPublication(), ErrPublicationFailed
			}
			return publisher.verifyReadback(ctx, remote, manifest, Handle{}, nil, false)
		}

		payload := Payload{
			Schema:      ManifestSchema,
			KeyID:       publisher.keyID,
			Sequence:    0,
			GeneratedAt: canonicalPublisherTime(publisher.now()).Format(manifestTimeLayout),
			Entries:     []Entry{},
		}
		envelope, signErr := Sign(payload, publisher.privateKey)
		if signErr != nil {
			return failedPublication(), ErrPublicationFailed
		}
		stored, casErr := publisher.transport.CompareAndSwap(ctx, "", []byte(envelope))
		if casErr != nil {
			if errors.Is(casErr, ErrCompareAndSwapConflict) {
				continue
			}
			return pendingPublication(0, ""), ErrPublicationPending
		}
		manifest, verifyErr := publisher.verifyStoredCandidate(stored, []byte(envelope), 0)
		if verifyErr != nil {
			return failedPublication(), ErrPublicationFailed
		}
		return publisher.verifyReadback(ctx, stored, manifest, Handle{}, nil, false)
	}
	return pendingPublication(0, ""), ErrPublicationPending
}

func (publisher *Publisher) publishPreparedLocked(ctx context.Context, prepared PreparedRevocation) (PublicationResult, error) {
	history := canonicalHandles(prepared.PublishedHandles)
	for attempt := 0; attempt < MaximumPublishAttempts; attempt++ {
		remote, err := publisher.transport.LoadAuthenticated(ctx)
		if err != nil {
			return pendingPublication(prepared.MinimumSequence, ""), ErrPublicationPending
		}
		if missingRemote(remote) {
			initialized, initializeErr := publisher.initializeLocked(ctx)
			if initializeErr != nil {
				return initialized, initializeErr
			}
			continue
		}

		manifest, verifyErr := publisher.verifyRemote(remote)
		if verifyErr != nil || !acceptableHistory(manifest, prepared.MinimumSequence, history) {
			return failedPublication(), ErrPublicationFailed
		}
		if manifest.Contains(prepared.Handle) {
			return publisher.verifyReadback(ctx, remote, manifest, prepared.Handle, history, true)
		}

		generatedAt := canonicalPublisherTime(publisher.now())
		currentGeneratedAt, ok := parseManifestTime(manifest.payload.GeneratedAt)
		if !ok {
			return failedPublication(), ErrPublicationFailed
		}
		if currentGeneratedAt.After(generatedAt) {
			generatedAt = currentGeneratedAt
		}
		if prepared.RequestedAt.After(generatedAt) {
			generatedAt = prepared.RequestedAt
		}
		envelope, buildErr := buildNextAt(manifest, prepared.Handle, prepared.RequestedAt, generatedAt, publisher.privateKey)
		if buildErr != nil {
			return failedPublication(), ErrPublicationFailed
		}
		expectedSequence := manifest.Sequence() + 1
		stored, casErr := publisher.transport.CompareAndSwap(ctx, remote.BlobID, []byte(envelope))
		if casErr != nil {
			if errors.Is(casErr, ErrCompareAndSwapConflict) {
				continue
			}
			return pendingPublication(prepared.MinimumSequence, ""), ErrPublicationPending
		}
		storedManifest, storedErr := publisher.verifyStoredCandidate(stored, []byte(envelope), expectedSequence)
		if storedErr != nil || !acceptableHistory(storedManifest, expectedSequence, append(history, prepared.Handle)) {
			return failedPublication(), ErrPublicationFailed
		}
		return publisher.verifyReadback(ctx, stored, storedManifest, prepared.Handle, history, true)
	}
	return pendingPublication(prepared.MinimumSequence, ""), ErrPublicationPending
}

func (publisher *Publisher) verifyReadback(ctx context.Context, expected RemoteFile, expectedManifest VerifiedManifest, target Handle, history []Handle, requireTarget bool) (PublicationResult, error) {
	authenticated, err := publisher.transport.LoadAuthenticated(ctx)
	if err != nil {
		return pendingPublication(expectedManifest.Sequence(), expected.BlobID), ErrPublicationPending
	}
	manifest, verifyErr := publisher.verifyRemote(authenticated)
	if verifyErr != nil || manifest.Sequence() < expectedManifest.Sequence() || !acceptableHistory(manifest, expectedManifest.Sequence(), history) || (requireTarget && !manifest.Contains(target)) {
		return failedPublication(), ErrPublicationFailed
	}
	if manifest.Sequence() == expectedManifest.Sequence() && manifest.Digest() != expectedManifest.Digest() {
		return failedPublication(), ErrPublicationFailed
	}

	for attempt := 0; attempt < AnonymousReadAttempts; attempt++ {
		anonymous, anonymousErr := publisher.transport.LoadAnonymous(ctx, authenticated.BlobID)
		if anonymousErr != nil || !bytes.Equal(anonymous, authenticated.Bytes) {
			continue
		}
		anonymousManifest, anonymousVerifyErr := Verify(string(anonymous), publisher.keyID, publisher.publicKey)
		if anonymousVerifyErr == nil && anonymousManifest.Digest() == manifest.Digest() {
			return PublicationResult{State: PublicationPublished, Sequence: manifest.Sequence(), BlobID: authenticated.BlobID}, nil
		}
	}
	return pendingPublication(manifest.Sequence(), authenticated.BlobID), ErrPublicationPending
}

func (publisher *Publisher) verifyStoredCandidate(stored RemoteFile, proposed []byte, expectedSequence uint64) (VerifiedManifest, error) {
	if !bytes.Equal(stored.Bytes, proposed) {
		return VerifiedManifest{}, ErrPublicationFailed
	}
	manifest, err := publisher.verifyRemote(stored)
	if err != nil || manifest.Sequence() != expectedSequence {
		return VerifiedManifest{}, ErrPublicationFailed
	}
	return manifest, nil
}

func (publisher *Publisher) verifyRemote(remote RemoteFile) (VerifiedManifest, error) {
	if len(remote.Bytes) == 0 || len(remote.Bytes) > MaxEnvelopeBytes || !validBlobID(remote.BlobID) {
		return VerifiedManifest{}, ErrPublicationFailed
	}
	return Verify(string(remote.Bytes), publisher.keyID, publisher.publicKey)
}

func (publisher *Publisher) usable() bool {
	return !publisher.closed && len(publisher.privateKey) == ed25519.PrivateKeySize && publisher.registry != nil && publisher.transport != nil && publisher.now != nil
}

func validPreparedRevocation(prepared PreparedRevocation, expected Handle) bool {
	if prepared.EventID == "" || len(prepared.EventID) > 256 || strings.TrimSpace(prepared.EventID) != prepared.EventID || prepared.Handle != expected || !canonicalStateTime(prepared.RequestedAt) || len(prepared.PublishedHandles) > MaxEntries {
		return false
	}
	switch prepared.State {
	case PublicationPending:
		if prepared.PublishedSequence == 0 {
			return prepared.RemoteBlobID == ""
		}
		return prepared.PublishedSequence >= prepared.MinimumSequence && validBlobID(prepared.RemoteBlobID)
	case PublicationFailed:
		return prepared.PublishedSequence == 0 && prepared.RemoteBlobID == ""
	case PublicationPublished:
		return prepared.PublishedSequence > 0 && validBlobID(prepared.RemoteBlobID)
	default:
		return false
	}
}

func acceptableHistory(manifest VerifiedManifest, minimumSequence uint64, history []Handle) bool {
	if !manifest.valid() || manifest.Sequence() < minimumSequence {
		return false
	}
	for _, handle := range history {
		if !manifest.Contains(handle) {
			return false
		}
	}
	return true
}

func missingRemote(remote RemoteFile) bool {
	return len(remote.Bytes) == 0 && remote.BlobID == ""
}

func validBlobID(blobID string) bool {
	if blobID == "" || len(blobID) > 256 {
		return false
	}
	for index := 0; index < len(blobID); index++ {
		if blobID[index] < 0x21 || blobID[index] > 0x7e {
			return false
		}
	}
	return true
}

func canonicalPublisherTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Second)
}

func pendingPublication(sequence uint64, blobID string) PublicationResult {
	return PublicationResult{State: PublicationPending, Sequence: sequence, BlobID: blobID}
}

func failedPublication() PublicationResult {
	return PublicationResult{State: PublicationFailed}
}
