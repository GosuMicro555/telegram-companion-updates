//go:build desktop

package main

import (
	"os"
	"path/filepath"
	"strconv"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/driveaccounts"
	"telegram-companion/internal/repository/sqlite"
	appcrypto "telegram-companion/internal/service/crypto"
	telegramgotd "telegram-companion/internal/telegram/gotd"
	wailsbindings "telegram-companion/internal/transport/wails"
	"telegram-companion/internal/usecase/runtimeconfig"
)

func configureDriveAccountImport(b *wailsbindings.Bindings, store *sqlite.AccountCredentialStore, factory *telegramgotd.PerAccountClientFactory, resolver telegramgotd.AccountResolver, dataDir string, credentials map[domain.ID]telegramgotd.AppCredentials) {
	// Public Telegram Desktop client parameters preserve the originating client
	// type for ordinary tdata that has no companion JSON. Explicit API settings
	// and per-archive metadata take precedence.
	// Source: github.com/thedemons/opentele, API.TelegramDesktop.
	defaults := appcrypto.AppCredentials{AppID: 2040, AppHash: "b18441a1ff607e10a989891a5462e627"}
	if id, err := strconv.Atoi(os.Getenv("TELEGRAM_API_ID")); err == nil && id > 0 && os.Getenv("TELEGRAM_API_HASH") != "" {
		defaults = appcrypto.AppCredentials{AppID: id, AppHash: os.Getenv("TELEGRAM_API_HASH")}
	}
	importer := &driveaccounts.Importer{Downloader: driveaccounts.NewDownloader(), Store: store.DriveAccountStore(), ScratchRoot: filepath.Join(dataDir, "drive-account-scratch"), SessionRoot: filepath.Join(dataDir, "drive-account-sessions"), Defaults: defaults, Verify: telegramgotd.NewDriveAccountVerifier(resolver)}
	importer.AfterCommit = func(candidates []driveaccounts.Candidate) {
		for _, c := range candidates {
			factory.UpsertCredentials(c.Account.ID, c.Credentials)
		}
		b.RuntimeStore().Update(func(next *runtimeconfig.Snapshot) {
			if next.Roles == nil {
				next.Roles = map[domain.ID]domain.AccountRole{}
			}
			if next.ProxyAssignments == nil {
				next.ProxyAssignments = map[domain.ID]string{}
			}
			for _, c := range candidates {
				next.Roles[c.Account.ID] = c.Account.Role
				next.ProxyAssignments[c.Account.ID] = string(domain.SystemProxyRouteID)
			}
		})
	}
	wailsbindings.ConfigureDriveAccounts(b, driveaccounts.NewService(importer.Process))
}
