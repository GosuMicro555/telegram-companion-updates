package logger

import "testing"

func TestNewAcceptsKnownLevels(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		if _, err := New(level); err != nil {
			t.Fatalf("New(%q) returned error: %v", level, err)
		}
	}
}

func TestNewRejectsUnknownLevel(t *testing.T) {
	if _, err := New("verbose"); err == nil {
		t.Fatal("New succeeded, want error")
	}
}
