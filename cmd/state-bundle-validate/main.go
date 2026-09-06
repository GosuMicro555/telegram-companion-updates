package main

import (
	"context"
	"flag"
	"os"
	"strings"

	"telegram-companion/internal/bootstrapstate"
)

type transientSecretWriter struct{}

func (transientSecretWriter) Set(_ context.Context, _ string, value []byte) error {
	clear(value)
	return nil
}

func main() {
	statePath := flag.String("state", "", "path to the encrypted seeded state")
	licensePath := flag.String("license-file", "", "path to the target license")
	version := flag.String("version", "", "application version")
	flag.Parse()

	if err := validate(*statePath, *licensePath, *version); err != nil {
		_, _ = os.Stderr.WriteString("seeded state validation failed\n")
		os.Exit(1)
	}
}

func validate(statePath, licensePath, version string) error {
	if !regularFile(statePath) || !regularFile(licensePath) || strings.TrimSpace(version) == "" {
		return bootstrapstate.ErrInvalidBundle
	}
	licenseBytes, err := os.ReadFile(licensePath)
	if err != nil {
		return bootstrapstate.ErrInvalidBundle
	}
	license := strings.TrimSpace(string(licenseBytes))
	clear(licenseBytes)
	if license == "" {
		return bootstrapstate.ErrInvalidBundle
	}

	validationRoot, err := os.MkdirTemp("", "telegram-companion-seed-validation-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(validationRoot)

	imported, err := bootstrapstate.Import(context.Background(), bootstrapstate.ImportConfig{
		BundlePath: statePath,
		TargetRoot: validationRoot,
		AppVersion: version,
		License:    license,
		Secrets:    transientSecretWriter{},
	})
	if err != nil || !imported {
		return bootstrapstate.ErrInvalidBundle
	}
	return nil
}

func regularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}
