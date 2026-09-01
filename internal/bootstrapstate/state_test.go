package bootstrapstate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type memorySecrets struct {
	values map[string][]byte
	err    error
}

func (s *memorySecrets) Get(_ context.Context, name string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	value, ok := s.values[name]
	if !ok {
		return nil, ErrSecretNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *memorySecrets) Set(_ context.Context, name string, value []byte) error {
	if s.err != nil {
		return s.err
	}
	if s.values == nil {
		s.values = make(map[string][]byte)
	}
	s.values[name] = append([]byte(nil), value...)
	return nil
}

func TestImportAbsentBundleIsNoOp(t *testing.T) {
	imported, err := Import(context.Background(), ImportConfig{
		BundlePath: filepath.Join(t.TempDir(), "missing.tcs"),
		TargetRoot: t.TempDir(),
		AppVersion: "0.8.2",
		License:    "license-token",
		Secrets:    &memorySecrets{},
	})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if imported {
		t.Fatal("Import() imported a missing bundle")
	}
}

func TestPackAndImportRoundTripReplacesDataAndPreservesLicense(t *testing.T) {
	ctx := context.Background()
	sourceData := filepath.Join(t.TempDir(), "data")
	mustWriteFile(t, filepath.Join(sourceData, "app.db"), []byte("database"))
	mustWriteFile(t, filepath.Join(sourceData, "tdata", "account", "map0"), []byte("telegram-session"))

	packedSecrets := &memorySecrets{values: map[string][]byte{
		"scout-message-key": []byte("01234567890123456789012345678901"),
	}}
	bundle := filepath.Join(t.TempDir(), "state.tcs")
	if err := Pack(ctx, PackConfig{
		SourceData: sourceData,
		OutputPath: bundle,
		BundleID:   "master-20260810",
		AppVersion: "0.8.2",
		License:    "license-token",
		Secrets:    packedSecrets,
	}); err != nil {
		t.Fatalf("Pack() error = %v", err)
	}

	targetRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(targetRoot, "license", "license.tcomplicense"), []byte("target-license"))
	mustWriteFile(t, filepath.Join(targetRoot, "data", "app.db"), []byte("empty-public-database"))
	importedSecrets := &memorySecrets{}
	imported, err := Import(ctx, ImportConfig{
		BundlePath: bundle,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "license-token",
		Secrets:    importedSecrets,
	})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if !imported {
		t.Fatal("Import() = false, want true")
	}
	assertFileContent(t, filepath.Join(targetRoot, "data", "app.db"), "database")
	assertFileContent(t, filepath.Join(targetRoot, "data", "tdata", "account", "map0"), "telegram-session")
	assertFileContent(t, filepath.Join(targetRoot, "license", "license.tcomplicense"), "target-license")
	if got := string(importedSecrets.values["scout-message-key"]); got != "01234567890123456789012345678901" {
		t.Fatalf("imported secret = %q", got)
	}
	markerData, err := os.ReadFile(filepath.Join(targetRoot, markerPath))
	if err != nil {
		t.Fatalf("read import marker: %v", err)
	}
	if len(markerData) == 0 || bytes.Contains(markerData, []byte("01234567890123456789012345678901")) {
		t.Fatal("import marker contains secret material")
	}
	var marker Manifest
	if err := json.Unmarshal(markerData, &marker); err != nil {
		t.Fatalf("decode import marker: %v", err)
	}
	if len(marker.Secrets) != 0 {
		t.Fatalf("import marker secrets = %v, want none", marker.Secrets)
	}

	imported, err = Import(ctx, ImportConfig{
		BundlePath: bundle,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "license-token",
		Secrets:    importedSecrets,
	})
	if err != nil {
		t.Fatalf("second Import() error = %v", err)
	}
	if imported {
		t.Fatal("second Import() ignored the import marker")
	}
}

