package gotd

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type sessionStorageStub struct {
	mu     sync.Mutex
	stored []byte
}

func (*sessionStorageStub) LoadSession(context.Context) ([]byte, error) { return nil, nil }

func (s *sessionStorageStub) StoreSession(_ context.Context, data []byte) error {
	s.mu.Lock()
	s.stored = append([]byte(nil), data...)
	s.mu.Unlock()
	return nil
}

func TestBarrierSessionStorageWriteCannotOverlapBackupRead(t *testing.T) {
	barrier := NewSessionBarrier()
	releaseBackup, err := barrier.AcquireRead(context.Background())
	require.NoError(t, err)
	storage := &sessionStorageStub{}
	coordinated := newBarrierSessionStorage(storage, barrier)
	done := make(chan error, 1)
	go func() { done <- coordinated.StoreSession(context.Background(), []byte("new-session")) }()

	select {
	case err := <-done:
		t.Fatalf("gotd session write overlapped backup read: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseBackup()
	require.NoError(t, <-done)
	require.Equal(t, []byte("new-session"), storage.stored)
}

func TestDefaultGotdBuilderUsesProductionSessionBarrier(t *testing.T) {
	factory := NewClientFactory(1, "hash", &recordingValidationStore{})
	builder, ok := factory.builder.(gotdRuntimeBuilder)
	require.True(t, ok)
	require.Same(t, ProductionSessionBarrier(), builder.sessionBarrier)
}
