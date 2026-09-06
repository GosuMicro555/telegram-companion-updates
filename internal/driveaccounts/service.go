package driveaccounts

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
)

type Result struct {
	Added   int
	Skipped int
}
type Processor func(context.Context, Reference, func(string)) (Result, error)
type Item struct {
	Ordinal int    `json:"ordinal"`
	Phase   string `json:"phase"`
	Added   int    `json:"added"`
	Skipped int    `json:"skipped"`
	Error   string `json:"error"`
}
type Batch struct {
	ID      string `json:"id"`
	Running bool   `json:"running"`
	Items   []Item `json:"items"`
}
type Service struct {
	mu      sync.Mutex
	batch   Batch
	cancel  context.CancelFunc
	process Processor
}

func NewService(process Processor) *Service {
	return &Service{process: process, batch: Batch{Items: []Item{}}}
}
func (s *Service) Start(parent context.Context, raw string, done func()) (Batch, error) {
	refs, err := ParseLinks(raw)
	if err != nil {
		return Batch{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.batch.Running {
		return Batch{}, errors.New("import_busy")
	}
	if s.process == nil {
		return Batch{}, errors.New("import_unavailable")
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.batch = Batch{ID: rand.Text(), Running: true, Items: make([]Item, len(refs))}
	for i := range refs {
		s.batch.Items[i] = Item{Ordinal: i + 1, Phase: "queued"}
	}
	snapshot := s.snapshot()
	go s.run(ctx, refs, done)
	return snapshot, nil
}
func (s *Service) run(ctx context.Context, refs []Reference, done func()) {
	defer done()
	defer func() { s.mu.Lock(); s.batch.Running = false; s.cancel(); s.cancel = nil; s.mu.Unlock() }()
	for i, ref := range refs {
		var result Result
		var err error
		if ctx.Err() != nil {
			err = ctx.Err()
		} else {
			result, err = s.process(ctx, ref, func(phase string) { s.mu.Lock(); s.batch.Items[i].Phase = phase; s.mu.Unlock() })
		}
		s.mu.Lock()
		item := &s.batch.Items[i]
		item.Added = result.Added
		item.Skipped = result.Skipped
		switch {
		case err == nil:
			item.Phase = "added"
			if result.Added == 0 {
				item.Phase = "skipped"
			}
		case ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)):
			item.Phase = "cancelled"
		default:
			item.Phase = "failed"
			item.Error = safeError(err)
		}
		s.mu.Unlock()
	}
}
func (s *Service) snapshot() Batch { b := s.batch; b.Items = append([]Item{}, b.Items...); return b }
func (s *Service) Status() Batch   { s.mu.Lock(); defer s.mu.Unlock(); return s.snapshot() }
func (s *Service) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}
func safeError(err error) string {
	for _, code := range []string{"input_invalid", "unsafe_content", "size_limit", "drive_unavailable", "zip_invalid", "tdata_invalid", "credentials_missing", "session_invalid", "route_unavailable", "storage_failed", "existing_session_unreadable"} {
		if err.Error() == code {
			return code
		}
	}
	return "import_failed"
}
