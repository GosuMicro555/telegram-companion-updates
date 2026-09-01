//go:build desktop

package main

import (
	"context"
	"errors"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/options"
)

const desktopSingleInstanceID = "b9f6d837-c30e-46e0-a718-f7a3d1e2c689"

func withDesktopSingleInstance(
	run func(*options.App) error,
	unminimise func(context.Context),
	show func(context.Context),
) func(*options.App) error {
	return func(config *options.App) error {
		if run == nil || config == nil || unminimise == nil || show == nil {
			return errors.New("desktop single-instance dependencies are required")
		}

		var (
			mu              sync.Mutex
			primaryContext  context.Context
			pendingActivate bool
		)
		activate := func(ctx context.Context) {
			unminimise(ctx)
			show(ctx)
		}

		originalStartup := config.OnStartup
		config.OnStartup = func(ctx context.Context) {
			if originalStartup != nil {
				originalStartup(ctx)
			}
			mu.Lock()
			primaryContext = ctx
			shouldActivate := pendingActivate
			pendingActivate = false
			mu.Unlock()
			if shouldActivate {
				activate(ctx)
			}
		}
		config.SingleInstanceLock = &options.SingleInstanceLock{
			UniqueId: desktopSingleInstanceID,
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				mu.Lock()
				ctx := primaryContext
				if ctx == nil {
					pendingActivate = true
				}
				mu.Unlock()
				if ctx != nil {
					activate(ctx)
				}
			},
		}
		return run(config)
	}
}
