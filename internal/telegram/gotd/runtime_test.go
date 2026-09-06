package gotd

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/usecase/runtimeconfig"
)

type runtimeManagerFake struct {
	mu       sync.Mutex
	revision uint64
	applied  chan uint64
}

func (m *runtimeManagerFake) Apply(snapshot runtimeconfig.Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if snapshot.Revision <= m.revision {
		return ErrStaleRevision
	}
	m.revision = snapshot.Revision
	m.applied <- snapshot.Revision
	return nil
}
func (m *runtimeManagerFake) Run(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }

func TestRuntimeRunnerAppliesInitialAndHotRevisionsAcrossRestart(t *testing.T) {
	store := runtimeconfig.NewStore(runtimeconfig.Snapshot{SharedReply: "old"})
	manager := &runtimeManagerFake{applied: make(chan uint64, 4)}
	runner := NewRuntimeRunner(manager, store)

	run := func() context.CancelFunc {
		ctx, cancel := context.WithCancel(context.Background())
		go func() { _ = runner.Run(ctx) }()
		return cancel
	}
	cancel := run()
	require.Equal(t, uint64(1), receiveRevision(t, manager.applied))
	require.Equal(t, uint64(2), store.Update(func(next *runtimeconfig.Snapshot) { next.SharedReply = "new" }))
	require.Equal(t, uint64(2), receiveRevision(t, manager.applied))
	cancel()
	time.Sleep(20 * time.Millisecond)

	cancel = run()
	require.Equal(t, uint64(3), store.Update(func(next *runtimeconfig.Snapshot) { next.SharedReply = "latest" }))
	require.Equal(t, uint64(3), receiveRevision(t, manager.applied))
	cancel()
}

func receiveRevision(t *testing.T, revisions <-chan uint64) uint64 {
	t.Helper()
	select {
	case revision := <-revisions:
		return revision
	case <-time.After(time.Second):
		t.Fatal("runtime revision not applied")
		return 0
	}
}
