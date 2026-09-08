//go:build darwin && cgo

package wake

import (
	"testing"
	"time"
)

func TestObserverReceivesWakeAndStopsWithoutMainQueuePump(t *testing.T) {
	signals := make(chan struct{}, 2)
	observer := New(func() { signals <- struct{}{} })
	started := make(chan struct{})
	go func() {
		observer.Start()
		close(started)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("observer start waited for a main-queue pump")
	}
	postWakeForTest()
	select {
	case <-signals:
	case <-time.After(time.Second):
		t.Fatal("wake notification did not reach observer")
	}

	stopped := make(chan struct{})
	go func() {
		observer.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("observer stop waited for a main-queue pump")
	}
	postWakeForTest()
	select {
	case <-signals:
		t.Fatal("stopped observer received a wake notification")
	case <-time.After(100 * time.Millisecond):
	}
}
