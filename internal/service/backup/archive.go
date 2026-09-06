package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"telegram-companion/internal/domain"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	recoveryKeyBytes    = chacha20poly1305.KeySize
	archiveChunkSize    = 64 * 1024
	maxArchiveEntrySize = int64(2 << 30)
	manifestArchivePath = "manifest.json"
	databaseArchivePath = "database.sqlite"
)

var (
	archiveMagic  = []byte("SCOUTBK1")
	ErrUnsafePath = errors.New("unsafe backup path")
)

type Manifest struct {
	ArchiveSchemaVersion int               `json:"archive_schema_version"`
	ID                   string            `json:"id"`
	Kind                 domain.BackupKind `json:"kind"`
	CreatedAt            time.Time         `json:"created_at"`
	DatabaseSchemaSHA256 string            `json:"database_schema_sha256"`
	Files                []ManifestFile    `json:"files"`
}

type ManifestFile struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

func encryptArchive(ctx context.Context, key []byte, path string, plaintext io.Reader) (retErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(key) != recoveryKeyBytes {
		return errors.New("invalid recovery key length")
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return fmt.Errorf("create archive cipher: %w", err)
	}
	output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create encrypted archive: %w", err)
	}
	defer func() {
		if closeErr := output.Close(); closeErr != nil {
			retErr = errors.Join(retErr, closeErr)
		}
		if retErr != nil {
			_ = os.Remove(path)
		}
	}()

	prefix := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, prefix); err != nil {
		return fmt.Errorf("generate archive nonce: %w", err)
	}
	if err := writeAll(output, archiveMagic); err != nil {
		return err
	}
	if err := writeAll(output, prefix); err != nil {
		return err
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], archiveChunkSize)
	if err := writeAll(output, size[:]); err != nil {
		return err
	}

	buffer := make([]byte, archiveChunkSize)
	var counter uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := io.ReadFull(plaintext, buffer)
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return fmt.Errorf("read archive plaintext: %w", readErr)
		}
		if n > 0 {
			ciphertext := aead.Seal(nil, archiveNonce(prefix, counter), buffer[:n], archiveAAD(prefix, counter))
			binary.BigEndian.PutUint32(size[:], uint32(len(ciphertext)))
			if err := writeAll(output, size[:]); err != nil {
				return err
			}
			if err := writeAll(output, ciphertext); err != nil {
				return err
			}
			counter++
		}
		if readErr != nil {
			break
		}
	}
	final := aead.Seal(nil, archiveNonce(prefix, counter), nil, archiveAAD(prefix, counter))
	binary.BigEndian.PutUint32(size[:], uint32(len(final)))
	if err := writeAll(output, size[:]); err != nil {
		return err
	}
	if err := writeAll(output, final); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("sync encrypted archive: %w", err)
	}
	return nil
}

func decryptArchive(ctx context.Context, key []byte, path string, plaintext io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(key) != recoveryKeyBytes {
		return errors.New("invalid recovery key length")
	}
	input, err := openRegularNoSymlink(path)
	if err != nil {
		return err
	}
	defer input.Close()
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return fmt.Errorf("create archive cipher: %w", err)
	}
	header := make([]byte, len(archiveMagic)+16+4)
	if _, err := io.ReadFull(input, header); err != nil {
		return fmt.Errorf("read archive header: %w", err)
	}
	if !bytes.Equal(header[:len(archiveMagic)], archiveMagic) {
		return errors.New("invalid archive magic")
	}
	prefix := header[len(archiveMagic) : len(archiveMagic)+16]
	if got := binary.BigEndian.Uint32(header[len(archiveMagic)+16:]); got != archiveChunkSize {
		return fmt.Errorf("unsupported archive chunk size %d", got)
	}

	var counter uint64
	var frameSize [4]byte
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := io.ReadFull(input, frameSize[:]); err != nil {
			return fmt.Errorf("read encrypted frame size: %w", err)
		}
		n := binary.BigEndian.Uint32(frameSize[:])
		if n < uint32(aead.Overhead()) || n > archiveChunkSize+uint32(aead.Overhead()) {
			return errors.New("invalid encrypted frame size")
		}
		ciphertext := make([]byte, n)
		if _, err := io.ReadFull(input, ciphertext); err != nil {
			return fmt.Errorf("read encrypted frame: %w", err)
		}
		chunk, err := aead.Open(nil, archiveNonce(prefix, counter), ciphertext, archiveAAD(prefix, counter))
		if err != nil {
			return errors.New("authenticate encrypted archive")
		}
		counter++
		if len(chunk) == 0 {
			var trailing [1]byte
			n, err := input.Read(trailing[:])
			if err != nil && !errors.Is(err, io.EOF) {
				return fmt.Errorf("read archive trailer: %w", err)
			}
			if n != 0 {
				return errors.New("archive has trailing data")
			}
			return nil
		}
		if err := writeAll(plaintext, chunk); err != nil {
			return fmt.Errorf("write decrypted archive: %w", err)
		}
	}
}

