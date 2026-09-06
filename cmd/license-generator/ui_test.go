package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestGeneratorUICopiesAndSavesIssuedLicense(t *testing.T) {
	backend := &fakeGeneratorBackend{}
	runtime := &fakeGeneratorRuntime{savePath: `C:\licenses\denis.tcomplicense`}
	ui := NewGeneratorUI(backend, runtime)
	ui.Startup(context.Background())
	token := testExportToken()

	if err := ui.CopyLicense(token); err != nil {
		t.Fatalf("CopyLicense() error = %v", err)
	}
	if runtime.clipboard != token {
		t.Fatalf("clipboard = %q, want token", runtime.clipboard)
	}
	saved, err := ui.SaveLicense(token, "license-denis")
	if err != nil {
		t.Fatalf("SaveLicense() error = %v", err)
	}
	if !saved || runtime.saveKind != fileKindLicense || runtime.defaultName != "license-denis.tcomplicense" {
		t.Fatalf("SaveLicense() dialog = (%v, %q, %q)", saved, runtime.saveKind, runtime.defaultName)
	}
	if runtime.writtenPath != runtime.savePath || string(runtime.writtenData) != token+"\n" || runtime.writtenMode != 0o600 {
		t.Fatalf("written file = (%q, %q, %v)", runtime.writtenPath, runtime.writtenData, runtime.writtenMode)
	}
}

func TestGeneratorUIExportConfirmsOnlyAfterSuccessfulWrite(t *testing.T) {
	backend := &fakeGeneratorBackend{backup: "TCPKEYBACKUP1.salt.nonce.ciphertext"}
	runtime := &fakeGeneratorRuntime{savePath: `C:\backups\generator.tcompkeybackup`}
	ui := NewGeneratorUI(backend, runtime)
	ui.Startup(context.Background())

	exported, err := ui.ExportBackup("portable password")
	if err != nil {
		t.Fatalf("ExportBackup() error = %v", err)
	}
	if !exported || backend.validateBackupCalls != 1 || backend.confirmCalls != 1 {
		t.Fatalf("ExportBackup() = %v, validate calls = %d, confirm calls = %d", exported, backend.validateBackupCalls, backend.confirmCalls)
	}
	if string(runtime.writtenData) != backend.backup+"\n" || runtime.saveKind != fileKindBackup {
		t.Fatalf("backup output = %q, kind = %q", runtime.writtenData, runtime.saveKind)
	}

	backend.confirmCalls = 0
	runtime.writeErr = errors.New("private filesystem details")
	if _, err := ui.ExportBackup("portable password"); !errors.Is(err, ErrGeneratorUIOperation) {
		t.Fatalf("ExportBackup(write failure) error = %v, want ErrGeneratorUIOperation", err)
	}
	if backend.confirmCalls != 0 {
		t.Fatal("ExportBackup() confirmed a backup that was not written")
	}
	if strings.Contains(ErrGeneratorUIOperation.Error(), runtime.writeErr.Error()) {
		t.Fatal("UI error exposes filesystem details")
	}

	backend.validateBackupCalls = 0
	backend.confirmCalls = 0
	runtime.writeErr = nil
	runtime.readData = []byte("corrupted backup after write")
	if _, err := ui.ExportBackup("portable password"); err == nil {
		t.Fatal("ExportBackup(corrupt reread) succeeded")
	}
	if backend.validateBackupCalls != 1 || backend.confirmCalls != 0 {
		t.Fatalf("corrupt reread validate calls = %d, confirm calls = %d", backend.validateBackupCalls, backend.confirmCalls)
	}
}

