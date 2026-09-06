package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"io"

	"telegram-companion/internal/licenseissuer"
)

type generatorState struct {
	PrivateKey      ed25519.PrivateKey
	PublicKey       ed25519.PublicKey
	BackupConfirmed bool
	History         []licenseissuer.HistoryEntry
	SeedID          string
	SeedKey         []byte
}

func newSeedGrant(random io.Reader) (string, []byte, error) {
	material := make([]byte, 48)
	if _, err := io.ReadFull(random, material); err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(material[:16]), material[16:], nil
}

func validSeedGrant(seedID string, seedKey []byte) bool {
	return validSeedID(seedID) && len(seedKey) == 32
}

func validSeedID(seedID string) bool {
	return len(seedID) == 32 && isLowerHex(seedID)
}

func isLowerHex(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
