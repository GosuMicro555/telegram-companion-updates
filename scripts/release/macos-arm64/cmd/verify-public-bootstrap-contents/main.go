package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"telegram-companion/internal/bootstrapstate"

	bolt "go.etcd.io/bbolt"
	_ "modernc.org/sqlite"
)

const (
	rawSeedKeyBytes        = 32
	operationalSecretBytes = 32
)

var requiredOperationalSecrets = map[string]struct{}{
	"scout-message-key":               {},
	"outbound-target-key":             {},
	"proxy-credentials-v1":            {},
	"telegram-account-credentials-v1": {},
}

type options struct {
	bundlePath  string
	seedID      string
	appVersion  string
	seedKeyFile string
}

type secretPresence struct {
	found     map[string]struct{}
	forbidden bool
}

func (s *secretPresence) Set(_ context.Context, name string, value []byte) error {
	if _, required := requiredOperationalSecrets[name]; required && len(value) == operationalSecretBytes {
		s.found[name] = struct{}{}
	} else if len(value) > 0 {
		s.forbidden = true
	}
	return nil
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("verify-public-bootstrap-contents", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var config options
	flags.StringVar(&config.bundlePath, "bundle", "", "TCSEED2 public bootstrap bundle path")
	flags.StringVar(&config.seedID, "seed-id", "", "expected public bootstrap seed identifier")
	flags.StringVar(&config.appVersion, "version", "", "expected application version")
	flags.StringVar(&config.seedKeyFile, "seed-key-file", "", "path to a raw 32-byte bootstrap seed key")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("verify public bootstrap contents: positional arguments are not supported")
	}
	if strings.TrimSpace(config.bundlePath) == "" || strings.TrimSpace(config.seedID) == "" ||
		strings.TrimSpace(config.appVersion) == "" || strings.TrimSpace(config.seedKeyFile) == "" {
		return errors.New("verify public bootstrap contents: bundle, seed ID, version, and seed key file are required")
	}
	if err := requireRegularFile(config.bundlePath, "bundle"); err != nil {
		return err
	}
	key, err := readRawSeedKey(config.seedKeyFile)
	if err != nil {
		return err
	}
	defer clear(key)

	targetRoot, err := os.MkdirTemp("", "telegram-companion-bootstrap-contents-*")
	if err != nil {
		return errors.New("verify public bootstrap contents: create temporary directory")
	}
	defer os.RemoveAll(targetRoot)

	secrets := &secretPresence{found: make(map[string]struct{}, len(requiredOperationalSecrets))}
	imported, err := bootstrapstate.Import(ctx, bootstrapstate.ImportConfig{
		BundlePath: config.bundlePath,
		TargetRoot: targetRoot,
		BundleID:   config.seedID,
		AppVersion: config.appVersion,
		Key:        key,
		Secrets:    secrets,
	})
	if err != nil {
		return errors.New("verify public bootstrap contents: unable to decrypt and import bundle")
	}
	if !imported {
		return errors.New("verify public bootstrap contents: bundle was not imported")
	}
	dataRoot := filepath.Join(targetRoot, "data")
	if err := verifyApplicationDatabase(filepath.Join(dataRoot, "app.db")); err != nil {
		return err
	}
	if err := verifyApplicationState(filepath.Join(dataRoot, "application-state.bolt")); err != nil {
		return err
	}
	accounts, err := hasAccountRow(filepath.Join(dataRoot, "app.db"))
	if err != nil {
		return err
	}
	if !accounts {
		return errors.New("verify public bootstrap contents: account data is missing")
	}
	artifacts, err := hasTelegramArtifact(dataRoot)
	if err != nil {
		return err
	}
	if !artifacts {
		return errors.New("verify public bootstrap contents: Telegram operational artifact is missing")
	}
	if err := verifyRequiredOperationalRows(filepath.Join(dataRoot, "app.db")); err != nil {
		return err
	}
	if len(secrets.found) != len(requiredOperationalSecrets) {
		return errors.New("verify public bootstrap contents: operational credentials are incomplete")
	}
	if secrets.forbidden {
		return errors.New("verify public bootstrap contents: forbidden credentials are present")
	}
	_, err = fmt.Fprintln(stdout, "public bootstrap contents verified")
	return err
}