func TestGeneratorUIExportsEncryptedBootstrapSeedArtifact(t *testing.T) {
	seedKey := []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef")
	backend := &fakeGeneratorBackend{
		seedID:              "0123456789abcdef0123456789abcdef",
		seedKey:             seedKey,
		bootstrapSeedExport: "TCBUILDSEED1.password-encrypted-artifact",
	}
	runtime := &fakeGeneratorRuntime{savePath: `C:\bootstrap\public.tcompbuildseed`}
	ui := NewGeneratorUI(backend, runtime)
	ui.Startup(context.Background())

	exported, err := ui.ExportBootstrapSeed("a separate build seed password")
	if err != nil {
		t.Fatalf("ExportBootstrapSeed() error = %v", err)
	}
	if !exported || runtime.saveKind != fileKindBuildSeed || runtime.defaultName != backend.seedID+".tcompbuildseed" {
		t.Fatalf("ExportBootstrapSeed() dialog = (%v, %q, %q)", exported, runtime.saveKind, runtime.defaultName)
	}
	if backend.bootstrapSeedPassword != "a separate build seed password" {
		t.Fatalf("CreateBootstrapSeedExport() password = %q", backend.bootstrapSeedPassword)
	}
	if runtime.writtenPath != runtime.savePath || string(runtime.writtenData) != backend.bootstrapSeedExport+"\n" || runtime.writtenMode != 0o600 {
		t.Fatalf("seed export = (%q, %q, %v)", runtime.writtenPath, runtime.writtenData, runtime.writtenMode)
	}
	if bytes.Contains(runtime.writtenData, seedKey) || bytes.Contains(runtime.writtenData, []byte(backend.seedID)) {
		t.Fatal("ExportBootstrapSeed() wrote plaintext seed material")
	}
	status, err := json.Marshal(ui.GetStatus())
	if err != nil {
		t.Fatalf("Marshal(GetStatus()) error = %v", err)
	}
	if bytes.Contains(status, seedKey) {
		t.Fatal("GetStatus() exposes plaintext seed key to the Wails UI")
	}
}

func TestGeneratorUIAssetsNeverOfferRawSeedKeyExport(t *testing.T) {
	javascript, err := os.ReadFile("frontend/dist/app.js")
	if err != nil {
		t.Fatalf("ReadFile(app.js) error = %v", err)
	}
	html, err := os.ReadFile("frontend/dist/index.html")
	if err != nil {
		t.Fatalf("ReadFile(index.html) error = %v", err)
	}
	combined := string(javascript) + string(html)
	for _, forbidden := range []string{"ExportBootstrapSeedKey", "Export raw SeedKey", ".tcompseedkey"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("license generator assets expose unsafe seed export %q", forbidden)
		}
	}
	if !strings.Contains(string(javascript), "ExportBootstrapSeed(elements.password.value)") {
		t.Fatal("license generator UI does not password-protect the build seed export")
	}
}

func TestGeneratorUICancelDoesNotWriteOrConfirm(t *testing.T) {
	backend := &fakeGeneratorBackend{backup: "TCPKEYBACKUP1.salt.nonce.ciphertext"}
	runtime := &fakeGeneratorRuntime{}
	ui := NewGeneratorUI(backend, runtime)
	ui.Startup(context.Background())

	if saved, err := ui.SaveLicense(testExportToken(), "license-id"); err != nil || saved {
		t.Fatalf("SaveLicense(cancel) = (%v, %v)", saved, err)
	}
	if exported, err := ui.ExportBackup("portable password"); err != nil || exported {
		t.Fatalf("ExportBackup(cancel) = (%v, %v)", exported, err)
	}
	if len(runtime.writtenData) != 0 || backend.confirmCalls != 0 {
		t.Fatal("cancelled operation wrote data or confirmed a backup")
	}
}

func TestGeneratorUIRestoresSelectedEncryptedBackup(t *testing.T) {
	backend := &fakeGeneratorBackend{}
	runtime := &fakeGeneratorRuntime{
		openPath: `C:\backups\generator.tcompkeybackup`,
		readData: []byte("TCPKEYBACKUP1.salt.nonce.ciphertext\n"),
	}
	ui := NewGeneratorUI(backend, runtime)
	ui.Startup(context.Background())

	restored, err := ui.RestoreBackup("portable password")
	if err != nil {
		t.Fatalf("RestoreBackup() error = %v", err)
	}
	if !restored || backend.restoredBackup != "TCPKEYBACKUP1.salt.nonce.ciphertext" || backend.restoredPassword != "portable password" {
		t.Fatalf("RestoreBackup() forwarded (%q, %q)", backend.restoredBackup, backend.restoredPassword)
	}
	if runtime.openKind != fileKindBackup || runtime.readLimit != maxBackupFileSize {
		t.Fatalf("RestoreBackup() open kind/limit = %q/%d", runtime.openKind, runtime.readLimit)
	}
}

