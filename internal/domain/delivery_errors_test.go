package domain

import (
	"errors"
	"fmt"
	"testing"
)

type privateMessageAccessError struct {
	message string
}

func (e *privateMessageAccessError) Error() string {
	return e.message
}

func TestPrivateMessageClosedNil(t *testing.T) {
	if got := PrivateMessageClosed(nil); got != nil {
		t.Fatalf("PrivateMessageClosed(nil) = %v, want nil", got)
	}
}

func TestPrivateMessageClosedClassifiesWrappedErrorAndPreservesCause(t *testing.T) {
	cause := &privateMessageAccessError{message: "privacy restricted"}
	wrapped := fmt.Errorf("send private message: %w", cause)
	err := PrivateMessageClosed(wrapped)

	if !IsPrivateMessageClosed(err) {
		t.Fatal("IsPrivateMessageClosed() = false, want true")
	}
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is() = false, want true for wrapped cause")
	}

	var target *privateMessageAccessError
	if !errors.As(err, &target) {
		t.Fatal("errors.As() = false, want true for wrapped cause")
	}
	if target != cause {
		t.Fatal("errors.As() returned a cause other than the wrapped error")
	}
}

func TestPrivateMessageClosedRemainsDistinctFromDeliveryFailureClasses(t *testing.T) {
	err := PrivateMessageClosed(errors.New("user privacy restricted"))

	if IsPermanentDeliveryFailure(err) {
		t.Fatal("closed DM must not be a permanent delivery failure")
	}
	if IsTransientDeliveryFailure(err) {
		t.Fatal("closed DM must not be a transient delivery failure")
	}
}
