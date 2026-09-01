package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
	"unicode"

	"telegram-companion/internal/license"
	"telegram-companion/internal/licenseissuer"
)

const (
	licenseProduct = "telegram-companion"
	licenseChannel = "public-macos-arm64"
)

type GeneratorError string

func (e GeneratorError) Error() string { return "license generator: " + string(e) }

func (e GeneratorError) Is(target error) bool {
	other, ok := target.(GeneratorError)
	return ok && e == other
}

const (
	ErrGeneratorInitialization GeneratorError = "initialization failed"
	ErrGeneratorStorage        GeneratorError = "state storage failed"
	ErrInvalidIssueRequest     GeneratorError = "invalid issue request"
	ErrGeneratorKeyMismatch    GeneratorError = "backup key does not match issued licenses"
)

type IssueRequest struct {
	MachineID string `json:"machineID"`
	Owner     string `json:"owner"`
	Comment   string `json:"comment"`
	ExpiresAt string `json:"expiresAt"`
}

type IssueResult struct {
	Token   string        `json:"token"`
	Payload IssuedPayload `json:"payload"`
}

// IssuedPayload is the non-secret license metadata returned to the Wails UI.
type IssuedPayload struct {
	Schema    int    `json:"schema"`
	LicenseID string `json:"license_id"`
	Product   string `json:"product"`
	Channel   string `json:"channel"`
	MachineID string `json:"machine_id"`
	Owner     string `json:"owner"`
	Comment   string `json:"comment,omitempty"`
	IssuedAt  string `json:"issued_at"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type GeneratorStatus struct {
	LicensePublicKey               string                       `json:"licensePublicKey"`
	PublicKeySPKI                  string                       `json:"publicKeySPKI"`
	RevocationPublicKey            string                       `json:"revocationPublicKey,omitempty"`
	SeedID                         string                       `json:"seedID"`
	BackupConfirmed                bool                         `json:"backupConfirmed"`
	RevocationBackupConfirmed      bool                         `json:"revocationBackupConfirmed"`
	RevocationRestoreRequired      bool                         `json:"revocationRestoreRequired"`
	RevocationPublicationCode      string                       `json:"revocationPublicationCode"`
	RevocationCredentialConfigured bool                         `json:"revocationCredentialConfigured"`
	Licenses                       []LicenseRow                 `json:"licenses"`
	History                        []licenseissuer.HistoryEntry `json:"-"`
}

type stateRepository interface {
	Load() (generatorState, error)
	Save(generatorState) error
}

type generatorService struct {
	mu                             sync.Mutex
	repository                     stateRepository
	revocationRepository           revocationStateStore
	now                            func() time.Time
	random                         io.Reader
	state                          generatorState
	revocationState                generatorRevocationState
	revocationRestore              bool
	revocationPublicationCode      string
	revocationCredentials          *revocationCredentials
	revocationFactory              generatorRevocationPublisherFactory
	revocationPublisher            generatorRevocationPublisher
	revocationCredentialConfigured bool
}

func newGeneratorService(repository stateRepository, now func() time.Time, random io.Reader) (*generatorService, error) {
	if repository == nil || now == nil || random == nil {
		return nil, ErrGeneratorInitialization
	}
	state, err := repository.Load()
	if errors.Is(err, licenseissuer.ErrStateNotFound) {
		publicKey, privateKey, seedID, seedKey, keyErr := generateInitialState(random)
		if keyErr != nil {
			return nil, ErrGeneratorInitialization
		}
		state = generatorState{
			PrivateKey: privateKey,
			PublicKey:  publicKey,
			History:    []licenseissuer.HistoryEntry{},
			SeedID:     seedID,
			SeedKey:    seedKey,
		}
		if err := repository.Save(state); err != nil {
			return nil, ErrGeneratorStorage
		}
	} else if err != nil {
		return nil, ErrGeneratorStorage
	}
	if !validKeyPair(state.PrivateKey, state.PublicKey) {
		return nil, ErrGeneratorInitialization
	}
	if state.SeedID == "" && len(state.SeedKey) == 0 {
		seedID, seedKey, seedErr := newSeedGrant(random)
		if seedErr != nil {
			return nil, ErrGeneratorInitialization
		}
		state.SeedID = seedID
		state.SeedKey = seedKey
		state.BackupConfirmed = false
		if err := repository.Save(state); err != nil {
			return nil, ErrGeneratorStorage
		}
	} else if !validSeedGrant(state.SeedID, state.SeedKey) {
		return nil, ErrGeneratorInitialization
	}
	return &generatorService{
		repository: repository,
		now:        now,
		random:     random,
		state:      state,
	}, nil
}

func (s *generatorService) Status() GeneratorStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	publicKeyDER, _ := x509.MarshalPKIXPublicKey(s.state.PublicKey)
	backupConfirmed := s.state.BackupConfirmed
	revocationPublicKey := ""
	revocationBackupConfirmed := false
	if s.revocationRepository != nil {
		revocationBackupConfirmed = validGeneratorRevocationState(s.revocationState) && s.revocationState.BackupConfirmed
		backupConfirmed = backupConfirmed && revocationBackupConfirmed
		if validGeneratorRevocationState(s.revocationState) {
			revocationPublicKey = base64.StdEncoding.EncodeToString(s.revocationState.PublicKey)
		}
	}
	revocationPublicationCode := s.revocationPublicationCode
	if revocationPublicationCode == "" {
		if s.revocationRestore {
			revocationPublicationCode = revocationPublicationRestoreRequired
		} else {
			revocationPublicationCode = revocationPublicationCredentialRequired
		}
	}
	return GeneratorStatus{
		LicensePublicKey:               base64.StdEncoding.EncodeToString(s.state.PublicKey),
		PublicKeySPKI:                  base64.RawStdEncoding.EncodeToString(publicKeyDER),
		RevocationPublicKey:            revocationPublicKey,
		SeedID:                         s.state.SeedID,
		BackupConfirmed:                backupConfirmed,
		RevocationBackupConfirmed:      revocationBackupConfirmed,
		RevocationRestoreRequired:      s.revocationRestore,
		RevocationPublicationCode:      revocationPublicationCode,
		RevocationCredentialConfigured: s.revocationCredentialConfigured,
		Licenses:                       s.licenseRowsLocked(),
		History:                        append([]licenseissuer.HistoryEntry(nil), s.state.History...),
	}
}

func (s *generatorService) Issue(request IssueRequest) (IssueResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	issuedAt := s.now().UTC().Truncate(time.Second)
	machineID, owner, comment, expiresAt, err := normalizeIssueRequest(request, issuedAt)
	if err != nil {
		return IssueResult{}, err
	}
	if containsSecretMaterial(machineID+"\n"+owner+"\n"+comment, s.state.PrivateKey, s.state.SeedKey, s.revocationState.PrivateKey) {
		return IssueResult{}, ErrInvalidIssueRequest
	}
	identifierBytes := make([]byte, 16)
	if _, err := io.ReadFull(s.random, identifierBytes); err != nil {
		return IssueResult{}, ErrGeneratorInitialization
	}
	payload := license.Payload{
		Schema:    2,
		LicenseID: "license-" + hex.EncodeToString(identifierBytes),
		Product:   licenseProduct,
		Channel:   licenseChannel,
		MachineID: machineID,
		Owner:     owner,
		Comment:   comment,
		IssuedAt:  issuedAt.Format(time.RFC3339),
		ExpiresAt: expiresAt,
		SeedID:    s.state.SeedID,
		SeedKey:   base64.RawURLEncoding.EncodeToString(s.state.SeedKey),
	}
	token, err := licenseissuer.Issue(s.state.PrivateKey, payload)
	if err != nil {
		return IssueResult{}, ErrGeneratorInitialization
	}
	entry, err := licenseissuer.NewHistoryEntry(payload)
	if err != nil {
		return IssueResult{}, ErrGeneratorInitialization
	}
	// History intentionally excludes the seed grant, so it uses the compatible schema.
	entry.Schema = 1
	next := s.state
	next.History = append(append([]licenseissuer.HistoryEntry(nil), s.state.History...), entry)
	if err := s.repository.Save(next); err != nil {
		return IssueResult{}, ErrGeneratorStorage
	}
	s.state = next
	return IssueResult{Token: token, Payload: issuedPayload(payload)}, nil
}

func (s *generatorService) CreateBackup(password string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revocationRepository != nil {
		if s.revocationRestore || !validGeneratorRevocationState(s.revocationState) {
			return "", ErrGeneratorInitialization
		}
		return createGeneratorBackupV3(generatorBackupMaterial{
			LicensePrivateKey:    s.state.PrivateKey,
			SeedID:               s.state.SeedID,
			SeedKey:              s.state.SeedKey,
			RevocationPrivateKey: s.revocationState.PrivateKey,
		}, password)
	}
	return createGeneratorBackup(s.state.PrivateKey, s.state.SeedID, s.state.SeedKey, password)
}

func (s *generatorService) ValidateBackup(backup, password string) error {
	if s == nil || !strings.HasPrefix(backup, generatorBackupV3Format+".") {
		return ErrGeneratorInitialization
	}
	material, err := restoreGeneratorBackupMaterial(backup, password)
	if err != nil {
		return ErrGeneratorInitialization
	}
	defer wipeGeneratorBackupMaterial(&material)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revocationRepository == nil || s.revocationRestore ||
		!bytes.Equal(material.LicensePrivateKey, s.state.PrivateKey) ||
		material.SeedID != s.state.SeedID || !bytes.Equal(material.SeedKey, s.state.SeedKey) ||
		!bytes.Equal(material.RevocationPrivateKey, s.revocationState.PrivateKey) {
		return ErrGeneratorInitialization
	}
	return nil
}

// CreateBootstrapSeedExport returns a password-encrypted, seed-only operator artifact.
func (s *generatorService) CreateBootstrapSeedExport(password string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return createBootstrapSeedExport(s.state.SeedID, s.state.SeedKey, password)
}

func (s *generatorService) ConfirmBackup() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revocationRepository != nil {
		if s.revocationRestore || !validGeneratorRevocationState(s.revocationState) {
			return ErrGeneratorInitialization
		}
		if err := s.refreshRevocationStateLocked(); err != nil {
			return ErrGeneratorStorage
		}
		if s.state.BackupConfirmed && s.revocationState.BackupConfirmed {
			return nil
		}
		previousSigning := s.state
		nextSigning := s.state
		nextSigning.BackupConfirmed = true
		signingSaved := false
		if !s.state.BackupConfirmed {
			if err := s.repository.Save(nextSigning); err != nil {
				return ErrGeneratorStorage
			}
			signingSaved = true
		}
		nextRevocation := s.revocationState
		nextRevocation.BackupConfirmed = true
		if !s.revocationState.BackupConfirmed {
			if err := s.revocationRepository.Save(nextRevocation); err != nil {
				if signingSaved {
					_ = s.repository.Save(previousSigning)
				}
				return ErrGeneratorStorage
			}
		}
		s.state.BackupConfirmed = true
		s.revocationState.BackupConfirmed = true
		return nil
	}
	if s.state.BackupConfirmed {
		return nil
	}
	next := s.state
	next.BackupConfirmed = true
	if err := s.repository.Save(next); err != nil {
		return ErrGeneratorStorage
	}
	s.state = next
	return nil
}

func (s *generatorService) RestoreBackup(backup, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	material, err := restoreGeneratorBackupMaterial(backup, password)
	if err != nil {
		return err
	}
	adopted := false
	defer func() {
		if !adopted {
			wipeGeneratorBackupMaterial(&material)
		}
	}()
	if s.revocationRepository != nil && !s.revocationRestore && validGeneratorRevocationState(s.revocationState) {
		if err := s.refreshRevocationStateLocked(); err != nil {
			return ErrGeneratorStorage
		}
	}
	publicKey, ok := material.LicensePrivateKey.Public().(ed25519.PublicKey)
	if !ok {
		return ErrGeneratorInitialization
	}
	if len(s.state.History) > 0 && !bytes.Equal(s.state.PublicKey, publicKey) {
		return ErrGeneratorKeyMismatch
	}
	hasRevocationKey := len(material.RevocationPrivateKey) == ed25519.PrivateKeySize
	if hasRevocationKey && s.revocationRepository == nil {
		return ErrGeneratorInitialization
	}
	next := s.state
	next.PrivateKey = material.LicensePrivateKey
	next.PublicKey = append(ed25519.PublicKey(nil), publicKey...)
	next.BackupConfirmed = material.SeedID != ""
	if material.SeedID != "" {
		next.SeedID = material.SeedID
		next.SeedKey = material.SeedKey
	}

	nextRevocation := s.revocationState
	saveRevocation := false
	if s.revocationRepository != nil {
		if hasRevocationKey {
			revocationPublicKey, publicOK := material.RevocationPrivateKey.Public().(ed25519.PublicKey)
			if !publicOK {
				return ErrGeneratorInitialization
			}
			if validGeneratorRevocationState(s.revocationState) && !bytes.Equal(s.revocationState.PublicKey, revocationPublicKey) {
				return ErrGeneratorKeyMismatch
			}
			if s.revocationFactory == nil {
				return ErrGeneratorRevocationUnavailable
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*generatorGitHubTimeout)
			evidence, inspectErr := s.revocationFactory.InspectRemote(ctx, revocationPublicKey)
			cancel()
			if inspectErr != nil {
				return ErrGeneratorRevocationUnavailable
			}
			nextRevocation, err = reconcileRevocationRestoreState(material.RevocationPrivateKey, revocationPublicKey, s.revocationState, evidence)
			if err != nil {
				return err
			}
			saveRevocation = true
		} else if validGeneratorRevocationState(s.revocationState) && s.revocationState.BackupConfirmed {
			nextRevocation.BackupConfirmed = false
			saveRevocation = true
		}
	}
	if err := s.repository.Save(next); err != nil {
		return ErrGeneratorStorage
	}
	if saveRevocation {
		if err := s.revocationRepository.Save(nextRevocation); err != nil {
			_ = s.repository.Save(s.state)
			return ErrGeneratorStorage
		}
	}
	wipeBytes(s.state.PrivateKey)
	if material.SeedID != "" {
		wipeBytes(s.state.SeedKey)
	}
	if hasRevocationKey && validGeneratorRevocationState(s.revocationState) {
		wipeBytes(s.revocationState.PrivateKey)
	}
	s.state = next
	if saveRevocation {
		s.revocationState = nextRevocation
	}
	if hasRevocationKey {
		s.revocationRestore = false
		if s.revocationPublisher != nil && s.revocationCredentialConfigured {
			s.revocationPublicationCode = revocationPublicationReady
		} else if s.revocationCredentialConfigured {
			s.revocationPublicationCode = revocationPublicationRestartRequired
		} else {
			s.revocationPublicationCode = revocationPublicationCredentialRequired
		}
	}
	adopted = true
	return nil
}

func (s *generatorService) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	publisher := s.revocationPublisher
	s.revocationPublisher = nil
	wipeBytes(s.state.PrivateKey)
	wipeBytes(s.state.SeedKey)
	wipeBytes(s.revocationState.PrivateKey)
	s.state.PrivateKey = nil
	s.state.SeedKey = nil
	s.revocationState.PrivateKey = nil
	s.mu.Unlock()
	if publisher != nil {
		publisher.Close()
	}
}

func generateInitialState(random io.Reader) (ed25519.PublicKey, ed25519.PrivateKey, string, []byte, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(random)
	if err != nil {
		return nil, nil, "", nil, err
	}
	seedID, seedKey, err := newSeedGrant(random)
	if err != nil {
		wipeBytes(privateKey)
		return nil, nil, "", nil, err
	}
	return publicKey, privateKey, seedID, seedKey, nil
}

func issuedPayload(payload license.Payload) IssuedPayload {
	return IssuedPayload{
		Schema:    payload.Schema,
		LicenseID: payload.LicenseID,
		Product:   payload.Product,
		Channel:   payload.Channel,
		MachineID: payload.MachineID,
		Owner:     payload.Owner,
		Comment:   payload.Comment,
		IssuedAt:  payload.IssuedAt,
		ExpiresAt: payload.ExpiresAt,
	}
}

func normalizeIssueRequest(request IssueRequest, now time.Time) (machineID, owner, comment, expiresAt string, err error) {
	machineID = strings.ToUpper(strings.TrimSpace(request.MachineID))
	owner = strings.TrimSpace(request.Owner)
	comment = strings.TrimSpace(request.Comment)
	if len(machineID) != 64 || !isUpperHex(machineID) || owner == "" || len(owner) > 200 || len(comment) > 2000 {
		return "", "", "", "", ErrInvalidIssueRequest
	}
	if strings.IndexFunc(owner+comment, unicode.IsControl) >= 0 {
		return "", "", "", "", ErrInvalidIssueRequest
	}
	if strings.TrimSpace(request.ExpiresAt) != "" {
		expiry, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(request.ExpiresAt))
		if parseErr != nil || !expiry.After(now) {
			return "", "", "", "", ErrInvalidIssueRequest
		}
		expiresAt = expiry.UTC().Format(time.RFC3339)
	}
	return machineID, owner, comment, expiresAt, nil
}

func isUpperHex(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'A' || character > 'F') {
			return false
		}
	}
	return true
}

func containsSecretMaterial(value string, privateKey ed25519.PrivateKey, seedKey []byte, additionalPrivateKeys ...ed25519.PrivateKey) bool {
	upper := strings.ToUpper(value)
	for _, marker := range []string{"TCPLIC1.", "TCPKEYBACKUP1.", generatorBackupFormat + ".", generatorBackupV3Format + ".", bootstrapSeedExportFormat + ".", "PRIVATE KEY-----"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	materials := make([][]byte, 0, 2+len(additionalPrivateKeys)*2)
	if len(seedKey) > 0 {
		materials = append(materials, seedKey)
	}
	privateKeys := append([]ed25519.PrivateKey{privateKey}, additionalPrivateKeys...)
	for _, candidate := range privateKeys {
		if len(candidate) != ed25519.PrivateKeySize {
			continue
		}
		seed := candidate.Seed()
		defer wipeBytes(seed)
		materials = append(materials, candidate, seed)
	}
	for _, material := range materials {
		for _, encoded := range []string{
			base64.StdEncoding.EncodeToString(material),
			base64.RawStdEncoding.EncodeToString(material),
			base64.URLEncoding.EncodeToString(material),
			base64.RawURLEncoding.EncodeToString(material),
			hex.EncodeToString(material),
			strings.ToUpper(hex.EncodeToString(material)),
		} {
			if strings.Contains(value, encoded) {
				return true
			}
		}
	}
	return false
}

func validKeyPair(privateKey ed25519.PrivateKey, publicKey ed25519.PublicKey) bool {
	if len(privateKey) != ed25519.PrivateKeySize || len(publicKey) != ed25519.PublicKeySize {
		return false
	}
	derived, ok := privateKey.Public().(ed25519.PublicKey)
	return ok && bytes.Equal(derived, publicKey)
}

func wipeBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