func TestGeneratorUIImportsSelectedLicenseAndWipesReadBuffer(t *testing.T) {
	token := testExportToken()
	backend := &fakeGeneratorBackend{importedRow: LicenseRow{LicenseID: "license-imported", RevocationState: licenseRevocationActive}}
	runtime := &fakeGeneratorRuntime{openPath: `C:\licenses\import.tcomplicense`, readData: []byte(token + "\n")}
	ui := NewGeneratorUI(backend, runtime)
	ui.Startup(context.Background())

	row, err := ui.ImportLicense()
	if err != nil {
		t.Fatalf("ImportLicense() error = %v", err)
	}
	if row != backend.importedRow || backend.importedToken != token || runtime.openKind != fileKindLicense || runtime.readLimit != maxLicenseFileSize {
		t.Fatalf("ImportLicense() row=%#v token=%q kind=%q limit=%d", row, backend.importedToken, runtime.openKind, runtime.readLimit)
	}
	if !bytes.Equal(runtime.returnedReadData, make([]byte, len(runtime.returnedReadData))) {
		t.Fatal("ImportLicense() retained the selected license token buffer")
	}
}

func TestGeneratorUIExposesBoundedRevocationActionsWithoutCredentialEcho(t *testing.T) {
	backend := &fakeGeneratorBackend{
		revokedRow: LicenseRow{LicenseID: "license-revoked", RevocationState: licenseRevocationPublished},
		retriedRow: LicenseRow{LicenseID: "license-retry", RevocationState: licenseRevocationPending},
	}
	ui := NewGeneratorUI(backend, &fakeGeneratorRuntime{})
	token := "github_pat_" + strings.Repeat("z", 82)
	if err := ui.ConfigureRevocationCredential(token); err != nil {
		t.Fatalf("ConfigureRevocationCredential() error = %v", err)
	}
	if backend.revocationCredential != token {
		t.Fatal("ConfigureRevocationCredential() did not forward the credential")
	}
	if row, err := ui.RevokeLicense("license-revoked"); err != nil || row != backend.revokedRow {
		t.Fatalf("RevokeLicense() = %#v, %v", row, err)
	}
	if row, err := ui.RetryRevocation("license-retry"); err != nil || row != backend.retriedRow {
		t.Fatalf("RetryRevocation() = %#v, %v", row, err)
	}
	if err := ui.ConfigureRevocationCredential(" token with spaces "); !errors.Is(err, ErrGeneratorUIInput) {
		t.Fatalf("ConfigureRevocationCredential(invalid) error = %v", err)
	}
	if _, err := ui.RevokeLicense(strings.Repeat("x", 257)); !errors.Is(err, ErrGeneratorUIInput) {
		t.Fatalf("RevokeLicense(invalid) error = %v", err)
	}
}

func TestGeneratorUIRejectsMalformedTokenBeforeClipboardOrDisk(t *testing.T) {
	runtime := &fakeGeneratorRuntime{savePath: `C:\licenses\invalid.tcomplicense`}
	ui := NewGeneratorUI(&fakeGeneratorBackend{}, runtime)
	ui.Startup(context.Background())

	if err := ui.CopyLicense("not-a-license"); !errors.Is(err, ErrGeneratorUIInput) {
		t.Fatalf("CopyLicense() error = %v, want ErrGeneratorUIInput", err)
	}
	if _, err := ui.SaveLicense("not-a-license", "id"); !errors.Is(err, ErrGeneratorUIInput) {
		t.Fatalf("SaveLicense() error = %v, want ErrGeneratorUIInput", err)
	}
	if runtime.clipboard != "" || len(runtime.writtenData) != 0 {
		t.Fatal("invalid token reached clipboard or disk")
	}
}

func TestGeneratorUIShowsBuildCompatibleRawLicensePublicKey(t *testing.T) {
	javascript, err := os.ReadFile("frontend/dist/app.js")
	if err != nil {
		t.Fatalf("ReadFile(app.js) error = %v", err)
	}
	html, err := os.ReadFile("frontend/dist/index.html")
	if err != nil {
		t.Fatalf("ReadFile(index.html) error = %v", err)
	}

	if !strings.Contains(string(javascript), "status.licensePublicKey") {
		t.Fatal("generator UI does not expose the raw build-compatible license public key")
	}
	if strings.Contains(string(html), "Публичный ключ SPKI") {
		t.Fatal("generator UI labels the operational key as SPKI")
	}
}

