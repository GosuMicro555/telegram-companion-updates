package usecase

import (
	"testing"
	"time"

	"telegram-companion/internal/domain"
)

func TestMemoryLimiterEnforcesGlobalInterval(t *testing.T) {
	limiter := NewMemoryLimiter(2*time.Second, 19)
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	if !limiter.Allow(domain.ID("a1"), now) {
		t.Fatal("first send denied")
	}
	if limiter.Allow(domain.ID("a2"), now.Add(time.Second)) {
		t.Fatal("second send within 2s allowed")
	}
	if !limiter.Allow(domain.ID("a2"), now.Add(2*time.Second)) {
		t.Fatal("send after 2s denied")
	}
}

func TestMemoryLimiterEnforcesPerAccountMinute(t *testing.T) {
	limiter := NewMemoryLimiter(0, 2)
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	if !limiter.Allow("a1", now) || !limiter.Allow("a1", now.Add(time.Second)) {
		t.Fatal("first two sends should be allowed")
	}
	if limiter.Allow("a1", now.Add(2*time.Second)) {
		t.Fatal("third send in one minute allowed")
	}
	if !limiter.Allow("a1", now.Add(61*time.Second)) {
		t.Fatal("send after window denied")
	}
}
