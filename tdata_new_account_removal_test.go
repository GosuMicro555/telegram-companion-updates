package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyTDataTransferRuntimeSurfaceIsRemoved(t *testing.T) {
	forbiddenPaths := []string{
		filepath.Join("frontend", "src", "Master"+"TDataInbox.tsx"),
		filepath.Join("frontend", "src", "Master"+"TDataInbox.test.tsx"),
		filepath.Join("internal", "tdata"+"relay"),
		filepath.Join("deploy", "cloudflare", "tdata-"+"relay"),
		"desktop_master_" + "inbox_internal.go",
		"desktop_master_" + "inbox_internal_test.go",
		"desktop_master_" + "inbox_public.go",
		filepath.Join("internal", "transport", "wails", "tdata_import_master_"+"bindings.go"),
		filepath.Join("internal", "transport", "wails", "tdata_import_master_"+"bindings_test.go"),
		filepath.Join("frontend", "src", "TData"+"ImportModal.tsx"),
		filepath.Join("frontend", "src", "TData"+"ImportModal.test.tsx"),
		filepath.Join("frontend", "src", "tdata"+"Import.ts"),
		filepath.Join("frontend", "src", "tdata"+"Import.test.ts"),
		filepath.Join("internal", "tdata"+"import"),
		filepath.Join("internal", "repository", "sqlite", "tdata_import_"+"store.go"),
		filepath.Join("internal", "repository", "sqlite", "tdata_import_"+"store_test.go"),
		filepath.Join("internal", "transport", "wails", "tdata_import_"+"bindings.go"),
		filepath.Join("internal", "transport", "wails", "tdata_import_"+"bindings_test.go"),
		filepath.Join("internal", "transport", "wails", "tdata_import_"+"surface_contract_test.go"),
		filepath.Join("internal", "telegram", "gotd", "session_"+"importer.go"),
		filepath.Join("internal", "telegram", "gotd", "session_"+"importer_test.go"),
		filepath.Join("scripts", "provision_master_"+"inbox.sh"),
	}
	for _, path := range forbiddenPaths {
		if _, err := os.Lstat(path); err == nil {
			t.Errorf("removed tdata new-account path still exists: %s", path)
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect %s: %v", path, err)
		}
	}

	forbiddenTokens := removedTDataTransferTokens()

	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			slashPath := filepath.ToSlash(path)
			base := entry.Name()
			if base == ".git" || base == "node_modules" || base == "artifacts" || base == "build" ||
				slashPath == "docs" || strings.HasPrefix(slashPath, "docs/") ||
				slashPath == "work" || strings.HasPrefix(slashPath, "work/") {
				return filepath.SkipDir
			}
			return nil
		}
		if path == "tdata_new_account_removal_test.go" {
			return nil
		}
		tokens := forbiddenTokens
		if filepath.ToSlash(path) == "scripts/release/macos-arm64/public_bootstrap_bundle_contract_test.go" {
			tokens = removeExactArtifactDenyTestEvidenceToken(tokens)
		}
		if !isTDataRemovalContractSource(path) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > 16<<20 {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, token := range tokens {
			if bytes.Contains(content, token) {
				t.Errorf("removed tdata new-account token %q remains in %s", token, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func removeExactArtifactDenyTestEvidenceToken(tokens [][]byte) [][]byte {
	result := make([][]byte, 0, len(tokens))
	for _, token := range tokens {
		if bytes.Equal(token, []byte("tdata-"+"import")) {
			continue
		}
		result = append(result, token)
	}
	return result
}

func TestTDataNewAccountDocumentationIsLimitedToTheReleaseAmendment(t *testing.T) {
	// The release source snapshot intentionally contains only the build tree;
	// documentation is audited in the outer repository.  Treat an absent docs
	// tree as an empty set, while still failing closed for any other filesystem
	// error or for forbidden material when docs are present.
	if _, err := os.Stat("docs"); err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("inspect docs: %v", err)
	}
	allowed := filepath.ToSlash(filepath.Join(
		"docs", "superpowers", "specs", "2026-08-25-license-revocation-083-amendment.md",
	))
	err := filepath.WalkDir("docs", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.ToSlash(path) == allowed || strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > 16<<20 {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, token := range removedTDataTransferTokens() {
			if bytes.Contains(content, token) {
				t.Errorf("removed capability documentation token %q remains outside the release amendment in %s", token, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func removedTDataTransferTokens() [][]byte {
	return [][]byte{
		[]byte("Master" + "TDataInbox"),
		[]byte("Get" + "MasterInboxStatus"),
		[]byte("List" + "MasterInbox"),
		[]byte("Ack" + "MasterInbox"),
		[]byte("MASTER_TDATA_" + "INBOX_ENABLED"),
		[]byte("RELAY_" + "BASE_URL"),
		[]byte("MASTER_" + "INBOX_ID"),
		[]byte("MASTER_" + "PUBLIC_KEY"),
		[]byte("tdata" + "relay"),
		[]byte("Relay" + "Status"),
		[]byte("Claim" + "Relay"),
		[]byte("Update" + "Relay"),
		[]byte("Relay" + "BaseURL"),
		[]byte("Relay" + "InboxID"),
		[]byte("Relay" + "PublicKey"),
		[]byte("relay" + "BaseURL"),
		[]byte("master" + "InboxID"),
		[]byte("master" + "PublicKey"),
		[]byte("tdata new " + "accounts"),
		[]byte("master-inbox-" + "private-key-v1"),
		[]byte("master-inbox-" + "admin-token-v1"),
		[]byte("SubmitTData" + "DriveLinks"),
		[]byte("ListTData" + "ImportItems"),
		[]byte("TData" + "ImportModal"),
		[]byte("ConfigureTData" + "Import"),
		[]byte("NewSession" + "Importer"),
		[]byte("NewTData" + "ImportStore"),
		[]byte("tdata-" + "import"),
	}
}

func isTDataRemovalContractSource(path string) bool {
	text := filepath.ToSlash(path)
	if strings.HasPrefix(text, ".github/") || text == "Makefile" {
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".ts", ".tsx", ".js", ".css", ".sh", ".sql", ".json", ".toml":
		return true
	default:
		return false
	}
}