func TestGeneratorUIAssetsExposeSafeRevocationRegistryActions(t *testing.T) {
	javascript, err := os.ReadFile("frontend/dist/app.js")
	if err != nil {
		t.Fatalf("ReadFile(app.js) error = %v", err)
	}
	html, err := os.ReadFile("frontend/dist/index.html")
	if err != nil {
		t.Fatalf("ReadFile(index.html) error = %v", err)
	}
	for _, required := range []string{
		"status.licenses",
		"ImportLicense()",
		"ConfigureRevocationCredential",
		"RevokeLicense",
		"RetryRevocation",
		"revocationPublicKey",
		"Старые версии приложения не проверяют отзывы лицензий",
	} {
		if !strings.Contains(string(javascript)+string(html), required) {
			t.Fatalf("Generator assets are missing %q", required)
		}
	}
	for _, forbidden := range []string{"status.history", "machine_id", "entry.comment"} {
		if strings.Contains(string(javascript), forbidden) {
			t.Fatalf("Generator registry renders forbidden private field %q", forbidden)
		}
	}
}

func TestGeneratorRevocationActionRequiresExplicitIrreversibleConfirmationAndAlwaysRefreshes(t *testing.T) {
	javascript, err := os.ReadFile("frontend/dist/app.js")
	if err != nil {
		t.Fatalf("ReadFile(app.js) error = %v", err)
	}
	source := string(javascript)
	for _, required := range []string{
		"window.confirm",
		"Владелец: ${entry.owner}",
		"Срок: ${formatDate(entry.expiresAt)}",
		"необратимо",
		"новую лицензию",
		"Отозвать лицензию",
		"Публикация отзыва",
		"Ошибка публикации",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("revocation confirmation/status copy is missing %q", required)
		}
	}
	confirmation := strings.Index(source, "window.confirm")
	revokeCall := strings.Index(source, "await backend().RevokeLicense")
	if confirmation < 0 || revokeCall < 0 || confirmation > revokeCall {
		t.Fatalf("confirmation index=%d, revoke index=%d", confirmation, revokeCall)
	}
	finallyIndex := strings.Index(source[revokeCall:], "finally {")
	refreshIndex := strings.Index(source[revokeCall:], "await refreshStatus()")
	reenableIndex := strings.LastIndex(source[revokeCall:], "action.disabled = currentRevocationPublicationCode")
	if finallyIndex < 0 || refreshIndex < finallyIndex || reenableIndex < refreshIndex {
		t.Fatalf("revocation finally=%d refresh=%d re-enable=%d", finallyIndex, refreshIndex, reenableIndex)
	}
}

func TestGeneratorOperatorCopyNamesTheFinalRevocationEnabledRelease(t *testing.T) {
	html, err := os.ReadFile("frontend/dist/index.html")
	if err != nil {
		t.Fatalf("ReadFile(index.html) error = %v", err)
	}
	text := string(html)
	if !strings.Contains(text, "финальной сборке 0.8.3 с поддержкой отзывов или более новой версии") {
		t.Fatal("operator copy does not identify the final revocation-enabled 0.8.3 build boundary")
	}
}

func TestGeneratorUIShowsOnlyBoundedRevocationPublicationStateAndDisablesPublication(t *testing.T) {
	javascript, err := os.ReadFile("frontend/dist/app.js")
	if err != nil {
		t.Fatalf("ReadFile(app.js) error = %v", err)
	}
	html, err := os.ReadFile("frontend/dist/index.html")
	if err != nil {
		t.Fatalf("ReadFile(index.html) error = %v", err)
	}
	combined := string(javascript) + string(html)
	for _, required := range []string{
		"status.revocationPublicationCode",
		"revocation-publication-state",
		"remote_restore_required",
		"Требуется восстановить V3 backup",
		"currentRevocationPublicationCode !== \"ready\"",
	} {
		if !strings.Contains(combined, required) {
			t.Fatalf("bounded revocation publication state is missing %q", required)
		}
	}
	if strings.Contains(string(javascript), "textContent = status.revocationPublicationCode") {
		t.Fatal("UI renders the backend publication code without a bounded operator label")
	}
}

func TestGeneratorUIAssetsKeepRussianTextInUTF8(t *testing.T) {
	files := map[string][]string{
		"frontend/dist/index.html": {"Генератор лицензий", "Новая лицензия", "Публичный ключ лицензирования для сборки"},
		"frontend/dist/app.js":     {"Бессрочно", "Лицензия выпущена", "Ключ скопирован"},
		"main_windows.go":          {"Не удалось открыть защищённое хранилище генератора"},
		"runtime_windows.go":       {"Лицензия Telegram Companion", "Резервная копия ключа"},
	}

	for path, expected := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		text := string(content)
		for _, fragment := range expected {
			if !strings.Contains(text, fragment) {
				t.Errorf("%s does not contain valid UTF-8 text %q", path, fragment)
			}
		}
	}
}

