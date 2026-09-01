//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	windowsoptions "github.com/wailsapp/wails/v2/pkg/options/windows"
	"golang.org/x/sys/windows"

	"telegram-companion/internal/licenseissuer"
	"telegram-companion/internal/revocation"
	"telegram-companion/internal/service/secrets"
)

const defaultBootstrapSeedPasswordEnv = "TC_LICENSE_GENERATOR_BOOTSTRAP_PASSWORD"

type bootstrapSeedExportCommand struct {
	outputPath  string
	passwordEnv string
}

//go:embed all:frontend/dist
var generatorAssets embed.FS

func main() {
	revocationCommand, handled, err := parseRevocationProvisioningCommand(os.Args[1:])
	if err != nil {
		exitGenerator("invalid revocation provisioning command")
	}
	if handled {
		if err := provisionRevocation(revocationCommand); err != nil {
			exitGenerator("revocation provisioning failed")
		}
		return
	}
	preparationCommand, handled, err := parseRevocationPreparationCommand(os.Args[1:])
	if err != nil {
		exitGenerator("invalid revocation preparation command")
	}
	if handled {
		if err := prepareRevocation(preparationCommand); err != nil {
			exitGenerator("revocation preparation failed")
		}
		return
	}

	command, handled, err := parseBootstrapSeedExportCommand(os.Args[1:])
	if err != nil {
		exitGenerator("invalid bootstrap seed export command")
	}
	if handled {
		if err := exportBootstrapSeed(command); err != nil {
			exitGenerator("bootstrap seed export failed")
		}
		return
	}

	root, err := licenseissuer.DefaultStateRoot()
	if err != nil {
		exitGenerator("Не удалось открыть защищённое хранилище генератора.")
	}
	store, err := licenseissuer.NewStateStore(root)
	if err != nil {
		exitGenerator("Не удалось открыть защищённое хранилище генератора.")
	}
	repository, err := newProtectedStateRepository(root, store)
	if err != nil {
		exitGenerator("Не удалось открыть защищённое хранилище генератора.")
	}
	revocationRepository, err := newRevocationStateRepository(root)
	if err != nil {
		exitGenerator("Не удалось открыть защищённое хранилище отзывов.")
	}
	service, err := newGeneratorServiceWithRevocation(repository, revocationRepository, revocationBootstrapRequireRestore, time.Now, rand.Reader)
	if err != nil {
		exitGenerator("Не удалось загрузить ключ лицензирования.")
	}
	credentials, err := newRevocationCredentials(secrets.NewSecretStore(generatorRevocationCredentialService))
	if err == nil {
		_ = service.AttachRevocationPublication(context.Background(), credentials, &githubRevocationPublisherFactory{})
	}
	ui := NewGeneratorUI(service, newWindowsGeneratorRuntime())

	err = wails.Run(&options.App{
		Title:            "Telegram Companion License Generator",
		Width:            1120,
		Height:           760,
		MinWidth:         920,
		MinHeight:        660,
		BackgroundColour: options.NewRGB(246, 248, 250),
		AssetServer:      &assetserver.Options{Assets: generatorAssets},
		OnStartup:        ui.Startup,
		OnShutdown:       func(context.Context) { service.Close() },
		Bind:             []interface{}{ui},
		Windows: &windowsoptions.Options{
			Theme:                windowsoptions.Light,
			BackdropType:         windowsoptions.Mica,
			IsZoomControlEnabled: false,
			DisablePinchZoom:     true,
			EnableSwipeGestures:  false,
			WebviewGpuIsDisabled: false,
			WindowClassName:      "TelegramCompanionLicenseGenerator",
		},
	})
	if err != nil {
		exitGenerator("Генератор лицензий завершился с ошибкой.")
	}
}

