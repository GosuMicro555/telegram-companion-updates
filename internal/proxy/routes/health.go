package routes

import (
	"context"
	"errors"
	"time"

	"telegram-companion/internal/domain"
)

func defaultProbe(ctx context.Context, route domain.ProxyRoute, target string) error {
	dial, err := NewDialer(route)
	if err != nil {
		return err
	}
	conn, err := dial(ctx, "tcp", target)
	if err != nil {
		return err
	}
	return conn.Close()
}

// refreshSystemHealth synchronizes the callback-backed managed route before a
// caller observes or uses it. The callback is intentionally evaluated outside
// the registry lock because the supervisor may take its own lock while
// reporting state. A generation token ensures that an older in-flight
// observation cannot overwrite a newer one when callbacks overlap.
func (r *Registry) refreshSystemHealth() {
	r.systemHealthMu.Lock()
	if r.systemHealth == nil {
		r.systemHealthMu.Unlock()
		return
	}
	r.systemHealthGeneration++
	generation := r.systemHealthGeneration
	health := r.systemHealth
	systemEnabled := r.system.Enabled
	r.systemHealthMu.Unlock()

	desired := routeRuntime{state: StateDegraded, lastError: "managed_proxy_unavailable"}
	if !systemEnabled {
		desired = routeRuntime{state: StateDisabled}
	} else if health() {
		desired.state = StateReady
		desired.lastError = ""
	}
	desired.updatedAt = r.now().UTC()

	r.systemHealthMu.Lock()
	defer r.systemHealthMu.Unlock()
	if generation != r.systemHealthGeneration {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.routes[domain.SystemProxyRouteID]
	if current.state == desired.state && current.lastError == desired.lastError && current.retryAt == nil {
		return
	}
	desired.revision = current.revision + 1
	r.routes[domain.SystemProxyRouteID] = desired
}

func (r *Registry) Check(ctx context.Context, routeID domain.ID) error {
	if routeID == domain.SystemProxyRouteID || r.repository == nil {
		return routeUnavailable(routeID)
	}
	check, checking, started, skipped := r.startCheck(routeID)
	if skipped {
		return nil
	}
	if !started {
		return r.waitCheck(ctx, check)
	}
	route, err := r.repository.Route(ctx, routeID)
	if err != nil {
		checkErr := routeUnavailable(routeID)
		runtime := failureRuntime(checking, r.now().UTC(), err)
		r.finishCheck(routeID, check, checking, runtime, checkErr)
		return checkErr
	}
	now := r.now().UTC()
	if !route.Enabled {
		runtime := routeRuntime{state: StateDisabled, updatedAt: now}
		if !r.finishCheck(routeID, check, checking, runtime, nil) {
			return nil
		}
		return r.repository.UpdateHealth(ctx, routeID, string(runtime.state), "", now)
	}
	probeCtx, cancel := context.WithTimeout(ctx, r.healthTimeout)
	err = r.probe(probeCtx, route, r.target)
	cancel()
	if err != nil {
		runtime := failureRuntime(checking, now, err)
		checkErr := errors.New("proxy health check failed")
		if !r.finishCheck(routeID, check, checking, runtime, checkErr) {
			return checkErr
		}
		if persistErr := r.repository.UpdateHealth(ctx, routeID, string(StateDegraded), runtime.lastError, now); persistErr != nil {
			return errors.Join(errors.New("proxy health check failed"), persistErr)
		}
		return checkErr
	}
	runtime := routeRuntime{state: StateReady, updatedAt: now}
	if !r.finishCheck(routeID, check, checking, runtime, nil) {
		return nil
	}
	if err := r.repository.UpdateHealth(ctx, routeID, string(StateReady), "", now); err != nil {
		return err
	}
	return nil
}

func (r *Registry) startCheck(routeID domain.ID) (*checkRun, routeRuntime, bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if check := r.checks[routeID]; check != nil {
		return check, routeRuntime{}, false, false
	}
	runtime := r.routes[routeID]
	if runtime.retryAt != nil && runtime.retryAt.After(r.now().UTC()) {
		return nil, routeRuntime{}, false, true
	}
	runtime.state = StateChecking
	runtime.updatedAt = r.now().UTC()
	runtime.revision++
	r.routes[routeID] = runtime
	check := &checkRun{done: make(chan struct{})}
	r.checks[routeID] = check
	return check, runtime, true, false
}

func (r *Registry) waitCheck(ctx context.Context, check *checkRun) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-check.done:
		return check.err
	}
}

func (r *Registry) finishCheck(routeID domain.ID, check *checkRun, checking, runtime routeRuntime, err error) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	applied := false
	if current := r.routes[routeID]; current.revision == checking.revision {
		runtime.revision = current.revision + 1
		r.routes[routeID] = runtime
		applied = true
	}
	check.err = err
	delete(r.checks, routeID)
	close(check.done)
	return applied
}

func failureRuntime(runtime routeRuntime, now time.Time, err error) routeRuntime {
	runtime.failures++
	delay := routeBackoff(runtime.failures)
	runtime.lastError = "transport_failure"
	var retry interface{ RetryAfter() time.Duration }
	if errors.As(err, &retry) && retry.RetryAfter() > 0 {
		delay = retry.RetryAfter()
		runtime.lastError = "retry_required"
	}
	retryAt := now.Add(delay)
	runtime.state = StateDegraded
	runtime.updatedAt = now
	runtime.retryAt = &retryAt
	runtime.lastError = "health_check_failed"
	return runtime
}

func (r *Registry) CheckAll(ctx context.Context) error {
	now := r.now().UTC()
	if !r.system.Enabled {
		r.setRuntime(domain.SystemProxyRouteID, routeRuntime{state: StateDisabled, updatedAt: now})
	} else if r.systemHealth != nil {
		r.refreshSystemHealth()
	} else {
		runtime := r.runtime(domain.SystemProxyRouteID)
		if runtime.retryAt == nil || !runtime.retryAt.After(now) {
			r.setRuntime(domain.SystemProxyRouteID, routeRuntime{state: StateReady, updatedAt: now})
		}
	}
	if r.repository == nil {
		return nil
	}
	profiles, err := r.repository.List(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, profile := range profiles {
		if !profile.Enabled {
			now := r.now().UTC()
			r.setRuntime(profile.ID, routeRuntime{state: StateDisabled, updatedAt: now})
			continue
		}
		runtime := r.runtime(profile.ID)
		if runtime.retryAt != nil && runtime.retryAt.After(r.now()) {
			continue
		}
		if err := r.Check(ctx, profile.ID); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (r *Registry) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	_ = r.CheckAll(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			_ = r.CheckAll(ctx)
		}
	}
}
