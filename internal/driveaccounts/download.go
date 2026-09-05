package driveaccounts

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

var ErrAccess = errors.New("drive_unavailable")
var downloadHost = regexp.MustCompile(`^doc-[a-z0-9-]+-docs\.googleusercontent\.com$`)

const maxHTML = 8 << 20

type Downloader struct{ client *http.Client }

func NewDownloader() *Downloader {
	return &Downloader{client: &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !allowedDownloadURL(req.URL) {
			return ErrAccess
		}
		return nil
	}}}
}
func allowedDownloadURL(u *url.URL) bool {
	return u.Scheme == "https" && u.User == nil && u.Port() == "" && u.Fragment == "" && (u.Host == "drive.google.com" || u.Host == "drive.usercontent.google.com" || downloadHost.MatchString(u.Host))
}
func (d *Downloader) get(ctx context.Context, raw string) (*http.Response, error) {
	u, err := url.Parse(raw)
	if err != nil || !allowedDownloadURL(u) {
		return nil, ErrAccess
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, ErrAccess
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 TelegramCompanion/0.8.6")
	resp, err := d.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrAccess
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, ErrAccess
	}
	return resp, nil
}
func fileURL(ref Reference) string {
	q := url.Values{"id": {ref.ID}, "export": {"download"}}
	if ref.ResourceKey != "" {
		q.Set("resourcekey", ref.ResourceKey)
	}
	return "https://drive.google.com/uc?" + q.Encode()
}

// Fetch writes only into a fresh import workspace. No cookies, credentials, raw
// URLs or Google identifiers are persisted by the downloader.
func (d *Downloader) Fetch(ctx context.Context, ref Reference, destination string, phase func(string)) error {
	var used int64
	phase("downloading")
	if ref.Folder {
		return d.folder(ctx, ref, destination, "", 0, map[string]bool{}, new(int), &used)
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close()
	err = d.file(ctx, ref, root, "source.zip", &used)
	if errors.Is(err, ErrAccess) { // A generic /uc ID may refer to a public folder.
		return d.folder(ctx, ref, destination, "", 0, map[string]bool{}, new(int), &used)
	}
	if err != nil {
		return err
	}
	phase("extracting")
	err = ExtractZIP(ctx, filepath.Join(destination, "source.zip"), destination)
	_ = root.Remove("source.zip")
	return err
}

func (d *Downloader) file(ctx context.Context, ref Reference, root *os.Root, name string, used *int64) error {
	next := fileURL(ref)
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := d.get(ctx, next)
		if err != nil {
			return err
		}
		reader := bufio.NewReader(resp.Body)
		head, _ := reader.Peek(512)
		if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") || strings.HasPrefix(strings.TrimSpace(string(head)), "<") {
			body, e := io.ReadAll(io.LimitReader(reader, maxHTML+1))
			resp.Body.Close()
			if e != nil || len(body) > maxHTML {
				return ErrAccess
			}
			next, err = confirmationURL(body, ref)
			if err != nil {
				return ErrAccess
			}
			continue
		}
		if resp.ContentLength > MaxDownloadBytes-*used {
			resp.Body.Close()
			return ErrLimit
		}
		if err = root.MkdirAll(path.Dir(name), 0700); err != nil {
			resp.Body.Close()
			return err
		}
		f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			resp.Body.Close()
			return err
		}
		n, e := io.Copy(f, io.LimitReader(cancelReader{ctx, reader}, MaxDownloadBytes-*used+1))
		ce := f.Close()
		resp.Body.Close()
		*used += n
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		if *used > MaxDownloadBytes {
			return ErrLimit
		}
		return nil
	}
	return ErrAccess
}
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func nodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(nodeText(c))
	}
	return b.String()
}
func visit(n *html.Node, f func(*html.Node)) {
	f(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		visit(c, f)
	}
}

func confirmationURL(body []byte, ref Reference) (string, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", ErrAccess
	}
	var form *html.Node
	visit(doc, func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" && attr(n, "id") == "download-form" {
			form = n
		}
	})
	if form == nil || (attr(form, "method") != "" && strings.ToLower(attr(form, "method")) != "get") {
		return "", ErrAccess
	}
	u, err := url.Parse(attr(form, "action"))
	if err != nil || !allowedDownloadURL(u) || u.Host != "drive.usercontent.google.com" || u.Path != "/download" || u.RawQuery != "" {
		return "", ErrAccess
	}
	q := url.Values{}
	invalid := false
	visit(form, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "input" {
			return
		}
		name, value := attr(n, "name"), attr(n, "value")
		switch name {
		case "id", "export", "confirm", "uuid", "resourcekey":
		default:
			invalid = true
		}
		if _, ok := q[name]; ok {
			invalid = true
		}
		if len(value) > 512 {
			invalid = true
		}
		q.Set(name, value)
	})
	if invalid || q.Get("id") != ref.ID || q.Get("export") != "download" || q.Get("confirm") == "" || (q.Get("resourcekey") != "" && q.Get("resourcekey") != ref.ResourceKey) {
		return "", ErrAccess
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

type folderEntry struct {
	Ref  Reference
	Name string
}

func folderEntries(body []byte) ([]folderEntry, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, ErrAccess
	}
	result := []folderEntry{}
	seen := map[string]bool{}
	invalid := false
	visit(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "a" {
			return
		}
		href := attr(n, "href")
		if !strings.HasPrefix(href, "https://drive.google.com/") {
			return
		}
		ref, e := parseReference(href)
		if e != nil {
			return
		}
		name := strings.TrimSpace(nodeText(n))
		if !safePath(name) || strings.Contains(name, "/") || seen[canonicalPath(name)] {
			invalid = true
			return
		}
		seen[canonicalPath(name)] = true
		result = append(result, folderEntry{ref, name})
	})
	if invalid {
		return nil, ErrUnsafe
	}
	if len(result) == 0 {
		return nil, ErrAccess
	}
	return result, nil
}
func (d *Downloader) folder(ctx context.Context, ref Reference, destination, prefix string, depth int, seen map[string]bool, count *int, used *int64) error {
	if depth > MaxDepth || seen[ref.ID] {
		return ErrUnsafe
	}
	seen[ref.ID] = true
	q := url.Values{"id": {ref.ID}}
	if ref.ResourceKey != "" {
		q.Set("resourcekey", ref.ResourceKey)
	}
	resp, err := d.get(ctx, "https://drive.google.com/embeddedfolderview?"+q.Encode())
	if err != nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTML+1))
	resp.Body.Close()
	if err != nil {
		return err
	}
	if len(body) > maxHTML {
		return ErrLimit
	}
	entries, err := folderEntries(body)
	if err != nil {
		return err
	}
	*count += len(entries)
	if *count > MaxEntries {
		return ErrLimit
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := path.Join(prefix, entry.Name)
		if !safePath(name) {
			return ErrUnsafe
		}
		if entry.Ref.Folder {
			if err = root.MkdirAll(name, 0700); err != nil {
				return err
			}
			err = d.folder(ctx, entry.Ref, destination, name, depth+1, seen, count, used)
		} else {
			err = d.file(ctx, entry.Ref, root, name, used)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
