package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncryptedArchiveRejectsTamperingAndHonorsCancellation(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, recoveryKeyBytes)
	plain := bytes.Repeat([]byte("scout-backup-payload"), 20000)
	path := filepath.Join(t.TempDir(), "backup.scout")
	require.NoError(t, encryptArchive(context.Background(), key, path, bytes.NewReader(plain)))

	var restored bytes.Buffer
	require.NoError(t, decryptArchive(context.Background(), key, path, &restored))
	require.Equal(t, plain, restored.Bytes())

	archive, err := os.ReadFile(path)
	require.NoError(t, err)
	archive[len(archive)-1] ^= 0xff
	require.NoError(t, os.WriteFile(path, archive, 0o600))
	restored.Reset()
	require.Error(t, decryptArchive(context.Background(), key, path, &restored))

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, encryptArchive(cancelled, key, filepath.Join(t.TempDir(), "cancelled"), bytes.NewReader(plain)), context.Canceled)
}

func TestExtractTarRejectsTraversalSymlinksAndDuplicates(t *testing.T) {
	for _, test := range []struct {
		name    string
		headers []tar.Header
	}{
		{name: "traversal", headers: []tar.Header{{Name: "../outside", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}}},
		{name: "absolute", headers: []tar.Header{{Name: "/outside", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}}},
		{name: "symlink", headers: []tar.Header{{Name: "session", Linkname: "outside", Typeflag: tar.TypeSymlink}}},
		{name: "duplicate", headers: []tar.Header{
			{Name: databaseArchivePath, Mode: 0o600, Size: 1, Typeflag: tar.TypeReg},
			{Name: databaseArchivePath, Mode: 0o600, Size: 1, Typeflag: tar.TypeReg},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var payload bytes.Buffer
			writer := tar.NewWriter(&payload)
			for i := range test.headers {
				require.NoError(t, writer.WriteHeader(&test.headers[i]))
				if test.headers[i].Size > 0 {
					_, err := writer.Write(bytes.Repeat([]byte{'x'}, int(test.headers[i].Size)))
					require.NoError(t, err)
				}
			}
			require.NoError(t, writer.Close())
			require.Error(t, extractTar(context.Background(), bytes.NewReader(payload.Bytes()), t.TempDir()))
		})
	}
}
