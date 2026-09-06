package license

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"
)

const goldenCanonicalPayload = `{"schema":1,"license_id":"license-golden-001","product":"telegram-companion","channel":"public-macos-arm64","machine_id":"9F85F7B16C2A4AA6A615B607820C250D","owner":"Golden Fixture","comment":"fixed test vector","issued_at":"2026-07-01T00:00:00Z","expires_at":"2026-08-01T00:00:00Z"}`

const goldenToken = "TCPLIC1.eyJzY2hlbWEiOjEsImxpY2Vuc2VfaWQiOiJsaWNlbnNlLWdvbGRlbi0wMDEiLCJwcm9kdWN0IjoidGVsZWdyYW0tY29tcGFuaW9uIiwiY2hhbm5lbCI6InB1YmxpYy1tYWNvcy1hcm02NCIsIm1hY2hpbmVfaWQiOiI5Rjg1RjdCMTZDMkE0QUE2QTYxNUI2MDc4MjBDMjUwRCIsIm93bmVyIjoiR29sZGVuIEZpeHR1cmUiLCJjb21tZW50IjoiZml4ZWQgdGVzdCB2ZWN0b3IiLCJpc3N1ZWRfYXQiOiIyMDI2LTA3LTAxVDAwOjAwOjAwWiIsImV4cGlyZXNfYXQiOiIyMDI2LTA4LTAxVDAwOjAwOjAwWiJ9.9OQIpU-TWQNi9qhUMiptNmlsEuyoucftqfEonW9BU6uHKF-Uqy5VvvukH0vbrv9IfOaycm8tYuQdx1RT05CpDg"

const replacementToken = "TCPLIC1.eyJzY2hlbWEiOjEsImxpY2Vuc2VfaWQiOiJsaWNlbnNlLWdvbGRlbi0wMDIiLCJwcm9kdWN0IjoidGVsZWdyYW0tY29tcGFuaW9uIiwiY2hhbm5lbCI6InB1YmxpYy1tYWNvcy1hcm02NCIsIm1hY2hpbmVfaWQiOiI5Rjg1RjdCMTZDMkE0QUE2QTYxNUI2MDc4MjBDMjUwRCIsIm93bmVyIjoiUmVwbGFjZW1lbnQgRml4dHVyZSIsImlzc3VlZF9hdCI6IjIwMjYtMDctMDFUMDA6MDA6MDBaIn0.XZqfbnbgEWb_E7RgTiQzLfW82b3_SFZbf6LOlW3_EHOkIjtNk-PQJsBsQ-TvDivQ2SDDyq2XsaujOx2OnxcuAw"

const copiedMalformedSentinel Error = ErrMalformed

func TestCanonicalPayloadAndTokenMatchFixedGoldenVector(t *testing.T) {
	want := Payload{
		Schema:    1,
		LicenseID: "license-golden-001",
		Product:   "telegram-companion",
		Channel:   "public-macos-arm64",
		MachineID: "9F85F7B16C2A4AA6A615B607820C250D",
		Owner:     "Golden Fixture",
		Comment:   "fixed test vector",
		IssuedAt:  "2026-07-01T00:00:00Z",
		ExpiresAt: "2026-08-01T00:00:00Z",
	}

	canonical, err := CanonicalPayload(want)
	if err != nil {
		t.Fatalf("CanonicalPayload() error = %v", err)
	}
	if got := string(canonical); got != goldenCanonicalPayload {
		t.Fatalf("CanonicalPayload() = %q, want %q", got, goldenCanonicalPayload)
	}

	got, err := ParseAndVerify(goldenToken, goldenVerifyOptions(t))
	if err != nil {
		t.Fatalf("ParseAndVerify() error = %v", err)
	}
	if got != want {
		t.Fatalf("ParseAndVerify() payload = %#v, want %#v", got, want)
	}
}

func TestParseAndVerifyAcceptsCanonicalSignedLicense(t *testing.T) {
	publicKey, privateKey := testKey(t)
	payload := testPayload()
	token := signToken(t, privateKey, payload)

	got, err := ParseAndVerify(token, testVerifyOptions(publicKey, payload))
	if err != nil {
		t.Fatalf("ParseAndVerify() error = %v", err)
	}
	if got != payload {
		t.Fatalf("ParseAndVerify() payload = %#v, want %#v", got, payload)
	}
}

