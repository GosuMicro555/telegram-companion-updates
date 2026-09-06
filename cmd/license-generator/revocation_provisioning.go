package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"path/filepath"
	"strings"
	"time"

	"telegram-companion/internal/licenseissuer"
	"telegram-companion/internal/revocation"
)

const (
	defaultRevocationBackupPasswordEnv = "TC_LICENSE_GENERATOR_REVOCATION_BACKUP_PASSWORD"
	defaultRevocationCredentialEnv     = "TC_LICENSE_GENERATOR_GITHUB_TOKEN"
)

type revocationProvisioningCommand struct {
	backupOutputPath string
	publicOutputPath string
	passwordEnv      string
	credentialEnv    string
}

type revocationPreparationCommand struct {
	backupOutputPath   string
	publicOutputPath   string
	manifestOutputPath string
	passwordEnv        string
}

type revocationAbsenceProver interface {
	ProveRemoteAbsent(context.Context) (bool, error)
}

type revocationProvisioningService interface {
	Status() GeneratorStatus
	CreateBackup(string) (string, error)
	ValidateBackup(string, string) error
	ConfirmBackup() error
	InitializeRevocationManifest(context.Context) error
}

type revocationProvisioningArtifacts interface {
	EnsureBackup(string, []byte, func([]byte) error) error
	EnsurePublic(string, []byte) error
}

type revocationPreparationService interface {
	CanPrepareInitialRevocationManifest() error
	Status() GeneratorStatus
	CreateBackup(string) (string, error)
	ValidateBackup(string, string) error
	ConfirmBackup() error
	PrepareInitialRevocationManifest() ([]byte, error)
	ValidateInitialRevocationManifest([]byte) error
}

type revocationPreparationArtifacts interface {
	EnsureBackup(string, []byte, func([]byte) error) error
	EnsurePublic(string, []byte) error
	EnsureManifest(string, func() ([]byte, error), func([]byte) error) error
}