type fakeGeneratorBackend struct {
	backup                string
	confirmCalls          int
	restoredBackup        string
	restoredPassword      string
	seedID                string
	seedKey               []byte
	bootstrapSeedExport   string
	bootstrapSeedPassword string
	validateBackupCalls   int
	validatedBackup       string
	validatedPassword     string
	importedToken         string
	importedRow           LicenseRow
	revocationCredential  string
	revokedRow            LicenseRow
	retriedRow            LicenseRow
}

func (backend *fakeGeneratorBackend) Status() GeneratorStatus {
	return GeneratorStatus{SeedID: backend.seedID}
}

func (*fakeGeneratorBackend) Issue(IssueRequest) (IssueResult, error) { return IssueResult{}, nil }

func (backend *fakeGeneratorBackend) CreateBackup(string) (string, error) {
	return backend.backup, nil
}

func (backend *fakeGeneratorBackend) ConfirmBackup() error {
	backend.confirmCalls++
	return nil
}

func (backend *fakeGeneratorBackend) ValidateBackup(backup, password string) error {
	backend.validateBackupCalls++
	backend.validatedBackup = backup
	backend.validatedPassword = password
	if backup != backend.backup {
		return errors.New("backup validation failed")
	}
	return nil
}

func (backend *fakeGeneratorBackend) RestoreBackup(backup, password string) error {
	backend.restoredBackup = backup
	backend.restoredPassword = password
	return nil
}

func (backend *fakeGeneratorBackend) CreateBootstrapSeedExport(password string) (string, error) {
	backend.bootstrapSeedPassword = password
	return backend.bootstrapSeedExport, nil
}

func (backend *fakeGeneratorBackend) ImportLicense(token string) (LicenseRow, error) {
	backend.importedToken = token
	return backend.importedRow, nil
}

func (backend *fakeGeneratorBackend) ConfigureRevocationCredential(token string) error {
	backend.revocationCredential = token
	return nil
}

func (backend *fakeGeneratorBackend) RevokeLicense(string) (LicenseRow, error) {
	return backend.revokedRow, nil
}

func (backend *fakeGeneratorBackend) RetryRevocation(string) (LicenseRow, error) {
	return backend.retriedRow, nil
}

type fakeGeneratorRuntime struct {
	clipboard        string
	savePath         string
	saveKind         fileKind
	defaultName      string
	openPath         string
	openKind         fileKind
	writtenPath      string
	writtenData      []byte
	writtenMode      os.FileMode
	writeErr         error
	readData         []byte
	readLimit        int64
	returnedReadData []byte
}

func (runtime *fakeGeneratorRuntime) CopyText(_ context.Context, text string) error {
	runtime.clipboard = text
	return nil
}

func (runtime *fakeGeneratorRuntime) SelectSavePath(_ context.Context, kind fileKind, defaultName string) (string, error) {
	runtime.saveKind = kind
	runtime.defaultName = defaultName
	return runtime.savePath, nil
}

func (runtime *fakeGeneratorRuntime) SelectOpenPath(_ context.Context, kind fileKind) (string, error) {
	runtime.openKind = kind
	return runtime.openPath, nil
}

func (runtime *fakeGeneratorRuntime) WriteFile(path string, data []byte, mode os.FileMode) error {
	if runtime.writeErr != nil {
		return runtime.writeErr
	}
	runtime.writtenPath = path
	runtime.writtenData = append([]byte(nil), data...)
	runtime.writtenMode = mode
	return nil
}

func (runtime *fakeGeneratorRuntime) ReadFile(_ string, limit int64) ([]byte, error) {
	runtime.readLimit = limit
	data := runtime.readData
	if data == nil {
		data = runtime.writtenData
	}
	runtime.returnedReadData = append([]byte(nil), data...)
	return runtime.returnedReadData, nil
}

var _ generatorBackend = (*fakeGeneratorBackend)(nil)
var _ generatorRuntime = (*fakeGeneratorRuntime)(nil)

func testExportToken() string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"schema":1}`))
	signature := base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	return "TCPLIC1." + payload + "." + signature
}
