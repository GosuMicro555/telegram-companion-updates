package usecase

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"telegram-companion/internal/domain"
)

var (
	ErrTopicRequired                     = errors.New("topic is required")
	ErrChannelNotFound                   = errors.New("channel not found")
	ErrUnsupportedCatalog                = errors.New("unsupported catalog")
	ErrCatalogActivationStoreUnsupported = errors.New("catalog activation store does not support atomic membership activation")
	ErrCatalogJoinScheduleUnsupported    = errors.New("catalog store does not support join scheduling")
	ErrCatalogMembershipLeaveUnsupported = errors.New("catalog store does not support membership leave requests")
	ErrCatalogMembershipRetryUnsupported = errors.New("catalog store does not support membership retry requests")
	ErrCatalogRemovalStoreUnsupported    = errors.New("catalog store does not support atomic removal requests")
	ErrChannelRemovalRequested           = errors.New("channel removal requested")
	ErrInvalidJoinDelay                  = errors.New("join delay is outside the allowed range")
)

type CatalogStore interface {
	ListCatalog(context.Context, domain.SourceCatalog) ([]domain.Channel, error)
	SaveCatalog(context.Context, domain.SourceCatalog, domain.Channel) error
	SetCatalogTopics(context.Context, domain.SourceCatalog, []domain.ID, string) error
}

type catalogAccountLister interface {
	ListAccounts(context.Context) ([]domain.Account, error)
}

type catalogMembershipLister interface {
	ListMemberships(context.Context, domain.SourceCatalog, domain.ID) ([]domain.ChannelMembership, error)
}

type JoinDelaySource interface {
	Minutes(min, max int) (int, error)
}

type CatalogActivationStore interface {
	ActivateCatalogWithMemberships(context.Context, domain.SourceCatalog, domain.Channel, []domain.ChannelMembership) error
}

type CatalogRemovalStore interface {
	RequestCatalogRemoval(context.Context, domain.SourceCatalog, []domain.ID) error
}

type CatalogMembershipLeaveStore interface {
	RequestCatalogLeave(context.Context, domain.SourceCatalog, domain.ID) error
}

type CatalogMembershipRetryStore interface {
	RetryPendingCatalogMemberships(context.Context, domain.SourceCatalog, domain.ID, time.Time) (int, error)
}

type CatalogService struct {
	store           CatalogStore
	delaySource     JoinDelaySource
	now             func() time.Time
	joinIntervalMu  sync.RWMutex
	joinInterval    domain.JoinIntervalRange
	joinIntervalOn  bool
	joinIntervalErr error
	groupRestMu     sync.RWMutex
	groupRestHours  int
	groupRestOn     bool
	groupRestErr    error
}

func NewCatalogService(store CatalogStore) *CatalogService {
	return NewCatalogServiceWithJoinSchedule(store, randomJoinDelaySource{}, time.Now)
}

func NewCatalogServiceWithJoinSchedule(store CatalogStore, delaySource JoinDelaySource, now func() time.Time) *CatalogService {
	return &CatalogService{
		store:          store,
		delaySource:    delaySource,
		now:            now,
		joinInterval:   domain.DefaultJoinIntervalRange(),
		joinIntervalOn: true,
		groupRestHours: domain.GroupRestDefaultHours,
		groupRestOn:    true,
	}
}

func (s *CatalogService) SetJoinIntervalRange(minMinutes, maxMinutes int) error {
	interval, err := domain.NewJoinIntervalRange(minMinutes, maxMinutes)
	s.joinIntervalMu.Lock()
	defer s.joinIntervalMu.Unlock()
	if err != nil {
		s.joinIntervalErr = err
		return err
	}
	s.joinInterval = interval
	s.joinIntervalErr = nil
	return nil
}

func (s *CatalogService) SetJoinIntervalEnabled(enabled bool) {
	s.joinIntervalMu.Lock()
	defer s.joinIntervalMu.Unlock()
	s.joinIntervalOn = enabled
}

func (s *CatalogService) SetGroupRestHours(hours int) error {
	err := domain.ValidateGroupRestHours(hours)
	s.groupRestMu.Lock()
	defer s.groupRestMu.Unlock()
	if err != nil {
		s.groupRestErr = err
		return err
	}
	s.groupRestHours = hours
	s.groupRestErr = nil
	return nil
}

func (s *CatalogService) SetGroupRestEnabled(enabled bool) {
	s.groupRestMu.Lock()
	defer s.groupRestMu.Unlock()
	s.groupRestOn = enabled
}

