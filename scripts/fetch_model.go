package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type manifest struct {
	SchemaVersion int            `json:"schema_version"`
	Repository    string         `json:"repository"`
	Revision      string         `json:"revision"`
	Destination   string         `json:"destination"`
	Files         []manifestFile `json:"files"`
}

type manifestFile struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}

func main() {
	manifestPath := flag.String("manifest", "models/manifest.json", "path to pinned model manifest")
	verifyManifestOnly := flag.Bool("verify-manifest-only", false, "validate the pinned manifest without downloading model files")
	verifyPayload := flag.Bool("verify-payload", false, "verify every pinned model file without downloading")
	packageRoot := flag.String("package-root", "", "copy the verified model payload into this artifact root")
	flag.Parse()
	var err error
	if strings.TrimSpace(*packageRoot) != "" {
		err = packageModel(context.Background(), *manifestPath, *packageRoot)
	} else if *verifyPayload {
		err = verifyModelPayload(context.Background(), *manifestPath)
	} else if *verifyManifestOnly {
		_, err = loadManifest(*manifestPath)
	} else {
		err = fetch(context.Background(), *manifestPath)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func fetch(ctx context.Context, manifestPath string) error {
	pinned, err := loadManifest(manifestPath)
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout:       45 * time.Minute,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error { return validateDownloadURL(request.URL) },
	}
	for _, file := range pinned.Files {
		destination := modelSourcePath(manifestPath, pinned, file)
		if err := verifyModelFile(ctx, destination, file.Size, file.SHA256); err == nil {
			continue
		}
		downloadURL := &url.URL{Scheme: "https", Host: "huggingface.co", Path: "/" + pinned.Repository + "/resolve/" + pinned.Revision + "/" + file.Source}
		if err := validateDownloadURL(downloadURL); err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL.String(), nil)
		if err != nil {
			return fmt.Errorf("create model request: %w", err)
		}
		response, err := client.Do(request)
		if err != nil {
			return fmt.Errorf("download %s: %w", file.Source, err)
		}
		if response.StatusCode != http.StatusOK {
			_ = response.Body.Close()
			return fmt.Errorf("download %s: HTTP %s", file.Source, response.Status)
		}
		err = writeVerified(ctx, response.Body, destination, file.Size, file.SHA256)
		closeErr := response.Body.Close()
		if err != nil {
			return fmt.Errorf("store %s: %w", file.Source, err)
		}
		if closeErr != nil {
			return fmt.Errorf("close %s response: %w", file.Source, closeErr)
		}
	}
	return nil
}

func verifyModelPayload(ctx context.Context, manifestPath string) error {
	pinned, err := loadManifest(manifestPath)
	if err != nil {
		return err
	}
	for _, file := range pinned.Files {
		if err := verifyModelFile(ctx, modelSourcePath(manifestPath, pinned, file), file.Size, file.SHA256); err != nil {
			return fmt.Errorf("verify %s: %w", file.Source, err)
		}
	}
	return nil
}

func packageModel(ctx context.Context, manifestPath, artifactRoot string) error {
	pinned, err := loadManifest(manifestPath)
	if err != nil {
		return err
	}
	artifactRoot = strings.TrimSpace(artifactRoot)
	if artifactRoot == "" {
		return errors.New("model package root is required")
	}
	artifactRoot, err = filepath.Abs(artifactRoot)
	if err != nil {
		return errors.New("model package root is invalid")
	}
	if err := os.MkdirAll(artifactRoot, 0o700); err != nil {
		return fmt.Errorf("create model package root: %w", err)
	}
	for _, file := range pinned.Files {
		source := modelSourcePath(manifestPath, pinned, file)
		input, err := os.Open(source)
		if err != nil {
			return fmt.Errorf("open verified model source: %w", err)
		}
		destination := filepath.Join(artifactRoot, filepath.FromSlash(pinned.Destination), file.Destination)
		copyErr := writeVerified(ctx, input, destination, file.Size, file.SHA256)
		closeErr := input.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
	}
	manifestContents, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read model manifest for packaging: %w", err)
	}
	manifestDigest := sha256.Sum256(manifestContents)
	return writeVerified(ctx, strings.NewReader(string(manifestContents)), filepath.Join(artifactRoot, "models", "manifest.json"), int64(len(manifestContents)), hex.EncodeToString(manifestDigest[:]))
}

