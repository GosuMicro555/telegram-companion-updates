package gotd

import (
	"context"
	"errors"

	"golang.org/x/sync/errgroup"

	"telegram-companion/internal/usecase/runtimeconfig"
)

type RuntimeManager interface {
	Run(context.Context) error
	Apply(runtimeconfig.Snapshot) error
}

// RuntimeRunner keeps manager revisions hot while preserving Manager's
// restartable START/STOP lifecycle.
type RuntimeRunner struct {
	manager RuntimeManager
	config  *runtimeconfig.Store
}

func NewRuntimeRunner(manager RuntimeManager, config *runtimeconfig.Store) *RuntimeRunner {
	return &RuntimeRunner{manager: manager, config: config}
}

func (r *RuntimeRunner) Run(ctx context.Context) error {
	if r == nil || r.manager == nil || r.config == nil {
		return errors.New("gotd runtime runner is not configured")
	}
	current := r.config.Current()
	if err := r.manager.Apply(current); err != nil && !errors.Is(err, ErrStaleRevision) {
		return err
	}
	updates, unsubscribe := r.config.Subscribe(current.Revision)
	defer unsubscribe()
	group, runCtx := errgroup.WithContext(ctx)
	group.Go(func() error { return r.manager.Run(runCtx) })
	group.Go(func() error {
		for {
			select {
			case <-runCtx.Done():
				return runCtx.Err()
			case snapshot, ok := <-updates:
				if !ok {
					return runCtx.Err()
				}
				if err := r.manager.Apply(snapshot); err != nil && !errors.Is(err, ErrStaleRevision) {
					return err
				}
			}
		}
	})
	return group.Wait()
}
