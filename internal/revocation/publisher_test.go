package revocation

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestPublisherInitializesSequenceZeroThenPublishesOneAppend(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	registry := newMemoryPublicationRegistry()
	transport := newMemoryPublishTransport()
	transport.onNetwork = func() {
		if registry.prepareCalls() == 0 {
			t.Error("network mutation started before local event preparation")
		}
	}
	publisher := newPublisherForTest(t, publicKey, privateKey, registry, transport)

	result, err := publisher.Revoke(context.Background(), "license-publisher-first")
	if err != nil || result.State != PublicationPublished || result.Sequence != 1 || result.BlobID == "" {
		t.Fatalf("Revoke result=%#v err=%v", result, err)
	}
	if transport.casCallCount() != 2 {
		t.Fatalf("CAS calls = %d, want sequence-0 initialization plus sequence-1 append", transport.casCallCount())
	}
	manifest := verifyPublisherRemote(t, publicKey, transport.remoteFile())
	handle, _ := DeriveHandle("license-publisher-first")
	if manifest.Sequence() != 1 || !manifest.Contains(handle) {
		t.Fatalf("remote manifest sequence=%d contains=%v", manifest.Sequence(), manifest.Contains(handle))
	}
}

func TestPublisherInitializeIsIdempotent(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	registry := newMemoryPublicationRegistry()
	transport := newMemoryPublishTransport()
	publisher := newPublisherForTest(t, publicKey, privateKey, registry, transport)

	first, err := publisher.Initialize(context.Background())
	if err != nil || first.State != PublicationPublished || first.Sequence != 0 {
		t.Fatalf("first Initialize=%#v err=%v", first, err)
	}
	second, err := publisher.Initialize(context.Background())
	if err != nil || second.State != PublicationPublished || second.Sequence != 0 || second.BlobID != first.BlobID {
		t.Fatalf("second Initialize=%#v err=%v", second, err)
	}
	if transport.casCallCount() != 1 {
		t.Fatalf("CAS calls = %d, want 1", transport.casCallCount())
	}
}

func TestPublisherDuplicateClickAndAlreadyRemoteReconciliationAreIdempotent(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	registry := newMemoryPublicationRegistry()
	transport := newMemoryPublishTransport()
	transport.setRemote(publisherEnvelope(t, privateKey, testPayload(0, []Entry{})), "blob-0")
	publisher := newPublisherForTest(t, publicKey, privateKey, registry, transport)

	first, err := publisher.Revoke(context.Background(), "license-publisher-duplicate")
	if err != nil || first.State != PublicationPublished {
		t.Fatalf("first Revoke=%#v err=%v", first, err)
	}
	networkAfterFirst := transport.networkCallCount()
	second, err := publisher.Revoke(context.Background(), "license-publisher-duplicate")
	if err != nil || second != first {
		t.Fatalf("duplicate Revoke=%#v err=%v, want %#v", second, err, first)
	}
	if transport.networkCallCount() != networkAfterFirst {
		t.Fatal("published duplicate performed network I/O")
	}

	reconcileRegistry := newMemoryPublicationRegistry()
	handle, _ := DeriveHandle("license-already-remote")
	reconcileRegistry.seedPending(handle, fixedPublisherTime)
	payload := testPayload(7, sortedEntriesForTest(t, handle, fixedPublisherTime))
	payload.GeneratedAt = fixedPublisherTime.Format(manifestTimeLayout)
	reconcileTransport := newMemoryPublishTransport()
	reconcileTransport.setRemote(publisherEnvelope(t, privateKey, payload), "blob-7")
	reconcilePublisher := newPublisherForTest(t, publicKey, privateKey, reconcileRegistry, reconcileTransport)
	reconciled, err := reconcilePublisher.Revoke(context.Background(), "license-already-remote")
	if err != nil || reconciled.State != PublicationPublished || reconciled.Sequence != 7 || reconcileTransport.casCallCount() != 0 {
		t.Fatalf("reconciled=%#v err=%v CAS=%d", reconciled, err, reconcileTransport.casCallCount())
	}
}

