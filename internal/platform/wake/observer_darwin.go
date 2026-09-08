//go:build darwin && cgo

package wake

/*
#cgo CFLAGS: -fobjc-arc -fmodules -fblocks
#cgo LDFLAGS: -framework Cocoa

#include <stdint.h>

typedef void *TCWakeObserverRef;

TCWakeObserverRef tc_wake_create(uintptr_t signal);
void tc_wake_destroy(TCWakeObserverRef observer);
void tc_wake_test_post(void);
*/
import "C"

import (
	"runtime/cgo"
	"sync"
)

type darwinObserver struct {
	mu       sync.Mutex
	callback func()
	handle   cgo.Handle
	native   C.TCWakeObserverRef
	started  bool
	stopped  bool
}

// New creates a Cocoa-backed observer. It defers native registration until
// Start, keeping construction independent of the application's UI lifecycle.
func New(callback func()) Observer {
	return &darwinObserver{callback: callback}
}

func (observer *darwinObserver) Start() {
	if observer == nil {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.started || observer.stopped {
		return
	}
	observer.handle = cgo.NewHandle(observer.callback)
	observer.native = C.tc_wake_create(C.uintptr_t(observer.handle))
	if observer.native == nil {
		observer.handle.Delete()
		return
	}
	observer.started = true
}

func (observer *darwinObserver) Stop() {
	if observer == nil {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.stopped {
		return
	}
	observer.stopped = true
	if observer.native != nil {
		C.tc_wake_destroy(observer.native)
		observer.native = nil
	}
	if observer.started {
		observer.handle.Delete()
	}
}

//export goWakeSignal
func goWakeSignal(value C.uintptr_t) {
	if value == 0 {
		return
	}
	if callback, ok := cgo.Handle(value).Value().(func()); ok && callback != nil {
		callback()
	}
}

func postWakeForTest() {
	C.tc_wake_test_post()
}
