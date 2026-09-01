package revocation

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSecureStateCanonicalRoundTrip(t *testing.T) {
	handleB, _ := DeriveHandle("license-b")
	handleA, _ := DeriveHandle("license-a")
	state := SecureState{
		Schema:          SecureStateSchema,
		HighestSequence: 7,
		LastSuccessUTC:  time.Date(2026, 8, 25, 1, 2, 3, 0, time.UTC),
		LastWallUTC:     time.Date(2026, 8, 25, 1, 3, 0, 0, time.UTC),
		ManifestDigest:  [32]byte{1, 2, 3},
		RevokedHandles:  []Handle{handleB, handleA, handleB},
	}
	encoded, err := MarshalSecureState(state)
	if err != nil {
		t.Fatalf("MarshalSecureState: %v", err)
	}
	decoded, err := UnmarshalSecureState(encoded)
	if err != nil {
		t.Fatalf("UnmarshalSecureState: %v", err)
	}
	if decoded.Schema != SecureStateSchema || decoded.HighestSequence != 7 || len(decoded.RevokedHandles) != 2 {
		t.Fatalf("decoded state = %#v", decoded)
	}
	if bytes.Compare(decoded.RevokedHandles[0][:], decoded.RevokedHandles[1][:]) >= 0 {
		t.Fatal("revoked receipts are not canonical sorted unique")
	}
	reencoded, err := MarshalSecureState(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatal("secure-state encoding is not canonical")
	}
}

func TestSecureStateRejectsMalformedOrNonCanonicalBytes(t *testing.T) {
	handle, _ := DeriveHandle("license-state")
	valid, err := MarshalSecureState(SecureState{
		Schema:          SecureStateSchema,
		HighestSequence: 1,
		LastSuccessUTC:  time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC),
		LastWallUTC:     time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC),
		ManifestDigest:  [32]byte{9},
		RevokedHandles:  []Handle{handle},
	})
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(valid, &object); err != nil {
		t.Fatal(err)
	}
	object["extra"] = true
	unknown, _ := json.Marshal(object)

	cases := [][]byte{
		nil,
		append(append([]byte(nil), valid...), '\n'),
		unknown,
		[]byte(`{"schema":1,"highest_sequence":0,"last_success_utc":"","last_wall_utc":"","manifest_digest":"","revoked_handles":null}`),
		bytes.Repeat([]byte{'x'}, MaxSecureStateBytes+1),
	}
	for _, value := range cases {
		if _, err := UnmarshalSecureState(value); err == nil {
			t.Fatalf("UnmarshalSecureState accepted %d bytes", len(value))
		}
	}

	tooMany := make([]Handle, MaxRevokedReceipts+1)
	for index := range tooMany {
		copy(tooMany[index][:], strings.Repeat("x", len(tooMany[index])))
		tooMany[index][30] = byte(index >> 8)
		tooMany[index][31] = byte(index)
	}
	if _, err := MarshalSecureState(SecureState{Schema: SecureStateSchema, RevokedHandles: tooMany}); err == nil {
		t.Fatal("receipt cap was not enforced")
	}
}
