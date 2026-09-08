//go:build !darwin || !cgo

package wake

type disabledObserver struct{}

// New returns a no-op observer on platforms that do not expose the macOS wake
// notification through Cocoa.
func New(func()) Observer { return disabledObserver{} }

func (disabledObserver) Start() {}
func (disabledObserver) Stop()  {}