func TestParseAndVerifyForRegistryAcceptsExpiredSchema1AndSchema2WithoutMachineContext(t *testing.T) {
	publicKey, privateKey := testKey(t)
	for _, schema := range []int{legacySchema, currentSchema} {
		t.Run(fmt.Sprintf("schema %d", schema), func(t *testing.T) {
			payload := testPayload()
			payload.Schema = schema
			payload.ExpiresAt = "2026-07-02T00:00:00Z"
			if schema == currentSchema {
				payload.SeedID = "public-seed-registry"
				payload.SeedKey = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x17}, 32))
			}
			token := signToken(t, privateKey, payload)
			got, err := ParseAndVerifyForRegistry(token, RegistryVerifyOptions{
				PublicKey: publicKey,
				Product:   payload.Product,
				Channel:   payload.Channel,
			})
			if err != nil {
				t.Fatalf("ParseAndVerifyForRegistry() error = %v", err)
			}
			if got != payload {
				t.Fatalf("ParseAndVerifyForRegistry() payload = %#v, want %#v", got, payload)
			}
		})
	}
}

func TestParseAndVerifyForRegistryKeepsStrictSignatureProductChannelAndCanonicalChecks(t *testing.T) {
	publicKey, privateKey := testKey(t)
	payload := testPayload()
	token := signToken(t, privateKey, payload)
	validOptions := RegistryVerifyOptions{PublicKey: publicKey, Product: payload.Product, Channel: payload.Channel}

	tamperedParts := strings.Split(token, ".")
	tamperedParts[1] = "A" + tamperedParts[1][1:]
	tampered := strings.Join(tamperedParts, ".")
	canonicalPayload, err := CanonicalPayload(payload)
	if err != nil {
		t.Fatalf("CanonicalPayload() error = %v", err)
	}
	nonCanonicalPayload := strings.Replace(string(canonicalPayload), `,"owner"`, `, "owner"`, 1)
	nonCanonical := signPayloadBytes(privateKey, []byte(nonCanonicalPayload))

	for name, test := range map[string]struct {
		token   string
		options RegistryVerifyOptions
		want    error
	}{
		"malformed":       {token: "not-a-license", options: validOptions, want: ErrMalformed},
		"bad signature":   {token: tampered, options: validOptions, want: ErrInvalidSignature},
		"wrong product":   {token: token, options: RegistryVerifyOptions{PublicKey: publicKey, Product: "other", Channel: payload.Channel}, want: ErrWrongProduct},
		"wrong channel":   {token: token, options: RegistryVerifyOptions{PublicKey: publicKey, Product: payload.Product, Channel: "internal"}, want: ErrWrongChannel},
		"noncanonical":    {token: nonCanonical, options: validOptions, want: ErrMalformed},
		"missing context": {token: token, options: RegistryVerifyOptions{PublicKey: publicKey}, want: ErrMalformed},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseAndVerifyForRegistry(test.token, test.options); !errors.Is(err, test.want) {
				t.Fatalf("ParseAndVerifyForRegistry() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestParseAndVerifyAcceptsSchema2LicenseWithUniversalSeedGrant(t *testing.T) {
	publicKey, privateKey := testKey(t)
	payload := testPayload()
	payload.Schema = 2
	payload.SeedID = "public-seed-082"
	payload.SeedKey = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	token := signToken(t, privateKey, payload)

	got, err := ParseAndVerify(token, testVerifyOptions(publicKey, payload))
	if err != nil {
		t.Fatalf("ParseAndVerify() error = %v", err)
	}
	grant, err := got.SeedGrant()
	if err != nil {
		t.Fatalf("SeedGrant() error = %v", err)
	}
	if grant.ID != payload.SeedID || len(grant.Key) != 32 {
		t.Fatalf("SeedGrant() = %#v, want ID %q and 32-byte key", grant, payload.SeedID)
	}
}

func TestSchema2LicensesForDifferentMachinesCarrySameSeedGrant(t *testing.T) {
	publicKey, privateKey := testKey(t)
	seedKey := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	first := testPayload()
	first.Schema = 2
	first.MachineID = "MAC-ONE"
	first.SeedID = "public-seed-082"
	first.SeedKey = seedKey
	second := first
	second.LicenseID = "license-456"
	second.MachineID = "MAC-TWO"

	gotFirst, err := ParseAndVerify(signToken(t, privateKey, first), testVerifyOptions(publicKey, first))
	if err != nil {
		t.Fatalf("ParseAndVerify(first) error = %v", err)
	}
	gotSecond, err := ParseAndVerify(signToken(t, privateKey, second), testVerifyOptions(publicKey, second))
	if err != nil {
		t.Fatalf("ParseAndVerify(second) error = %v", err)
	}
	firstGrant, err := gotFirst.SeedGrant()
	if err != nil {
		t.Fatalf("first SeedGrant() error = %v", err)
	}
	secondGrant, err := gotSecond.SeedGrant()
	if err != nil {
		t.Fatalf("second SeedGrant() error = %v", err)
	}
	if firstGrant.ID != secondGrant.ID || !constantEqualBytes(firstGrant.Key, secondGrant.Key) {
		t.Fatalf("seed grants differ: first=%#v second=%#v", firstGrant, secondGrant)
	}
}

func TestSchema2LicenseRejectsMissingOrMalformedSeedGrant(t *testing.T) {
	validKey := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	tests := []struct {
		name    string
		seedID  string
		seedKey string
	}{
		{name: "missing id", seedKey: validKey},
		{name: "blank id", seedID: " ", seedKey: validKey},
		{name: "id with surrounding whitespace", seedID: " public-seed-082 ", seedKey: validKey},
		{name: "missing key", seedID: "public-seed-082"},
		{name: "malformed base64", seedID: "public-seed-082", seedKey: "not+base64"},
		{name: "wrong key size", seedID: "public-seed-082", seedKey: base64.RawURLEncoding.EncodeToString(make([]byte, 31))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := testPayload()
			payload.Schema = 2
			payload.SeedID = tt.seedID
			payload.SeedKey = tt.seedKey
			if _, err := CanonicalPayload(payload); !errors.Is(err, ErrMalformed) {
				t.Fatalf("CanonicalPayload() error = %v, want ErrMalformed", err)
			}
		})
	}
}

func TestSchema1LicenseHasNoSeedGrant(t *testing.T) {
	payload := testPayload()
	if _, err := payload.SeedGrant(); !errors.Is(err, ErrNoSeedGrant) {
		t.Fatalf("SeedGrant() error = %v, want ErrNoSeedGrant", err)
	}
}

func TestParseAndVerifyRejectsMalformedToken(t *testing.T) {
	_, err := ParseAndVerify("not-a-license", goldenVerifyOptions(t))
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("ParseAndVerify() error = %v, want ErrMalformed", err)
	}
}

func TestParseAndVerifyRejectsUnsupportedSchema(t *testing.T) {
	publicKey, privateKey := testKey(t)
	payload := testPayload()
	payload.Schema = currentSchema + 1
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	token := signPayloadBytes(privateKey, rawPayload)

	_, err = ParseAndVerify(token, testVerifyOptions(publicKey, payload))
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("ParseAndVerify() error = %v, want ErrUnsupportedSchema", err)
	}
}

