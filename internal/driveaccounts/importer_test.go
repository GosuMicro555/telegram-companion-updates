package driveaccounts

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/crypto"
	"github.com/gotd/td/session/tdesktop"
	appcrypto "telegram-companion/internal/service/crypto"
)

func TestConvertAccountUsesStableIdentityAndGotdSession(t *testing.T) {
	var key crypto.Key
	for i := range key {
		key[i] = byte(i)
	}
	source := tdesktop.Account{Authorization: tdesktop.MTPAuthorization{UserID: 12345, MainDC: 2, Keys: map[int]crypto.Key{2: key}}}
	c, err := convertAccount(source, appcrypto.AppCredentials{AppID: 123, AppHash: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if c.UserID != 12345 || c.Account.ID == "" || len(c.Session.AuthKey) != 256 {
		t.Fatal("incomplete conversion")
	}
	source.IDx = 8
	other, err := convertAccount(source, c.Credentials)
	if err != nil || other.Account.ID != c.Account.ID {
		t.Fatal("identity depends on archive position")
	}
}
func TestDiscoverFindsRootsAtAnyLevelAndRejectsUnknownData(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "outer", "tdata")
	if err := os.MkdirAll(p, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "key_datas"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	roots, err := discoverRoots(context.Background(), root)
	if err != nil || len(roots) != 1 || roots[0] != "outer/tdata" {
		t.Fatal("root discovery", err, roots)
	}
	if _, err = readCandidates(context.Background(), root, appcrypto.AppCredentials{AppID: 1, AppHash: "test"}); err == nil {
		t.Fatal("accepted invalid TData")
	}
}