func modelSourcePath(manifestPath string, pinned manifest, file manifestFile) string {
	return filepath.Join(filepath.Dir(manifestPath), filepath.Base(filepath.FromSlash(pinned.Destination)), file.Destination)
}

func verifyModelFile(ctx context.Context, path string, expectedSize int64, expectedDigest string) error {
	entry, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !entry.Mode().IsRegular() || entry.Mode()&os.ModeSymlink != 0 {
		return errors.New("model artifact is not a regular file")
	}
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != expectedSize {
		return errors.New("model artifact size or type does not match manifest")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, &contextReader{ctx: ctx, reader: input}); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(expectedDigest) {
		return errors.New("model artifact digest does not match manifest")
	}
	return nil
}

func loadManifest(path string) (manifest, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, fmt.Errorf("read model manifest: %w", err)
	}
	var result manifest
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return manifest{}, fmt.Errorf("decode model manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return manifest{}, errors.New("model manifest contains trailing JSON data")
		}
		return manifest{}, fmt.Errorf("decode trailing model manifest data: %w", err)
	}
	if result.SchemaVersion != 1 || result.Repository == "" || result.Revision == "" || result.Destination == "" || len(result.Files) == 0 {
		return manifest{}, errors.New("model manifest is incomplete")
	}
	cleanDestination := filepath.Clean(filepath.FromSlash(result.Destination))
	if filepath.IsAbs(cleanDestination) || cleanDestination == "." || cleanDestination == ".." || strings.HasPrefix(cleanDestination, ".."+string(filepath.Separator)) || filepath.Base(filepath.Dir(cleanDestination)) != "models" {
		return manifest{}, errors.New("model manifest destination is invalid")
	}
	for _, file := range result.Files {
		if file.Size <= 0 || !validManifestSHA(file.SHA256) || filepath.Base(file.Destination) != file.Destination || strings.Contains(file.Source, "..") || strings.HasPrefix(file.Source, "/") {
			return manifest{}, errors.New("model manifest contains an invalid file")
		}
	}
	return result, nil
}

func validateDownloadURL(value *url.URL) error {
	if value == nil || value.Scheme != "https" {
		return errors.New("model downloads and redirects must use HTTPS")
	}
	host := strings.ToLower(value.Hostname())
	if host == "huggingface.co" || strings.HasSuffix(host, ".huggingface.co") || strings.HasSuffix(host, ".hf.co") {
		return nil
	}
	return fmt.Errorf("model download host %q is not allowlisted", host)
}

func writeVerified(ctx context.Context, reader io.Reader, destination string, expectedSize int64, expectedDigest string) error {
	if !validManifestSHA(expectedDigest) || expectedSize < 0 {
		return errors.New("invalid expected model artifact metadata")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create model directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".*")
	if err != nil {
		return fmt.Errorf("create model temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		_ = temporary.Close()
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("protect model temporary file: %w", err)
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(&contextReader{ctx: ctx, reader: reader}, expectedSize+1))
	if err != nil {
		return fmt.Errorf("write model temporary file: %w", err)
	}
	if written != expectedSize {
		return fmt.Errorf("model artifact size is %d, expected %d", written, expectedSize)
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); actual != strings.ToLower(expectedDigest) {
		return fmt.Errorf("model artifact SHA-256 is %s, expected %s", actual, expectedDigest)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync model temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close model temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("publish model artifact: %w", err)
	}
	keep = true
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return fmt.Errorf("open model directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync model directory: %w", err)
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(buffer)
	}
}

func validManifestSHA(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