func TestParseAndVerifyClassifiesFutureSchemaWithUnknownFields(t *testing.T) {
	publicKey, privateKey := testKey(t)
	rawPayload := []byte(`{"schema":3,"license_id":"license-future-001","product":"telegram-companion","channel":"public-macos-arm64","machine_id":"A1B2C3D4","owner":"Future Owner","issued_at":"2026-07-01T00:00:00Z","entitlements":{"updates":true,"seats":3},"not_before":"2026-07-02T00:00:00Z"}`)
	token := signPayloadBytes(privateKey, rawPayload)

	_, err := ParseAndVerify(token, testVerifyOptions(publicKey, testPayload()))
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("ParseAndVerify() error = %v, want ErrUnsupportedSchema", err)
	}
}

func TestErrorSentinelsAreConstantsAndSupportErrorsIs(t *testing.T) {
	sentinels := []Error{
		copiedMalformedSentinel,
		ErrUnsupportedSchema,
		ErrInvalidSignature,
		ErrInvalidPublicKey,
		ErrWrongProduct,
		ErrWrongChannel,
		ErrWrongMachine,
		ErrExpired,
		ErrNotFound,
		ErrMachineUnavailable,
	}
	for _, sentinel := range sentinels {
		wrapped := fmt.Errorf("wrapped: %w", sentinel)
		if !errors.Is(wrapped, sentinel) {
			t.Fatalf("errors.Is(%v, %v) = false", wrapped, sentinel)
		}
	}
	if !errors.Is(Error(CodeMalformed), ErrMalformed) {
		t.Fatal("equivalent typed errors no longer match ErrMalformed")
	}
	if !errors.Is(ErrNotFound, fs.ErrNotExist) {
		t.Fatal("ErrNotFound no longer matches fs.ErrNotExist")
	}
}

func TestParseAndVerifyRejectsWrongProduct(t *testing.T) {
	publicKey, privateKey := testKey(t)
	payload := testPayload()
	token := signToken(t, privateKey, payload)
	options := testVerifyOptions(publicKey, payload)
	options.Product = "other-product"

	_, err := ParseAndVerify(token, options)
	if !errors.Is(err, ErrWrongProduct) {
		t.Fatalf("ParseAndVerify() error = %v, want ErrWrongProduct", err)
	}
}

