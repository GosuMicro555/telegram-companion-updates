package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManifestIsExactlyPinned(t *testing.T) {
	manifest, err := loadManifest(filepath.Join("..", "models", "manifest.json"))
	require.NoError(t, err)
	require.Equal(t, 1, manifest.SchemaVersion)
	require.Equal(t, "intfloat/multilingual-e5-small", manifest.Repository)
	require.Equal(t, "614241f622f53c4eeff9890bdc4f31cfecc418b3", manifest.Revision)
	require.Equal(t, []manifestFile{
		{Source: "onnx/model.onnx", Destination: "model.onnx", Size: 470268510, SHA256: "ca456c06b3a9505ddfd9131408916dd79290368331e7d76bb621f1cba6bc8665"},
		{Source: "tokenizer.json", Destination: "tokenizer.json", Size: 17082730, SHA256: "0b44a9d7b51c3c62626640cda0e2c2f70fdacdc25bbbd68038369d14ebdf4c39"},
	}, manifest.Files)
}

func TestManifestRejectsAppendedJSONAndAllowsTrailingWhitespace(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "models", "manifest.json"))
	require.NoError(t, err)
	validPath := filepath.Join(t.TempDir(), "valid.json")
	require.NoError(t, os.WriteFile(validPath, append(append([]byte(nil), contents...), []byte(" \n\t")...), 0o600))
	_, err = loadManifest(validPath)
	require.NoError(t, err)

	invalidPath := filepath.Join(t.TempDir(), "invalid.json")
	require.NoError(t, os.WriteFile(invalidPath, append(append([]byte(nil), contents...), []byte("\n{}")...), 0o600))
	_, err = loadManifest(invalidPath)
	require.Error(t, err)
	require.Contains(t, err.Error(), "trailing")
}

func TestAllowedDownloadURLRejectsNonHTTPSAndForeignRedirects(t *testing.T) {
	for _, raw := range []string{
		"https://huggingface.co/repo/file", "https://cdn-lfs.huggingface.co/file", "https://transfer.xethub.hf.co/file",
	} {
		parsed, err := url.Parse(raw)
		require.NoError(t, err)
		require.NoError(t, validateDownloadURL(parsed), raw)
	}
	for _, raw := range []string{
		"http://huggingface.co/repo/file", "https://huggingface.co.evil.test/file", "https://hf.co/file", "https://example.com/file",
	} {
		parsed, err := url.Parse(raw)
		require.NoError(t, err)
		require.Error(t, validateDownloadURL(parsed), raw)
	}
}

func TestWriteVerifiedUsesOwnerOnlyAtomicFileAndCleansMismatch(t *testing.T) {
	contents := []byte("tiny model")
	sum := sha256.Sum256(contents)
	dir := t.TempDir()
	destination := filepath.Join(dir, "model.onnx")

	err := writeVerified(context.Background(), bytes.NewReader(contents), destination, int64(len(contents)), hex.EncodeToString(sum[:]))
	require.NoError(t, err)
	info, err := os.Stat(destination)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.Equal(t, contents, mustRead(t, destination))
	matches, err := filepath.Glob(filepath.Join(dir, ".model.onnx.*"))
	require.NoError(t, err)
	require.Empty(t, matches)

	badDestination := filepath.Join(dir, "bad.onnx")
	err = writeVerified(context.Background(), bytes.NewReader(contents), badDestination, int64(len(contents)+1), hex.EncodeToString(sum[:]))
	require.Error(t, err)
	_, err = os.Stat(badDestination)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	return contents
}
