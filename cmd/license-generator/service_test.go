package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"telegram-companion/internal/license"
	"telegram-companion/internal/licenseissuer"
)

func TestNewGeneratorServiceCreatesAndPersistsMissingKey(t *testing.T) {
	repository := &memoryStateRepository{loadErr: licenseissuer.ErrStateNotFound}
	service, err := newGeneratorService(repository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService() error = %v", err)
	}
	if repository.saveCalls != 1 {
		t.Fatalf("Save() calls = %d, want 1", repository.saveCalls)
	}
	status := service.Status()
	wantPublicKey := base64.StdEncoding.EncodeToString(repository.state.PublicKey)
	if status.LicensePublicKey != wantPublicKey || status.PublicKeySPKI == "" || status.BackupConfirmed || len(status.History) != 0 {
		t.Fatalf("Status() = %#v", status)
	}
	if len(repository.state.PrivateKey) != ed25519.PrivateKeySize || len(repository.state.PublicKey) != ed25519.PublicKeySize {
		t.Fatal("newGeneratorService() did not persist a full Ed25519 key pair")
	}
}

func TestIssueCreatesPublicMacLicenseAndPersistsTokenFreeHistory(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	service, err := newGeneratorService(repository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService() error = %v", err)
	}
	machineID := strings.Repeat("a1", 32)
	result, err := service.Issue(IssueRequest{
		MachineID: machineID + " ",
		Owner:     "  Denis  ",
		Comment:   "  first Mac  ",
		ExpiresAt: "2027-07-21T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if !strings.HasPrefix(result.Token, license.TokenPrefix+".") {
		t.Fatalf("Issue() token = %q", result.Token)
	}
	payload, err := license.ParseAndVerify(result.Token, license.VerifyOptions{
		PublicKey: repository.state.PublicKey,
		Product:   "telegram-companion",
		Channel:   "public-macos-arm64",
		MachineID: strings.ToUpper(machineID),
		Now:       fixedNow(),
	})
	if err != nil {
		t.Fatalf("ParseAndVerify() error = %v", err)
	}
	if payload.Owner != "Denis" || payload.Comment != "first Mac" || payload.IssuedAt != fixedNow().Format(time.RFC3339) {
		t.Fatalf("issued payload = %#v", payload)
	}
	if repository.saveCalls != 1 || len(repository.state.History) != 1 {
		t.Fatalf("persisted state = %#v, saves = %d", repository.state, repository.saveCalls)
	}
	if repository.state.History[0].Schema != 1 {
		t.Fatalf("history schema = %d, want 1 after removing the secret seed grant", repository.state.History[0].Schema)
	}
	historyJSON, err := licenseissuer.MarshalHistory(repository.state.History)
	if err != nil {
		t.Fatalf("MarshalHistory() error = %v", err)
	}
	if bytes.Contains(historyJSON, []byte(result.Token)) || bytes.Contains(historyJSON, repository.state.PrivateKey) {
		t.Fatal("issuance history persisted token or private-key material")
	}
}

func TestIssueCreatesSchemaV2LicensesWithOneStableSeedGrant(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	service, err := newGeneratorService(repository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService() error = %v", err)
	}

	first, err := service.Issue(IssueRequest{MachineID: strings.Repeat("AB", 32), Owner: "Denis"})
	if err != nil {
		t.Fatalf("Issue(first) error = %v", err)
	}
	second, err := service.Issue(IssueRequest{MachineID: strings.Repeat("CD", 32), Owner: "Maria"})
	if err != nil {
		t.Fatalf("Issue(second) error = %v", err)
	}

	parse := func(token, machineID string) license.Payload {
		t.Helper()
		payload, parseErr := license.ParseAndVerify(token, license.VerifyOptions{
			PublicKey: repository.state.PublicKey,
			Product:   licenseProduct,
			Channel:   licenseChannel,
			MachineID: machineID,
			Now:       fixedNow(),
		})
		if parseErr != nil {
			t.Fatalf("ParseAndVerify() error = %v", parseErr)
		}
		return payload
	}
	firstPayload := parse(first.Token, strings.Repeat("AB", 32))
	secondPayload := parse(second.Token, strings.Repeat("CD", 32))
	firstGrant, err := firstPayload.SeedGrant()
	if err != nil {
		t.Fatalf("first SeedGrant() error = %v", err)
	}
	secondGrant, err := secondPayload.SeedGrant()
	if err != nil {
		t.Fatalf("second SeedGrant() error = %v", err)
	}
	if firstPayload.Schema != 2 || secondPayload.Schema != 2 || firstGrant.ID != secondGrant.ID || !bytes.Equal(firstGrant.Key, secondGrant.Key) || len(firstGrant.Key) != 32 {
		t.Fatalf("issued seed grants = %#v and %#v", firstPayload, secondPayload)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("Marshal(IssueResult) error = %v", err)
	}
	if bytes.Contains(encoded, []byte(firstPayload.SeedKey)) {
		t.Fatal("IssueResult exposes the seed key outside the license token")
	}
}

func TestStatusExposesStableSeedIDWithoutSeedKey(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	service, err := newGeneratorService(repository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService() error = %v", err)
	}

	status, err := json.Marshal(service.Status())
	if err != nil {
		t.Fatalf("Marshal(Status()) error = %v", err)
	}
	if !bytes.Contains(status, []byte(repository.state.SeedID)) {
		t.Fatal("Status() does not expose the stable SeedID for bootstrap assembly")
	}
	for _, secret := range []string{
		base64.RawStdEncoding.EncodeToString(repository.state.SeedKey),
		hex.EncodeToString(repository.state.SeedKey),
	} {
		if bytes.Contains(status, []byte(secret)) {
			t.Fatal("Status() exposes plaintext SeedKey")
		}
	}
}

func TestNewGeneratorServiceMigratesExistingStateWithoutReplacingSigningKeyOrHistory(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	entry, err := licenseissuer.NewHistoryEntry(validPayload())
	if err != nil {
		t.Fatalf("NewHistoryEntry() error = %v", err)
	}
	repository.state.History = []licenseissuer.HistoryEntry{entry}
	repository.state.SeedID = ""
	repository.state.SeedKey = nil
	originalKey := append(ed25519.PrivateKey(nil), repository.state.PrivateKey...)
	originalPublicKey := append(ed25519.PublicKey(nil), repository.state.PublicKey...)

	service, err := newGeneratorService(repository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService() error = %v", err)
	}
	if repository.saveCalls != 1 {
		t.Fatalf("migration Save() calls = %d, want 1", repository.saveCalls)
	}
	if !bytes.Equal(repository.state.PrivateKey, originalKey) || !bytes.Equal(repository.state.PublicKey, originalPublicKey) || len(repository.state.History) != 1 || repository.state.History[0] != entry {
		t.Fatal("migration replaced the signing key or discarded history")
	}
	issued, err := service.Issue(IssueRequest{MachineID: strings.Repeat("AB", 32), Owner: "Denis"})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	payload, err := license.ParseAndVerify(issued.Token, license.VerifyOptions{
		PublicKey: repository.state.PublicKey,
		Product:   licenseProduct,
		Channel:   licenseChannel,
		MachineID: strings.Repeat("AB", 32),
		Now:       fixedNow(),
	})
	if err != nil {
		t.Fatalf("ParseAndVerify() error = %v", err)
	}
	grant, err := payload.SeedGrant()
	if err != nil {
		t.Fatalf("SeedGrant() error = %v", err)
	}

	reloaded, err := newGeneratorService(repository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService(reload) error = %v", err)
	}
	if repository.saveCalls != 2 {
		t.Fatalf("reload Save() calls = %d, want 2", repository.saveCalls)
	}
	issuedAgain, err := reloaded.Issue(IssueRequest{MachineID: strings.Repeat("CD", 32), Owner: "Maria"})
	if err != nil {
		t.Fatalf("Issue(reload) error = %v", err)
	}
	payloadAgain, err := license.ParseAndVerify(issuedAgain.Token, license.VerifyOptions{
		PublicKey: repository.state.PublicKey,
		Product:   licenseProduct,
		Channel:   licenseChannel,
		MachineID: strings.Repeat("CD", 32),
		Now:       fixedNow(),
	})
	if err != nil {
		t.Fatalf("ParseAndVerify(reload) error = %v", err)
	}
	grantAgain, err := payloadAgain.SeedGrant()
	if err != nil {
		t.Fatalf("SeedGrant(reload) error = %v", err)
	}
	if grant.ID != grantAgain.ID || !bytes.Equal(grant.Key, grantAgain.Key) {
		t.Fatal("migration did not persist a stable seed grant")
	}
}

func TestNewGeneratorServiceResetsBackupConfirmationWhenGeneratingMissingSeedGrant(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	repository.state.BackupConfirmed = true
	repository.state.SeedID = ""
	repository.state.SeedKey = nil

	service, err := newGeneratorService(repository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService() error = %v", err)
	}
	if service.Status().BackupConfirmed || repository.state.BackupConfirmed {
		t.Fatal("newGeneratorService() retained backup confirmation after generating a new seed grant")
	}
}

func TestIssueUsesOneConsistentTimestamp(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	clockCalls := 0
	clock := func() time.Time {
		value := fixedNow().Add(time.Duration(clockCalls) * time.Hour)
		clockCalls++
		return value
	}
	service, err := newGeneratorService(repository, clock, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService() error = %v", err)
	}
	result, err := service.Issue(IssueRequest{
		MachineID: strings.Repeat("AB", 32),
		Owner:     "Denis",
		ExpiresAt: fixedNow().Add(30 * time.Minute).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if clockCalls != 1 {
		t.Fatalf("clock calls = %d, want 1", clockCalls)
	}
	if result.Payload.IssuedAt != fixedNow().Format(time.RFC3339) {
		t.Fatalf("IssuedAt = %q, want %q", result.Payload.IssuedAt, fixedNow().Format(time.RFC3339))
	}
}

func TestIssueDoesNotReturnUnrecordedTokenWhenStateSaveFails(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	repository.saveErr = errors.New("disk path and secret must stay private")
	service, err := newGeneratorService(repository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService() error = %v", err)
	}
	result, err := service.Issue(IssueRequest{
		MachineID: strings.Repeat("AB", 32),
		Owner:     "Denis",
	})
	if !errors.Is(err, ErrGeneratorStorage) {
		t.Fatalf("Issue() error = %v, want ErrGeneratorStorage", err)
	}
	if result.Token != "" || len(service.Status().History) != 0 {
		t.Fatal("Issue() returned or retained an unrecorded token")
	}
	if strings.Contains(err.Error(), repository.saveErr.Error()) {
		t.Fatal("Issue() error exposes storage details")
	}
}

func TestIssueRejectsInvalidInput(t *testing.T) {
	tests := []IssueRequest{
		{MachineID: "short", Owner: "Denis"},
		{MachineID: strings.Repeat("ZZ", 32), Owner: "Denis"},
		{MachineID: strings.Repeat("AB", 32), Owner: "  "},
		{MachineID: strings.Repeat("AB", 32), Owner: "Denis", ExpiresAt: "not-a-date"},
		{MachineID: strings.Repeat("AB", 32), Owner: "Denis", ExpiresAt: fixedNow().Format(time.RFC3339)},
	}
	for index, request := range tests {
		t.Run(string(rune('A'+index)), func(t *testing.T) {
			repository := repositoryWithGeneratedState(t)
			service, err := newGeneratorService(repository, fixedNow, deterministicRandom())
			if err != nil {
				t.Fatalf("newGeneratorService() error = %v", err)
			}
			if _, err := service.Issue(request); !errors.Is(err, ErrInvalidIssueRequest) {
				t.Fatalf("Issue() error = %v, want ErrInvalidIssueRequest", err)
			}
		})
	}
}

func TestIssueRejectsSecretMaterialBeforeWritingHistory(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	service, err := newGeneratorService(repository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService() error = %v", err)
	}
	privateKey := repository.state.PrivateKey
	seed := privateKey.Seed()
	tests := []IssueRequest{
		{MachineID: strings.Repeat("AB", 32), Owner: "Denis", Comment: "previous " + testExportToken()},
		{MachineID: strings.Repeat("AB", 32), Owner: "Denis", Comment: "TCPKEYBACKUP1.salt.nonce.ciphertext"},
		{MachineID: strings.Repeat("AB", 32), Owner: "Denis", Comment: "TCPKEYBACKUP2.signing.seed"},
		{MachineID: strings.Repeat("AB", 32), Owner: "Denis", Comment: "TCBUILDSEED1.encrypted-seed-artifact"},
		{MachineID: strings.Repeat("AB", 32), Owner: "Denis", Comment: base64.StdEncoding.EncodeToString(privateKey)},
		{MachineID: strings.Repeat("AB", 32), Owner: "Denis", Comment: base64.RawURLEncoding.EncodeToString(privateKey)},
		{MachineID: strings.Repeat("AB", 32), Owner: "Denis", Comment: base64.StdEncoding.EncodeToString(seed)},
		{MachineID: strings.Repeat("AB", 32), Owner: "Denis", Comment: base64.RawURLEncoding.EncodeToString(seed)},
		{MachineID: strings.Repeat("AB", 32), Owner: "Denis", Comment: hex.EncodeToString(seed)},
		{MachineID: strings.Repeat("AB", 32), Owner: strings.ToUpper(hex.EncodeToString(repository.state.SeedKey)), Comment: "do not persist"},
		{MachineID: strings.Repeat("AB", 32), Owner: "Denis", Comment: hex.EncodeToString(repository.state.SeedKey)},
		{MachineID: strings.Repeat("AB", 32), Owner: "-----BEGIN PRIVATE KEY-----", Comment: "do not persist"},
	}

	for index, request := range tests {
		if _, err := service.Issue(request); !errors.Is(err, ErrInvalidIssueRequest) {
			t.Errorf("Issue(secret %d) error = %v, want ErrInvalidIssueRequest", index, err)
		}
	}
	if len(service.Status().History) != 0 || repository.saveCalls != 0 {
		t.Fatal("secret-bearing request reached issuance history")
	}
}

func TestIssueRejectsSecretHexAsMachineIDBeforeWritingHistory(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	service, err := newGeneratorService(repository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService() error = %v", err)
	}
	secretMachineIDs := map[string]string{
		"seed key":     strings.ToUpper(hex.EncodeToString(repository.state.SeedKey)),
		"private seed": strings.ToUpper(hex.EncodeToString(repository.state.PrivateKey.Seed())),
	}
	for name, machineID := range secretMachineIDs {
		t.Run(name, func(t *testing.T) {
			result, issueErr := service.Issue(IssueRequest{MachineID: machineID, Owner: "Denis"})
			if !errors.Is(issueErr, ErrInvalidIssueRequest) {
				t.Fatalf("Issue() error = %v, want ErrInvalidIssueRequest", issueErr)
			}
			if result.Token != "" || result.Payload.MachineID != "" {
				t.Fatalf("Issue() returned secret-bearing result = %#v", result)
			}
		})
	}
	if len(service.Status().History) != 0 || repository.saveCalls != 0 {
		t.Fatal("secret-bearing Machine ID reached issuance history")
	}
}

func TestBackupConfirmationRejectsDifferentKeyAfterIssuance(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	entry, err := licenseissuer.NewHistoryEntry(validPayload())
	if err != nil {
		t.Fatalf("NewHistoryEntry() error = %v", err)
	}
	repository.state.History = []licenseissuer.HistoryEntry{entry}
	service, err := newGeneratorService(repository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService() error = %v", err)
	}

	backup, err := service.CreateBackup("a long portable password")
	if err != nil {
		t.Fatalf("CreateBackup() error = %v", err)
	}
	if service.Status().BackupConfirmed {
		t.Fatal("CreateBackup() marked backup confirmed before successful export")
	}
	if err := service.ConfirmBackup(); err != nil {
		t.Fatalf("ConfirmBackup() error = %v", err)
	}
	if !service.Status().BackupConfirmed {
		t.Fatal("ConfirmBackup() did not persist confirmation")
	}

	_, replacementKey, err := licenseissuer.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	replacementBackup, err := licenseissuer.BackupPrivateKey(replacementKey, "replacement password")
	if err != nil {
		t.Fatalf("BackupPrivateKey() error = %v", err)
	}
	originalKey := append(ed25519.PrivateKey(nil), repository.state.PrivateKey...)
	if err := service.RestoreBackup(replacementBackup, "replacement password"); !errors.Is(err, ErrGeneratorKeyMismatch) {
		t.Fatalf("RestoreBackup() error = %v, want ErrGeneratorKeyMismatch", err)
	}
	if !bytes.Equal(repository.state.PrivateKey, originalKey) {
		t.Fatal("RestoreBackup() replaced a signing key after licenses were issued")
	}
	if len(service.Status().History) != 1 || service.Status().History[0] != entry {
		t.Fatal("RestoreBackup() discarded issuance history")
	}
	if backup == replacementBackup {
		t.Fatal("test backups unexpectedly match")
	}
}

func TestRestoreBackupAcceptsLegacyKeyOnlyBackupWithoutReplacingSeedGrant(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	repository.state.BackupConfirmed = true
	service, err := newGeneratorService(repository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService() error = %v", err)
	}
	_, replacementKey, err := licenseissuer.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	replacementBackup, err := licenseissuer.BackupPrivateKey(replacementKey, "replacement password")
	if err != nil {
		t.Fatalf("BackupPrivateKey() error = %v", err)
	}
	originalSeedID := repository.state.SeedID
	originalSeedKey := append([]byte(nil), repository.state.SeedKey...)

	if err := service.RestoreBackup(replacementBackup, "replacement password"); err != nil {
		t.Fatalf("RestoreBackup() error = %v", err)
	}
	if !bytes.Equal(repository.state.PrivateKey, replacementKey) {
		t.Fatal("RestoreBackup() did not replace an unused generated key")
	}
	if repository.state.SeedID != originalSeedID || !bytes.Equal(repository.state.SeedKey, originalSeedKey) {
		t.Fatal("RestoreBackup() replaced the local seed grant for a legacy key-only backup")
	}
	if service.Status().BackupConfirmed {
		t.Fatal("RestoreBackup() confirmed the current seed from a legacy key-only backup")
	}
}

func TestGeneratorServiceCloseWipesActivePrivateKey(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	service, err := newGeneratorService(repository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService() error = %v", err)
	}
	privateKey := service.state.PrivateKey
	service.Close()

	for index, value := range privateKey {
		if value != 0 {
			t.Fatalf("private key byte %d was not wiped", index)
		}
	}
	if service.state.PrivateKey != nil {
		t.Fatal("service retained the wiped private key slice")
	}
}

func TestGeneratorServiceCloseWipesOriginalKeyAfterStateChanges(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	service, err := newGeneratorService(repository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService() error = %v", err)
	}
	original := service.state.PrivateKey
	if _, err := service.Issue(IssueRequest{MachineID: strings.Repeat("AB", 32), Owner: "Denis"}); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if err := service.ConfirmBackup(); err != nil {
		t.Fatalf("ConfirmBackup() error = %v", err)
	}

	service.Close()

	for index, value := range original {
		if value != 0 {
			t.Fatalf("original private key byte %d was not wiped", index)
		}
	}
}

func TestRepositoryIgnoresPortableKeyBackups(t *testing.T) {
	contents, err := os.ReadFile("../../.gitignore")
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(contents), "*.tcompkeybackup") {
		t.Fatal(".gitignore does not exclude exported portable key backups")
	}
}

func repositoryWithGeneratedState(t *testing.T) *memoryStateRepository {
	t.Helper()
	publicKey, privateKey, err := licenseissuer.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	return &memoryStateRepository{state: generatorState{
		PrivateKey: privateKey,
		PublicKey:  publicKey,
		History:    []licenseissuer.HistoryEntry{},
		SeedID:     strings.Repeat("a", 32),
		SeedKey:    bytes.Repeat([]byte{0x17}, 32),
	}}
}

type memoryStateRepository struct {
	state     generatorState
	loadErr   error
	saveErr   error
	saveCalls int
}

func (repository *memoryStateRepository) Load() (generatorState, error) {
	if repository.loadErr != nil {
		return generatorState{}, repository.loadErr
	}
	return cloneGeneratorState(repository.state), nil
}

func (repository *memoryStateRepository) Save(state generatorState) error {
	repository.saveCalls++
	if repository.saveErr != nil {
		return repository.saveErr
	}
	repository.state = cloneGeneratorState(state)
	return nil
}

func cloneGeneratorState(state generatorState) generatorState {
	return generatorState{
		PrivateKey:      append(ed25519.PrivateKey(nil), state.PrivateKey...),
		PublicKey:       append(ed25519.PublicKey(nil), state.PublicKey...),
		BackupConfirmed: state.BackupConfirmed,
		History:         append([]licenseissuer.HistoryEntry(nil), state.History...),
		SeedID:          state.SeedID,
		SeedKey:         append([]byte(nil), state.SeedKey...),
	}
}

func fixedNow() time.Time {
	return time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
}

func deterministicRandom() io.Reader {
	return bytes.NewReader(bytes.Repeat([]byte{0x42}, 256))
}

func validPayload() license.Payload {
	return license.Payload{
		Schema:    1,
		LicenseID: "license-existing",
		Product:   "telegram-companion",
		Channel:   "public-macos-arm64",
		MachineID: strings.Repeat("AB", 32),
		Owner:     "Existing owner",
		IssuedAt:  "2026-07-20T12:00:00Z",
	}
}