func TestPublisherCASConflictRefetchesAndRebuilds(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	other, _ := DeriveHandle("license-concurrent-other")
	conflictPayload := testPayload(1, sortedEntriesForTest(t, other, fixedPublisherTime))
	conflictPayload.GeneratedAt = fixedPublisherTime.Add(time.Hour).Format(manifestTimeLayout)
	transport := newMemoryPublishTransport()
	transport.setRemote(publisherEnvelope(t, privateKey, testPayload(0, []Entry{})), "blob-0")
	transport.conflictOnce = publisherEnvelope(t, privateKey, conflictPayload)
	registry := newMemoryPublicationRegistry()
	publisher := newPublisherForTest(t, publicKey, privateKey, registry, transport)

	result, err := publisher.Revoke(context.Background(), "license-after-conflict")
	if err != nil || result.State != PublicationPublished || result.Sequence != 2 {
		t.Fatalf("Revoke=%#v err=%v", result, err)
	}
	manifest := verifyPublisherRemote(t, publicKey, transport.remoteFile())
	target, _ := DeriveHandle("license-after-conflict")
	if !manifest.Contains(other) || !manifest.Contains(target) || transport.casCallCount() != 2 {
		t.Fatalf("conflict reconciliation failed: sequence=%d CAS=%d", manifest.Sequence(), transport.casCallCount())
	}
}

func TestPublisherReconcilesAmbiguousUploadWithoutSequenceSkip(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	registry := newMemoryPublicationRegistry()
	transport := newMemoryPublishTransport()
	transport.setRemote(publisherEnvelope(t, privateKey, testPayload(0, []Entry{})), "blob-0")
	transport.ambiguousOnce = true
	publisher := newPublisherForTest(t, publicKey, privateKey, registry, transport)

	pendingWritten, err := publisher.Revoke(context.Background(), "license-ambiguous-written")
	if !errors.Is(err, ErrPublicationPending) || pendingWritten.State != PublicationPending || pendingWritten.Sequence != 0 || transport.casCallCount() != 1 {
		t.Fatalf("ambiguous first result=%#v err=%v CAS=%d", pendingWritten, err, transport.casCallCount())
	}
	result, err := publisher.Revoke(context.Background(), "license-ambiguous-written")
	if err != nil || result.State != PublicationPublished || result.Sequence != 1 || transport.casCallCount() != 1 {
		t.Fatalf("ambiguous retry reconciliation=%#v err=%v CAS=%d", result, err, transport.casCallCount())
	}

	pendingRegistry := newMemoryPublicationRegistry()
	pendingTransport := newMemoryPublishTransport()
	pendingTransport.setRemote(publisherEnvelope(t, privateKey, testPayload(0, []Entry{})), "blob-0")
	pendingTransport.failCAS = true
	pendingPublisher := newPublisherForTest(t, publicKey, privateKey, pendingRegistry, pendingTransport)
	pending, err := pendingPublisher.Revoke(context.Background(), "license-ambiguous-not-written")
	if !errors.Is(err, ErrPublicationPending) || pending.State != PublicationPending || pending.Sequence != 0 || pendingTransport.casCallCount() != 1 {
		t.Fatalf("pending=%#v err=%v", pending, err)
	}
	pendingTransport.failCAS = false
	published, err := pendingPublisher.Revoke(context.Background(), "license-ambiguous-not-written")
	if err != nil || published.State != PublicationPublished || published.Sequence != 1 {
		t.Fatalf("retry=%#v err=%v", published, err)
	}
	manifest := verifyPublisherRemote(t, publicKey, pendingTransport.remoteFile())
	if manifest.Sequence() != 1 {
		t.Fatalf("retry skipped sequence: %d", manifest.Sequence())
	}
}

func TestPublisherRejectsInvalidRollbackAndMissingHistory(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	history, _ := DeriveHandle("license-published-history")
	tests := []struct {
		name    string
		remote  string
		minimum uint64
		history []Handle
	}{
		{name: "invalid signature", remote: "TCREV1.invalid.invalid"},
		{name: "lower sequence", remote: publisherEnvelope(t, privateKey, testPayload(1, []Entry{})), minimum: 2},
		{name: "missing historical entry", remote: publisherEnvelope(t, privateKey, testPayload(2, []Entry{})), minimum: 2, history: []Handle{history}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := newMemoryPublicationRegistry()
			registry.minimumSequence = test.minimum
			registry.publishedHandles = append([]Handle(nil), test.history...)
			transport := newMemoryPublishTransport()
			transport.setRemote(test.remote, "blob-existing")
			publisher := newPublisherForTest(t, publicKey, privateKey, registry, transport)
			result, err := publisher.Revoke(context.Background(), "license-invalid-baseline")
			if !errors.Is(err, ErrPublicationFailed) || result.State != PublicationFailed || transport.casCallCount() != 0 {
				t.Fatalf("result=%#v err=%v CAS=%d", result, err, transport.casCallCount())
			}
		})
	}
}

