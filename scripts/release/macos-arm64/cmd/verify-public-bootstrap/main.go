package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"telegram-companion/internal/bootstrapstate"
)

const rawSeedKeyBytes = 32

type options struct {
	bundlePath  string
	seedID      string
	appVersion  string
	seedKeyFile string
}

type discardSecretWriter struct{}

func (discardSecretWriter) Set(context.Context, string, []byte) error {
	return nil
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("verify-public-bootstrap", flag.ContinueOnError)
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
		return errors.New("verify public bootstrap: positional arguments are not supported")
	}
	if strings.TrimSpace(config.bundlePath) == "" || strings.TrimSpace(config.seedID) == "" ||
		strings.TrimSpace(config.appVersion) == "" || strings.TrimSpace(config.seedKeyFile) == "" {
		return errors.New("verify public bootstrap: bundle, seed ID, version, and seed key file are required")
	}
	if err := requireRegularFile(config.bundlePath, "bundle"); err != nil {
		return err
	}
	key, err := readRawSeedKey(config.seedKeyFile)
	if err != nil {
		return err
	}
	defer clear(key)

	targetRoot, err := os.MkdirTemp("", "telegram-companion-bootstrap-verify-*")
	if err != nil {
		return errors.New("verify public bootstrap: create temporary directory")
	}
	defer os.RemoveAll(targetRoot)

	imported, err := bootstrapstate.Import(ctx, bootstrapstate.ImportConfig{
		BundlePath: config.bundlePath,
		TargetRoot: targetRoot,
		BundleID:   config.seedID,
		AppVersion: config.appVersion,
		Key:        key,
		Secrets:    discardSecretWriter{},
	})
	if err != nil {
		return fmt.Errorf("verify public bootstrap: %w", err)
	}
	if !imported {
		return errors.New("verify public bootstrap: bundle was not imported")
	}
	_, err = fmt.Fprintln(stdout, "public bootstrap bundle decrypted and verified")
	return err
}

func readRawSeedKey(path string) ([]byte, error) {
	if err := requireRegularFile(path, "seed key file"); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("verify public bootstrap: open seed key file")
	}
	defer file.Close()
	key, err := io.ReadAll(io.LimitReader(file, rawSeedKeyBytes+1))
	if err != nil {
		return nil, errors.New("verify public bootstrap: read seed key file")
	}
	if len(key) != rawSeedKeyBytes {
		clear(key)
		return nil, errors.New("verify public bootstrap: seed key must be exactly 32-byte raw key material")
	}
	return key, nil
}

func requireRegularFile(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("verify public bootstrap: %s is unavailable", label)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("verify public bootstrap: %s must be a regular file", label)
	}
	return nil
}

func clear(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
