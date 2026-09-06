package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"telegram-companion/internal/bootstrapstate"
	"telegram-companion/internal/license"
)

func TestIssuedMachineBoundSeedGrantUnlocksCompatibleBootstrapState(t *testing.T) {
	const appVersion = "0.8.2"
	ctx := context.Background()
	repository := repositoryWithGeneratedState(t)
	service, err := newGeneratorService(repository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService() error = %v", err)
	}

	firstMachineID := strings.Repeat("AB", 32)
	first, err := service.Issue(IssueRequest{MachineID: firstMachineID, Owner: "First Mac"})
	if err != nil {
		t.Fatalf("Issue(first) error = %v", err)
	}
	second, err := service.Issue(IssueRequest{MachineID: strings.Repeat("CD", 32), Owner: "Second Mac"})
	if err != nil {
		t.Fatalf("Issue(second) error = %v", err)
	}

	verify := func(token, machineID string) license.Payload {
		t.Helper()
		payload, verifyErr := license.ParseAndVerify(token, license.VerifyOptions{
			PublicKey: repository.state.PublicKey,
			Product:   licenseProduct,
			Channel:   licenseChannel,
			MachineID: machineID,
			Now:       fixedNow(),
		})
		if verifyErr != nil {
			t.Fatalf("ParseAndVerify() error = %v", verifyErr)
		}
		return payload
	}
	firstPayload := verify(first.Token, firstMachineID)
	secondPayload := verify(second.Token, strings.Repeat("CD", 32))
	if _, err := license.ParseAndVerify(first.Token, license.VerifyOptions{
		PublicKey: repository.state.PublicKey,
		Product:   licenseProduct,
		Channel:   licenseChannel,
		MachineID: strings.Repeat("EF", 32),
		Now:       fixedNow(),
	}); !errors.Is(err, license.ErrWrongMachine) {
		t.Fatalf("ParseAndVerify(wrong machine) error = %v, want ErrWrongMachine", err)
	}

	firstGrant, err := firstPayload.SeedGrant()
	if err != nil {
		t.Fatalf("first SeedGrant() error = %v", err)
	}
	secondGrant, err := secondPayload.SeedGrant()
	if err != nil {
		t.Fatalf("second SeedGrant() error = %v", err)
	}
	if firstPayload.Schema != 2 || secondPayload.Schema != 2 || firstGrant.ID != secondGrant.ID || !bytes.Equal(firstGrant.Key, secondGrant.Key) {
		t.Fatalf("issued payloads do not share a schema-2 seed grant: %#v, %#v", firstPayload, secondPayload)
	}

	sourceData := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(sourceData, 0o700); err != nil {
		t.Fatalf("MkdirAll(source data) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceData, "app.db"), []byte("seeded database"), 0o600); err != nil {
		t.Fatalf("WriteFile(app.db) error = %v", err)
	}
	bundlePath := filepath.Join(t.TempDir(), bootstrapstate.BundleFile)
	packedSecrets := &bootstrapStateContractSecrets{values: map[string][]byte{
		"telegram-account-credentials-v1": []byte("seeded account"),
	}}
	if err := bootstrapstate.Pack(ctx, bootstrapstate.PackConfig{
		SourceData: sourceData,
		OutputPath: bundlePath,
		BundleID:   firstGrant.ID,
		AppVersion: appVersion,
		Key:        firstGrant.Key,
		Secrets:    packedSecrets,
	}); err != nil {
		t.Fatalf("bootstrapstate.Pack() error = %v", err)
	}

	importedSecrets := &bootstrapStateContractSecrets{}
	targetRoot := t.TempDir()
	imported, err := bootstrapstate.Import(ctx, bootstrapstate.ImportConfig{
		BundlePath: bundlePath,
		TargetRoot: targetRoot,
		BundleID:   secondGrant.ID,
		AppVersion: appVersion,
		Key:        secondGrant.Key,
		Secrets:    importedSecrets,
	})
	if err != nil || !imported {
		t.Fatalf("bootstrapstate.Import() = (%t, %v), want (true, nil)", imported, err)
	}
	if database, err := os.ReadFile(filepath.Join(targetRoot, "data", "app.db")); err != nil || string(database) != "seeded database" {
		t.Fatalf("imported app.db = %q, %v", database, err)
	}
	if got := importedSecrets.values["telegram-account-credentials-v1"]; !bytes.Equal(got, []byte("seeded account")) {
		t.Fatalf("imported secret = %q, want seeded account", got)
	}

	wrongGrant := firstGrant
	wrongGrant.Key = append([]byte(nil), firstGrant.Key...)
	wrongGrant.Key[0] ^= 0xff
	if _, err := bootstrapstate.Import(ctx, bootstrapstate.ImportConfig{
		BundlePath: bundlePath,
		TargetRoot: t.TempDir(),
		BundleID:   wrongGrant.ID,
		AppVersion: appVersion,
		Key:        wrongGrant.Key,
		Secrets:    &bootstrapStateContractSecrets{},
	}); err == nil {
		t.Fatal("bootstrapstate.Import() with wrong seed grant succeeded")
	}
	if _, err := bootstrapstate.Import(ctx, bootstrapstate.ImportConfig{
		BundlePath: bundlePath,
		TargetRoot: t.TempDir(),
		BundleID:   firstGrant.ID,
		AppVersion: "0.8.3",
		Key:        firstGrant.Key,
		Secrets:    &bootstrapStateContractSecrets{},
	}); !errors.Is(err, bootstrapstate.ErrVersionMismatch) {
		t.Fatalf("bootstrapstate.Import() with wrong version error = %v, want ErrVersionMismatch", err)
	}
}

type bootstrapStateContractSecrets struct {
	values map[string][]byte
}

func (s *bootstrapStateContractSecrets) Get(_ context.Context, name string) ([]byte, error) {
	value, ok := s.values[name]
	if !ok {
		return nil, bootstrapstate.ErrSecretNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *bootstrapStateContractSecrets) Set(_ context.Context, name string, value []byte) error {
	if s.values == nil {
		s.values = make(map[string][]byte)
	}
	s.values[name] = append([]byte(nil), value...)
	return nil
}