func TestPublisherRequiresVerifiedAuthenticatedAndAnonymousReadback(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	t.Run("authenticated mismatch", func(t *testing.T) {
		registry := newMemoryPublicationRegistry()
		transport := newMemoryPublishTransport()
		transport.setRemote(publisherEnvelope(t, privateKey, testPayload(0, []Entry{})), "blob-0")
		other, _ := DeriveHandle("license-readback-other")
		sibling := testPayload(1, sortedEntriesForTest(t, other, fixedPublisherTime))
		sibling.GeneratedAt = fixedPublisherTime.Format(manifestTimeLayout)
		transport.authOverrideAfterCAS = publisherEnvelope(t, privateKey, sibling)
		publisher := newPublisherForTest(t, publicKey, privateKey, registry, transport)
		result, err := publisher.Revoke(context.Background(), "license-readback-target")
		if !errors.Is(err, ErrPublicationFailed) || result.State != PublicationFailed {
			t.Fatalf("result=%#v err=%v", result, err)
		}
	})

	t.Run("anonymous stale then current", func(t *testing.T) {
		registry := newMemoryPublicationRegistry()
		transport := newMemoryPublishTransport()
		sequence0 := publisherEnvelope(t, privateKey, testPayload(0, []Entry{}))
		transport.setRemote(sequence0, "blob-0")
		transport.staleAnonymous = sequence0
		transport.staleAnonymousReads = 1
		publisher := newPublisherForTest(t, publicKey, privateKey, registry, transport)
		result, err := publisher.Revoke(context.Background(), "license-anonymous-retry")
		if err != nil || result.State != PublicationPublished || transport.anonymousCallCount() != 2 {
			t.Fatalf("result=%#v err=%v anonymous=%d", result, err, transport.anonymousCallCount())
		}
	})

	t.Run("anonymous remains stale", func(t *testing.T) {
		registry := newMemoryPublicationRegistry()
		transport := newMemoryPublishTransport()
		sequence0 := publisherEnvelope(t, privateKey, testPayload(0, []Entry{}))
		transport.setRemote(sequence0, "blob-0")
		transport.staleAnonymous = sequence0
		transport.staleAnonymousReads = 100
		publisher := newPublisherForTest(t, publicKey, privateKey, registry, transport)
		result, err := publisher.Revoke(context.Background(), "license-anonymous-stale")
		if !errors.Is(err, ErrPublicationPending) || result.State != PublicationPending {
			t.Fatalf("result=%#v err=%v", result, err)
		}
		casCalls := transport.casCallCount()
		transport.mu.Lock()
		transport.staleAnonymousReads = 0
		transport.mu.Unlock()
		retried, err := publisher.Revoke(context.Background(), "license-anonymous-stale")
		if err != nil || retried.State != PublicationPublished || retried.Sequence != 1 || transport.casCallCount() != casCalls {
			t.Fatalf("retry=%#v err=%v CAS=%d want=%d", retried, err, transport.casCallCount(), casCalls)
		}
	})
}

func TestPublisherConcurrentProcessesPublishOneEventOnce(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	registry := newMemoryPublicationRegistry()
	transport := newMemoryPublishTransport()
	transport.setRemote(publisherEnvelope(t, privateKey, testPayload(0, []Entry{})), "blob-0")
	transport.barrierLoads = 2
	first := newPublisherForTest(t, publicKey, privateKey, registry, transport)
	second := newPublisherForTest(t, publicKey, privateKey, registry, transport)

	results := make(chan PublicationResult, 2)
	errorsChannel := make(chan error, 2)
	var wait sync.WaitGroup
	for _, publisher := range []*Publisher{first, second} {
		wait.Add(1)
		go func(publisher *Publisher) {
			defer wait.Done()
			result, err := publisher.Revoke(context.Background(), "license-two-processes")
			results <- result
			errorsChannel <- err
		}(publisher)
	}
	wait.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent Revoke: %v", err)
		}
	}
	for result := range results {
		if result.State != PublicationPublished || result.Sequence != 1 {
			t.Fatalf("concurrent result=%#v", result)
		}
	}
	manifest := verifyPublisherRemote(t, publicKey, transport.remoteFile())
	handle, _ := DeriveHandle("license-two-processes")
	if manifest.Sequence() != 1 || !manifest.Contains(handle) {
		t.Fatalf("remote sequence=%d contains=%v", manifest.Sequence(), manifest.Contains(handle))
	}
}