func TestImportPreflightReportsLiteralFootprintBeforeExtraction(t *testing.T) {
	ctx := context.Background()
	sourceData := filepath.Join(t.TempDir(), "data")
	database := bytes.Repeat([]byte{'d'}, 5<<20)
	session := bytes.Repeat([]byte{'s'}, 3<<20)
	mustWriteFile(t, filepath.Join(sourceData, "app.db"), database)
	mustWriteFile(t, filepath.Join(sourceData, "sessions", "account", "session.bin"), session)
	bundle := filepath.Join(t.TempDir(), "state.tcs")
	if err := Pack(ctx, PackConfig{
		SourceData: sourceData,
		OutputPath: bundle,
		BundleID:   "preflight-test",
		AppVersion: "0.8.2",
		License:    "license-token",
	}); err != nil {
		t.Fatalf("Pack() error = %v", err)
	}

	targetRoot := t.TempDir()
	secrets := &memorySecrets{}
	preflightErr := errors.New("preflight stopped import")
	var got BundleFootprint
	preflightCalls := 0
	imported, err := Import(ctx, ImportConfig{
		BundlePath: bundle,
		TargetRoot: targetRoot,
		BundleID:   "preflight-test",
		AppVersion: "0.8.2",
		License:    "license-token",
		Secrets:    secrets,
		Preflight: func(_ context.Context, footprint BundleFootprint) error {
			preflightCalls++
			got = footprint
			entries, readErr := os.ReadDir(targetRoot)
			if readErr != nil {
				t.Fatalf("read target root during preflight: %v", readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("target entries during preflight = %d, want 0", len(entries))
			}
			if len(secrets.values) != 0 {
				t.Fatal("secrets were installed before preflight")
			}
			return preflightErr
		},
	})
	if imported {
		t.Fatal("Import() = true after preflight failure")
	}
	if !errors.Is(err, preflightErr) {
		t.Fatalf("Import() error = %v, want preflight error", err)
	}
	if preflightCalls != 1 {
		t.Fatalf("preflight calls = %d, want 1", preflightCalls)
	}
	want := BundleFootprint{
		DataBytes:     uint64(len(database) + len(session)),
		RegularFiles:  2,
		Directories:   3,
		DatabaseBytes: uint64(len(database)),
	}
	if got != want {
		t.Fatalf("preflight footprint = %+v, want %+v", got, want)
	}
}

func TestImportPreflightRejectsTruncatedAuthenticatedTail(t *testing.T) {
	ctx := context.Background()
	sourceData := filepath.Join(t.TempDir(), "data")
	mustWriteFile(t, filepath.Join(sourceData, "app.db"), bytes.Repeat([]byte{'x'}, 1<<20))
	bundle := filepath.Join(t.TempDir(), "state.tcs")
	if err := Pack(ctx, PackConfig{
		SourceData: sourceData,
		OutputPath: bundle,
		BundleID:   "truncated-tail-test",
		AppVersion: "0.8.2",
		License:    "license-token",
	}); err != nil {
		t.Fatalf("Pack() error = %v", err)
	}
	info, err := os.Stat(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(bundle, info.Size()-1); err != nil {
		t.Fatal(err)
	}

	preflightCalls := 0
	_, err = Import(ctx, ImportConfig{
		BundlePath: bundle,
		TargetRoot: t.TempDir(),
		BundleID:   "truncated-tail-test",
		AppVersion: "0.8.2",
		License:    "license-token",
		Secrets:    &memorySecrets{},
		Preflight: func(context.Context, BundleFootprint) error {
			preflightCalls++
			return nil
		},
	})
	if !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("Import() error = %v, want ErrInvalidBundle", err)
	}
	if preflightCalls != 0 {
		t.Fatalf("preflight calls = %d, want 0 for unauthenticated bundle", preflightCalls)
	}
}

func TestImportPreflightRejectsBundleChangedBetweenPasses(t *testing.T) {
	ctx := context.Background()
	packBundle := func(contents string) string {
		sourceData := filepath.Join(t.TempDir(), "data")
		mustWriteFile(t, filepath.Join(sourceData, "app.db"), []byte(contents))
		bundle := filepath.Join(t.TempDir(), "state.tcs")
		if err := Pack(ctx, PackConfig{
			SourceData: sourceData,
			OutputPath: bundle,
			BundleID:   "two-pass-test",
			AppVersion: "0.8.2",
			License:    "license-token",
		}); err != nil {
			t.Fatalf("Pack() error = %v", err)
		}
		return bundle
	}
	bundle := packBundle("original")
	replacement := packBundle("changed!")
	targetRoot := t.TempDir()
	secrets := &memorySecrets{}
	_, err := Import(ctx, ImportConfig{
		BundlePath: bundle,
		TargetRoot: targetRoot,
		BundleID:   "two-pass-test",
		AppVersion: "0.8.2",
		License:    "license-token",
		Secrets:    secrets,
		Preflight: func(context.Context, BundleFootprint) error {
			return os.Rename(replacement, bundle)
		},
	})
	if !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("Import() error = %v, want ErrInvalidBundle", err)
	}
	if len(secrets.values) != 0 {
		t.Fatal("secrets were installed from a bundle changed between passes")
	}
	if _, statErr := os.Stat(filepath.Join(targetRoot, "data")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target data exists after bundle changed between passes: %v", statErr)
	}
}

