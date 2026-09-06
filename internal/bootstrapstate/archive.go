package bootstrapstate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxExtractedBytes int64  = 64 << 30
	maxArchiveEntries uint64 = 100_000
)

var errArchiveUncompressedLimit = errors.New("bootstrap state: uncompressed archive limit exceeded")

var syncExtractedFile = (*os.File).Sync

type archiveScanLimits struct {
	maxUncompressedBytes int64
	maxEntries           uint64
}

type archiveLimitReader struct {
	reader    io.Reader
	remaining int64
}

func (r *archiveLimitReader) Read(destination []byte) (int, error) {
	if r.remaining == 0 {
		var probe [1]byte
		read, err := r.reader.Read(probe[:])
		if read > 0 {
			return 0, errArchiveUncompressedLimit
		}
		return 0, err
	}
	if int64(len(destination)) > r.remaining {
		destination = destination[:r.remaining]
	}
	read, err := r.reader.Read(destination)
	r.remaining -= int64(read)
	return read, err
}

type archiveScanResult struct {
	manifest      Manifest
	footprint     BundleFootprint
	digest        [sha256.Size]byte
	databaseFound bool
}

func writeArchive(ctx context.Context, destination io.Writer, sourceData string, manifest Manifest) error {
	gzipWriter := gzip.NewWriter(destination)
	tarWriter := tar.NewWriter(gzipWriter)
	closeArchive := func() error {
		return errors.Join(tarWriter.Close(), gzipWriter.Close())
	}

	manifestData, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := tarWriter.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o600, Size: int64(len(manifestData)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := tarWriter.Write(manifestData); err != nil {
		return err
	}

	err = filepath.WalkDir(sourceData, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(sourceData, filePath)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if isSQLiteSidecar(relative) {
			return ErrInvalidBundle
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsRegular() && !info.IsDir()) {
			return ErrUnsafePath
		}
		name := path.Join("data", filepath.ToSlash(relative))
		header := &tar.Header{Name: name, Mode: 0o600, Typeflag: tar.TypeReg}
		if info.IsDir() {
			header.Mode = 0o700
			header.Typeflag = tar.TypeDir
		} else {
			header.Size = info.Size()
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		file, err := os.Open(filePath)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tarWriter, file)
		closeErr := file.Close()
		return errors.Join(copyErr, closeErr)
	})
	if err != nil {
		return err
	}
	return closeArchive()
}

func isSQLiteSidecar(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	return base == "app.db-shm" || base == "app.db-wal"
}

func scanArchive(ctx context.Context, source io.Reader, destination string) (archiveScanResult, error) {
	return scanArchiveWithLimits(ctx, source, destination, archiveScanLimits{
		maxUncompressedBytes: maxExtractedBytes,
		maxEntries:           maxArchiveEntries,
	})
}

