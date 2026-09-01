package runtimeconfig

import (
	"sync"
	"sync/atomic"

	"telegram-companion/internal/domain"
)

type RateLimits struct {
	RepliesPerMinute   int
	MinIntervalSeconds int
}

type CanonicalTrigger struct {
	ID        string
	Canonical string
	Forms     []string
}

type Snapshot struct {
	Revision              uint64
	OutboundPaused        bool
	Roles                 map[domain.ID]domain.AccountRole
	CatalogAssignments    map[domain.SourceCatalog][]domain.ID
	Keywords              []string
	MinusKeywords         []string
	CanonicalTriggers     []CanonicalTrigger
	SharedReply           string
	PrivateReply          string
	PrivateReplyPresent   bool
	DeliveryMode          domain.KeywordDeliveryMode
	RateLimits            RateLimits
	DirectMessages        bool
	DirectMessageKeywords []string
	ProxyAssignments      map[domain.ID]string
}

type subscriber struct {
	after   uint64
	updates chan Snapshot
}

type Store struct {
	current     atomic.Pointer[Snapshot]
	mu          sync.Mutex
	revision    uint64
	subscribers []subscriber
}

func NewStore(initial Snapshot) *Store {
	initial = cloneSnapshot(initial)
	initial.Revision = 1
	store := &Store{revision: initial.Revision}
	store.current.Store(&initial)
	return store
}

func (s *Store) Current() Snapshot {
	current := s.current.Load()
	if current == nil {
		return Snapshot{}
	}
	return cloneSnapshot(*current)
}

func (s *Store) Publish(next Snapshot) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.publishLocked(next)
}

func (s *Store) Update(mutate func(*Snapshot)) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := Snapshot{}
	if current := s.current.Load(); current != nil {
		next = cloneSnapshot(*current)
	}
	if mutate != nil {
		mutate(&next)
	}
	return s.publishLocked(next)
}

func (s *Store) publishLocked(next Snapshot) uint64 {
	s.revision++
	next = cloneSnapshot(next)
	next.Revision = s.revision
	s.current.Store(&next)

	for _, subscriber := range s.subscribers {
		if next.Revision > subscriber.after {
			replacePending(subscriber.updates, cloneSnapshot(next))
		}
	}
	return next.Revision
}

func (s *Store) Subscribe(after uint64) (<-chan Snapshot, func()) {
	updates := make(chan Snapshot, 1)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.subscribers = append(s.subscribers, subscriber{after: after, updates: updates})
	if current := s.current.Load(); current != nil && current.Revision > after {
		replacePending(updates, cloneSnapshot(*current))
	}
	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			s.mu.Lock()
			for index := range s.subscribers {
				if s.subscribers[index].updates == updates {
					s.subscribers = append(s.subscribers[:index], s.subscribers[index+1:]...)
					break
				}
			}
			close(updates)
			s.mu.Unlock()
		})
	}
	return updates, unsubscribe
}

func replacePending(updates chan Snapshot, next Snapshot) {
	select {
	case updates <- next:
		return
	default:
	}
	select {
	case <-updates:
	default:
	}
	select {
	case updates <- next:
	default:
	}
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	clone := snapshot
	clone.Roles = cloneMap(snapshot.Roles)
	clone.CatalogAssignments = cloneCatalogAssignments(snapshot.CatalogAssignments)
	clone.Keywords = cloneStrings(snapshot.Keywords)
	clone.MinusKeywords = cloneStrings(snapshot.MinusKeywords)
	clone.CanonicalTriggers = cloneCanonicalTriggers(snapshot.CanonicalTriggers)
	clone.DirectMessageKeywords = cloneStrings(snapshot.DirectMessageKeywords)
	clone.ProxyAssignments = cloneMap(snapshot.ProxyAssignments)
	return clone
}

func cloneCanonicalTriggers(source []CanonicalTrigger) []CanonicalTrigger {
	clone := make([]CanonicalTrigger, len(source))
	for index, trigger := range source {
		clone[index] = trigger
		clone[index].Forms = cloneStrings(trigger.Forms)
	}
	return clone
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	if source == nil {
		return nil
	}
	clone := make(map[K]V, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func cloneCatalogAssignments(source map[domain.SourceCatalog][]domain.ID) map[domain.SourceCatalog][]domain.ID {
	if source == nil {
		return nil
	}
	clone := make(map[domain.SourceCatalog][]domain.ID, len(source))
	for catalog, assignments := range source {
		clone[catalog] = append([]domain.ID(nil), assignments...)
	}
	return clone
}

func cloneStrings(source []string) []string {
	return append([]string(nil), source...)
}