func TestArchiveEntryLimitRejectsMoreThanOneHundredThousandEntries(t *testing.T) {
	bundle := writeRawTestBundle(t, func(writer *tar.Writer) {
		writeRawTestHeader(t, writer, &tar.Header{
			Name:     "data/app.db",
			Mode:     0o600,
			Size:     1,
			Typeflag: tar.TypeReg,
		})
		if _, err := writer.Write([]byte{'d'}); err != nil {
			t.Fatal(err)
		}
		for index := 0; index < 99_999; index++ {
			writeRawTestHeader(t, writer, &tar.Header{
				Name:     fmt.Sprintf("data/d%05d", index),
				Mode:     0o700,
				Typeflag: tar.TypeDir,
			})
		}
	})

	preflightCalls := 0
	_, err := Import(context.Background(), ImportConfig{
		BundlePath: bundle,
		TargetRoot: t.TempDir(),
		BundleID:   "entry-limit-test",
		AppVersion: "0.8.2",
		License:    "license-token",
		Secrets:    &memorySecrets{},
		Preflight: func(context.Context, BundleFootprint) error {
			preflightCalls++
			return nil
		},
	})
	if !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("Import() error = %v, want ErrInvalidBundle", err)
	}
	if preflightCalls != 0 {
		t.Fatalf("preflight calls = %d, want 0 for oversized archive", preflightCalls)
	}
}

func TestArchiveUncompressedLimitIncludesPostTarTail(t *testing.T) {
	bundle := writeRawTestBundleWithTail(t, func(writer *tar.Writer) {
		writeRawTestHeader(t, writer, &tar.Header{
			Name:     "data/app.db",
			Mode:     0o600,
			Size:     1,
			Typeflag: tar.TypeReg,
		})
		if _, err := writer.Write([]byte{'d'}); err != nil {
			t.Fatal(err)
		}
	}, bytes.Repeat([]byte{'x'}, 8<<10))
	file, err := os.Open(bundle)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	tracked := &countingTestReader{reader: file}
	decrypted, err := newEncryptedReader(tracked, "license-token")
	if err != nil {
		t.Fatal(err)
	}
	_, err = scanArchiveWithLimits(context.Background(), decrypted, "", archiveScanLimits{
		maxUncompressedBytes: 4 << 10,
		maxEntries:           100_000,
	})
	if !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("scanArchiveWithLimits() error = %v, want ErrInvalidBundle", err)
	}
	if tracked.read != info.Size() {
		t.Fatalf("authenticated source bytes read = %d, want complete %d-byte stream", tracked.read, info.Size())
	}
}

