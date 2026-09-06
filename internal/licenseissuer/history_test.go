package licenseissuer

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"telegram-companion/internal/license"
)

func TestMarshalHistoryPersistsOnlyWhitelistedIssuanceMetadata(t *testing.T) {
	publicKey, privateKey, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	payload := testPayload()
	token, err := Issue(privateKey, payload)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	entry, err := NewHistoryEntry(payload)
	if err != nil {
		t.Fatalf("NewHistoryEntry() error = %v", err)
	}

	encoded, err := MarshalHistory([]HistoryEntry{entry})
	if err != nil {
		t.Fatalf("MarshalHistory() error = %v", err)
	}
	for _, field := range []string{`"token"`, `"license_token"`, `"private_key"`, `"privateKey"`} {
		if bytes.Contains(encoded, []byte(field)) {
			t.Fatalf("MarshalHistory() persisted forbidden field %s: %s", field, encoded)
		}
	}
	for _, secret := range []string{
		token,
		string(privateKey),
		base64.RawURLEncoding.EncodeToString(privateKey),
		base64.StdEncoding.EncodeToString(privateKey),
		hex.EncodeToString(privateKey),
	} {
		if secret != "" && bytes.Contains(encoded, []byte(secret)) {
			t.Fatal("MarshalHistory() persisted token or private-key material")
		}
	}
	if bytes.Contains(encoded, publicKey) {
		t.Fatal("MarshalHistory() unexpectedly persisted public-key material")
	}

	for _, metadata := range []string{
		payload.LicenseID,
		payload.Product,
		payload.Channel,
		payload.MachineID,
		payload.Owner,
		payload.Comment,
		payload.IssuedAt,
	} {
		if !bytes.Contains(encoded, []byte(metadata)) {
			t.Fatalf("MarshalHistory() omitted metadata %q: %s", metadata, encoded)
		}
	}
}

func TestHistoryRoundTripPreservesIssuanceMetadata(t *testing.T) {
	payload := testPayload()
	payload.ExpiresAt = "2027-07-01T00:00:00Z"
	entry, err := NewHistoryEntry(payload)
	if err != nil {
		t.Fatalf("NewHistoryEntry() error = %v", err)
	}

	encoded, err := MarshalHistory([]HistoryEntry{entry})
	if err != nil {
		t.Fatalf("MarshalHistory() error = %v", err)
	}
	got, err := UnmarshalHistory(encoded)
	if err != nil {
		t.Fatalf("UnmarshalHistory() error = %v", err)
	}
	if len(got) != 1 || got[0] != entry {
		t.Fatalf("UnmarshalHistory() = %#v, want %#v", got, []HistoryEntry{entry})
	}
	if got[0].payload() != payload {
		t.Fatalf("HistoryEntry payload = %#v, want %#v", got[0].payload(), payload)
	}
}

func TestMarshalHistoryUsesStableEmptyDocument(t *testing.T) {
	encoded, err := MarshalHistory(nil)
	if err != nil {
		t.Fatalf("MarshalHistory() error = %v", err)
	}
	const want = `{"schema":1,"entries":[]}`
	if string(encoded) != want {
		t.Fatalf("MarshalHistory(nil) = %q, want %q", encoded, want)
	}
}

func TestNewHistoryEntryRejectsInvalidPayload(t *testing.T) {
	payload := testPayload()
	payload.MachineID = ""

	_, err := NewHistoryEntry(payload)
	if !errors.Is(err, license.ErrMalformed) {
		t.Fatalf("NewHistoryEntry() error = %v, want license.ErrMalformed", err)
	}
}

func TestUnmarshalHistoryRejectsUnknownOrSecretFieldsWithSafeError(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{
			name: "token",
			json: `{"schema":1,"entries":[{"license_id":"id","product":"p","channel":"c","machine_id":"m","owner":"o","issued_at":"2026-07-01T00:00:00Z","token":"TCPLIC1.secret"}]}`,
		},
		{
			name: "private key",
			json: `{"schema":1,"entries":[{"license_id":"id","product":"p","channel":"c","machine_id":"m","owner":"o","issued_at":"2026-07-01T00:00:00Z","private_key":"secret-private-key"}]}`,
		},
		{
			name: "unknown document field",
			json: `{"schema":1,"entries":[],"secret":"value"}`,
		},
		{
			name: "unsupported schema",
			json: `{"schema":2,"entries":[]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entries, err := UnmarshalHistory([]byte(test.json))
			if entries != nil {
				t.Fatalf("UnmarshalHistory() entries = %#v on failure", entries)
			}
			if !errors.Is(err, ErrInvalidHistory) {
				t.Fatalf("UnmarshalHistory() error = %v, want ErrInvalidHistory", err)
			}
			if strings.Contains(err.Error(), test.json) || strings.Contains(err.Error(), "secret-private-key") {
				t.Fatal("UnmarshalHistory() error exposes persisted input")
			}
		})
	}
}