func archiveNonce(prefix []byte, counter uint64) []byte {
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	copy(nonce, prefix)
	binary.BigEndian.PutUint64(nonce[16:], counter)
	return nonce
}

func archiveAAD(prefix []byte, counter uint64) []byte {
	aad := make([]byte, 0, len(archiveMagic)+len(prefix)+8)
	aad = append(aad, archiveMagic...)
	aad = append(aad, prefix...)
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], counter)
	return append(aad, encoded[:]...)
}

func writeSnapshotTar(ctx context.Context, snapshotDir string, manifest Manifest, output io.Writer) error {
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode backup manifest: %w", err)
	}
	w := tar.NewWriter(output)
	if err := writeTarBytes(w, manifestArchivePath, manifestData); err != nil {
		return err
	}
	files := append([]ManifestFile(nil), manifest.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !safeArchivePath(file.Path) {
			return ErrUnsafePath
		}
		path := filepath.Join(snapshotDir, filepath.FromSlash(file.Path))
		input, err := openRegularNoSymlink(path)
		if err != nil {
			return err
		}
		info, err := input.Stat()
		if err != nil {
			_ = input.Close()
			return err
		}
		if info.Size() != file.SizeBytes {
			_ = input.Close()
			return errors.New("snapshot changed while archiving")
		}
		if err := w.WriteHeader(&tar.Header{Name: file.Path, Mode: 0o600, Size: info.Size(), Typeflag: tar.TypeReg}); err != nil {
			_ = input.Close()
			return fmt.Errorf("write tar header: %w", err)
		}
		_, copyErr := copyContext(ctx, w, input)
		closeErr := input.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close tar archive: %w", err)
	}
	return nil
}

func writeTarBytes(w *tar.Writer, name string, data []byte) error {
	if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		return fmt.Errorf("write tar header: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write tar data: %w", err)
	}
	return nil
}

func extractTar(ctx context.Context, input io.Reader, destination string) error {
	reader := tar.NewReader(input)
	seen := make(map[string]struct{})
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar header: %w", err)
		}
		if header.Typeflag != tar.TypeReg || !safeArchivePath(header.Name) || header.Size < 0 || header.Size > maxArchiveEntrySize {
			return ErrUnsafePath
		}
		if _, exists := seen[header.Name]; exists {
			return ErrUnsafePath
		}
		seen[header.Name] = struct{}{}
		path := filepath.Join(destination, filepath.FromSlash(header.Name))
		if err := ensureWithin(destination, path); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create restore directory: %w", err)
		}
		output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create restored file: %w", err)
		}
		_, copyErr := copyContext(ctx, output, io.LimitReader(reader, header.Size))
		syncErr := output.Sync()
		closeErr := output.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil {
			return errors.Join(copyErr, syncErr, closeErr)
		}
	}
}

func extractTarWorkspace(ctx context.Context, input io.Reader, destination restoreWorkspace) ([]string, error) {
	reader := tar.NewReader(input)
	seen := make(map[string]struct{})
	var paths []string
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return paths, destination.Sync()
		}
		if err != nil {
			return nil, fmt.Errorf("read tar header: %w", err)
		}
		if header.Typeflag != tar.TypeReg || !safeArchivePath(header.Name) || header.Size < 0 || header.Size > maxArchiveEntrySize {
			return nil, ErrUnsafePath
		}
		if _, exists := seen[header.Name]; exists {
			return nil, ErrUnsafePath
		}
		seen[header.Name] = struct{}{}
		output, err := destination.CreateFile(header.Name)
		if err != nil {
			return nil, err
		}
		_, copyErr := copyContext(ctx, output, io.LimitReader(reader, header.Size))
		syncErr := output.Sync()
		closeErr := output.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil {
			return nil, errors.Join(copyErr, syncErr, closeErr)
		}
		paths = append(paths, header.Name)
	}
}

