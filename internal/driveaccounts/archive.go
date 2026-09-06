package driveaccounts

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const MaxDownloadBytes int64 = 2 << 30
const MaxExtractedBytes int64 = 5 << 30
const MaxEntries = 50000
const MaxDepth = 32

var ErrUnsafe = errors.New("unsafe_content")
var ErrLimit = errors.New("size_limit")

func canonicalPath(name string) string { return cases.Fold().String(norm.NFC.String(name)) }

func safePath(name string) bool {
	if name == "" || len(name) > 2048 || strings.ContainsAny(name, "\\:") || strings.HasPrefix(name, "/") || len(strings.Split(name, "/")) > MaxDepth {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || strings.TrimRight(part, " .") != part || strings.ContainsAny(part, `<>"|?*`) {
			return false
		}
		for _, r := range part {
			if unicode.IsControl(r) {
				return false
			}
		}
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		switch stem {
		case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
			return false
		}
		if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9' {
			return false
		}
	}
	return true
}

// ExtractZIP admits the whole archive before the first write and confines all writes
// beneath an os.Root, including against path replacement during extraction.
func ExtractZIP(ctx context.Context, archive, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	z, err := zip.OpenReader(archive)
	if err != nil {
		return errors.New("zip_invalid")
	}
	defer z.Close()
	if len(z.File) > MaxEntries {
		return ErrLimit
	}
	seen := map[string]bool{}
	type member struct {
		name      string
		directory bool
	}
	paths := map[string]member{}
	var total uint64
	for _, f := range z.File {
		name := strings.TrimSuffix(f.Name, "/")
		key := canonicalPath(name)
		if !safePath(name) || seen[key] || (f.Flags&1) != 0 || (!f.Mode().IsRegular() && !f.Mode().IsDir()) || (f.Method != zip.Store && f.Method != zip.Deflate) {
			return ErrUnsafe
		}
		seen[key] = true
		parts := strings.Split(name, "/")
		for n := range parts {
			prefix := strings.Join(parts[:n+1], "/")
			folded := canonicalPath(prefix)
			directory := n < len(parts)-1 || f.FileInfo().IsDir()
			if prior, ok := paths[folded]; ok && (prior.name != prefix || prior.directory != directory) {
				return ErrUnsafe
			}
			paths[folded] = member{prefix, directory}
		}
		if f.UncompressedSize64 > uint64(MaxExtractedBytes)-total {
			return ErrLimit
		}
		total += f.UncompressedSize64
		if f.UncompressedSize64 > 0 && (f.CompressedSize64 == 0 || f.UncompressedSize64/f.CompressedSize64 > 200) {
			return ErrLimit
		}
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close()
	var written int64
	for _, f := range z.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := strings.TrimSuffix(f.Name, "/")
		if f.FileInfo().IsDir() {
			if err := root.MkdirAll(name, 0700); err != nil {
				return err
			}
			continue
		}
		if err := root.MkdirAll(path.Dir(name), 0700); err != nil {
			return err
		}
		src, err := f.Open()
		if err != nil {
			return ErrUnsafe
		}
		dst, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			src.Close()
			return err
		}
		n, copyErr := io.Copy(dst, io.LimitReader(cancelReader{ctx, src}, MaxExtractedBytes-written+1))
		closeErr := dst.Close()
		src.Close()
		written += n
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written > MaxExtractedBytes {
			return ErrLimit
		}
	}
	return nil
}

type cancelReader struct {
	ctx context.Context
	r   io.Reader
}

func (r cancelReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
