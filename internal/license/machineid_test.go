package license

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestDeriveMachineIDNormalizesAndDomainSeparates(t *testing.T) {
	const raw = "  9f85f7b1-6c2a-4aa6-a615-b607820c250d  "
	got := DeriveMachineID(raw)
	wantHash := sha256.Sum256([]byte("telegram-companion:machine:v1:9F85F7B1-6C2A-4AA6-A615-B607820C250D"))
	want := strings.ToUpper(hex.EncodeToString(wantHash[:]))

	if got != want {
		t.Fatalf("DeriveMachineID() = %q, want %q", got, want)
	}
	if got != DeriveMachineID(strings.ToUpper(strings.TrimSpace(raw))) {
		t.Fatal("DeriveMachineID() did not normalize casing and surrounding whitespace")
	}
	plainHash := sha256.Sum256([]byte("9F85F7B1-6C2A-4AA6-A615-B607820C250D"))
	if got == strings.ToUpper(hex.EncodeToString(plainHash[:])) {
		t.Fatal("DeriveMachineID() is missing its domain separation prefix")
	}
}

func TestMachineIDErrorDoesNotLeakRawIdentifier(t *testing.T) {
	const raw = "\x00do-not-leak-this-platform-identifier"
	_, err := machineIDFromRaw(raw)
	if err != nil {
		if strings.Contains(err.Error(), raw) {
			t.Fatalf("machine ID error leaked raw identifier: %v", err)
		}
		return
	}
	t.Fatal("machineIDFromRaw() unexpectedly accepted an invalid platform identifier")
}
