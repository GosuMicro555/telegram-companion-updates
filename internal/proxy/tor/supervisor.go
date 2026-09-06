package tor

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateReady    State = "ready"
	StateDegraded State = "degraded"
	StateError    State = "error"
)

type Status struct {
	State        State
	Address      string
	Transport    string
	LastError    string
	RestartCount int
	UpdatedAt    time.Time
}

type runState struct {
	ready    chan struct{}
	readyErr error
	closed   bool
}

type Supervisor struct {
	mu         sync.RWMutex
	config     resolvedConfig
	log        *slog.Logger
	status     Status
	current    *runState
	running    bool
	generation uint64
	changed    chan struct{}
}

func New(input Config, log *slog.Logger) (*Supervisor, error) {
	config, err := resolveConfig(input)
	if err != nil {
		return nil, err
	}
	if err := prepareState(config); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Supervisor{
		config:  config,
		log:     log,
		status:  Status{State: StateStopped, Address: config.socksAddress, Transport: candidateTransport(config), UpdatedAt: config.now().UTC()},
		changed: make(chan struct{}),
	}, nil
}

func (s *Supervisor) SOCKSAddress() string { return s.config.socksAddress }

func (s *Supervisor) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *Supervisor) Generation() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.generation
}

func (s *Supervisor) WaitReadyAfter(ctx context.Context, generation uint64) error {
	for {
		s.mu.RLock()
		current := s.current
		currentGeneration := s.generation
		changed := s.changed
		s.mu.RUnlock()
		if current == nil || currentGeneration <= generation {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
				continue
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-current.ready:
			s.mu.RLock()
			err := current.readyErr
			s.mu.RUnlock()
			return err
		}
	}
}

func (s *Supervisor) WaitReady(ctx context.Context) error {
	for {
		s.mu.RLock()
		current := s.current
		changed := s.changed
		running := s.running
		state := s.status.State
		s.mu.RUnlock()
		if current == nil || (!running && state == StateStopped) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
				continue
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-current.ready:
			s.mu.RLock()
			err := current.readyErr
			s.mu.RUnlock()
			return err
		}
	}
}

func (s *Supervisor) Run(ctx context.Context) error {
	run, err := s.beginRun()
	if err != nil {
		return err
	}
	defer s.finishRun()
	readyOnce := false

	for {
		candidates := attemptCandidates(s.config)
		cycleCode := "all_candidates_exhausted"
		for candidateIndex, candidate := range candidates {
			s.setStatusForCandidate(StateStarting, "", s.statusRestartCount(), candidate)
			cycleCode = ""
			transportPath, pathErr := transportPathForCandidate(s.config, candidate)
			if pathErr != nil {
				cycleCode = "transport_alias_unavailable"
				if candidateIndex+1 < len(candidates) {
					continue
				}
				if !readyOnce && len(s.config.bridgeCandidates) == 0 {
					return s.failInitial(run, cycleCode, "prepare managed Tor transport")
				}
				break
			}
			if _, err := prepareManagedTransportExecutable(transportPath, s.config.transportAliasDir); err != nil {
				cycleCode = "transport_alias_unavailable"
				if candidateIndex+1 < len(candidates) {
					continue
				}
				if !readyOnce && len(s.config.bridgeCandidates) == 0 {
					return s.failInitial(run, cycleCode, "prepare managed Tor transport")
				}
				break
			}
			contents, renderErr := renderTorrc(s.config, candidate)
			if renderErr != nil {
				cycleCode = "state_io"
			} else if err := writePrivateAtomic(s.config.torrcPath, []byte(contents), 0o600); err != nil {
				cycleCode = "state_io"
			}
			if cycleCode == "state_io" {
				if candidateIndex+1 < len(candidates) {
					continue
				}
				if !readyOnce && len(s.config.bridgeCandidates) == 0 {
					return s.failInitial(run, cycleCode, "prepare managed Tor")
				}
				break
			}
			if err := resetProcessLogs(s.config); err != nil {
				cycleCode = "state_io"
				if candidateIndex+1 < len(candidates) {
					continue
				}
				if !readyOnce && len(s.config.bridgeCandidates) == 0 {
					return s.failInitial(run, cycleCode, "prepare managed Tor")
				}
				break
			}
			process, startErr := s.config.startProcess(processSpec{
				path: s.config.torPath, configPath: s.config.torrcPath,
				workingDir: s.config.stateDir, outputPath: s.config.outputPath,
			})
			if startErr != nil {
				cycleCode = "proxy_start_failed"
				if candidateIndex+1 < len(candidates) {
					continue
				}
				if !readyOnce && len(s.config.bridgeCandidates) == 0 {
					return s.failInitial(run, cycleCode, "start managed Tor")
				}
				break
			}

			exited := make(chan error, 1)
			go func() { exited <- process.Wait() }()
			bootstrapCtx, cancelBootstrap := context.WithTimeout(ctx, s.config.bootstrapTimeout)
			readiness := make(chan error, 1)
			go func() {
				readiness <- s.config.waitReady(bootstrapCtx, readinessSpec{
					bootstrapLogPath: s.config.outputPath,
					socksAddress:     s.config.socksAddress,
					probeAddress:     s.config.probeAddress,
				})
			}()

			candidateReady := false
			select {
			case <-ctx.Done():
				cancelBootstrap()
				s.stopProcess(process, exited)
				return s.stopForContext(run, ctx.Err())
			case <-exited:
				cancelBootstrap()
				cycleCode = "proxy_exited"
				if candidateIndex+1 < len(candidates) {
					continue
				}
				if !readyOnce && len(s.config.bridgeCandidates) == 0 {
					return s.failInitial(run, cycleCode, "managed Tor exited during bootstrap")
				}
			case readyErr := <-readiness:
				cancelBootstrap()
				if readyErr != nil {
					s.stopProcess(process, exited)
					cycleCode = readinessErrorCode(readyErr)
					if candidateIndex+1 < len(candidates) {
						continue
					}
					break
				}
				candidateReady = true
			}
			if !candidateReady {
				break
			}

			readyOnce = true
			s.setStatusForCandidate(StateReady, "", s.statusRestartCount(), candidate)
			s.signalReady(run, nil)
			s.log.Info("managed Tor ready", "address", s.config.socksAddress, "transport", candidate.Transport)
			select {
			case <-ctx.Done():
				s.stopProcess(process, exited)
				return s.stopForContext(run, ctx.Err())
			case <-exited:
				cycleCode = "proxy_exited"
			}
			break
		}
		if !readyOnce {
			cycleCode = "all_candidates_exhausted"
		} else if cycleCode == "" {
			cycleCode = "all_candidates_exhausted"
		}
		if err := s.retry(ctx, cycleCode); err != nil {
			return s.stopForContext(run, err)
		}
	}
}