func parseRevocationProvisioningCommand(arguments []string) (revocationProvisioningCommand, bool, error) {
	requested := false
	for _, argument := range arguments {
		if argument == "--provision-revocation" {
			requested = true
			break
		}
	}
	if !requested {
		return revocationProvisioningCommand{}, false, nil
	}

	flags := flag.NewFlagSet("license-generator", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	provision := flags.Bool("provision-revocation", false, "provision revocation publication")
	backupOutput := flags.String("backup-output", "", "new or matching V3 backup path")
	publicOutput := flags.String("public-output", "", "new or matching public key export path")
	passwordEnv := flags.String("password-env", defaultRevocationBackupPasswordEnv, "environment variable containing the V3 backup password")
	credentialEnv := flags.String("credential-env", defaultRevocationCredentialEnv, "environment variable containing the GitHub credential")
	if err := flags.Parse(arguments); err != nil || !*provision || flags.NArg() != 0 {
		return revocationProvisioningCommand{}, true, ErrGeneratorRevocationUnavailable
	}
	command := revocationProvisioningCommand{
		backupOutputPath: strings.TrimSpace(*backupOutput),
		publicOutputPath: strings.TrimSpace(*publicOutput),
		passwordEnv:      strings.TrimSpace(*passwordEnv),
		credentialEnv:    strings.TrimSpace(*credentialEnv),
	}
	if command.backupOutputPath == "" || command.publicOutputPath == "" || command.backupOutputPath == command.publicOutputPath || !validProvisioningEnvironmentName(command.passwordEnv) || !validProvisioningEnvironmentName(command.credentialEnv) || command.passwordEnv == command.credentialEnv {
		return revocationProvisioningCommand{}, true, ErrGeneratorRevocationUnavailable
	}
	return command, true, nil
}

func parseRevocationPreparationCommand(arguments []string) (revocationPreparationCommand, bool, error) {
	requested := false
	for _, argument := range arguments {
		if argument == "--prepare-revocation" {
			requested = true
			break
		}
		if strings.HasPrefix(argument, "--prepare-revocation=") {
			return revocationPreparationCommand{}, true, ErrGeneratorRevocationUnavailable
		}
	}
	if !requested {
		return revocationPreparationCommand{}, false, nil
	}

	flags := flag.NewFlagSet("license-generator", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	prepare := flags.Bool("prepare-revocation", false, "prepare revocation trust root offline")
	backupOutput := flags.String("backup-output", "", "new or matching V3 backup path")
	publicOutput := flags.String("public-output", "", "new or identical public key export path")
	manifestOutput := flags.String("manifest-output", "", "new or verified sequence-zero manifest path")
	passwordEnv := flags.String("password-env", defaultRevocationBackupPasswordEnv, "environment variable containing the V3 backup password")
	if err := flags.Parse(arguments); err != nil || !*prepare || flags.NArg() != 0 {
		return revocationPreparationCommand{}, true, ErrGeneratorRevocationUnavailable
	}
	command := revocationPreparationCommand{
		backupOutputPath:   strings.TrimSpace(*backupOutput),
		publicOutputPath:   strings.TrimSpace(*publicOutput),
		manifestOutputPath: strings.TrimSpace(*manifestOutput),
		passwordEnv:        strings.TrimSpace(*passwordEnv),
	}
	normalized, ok := normalizeRevocationPreparationPaths(command)
	if !ok || !validProvisioningEnvironmentName(command.passwordEnv) {
		return revocationPreparationCommand{}, true, ErrGeneratorRevocationUnavailable
	}
	normalized.passwordEnv = command.passwordEnv
	return normalized, true, nil
}

func normalizeRevocationPreparationPaths(command revocationPreparationCommand) (revocationPreparationCommand, bool) {
	values := []string{command.backupOutputPath, command.publicOutputPath, command.manifestOutputPath}
	for index, value := range values {
		if strings.TrimSpace(value) == "" {
			return revocationPreparationCommand{}, false
		}
		absolute, err := filepath.Abs(filepath.Clean(value))
		if err != nil {
			return revocationPreparationCommand{}, false
		}
		values[index] = filepath.Clean(absolute)
	}
	for left := range values {
		for right := left + 1; right < len(values); right++ {
			if strings.EqualFold(values[left], values[right]) {
				return revocationPreparationCommand{}, false
			}
		}
	}
	return revocationPreparationCommand{
		backupOutputPath:   values[0],
		publicOutputPath:   values[1],
		manifestOutputPath: values[2],
		passwordEnv:        command.passwordEnv,
	}, true
}

func validProvisioningEnvironmentName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for index, character := range name {
		if (character >= 'A' && character <= 'Z') || character == '_' || (index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}

func openRevocationProvisioningService(ctx context.Context, repository stateRepository, revocationRepository revocationStateStore, probe revocationAbsenceProver, now func() time.Time, random io.Reader) (*generatorService, error) {
	if ctx == nil || repository == nil || revocationRepository == nil || probe == nil || now == nil || random == nil {
		return nil, ErrGeneratorRevocationUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, ErrGeneratorRevocationUnavailable
	}
	signingState, err := repository.Load()
	if err != nil || !validKeyPair(signingState.PrivateKey, signingState.PublicKey) || !validSeedGrant(signingState.SeedID, signingState.SeedKey) {
		wipeBytes(signingState.PrivateKey)
		wipeBytes(signingState.SeedKey)
		return nil, ErrGeneratorRevocationUnavailable
	}
	wipeBytes(signingState.PrivateKey)
	wipeBytes(signingState.SeedKey)

	decision := revocationBootstrapRequireRestore
	revocationState, err := revocationRepository.Load()
	if errors.Is(err, licenseissuer.ErrStateNotFound) {
		absent, proofErr := probe.ProveRemoteAbsent(ctx)
		if proofErr != nil || !absent {
			return nil, ErrGeneratorRevocationUnavailable
		}
		decision = revocationBootstrapRemoteAbsent
	} else {
		defer wipeBytes(revocationState.PrivateKey)
		if err != nil || !validGeneratorRevocationState(revocationState) {
			return nil, ErrGeneratorRevocationUnavailable
		}
	}
	service, err := newGeneratorServiceWithRevocation(repository, revocationRepository, decision, now, random)
	if err != nil {
		return nil, ErrGeneratorRevocationUnavailable
	}
	return service, nil
}

func openRevocationPreparationService(ctx context.Context, repository stateRepository, revocationRepository revocationStateStore, probe revocationAbsenceProver, now func() time.Time, random io.Reader) (*generatorService, error) {
	if ctx == nil || repository == nil || revocationRepository == nil || probe == nil || now == nil || random == nil {
		return nil, ErrGeneratorRevocationUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, ErrGeneratorRevocationUnavailable
	}
	signingState, err := repository.Load()
	if err != nil || !validKeyPair(signingState.PrivateKey, signingState.PublicKey) || !validSeedGrant(signingState.SeedID, signingState.SeedKey) {
		wipeBytes(signingState.PrivateKey)
		wipeBytes(signingState.SeedKey)
		return nil, ErrGeneratorRevocationUnavailable
	}
	wipeBytes(signingState.PrivateKey)
	wipeBytes(signingState.SeedKey)

	absent, proofErr := probe.ProveRemoteAbsent(ctx)
	if proofErr != nil || !absent {
		return nil, ErrGeneratorRevocationUnavailable
	}
	current, loadErr := revocationRepository.Load()
	if loadErr == nil {
		defer wipeBytes(current.PrivateKey)
		if !validGeneratorRevocationState(current) || len(current.Records) != 0 {
			return nil, ErrGeneratorRevocationUnavailable
		}
	} else if !errors.Is(loadErr, licenseissuer.ErrStateNotFound) {
		return nil, ErrGeneratorRevocationUnavailable
	}
	service, err := newGeneratorServiceWithRevocation(repository, revocationRepository, revocationBootstrapRemoteAbsent, now, random)
	if err != nil {
		return nil, ErrGeneratorRevocationUnavailable
	}
	return service, nil
}

func runRevocationProvisioning(ctx context.Context, command revocationProvisioningCommand, password string, service revocationProvisioningService, artifacts revocationProvisioningArtifacts) error {
	if ctx == nil || service == nil || artifacts == nil || password == "" || command.backupOutputPath == "" || command.publicOutputPath == "" || command.backupOutputPath == command.publicOutputPath {
		return ErrGeneratorRevocationUnavailable
	}
	status := service.Status()
	publicKey, ok := decodeProvisioningPublicKey(status.RevocationPublicKey)
	if !ok || status.RevocationRestoreRequired {
		return ErrGeneratorRevocationUnavailable
	}
	backup, err := service.CreateBackup(password)
	if err != nil || !strings.HasPrefix(backup, generatorBackupV3Format+".") {
		return ErrGeneratorRevocationUnavailable
	}
	backupBytes := []byte(backup)
	defer wipeBytes(backupBytes)
	if err := artifacts.EnsureBackup(command.backupOutputPath, backupBytes, func(stored []byte) error {
		if len(stored) == 0 || !strings.HasPrefix(string(stored), generatorBackupV3Format+".") {
			return ErrGeneratorRevocationUnavailable
		}
		return service.ValidateBackup(string(stored), password)
	}); err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	if err := service.ConfirmBackup(); err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	confirmed := service.Status()
	if !confirmed.BackupConfirmed || !confirmed.RevocationBackupConfirmed || confirmed.RevocationPublicKey != status.RevocationPublicKey {
		return ErrGeneratorRevocationUnavailable
	}
	publicDocument, err := json.Marshal(struct {
		KeyID     string `json:"key_id"`
		PublicKey string `json:"public_key"`
	}{KeyID: generatorRevocationKeyID, PublicKey: base64.StdEncoding.EncodeToString(publicKey)})
	if err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	if err := artifacts.EnsurePublic(command.publicOutputPath, publicDocument); err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	if err := service.InitializeRevocationManifest(ctx); err != nil {
		return err
	}
	return nil
}

func runRevocationPreparation(ctx context.Context, command revocationPreparationCommand, password string, service revocationPreparationService, artifacts revocationPreparationArtifacts) error {
	if ctx == nil || service == nil || artifacts == nil || password == "" {
		return ErrGeneratorRevocationUnavailable
	}
	normalized, ok := normalizeRevocationPreparationPaths(command)
	if !ok {
		return ErrGeneratorRevocationUnavailable
	}
	command = normalized
	if err := service.CanPrepareInitialRevocationManifest(); err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	status := service.Status()
	publicKey, ok := decodeProvisioningPublicKey(status.RevocationPublicKey)
	if !ok || status.RevocationRestoreRequired {
		return ErrGeneratorRevocationUnavailable
	}
	backup, err := service.CreateBackup(password)
	if err != nil || !strings.HasPrefix(backup, generatorBackupV3Format+".") {
		return ErrGeneratorRevocationUnavailable
	}
	backupBytes := []byte(backup)
	defer wipeBytes(backupBytes)
	if err := artifacts.EnsureBackup(command.backupOutputPath, backupBytes, func(stored []byte) error {
		if len(stored) == 0 || !strings.HasPrefix(string(stored), generatorBackupV3Format+".") {
			return ErrGeneratorRevocationUnavailable
		}
		return service.ValidateBackup(string(stored), password)
	}); err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	if err := service.ConfirmBackup(); err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	confirmed := service.Status()
	if !confirmed.BackupConfirmed || !confirmed.RevocationBackupConfirmed || confirmed.RevocationPublicKey != status.RevocationPublicKey {
		return ErrGeneratorRevocationUnavailable
	}
	publicDocument, err := json.Marshal(struct {
		KeyID     string `json:"key_id"`
		PublicKey string `json:"public_key"`
	}{KeyID: generatorRevocationKeyID, PublicKey: base64.StdEncoding.EncodeToString(publicKey)})
	if err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	if err := artifacts.EnsurePublic(command.publicOutputPath, publicDocument); err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	if err := artifacts.EnsureManifest(command.manifestOutputPath, service.PrepareInitialRevocationManifest, service.ValidateInitialRevocationManifest); err != nil {
		return ErrGeneratorRevocationUnavailable
	}
	return nil
}

func (service *generatorService) PrepareInitialRevocationManifest() ([]byte, error) {
	if service == nil {
		return nil, ErrGeneratorRevocationUnavailable
	}
	service.mu.Lock()
	if service.now == nil || service.refreshRevocationStateLocked() != nil || !service.state.BackupConfirmed || !service.revocationState.BackupConfirmed || len(service.revocationState.Records) != 0 {
		service.mu.Unlock()
		return nil, ErrGeneratorRevocationUnavailable
	}
	publicKey := append(ed25519.PublicKey(nil), service.revocationState.PublicKey...)
	privateKey := append(ed25519.PrivateKey(nil), service.revocationState.PrivateKey...)
	now := service.now
	service.mu.Unlock()
	defer wipeBytes(privateKey)

	payload := revocation.Payload{
		Schema:      revocation.ManifestSchema,
		KeyID:       generatorRevocationKeyID,
		Sequence:    0,
		GeneratedAt: now().UTC().Truncate(time.Second).Format(time.RFC3339),
		Entries:     []revocation.Entry{},
	}
	envelope, err := revocation.Sign(payload, privateKey)
	if err != nil {
		return nil, ErrGeneratorRevocationUnavailable
	}
	manifest := []byte(envelope)
	if !validInitialRevocationManifest(manifest, publicKey) {
		return nil, ErrGeneratorRevocationUnavailable
	}
	return manifest, nil
}

func (service *generatorService) ValidateInitialRevocationManifest(candidate []byte) error {
	if service == nil {
		return ErrGeneratorRevocationUnavailable
	}
	service.mu.Lock()
	if service.refreshRevocationStateLocked() != nil || !service.state.BackupConfirmed || !service.revocationState.BackupConfirmed || len(service.revocationState.Records) != 0 {
		service.mu.Unlock()
		return ErrGeneratorRevocationUnavailable
	}
	publicKey := append(ed25519.PublicKey(nil), service.revocationState.PublicKey...)
	service.mu.Unlock()
	if !validInitialRevocationManifest(candidate, publicKey) {
		return ErrGeneratorRevocationUnavailable
	}
	return nil
}

func (service *generatorService) CanPrepareInitialRevocationManifest() error {
	if service == nil {
		return ErrGeneratorRevocationUnavailable
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.refreshRevocationStateLocked() != nil || len(service.revocationState.Records) != 0 {
		return ErrGeneratorRevocationUnavailable
	}
	return nil
}

func validInitialRevocationManifest(candidate []byte, publicKey ed25519.PublicKey) bool {
	if len(candidate) == 0 || len(candidate) > revocation.MaxEnvelopeBytes || len(publicKey) != ed25519.PublicKeySize {
		return false
	}
	verified, err := revocation.Verify(string(candidate), generatorRevocationKeyID, publicKey)
	if err != nil {
		return false
	}
	payload := verified.PayloadCopy()
	return payload.Schema == revocation.ManifestSchema && payload.KeyID == generatorRevocationKeyID && payload.Sequence == 0 && payload.Entries != nil && len(payload.Entries) == 0
}

func decodeProvisioningPublicKey(encoded string) (ed25519.PublicKey, bool) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != ed25519.PublicKeySize || base64.StdEncoding.EncodeToString(decoded) != encoded {
		return nil, false
	}
	return ed25519.PublicKey(decoded), true
}