func TestArchiveUncompressedLimitExactBoundary(t *testing.T) {
	writeEntries := func(writer *tar.Writer) {
		writeRawTestHeader(t, writer, &tar.Header{
			Name:     "data/app.db",
			Mode:     0o600,
			Size:     1,
			Typeflag: tar.TypeReg,
		})
		if _, err := writer.Write([]byte{'d'}); err != nil {
			t.Fatal(err)
		}
	}
	exactBundle := writeRawTestBundleWithTail(t, writeEntries, nil)
	limit := rawTestBundleUncompressedBytes(t, exactBundle)
	for _, test := range []struct {
		name    string
		bundle  string
		wantErr bool
	}{
		{name: "exact limit accepted", bundle: exactBundle},
		{name: "limit plus one rejected", bundle: writeRawTestBundleWithTail(t, writeEntries, []byte{'x'}), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, err := os.Open(test.bundle)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil {
				t.Fatal(err)
			}
			tracked := &countingTestReader{reader: file}
			decrypted, err := newEncryptedReader(tracked, "license-token")
			if err != nil {
				t.Fatal(err)
			}
			_, err = scanArchiveWithLimits(context.Background(), decrypted, "", archiveScanLimits{
				maxUncompressedBytes: limit,
				maxEntries:           100_000,
			})
			if test.wantErr && !errors.Is(err, ErrInvalidBundle) {
				t.Fatalf("scanArchiveWithLimits() error = %v, want ErrInvalidBundle", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("scanArchiveWithLimits() error = %v at exact limit", err)
			}
			if tracked.read != info.Size() {
				t.Fatalf("authenticated source bytes read = %d, want complete %d-byte stream", tracked.read, info.Size())
			}
		})
	}
}

func TestImportPreflightCountsImplicitUniqueDirectories(t *testing.T) {
	bundle := writeRawTestBundle(t, func(writer *tar.Writer) {
		writeRawTestHeader(t, writer, &tar.Header{
			Name:     "data/accounts",
			Mode:     0o700,
			Typeflag: tar.TypeDir,
		})
		for _, name := range []string{
			"data/app.db",
			"data/accounts/one/session.bin",
			"data/accounts/two/session.bin",
		} {
			writeRawTestHeader(t, writer, &tar.Header{
				Name:     name,
				Mode:     0o600,
				Size:     1,
				Typeflag: tar.TypeReg,
			})
			if _, err := writer.Write([]byte{'d'}); err != nil {
				t.Fatal(err)
			}
		}
	})
	preflightErr := errors.New("stop after footprint")
	var got BundleFootprint
	_, err := Import(context.Background(), ImportConfig{
		BundlePath: bundle,
		TargetRoot: t.TempDir(),
		BundleID:   "entry-limit-test",
		AppVersion: "0.8.2",
		License:    "license-token",
		Secrets:    &memorySecrets{},
		Preflight: func(_ context.Context, footprint BundleFootprint) error {
			got = footprint
			return preflightErr
		},
	})
	if !errors.Is(err, preflightErr) {
		t.Fatalf("Import() error = %v, want preflight error", err)
	}
	if got.Directories != 4 {
		t.Fatalf("footprint directories = %d, want 4 unique materialized directories", got.Directories)
	}
}

