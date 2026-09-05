package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"telegram-companion/internal/domain"
	"telegram-companion/internal/driveaccounts"
	appcrypto "telegram-companion/internal/service/crypto"
	"testing"
)

func TestDriveAccountCommitIsAtomicAndDoesNotOverwrite(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.ToSlash(filepath.Join(t.TempDir(), "app.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	cipher, err := appcrypto.NewAccountCredentialsCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := NewDriveAccountStore(db, cipher)
	makeCandidate := func(id string) driveaccounts.Candidate {
		return driveaccounts.Candidate{Account: domain.Account{ID: domain.ID(id), DisplayName: "original", SessionPath: "private/session.json", Role: domain.AccountRoleSpammer}, Credentials: appcrypto.AppCredentials{AppID: 123, AppHash: "synthetic"}}
	}
	a := makeCandidate("one")
	if err = store.CommitDriveAccounts(ctx, []driveaccounts.Candidate{a}); err != nil {
		t.Fatal(err)
	}
	a.Account.DisplayName = "overwritten"
	if err = store.CommitDriveAccounts(ctx, []driveaccounts.Candidate{makeCandidate("two"), a}); err == nil {
		t.Fatal("accepted duplicate")
	}
	var count int
	var name string
	if err = db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&count); err != nil || count != 1 {
		t.Fatal("partial commit", err, count)
	}
	if err = db.QueryRow(`SELECT display_name FROM accounts WHERE id='one'`).Scan(&name); err != nil || name != "original" {
		t.Fatal("overwrote existing account")
	}
	creds, err := NewAccountCredentialStore(db, cipher).Load(ctx, "one")
	if err != nil || creds.AppHash != "synthetic" {
		t.Fatal("credential persistence", err)
	}
}
