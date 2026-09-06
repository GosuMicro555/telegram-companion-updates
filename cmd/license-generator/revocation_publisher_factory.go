package main

import (
	"context"
	"crypto/ed25519"
	"time"

	"telegram-companion/internal/revocation"
)

const generatorRevocationKeyID = "revocation-2026-01"

type githubRevocationPublisherFactory struct{}

func (*githubRevocationPublisherFactory) ProveRemoteAbsent(ctx context.Context) (bool, error) {
	probe, err := newProductionGitHubRevocationAbsenceProbe()
	if err != nil {
		return false, err
	}
	return probe.ProveRemoteAbsent(ctx)
}

func (*githubRevocationPublisherFactory) InspectRemote(ctx context.Context, publicKey ed25519.PublicKey) (revocationRestoreEvidence, error) {
	probe, err := newProductionGitHubRevocationAbsenceProbe()
	if err != nil {
		return revocationRestoreEvidence{}, err
	}
	return probe.InspectRemote(ctx, publicKey)
}

func (*githubRevocationPublisherFactory) New(token []byte, publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey, registry revocation.PublicationRegistry, now func() time.Time) (generatorRevocationPublisher, error) {
	transport, err := newProductionGitHubRevocationTransport(token)
	if err != nil {
		return nil, err
	}
	publisher, err := revocation.NewPublisher(generatorRevocationKeyID, publicKey, privateKey, registry, transport, now)
	if err != nil {
		transport.Close()
		return nil, err
	}
	return &githubGeneratorRevocationPublisher{publisher: publisher, transport: transport}, nil
}

type githubGeneratorRevocationPublisher struct {
	publisher *revocation.Publisher
	transport *githubRevocationTransport
}

func (publisher *githubGeneratorRevocationPublisher) Revoke(ctx context.Context, licenseID string) (revocation.PublicationResult, error) {
	if publisher == nil || publisher.publisher == nil {
		return revocation.PublicationResult{}, ErrGeneratorRevocationUnavailable
	}
	return publisher.publisher.Revoke(ctx, licenseID)
}

func (publisher *githubGeneratorRevocationPublisher) Initialize(ctx context.Context) (revocation.PublicationResult, error) {
	if publisher == nil || publisher.publisher == nil {
		return revocation.PublicationResult{}, ErrGeneratorRevocationUnavailable
	}
	return publisher.publisher.Initialize(ctx)
}

func (publisher *githubGeneratorRevocationPublisher) Close() {
	if publisher == nil {
		return
	}
	if publisher.publisher != nil {
		publisher.publisher.Close()
	}
	if publisher.transport != nil {
		publisher.transport.Close()
	}
}

var _ generatorRevocationPublisherFactory = (*githubRevocationPublisherFactory)(nil)