func TestImportSyncFailureAbortsBeforeSecretsAndCommit(t *testing.T) {
	ctx := context.Background()
	sourceData := filepath.Join(t.TempDir(), "data")
	mustWriteFile(t, filepath.Join(sourceData, "app.db"), []byte("database"))
	bundle := filepath.Join(t.TempDir(), "state.tcs")
	if err := Pack(ctx, PackConfig{
		SourceData: sourceData,
		OutputPath: bundle,
		BundleID:   "sync-failure-test",
		AppVersion: "0.8.2",
		License:    "license-token",
	}); err != nil {
		t.Fatalf("Pack() error = %v", err)
	}
	syncErr := errors.New("forced extracted file sync failure")
	previousSync := syncExtractedFile
	syncExtractedFile = func(*os.File) error { return syncErr }
	t.Cleanup(func() { syncExtractedFile = previousSync })
	preflightCalls := 0
	secrets := &memorySecrets{}
	targetRoot := t.TempDir()
	imported, err := Import(ctx, ImportConfig{
		BundlePath: bundle,
		TargetRoot: targetRoot,
		BundleID:   "sync-failure-test",
		AppVersion: "0.8.2",
		License:    "license-token",
		Secrets:    secrets,
		Preflight: func(context.Context, BundleFootprint) error {
			preflightCalls++
			return nil
		},
	})
	if imported {
		t.Fatal("Import() imported after extracted file sync failure")
	}
	if !errors.Is(err, syncErr) {
		t.Fatalf("Import() error = %v, want sync failure", err)
	}
	if preflightCalls != 1 {
		t.Fatalf("preflight calls = %d, want 1", preflightCalls)
	}
	if len(secrets.values) != 0 {
		t.Fatal("secrets were installed after extracted file sync failure")
	}
	entries, err := os.ReadDir(targetRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("target entries after sync failure = %d, want 0", len(entries))
	}
}

func TestUniversalKeyBundleImportsIndependentlyOfLicenseToken(t *testing.T) {
	ctx := context.Background()
	seedKey := []byte("0123456789abcdef0123456789abcdef")
	sourceData := filepath.Join(t.TempDir(), "data")
	mustWriteFile(t, filepath.Join(sourceData, "app.db"), []byte("portable-database"))
	bundle := filepath.Join(t.TempDir(), "state.tcs")
	if err := Pack(ctx, PackConfig{
		SourceData: sourceData,
		OutputPath: bundle,
		BundleID:   "public-seed-082",
		AppVersion: "0.8.2",
		Key:        seedKey,
		Secrets:    &memorySecrets{},
	}); err != nil {
		t.Fatalf("Pack() error = %v", err)
	}

	for _, target := range []string{"mac-one", "mac-two"} {
		t.Run(target, func(t *testing.T) {
			targetRoot := t.TempDir()
			imported, err := Import(ctx, ImportConfig{
				BundlePath: bundle,
				TargetRoot: targetRoot,
				AppVersion: "0.8.2",
				Key:        append([]byte(nil), seedKey...),
				Secrets:    &memorySecrets{},
			})
			if err != nil {
				t.Fatalf("Import() error = %v", err)
			}
			if !imported {
				t.Fatal("Import() = false, want true")
			}
			assertFileContent(t, filepath.Join(targetRoot, "data", "app.db"), "portable-database")
		})
	}
}

func TestUniversalKeyBundleRejectsWrongKey(t *testing.T) {
	ctx := context.Background()
	sourceData := filepath.Join(t.TempDir(), "data")
	mustWriteFile(t, filepath.Join(sourceData, "app.db"), []byte("database"))
	bundle := filepath.Join(t.TempDir(), "state.tcs")
	if err := Pack(ctx, PackConfig{
		SourceData: sourceData,
		OutputPath: bundle,
		BundleID:   "public-seed-082",
		AppVersion: "0.8.2",
		Key:        []byte("0123456789abcdef0123456789abcdef"),
		Secrets:    &memorySecrets{},
	}); err != nil {
		t.Fatalf("Pack() error = %v", err)
	}

	_, err := Import(ctx, ImportConfig{
		BundlePath: bundle,
		TargetRoot: t.TempDir(),
		AppVersion: "0.8.2",
		Key:        []byte("fedcba9876543210fedcba9876543210"),
		Secrets:    &memorySecrets{},
	})
	if !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("Import() error = %v, want ErrInvalidBundle", err)
	}
}