func verifyRequiredOperationalRows(path string) error {
	db, err := openReadOnlyDatabase(path)
	if err != nil {
		return errors.New("verify public bootstrap contents: application database is unusable")
	}
	defer db.Close()
	checks := []struct {
		query   string
		missing string
	}{
		{query: "SELECT EXISTS(SELECT 1 FROM outbound_channels LIMIT 1)", missing: "channel data"},
		{query: "SELECT EXISTS(SELECT 1 FROM canonical_keywords WHERE trigger_active = 1 LIMIT 1)", missing: "active keyword data"},
		{query: "SELECT EXISTS(SELECT 1 FROM live_delivery_history LIMIT 1)", missing: "Live-statistics data"},
	}
	for _, check := range checks {
		var present int
		if err := db.QueryRow(check.query).Scan(&present); err != nil {
			return errors.New("verify public bootstrap contents: application database is unusable")
		}
		if present != 1 {
			return fmt.Errorf("verify public bootstrap contents: %s is missing", check.missing)
		}
	}
	return nil
}

func verifyApplicationDatabase(path string) error {
	if err := requireNonEmptyRegularFile(path); err != nil {
		return errors.New("verify public bootstrap contents: application database is unusable")
	}
	db, err := openReadOnlyDatabase(path)
	if err != nil {
		return errors.New("verify public bootstrap contents: application database is unusable")
	}
	defer db.Close()
	var result string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&result); err != nil || result != "ok" {
		return errors.New("verify public bootstrap contents: application database is unusable")
	}
	return nil
}

func verifyApplicationState(path string) error {
	if err := requireNonEmptyRegularFile(path); err != nil {
		return errors.New("verify public bootstrap contents: application state is unusable")
	}
	database, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return errors.New("verify public bootstrap contents: application state is unusable")
	}
	defer database.Close()
	if err := database.View(func(*bolt.Tx) error { return nil }); err != nil {
		return errors.New("verify public bootstrap contents: application state is unusable")
	}
	return nil
}

func hasAccountRow(path string) (bool, error) {
	db, err := openReadOnlyDatabase(path)
	if err != nil {
		return false, errors.New("verify public bootstrap contents: application database is unusable")
	}
	defer db.Close()
	var accountID string
	err = db.QueryRow("SELECT id FROM accounts LIMIT 1").Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("verify public bootstrap contents: application database is unusable")
	}
	return true, nil
}

func hasTelegramArtifact(dataRoot string) (bool, error) {
	for _, directory := range []string{"tdata", "sessions", "gotd-import-staging"} {
		found, err := hasNonEmptyRegularFile(filepath.Join(dataRoot, directory))
		if err != nil {
			return false, errors.New("verify public bootstrap contents: inspect Telegram operational artifacts")
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}

func hasNonEmptyRegularFile(root string) (bool, error) {
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.IsDir() {
		return false, err
	}
	var found bool
	err = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Size() > 0 {
				found = true
				return fs.SkipDir
			}
		}
		return nil
	})
	return found, err
}

func openReadOnlyDatabase(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
}

func readRawSeedKey(path string) ([]byte, error) {
	if err := requireRegularFile(path, "seed key file"); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("verify public bootstrap contents: open seed key file")
	}
	defer file.Close()
	key, err := io.ReadAll(io.LimitReader(file, rawSeedKeyBytes+1))
	if err != nil {
		return nil, errors.New("verify public bootstrap contents: read seed key file")
	}
	if len(key) != rawSeedKeyBytes {
		clear(key)
		return nil, errors.New("verify public bootstrap contents: seed key must be exactly 32-byte raw key material")
	}
	return key, nil
}

func requireRegularFile(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("verify public bootstrap contents: %s is unavailable", label)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("verify public bootstrap contents: %s must be a regular file", label)
	}
	return nil
}

func requireNonEmptyRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("file is unusable")
	}
	return nil
}

func clear(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
