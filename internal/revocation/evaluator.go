package revocation

import (
	"bytes"
	"errors"
	"sort"
	"time"
)

const (
	OfflineGracePeriod = 24 * time.Hour
	ClockRollbackLimit = 5 * time.Minute
)

type Decision string

const (
	Active        Decision = "active"
	ActiveInGrace Decision = "active_in_grace"
	CheckRequired Decision = "check_required"
	Revoked       Decision = "revoked"
)

type FetchResult struct {
	Manifest *VerifiedManifest
	Err      error
}

var errInvalidEvaluation = errors.New("revocation: invalid evaluation")

func Evaluate(handle Handle, current SecureState, fetch FetchResult, now time.Time) (Decision, SecureState, error) {
	if !canonicalStateTime(now) {
		return CheckRequired, SecureState{}, errInvalidEvaluation
	}
	state, err := canonicalSecureState(current)
	if err != nil {
		return CheckRequired, SecureState{}, errInvalidEvaluation
	}
	if containsReceipt(state.RevokedHandles, handle) {
		state.LastWallUTC = laterTime(state.LastWallUTC, now)
		return Revoked, state, nil
	}

	rollback := !state.LastWallUTC.IsZero() && now.Before(state.LastWallUTC.Add(-ClockRollbackLimit))
	manifest, validFetch := validatedFetch(fetch, state)
	if validFetch {
		state.Schema = SecureStateSchema
		state.HighestSequence = manifest.Sequence()
		state.LastSuccessUTC = now
		state.LastWallUTC = laterTime(state.LastWallUTC, now)
		state.ManifestDigest = manifest.Digest()
		if manifest.Contains(handle) {
			if len(state.RevokedHandles) >= MaxRevokedReceipts {
				return CheckRequired, state, errInvalidEvaluation
			}
			state.RevokedHandles = canonicalHandles(append(state.RevokedHandles, handle))
			return Revoked, state, nil
		}
		return Active, state, nil
	}

	if !state.LastSuccessUTC.IsZero() {
		state.LastWallUTC = laterTime(state.LastWallUTC, now)
	}
	if rollback || state.LastSuccessUTC.IsZero() || !now.Before(state.LastSuccessUTC.Add(OfflineGracePeriod)) {
		return CheckRequired, state, nil
	}
	return ActiveInGrace, state, nil
}

func validatedFetch(fetch FetchResult, state SecureState) (VerifiedManifest, bool) {
	if fetch.Manifest == nil || fetch.Err != nil {
		return VerifiedManifest{}, false
	}
	manifest := *fetch.Manifest
	if !manifest.valid() {
		return VerifiedManifest{}, false
	}
	if !state.LastSuccessUTC.IsZero() {
		if manifest.Sequence() < state.HighestSequence {
			return VerifiedManifest{}, false
		}
		if manifest.Sequence() == state.HighestSequence && state.ManifestDigest != manifest.Digest() {
			return VerifiedManifest{}, false
		}
	}
	return manifest, true
}

func containsReceipt(receipts []Handle, handle Handle) bool {
	index := sort.Search(len(receipts), func(index int) bool {
		return bytes.Compare(receipts[index][:], handle[:]) >= 0
	})
	return index < len(receipts) && receipts[index] == handle
}

func laterTime(left, right time.Time) time.Time {
	if left.IsZero() || right.After(left) {
		return right
	}
	return left
}
