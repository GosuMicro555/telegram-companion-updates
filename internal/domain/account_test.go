package domain

import "testing"

func TestAccountEligibleKeepsRuntimeTransientStatesManaged(t *testing.T) {
	for _, status := range []AccountStatus{"joining", "ready", "partial"} {
		if !(Account{Status: status}).Eligible() {
			t.Fatalf("status %q must remain eligible while runtime reconnects", status)
		}
	}
	for _, status := range []AccountStatus{AccountPaused, AccountError, AccountLimited, AccountFloodWait} {
		if (Account{Status: status}).Eligible() {
			t.Fatalf("status %q must not be eligible", status)
		}
	}
}

func TestUnassignedAccountIsNeverEligible(t *testing.T) {
	for _, status := range []AccountStatus{AccountActive, AccountStopped, "ready", "joining", "partial"} {
		if (Account{Status: status, ProxyMode: ProxyModeUnassigned}).Eligible() {
			t.Fatalf("unassigned account with status %q must not be eligible", status)
		}
	}
}

func TestAccountEffectiveNextDeliveryDefaultsToPrivate(t *testing.T) {
	if got := (Account{}).EffectiveNextDelivery(); got != DeliveryTargetPrivate {
		t.Fatalf("EffectiveNextDelivery() = %q, want %q", got, DeliveryTargetPrivate)
	}
}

func TestAccountEffectiveNextDeliveryKeepsExplicitPublicCursor(t *testing.T) {
	account := Account{NextDelivery: DeliveryTargetPublic}
	if got := account.EffectiveNextDelivery(); got != DeliveryTargetPublic {
		t.Fatalf("EffectiveNextDelivery() = %q, want %q", got, DeliveryTargetPublic)
	}
}
