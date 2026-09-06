package main

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"sync"
	"unicode"

	"telegram-companion/internal/license"
	"telegram-companion/internal/revocation"
)

const maxBackupFileSize int64 = 1 << 20
const maxLicenseFileSize int64 = 64 << 10

type fileKind string

const (
	fileKindLicense   fileKind = "license"
	fileKindBackup    fileKind = "backup"
	fileKindBuildSeed fileKind = "build_seed"
)

type GeneratorUIError string

func (e GeneratorUIError) Error() string { return "license generator UI: " + string(e) }

func (e GeneratorUIError) Is(target error) bool {
	other, ok := target.(GeneratorUIError)
	return ok && e == other
}

const (
	ErrGeneratorUIInput     GeneratorUIError = "invalid input"
	ErrGeneratorUIOperation GeneratorUIError = "operation failed"
)

type generatorBackend interface {
	Status() GeneratorStatus
	Issue(IssueRequest) (IssueResult, error)
	CreateBackup(string) (string, error)
	ValidateBackup(string, string) error
	CreateBootstrapSeedExport(string) (string, error)
	ConfirmBackup() error
	RestoreBackup(string, string) error
	ImportLicense(string) (LicenseRow, error)
	ConfigureRevocationCredential(string) error
	RevokeLicense(string) (LicenseRow, error)
	RetryRevocation(string) (LicenseRow, error)
}

type generatorRuntime interface {
	CopyText(context.Context, string) error
	SelectSavePath(context.Context, fileKind, string) (string, error)
	SelectOpenPath(context.Context, fileKind) (string, error)
	WriteFile(string, []byte, os.FileMode) error
	ReadFile(string, int64) ([]byte, error)
}

// GeneratorUI is the small Wails-facing API for the private Windows tool.
type GeneratorUI struct {
	backend generatorBackend
	runtime generatorRuntime
	mu      sync.RWMutex
	ctx     context.Context
}

func NewGeneratorUI(backend generatorBackend, runtime generatorRuntime) *GeneratorUI {
	return &GeneratorUI{backend: backend, runtime: runtime}
}

func (ui *GeneratorUI) Startup(ctx context.Context) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.ctx = ctx
}

func (ui *GeneratorUI) GetStatus() GeneratorStatus {
	if ui == nil || ui.backend == nil {
		return GeneratorStatus{}
	}
	return ui.backend.Status()
}

func (ui *GeneratorUI) Issue(request IssueRequest) (IssueResult, error) {
	if ui == nil || ui.backend == nil {
		return IssueResult{}, ErrGeneratorUIOperation
	}
	return ui.backend.Issue(request)
}

func (ui *GeneratorUI) CopyLicense(token string) error {
	ctx, ok := ui.operationContext()
	if !ok || !validLicenseToken(token) {
		return ErrGeneratorUIInput
	}
	if err := ui.runtime.CopyText(ctx, token); err != nil {
		return ErrGeneratorUIOperation
	}
	return nil
}

func (ui *GeneratorUI) SaveLicense(token, licenseID string) (bool, error) {
	ctx, ok := ui.operationContext()
	if !ok || !validLicenseToken(token) {
		return false, ErrGeneratorUIInput
	}
	path, err := ui.runtime.SelectSavePath(ctx, fileKindLicense, safeFileStem(licenseID)+".tcomplicense")
	if err != nil {
		return false, ErrGeneratorUIOperation
	}
	if path == "" {
		return false, nil
	}
	if err := ui.runtime.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return false, ErrGeneratorUIOperation
	}
	return true, nil
}

func (ui *GeneratorUI) ExportBackup(password string) (bool, error) {
	ctx, ok := ui.operationContext()
	if !ok {
		return false, ErrGeneratorUIOperation
	}
	backup, err := ui.backend.CreateBackup(password)
	if err != nil {
		return false, err
	}
	path, err := ui.runtime.SelectSavePath(ctx, fileKindBackup, "telegram-companion-license-key.tcompkeybackup")
	if err != nil {
		return false, ErrGeneratorUIOperation
	}
	if path == "" {
		return false, nil
	}
	if err := ui.runtime.WriteFile(path, []byte(backup+"\n"), 0o600); err != nil {
		return false, ErrGeneratorUIOperation
	}
	written, err := ui.runtime.ReadFile(path, maxBackupFileSize)
	if err != nil {
		return false, ErrGeneratorUIOperation
	}
	defer wipeBytes(written)
	if err := ui.backend.ValidateBackup(strings.TrimSpace(string(written)), password); err != nil {
		return false, err
	}
	if err := ui.backend.ConfirmBackup(); err != nil {
		return false, err
	}
	return true, nil
}

