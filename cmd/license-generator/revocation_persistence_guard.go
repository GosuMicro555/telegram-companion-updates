package main

import (
	"crypto/sha256"
	"crypto/subtle"
)

type revocationStateBaseline struct {
	initialized bool
	exists      bool
	digest      [sha256.Size]byte
}

func (baseline *revocationStateBaseline) Observe(encoded []byte, exists bool) {
	if baseline == nil {
		return
	}
	baseline.initialized = true
	baseline.exists = exists
	baseline.digest = [sha256.Size]byte{}
	if exists {
		baseline.digest = sha256.Sum256(encoded)
	}
}

func (baseline revocationStateBaseline) Matches(encoded []byte, exists bool) bool {
	if !baseline.initialized || baseline.exists != exists {
		return false
	}
	if !exists {
		return true
	}
	digest := sha256.Sum256(encoded)
	return subtle.ConstantTimeCompare(baseline.digest[:], digest[:]) == 1
}
