package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAutomationControllerStartStop(t *testing.T) {
	started := make(chan struct{})
	controller := NewAutomationController(AutomationRunnerFunc(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}))
	if controller.Running() {
		t.Fatal("new controller is running")
	}
	if err := controller.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	<-started
	if !controller.Running() {
		t.Fatal("controller did not start")
	}
	if err := controller.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if controller.Running() {
		t.Fatal("controller did not stop")
	}
}

func TestAutomationControllerRequiresRunner(t *testing.T) {
	controller := NewAutomationController(nil)

	if err := controller.Start(context.Background()); !errors.Is(err, ErrAutomationRunnerNotConfigured) {
		t.Fatalf("Start error = %v, want ErrAutomationRunnerNotConfigured", err)
	}
}

func TestAutomationControllerRunIsChildOfProvidedRootContext(t *testing.T) {
	root, cancelRoot := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	controller := NewAutomationController(AutomationRunnerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}))
	require.NoError(t, controller.Start(root))
	cancelRoot()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("automation did not inherit root cancellation")
	}
	require.Eventually(t, func() bool { return !controller.Running() }, time.Second, time.Millisecond)
}
