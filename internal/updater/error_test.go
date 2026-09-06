package updater

import (
	"errors"
	"testing"
)

func TestCodedErrorExposesOnlyAnAllowlistedCode(t *testing.T) {
	cause := errors.New("Sparkle private path /Users/test/secret.zip")
	err := NewCodedError(CodeSignatureInvalid, cause)

	if got := ErrorCodeOf(err); got != CodeSignatureInvalid {
		t.Fatalf("ErrorCodeOf() = %q, want %q", got, CodeSignatureInvalid)
	}
	if !errors.Is(err, cause) {
		t.Fatal("coded error no longer unwraps its internal cause")
	}
	if got := err.Error(); got != "update signature verification failed" {
		t.Fatalf("coded error text = %q, want a safe message", got)
	}
	if got := err.Error(); containsSensitiveUpdateText(got) {
		t.Fatalf("coded error leaked sensitive detail: %q", got)
	}
}

func TestUnknownCodedErrorFallsBackToGenericCode(t *testing.T) {
	err := NewCodedError(ErrorCode("private_path"), errors.New("private path"))
	if got := ErrorCodeOf(err); got != "" {
		t.Fatalf("ErrorCodeOf(unknown) = %q, want empty", got)
	}
}

func containsSensitiveUpdateText(value string) bool {
	for _, fragment := range []string{"/Users/", "secret.zip", "private path"} {
		if len(value) >= len(fragment) {
			for i := 0; i+len(fragment) <= len(value); i++ {
				if value[i:i+len(fragment)] == fragment {
					return true
				}
			}
		}
	}
	return false
}
