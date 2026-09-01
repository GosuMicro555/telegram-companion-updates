package gotd

import (
	"context"
	"errors"
	"sync"

	"github.com/gotd/td/session"
)

// SessionBarrier is shared by gotd session persistence and backup snapshots.
// Session reads and writes are serialized with snapshots so no partial session
// file can enter an archive.
type SessionBarrier struct {
	gate chan struct{}
}

var productionSessionBarrier = NewSessionBarrier()

func NewSessionBarrier() *SessionBarrier {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &SessionBarrier{gate: gate}
}

func ProductionSessionBarrier() *SessionBarrier { return productionSessionBarrier }

func (b *SessionBarrier) AcquireRead(ctx context.Context) (func(), error) {
	return b.acquire(ctx)
}

func (b *SessionBarrier) AcquireWrite(ctx context.Context) (func(), error) {
	return b.acquire(ctx)
}

func (b *SessionBarrier) acquire(ctx context.Context) (func(), error) {
	if b == nil || b.gate == nil {
		return nil, errors.New("gotd session barrier is not configured")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.gate:
	}
	var once sync.Once
	return func() { once.Do(func() { b.gate <- struct{}{} }) }, nil
}

type barrierSessionStorage struct {
	storage session.Storage
	barrier *SessionBarrier
}

func newBarrierSessionStorage(storage session.Storage, barrier *SessionBarrier) session.Storage {
	return &barrierSessionStorage{storage: storage, barrier: barrier}
}

func (s *barrierSessionStorage) LoadSession(ctx context.Context) ([]byte, error) {
	if s == nil || s.storage == nil || s.barrier == nil {
		return nil, errors.New("gotd session storage is not configured")
	}
	release, err := s.barrier.AcquireRead(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.storage.LoadSession(ctx)
}

func (s *barrierSessionStorage) StoreSession(ctx context.Context, data []byte) error {
	if s == nil || s.storage == nil || s.barrier == nil {
		return errors.New("gotd session storage is not configured")
	}
	release, err := s.barrier.AcquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	return s.storage.StoreSession(ctx, data)
}
