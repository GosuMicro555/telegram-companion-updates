package revocation

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"time"
)

var errInvalidChecker = errors.New("revocation: check failed")

// Checker owns the complete load-fetch-verify-evaluate-save transition. The
// transition is serialized so concurrent foreground and supervisor checks
// cannot overwrite a newer secure state with an older one.
type Checker struct {
	mu        sync.Mutex
	store     StateStore
	fetcher   Fetcher
	keyID     string
	publicKey ed25519.PublicKey
	now       func() time.Time
}

func NewChecker(store StateStore, fetcher Fetcher, keyID string, publicKey ed25519.PublicKey, now func() time.Time) (*Checker, error) {
	if store == nil || fetcher == nil || !validKeyID(keyID) || len(publicKey) != ed25519.PublicKeySize || now == nil {
		return nil, errInvalidChecker
	}
	return &Checker{
		store:     store,
		fetcher:   fetcher,
		keyID:     keyID,
		publicKey: append(ed25519.PublicKey(nil), publicKey...),
		now:       now,
	}, nil
}

func (checker *Checker) Check(ctx context.Context, licenseID string) (Decision, error) {
	if checker == nil {
		return CheckRequired, errInvalidChecker
	}
	if err := ctx.Err(); err != nil {
		return CheckRequired, err
	}

	checker.mu.Lock()
	defer checker.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return CheckRequired, err
	}

	handle, err := DeriveHandle(licenseID)
	if err != nil {
		return CheckRequired, errInvalidChecker
	}
	now := checker.now().UTC().Truncate(time.Second)
	if !canonicalStateTime(now) {
		return CheckRequired, errInvalidChecker
	}
	current, err := checker.store.Load(ctx)
	if err != nil {
		return CheckRequired, contextOrCheckError(ctx)
	}
	if _, err := canonicalSecureState(current); err != nil {
		return CheckRequired, errInvalidChecker
	}

	fetch := FetchResult{}
	envelope, fetchErr := checker.fetcher.Fetch(ctx)
	if err := ctx.Err(); err != nil {
		return CheckRequired, err
	}
	if fetchErr != nil {
		fetch.Err = fetchErr
	} else {
		manifest, verifyErr := Verify(string(envelope), checker.keyID, checker.publicKey)
		if verifyErr != nil {
			fetch.Err = verifyErr
		} else {
			fetch.Manifest = &manifest
		}
	}

	decision, next, evaluateErr := Evaluate(handle, current, fetch, now)
	if _, err := canonicalSecureState(next); err != nil {
		return CheckRequired, errInvalidChecker
	}
	if err := checker.store.Save(ctx, next); err != nil {
		return CheckRequired, contextOrCheckError(ctx)
	}
	if evaluateErr != nil {
		return CheckRequired, errInvalidChecker
	}
	return decision, nil
}

func contextOrCheckError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errInvalidChecker
}