func parseBootstrapSeedExportCommand(arguments []string) (bootstrapSeedExportCommand, bool, error) {
	requested := false
	for _, argument := range arguments {
		if argument == "--export-bootstrap-seed" {
			requested = true
			break
		}
	}
	if !requested {
		return bootstrapSeedExportCommand{}, false, nil
	}

	flags := flag.NewFlagSet("license-generator", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	export := flags.Bool("export-bootstrap-seed", false, "export the universal bootstrap seed")
	output := flags.String("output", "", "new encrypted seed artifact path")
	passwordEnv := flags.String("password-env", defaultBootstrapSeedPasswordEnv, "environment variable containing the artifact password")
	if err := flags.Parse(arguments); err != nil || !*export || flags.NArg() != 0 {
		return bootstrapSeedExportCommand{}, true, errors.New("invalid bootstrap seed export arguments")
	}
	if strings.TrimSpace(*output) == "" || strings.TrimSpace(*passwordEnv) == "" {
		return bootstrapSeedExportCommand{}, true, errors.New("bootstrap seed export output and password environment are required")
	}
	return bootstrapSeedExportCommand{outputPath: *output, passwordEnv: *passwordEnv}, true, nil
}

func exportBootstrapSeed(command bootstrapSeedExportCommand) error {
	password := os.Getenv(command.passwordEnv)
	if password == "" {
		return errors.New("bootstrap seed export password is unavailable")
	}

	root, err := licenseissuer.DefaultStateRoot()
	if err != nil {
		return err
	}
	store, err := licenseissuer.NewStateStore(root)
	if err != nil {
		return err
	}
	repository, err := newProtectedStateRepository(root, store)
	if err != nil {
		return err
	}
	service, err := newExistingGeneratorService(repository)
	if err != nil {
		return err
	}
	defer service.Close()

	artifact, err := service.CreateBootstrapSeedExport(password)
	if err != nil {
		return err
	}
	return writeNewBootstrapSeedExport(command.outputPath, []byte(artifact+"\n"))
}

func provisionRevocation(command revocationProvisioningCommand) error {
	password := os.Getenv(command.passwordEnv)
	credential := os.Getenv(command.credentialEnv)
	_ = os.Unsetenv(command.passwordEnv)
	_ = os.Unsetenv(command.credentialEnv)
	if password == "" || credential == "" {
		return ErrGeneratorRevocationUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	root, err := licenseissuer.DefaultStateRoot()
	if err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	store, err := licenseissuer.NewStateStore(root)
	if err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	repository, err := newProtectedStateRepository(root, store)
	if err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	revocationRepository, err := newRevocationStateRepository(root)
	if err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	probe, err := newProductionGitHubRevocationAbsenceProbe()
	if err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	service, err := openRevocationProvisioningService(ctx, repository, revocationRepository, probe, time.Now, rand.Reader)
	if err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	defer service.Close()

	credentials, err := newRevocationCredentials(secrets.NewSecretStore(generatorRevocationCredentialService))
	if err != nil || credentials.Configure(ctx, credential) != nil {
		return ErrGeneratorRevocationUnavailable
	}
	if err := service.AttachRevocationPublication(ctx, credentials, &githubRevocationPublisherFactory{}); err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	return runRevocationProvisioning(ctx, command, password, service, windowsRevocationProvisioningArtifacts{})
}

func prepareRevocation(command revocationPreparationCommand) error {
	password := os.Getenv(command.passwordEnv)
	_ = os.Unsetenv(command.passwordEnv)
	if password == "" {
		return ErrGeneratorRevocationUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	root, err := licenseissuer.DefaultStateRoot()
	if err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	store, err := licenseissuer.NewStateStore(root)
	if err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	repository, err := newProtectedStateRepository(root, store)
	if err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	revocationRepository, err := newRevocationStateRepository(root)
	if err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	probe, err := newProductionGitHubRevocationAbsenceProbe()
	if err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	service, err := openRevocationPreparationService(ctx, repository, revocationRepository, probe, time.Now, rand.Reader)
	if err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	defer service.Close()
	return runRevocationPreparation(ctx, command, password, service, windowsRevocationProvisioningArtifacts{})
}

const (
	maxRevocationProvisioningBackupSize = 256 << 10
	maxRevocationProvisioningPublicSize = 4 << 10
)

type windowsRevocationProvisioningArtifacts struct{}

func (windowsRevocationProvisioningArtifacts) EnsureBackup(path string, candidate []byte, validate func([]byte) error) error {
	if strings.TrimSpace(path) == "" || len(candidate) == 0 || len(candidate) > maxRevocationProvisioningBackupSize || validate == nil {
		return ErrGeneratorRevocationUnavailable
	}
	return ensureRevocationProvisioningArtifact(path, maxRevocationProvisioningBackupSize, func() ([]byte, error) {
		return candidate, nil
	}, validate)
}

func (windowsRevocationProvisioningArtifacts) EnsurePublic(path string, document []byte) error {
	if strings.TrimSpace(path) == "" || len(document) == 0 || len(document) > maxRevocationProvisioningPublicSize {
		return ErrGeneratorRevocationUnavailable
	}
	return ensureRevocationProvisioningArtifact(path, maxRevocationProvisioningPublicSize, func() ([]byte, error) {
		return document, nil
	}, func(stored []byte) error {
		if !bytes.Equal(stored, document) {
			return ErrGeneratorRevocationUnavailable
		}
		return nil
	})
}

func (windowsRevocationProvisioningArtifacts) EnsureManifest(path string, create func() ([]byte, error), validate func([]byte) error) error {
	if strings.TrimSpace(path) == "" || create == nil || validate == nil {
		return ErrGeneratorRevocationUnavailable
	}
	return ensureRevocationProvisioningArtifact(path, int64(revocation.MaxEnvelopeBytes), create, validate)
}

func ensureRevocationProvisioningArtifact(path string, limit int64, create func() ([]byte, error), validate func([]byte) error) error {
	if strings.TrimSpace(path) == "" || limit < 1 || create == nil || validate == nil {
		return ErrGeneratorRevocationUnavailable
	}
	existing, err := readRevocationProvisioningArtifact(path, limit)
	if err == nil {
		defer wipeBytes(existing)
		if validate(existing) != nil {
			return ErrGeneratorRevocationUnavailable
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return ErrGeneratorRevocationUnavailable
	}
	candidate, err := create()
	if err != nil || len(candidate) == 0 || int64(len(candidate)) > limit || validate(candidate) != nil {
		return ErrGeneratorRevocationUnavailable
	}
	if err := writeNewBootstrapSeedExport(path, candidate); err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	stored, err := readRevocationProvisioningArtifact(path, limit)
	if err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	defer wipeBytes(stored)
	if !bytes.Equal(stored, candidate) || validate(stored) != nil {
		return ErrGeneratorRevocationUnavailable
	}
	return nil
}

func readRevocationProvisioningArtifact(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > limit {
		return nil, ErrGeneratorRevocationUnavailable
	}
	contents, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(contents) == 0 || int64(len(contents)) > limit {
		return nil, ErrGeneratorRevocationUnavailable
	}
	return contents, nil
}

func newExistingGeneratorService(repository stateRepository) (*generatorService, error) {
	if repository == nil {
		return nil, ErrGeneratorInitialization
	}
	state, err := repository.Load()
	if err != nil || !validKeyPair(state.PrivateKey, state.PublicKey) {
		return nil, ErrGeneratorInitialization
	}
	if state.SeedID == "" && len(state.SeedKey) == 0 {
		seedID, seedKey, seedErr := newSeedGrant(rand.Reader)
		if seedErr != nil {
			return nil, ErrGeneratorInitialization
		}
		state.SeedID = seedID
		state.SeedKey = seedKey
		state.BackupConfirmed = false
		if err := repository.Save(state); err != nil {
			wipeBytes(seedKey)
			return nil, ErrGeneratorStorage
		}
	} else if !validSeedGrant(state.SeedID, state.SeedKey) {
		return nil, ErrGeneratorInitialization
	}
	return &generatorService{
		repository: repository,
		now:        time.Now,
		random:     rand.Reader,
		state:      state,
	}, nil
}

func writeNewBootstrapSeedExport(path string, artifact []byte) error {
	if strings.TrimSpace(path) == "" || len(artifact) == 0 {
		return errors.New("invalid bootstrap seed export output")
	}
	outputPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(outputPath); err == nil {
		return errors.New("bootstrap seed export output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	directory := filepath.Dir(outputPath)
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return errors.New("bootstrap seed export directory is unavailable")
	}
	temporary, err := os.CreateTemp(directory, ".telegram-companion-build-seed-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	writeErr := temporary.Chmod(0o600)
	if writeErr == nil {
		_, writeErr = temporary.Write(artifact)
	}
	if writeErr == nil {
		writeErr = temporary.Sync()
	}
	closeErr := temporary.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}

	from, err := windows.UTF16PtrFromString(temporaryPath)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(outputPath)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}

func exitGenerator(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