func safeArchivePath(path string) bool {
	if path == "" || strings.Contains(path, "\\") || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	return clean == path && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func ensureWithin(root, path string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return ErrUnsafePath
	}
	return nil
}

func fileManifest(ctx context.Context, root, archivePath string) (ManifestFile, error) {
	if !safeArchivePath(archivePath) {
		return ManifestFile{}, ErrUnsafePath
	}
	path := filepath.Join(root, filepath.FromSlash(archivePath))
	input, err := openRegularNoSymlink(path)
	if err != nil {
		return ManifestFile{}, err
	}
	defer input.Close()
	hash := sha256.New()
	size, err := copyContext(ctx, hash, input)
	if err != nil {
		return ManifestFile{}, err
	}
	return ManifestFile{Path: archivePath, SizeBytes: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func workspaceManifestFile(ctx context.Context, root restoreWorkspace, archivePath string) (ManifestFile, error) {
	if !safeArchivePath(archivePath) {
		return ManifestFile{}, ErrUnsafePath
	}
	input, err := root.OpenFile(archivePath)
	if err != nil {
		return ManifestFile{}, err
	}
	defer input.Close()
	hash := sha256.New()
	size, err := copyContext(ctx, hash, input)
	if err != nil {
		return ManifestFile{}, err
	}
	return ManifestFile{Path: archivePath, SizeBytes: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func verifyManifestWorkspace(ctx context.Context, root restoreWorkspace, extracted []string, manifest Manifest) error {
	listed := make(map[string]ManifestFile, len(manifest.Files))
	for _, expected := range manifest.Files {
		if !safeArchivePath(expected.Path) || expected.SizeBytes < 0 || expected.SizeBytes > maxArchiveEntrySize {
			return ErrUnsafePath
		}
		if _, exists := listed[expected.Path]; exists {
			return errors.New("duplicate manifest path")
		}
		listed[expected.Path] = expected
		actual, err := workspaceManifestFile(ctx, root, expected.Path)
		if err != nil {
			return err
		}
		if actual.SizeBytes != expected.SizeBytes || actual.SHA256 != expected.SHA256 {
			return fmt.Errorf("backup hash mismatch for %s", expected.Path)
		}
	}
	actual := make(map[string]struct{}, len(extracted))
	for _, path := range extracted {
		if _, duplicate := actual[path]; duplicate {
			return errors.New("duplicate extracted archive path")
		}
		actual[path] = struct{}{}
		if path == manifestArchivePath {
			continue
		}
		if _, ok := listed[path]; !ok {
			return fmt.Errorf("unlisted archive file %s", path)
		}
	}
	if len(actual) != len(listed)+1 {
		return errors.New("archive file count mismatch")
	}
	if _, ok := actual[manifestArchivePath]; !ok {
		return errors.New("backup manifest is missing")
	}
	return nil
}

func verifyManifestFiles(ctx context.Context, root string, manifest Manifest) error {
	listed := make(map[string]ManifestFile, len(manifest.Files))
	for _, expected := range manifest.Files {
		if !safeArchivePath(expected.Path) || expected.SizeBytes < 0 || expected.SizeBytes > maxArchiveEntrySize {
			return ErrUnsafePath
		}
		if _, exists := listed[expected.Path]; exists {
			return errors.New("duplicate manifest path")
		}
		listed[expected.Path] = expected
		actual, err := fileManifest(ctx, root, expected.Path)
		if err != nil {
			return err
		}
		if actual.SizeBytes != expected.SizeBytes || actual.SHA256 != expected.SHA256 {
			return fmt.Errorf("backup hash mismatch for %s", expected.Path)
		}
	}
	var actualCount int
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrUnsafePath
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		archivePath := filepath.ToSlash(relative)
		if archivePath == manifestArchivePath {
			return nil
		}
		actualCount++
		if _, ok := listed[archivePath]; !ok {
			return fmt.Errorf("unlisted archive file %s", archivePath)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if actualCount != len(listed) {
		return errors.New("archive file count mismatch")
	}
	return nil
}

func openRegularNoSymlink(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, ErrUnsafePath
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, ErrUnsafePath
	}
	return file, nil
}

func copyContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, 64*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := source.Read(buffer)
		if n > 0 {
			written, writeErr := destination.Write(buffer[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