func (s *CatalogService) List(ctx context.Context, catalog domain.SourceCatalog) ([]domain.Channel, error) {
	if err := validateCatalog(catalog); err != nil {
		return nil, err
	}
	return s.store.ListCatalog(ctx, catalog)
}

func (s *CatalogService) AddLinks(ctx context.Context, catalog domain.SourceCatalog, links []string, topic string) ([]domain.Channel, error) {
	if err := validateCatalog(catalog); err != nil {
		return nil, err
	}
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return nil, ErrTopicRequired
	}
	rows, err := s.store.ListCatalog(ctx, catalog)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(rows)+len(links))
	for _, row := range rows {
		seen[strings.ToLower(strings.TrimSpace(row.Link))] = struct{}{}
	}
	for _, link := range links {
		link = strings.TrimSpace(link)
		normalized := strings.ToLower(link)
		if link == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		now := time.Now().UTC()
		row := domain.Channel{
			ID:         domain.ID(randomID()),
			TelegramID: randomID(),
			Title:      link,
			Link:       link,
			Topic:      topic,
			Status:     domain.ChannelPaused,
			Active:     false,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := s.store.SaveCatalog(ctx, catalog, row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return s.store.ListCatalog(ctx, catalog)
}

func (s *CatalogService) SetTopic(ctx context.Context, catalog domain.SourceCatalog, id domain.ID, topic string) ([]domain.Channel, error) {
	return s.SetTopics(ctx, catalog, []domain.ID{id}, topic)
}

func (s *CatalogService) SetTopics(ctx context.Context, catalog domain.SourceCatalog, ids []domain.ID, topic string) ([]domain.Channel, error) {
	if err := validateCatalog(catalog); err != nil {
		return nil, err
	}
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return nil, ErrTopicRequired
	}
	if err := s.store.SetCatalogTopics(ctx, catalog, uniqueIDs(ids), topic); err != nil {
		return nil, err
	}
	return s.store.ListCatalog(ctx, catalog)
}

func (s *CatalogService) Delete(ctx context.Context, catalog domain.SourceCatalog, ids []domain.ID) ([]domain.Channel, error) {
	if err := validateCatalog(catalog); err != nil {
		return nil, err
	}
	removals, ok := s.store.(CatalogRemovalStore)
	if !ok {
		return nil, ErrCatalogRemovalStoreUnsupported
	}
	if err := removals.RequestCatalogRemoval(ctx, catalog, uniqueIDs(ids)); err != nil {
		return nil, err
	}
	return s.store.ListCatalog(ctx, catalog)
}

func (s *CatalogService) Toggle(ctx context.Context, catalog domain.SourceCatalog, id domain.ID, active bool) ([]domain.Channel, error) {
	if err := validateCatalog(catalog); err != nil {
		return nil, err
	}
	rows, err := s.store.ListCatalog(ctx, catalog)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.ID != id {
			continue
		}
		if active && row.RemovalRequestedAt != nil {
			return nil, ErrChannelRemovalRequested
		}
		if row.Active == active {
			return rows, nil
		}
		row.Active = active
		row.UpdatedAt = s.clockNow()
		if err := s.store.SaveCatalog(ctx, catalog, row); err != nil {
			return nil, err
		}
		return s.store.ListCatalog(ctx, catalog)
	}
	return nil, ErrChannelNotFound
}

// Join creates membership intents without changing whether a catalog channel is active.
func (s *CatalogService) Join(ctx context.Context, catalog domain.SourceCatalog, id domain.ID) ([]domain.Channel, error) {
	if err := validateCatalog(catalog); err != nil {
		return nil, err
	}
	rows, err := s.store.ListCatalog(ctx, catalog)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.ID != id {
			continue
		}
		if row.RemovalRequestedAt != nil {
			return nil, ErrChannelRemovalRequested
		}
		activationStore, ok := s.store.(CatalogActivationStore)
		if !ok {
			return nil, ErrCatalogActivationStoreUnsupported
		}
		now := s.clockNow()
		memberships, err := s.joiningMemberships(ctx, catalog, row.ID, now)
		if err != nil {
			return nil, err
		}
		row.Status = domain.ChannelJoining
		row.UpdatedAt = now
		if err := activationStore.ActivateCatalogWithMemberships(ctx, catalog, row, memberships); err != nil {
			return nil, err
		}
		return s.store.ListCatalog(ctx, catalog)
	}
	return nil, ErrChannelNotFound
}