func (s *Supervisor) beginRun() (*runState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil, errors.New("managed Tor is already running")
	}
	run := &runState{ready: make(chan struct{})}
	s.running = true
	s.generation++
	s.current = run
	close(s.changed)
	s.changed = make(chan struct{})
	s.status = Status{State: StateStarting, Address: s.config.socksAddress, Transport: candidateTransport(s.config), UpdatedAt: s.config.now().UTC()}
	return run, nil
}

func (s *Supervisor) finishRun() {
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
}

func (s *Supervisor) signalReady(run *runState, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run.closed {
		return
	}
	run.readyErr = err
	run.closed = true
	close(run.ready)
}

func (s *Supervisor) failInitial(run *runState, code, message string) error {
	err := errors.New(message)
	s.setStatusForTransport(StateError, code, s.statusRestartCount(), s.statusTransport())
	s.signalReady(run, err)
	s.log.Error(message, "error_code", code)
	return err
}

func (s *Supervisor) retry(ctx context.Context, code string) error {
	restarts := s.statusRestartCount() + 1
	s.setStatusForTransport(StateDegraded, code, restarts, s.statusTransport())
	delay := restartBackoff(restarts - 1)
	s.log.Warn("managed Tor restarting", "error_code", code, "restart_count", restarts, "retry_in", delay)
	return s.config.waitBackoff(ctx, delay)
}

func (s *Supervisor) stopForContext(run *runState, err error) error {
	s.setStatusForTransport(StateStopped, "", s.statusRestartCount(), s.statusTransport())
	s.signalReady(run, err)
	return err
}

func (s *Supervisor) stopProcess(process managedProcess, exited <-chan error) {
	_ = process.Interrupt()
	timer := time.NewTimer(s.config.stopTimeout)
	defer timer.Stop()
	select {
	case <-exited:
		return
	case <-timer.C:
		_ = process.Kill()
		<-exited
	}
}

func (s *Supervisor) setStatus(state State, code string, restarts int) {
	s.setStatusForTransport(state, code, restarts, candidateTransport(s.config))
}

func (s *Supervisor) statusTransport() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.status.Transport != "" {
		return s.status.Transport
	}
	return candidateTransport(s.config)
}

func (s *Supervisor) setStatusForCandidate(state State, code string, restarts int, candidate BridgeCandidate) {
	s.setStatusForTransport(state, code, restarts, string(candidate.Transport))
}

func (s *Supervisor) setStatusForTransport(state State, code string, restarts int, transport string) {
	s.mu.Lock()
	s.status = Status{
		State: state, Address: s.config.socksAddress, Transport: transport,
		LastError: code, RestartCount: restarts, UpdatedAt: s.config.now().UTC(),
	}
	s.mu.Unlock()
}

func readinessErrorCode(err error) string {
	switch {
	case errors.Is(err, errSOCKSProbe):
		return "socks_probe_failed"
	case errors.Is(err, errBootstrapFailed), errors.Is(err, context.DeadlineExceeded):
		return "bootstrap_failed"
	default:
		return "bootstrap_failed"
	}
}

func attemptCandidates(config resolvedConfig) []BridgeCandidate {
	if len(config.bridgeCandidates) == 0 {
		return []BridgeCandidate{config.snowflakeFallback}
	}
	obfs4 := make([]BridgeCandidate, 0, len(config.bridgeCandidates))
	snowflake := make([]BridgeCandidate, 0, len(config.bridgeCandidates))
	seen := make(map[string]struct{}, len(config.bridgeCandidates))
	for _, candidate := range config.bridgeCandidates {
		if _, exists := seen[candidate.ID]; exists {
			continue
		}
		seen[candidate.ID] = struct{}{}
		switch candidate.Transport {
		case TransportObfs4:
			obfs4 = append(obfs4, candidate)
		case TransportSnowflake:
			snowflake = append(snowflake, candidate)
		}
	}
	config.shuffleCandidates(obfs4)
	config.shuffleCandidates(snowflake)
	return append(snowflake, obfs4...)
}

func activeCandidate(config resolvedConfig) BridgeCandidate {
	for _, candidate := range config.bridgeCandidates {
		if candidate.Transport == TransportSnowflake {
			return candidate
		}
	}
	if len(config.bridgeCandidates) > 0 {
		return config.bridgeCandidates[0]
	}
	return config.snowflakeFallback
}

func candidateTransport(config resolvedConfig) string {
	return string(activeCandidate(config).Transport)
}

func (s *Supervisor) statusRestartCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status.RestartCount
}

func restartBackoff(restart int) time.Duration {
	if restart < 0 {
		restart = 0
	}
	delay := time.Second
	for i := 0; i < restart && delay < 30*time.Second; i++ {
		delay *= 2
	}
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}