func TestPublisherRejectsMismatchedKeyAndWipesPrivateCopyOnClose(t *testing.T) {
	publicKey, privateKey := testManifestKey()
	otherPublic, _ := testAlternateManifestKey()
	registry := newMemoryPublicationRegistry()
	transport := newMemoryPublishTransport()
	if _, err := NewPublisher(testKeyID, otherPublic, privateKey, registry, transport, time.Now); !errors.Is(err, ErrPublicationFailed) {
		t.Fatalf("mismatched key error = %v", err)
	}
	publisher, err := NewPublisher(testKeyID, publicKey, privateKey, registry, transport, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	publisher.Close()
	for index, value := range publisher.privateKey {
		if value != 0 {
			t.Fatalf("private-key copy byte %d was not wiped", index)
		}
	}
	if result, err := publisher.Revoke(context.Background(), "license-after-close"); !errors.Is(err, ErrPublicationFailed) || result.State != PublicationFailed || registry.prepareCalls() != 0 {
		t.Fatalf("Revoke after close=%#v err=%v prepares=%d", result, err, registry.prepareCalls())
	}
}

var fixedPublisherTime = time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)

func newPublisherForTest(t *testing.T, publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey, registry PublicationRegistry, transport PublishTransport) *Publisher {
	t.Helper()
	publisher, err := NewPublisher(testKeyID, publicKey, privateKey, registry, transport, func() time.Time { return fixedPublisherTime })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(publisher.Close)
	return publisher
}

func publisherEnvelope(t *testing.T, privateKey ed25519.PrivateKey, payload Payload) string {
	t.Helper()
	envelope, err := Sign(payload, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func verifyPublisherRemote(t *testing.T, publicKey ed25519.PublicKey, remote RemoteFile) VerifiedManifest {
	t.Helper()
	manifest, err := Verify(string(remote.Bytes), testKeyID, publicKey)
	if err != nil {
		t.Fatalf("verify remote: %v", err)
	}
	return manifest
}

type memoryPublicationRegistry struct {
	mu               sync.Mutex
	events           map[Handle]PreparedRevocation
	prepareCount     int
	minimumSequence  uint64
	publishedHandles []Handle
}

func newMemoryPublicationRegistry() *memoryPublicationRegistry {
	return &memoryPublicationRegistry{events: make(map[Handle]PreparedRevocation)}
}

func (registry *memoryPublicationRegistry) Prepare(_ context.Context, handle Handle, requestedAt time.Time) (PreparedRevocation, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.prepareCount++
	if event, ok := registry.events[handle]; ok {
		return clonePreparedRevocationForTest(event), nil
	}
	event := PreparedRevocation{
		EventID:          fmt.Sprintf("event-%d", len(registry.events)+1),
		Handle:           handle,
		RequestedAt:      requestedAt,
		State:            PublicationPending,
		MinimumSequence:  registry.minimumSequence,
		PublishedHandles: append([]Handle(nil), registry.publishedHandles...),
	}
	registry.events[handle] = event
	return clonePreparedRevocationForTest(event), nil
}

func (registry *memoryPublicationRegistry) RecordOutcome(_ context.Context, eventID string, result PublicationResult) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for handle, event := range registry.events {
		if event.EventID != eventID {
			continue
		}
		event.State = result.State
		event.PublishedSequence = result.Sequence
		event.RemoteBlobID = result.BlobID
		registry.events[handle] = event
		return nil
	}
	return errors.New("unknown event")
}

func (registry *memoryPublicationRegistry) seedPending(handle Handle, requestedAt time.Time) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.events[handle] = PreparedRevocation{EventID: "event-seeded", Handle: handle, RequestedAt: requestedAt, State: PublicationPending, PublishedHandles: []Handle{}}
}

