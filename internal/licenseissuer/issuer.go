// Package licenseissuer creates and protects Telegram Companion license keys.
package licenseissuer

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"

	"telegram-companion/internal/license"
)

// ErrInvalidPrivateKey is returned when signing or backup input is not a full
// Ed25519 private key.
var ErrInvalidPrivateKey = errors.New("licenseissuer: invalid private key")

// GenerateKey creates a new Ed25519 license-signing key pair.
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// Issue returns the canonical TCPLIC1 token for payload.
func Issue(privateKey ed25519.PrivateKey, payload license.Payload) (string, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", ErrInvalidPrivateKey
	}

	canonical, err := license.CanonicalPayload(payload)
	if err != nil {
		return "", err
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(canonical)
	message := license.TokenPrefix + "." + payloadPart
	signature := ed25519.Sign(privateKey, []byte(message))

	return message + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
