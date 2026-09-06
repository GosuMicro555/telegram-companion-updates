package main

import (
	"telegram-companion/internal/license"
	"telegram-companion/internal/licenseissuer"
	"telegram-companion/internal/revocation"
)

const (
	licenseRevocationActive    = "active"
	licenseRevocationPending   = "pending"
	licenseRevocationFailed    = "failed"
	licenseRevocationPublished = "revoked"
)

const ErrInvalidLicenseImport GeneratorError = "invalid license import"

type LicenseRow struct {
	LicenseID       string `json:"licenseID"`
	Owner           string `json:"owner"`
	IssuedAt        string `json:"issuedAt"`
	ExpiresAt       string `json:"expiresAt,omitempty"`
	RevocationState string `json:"revocationState"`
	RevokedAt       string `json:"revokedAt,omitempty"`
}

func (service *generatorService) ImportLicense(token string) (LicenseRow, error) {
	if service == nil {
		return LicenseRow{}, ErrInvalidLicenseImport
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	payload, err := license.ParseAndVerifyForRegistry(token, license.RegistryVerifyOptions{
		PublicKey: service.state.PublicKey,
		Product:   licenseProduct,
		Channel:   licenseChannel,
	})
	if err != nil {
		return LicenseRow{}, ErrInvalidLicenseImport
	}
	entry, err := licenseissuer.NewHistoryEntry(payload)
	payload = license.Payload{}
	if err != nil {
		return LicenseRow{}, ErrInvalidLicenseImport
	}
	// Issuance history intentionally excludes the schema-2 seed grant.
	entry.Schema = 1
	for _, existing := range service.state.History {
		if existing.LicenseID != entry.LicenseID {
			continue
		}
		if existing != entry {
			return LicenseRow{}, ErrInvalidLicenseImport
		}
		return service.licenseRowForEntryLocked(existing), nil
	}
	next := service.state
	next.History = append(append([]licenseissuer.HistoryEntry(nil), service.state.History...), entry)
	if err := service.repository.Save(next); err != nil {
		return LicenseRow{}, ErrGeneratorStorage
	}
	service.state.History = next.History
	return service.licenseRowForEntryLocked(entry), nil
}

func (service *generatorService) licenseRowsLocked() []LicenseRow {
	rows := make([]LicenseRow, 0, len(service.state.History))
	for _, entry := range service.state.History {
		rows = append(rows, service.licenseRowForEntryLocked(entry))
	}
	return rows
}

func (service *generatorService) licenseRowForEntryLocked(entry licenseissuer.HistoryEntry) LicenseRow {
	row := LicenseRow{
		LicenseID:       entry.LicenseID,
		Owner:           entry.Owner,
		IssuedAt:        entry.IssuedAt,
		ExpiresAt:       entry.ExpiresAt,
		RevocationState: licenseRevocationActive,
	}
	handle, err := revocation.DeriveHandle(entry.LicenseID)
	if err != nil {
		return row
	}
	for _, record := range service.revocationState.Records {
		if record.Handle != handle.String() {
			continue
		}
		switch record.State {
		case revocationPublicationPending:
			row.RevocationState = licenseRevocationPending
		case revocationPublicationFailed:
			row.RevocationState = licenseRevocationFailed
		case revocationPublicationPublished:
			row.RevocationState = licenseRevocationPublished
			row.RevokedAt = record.RequestedAt
		}
		break
	}
	return row
}