// Leave disables processing for the channel and asks the membership reconciler to exit it.
func (s *CatalogService) Leave(ctx context.Context, catalog domain.SourceCatalog, id domain.ID) ([]domain.Channel, error) {
	if err := validateCatalog(catalog); err != nil {
		return nil, err
	}
	rows, err := s.store.ListCatalog(ctx, catalog)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.ID != id {
			continue
		}
		leaveStore, ok := s.store.(CatalogMembershipLeaveStore)
		if !ok {
			return nil, ErrCatalogMembershipLeaveUnsupported
		}
		if err := leaveStore.RequestCatalogLeave(ctx, catalog, row.ID); err != nil {
			return nil, err
		}
		return s.store.ListCatalog(ctx, catalog)
	}
	return nil, ErrChannelNotFound
}

// RetryJoin clears the previous approval attempt for pending accounts so the
// membership reconciler performs a fresh Telegram join request.
func (s *CatalogService) RetryJoin(ctx context.Context, catalog domain.SourceCatalog, id domain.ID) ([]domain.Channel, error) {
	if err := validateCatalog(catalog); err != nil {
		return nil, err
	}
	rows, err := s.store.ListCatalog(ctx, catalog)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.ID != id {
			continue
		}
		if row.RemovalRequestedAt != nil {
			return nil, ErrChannelRemovalRequested
		}
		retryStore, ok := s.store.(CatalogMembershipRetryStore)
		if !ok {
			return nil, ErrCatalogMembershipRetryUnsupported
		}
		if _, err := retryStore.RetryPendingCatalogMemberships(ctx, catalog, row.ID, s.clockNow()); err != nil {
			return nil, err
		}
		return s.store.ListCatalog(ctx, catalog)
	}
	return nil, ErrChannelNotFound
}

func (s *CatalogService) joiningMemberships(ctx context.Context, catalog domain.SourceCatalog, channelID domain.ID, now time.Time) ([]domain.ChannelMembership, error) {
	accountsStore, hasAccounts := s.store.(catalogAccountLister)
	membershipStore, hasMemberships := s.store.(catalogMembershipLister)
	if !hasAccounts || !hasMemberships {
		return nil, ErrCatalogJoinScheduleUnsupported
	}
	interval, intervalEnabled, intervalErr := s.currentJoinInterval()
	if intervalEnabled && intervalErr != nil {
		return nil, intervalErr
	}
	if intervalEnabled && s.delaySource == nil {
		return nil, ErrCatalogJoinScheduleUnsupported
	}
	groupRestHours, err := s.currentGroupRestHours()
	if err != nil {
		return nil, err
	}

	accounts, err := accountsStore.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	memberships, err := membershipStore.ListMemberships(ctx, catalog, channelID)
	if err != nil {
		return nil, err
	}
	existing := make(map[domain.ID]domain.ChannelMembership)
	for _, membership := range memberships {
		existing[membership.AccountID] = membership
	}

	joining := make([]domain.ChannelMembership, 0, len(accounts))
	due := now.UTC()
	for _, account := range accounts {
		if !account.Eligible() {
			continue
		}
		if _, found := existing[account.ID]; found {
			continue
		}
		if intervalEnabled && len(joining) > 0 {
			delay, err := s.delaySource.Minutes(interval.MinMinutes, interval.MaxMinutes)
			if err != nil {
				return nil, err
			}
			if delay < interval.MinMinutes || delay > interval.MaxMinutes {
				return nil, ErrInvalidJoinDelay
			}
			due = due.Add(time.Duration(delay) * time.Minute)
		}
		dueAt := due
		joining = append(joining, domain.ChannelMembership{
			AccountID: account.ID, ChannelID: channelID, Status: "joining",
			JoinNotBefore: &dueAt, RestDurationHours: groupRestHours,
		})
	}
	return joining, nil
}

func (s *CatalogService) currentJoinInterval() (domain.JoinIntervalRange, bool, error) {
	s.joinIntervalMu.RLock()
	defer s.joinIntervalMu.RUnlock()
	return s.joinInterval, s.joinIntervalOn, s.joinIntervalErr
}

func (s *CatalogService) currentGroupRestHours() (int, error) {
	s.groupRestMu.RLock()
	defer s.groupRestMu.RUnlock()
	if !s.groupRestOn {
		return 0, nil
	}
	return s.groupRestHours, s.groupRestErr
}

func (s *CatalogService) clockNow() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

func validateCatalog(catalog domain.SourceCatalog) error {
	if catalog != domain.SourceCatalogOutbound && catalog != domain.SourceCatalogScout {
		return ErrUnsupportedCatalog
	}
	return nil
}

func uniqueIDs(ids []domain.ID) []domain.ID {
	seen := make(map[domain.ID]struct{}, len(ids))
	out := make([]domain.ID, 0, len(ids))
	for _, id := range ids {
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
