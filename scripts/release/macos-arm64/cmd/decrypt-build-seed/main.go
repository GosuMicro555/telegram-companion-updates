package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"telegram-companion/internal/licenseissuer"
)

type options struct {
	artifactPath string
	passwordPath string
	seedIDPath   string
	seedKeyPath  string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	configuration, err := parseOptions(arguments)
	if err != nil {
		return err
	}

	artifact, err := os.ReadFile(configuration.artifactPath)
	if err != nil {
		return errors.New("decrypt-build-seed: cannot read encrypted build seed")
	}
	password, err := os.ReadFile(configuration.passwordPath)
	if err != nil {
		return errors.New("decrypt-build-seed: cannot read build-seed password")
	}
	defer wipe(password)

	seedID, seedKey, err := licenseissuer.RestoreBootstrapSeedExport(
		strings.TrimSpace(string(artifact)),
		trimLineEndings(string(password)),
	)
	if err != nil {
		return errors.New("decrypt-build-seed: encrypted build seed could not be authenticated")
	}
	defer wipe(seedKey)

	if err := writePrivate(configuration.seedIDPath, []byte(seedID)); err != nil {
		return errors.New("decrypt-build-seed: cannot write temporary seed identity")
	}
	if err := writePrivate(configuration.seedKeyPath, seedKey); err != nil {
		_ = os.Remove(configuration.seedIDPath)
		return errors.New("decrypt-build-seed: cannot write temporary seed key")
	}
	return nil
}

func parseOptions(arguments []string) (options, error) {
	var result options
	flags := flag.NewFlagSet("decrypt-build-seed", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&result.artifactPath, "artifact", "", "encrypted .tcompbuildseed artifact")
	flags.StringVar(&result.passwordPath, "password-file", "", "file containing the artifact password")
	flags.StringVar(&result.seedIDPath, "seed-id-output", "", "private temporary seed ID output")
	flags.StringVar(&result.seedKeyPath, "seed-key-output", "", "private temporary raw key output")
	if err := flags.Parse(arguments); err != nil {
		return options{}, errors.New("decrypt-build-seed: invalid arguments")
	}
	if flags.NArg() != 0 || result.artifactPath == "" || result.passwordPath == "" ||
		result.seedIDPath == "" || result.seedKeyPath == "" || result.seedIDPath == result.seedKeyPath {
		return options{}, errors.New("decrypt-build-seed: all input and output paths are required")
	}
	return result, nil
}

func writePrivate(path string, value []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	written := false
	defer func() {
		_ = file.Close()
		if !written {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(value); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	written = true
	return nil
}

func trimLineEndings(value string) string {
	return strings.TrimRight(value, "\r\n")
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
