// Package revocation implements Telegram Companion's signed license-revocation protocol.
package revocation

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	revocationDomain  = "telegram-companion/revocation/license-id/v1\x00"
	maxLicenseIDBytes = 256
)

var errInvalidHandle = errors.New("revocation: invalid handle")

// Handle is the non-reversible public identity of one License ID.
type Handle [sha256.Size]byte

// DeriveHandle returns the domain-separated SHA-256 identity for licenseID.
func DeriveHandle(licenseID string) (Handle, error) {
	if licenseID == "" || len(licenseID) > maxLicenseIDBytes || !utf8.ValidString(licenseID) || strings.TrimSpace(licenseID) != licenseID {
		return Handle{}, errInvalidHandle
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(revocationDomain))
	_, _ = hasher.Write([]byte(licenseID))
	var handle Handle
	copy(handle[:], hasher.Sum(nil))
	return handle, nil
}

// String returns the canonical unpadded base64url representation.
func (h Handle) String() string {
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// ParseHandle accepts only a canonical unpadded base64url 32-byte handle.
func ParseHandle(value string) (Handle, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return Handle{}, errInvalidHandle
	}
	var handle Handle
	copy(handle[:], decoded)
	return handle, nil
}
