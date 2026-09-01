package revocation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	FetchTimeout          = 5 * time.Second
	ProductionManifestURL = "https://raw.githubusercontent.com/GosuMicro555/telegram-companion-updates/main/revocations.tcrev"
)

var errManifestFetch = errors.New("revocation: manifest fetch failed")

type Fetcher interface {
	Fetch(context.Context) ([]byte, error)
}

type HTTPFetcher struct {
	manifestURL *url.URL
	client      *http.Client
	now         func() time.Time
	timeout     time.Duration
}

func NewHTTPFetcher(rawURL string) (*HTTPFetcher, error) {
	if rawURL != ProductionManifestURL {
		return nil, errManifestFetch
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return newHTTPFetcher(rawURL, &http.Client{Transport: transport}, time.Now, false)
}

func newHTTPFetcher(rawURL string, sourceClient *http.Client, now func() time.Time, allowHTTP bool) (*HTTPFetcher, error) {
	return newHTTPFetcherWithTimeout(rawURL, sourceClient, now, allowHTTP, FetchTimeout)
}

func newHTTPFetcherWithTimeout(rawURL string, sourceClient *http.Client, now func() time.Time, allowHTTP bool, timeout time.Duration) (*HTTPFetcher, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !validManifestURL(parsed, allowHTTP) || sourceClient == nil || sourceClient.Transport == nil || now == nil || timeout <= 0 {
		return nil, errManifestFetch
	}
	client := *sourceClient
	client.Timeout = timeout
	baseScheme := parsed.Scheme
	baseHost := parsed.Host
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 || request.URL.Scheme != baseScheme || request.URL.Host != baseHost {
			return errManifestFetch
		}
		return nil
	}
	copyURL := *parsed
	return &HTTPFetcher{manifestURL: &copyURL, client: &client, now: now, timeout: timeout}, nil
}

func (fetcher *HTTPFetcher) Fetch(ctx context.Context) ([]byte, error) {
	if fetcher == nil || fetcher.manifestURL == nil || fetcher.client == nil || fetcher.now == nil || fetcher.timeout <= 0 {
		return nil, errManifestFetch
	}
	requestContext, cancel := context.WithTimeout(ctx, fetcher.timeout)
	defer cancel()

	requestURL := *fetcher.manifestURL
	query := requestURL.Query()
	query.Set("revocation_check", fetcher.now().UTC().Format("200601021504"))
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, errManifestFetch
	}
	request.Header.Set("Cache-Control", "no-cache")
	request.Header.Set("Accept", "application/octet-stream")

	response, err := fetcher.client.Do(request)
	if err != nil {
		return nil, errManifestFetch
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.CopyN(io.Discard, response.Body, 4096)
		return nil, errManifestFetch
	}
	if response.ContentLength > MaxEnvelopeBytes {
		return nil, errManifestFetch
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxEnvelopeBytes+1))
	if err != nil || len(body) == 0 || len(body) > MaxEnvelopeBytes {
		return nil, errManifestFetch
	}
	return body, nil
}

func validManifestURL(parsed *url.URL, allowHTTP bool) bool {
	if parsed == nil || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, "/") {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	return allowHTTP && parsed.Scheme == "http"
}
