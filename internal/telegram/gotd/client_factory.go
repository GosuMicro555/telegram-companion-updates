package gotd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	telegramupdates "github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"golang.org/x/sync/errgroup"

	"telegram-companion/internal/domain"
)

type Identity struct {
	ID          int64
	Self        bool
	Phone       string
	Username    string
	DisplayName string
}

type ValidationStore interface {
	MarkValidated(ctx context.Context, accountID domain.ID, at time.Time) error
}

type TelegramClient interface {
	Run(ctx context.Context, callback func(context.Context) error) error
	API() *tg.Client
	Validate(ctx context.Context) (time.Time, error)
}

type clientRuntime interface {
	Run(ctx context.Context, callback func(context.Context) error) error
	API() *tg.Client
	FullSelf(ctx context.Context) (Identity, error)
}

type runtimeBuilder interface {
	New(sessionPath string) (clientRuntime, error)
}

type ClientFactory struct {
	builder     runtimeBuilder
	validations ValidationStore
	now         func() time.Time
}

func NewClientFactory(appID int, appHash string, validations ValidationStore) *ClientFactory {
	return NewClientFactoryWithSessionBarrier(appID, appHash, validations, ProductionSessionBarrier())
}

// NewClientFactoryWithSessionBarrier lets the Task 12 production cutover share
// the exact barrier used by the backup manager.
func NewClientFactoryWithSessionBarrier(appID int, appHash string, validations ValidationStore, barrier *SessionBarrier) *ClientFactory {
	return NewClientFactoryWithResolver(appID, appHash, validations, barrier, nil)
}

func NewClientFactoryWithResolver(appID int, appHash string, validations ValidationStore, barrier *SessionBarrier, resolver dcs.Resolver) *ClientFactory {
	return newClientFactory(gotdRuntimeBuilder{appID: appID, appHash: appHash, sessionBarrier: barrier, updateStates: newUpdateStateRegistry(), resolver: resolver}, validations, time.Now)
}

func newClientFactory(builder runtimeBuilder, validations ValidationStore, now func() time.Time) *ClientFactory {
	return &ClientFactory{builder: builder, validations: validations, now: now}
}