// ExportBootstrapSeed writes a password-encrypted seed-only artifact for the
// build operator. Neither the raw seed nor the artifact enters status/history.
func (ui *GeneratorUI) ExportBootstrapSeed(password string) (bool, error) {
	ctx, ok := ui.operationContext()
	if !ok {
		return false, ErrGeneratorUIOperation
	}
	artifact, err := ui.backend.CreateBootstrapSeedExport(password)
	if err != nil {
		return false, err
	}
	seedID := ui.backend.Status().SeedID
	if !validSeedID(seedID) {
		return false, ErrGeneratorUIOperation
	}
	path, err := ui.runtime.SelectSavePath(ctx, fileKindBuildSeed, seedID+".tcompbuildseed")
	if err != nil {
		return false, ErrGeneratorUIOperation
	}
	if path == "" {
		return false, nil
	}
	if err := ui.runtime.WriteFile(path, []byte(artifact+"\n"), 0o600); err != nil {
		return false, ErrGeneratorUIOperation
	}
	return true, nil
}

func (ui *GeneratorUI) RestoreBackup(password string) (bool, error) {
	ctx, ok := ui.operationContext()
	if !ok {
		return false, ErrGeneratorUIOperation
	}
	path, err := ui.runtime.SelectOpenPath(ctx, fileKindBackup)
	if err != nil {
		return false, ErrGeneratorUIOperation
	}
	if path == "" {
		return false, nil
	}
	encoded, err := ui.runtime.ReadFile(path, maxBackupFileSize)
	if err != nil {
		return false, ErrGeneratorUIOperation
	}
	if err := ui.backend.RestoreBackup(strings.TrimSpace(string(encoded)), password); err != nil {
		return false, err
	}
	return true, nil
}

func (ui *GeneratorUI) ImportLicense() (LicenseRow, error) {
	ctx, ok := ui.operationContext()
	if !ok {
		return LicenseRow{}, ErrGeneratorUIOperation
	}
	path, err := ui.runtime.SelectOpenPath(ctx, fileKindLicense)
	if err != nil {
		return LicenseRow{}, ErrGeneratorUIOperation
	}
	if path == "" {
		return LicenseRow{}, nil
	}
	encoded, err := ui.runtime.ReadFile(path, maxLicenseFileSize)
	if err != nil {
		return LicenseRow{}, ErrGeneratorUIOperation
	}
	defer wipeBytes(encoded)
	token := strings.TrimSpace(string(encoded))
	if !validLicenseToken(token) {
		return LicenseRow{}, ErrGeneratorUIInput
	}
	return ui.backend.ImportLicense(token)
}

func (ui *GeneratorUI) ConfigureRevocationCredential(token string) error {
	if ui == nil || ui.backend == nil {
		return ErrGeneratorUIOperation
	}
	secret := []byte(token)
	defer wipeBytes(secret)
	if !validGeneratorRevocationCredential(secret) {
		return ErrGeneratorUIInput
	}
	return ui.backend.ConfigureRevocationCredential(token)
}

func (ui *GeneratorUI) RevokeLicense(licenseID string) (LicenseRow, error) {
	if ui == nil || ui.backend == nil || !validRevocationLicenseID(licenseID) {
		return LicenseRow{}, ErrGeneratorUIInput
	}
	return ui.backend.RevokeLicense(licenseID)
}

func (ui *GeneratorUI) RetryRevocation(licenseID string) (LicenseRow, error) {
	if ui == nil || ui.backend == nil || !validRevocationLicenseID(licenseID) {
		return LicenseRow{}, ErrGeneratorUIInput
	}
	return ui.backend.RetryRevocation(licenseID)
}

func validRevocationLicenseID(licenseID string) bool {
	_, err := revocation.DeriveHandle(licenseID)
	return err == nil
}

func (ui *GeneratorUI) operationContext() (context.Context, bool) {
	if ui == nil || ui.backend == nil || ui.runtime == nil {
		return nil, false
	}
	ui.mu.RLock()
	defer ui.mu.RUnlock()
	return ui.ctx, ui.ctx != nil
}

func validLicenseToken(token string) bool {
	if len(token) == 0 || len(token) > 64<<10 || strings.IndexFunc(token, unicode.IsSpace) >= 0 {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != license.TokenPrefix || parts[1] == "" {
		return false
	}
	payload, payloadErr := base64.RawURLEncoding.DecodeString(parts[1])
	signature, signatureErr := base64.RawURLEncoding.DecodeString(parts[2])
	return payloadErr == nil && len(payload) > 0 && signatureErr == nil && len(signature) == 64
}

func safeFileStem(value string) string {
	value = strings.TrimSpace(value)
	var result strings.Builder
	for _, character := range value {
		if result.Len() >= 96 {
			break
		}
		switch {
		case character >= 'a' && character <= 'z':
			result.WriteRune(character)
		case character >= 'A' && character <= 'Z':
			result.WriteRune(character)
		case character >= '0' && character <= '9':
			result.WriteRune(character)
		case character == '-' || character == '_':
			result.WriteRune(character)
		}
	}
	if result.Len() == 0 {
		return "telegram-companion-license"
	}
	return result.String()
}