func scanArchiveWithLimits(ctx context.Context, source io.Reader, destination string, limits archiveScanLimits) (archiveScanResult, error) {
	if limits.maxUncompressedBytes <= 0 || limits.maxEntries == 0 {
		return archiveScanResult{}, ErrInvalidBundle
	}
	hasher := sha256.New()
	compressed := io.TeeReader(source, hasher)
	gzipReader, err := gzip.NewReader(compressed)
	if err != nil {
		return archiveScanResult{}, ErrInvalidBundle
	}
	uncompressed := &archiveLimitReader{reader: gzipReader, remaining: limits.maxUncompressedBytes}
	tarReader := tar.NewReader(uncompressed)
	var result archiveScanResult
	var manifestFound bool
	var entries uint64
	materializedDirectories := make(map[string]struct{})

	for {
		if err := ctx.Err(); err != nil {
			return archiveScanResult{}, err
		}
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, errArchiveUncompressedLimit) {
			return archiveScanResult{}, rejectOversizedArchive(ctx, gzipReader, compressed)
		}
		if err != nil {
			return archiveScanResult{}, ErrInvalidBundle
		}
		entries++
		if entries > limits.maxEntries {
			return archiveScanResult{}, ErrInvalidBundle
		}
		cleanName, ok := safeArchiveName(header.Name)
		if !ok || (cleanName != "manifest.json" && !strings.HasPrefix(cleanName, "data/")) {
			return archiveScanResult{}, ErrUnsafePath
		}
		if header.Size < 0 {
			return archiveScanResult{}, ErrInvalidBundle
		}
		if cleanName == "manifest.json" {
			if manifestFound || header.Typeflag != tar.TypeReg || header.Size > 1024*1024 {
				return archiveScanResult{}, ErrInvalidBundle
			}
			manifestData := make([]byte, header.Size)
			if _, err := io.ReadFull(tarReader, manifestData); err != nil {
				if errors.Is(err, errArchiveUncompressedLimit) {
					return archiveScanResult{}, rejectOversizedArchive(ctx, gzipReader, compressed)
				}
				return archiveScanResult{}, ErrInvalidBundle
			}
			if err := json.Unmarshal(manifestData, &result.manifest); err != nil {
				return archiveScanResult{}, ErrInvalidBundle
			}
			manifestFound = true
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if header.Size != 0 {
				return archiveScanResult{}, ErrInvalidBundle
			}
			addMaterializedDirectories(materializedDirectories, cleanName, true)
			if destination != "" {
				target := filepath.Join(destination, filepath.FromSlash(cleanName))
				if err := os.MkdirAll(target, 0o700); err != nil {
					return archiveScanResult{}, err
				}
			}
		case tar.TypeReg, tar.TypeRegA:
			addMaterializedDirectories(materializedDirectories, cleanName, false)
			result.footprint.DataBytes += uint64(header.Size)
			result.footprint.RegularFiles++
			if cleanName == "data/app.db" {
				result.footprint.DatabaseBytes = uint64(header.Size)
				result.databaseFound = true
			}
			if destination == "" {
				if _, err := io.CopyN(io.Discard, tarReader, header.Size); err != nil {
					if errors.Is(err, errArchiveUncompressedLimit) {
						return archiveScanResult{}, rejectOversizedArchive(ctx, gzipReader, compressed)
					}
					return archiveScanResult{}, ErrInvalidBundle
				}
				continue
			}
			target := filepath.Join(destination, filepath.FromSlash(cleanName))
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return archiveScanResult{}, err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return archiveScanResult{}, err
			}
			_, copyErr := io.CopyN(file, tarReader, header.Size)
			var syncErr error
			if copyErr == nil {
				syncErr = syncExtractedFile(file)
			}
			closeErr := file.Close()
			if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
				if errors.Is(err, errArchiveUncompressedLimit) {
					return archiveScanResult{}, rejectOversizedArchive(ctx, gzipReader, compressed)
				}
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, ErrInvalidBundle) {
					return archiveScanResult{}, ErrInvalidBundle
				}
				return archiveScanResult{}, err
			}
		default:
			return archiveScanResult{}, ErrUnsafePath
		}
	}
	if !manifestFound {
		return archiveScanResult{}, ErrInvalidBundle
	}
	result.footprint.Directories = uint64(len(materializedDirectories))
	if _, err := io.Copy(io.Discard, uncompressed); err != nil {
		if errors.Is(err, errArchiveUncompressedLimit) {
			return archiveScanResult{}, rejectOversizedArchive(ctx, gzipReader, compressed)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return archiveScanResult{}, ctxErr
		}
		return archiveScanResult{}, ErrInvalidBundle
	}
	if err := gzipReader.Close(); err != nil {
		return archiveScanResult{}, ErrInvalidBundle
	}
	if _, err := io.Copy(io.Discard, compressed); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return archiveScanResult{}, ctxErr
		}
		return archiveScanResult{}, ErrInvalidBundle
	}
	copy(result.digest[:], hasher.Sum(nil))
	return result, nil
}

func addMaterializedDirectories(directories map[string]struct{}, cleanName string, includeEntry bool) {
	directory := path.Dir(cleanName)
	if includeEntry {
		directory = cleanName
	}
	for directory == "data" || strings.HasPrefix(directory, "data/") {
		directories[directory] = struct{}{}
		if directory == "data" {
			return
		}
		directory = path.Dir(directory)
	}
}

func rejectOversizedArchive(ctx context.Context, gzipReader *gzip.Reader, compressed io.Reader) error {
	_ = gzipReader.Close()
	if _, err := io.Copy(io.Discard, compressed); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	return ErrInvalidBundle
}

func safeArchiveName(name string) (string, bool) {
	if name == "" || strings.Contains(name, "\\") || path.IsAbs(name) {
		return "", false
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != name {
		return "", false
	}
	return clean, true
}
