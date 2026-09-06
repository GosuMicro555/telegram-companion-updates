package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	keyring "github.com/zalando/go-keyring"

	"telegram-companion/internal/bootstrapstate"
	secretservice "telegram-companion/internal/service/secrets"
)

const rawSeedKeyBytes = 32

var environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type secretReader interface {
	Get(context.Context, string) ([]byte, error)
}

type optionalSecretReader struct {
	reader   secretReader
	excluded map[string]struct{}
}

func (r optionalSecretReader) Get(ctx context.Context, name string) ([]byte, error) {
	if _, ok := r.excluded[name]; ok {
		return nil, bootstrapstate.ErrSecretNotFound
	}
	value, err := r.reader.Get(ctx, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, bootstrapstate.ErrSecretNotFound
	}
	return value, err
}

type options struct {
	sourceData  string
	outputPath  string
	seedID      string
	appVersion  string
	seedKeyFile string
	seedKeyEnv  string
	keyringName string
	excluded    stringListFlag
}

type stringListFlag []string

func (values *stringListFlag) String() string { return strings.Join(*values, ",") }

func (values *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("state-bundle: excluded secret name is empty")
	}
	*values = append(*values, value)
	return nil
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("state-bundle", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var config options
	flags.StringVar(&config.sourceData, "source-data", "", "consistent application data directory")
	flags.StringVar(&config.outputPath, "output", "", "encrypted bootstrap bundle path")
	flags.StringVar(&config.seedID, "seed-id", "", "stable public bootstrap seed identifier")
	flags.StringVar(&config.appVersion, "version", "", "target application version")
	flags.StringVar(&config.seedKeyFile, "seed-key-file", "", "path to a raw 32-byte bootstrap seed key")
	flags.StringVar(&config.seedKeyEnv, "seed-key-env", "", "environment variable containing a base64 raw bootstrap seed key")
	flags.StringVar(&config.keyringName, "keyring-service", "telegram-companion", "source keyring service")
	flags.Var(&config.excluded, "exclude-secret", "keyring secret name to omit (repeatable)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("state-bundle: positional arguments are not supported")
	}
	seedKey, err := readSeedKey(config.seedKeyFile, config.seedKeyEnv)
	if err != nil {
		return err
	}
	defer clear(seedKey)
	excluded, err := validateExcludedSecrets(config.excluded)
	if err != nil {
		return err
	}
	store := secretservice.NewSecretStore(strings.TrimSpace(config.keyringName))
	if err := bootstrapstate.Pack(ctx, bootstrapstate.PackConfig{
		SourceData: config.sourceData,
		OutputPath: config.outputPath,
		BundleID:   config.seedID,
		AppVersion: config.appVersion,
		Key:        seedKey,
		Secrets:    optionalSecretReader{reader: store, excluded: excluded},
	}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "encrypted bootstrap state created: %s\n", config.outputPath)
	return err
}

func validateExcludedSecrets(values []string) (map[string]struct{}, error) {
	allowed := make(map[string]struct{})
	for _, name := range bootstrapstate.SecretNames() {
		allowed[name] = struct{}{}
	}
	excluded := make(map[string]struct{}, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value)
		if _, ok := allowed[name]; !ok {
			return nil, errors.New("state-bundle: excluded secret name is unsupported")
		}
		excluded[name] = struct{}{}
	}
	return excluded, nil
}

func readSeedKey(filePath, environmentName string) ([]byte, error) {
	filePath = strings.TrimSpace(filePath)
	environmentName = strings.TrimSpace(environmentName)
	if (filePath == "") == (environmentName == "") {
		return nil, errors.New("state-bundle: exactly one seed key source is required")
	}
	if filePath != "" {
		return readSeedKeyFile(filePath)
	}
	return readSeedKeyEnvironment(environmentName)
}

func readSeedKeyFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("state-bundle: read seed key file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("state-bundle: seed key file must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("state-bundle: read seed key file: %w", err)
	}
	defer file.Close()
	key, err := io.ReadAll(io.LimitReader(file, rawSeedKeyBytes+1))
	if err != nil {
		return nil, errors.New("state-bundle: read seed key file")
	}
	return validateSeedKey(key)
}

func readSeedKeyEnvironment(name string) ([]byte, error) {
	if !environmentNamePattern.MatchString(name) {
		return nil, errors.New("state-bundle: seed key environment variable name is invalid")
	}
	encoded, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(encoded) == "" {
		return nil, errors.New("state-bundle: seed key environment variable is required")
	}
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, errors.New("state-bundle: seed key environment variable must be base64")
	}
	return validateSeedKey(key)
}

func validateSeedKey(key []byte) ([]byte, error) {
	if len(key) != rawSeedKeyBytes {
		clear(key)
		return nil, errors.New("state-bundle: seed key must be exactly 32-byte raw key material")
	}
	return key, nil
}

func clear(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
