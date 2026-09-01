package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"telegram-companion/internal/license"
	"telegram-companion/internal/licenseissuer"
	"telegram-companion/internal/revocation"
)

func TestGeneratorServiceStatusExposesSafeLicenseRowsWithoutMachineIDs(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	payload := validPayload()
	payload.Comment = "private status comment"
	entry, err := licenseissuer.NewHistoryEntry(payload)
	if err != nil {
		t.Fatalf("NewHistoryEntry() error = %v", err)
	}
	repository.state.History = []licenseissuer.HistoryEntry{entry}
	revocationState := testGeneratorRevocationState(t)
	handle, err := revocation.DeriveHandle(payload.LicenseID)
	if err != nil {
		t.Fatalf("DeriveHandle() error = %v", err)
	}
	revocationState.Records = []revocationPublicationRecord{{
		EventID:     "event-status-row",
		Handle:      handle.String(),
		RequestedAt: fixedNow().Format("2006-01-02T15:04:05Z07:00"),
		State:       revocationPublicationPending,
	}}
	service, err := newGeneratorServiceWithRevocation(repository, &memoryRevocationStateStore{state: revocationState}, revocationBootstrapRequireRestore, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
	}
	defer service.Close()
	status := service.Status()
	if len(status.Licenses) != 1 || status.Licenses[0].LicenseID != payload.LicenseID || status.Licenses[0].Owner != payload.Owner || status.Licenses[0].RevocationState != licenseRevocationPending {
		t.Fatalf("Status().Licenses = %#v", status.Licenses)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("Marshal(Status()) error = %v", err)
	}
	if bytes.Contains(encoded, []byte(payload.MachineID)) || bytes.Contains(encoded, []byte(payload.Comment)) || bytes.Contains(encoded, []byte(`"history"`)) {
		t.Fatalf("Status JSON exposes unsafe issuance fields: %s", encoded)
	}
}

func TestGeneratorServiceImportsExpiredSchema2LicenseAsSafeIdempotentMetadata(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	service, err := newGeneratorServiceWithRevocation(repository, &memoryRevocationStateStore{state: testGeneratorRevocationState(t)}, revocationBootstrapRequireRestore, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
	}
	defer service.Close()
	payload := license.Payload{
		Schema:    2,
		LicenseID: "license-import-expired",
		Product:   licenseProduct,
		Channel:   licenseChannel,
		MachineID: strings.Repeat("CD", 32),
		Owner:     "Imported owner",
		Comment:   "private local note",
		IssuedAt:  "2025-01-01T00:00:00Z",
		ExpiresAt: "2025-12-31T00:00:00Z",
		SeedID:    repository.state.SeedID,
		SeedKey:   base64.RawURLEncoding.EncodeToString(repository.state.SeedKey),
	}
	token, err := licenseissuer.Issue(repository.state.PrivateKey, payload)
	if err != nil {
		t.Fatalf("Issue(import token) error = %v", err)
	}
	row, err := service.ImportLicense(token)
	if err != nil {
		t.Fatalf("ImportLicense() error = %v", err)
	}
	if row.LicenseID != payload.LicenseID || row.Owner != payload.Owner || row.RevocationState != licenseRevocationActive || row.ExpiresAt != payload.ExpiresAt {
		t.Fatalf("ImportLicense() row = %#v", row)
	}
	if repository.saveCalls != 1 || len(repository.state.History) != 1 || repository.state.History[0].Schema != 1 {
		t.Fatalf("persisted import = %#v, saves = %d", repository.state.History, repository.saveCalls)
	}
	statusJSON, err := json.Marshal(service.Status())
	if err != nil {
		t.Fatalf("Marshal(Status()) error = %v", err)
	}
	for _, forbidden := range []string{token, payload.SeedKey, payload.MachineID, payload.Comment} {
		if strings.Contains(string(statusJSON), forbidden) {
			t.Fatalf("Status() retained imported secret/private field %q", forbidden)
		}
	}
	if _, err := service.ImportLicense(token); err != nil {
		t.Fatalf("ImportLicense(duplicate) error = %v", err)
	}
	if repository.saveCalls != 1 || len(repository.state.History) != 1 {
		t.Fatal("duplicate import persisted a second row")
	}
}

func TestGeneratorServiceRejectsUntrustedRegistryImportsWithoutPersistence(t *testing.T) {
	validRepository := repositoryWithGeneratedState(t)
	otherRepository := repositoryWithGeneratedState(t)
	base := license.Payload{
		Schema:    1,
		LicenseID: "license-import-invalid",
		Product:   licenseProduct,
		Channel:   licenseChannel,
		MachineID: strings.Repeat("EF", 32),
		Owner:     "Invalid import",
		IssuedAt:  "2026-01-01T00:00:00Z",
	}
	wrongProduct := base
	wrongProduct.Product = "different-product"
	wrongChannel := base
	wrongChannel.Channel = "different-channel"
	tests := map[string]struct {
		payload license.Payload
		key     ed25519.PrivateKey
	}{
		"wrong signature": {payload: base, key: otherRepository.state.PrivateKey},
		"wrong product":   {payload: wrongProduct, key: validRepository.state.PrivateKey},
		"wrong channel":   {payload: wrongChannel, key: validRepository.state.PrivateKey},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			repository := &memoryStateRepository{state: cloneGeneratorState(validRepository.state)}
			service, err := newGeneratorServiceWithRevocation(repository, &memoryRevocationStateStore{state: testGeneratorRevocationState(t)}, revocationBootstrapRequireRestore, fixedNow, deterministicRandom())
			if err != nil {
				t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
			}
			defer service.Close()
			token, err := licenseissuer.Issue(test.key, test.payload)
			if err != nil {
				t.Fatalf("Issue(test token) error = %v", err)
			}
			if _, err := service.ImportLicense(token); !errors.Is(err, ErrInvalidLicenseImport) {
				t.Fatalf("ImportLicense() error = %v, want ErrInvalidLicenseImport", err)
			}
			if repository.saveCalls != 0 || len(repository.state.History) != 0 {
				t.Fatal("untrusted import mutated issuance history")
			}
		})
	}
}
