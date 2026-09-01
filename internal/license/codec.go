// Package license verifies offline Telegram Companion license tokens.
package license

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"strings"
	"time"
)

const (
	// TokenPrefix identifies the versioned Telegram Companion license envelope.
	TokenPrefix = "TCPLIC1"

	legacySchema  = 1
	currentSchema = 2
)

// Payload is the signed, product-scoped content of an offline license.
type Payload struct {
	Schema    int    `json:"schema"`
	LicenseID string `json:"license_id"`
	Product   string `json:"product"`
	Channel   string `json:"channel"`
	MachineID string `json:"machine_id"`
	Owner     string `json:"owner"`
	Comment   string `json:"comment,omitempty"`
	IssuedAt  string `json:"issued_at"`
	ExpiresAt string `json:"expires_at,omitempty"`
	SeedID    string `json:"seed_id,omitempty"`
	SeedKey   string `json:"seed_key,omitempty"`
}

// SeedGrant unlocks the universal first-launch state bundled with Public builds.
type SeedGrant struct {
	ID  string
	Key []byte
}

// VerifyOptions supplies the trust and product context for token verification.
// It intentionally does not depend on build metadata packages.
type VerifyOptions struct {
	PublicKey ed25519.PublicKey
	Product   string
	Channel   string
	MachineID string
	Now       time.Time
}

// RegistryVerifyOptions supplies trust and product scope for importing a
// license into the owner's local issuance registry. It deliberately has no
// Machine ID or current-time input.
type RegistryVerifyOptions struct {
	PublicKey ed25519.PublicKey
	Product   string
	Channel   string
}

// ErrorCode classifies a license error without exposing token or hardware data.
type ErrorCode string

const (
	CodeMalformed          ErrorCode = "malformed"
	CodeUnsupportedSchema  ErrorCode = "unsupported_schema"
	CodeInvalidSignature   ErrorCode = "invalid_signature"
	CodeInvalidPublicKey   ErrorCode = "invalid_public_key"
	CodeWrongProduct       ErrorCode = "wrong_product"
	CodeWrongChannel       ErrorCode = "wrong_channel"
	CodeWrongMachine       ErrorCode = "wrong_machine"
	CodeExpired            ErrorCode = "expired"
	CodeNotFound           ErrorCode = "not_found"
	CodeMachineUnavailable ErrorCode = "machine_unavailable"
	CodeNoSeedGrant        ErrorCode = "no_seed_grant"
)

// Error is an immutable, safe-to-display classification of a license failure.
type Error string

func (e Error) Error() string {
	return "license: " + string(e)
}

func (e Error) Is(target error) bool {
	if target == fs.ErrNotExist {
		return e == ErrNotFound
	}
	switch other := target.(type) {
	case Error:
		return e == other
	case *Error:
		return other != nil && e == *other
	default:
		return false
	}
}

const (
	ErrMalformed          Error = Error(CodeMalformed)
	ErrUnsupportedSchema  Error = Error(CodeUnsupportedSchema)
	ErrInvalidSignature   Error = Error(CodeInvalidSignature)
	ErrInvalidPublicKey   Error = Error(CodeInvalidPublicKey)
	ErrWrongProduct       Error = Error(CodeWrongProduct)
	ErrWrongChannel       Error = Error(CodeWrongChannel)
	ErrWrongMachine       Error = Error(CodeWrongMachine)
	ErrExpired            Error = Error(CodeExpired)
	ErrNotFound           Error = Error(CodeNotFound)
	ErrMachineUnavailable Error = Error(CodeMachineUnavailable)
	ErrNoSeedGrant        Error = Error(CodeNoSeedGrant)
)

// SeedGrant returns a defensive copy of the signed universal seed grant.
func (p Payload) SeedGrant() (SeedGrant, error) {
	if p.Schema == legacySchema && p.SeedID == "" && p.SeedKey == "" {
		return SeedGrant{}, ErrNoSeedGrant
	}
	if p.Schema != currentSchema || strings.TrimSpace(p.SeedID) == "" || strings.TrimSpace(p.SeedID) != p.SeedID || p.SeedKey == "" {
		return SeedGrant{}, ErrMalformed
	}
	key, err := decodeCanonicalBase64(p.SeedKey)
	if err != nil || len(key) != 32 {
		return SeedGrant{}, ErrMalformed
	}
	return SeedGrant{ID: p.SeedID, Key: append([]byte(nil), key...)}, nil
}

// CanonicalPayload validates p and returns its deterministic JSON encoding.
func CanonicalPayload(p Payload) ([]byte, error) {
	if err := validatePayload(p); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		return nil, ErrMalformed
	}
	return encoded, nil
}

