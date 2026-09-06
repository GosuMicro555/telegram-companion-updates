package usecase

import (
	"sync"
	"time"

	"telegram-companion/internal/domain"
)

type MemoryLimiter struct {
	mu           sync.Mutex
	interval     time.Duration
	maxPerMinute int
	lastGlobal   time.Time
	perAccount   map[domain.ID][]time.Time
}

func NewMemoryLimiter(interval time.Duration, maxPerMinute int) *MemoryLimiter {
	return &MemoryLimiter{
		interval:     interval,
		maxPerMinute: maxPerMinute,
		perAccount:   make(map[domain.ID][]time.Time),
	}
}

func (l *MemoryLimiter) Allow(accountID domain.ID, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.lastGlobal.IsZero() && now.Sub(l.lastGlobal) < l.interval {
		return false
	}

	windowStart := now.Add(-time.Minute)
	history := l.perAccount[accountID]
	kept := history[:0]
	for _, ts := range history {
		if ts.After(windowStart) {
			kept = append(kept, ts)
		}
	}
	if len(kept) >= l.maxPerMinute {
		l.perAccount[accountID] = kept
		return false
	}

	kept = append(kept, now)
	l.perAccount[accountID] = kept
	l.lastGlobal = now
	return true
}
