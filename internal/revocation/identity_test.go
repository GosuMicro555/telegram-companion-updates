package revocation

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestDeriveHandleGolden(t *testing.T) {
	handle, err := DeriveHandle("license-0123456789abcdef")
	if err != nil {
		t.Fatalf("DeriveHandle: %v", err)
	}
	if got, want := hex.EncodeToString(handle[:]), "a8f8d55bc3d46861990888c9f3a4290eccb494d56adcc8be3145fc44e4a3c198"; got != want {
		t.Fatalf("digest = %q, want %q", got, want)
	}
	if got, want := handle.String(), "qPjVW8PUaGGZCIjJ86QpDsy0lNVq3Mi-MUX8ROSjwZg"; got != want {
		t.Fatalf("String = %q, want %q", got, want)
	}
	parsed, err := ParseHandle(handle.String())
	if err != nil {
		t.Fatalf("ParseHandle: %v", err)
	}
	if parsed != handle {
		t.Fatal("parsed handle differs")
	}
}

func TestDeriveHandleRejectsInvalidLicenseIDs(t *testing.T) {
	for _, value := range []string{
		"",
		" license-id",
		"license-id ",
		string([]byte{0xff}),
		strings.Repeat("x", maxLicenseIDBytes+1),
	} {
		if _, err := DeriveHandle(value); err == nil {
			t.Fatalf("DeriveHandle(%q) succeeded", value)
		}
	}
}

func TestParseHandleRequiresCanonicalUnpaddedBase64URL(t *testing.T) {
	valid, err := DeriveHandle("license-valid")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		valid.String() + "=",
		"",
		"abcd",
		strings.Repeat("a", 44),
	} {
		if _, err := ParseHandle(value); err == nil {
			t.Fatalf("ParseHandle(%q) succeeded", value)
		}
	}
}
