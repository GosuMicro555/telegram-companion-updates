package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"time"

	"telegram-companion/internal/revocation"
)

type revocationRestoreEvidence struct {
	Present        bool
	Sequence       uint64
	ManifestDigest [32]byte
	Entries        []revocation.Entry
}

func reconcileRevocationRestoreState(privateKey ed25519.PrivateKey, publicKey ed25519.PublicKey, current generatorRevocationState, evidence revocationRestoreEvidence) (generatorRevocationState, error) {
	if len(privateKey) != ed25519.PrivateKeySize || len(publicKey) != ed25519.PublicKeySize || !bytes.Equal(privateKey[ed25519.SeedSize:], publicKey) || !validRevocationRestoreEvidence(evidence) {
		return generatorRevocationState{}, ErrGeneratorRevocationUnavailable
	}
	currentValid := validGeneratorRevocationState(current)
	if currentValid && !bytes.Equal(current.PublicKey, publicKey) {
		return generatorRevocationState{}, ErrGeneratorKeyMismatch
	}

	remoteEntries := make(map[string]revocation.Entry, len(evidence.Entries))
	for _, entry := range evidence.Entries {
		remoteEntries[entry.Value] = entry
	}
	records := make([]revocationPublicationRecord, 0, len(evidence.Entries))
	knownHandles := make(map[string]struct{}, len(evidence.Entries))
	if currentValid {
		for _, record := range current.Records {
			entry, remote := remoteEntries[record.Handle]
			if record.State == revocationPublicationPublished && (!remote || record.PublishedSequence > evidence.Sequence) {
				return generatorRevocationState{}, ErrGeneratorRevocationUnavailable
			}
			if remote {
				record.State = revocationPublicationPublished
				record.PublishedSequence = evidence.Sequence
				record.RemoteBlobID = revocationRestoreBlobID(evidence.ManifestDigest)
				record.RequestedAt = entry.RevokedAt
			}
			records = append(records, record)
			knownHandles[record.Handle] = struct{}{}
		}
	}
	for _, entry := range evidence.Entries {
		if _, known := knownHandles[entry.Value]; known {
			continue
		}
		records = append(records, revocationPublicationRecord{
			EventID:           "remote-restore-" + entry.Value,
			Handle:            entry.Value,
			RequestedAt:       entry.RevokedAt,
			PublishedSequence: evidence.Sequence,
			RemoteBlobID:      revocationRestoreBlobID(evidence.ManifestDigest),
			State:             revocationPublicationPublished,
		})
	}
	next := generatorRevocationState{
		PrivateKey:      privateKey,
		PublicKey:       append(ed25519.PublicKey(nil), publicKey...),
		BackupConfirmed: true,
		Records:         records,
	}
	if !validGeneratorRevocationState(next) {
		return generatorRevocationState{}, ErrGeneratorRevocationUnavailable
	}
	return next, nil
}

func validRevocationRestoreEvidence(evidence revocationRestoreEvidence) bool {
	if !evidence.Present {
		return evidence.Sequence == 0 && evidence.ManifestDigest == ([32]byte{}) && evidence.Entries != nil && len(evidence.Entries) == 0
	}
	if evidence.ManifestDigest == ([32]byte{}) || evidence.Entries == nil || len(evidence.Entries) > revocation.MaxEntries || (len(evidence.Entries) > 0 && evidence.Sequence == 0) {
		return false
	}
	handles := make(map[string]struct{}, len(evidence.Entries))
	for _, entry := range evidence.Entries {
		if entry.Kind != revocation.EntryKindLicenseIDSHA256 {
			return false
		}
		if _, err := revocation.ParseHandle(entry.Value); err != nil {
			return false
		}
		revokedAt, err := time.Parse(time.RFC3339, entry.RevokedAt)
		if err != nil || revokedAt.Location() != time.UTC || revokedAt.Nanosecond() != 0 || revokedAt.Format(time.RFC3339) != entry.RevokedAt {
			return false
		}
		if _, duplicate := handles[entry.Value]; duplicate {
			return false
		}
		handles[entry.Value] = struct{}{}
	}
	return true
}

func revocationRestoreBlobID(digest [32]byte) string {
	if digest == ([32]byte{}) {
		return ""
	}
	return "manifest-sha256-" + hex.EncodeToString(digest[:])
}