func TestUniversalKeyBundleRejectsUnexpectedBundleID(t *testing.T) {
	ctx := context.Background()
	seedKey := []byte("0123456789abcdef0123456789abcdef")
	sourceData := filepath.Join(t.TempDir(), "data")
	mustWriteFile(t, filepath.Join(sourceData, "app.db"), []byte("database"))
	bundle := filepath.Join(t.TempDir(), "state.tcs")
	if err := Pack(ctx, PackConfig{
		SourceData: sourceData,
		OutputPath: bundle,
		BundleID:   "public-seed-082",
		AppVersion: "0.8.2",
		Key:        seedKey,
		Secrets:    &memorySecrets{},
	}); err != nil {
		t.Fatalf("Pack() error = %v", err)
	}

	_, err := Import(ctx, ImportConfig{
		BundlePath: bundle,
		TargetRoot: t.TempDir(),
		BundleID:   "other-public-seed",
		AppVersion: "0.8.2",
		Key:        append([]byte(nil), seedKey...),
		Secrets:    &memorySecrets{},
	})
	if !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("Import() error = %v, want ErrInvalidBundle", err)
	}
}

func TestUniversalKeyConfigurationRequiresExactly32Bytes(t *testing.T) {
	sourceData := filepath.Join(t.TempDir(), "data")
	mustWriteFile(t, filepath.Join(sourceData, "app.db"), []byte("database"))
	err := Pack(context.Background(), PackConfig{
		SourceData: sourceData,
		OutputPath: filepath.Join(t.TempDir(), "state.tcs"),
		BundleID:   "public-seed-082",
		AppVersion: "0.8.2",
		Key:        make([]byte, 31),
		Secrets:    &memorySecrets{},
	})
	if err == nil {
		t.Fatal("Pack() error = nil, want invalid key error")
	}
}

func TestImportRejectsWrongLicenseAndLeavesExistingData(t *testing.T) {
	ctx := context.Background()
	sourceData := filepath.Join(t.TempDir(), "data")
	mustWriteFile(t, filepath.Join(sourceData, "app.db"), []byte("database"))
	bundle := filepath.Join(t.TempDir(), "state.tcs")
	if err := Pack(ctx, PackConfig{
		SourceData: sourceData,
		OutputPath: bundle,
		BundleID:   "bundle",
		AppVersion: "0.8.2",
		License:    "correct-license",
		Secrets:    &memorySecrets{},
	}); err != nil {
		t.Fatalf("Pack() error = %v", err)
	}

	targetRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(targetRoot, "data", "app.db"), []byte("existing"))
	if _, err := Import(ctx, ImportConfig{
		BundlePath: bundle,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "wrong-license",
		Secrets:    &memorySecrets{},
	}); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("Import() error = %v, want ErrInvalidBundle", err)
	}
	assertFileContent(t, filepath.Join(targetRoot, "data", "app.db"), "existing")
}

func TestImportRejectsVersionMismatch(t *testing.T) {
	ctx := context.Background()
	sourceData := filepath.Join(t.TempDir(), "data")
	mustWriteFile(t, filepath.Join(sourceData, "app.db"), []byte("database"))
	bundle := filepath.Join(t.TempDir(), "state.tcs")
	if err := Pack(ctx, PackConfig{
		SourceData: sourceData,
		OutputPath: bundle,
		BundleID:   "bundle",
		AppVersion: "0.8.2",
		License:    "license-token",
		Secrets:    &memorySecrets{},
	}); err != nil {
		t.Fatalf("Pack() error = %v", err)
	}

	_, err := Import(ctx, ImportConfig{
		BundlePath: bundle,
		TargetRoot: t.TempDir(),
		AppVersion: "0.8.3",
		License:    "license-token",
		Secrets:    &memorySecrets{},
	})
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("Import() error = %v, want ErrVersionMismatch", err)
	}
}

func TestPackRejectsSymlinks(t *testing.T) {
	sourceData := filepath.Join(t.TempDir(), "data")
	mustWriteFile(t, filepath.Join(sourceData, "app.db"), []byte("database"))
	if err := os.Symlink(filepath.Join(sourceData, "app.db"), filepath.Join(sourceData, "linked.db")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := Pack(context.Background(), PackConfig{
		SourceData: sourceData,
		OutputPath: filepath.Join(t.TempDir(), "state.tcs"),
		BundleID:   "bundle",
		AppVersion: "0.8.2",
		License:    "license-token",
		Secrets:    &memorySecrets{},
	})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Pack() error = %v, want ErrUnsafePath", err)
	}
}

