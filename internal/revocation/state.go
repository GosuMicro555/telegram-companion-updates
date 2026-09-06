package revocation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"time"
)

const (
	SecureStateSchema   uint64 = 1
	MaxSecureStateBytes        = 1_048_576
	MaxRevokedReceipts         = MaxEntries
)

var errInvalidSecureState = errors.New("revocation: invalid secure state")

type SecureState struct {
	Schema          uint64
	HighestSequence uint64
	LastSuccessUTC  time.Time
	LastWallUTC     time.Time
	ManifestDigest  [32]byte
	RevokedHandles  []Handle
}

type StateStore interface {
	Load(context.Context) (SecureState, error)
	Save(context.Context, SecureState) error
}

type persistedSecureState struct {
	Schema          uint64   `json:"schema"`
	HighestSequence uint64   `json:"highest_sequence"`
	LastSuccessUTC  string   `json:"last_success_utc"`
	LastWallUTC     string   `json:"last_wall_utc"`
	ManifestDigest  string   `json:"manifest_digest"`
	RevokedHandles  []string `json:"revoked_handles"`
}

func MarshalSecureState(state SecureState) ([]byte, error) {
	canonical, err := canonicalSecureState(state)
	if err != nil {
		return nil, err
	}
	persisted, err := persistedState(canonical)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(persisted)
	if err != nil || len(encoded) > MaxSecureStateBytes {
		return nil, errInvalidSecureState
	}
	return encoded, nil
}

func UnmarshalSecureState(encoded []byte) (SecureState, error) {
	if len(encoded) == 0 || len(encoded) > MaxSecureStateBytes {
		return SecureState{}, errInvalidSecureState
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var persisted persistedSecureState
	if err := decoder.Decode(&persisted); err != nil {
		return SecureState{}, errInvalidSecureState
	}
	if err := requireSecureStateEOF(decoder); err != nil {
		return SecureState{}, errInvalidSecureState
	}
	state, err := stateFromPersisted(persisted)
	if err != nil {
		return SecureState{}, err
	}
	reencoded, err := MarshalSecureState(state)
	if err != nil || !bytes.Equal(reencoded, encoded) {
		return SecureState{}, errInvalidSecureState
	}
	return state, nil
}

func canonicalSecureState(state SecureState) (SecureState, error) {
	if state.Schema == 0 {
		state.Schema = SecureStateSchema
	}
	if state.Schema != SecureStateSchema || len(state.RevokedHandles) > MaxRevokedReceipts {
		return SecureState{}, errInvalidSecureState
	}
	if !validStateTimes(state.LastSuccessUTC, state.LastWallUTC) {
		return SecureState{}, errInvalidSecureState
	}
	if state.LastSuccessUTC.IsZero() {
		if state.HighestSequence != 0 || state.ManifestDigest != ([32]byte{}) || len(state.RevokedHandles) != 0 {
			return SecureState{}, errInvalidSecureState
		}
	} else if state.ManifestDigest == ([32]byte{}) {
		return SecureState{}, errInvalidSecureState
	}
	state.RevokedHandles = canonicalHandles(state.RevokedHandles)
	return state, nil
}

func canonicalHandles(handles []Handle) []Handle {
	canonical := append([]Handle(nil), handles...)
	sort.Slice(canonical, func(left, right int) bool {
		return bytes.Compare(canonical[left][:], canonical[right][:]) < 0
	})
	output := make([]Handle, 0, len(canonical))
	for _, handle := range canonical {
		if len(output) == 0 || output[len(output)-1] != handle {
			output = append(output, handle)
		}
	}
	return output
}

func validStateTimes(lastSuccess, lastWall time.Time) bool {
	if lastSuccess.IsZero() || lastWall.IsZero() {
		return lastSuccess.IsZero() && lastWall.IsZero()
	}
	return canonicalStateTime(lastSuccess) && canonicalStateTime(lastWall) && !lastWall.Before(lastSuccess)
}

func canonicalStateTime(value time.Time) bool {
	return value.Location() == time.UTC && value.Nanosecond() == 0
}

func persistedState(state SecureState) (persistedSecureState, error) {
	persisted := persistedSecureState{
		Schema:          state.Schema,
		HighestSequence: state.HighestSequence,
		RevokedHandles:  make([]string, len(state.RevokedHandles)),
	}
	if !state.LastSuccessUTC.IsZero() {
		persisted.LastSuccessUTC = state.LastSuccessUTC.Format(manifestTimeLayout)
		persisted.LastWallUTC = state.LastWallUTC.Format(manifestTimeLayout)
		persisted.ManifestDigest = base64.RawURLEncoding.EncodeToString(state.ManifestDigest[:])
	}
	for index, handle := range state.RevokedHandles {
		persisted.RevokedHandles[index] = handle.String()
	}
	return persisted, nil
}

func stateFromPersisted(persisted persistedSecureState) (SecureState, error) {
	if persisted.Schema != SecureStateSchema || persisted.RevokedHandles == nil || len(persisted.RevokedHandles) > MaxRevokedReceipts {
		return SecureState{}, errInvalidSecureState
	}
	state := SecureState{Schema: persisted.Schema, HighestSequence: persisted.HighestSequence, RevokedHandles: make([]Handle, len(persisted.RevokedHandles))}
	if persisted.LastSuccessUTC == "" || persisted.LastWallUTC == "" || persisted.ManifestDigest == "" {
		if persisted.LastSuccessUTC != "" || persisted.LastWallUTC != "" || persisted.ManifestDigest != "" {
			return SecureState{}, errInvalidSecureState
		}
	} else {
		lastSuccess, ok := parseManifestTime(persisted.LastSuccessUTC)
		if !ok {
			return SecureState{}, errInvalidSecureState
		}
		lastWall, ok := parseManifestTime(persisted.LastWallUTC)
		if !ok {
			return SecureState{}, errInvalidSecureState
		}
		digest, ok := decodeCanonicalBase64URL(persisted.ManifestDigest, 32)
		if !ok || len(digest) != 32 {
			return SecureState{}, errInvalidSecureState
		}
		state.LastSuccessUTC = lastSuccess
		state.LastWallUTC = lastWall
		copy(state.ManifestDigest[:], digest)
	}
	for index, value := range persisted.RevokedHandles {
		handle, err := ParseHandle(value)
		if err != nil {
			return SecureState{}, errInvalidSecureState
		}
		state.RevokedHandles[index] = handle
	}
	canonical, err := canonicalSecureState(state)
	if err != nil || len(canonical.RevokedHandles) != len(state.RevokedHandles) {
		return SecureState{}, errInvalidSecureState
	}
	for index := range canonical.RevokedHandles {
		if canonical.RevokedHandles[index] != state.RevokedHandles[index] {
			return SecureState{}, errInvalidSecureState
		}
	}
	return state, nil
}

func requireSecureStateEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errInvalidSecureState
	}
	return nil
}