// ParseAndVerify parses a TCPLIC1 token, validates its signature, and confirms
// that it was issued for the configured product, channel, and Machine ID.
func ParseAndVerify(token string, options VerifyOptions) (Payload, error) {
	var zero Payload
	if len(options.PublicKey) != ed25519.PublicKeySize {
		return zero, ErrInvalidPublicKey
	}
	if options.Product == "" || options.Channel == "" || options.MachineID == "" {
		return zero, ErrMalformed
	}
	payload, err := parseAndVerifyScoped(token, options.PublicKey, options.Product, options.Channel)
	if err != nil {
		return zero, err
	}
	if !constantEqual(payload.MachineID, options.MachineID) {
		return zero, ErrWrongMachine
	}
	if payload.ExpiresAt != "" {
		expiresAt, _ := time.Parse(time.RFC3339, payload.ExpiresAt)
		now := options.Now
		if now.IsZero() {
			now = time.Now()
		}
		if !expiresAt.After(now) {
			return zero, ErrExpired
		}
	}
	return payload, nil
}

// ParseAndVerifyForRegistry verifies a canonical signed license for the
// configured product and channel while intentionally ignoring machine binding
// and expiry. Callers must reduce the returned payload to safe local metadata
// and must never use this function for runtime authorization.
func ParseAndVerifyForRegistry(token string, options RegistryVerifyOptions) (Payload, error) {
	var zero Payload
	if len(options.PublicKey) != ed25519.PublicKeySize {
		return zero, ErrInvalidPublicKey
	}
	if options.Product == "" || options.Channel == "" {
		return zero, ErrMalformed
	}
	return parseAndVerifyScoped(token, options.PublicKey, options.Product, options.Channel)
}

func parseAndVerifyScoped(token string, publicKey ed25519.PublicKey, product, channel string) (Payload, error) {
	var zero Payload

	payloadPart, signaturePart, err := splitToken(token)
	if err != nil {
		return zero, err
	}
	payloadBytes, err := decodeCanonicalBase64(payloadPart)
	if err != nil {
		return zero, ErrMalformed
	}
	signature, err := decodeCanonicalBase64(signaturePart)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return zero, ErrMalformed
	}
	if !ed25519.Verify(publicKey, []byte(TokenPrefix+"."+payloadPart), signature) {
		return zero, ErrInvalidSignature
	}
	schema, err := decodePayloadSchema(payloadBytes)
	if err != nil {
		return zero, err
	}
	if schema != legacySchema && schema != currentSchema {
		return zero, ErrUnsupportedSchema
	}

	var payload Payload
	decoder := json.NewDecoder(strings.NewReader(string(payloadBytes)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return zero, ErrMalformed
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return zero, ErrMalformed
	}
	canonical, err := CanonicalPayload(payload)
	if err != nil {
		return zero, err
	}
	if !constantEqualBytes(payloadBytes, canonical) {
		return zero, ErrMalformed
	}

	if !constantEqual(payload.Product, product) {
		return zero, ErrWrongProduct
	}
	if !constantEqual(payload.Channel, channel) {
		return zero, ErrWrongChannel
	}
	return payload, nil
}

func decodePayloadSchema(payload []byte) (int, error) {
	var envelope struct {
		Schema *int `json:"schema"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Schema == nil {
		return 0, ErrMalformed
	}
	return *envelope.Schema, nil
}

func validatePayload(p Payload) error {
	if p.Schema != legacySchema && p.Schema != currentSchema {
		return ErrUnsupportedSchema
	}
	if p.LicenseID == "" || p.Product == "" || p.Channel == "" || p.MachineID == "" || p.Owner == "" || p.IssuedAt == "" {
		return ErrMalformed
	}
	if _, err := time.Parse(time.RFC3339, p.IssuedAt); err != nil {
		return ErrMalformed
	}
	if p.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, p.ExpiresAt); err != nil {
			return ErrMalformed
		}
	}
	if p.Schema == legacySchema {
		if p.SeedID != "" || p.SeedKey != "" {
			return ErrMalformed
		}
		return nil
	}
	if _, err := p.SeedGrant(); err != nil {
		return ErrMalformed
	}
	return nil
}

func splitToken(token string) (string, string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != TokenPrefix || parts[1] == "" || parts[2] == "" || strings.IndexFunc(token, isWhitespace) >= 0 {
		return "", "", ErrMalformed
	}
	return parts[1], parts[2], nil
}

func decodeCanonicalBase64(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, errors.New("invalid canonical base64")
	}
	return decoded, nil
}

func validateTokenEnvelope(token string) error {
	payloadPart, signaturePart, err := splitToken(token)
	if err != nil {
		return err
	}
	if _, err := decodeCanonicalBase64(payloadPart); err != nil {
		return ErrMalformed
	}
	signature, err := decodeCanonicalBase64(signaturePart)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return ErrMalformed
	}
	return nil
}

func constantEqual(left, right string) bool {
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}

func constantEqualBytes(left, right []byte) bool {
	leftHash := sha256.Sum256(left)
	rightHash := sha256.Sum256(right)
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}

func isWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n'
}
