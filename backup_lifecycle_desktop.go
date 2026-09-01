//go:build desktop

package main

import "context"

func (a *DesktopApp) closeBackgroundServices(ctx context.Context) {
	if err := a.bindings.CloseBackgroundServices(ctx); err != nil {
		a.log.Error("stop backup background services on desktop shutdown", "error", err)
	}
}
