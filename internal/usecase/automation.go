package usecase

import (
	"context"
	"errors"
	"sync"
)

var ErrAutomationRunnerNotConfigured = errors.New("automation runner is not configured")

type AutomationRunner interface {
	Run(ctx context.Context) error
}

type AutomationRunnerFunc func(ctx context.Context) error

func (f AutomationRunnerFunc) Run(ctx context.Context) error {
	return f(ctx)
}

type AutomationController struct {
	mu      sync.RWMutex
	runner  AutomationRunner
	cancel  context.CancelFunc
	done    chan error
	running bool
}

func NewAutomationController(runner AutomationRunner) *AutomationController {
	return &AutomationController{runner: runner}
}

func (c *AutomationController) Start(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return nil
	}
	if c.runner == nil {
		return ErrAutomationRunnerNotConfigured
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	c.cancel = cancel
	c.done = done
	c.running = true
	go c.run(runCtx, done)
	return nil
}

func (c *AutomationController) Stop(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return nil
	}
	cancel := c.cancel
	done := c.done
	c.mu.Unlock()

	cancel()
	select {
	case err := <-done:
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *AutomationController) Running() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}

func (c *AutomationController) run(ctx context.Context, done chan<- error) {
	err := c.runner.Run(ctx)

	c.mu.Lock()
	if c.done == done {
		c.running = false
		c.cancel = nil
		c.done = nil
	}
	c.mu.Unlock()

	done <- err
}
