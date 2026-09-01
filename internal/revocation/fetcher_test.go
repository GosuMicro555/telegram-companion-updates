package revocation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewHTTPFetcherRequiresSafeHTTPSURL(t *testing.T) {
	for _, rawURL := range []string{
		"",
		"http://raw.githubusercontent.com/owner/repo/main/revocations.tcrev",
		"https://user@raw.githubusercontent.com/owner/repo/main/revocations.tcrev",
		"https://raw.githubusercontent.com/owner/repo/main/revocations.tcrev?x=1",
		"https://raw.githubusercontent.com/owner/repo/main/revocations.tcrev#fragment",
		"https://raw.githubusercontent.com/owner/repo/main/revocations.tcrev",
		"https://example.com/GosuMicro555/telegram-companion-updates/main/revocations.tcrev",
		"https://raw.githubusercontent.com/GosuMicro555/telegram-companion-updates/main/other.tcrev",
	} {
		if _, err := NewHTTPFetcher(rawURL); err == nil {
			t.Fatalf("NewHTTPFetcher(%q) succeeded", rawURL)
		}
	}
	fetcher, err := NewHTTPFetcher(ProductionManifestURL)
	if err != nil {
		t.Fatalf("NewHTTPFetcher: %v", err)
	}
	if fetcher.client == http.DefaultClient || fetcher.client.Timeout != FetchTimeout {
		t.Fatal("fetcher does not own a dedicated five-second client")
	}
}

func TestHTTPFetcherSendsAnonymousNoCacheRequest(t *testing.T) {
	fixedNow := time.Date(2026, 8, 25, 12, 34, 56, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("method = %q", request.Method)
		}
		if got := request.Header.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("Cache-Control = %q", got)
		}
		query := request.URL.Query()
		if len(query) != 1 || query.Get("revocation_check") != "202608251234" {
			t.Errorf("query = %#v", query)
		}
		if strings.Contains(request.URL.RawQuery, "license") || strings.Contains(request.URL.RawQuery, "machine") {
			t.Error("query contains an identifier")
		}
		_, _ = io.WriteString(writer, "TCREV1.test.test")
	}))
	defer server.Close()

	fetcher, err := newHTTPFetcher(server.URL+"/revocations.tcrev", server.Client(), func() time.Time { return fixedNow }, true)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := fetcher.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got, want := string(bytes), "TCREV1.test.test"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestHTTPFetcherRejectsStatusRedirectAndOversize(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{name: "non-2xx", handler: func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusServiceUnavailable) }},
		{name: "non-https redirect", handler: func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, "http://example.com/revocations.tcrev", http.StatusFound)
		}},
		{name: "oversized", handler: func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(writer, strings.Repeat("x", MaxEnvelopeBytes+1))
		}},
		{name: "declared oversized", handler: func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Length", strconv.Itoa(MaxEnvelopeBytes+1))
			writer.WriteHeader(http.StatusOK)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			fetcher, err := newHTTPFetcher(server.URL+"/revocations.tcrev", server.Client(), time.Now, true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fetcher.Fetch(context.Background()); err == nil {
				t.Fatal("Fetch succeeded")
			}
		})
	}
}

func TestHTTPFetcherRedirectPolicyRejectsDifferentHostOrScheme(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unused")
	})}
	fetcher, err := newHTTPFetcher("https://source.example/revocations.tcrev", client, time.Now, false)
	if err != nil {
		t.Fatal(err)
	}
	via, _ := http.NewRequest(http.MethodGet, "https://source.example/revocations.tcrev", nil)
	for _, target := range []string{
		"https://other.example/revocations.tcrev",
		"http://source.example/revocations.tcrev",
	} {
		request, _ := http.NewRequest(http.MethodGet, target, nil)
		if redirectErr := fetcher.client.CheckRedirect(request, []*http.Request{via}); redirectErr == nil {
			t.Fatalf("redirect to %q accepted", target)
		}
	}
	sameOrigin, _ := http.NewRequest(http.MethodGet, "https://source.example/moved.tcrev", nil)
	if redirectErr := fetcher.client.CheckRedirect(sameOrigin, []*http.Request{via}); redirectErr != nil {
		t.Fatalf("same-origin HTTPS redirect rejected: %v", redirectErr)
	}
}

func TestHTTPFetcherAppliesDeadlineAndClosesBody(t *testing.T) {
	body := &trackingReadCloser{Reader: strings.NewReader("TCREV1.test.test")}
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		deadline, ok := request.Context().Deadline()
		if !ok || time.Until(deadline) > FetchTimeout || time.Until(deadline) < FetchTimeout-time.Second {
			return nil, errors.New("missing bounded deadline")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
	})
	client := &http.Client{Transport: transport, Timeout: FetchTimeout}
	fetcher, err := newHTTPFetcher("https://example.com/revocations.tcrev", client, time.Now, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fetcher.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !body.closed.Load() {
		t.Fatal("response body was not closed")
	}
}

func TestHTTPFetcherCancelsBlockedRoundTripAtTimeout(t *testing.T) {
	const timeout = 25 * time.Millisecond
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	fetcher, err := newHTTPFetcherWithTimeout(
		"https://example.com/revocations.tcrev",
		&http.Client{Transport: transport},
		time.Now,
		false,
		timeout,
	)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := fetcher.Fetch(context.Background()); err == nil {
		t.Fatal("blocked round trip succeeded")
	}
	if elapsed := time.Since(started); elapsed < timeout/2 || elapsed > time.Second {
		t.Fatalf("timeout elapsed = %v", elapsed)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type trackingReadCloser struct {
	io.Reader
	closed atomic.Bool
}

func (body *trackingReadCloser) Close() error {
	body.closed.Store(true)
	return nil
}
