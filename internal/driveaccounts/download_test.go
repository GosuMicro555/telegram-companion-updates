package driveaccounts

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}
func TestDownloadTraversesPublicFolderAndPreservesNames(t *testing.T) {
	d := NewDownloader()
	d.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "drive.google.com" {
			t.Fatal("unexpected host")
		}
		switch r.URL.Path {
		case "/embeddedfolderview":
			if r.URL.Query().Get("id") == "root" {
				return response(`<html><title>root</title><a href="https://drive.google.com/drive/folders/child">tdata</a></html>`), nil
			}
			return response(`<html><title>tdata</title><a href="https://drive.google.com/file/d/keyfile/view">key_datas</a></html>`), nil
		case "/uc":
			return response("TDF$synthetic"), nil
		}
		t.Fatalf("unexpected path %s", r.URL.Path)
		return nil, nil
	})
	root := t.TempDir()
	if err := d.Fetch(context.Background(), Reference{ID: "root", Folder: true}, root, func(string) {}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "tdata", "key_datas"))
	if err != nil || !bytes.HasPrefix(b, []byte("TDF$")) {
		t.Fatal("folder content not downloaded")
	}
}
func TestDownloadRejectsFolderTraversalAndForeignRedirect(t *testing.T) {
	d := NewDownloader()
	d.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		return response(`<html><title>test</title><a href="https://drive.google.com/file/d/key/view">../escape</a></html>`), nil
	})
	if err := d.Fetch(context.Background(), Reference{ID: "root", Folder: true}, t.TempDir(), func(string) {}); err == nil {
		t.Fatal("accepted folder path traversal")
	}
	request, _ := http.NewRequest("GET", "https://evil.example/download", nil)
	if err := d.client.CheckRedirect(request, []*http.Request{{}}); err == nil {
		t.Fatal("allowed foreign redirect")
	}
}
func TestDownloadConfirmationBoundToOriginalFile(t *testing.T) {
	good := `<html><form id="download-form" action="https://drive.usercontent.google.com/download" method="get"><input name="id" value="file1"><input name="export" value="download"><input name="confirm" value="t"><input name="uuid" value="token"></form></html>`
	u, err := confirmationURL([]byte(good), Reference{ID: "file1"})
	if err != nil || !strings.Contains(u, "confirm=t") {
		t.Fatal("valid confirmation rejected", err)
	}
	for _, bad := range []string{strings.ReplaceAll(good, "file1", "different"), strings.ReplaceAll(good, "drive.usercontent.google.com", "evil.example"), strings.ReplaceAll(good, `method="get"`, `method="post"`)} {
		if _, err := confirmationURL([]byte(bad), Reference{ID: "file1"}); err == nil {
			t.Fatal("accepted unsafe confirmation")
		}
	}
}
