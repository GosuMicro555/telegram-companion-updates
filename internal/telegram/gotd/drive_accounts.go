package gotd

import (
	"context"
	"errors"
	"strings"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"telegram-companion/internal/domain"
	"telegram-companion/internal/driveaccounts"
)

func NewDriveAccountVerifier(resolver AccountResolver) func(context.Context, *driveaccounts.Candidate) error {
	return func(ctx context.Context, c *driveaccounts.Candidate) error {
		if resolver == nil {
			return errors.New("route_unavailable")
		}
		// Verify through the existing configured system route, without registering or
		// activating an account before its entire source has passed validation.
		temporary := c.Account
		temporary.ProxyMode = domain.ProxyModeGlobal
		route, err := resolver.ResolverFor(temporary)
		if err != nil || route == nil {
			return errors.New("route_unavailable")
		}
		client := telegram.NewClient(c.Credentials.AppID, c.Credentials.AppHash, telegram.Options{
			SessionStorage: newBarrierSessionStorage(&session.FileStorage{Path: c.Account.SessionPath}, ProductionSessionBarrier()), Resolver: route, NoUpdates: true,
		})
		err = client.Run(ctx, func(runCtx context.Context) error {
			user, e := client.Self(runCtx)
			if e != nil {
				return e
			}
			return applyDriveIdentity(c, user)
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return errors.New("session_invalid")
		}
		return nil
	}
}
func applyDriveIdentity(c *driveaccounts.Candidate, user *tg.User) error {
	if user == nil || !user.Self || user.ID <= 0 || uint64(user.ID) != c.UserID {
		return errors.New("session_invalid")
	}
	c.Account.DisplayName = strings.TrimSpace(user.FirstName + " " + user.LastName)
	c.Account.Username = user.Username
	phone := []rune(user.Phone)
	if len(phone) > 4 {
		for i := 0; i < len(phone)-4; i++ {
			phone[i] = '*'
		}
	} else {
		for i := range phone {
			phone[i] = '*'
		}
	}
	c.Account.PhoneMasked = string(phone)
	return nil
}