func (f *ClientFactory) New(account domain.Account) (TelegramClient, error) {
	if f == nil || f.builder == nil || f.validations == nil || f.now == nil {
		return nil, errors.New("client factory is not configured")
	}
	if account.ID == "" {
		return nil, errors.New("account ID is required")
	}
	if account.SessionPath == "" {
		return nil, errors.New("account session path is required")
	}
	info, err := os.Lstat(account.SessionPath)
	if err != nil {
		return nil, fmt.Errorf("inspect account session: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("account session path must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("account session must be a regular file")
	}
	if info.Mode().Perm() != fs.FileMode(0o600) {
		return nil, errors.New("account session must be owner-only (0600)")
	}
	runtime, err := f.builder.New(account.SessionPath)
	if err != nil {
		return nil, fmt.Errorf("create telegram client: %w", err)
	}
	return &validatedClient{runtime: runtime, account: account, validations: f.validations, now: f.now}, nil
}

func (f *ClientFactory) ResetExplicitLifecycle() {
	if resetter, ok := f.builder.(interface{ ResetExplicitLifecycle() }); ok {
		resetter.ResetExplicitLifecycle()
	}
}

type validatedClient struct {
	runtime     clientRuntime
	account     domain.Account
	validations ValidationStore
	now         func() time.Time
	selfUserID  atomic.Int64
}

func (c *validatedClient) Run(ctx context.Context, callback func(context.Context) error) error {
	return c.runtime.Run(ctx, func(runCtx context.Context) error {
		identity, err := c.runtime.FullSelf(runCtx)
		if err != nil {
			return fmt.Errorf("get full self identity: %w", err)
		}
		if !matchesExpectedIdentity(c.account, identity) {
			return errors.New("telegram account identity mismatch")
		}
		c.selfUserID.Store(identity.ID)
		validatedAt := c.now().UTC()
		if err := c.validations.MarkValidated(runCtx, c.account.ID, validatedAt); err != nil {
			return fmt.Errorf("persist account validation: %w", err)
		}
		return callback(runCtx)
	})
}

func (c *validatedClient) API() *tg.Client { return c.runtime.API() }

func (c *validatedClient) SelfUserID() int64 { return c.selfUserID.Load() }

func (c *validatedClient) SetUpdateHandler(handler telegram.UpdateHandler) {
	if setter, ok := c.runtime.(rawUpdateHandlerSetter); ok {
		setter.SetUpdateHandler(handler)
	}
}

func (c *validatedClient) Validate(ctx context.Context) (time.Time, error) {
	var identity Identity
	err := c.runtime.Run(ctx, func(runCtx context.Context) error {
		resolved, err := c.runtime.FullSelf(runCtx)
		if err != nil {
			return fmt.Errorf("get full self identity: %w", err)
		}
		identity = resolved
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	if !matchesExpectedIdentity(c.account, identity) {
		return time.Time{}, errors.New("telegram account identity mismatch")
	}
	c.selfUserID.Store(identity.ID)
	validatedAt := c.now().UTC()
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	if err := c.validations.MarkValidated(ctx, c.account.ID, validatedAt); err != nil {
		return time.Time{}, fmt.Errorf("persist account validation: %w", err)
	}
	return validatedAt, nil
}

func matchesExpectedIdentity(account domain.Account, actual Identity) bool {
	if !actual.Self {
		return false
	}
	if strings.TrimSpace(account.PhoneMasked) != "" {
		return maskedPhoneMatches(account.PhoneMasked, actual.Phone)
	}
	if username := normalizeUsername(account.Username); username != "" {
		return username == normalizeUsername(actual.Username)
	}
	expectedName := normalizeLabel(account.DisplayName)
	if expectedName == "" {
		return true
	}
	return expectedName == normalizeLabel(actual.DisplayName)
}

func maskedPhoneMatches(mask, phone string) bool {
	pattern := make([]rune, 0, len(mask))
	for _, char := range mask {
		switch {
		case unicode.IsDigit(char):
			pattern = append(pattern, char)
		case char == '*':
			pattern = append(pattern, '*')
		}
	}
	digits := []rune(digitsOnly(phone))
	if len(pattern) != len(digits) || len(pattern) == 0 {
		return false
	}
	for index, expected := range pattern {
		if expected != '*' && expected != digits[index] {
			return false
		}
	}
	return true
}

func digitsOnly(value string) string {
	return strings.Map(func(char rune) rune {
		if unicode.IsDigit(char) {
			return char
		}
		return -1
	}, value)
}

func normalizeUsername(value string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), "@"))
}