func (registry *memoryPublicationRegistry) prepareCalls() int {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.prepareCount
}

func clonePreparedRevocationForTest(event PreparedRevocation) PreparedRevocation {
	event.PublishedHandles = append([]Handle(nil), event.PublishedHandles...)
	return event
}

type memoryPublishTransport struct {
	mu                   sync.Mutex
	remote               RemoteFile
	nextBlob             int
	casCalls             int
	networkCalls         int
	anonymousCalls       int
	onNetwork            func()
	conflictOnce         string
	ambiguousOnce        bool
	failCAS              bool
	authOverrideAfterCAS string
	staleAnonymous       string
	staleAnonymousReads  int
	barrierLoads         int
	barrierLoadCount     int
	barrier              chan struct{}
}

func newMemoryPublishTransport() *memoryPublishTransport {
	return &memoryPublishTransport{nextBlob: 1, barrier: make(chan struct{})}
}

func (transport *memoryPublishTransport) LoadAuthenticated(context.Context) (RemoteFile, error) {
	transport.network()
	transport.mu.Lock()
	transport.networkCalls++
	if transport.barrierLoads > 0 && transport.barrierLoadCount < transport.barrierLoads {
		transport.barrierLoadCount++
		barrier := transport.barrier
		if transport.barrierLoadCount == transport.barrierLoads {
			close(barrier)
		}
		transport.mu.Unlock()
		<-barrier
		transport.mu.Lock()
	}
	remote := cloneRemoteFileForTest(transport.remote)
	if transport.casCalls > 0 && transport.authOverrideAfterCAS != "" {
		remote.Bytes = []byte(transport.authOverrideAfterCAS)
	}
	transport.mu.Unlock()
	return remote, nil
}

func (transport *memoryPublishTransport) CompareAndSwap(_ context.Context, expectedBlob string, proposed []byte) (RemoteFile, error) {
	transport.network()
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.networkCalls++
	transport.casCalls++
	if transport.conflictOnce != "" {
		transport.remote = RemoteFile{Bytes: []byte(transport.conflictOnce), BlobID: transport.allocateBlobLocked()}
		transport.conflictOnce = ""
		return RemoteFile{}, ErrCompareAndSwapConflict
	}
	if expectedBlob != transport.remote.BlobID || transport.failCAS {
		if expectedBlob != transport.remote.BlobID {
			return RemoteFile{}, ErrCompareAndSwapConflict
		}
		return RemoteFile{}, errors.New("compare-and-swap failed")
	}
	stored := RemoteFile{Bytes: append([]byte(nil), proposed...), BlobID: transport.allocateBlobLocked()}
	transport.remote = stored
	if transport.ambiguousOnce {
		transport.ambiguousOnce = false
		return RemoteFile{}, errors.New("ambiguous upload")
	}
	return cloneRemoteFileForTest(stored), nil
}

func (transport *memoryPublishTransport) LoadAnonymous(_ context.Context, _ string) ([]byte, error) {
	transport.network()
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.networkCalls++
	transport.anonymousCalls++
	if transport.staleAnonymousReads > 0 {
		transport.staleAnonymousReads--
		return []byte(transport.staleAnonymous), nil
	}
	return append([]byte(nil), transport.remote.Bytes...), nil
}

func (transport *memoryPublishTransport) setRemote(envelope, blobID string) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.remote = RemoteFile{Bytes: []byte(envelope), BlobID: blobID}
}

func (transport *memoryPublishTransport) remoteFile() RemoteFile {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return cloneRemoteFileForTest(transport.remote)
}

func (transport *memoryPublishTransport) casCallCount() int {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.casCalls
}

func (transport *memoryPublishTransport) anonymousCallCount() int {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.anonymousCalls
}

func (transport *memoryPublishTransport) networkCallCount() int {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.networkCalls
}

func (transport *memoryPublishTransport) allocateBlobLocked() string {
	blobID := fmt.Sprintf("blob-%d", transport.nextBlob)
	transport.nextBlob++
	return blobID
}

func (transport *memoryPublishTransport) network() {
	if transport.onNetwork != nil {
		transport.onNetwork()
	}
}

func cloneRemoteFileForTest(remote RemoteFile) RemoteFile {
	return RemoteFile{Bytes: bytes.Clone(remote.Bytes), BlobID: remote.BlobID}
}