func TestPackRejectsSQLiteSidecars(t *testing.T) {
	for _, sidecar := range []string{"app.db-shm", "app.db-wal"} {
		t.Run(sidecar, func(t *testing.T) {
			sourceData := filepath.Join(t.TempDir(), "data")
			mustWriteFile(t, filepath.Join(sourceData, "app.db"), []byte("database"))
			mustWriteFile(t, filepath.Join(sourceData, sidecar), []byte("transient"))

			err := Pack(context.Background(), PackConfig{
				SourceData: sourceData,
				OutputPath: filepath.Join(t.TempDir(), "state.tcs"),
				BundleID:   "bundle",
				AppVersion: "0.8.2",
				License:    "license-token",
				Secrets:    &memorySecrets{},
			})
			if !errors.Is(err, ErrInvalidBundle) {
				t.Fatalf("Pack() error = %v, want ErrInvalidBundle", err)
			}
		})
	}
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}

func writeRawTestBundle(t *testing.T, writeEntries func(*tar.Writer)) string {
	t.Helper()
	return writeRawTestBundleWithTail(t, writeEntries, nil)
}

func writeRawTestBundleWithTail(t *testing.T, writeEntries func(*tar.Writer), tail []byte) string {
	t.Helper()
	bundle := filepath.Join(t.TempDir(), "state.tcs")
	file, err := os.OpenFile(bundle, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := newEncryptedWriter(file, "license-token")
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(encrypted)
	tarWriter := tar.NewWriter(gzipWriter)
	manifest, err := json.Marshal(Manifest{
		SchemaVersion: SchemaVersion,
		BundleID:      "entry-limit-test",
		AppVersion:    "0.8.2",
	})
	if err != nil {
		t.Fatal(err)
	}
	writeRawTestHeader(t, tarWriter, &tar.Header{
		Name:     "manifest.json",
		Mode:     0o600,
		Size:     int64(len(manifest)),
		Typeflag: tar.TypeReg,
	})
	if _, err := tarWriter.Write(manifest); err != nil {
		t.Fatal(err)
	}
	writeEntries(tarWriter)
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if len(tail) > 0 {
		if _, err := gzipWriter.Write(tail); err != nil {
			t.Fatalf("write post-tar tail: %v", err)
		}
	}
	for _, closer := range []struct {
		name  string
		close func() error
	}{
		{name: "gzip writer", close: gzipWriter.Close},
		{name: "encrypted writer", close: encrypted.Close},
		{name: "bundle file", close: file.Close},
	} {
		if err := closer.close(); err != nil {
			t.Fatalf("close %s: %v", closer.name, err)
		}
	}
	return bundle
}

func writeRawTestHeader(t *testing.T, writer *tar.Writer, header *tar.Header) {
	t.Helper()
	if err := writer.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
}

func rawTestBundleUncompressedBytes(t *testing.T, bundle string) int64 {
	t.Helper()
	file, err := os.Open(bundle)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decrypted, err := newEncryptedReader(file, "license-token")
	if err != nil {
		t.Fatal(err)
	}
	gzipReader, err := gzip.NewReader(decrypted)
	if err != nil {
		t.Fatal(err)
	}
	written, err := io.Copy(io.Discard, gzipReader)
	if err != nil {
		t.Fatal(err)
	}
	if err := gzipReader.Close(); err != nil {
		t.Fatal(err)
	}
	return written
}

type countingTestReader struct {
	reader io.Reader
	read   int64
}

func (r *countingTestReader) Read(destination []byte) (int, error) {
	read, err := r.reader.Read(destination)
	r.read += int64(read)
	return read, err
}
