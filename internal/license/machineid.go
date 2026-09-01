package license

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

const machineIDDomain = "telegram-companion:machine:v1:"

// DeriveMachineID returns the uppercase, domain-separated SHA-256 Machine ID.
func DeriveMachineID(raw string) string {
	normalized := normalizeMachineIdentifier(raw)
	digest := sha256.Sum256([]byte(machineIDDomain + normalized))
	return strings.ToUpper(hex.EncodeToString(digest[:]))
}

// MachineID derives the stable application identifier from the platform source.
// It never returns the raw platform identifier.
func MachineID() (string, error) {
	raw, err := platformMachineIdentifier()
	if err != nil {
		return "", ErrMachineUnavailable
	}
	return machineIDFromRaw(raw)
}

func machineIDFromRaw(raw string) (string, error) {
	normalized := normalizeMachineIdentifier(raw)
	if normalized == "" || strings.IndexFunc(normalized, unicode.IsControl) >= 0 {
		return "", ErrMachineUnavailable
	}
	return DeriveMachineID(normalized), nil
}

func normalizeMachineIdentifier(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}
