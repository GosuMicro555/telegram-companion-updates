package routes

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"telegram-companion/internal/domain"
)

const (
	RouteCapacity       = 10
	maxReconnectStagger = 10
)

var ErrRouteUnavailable = errors.New("proxy route is unavailable")

type State string

const (
	StateReady    State = "ready"
	StateChecking State = "checking"
	StateDegraded State = "degraded"
	StateDisabled State = "disabled"
	StateFull     State = "full"
)

type Status struct {
	RouteID   domain.ID
	Name      string
	State     State
	Usage     int
	Capacity  int
	LastError string
	UpdatedAt time.Time
	RetryAt   *time.Time
}

type Probe func(context.Context, domain.ProxyRoute, string) error

// SystemHealth reports whether the managed system route is currently ready.
// The supervisor owns this signal; the registry only uses it to gate routing.
type SystemHealth func() bool

type Option func(*Registry)

func WithProbe(probe Probe) Option {
	return func(registry *Registry) {
		if probe != nil {
			registry.probe = probe
		}
	}
}

func WithClock(now func() time.Time) Option {
	return func(registry *Registry) {
		if now != nil {
			registry.now = now
		}
	}
}

func WithHealthTimeout(timeout time.Duration) Option {
	return func(registry *Registry) {
		if timeout > 0 {
			registry.healthTimeout = timeout
		}
	}
}

func WithSystemHealth(health SystemHealth) Option {
	return func(registry *Registry) {
		registry.systemHealth = health
	}
}

type routeRuntime struct {
	state     State
	lastError string
	updatedAt time.Time
	retryAt   *time.Time
	failures  int
	revision  uint64
}

type checkRun struct {
	done chan struct{}
	err  error
}

type Registry struct {
	repository    domain.ProxyProfileRepository
	system        domain.ProxyRoute
	probe         Probe
	systemHealth  SystemHealth
	now           func() time.Time
	healthTimeout time.Duration
	target        string

	mu     sync.RWMutex
	routes map[domain.ID]routeRuntime
	checks map[domain.ID]*checkRun

	// systemHealthMu serializes observation tokens, not callback execution.
	// This keeps supervisor callbacks outside mu while allowing a newer health
	// observation to supersede an older in-flight one.
	systemHealthMu         sync.Mutex
	systemHealthGeneration uint64
}

func NewRegistry(repository domain.ProxyProfileRepository, system domain.ProxyRoute, options ...Option) *Registry {
	if system.ID == "" {
		system.ID = domain.SystemProxyRouteID
	}
	registry := &Registry{
		repository:    repository,
		system:        system,
		probe:         defaultProbe,
		now:           time.Now,
		healthTimeout: 5 * time.Second,
		target:        "149.154.167.50:443",
		routes:        make(map[domain.ID]routeRuntime),
		checks:        make(map[domain.ID]*checkRun),
	}
	for _, option := range options {
		option(registry)
	}
	state := StateReady
	if !system.Enabled {
		state = StateDisabled
	} else if registry.systemHealth != nil && !registry.systemHealth() {
		state = StateChecking
	}
	registry.routes[domain.SystemProxyRouteID] = routeRuntime{state: state, updatedAt: registry.now().UTC()}
	return registry
}

func (r *Registry) RouteFor(ctx context.Context, account domain.Account) (domain.ProxyRoute, error) {
	routeID, err := routeIDFor(account)
	if err != nil {
		return domain.ProxyRoute{}, err
	}
	if routeID == domain.SystemProxyRouteID {
		r.refreshSystemHealth()
		if !r.available(routeID) || !r.system.Enabled {
			return domain.ProxyRoute{}, routeUnavailable(routeID)
		}
		return r.system, nil
	}
	if r.repository == nil {
		return domain.ProxyRoute{}, routeUnavailable(routeID)
	}
	route, err := r.repository.Route(ctx, routeID)
	if err != nil || !route.Enabled {
		if !route.Enabled {
			r.setRuntime(routeID, routeRuntime{state: StateDisabled, updatedAt: r.now().UTC()})
		}
		return domain.ProxyRoute{}, routeUnavailable(routeID)
	}
	if !r.available(routeID) {
		return domain.ProxyRoute{}, routeUnavailable(routeID)
	}
	return route, nil
}

