package backup

import (
	"context"
	"sync"
)

// SessionBarrier coordinates gotd session-store snapshots and writes. Both
// operations are exclusive because a backup must observe a complete file set.
type SessionBarrier struct {
	gate chan struct{}
}

func NewSessionBarrier() *SessionBarrier {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &SessionBarrier{gate: gate}
}

func (b *SessionBarrier) AcquireRead(ctx context.Context) (func(), error) {
	return b.acquire(ctx)
}

func (b *SessionBarrier) AcquireWrite(ctx context.Context) (func(), error) {
	return b.acquire(ctx)
}

func (b *SessionBarrier) acquire(ctx context.Context) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.gate:
	}
	var once sync.Once
	return func() {
		once.Do(func() { b.gate <- struct{}{} })
	}, nil
}