func normalizeLabel(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

type gotdRuntimeBuilder struct {
	appID          int
	appHash        string
	sessionBarrier *SessionBarrier
	updateStates   *updateStateRegistry
	resolver       dcs.Resolver
}

func (b gotdRuntimeBuilder) New(sessionPath string) (clientRuntime, error) {
	if b.appID <= 0 || strings.TrimSpace(b.appHash) == "" {
		return nil, errors.New("telegram app credentials are required")
	}
	if b.sessionBarrier == nil {
		return nil, errors.New("telegram session barrier is required")
	}
	if b.updateStates == nil {
		return nil, errors.New("telegram update state registry is required")
	}
	state := b.updateStates.forSession(sessionPath)
	client := telegram.NewClient(b.appID, b.appHash, telegram.Options{
		SessionStorage: newBarrierSessionStorage(&session.FileStorage{Path: sessionPath}, b.sessionBarrier),
		UpdateHandler:  state.gaps,
		Resolver:       b.resolver,
	})
	return &gotdRuntime{client: client, state: state}, nil
}

type gotdRuntime struct {
	client *telegram.Client
	state  *sessionUpdateState
}

func (r *gotdRuntime) Run(ctx context.Context, callback func(context.Context) error) error {
	return r.client.Run(ctx, func(runCtx context.Context) error {
		identity, err := r.FullSelf(runCtx)
		if err != nil {
			return err
		}
		ready := r.state.beginRun()
		group, groupCtx := errgroup.WithContext(runCtx)
		group.Go(func() error {
			if err := callback(groupCtx); err != nil {
				return err
			}
			return errClientCallbackComplete
		})
		group.Go(func() error {
			select {
			case <-groupCtx.Done():
				return groupCtx.Err()
			case <-ready:
			}
			forget := !r.state.initialized.Load()
			return r.state.gaps.Run(groupCtx, r.client.API(), identity.ID, telegramupdates.AuthOptions{
				Forget:  forget,
				OnStart: func(context.Context) { r.state.initialized.Store(true) },
			})
		})
		err = group.Wait()
		r.state.gaps.Reset()
		if errors.Is(err, errClientCallbackComplete) {
			return nil
		}
		return err
	})
}

func (r *gotdRuntime) API() *tg.Client { return r.client.API() }

func (r *gotdRuntime) SetUpdateHandler(handler telegram.UpdateHandler) {
	r.state.setHandler(handler)
}

func (r *gotdRuntime) FullSelf(ctx context.Context) (Identity, error) {
	full, err := r.client.API().UsersGetFullUser(ctx, &tg.InputUserSelf{})
	if err != nil {
		return Identity{}, err
	}
	for _, candidate := range full.Users {
		user, ok := candidate.(*tg.User)
		if !ok || !user.Self {
			continue
		}
		return identityFromUser(user), nil
	}
	return Identity{}, errors.New("users.getFullUser response did not contain self")
}

func identityFromUser(user *tg.User) Identity {
	return Identity{
		ID:          user.ID,
		Self:        user.Self,
		Phone:       user.Phone,
		Username:    user.Username,
		DisplayName: strings.TrimSpace(strings.Join([]string{user.FirstName, user.LastName}, " ")),
	}
}

var errClientCallbackComplete = errors.New("telegram client callback complete")

type switchableUpdateHandler struct {
	mu      sync.RWMutex
	handler telegram.UpdateHandler
}

type sessionUpdateState struct {
	gaps        *telegramupdates.Manager
	downstream  *switchableUpdateHandler
	initialized atomic.Bool
	readyMu     sync.Mutex
	ready       chan struct{}
	readySet    bool
}

func (s *sessionUpdateState) beginRun() <-chan struct{} {
	s.readyMu.Lock()
	s.ready = make(chan struct{})
	s.readySet = false
	ready := s.ready
	s.readyMu.Unlock()
	s.downstream.Set(nil)
	return ready
}

func (s *sessionUpdateState) setHandler(handler telegram.UpdateHandler) {
	s.downstream.Set(handler)
	s.readyMu.Lock()
	if s.ready != nil && !s.readySet {
		close(s.ready)
		s.readySet = true
	}
	s.readyMu.Unlock()
}

func (s *sessionUpdateState) resetExplicitLifecycle() {
	s.downstream.Set(nil)
	s.gaps.Reset()
	s.initialized.Store(false)
}

type updateStateRegistry struct {
	mu       sync.Mutex
	sessions map[string]*sessionUpdateState
}

func newUpdateStateRegistry() *updateStateRegistry {
	return &updateStateRegistry{sessions: make(map[string]*sessionUpdateState)}
}

func (r *updateStateRegistry) forSession(sessionPath string) *sessionUpdateState {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing := r.sessions[sessionPath]; existing != nil {
		return existing
	}
	downstream := newSwitchableUpdateHandler()
	state := &sessionUpdateState{
		downstream: downstream,
		gaps:       telegramupdates.New(telegramupdates.Config{Handler: downstream, MaxChannelDifferenceConcurrency: 1}),
	}
	r.sessions[sessionPath] = state
	return state
}

func (r *updateStateRegistry) ResetExplicitLifecycle() {
	r.mu.Lock()
	states := make([]*sessionUpdateState, 0, len(r.sessions))
	for _, state := range r.sessions {
		states = append(states, state)
	}
	r.mu.Unlock()
	for _, state := range states {
		state.resetExplicitLifecycle()
	}
}

func (b gotdRuntimeBuilder) ResetExplicitLifecycle() {
	b.updateStates.ResetExplicitLifecycle()
}

func newSwitchableUpdateHandler() *switchableUpdateHandler {
	return &switchableUpdateHandler{handler: telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil })}
}

func (h *switchableUpdateHandler) Set(handler telegram.UpdateHandler) {
	if handler == nil {
		handler = telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil })
	}
	h.mu.Lock()
	h.handler = handler
	h.mu.Unlock()
}

func (h *switchableUpdateHandler) Handle(ctx context.Context, updates tg.UpdatesClass) error {
	h.mu.RLock()
	handler := h.handler
	h.mu.RUnlock()
	return handler.Handle(ctx, updates)
}