func (r *Registry) Status(ctx context.Context, routeID domain.ID) (Status, error) {
	if routeID == domain.SystemProxyRouteID {
		r.refreshSystemHealth()
	}
	name := "Tor/Snowflake"
	enabled := r.system.Enabled
	if routeID != domain.SystemProxyRouteID {
		if r.repository == nil {
			return Status{}, routeUnavailable(routeID)
		}
		profiles, err := r.repository.List(ctx)
		if err != nil {
			return Status{}, err
		}
		found := false
		for _, profile := range profiles {
			if profile.ID == routeID {
				name = profile.Name
				enabled = profile.Enabled
				found = true
				break
			}
		}
		if !found {
			return Status{}, routeUnavailable(routeID)
		}
	}
	runtime := r.runtime(routeID)
	if !enabled {
		runtime.state = StateDisabled
	}
	usage := 0
	if r.repository != nil {
		counts, err := r.repository.AssignmentCounts(ctx)
		if err != nil {
			return Status{}, err
		}
		usage = counts[routeID]
	}
	capacity := RouteCapacity
	if routeID == domain.SystemProxyRouteID {
		capacity = 0
	}
	state := runtime.state
	if state == StateReady && capacity > 0 && usage >= capacity {
		state = StateFull
	}
	return Status{
		RouteID: routeID, Name: name, State: state, Usage: usage, Capacity: capacity,
		LastError: runtime.lastError, UpdatedAt: runtime.updatedAt, RetryAt: cloneTime(runtime.retryAt),
	}, nil
}

func (r *Registry) RecordFailure(routeID domain.ID, err error) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	runtime := r.routes[routeID]
	runtime.failures++
	delay := routeBackoff(runtime.failures)
	runtime.lastError = "transport_failure"
	var retry interface{ RetryAfter() time.Duration }
	if errors.As(err, &retry) && retry.RetryAfter() > 0 {
		delay = retry.RetryAfter()
		runtime.lastError = "retry_required"
	}
	now := r.now().UTC()
	retryAt := now.Add(delay)
	runtime.state = StateDegraded
	runtime.updatedAt = now
	runtime.retryAt = &retryAt
	runtime.revision++
	r.routes[routeID] = runtime
	return delay
}

func (r *Registry) ReconnectDelay(position int) time.Duration {
	if position <= 0 {
		return 0
	}
	if position > maxReconnectStagger {
		position = maxReconnectStagger
	}
	return time.Duration(position) * 300 * time.Millisecond
}

func (r *Registry) available(routeID domain.ID) bool {
	runtime := r.runtime(routeID)
	return runtime.state == StateReady || runtime.state == StateFull
}

func (r *Registry) runtime(routeID domain.ID) routeRuntime {
	r.mu.RLock()
	runtime, ok := r.routes[routeID]
	r.mu.RUnlock()
	if !ok {
		return routeRuntime{state: StateChecking, updatedAt: r.now().UTC()}
	}
	return runtime
}

func (r *Registry) setRuntime(routeID domain.ID, runtime routeRuntime) {
	r.mu.Lock()
	runtime.revision = r.routes[routeID].revision + 1
	r.routes[routeID] = runtime
	r.mu.Unlock()
}

func routeIDFor(account domain.Account) (domain.ID, error) {
	switch account.ProxyMode {
	case "", domain.ProxyModeGlobal:
		return domain.SystemProxyRouteID, nil
	case domain.ProxyModeAssigned:
		if account.ProxyProfileID != nil && *account.ProxyProfileID != "" {
			return *account.ProxyProfileID, nil
		}
	}
	return "", routeUnavailable("")
}

func routeUnavailable(routeID domain.ID) error {
	if routeID == "" {
		return ErrRouteUnavailable
	}
	return fmt.Errorf("%w: %s", ErrRouteUnavailable, routeID)
}

func routeBackoff(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	if failures > 6 {
		failures = 6
	}
	delay := time.Second * time.Duration(1<<(failures-1))
	if delay > time.Minute {
		return time.Minute
	}
	return delay
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
