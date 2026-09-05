package gotd

import (
	"github.com/gotd/td/tg"
	"telegram-companion/internal/driveaccounts"
	"testing"
)

func TestDriveIdentityMustMatchDecodedAccount(t *testing.T) {
	c := driveaccounts.Candidate{UserID: 12345}
	if err := applyDriveIdentity(&c, &tg.User{ID: 54321, Self: true}); err == nil {
		t.Fatal("accepted wrong identity")
	}
	if err := applyDriveIdentity(&c, &tg.User{ID: 12345}); err == nil {
		t.Fatal("accepted non-self identity")
	}
	if err := applyDriveIdentity(&c, &tg.User{ID: 12345, Self: true, FirstName: "Test", LastName: "Account", Phone: "123456789", Username: "test"}); err != nil {
		t.Fatal(err)
	}
	if c.Account.DisplayName != "Test Account" || c.Account.PhoneMasked == "123456789" || c.Account.PhoneMasked == "" {
		t.Fatal("identity was not safely mapped")
	}
}
