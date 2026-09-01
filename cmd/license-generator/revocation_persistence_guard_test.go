package main

import "testing"

func TestRevocationStateBaselineRejectsStaleWholeStateReplacement(t *testing.T) {
	initial := []byte("protected-revocation-state-v1")
	updated := []byte("protected-revocation-state-v2")
	var first, stale revocationStateBaseline
	first.Observe(initial, true)
	stale.Observe(initial, true)
	if !first.Matches(initial, true) || !stale.Matches(initial, true) {
		t.Fatal("fresh baselines do not match the state they observed")
	}
	first.Observe(updated, true)
	if stale.Matches(updated, true) {
		t.Fatal("stale baseline accepted a whole-state replacement after another writer")
	}
	if !first.Matches(updated, true) {
		t.Fatal("successful writer did not advance its own baseline")
	}
}

func TestRevocationStateBaselineDistinguishesMissingAndPresentState(t *testing.T) {
	var baseline revocationStateBaseline
	baseline.Observe(nil, false)
	if !baseline.Matches(nil, false) || baseline.Matches([]byte("state"), true) {
		t.Fatal("missing baseline did not fail closed when state appeared")
	}
}
