package licenseissuer

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"telegram-companion/internal/license"
)

const goldenCanonicalPayload = `{"schema":1,"license_id":"license-golden-001","product":"telegram-companion","channel":"public-macos-arm64","machine_id":"9F85F7B16C2A4AA6A615B607820C250D","owner":"Golden Fixture","comment":"fixed test vector","issued_at":"2026-07-01T00:00:00Z","expires_at":"2026-08-01T00:00:00Z"}`

func TestIssueUsesCanonicalTCPLIC1Envelope(t *testing.T) {
	publicKey, privateKey, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	payload := goldenPayload()
	token, err := Issue(privateKey, payload)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != license.TokenPrefix {
		t.Fatalf("Issue() token envelope = %q", token)
	}
	if parts[1] != base64.RawURLEncoding.EncodeToString([]byte(goldenCanonicalPayload)) {
		t.Fatalf("Issue() payload part = %q, want exact canonical payload", parts[1])
	}

	again, err := Issue(privateKey, payload)
	if err != nil {
		t.Fatalf("second Issue() error = %v", err)
	}
	if again != token {
		t.Fatal("Issue() is not deterministic for the same key and payload")
	}
	if _, err := license.ParseAndVerify(token, verifyOptions(publicKey, payload)); err != nil {
		t.Fatalf("license.ParseAndVerify() error = %v", err)
	}
}

func TestGenerateKeyIssuesLicenseVerifiedByLicensePackage(t *testing.T) {
	publicKey, privateKey, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	if len(publicKey) != ed25519.PublicKeySize || len(privateKey) != ed25519.PrivateKeySize {
		t.Fatalf("GenerateKey() lengths = (%d, %d)", len(publicKey), len(privateKey))
	}

	payload := testPayload()
	token, err := Issue(privateKey, payload)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if !strings.HasPrefix(token, license.TokenPrefix+".") {
		t.Fatalf("Issue() token prefix = %q", token)
	}

	got, err := license.ParseAndVerify(token, verifyOptions(publicKey, payload))
	if err != nil {
		t.Fatalf("license.ParseAndVerify() error = %v", err)
	}
	if got != payload {
		t.Fatalf("license.ParseAndVerify() payload = %#v, want %#v", got, payload)
	}
}

func TestIssuedLicenseIsProductChannelAndMachineScoped(t *testing.T) {
	publicKey, privateKey, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	payload := testPayload()
	token, err := Issue(privateKey, payload)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*license.VerifyOptions)
		wantErr error
	}{
		{
			name: "product",
			mutate: func(options *license.VerifyOptions) {
				options.Product = "other-product"
			},
			wantErr: license.ErrWrongProduct,
		},
		{
			name: "channel",
			mutate: func(options *license.VerifyOptions) {
				options.Channel = "internal"
			},
			wantErr: license.ErrWrongChannel,
		},
		{
			name: "machine",
			mutate: func(options *license.VerifyOptions) {
				options.MachineID = "OTHER-MACHINE"
			},
			wantErr: license.ErrWrongMachine,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := verifyOptions(publicKey, payload)
			test.mutate(&options)

			_, err := license.ParseAndVerify(token, options)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ParseAndVerify() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestIssueRejectsInvalidPrivateKeyWithoutPanicking(t *testing.T) {
	_, err := Issue(ed25519.PrivateKey("not-a-private-key"), testPayload())
	if !errors.Is(err, ErrInvalidPrivateKey) {
		t.Fatalf("Issue() error = %v, want ErrInvalidPrivateKey", err)
	}
}

func testPayload() license.Payload {
	return license.Payload{
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

func goldenPayload() license.Payload {
	return license.Payload{
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
}

func verifyOptions(publicKey ed25519.PublicKey, payload license.Payload) license.VerifyOptions {
	return license.VerifyOptions{
		PublicKey: publicKey,
		Product:   payload.Product,
		Channel:   payload.Channel,
		MachineID: payload.MachineID,
		Now:       time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC),
	}
}