func TestParseAndVerifyRejectsWrongChannel(t *testing.T) {
	publicKey, privateKey := testKey(t)
	payload := testPayload()
	token := signToken(t, privateKey, payload)
	options := testVerifyOptions(publicKey, payload)
	options.Channel = "internal"

	_, err := ParseAndVerify(token, options)
	if !errors.Is(err, ErrWrongChannel) {
		t.Fatalf("ParseAndVerify() error = %v, want ErrWrongChannel", err)
	}
}

func TestParseAndVerifyRejectsWrongMachine(t *testing.T) {
	publicKey, privateKey := testKey(t)
	payload := testPayload()
	token := signToken(t, privateKey, payload)
	options := testVerifyOptions(publicKey, payload)
	options.MachineID = "A DIFFERENT MACHINE"

	_, err := ParseAndVerify(token, options)
	if !errors.Is(err, ErrWrongMachine) {
		t.Fatalf("ParseAndVerify() error = %v, want ErrWrongMachine", err)
	}
}

func TestParseAndVerifyRejectsExpiredLicense(t *testing.T) {
	publicKey, privateKey := testKey(t)
	payload := testPayload()
	payload.ExpiresAt = "2026-07-20T00:00:00Z"
	token := signToken(t, privateKey, payload)

	_, err := ParseAndVerify(token, testVerifyOptions(publicKey, payload))
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("ParseAndVerify() error = %v, want ErrExpired", err)
	}
}

func TestParseAndVerifyRejectsTamperedToken(t *testing.T) {
	publicKey, privateKey := testKey(t)
	payload := testPayload()
	token := signToken(t, privateKey, payload)
	parts := strings.Split(token, ".")
	parts[1] = "A" + parts[1][1:]
	token = strings.Join(parts, ".")

	_, err := ParseAndVerify(token, testVerifyOptions(publicKey, payload))
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("ParseAndVerify() error = %v, want ErrInvalidSignature", err)
	}
}

func TestParseAndVerifyRejectsNonCanonicalPayload(t *testing.T) {
	publicKey, privateKey := testKey(t)
	payload := testPayload()
	canonical, err := CanonicalPayload(payload)
	if err != nil {
		t.Fatalf("CanonicalPayload() error = %v", err)
	}
	nonCanonical := strings.Replace(string(canonical), `,"owner"`, `, "owner"`, 1)
	payloadPart := base64.RawURLEncoding.EncodeToString([]byte(nonCanonical))
	message := []byte(TokenPrefix + "." + payloadPart)
	signature := ed25519.Sign(privateKey, message)
	token := TokenPrefix + "." + payloadPart + "." + base64.RawURLEncoding.EncodeToString(signature)

	_, err = ParseAndVerify(token, testVerifyOptions(publicKey, payload))
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("ParseAndVerify() error = %v, want ErrMalformed", err)
	}
}

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	return publicKey, privateKey
}

func testPayload() Payload {
	return Payload{
		Schema:    1,
		LicenseID: "license-123",
		Product:   "telegram-companion",
		Channel:   "public-macos-arm64",
		MachineID: "A1B2C3D4",
		Owner:     "Alice",
		Comment:   "test license",
		IssuedAt:  "2026-07-01T00:00:00Z",
	}
}

func testVerifyOptions(publicKey ed25519.PublicKey, payload Payload) VerifyOptions {
	return VerifyOptions{
		PublicKey: publicKey,
		Product:   payload.Product,
		Channel:   payload.Channel,
		MachineID: payload.MachineID,
		Now:       time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC),
	}
}

func goldenVerifyOptions(t *testing.T) VerifyOptions {
	t.Helper()
	publicKey, err := hex.DecodeString("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a")
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	return VerifyOptions{
		PublicKey: ed25519.PublicKey(publicKey),
		Product:   "telegram-companion",
		Channel:   "public-macos-arm64",
		MachineID: "9F85F7B16C2A4AA6A615B607820C250D",
		Now:       time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC),
	}
}

func signToken(t *testing.T, privateKey ed25519.PrivateKey, payload Payload) string {
	t.Helper()
	canonical, err := CanonicalPayload(payload)
	if err != nil {
		t.Fatalf("CanonicalPayload() error = %v", err)
	}
	return signPayloadBytes(privateKey, canonical)
}

func signPayloadBytes(privateKey ed25519.PrivateKey, payload []byte) string {
	payloadPart := base64.RawURLEncoding.EncodeToString(payload)
	message := []byte(TokenPrefix + "." + payloadPart)
	signature := ed25519.Sign(privateKey, message)
	return TokenPrefix + "." + payloadPart + "." + base64.RawURLEncoding.EncodeToString(signature)
}
