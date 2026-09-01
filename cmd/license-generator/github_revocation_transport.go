package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"telegram-companion/internal/revocation"
)

const (
	generatorRevocationOwner        = "GosuMicro555"
	generatorRevocationRepository   = "telegram-companion-updates"
	generatorRevocationBranch       = "main"
	generatorRevocationFile         = "revocations.tcrev"
	generatorRevocationContentsPath = "/repos/" + generatorRevocationOwner + "/" + generatorRevocationRepository + "/contents/" + generatorRevocationFile
	generatorRevocationRawPath      = "/" + generatorRevocationOwner + "/" + generatorRevocationRepository + "/" + generatorRevocationBranch + "/" + generatorRevocationFile
	generatorRevocationAPIURL       = "https://api.github.com" + generatorRevocationContentsPath
	generatorRevocationRawURL       = "https://raw.githubusercontent.com" + generatorRevocationRawPath
	generatorGitHubAccept           = "application/vnd.github+json"
	generatorGitHubAPIVersion       = "2022-11-28"
	generatorGitHubTimeout          = 5 * time.Second
	maxGeneratorGitHubResponseSize  = int64(revocation.MaxEnvelopeBytes*2) + 64<<10
)

const ErrGitHubRevocationTransport GeneratorError = "revocation transport failed"

type githubRevocationTransport struct {
	mu        sync.Mutex
	token     []byte
	apiClient *http.Client
	rawClient *http.Client
	closed    bool
}

type githubRevocationAbsenceProbe struct {
	rawClient *http.Client
}

func newProductionGitHubRevocationAbsenceProbe() (*githubRevocationAbsenceProbe, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, ErrGitHubRevocationTransport
	}
	roundTripper := base.Clone()
	roundTripper.Proxy = nil
	return newGitHubRevocationAbsenceProbe(&http.Client{Transport: roundTripper})
}

func newGitHubRevocationAbsenceProbe(source *http.Client) (*githubRevocationAbsenceProbe, error) {
	if source == nil || source.Transport == nil {
		return nil, ErrGitHubRevocationTransport
	}
	return &githubRevocationAbsenceProbe{
		rawClient: noRedirectGeneratorGitHubClient(source),
	}, nil
}

func noRedirectGeneratorGitHubClient(source *http.Client) *http.Client {
	client := *source
	client.Timeout = generatorGitHubTimeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return ErrGitHubRevocationTransport
	}
	return &client
}

// ProveRemoteAbsent succeeds only for an anonymous 404 from the fixed public URL.
// Every transport, redirect, or status ambiguity fails closed.
func (probe *githubRevocationAbsenceProbe) ProveRemoteAbsent(ctx context.Context) (bool, error) {
	if probe == nil || probe.rawClient == nil || ctx == nil {
		return false, ErrGitHubRevocationTransport
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, generatorRevocationRawURL, nil)
	if err != nil {
		return false, ErrGitHubRevocationTransport
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("Cache-Control", "no-cache")
	response, err := probe.rawClient.Do(request)
	if err != nil {
		return false, ErrGitHubRevocationTransport
	}
	defer response.Body.Close()
	drainGeneratorGitHubResponse(response.Body)
	switch {
	case response.StatusCode == http.StatusNotFound:
		return true, nil
	case response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices:
		return false, nil
	default:
		return false, ErrGitHubRevocationTransport
	}
}

// InspectRemote authenticates the fixed public manifest with the candidate
// backup key. Only an exact anonymous 404 is accepted as evidence of absence.
func (probe *githubRevocationAbsenceProbe) InspectRemote(ctx context.Context, publicKey ed25519.PublicKey) (revocationRestoreEvidence, error) {
	if probe == nil || probe.rawClient == nil || ctx == nil || len(publicKey) != ed25519.PublicKeySize {
		return revocationRestoreEvidence{}, ErrGitHubRevocationTransport
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, generatorRevocationRawURL, nil)
	if err != nil {
		return revocationRestoreEvidence{}, ErrGitHubRevocationTransport
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("Cache-Control", "no-cache")
	response, err := probe.rawClient.Do(request)
	if err != nil {
		return revocationRestoreEvidence{}, ErrGitHubRevocationTransport
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		drainGeneratorGitHubResponse(response.Body)
		return revocationRestoreEvidence{Entries: []revocation.Entry{}}, nil
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		drainGeneratorGitHubResponse(response.Body)
		return revocationRestoreEvidence{}, ErrGitHubRevocationTransport
	}
	body, err := readGeneratorGitHubResponse(response, int64(revocation.MaxEnvelopeBytes))
	if err != nil {
		return revocationRestoreEvidence{}, ErrGitHubRevocationTransport
	}
	verified, err := revocation.Verify(string(body), generatorRevocationKeyID, publicKey)
	if err != nil {
		return revocationRestoreEvidence{}, ErrGitHubRevocationTransport
	}
	payload := verified.PayloadCopy()
	return revocationRestoreEvidence{
		Present:        true,
		Sequence:       payload.Sequence,
		ManifestDigest: verified.Digest(),
		Entries:        append([]revocation.Entry(nil), payload.Entries...),
	}, nil
}

func newProductionGitHubRevocationTransport(token []byte) (*githubRevocationTransport, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, ErrGitHubRevocationTransport
	}
	apiRoundTripper := base.Clone()
	apiRoundTripper.Proxy = nil
	rawRoundTripper := base.Clone()
	rawRoundTripper.Proxy = nil
	return newGitHubRevocationTransport(
		token,
		&http.Client{Transport: apiRoundTripper},
		&http.Client{Transport: rawRoundTripper},
	)
}

func newGitHubRevocationTransport(token []byte, apiSource, rawSource *http.Client) (*githubRevocationTransport, error) {
	if !validGeneratorRevocationCredential(token) || apiSource == nil || apiSource.Transport == nil || rawSource == nil || rawSource.Transport == nil {
		return nil, ErrGitHubRevocationTransport
	}
	apiClient := restrictedGeneratorGitHubClient(apiSource, "api.github.com")
	rawClient := restrictedGeneratorGitHubClient(rawSource, "raw.githubusercontent.com")
	return &githubRevocationTransport{
		token:     append([]byte(nil), token...),
		apiClient: apiClient,
		rawClient: rawClient,
	}, nil
}

func restrictedGeneratorGitHubClient(source *http.Client, allowedHost string) *http.Client {
	client := *source
	client.Timeout = generatorGitHubTimeout
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request == nil || request.URL == nil || len(via) >= 10 || request.URL.Scheme != "https" || request.URL.Host != allowedHost {
			return ErrGitHubRevocationTransport
		}
		return nil
	}
	return &client
}

func (transport *githubRevocationTransport) LoadAuthenticated(ctx context.Context) (revocation.RemoteFile, error) {
	authorization, ok := transport.authorizationHeader()
	if !ok || ctx == nil {
		return revocation.RemoteFile{}, ErrGitHubRevocationTransport
	}
	endpoint := generatorRevocationAPIURL + "?ref=" + url.QueryEscape(generatorRevocationBranch)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return revocation.RemoteFile{}, ErrGitHubRevocationTransport
	}
	setGeneratorGitHubHeaders(request, authorization)
	response, err := transport.apiClient.Do(request)
	if err != nil {
		return revocation.RemoteFile{}, ErrGitHubRevocationTransport
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		drainGeneratorGitHubResponse(response.Body)
		return revocation.RemoteFile{}, nil
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		drainGeneratorGitHubResponse(response.Body)
		return revocation.RemoteFile{}, ErrGitHubRevocationTransport
	}
	body, err := readGeneratorGitHubResponse(response, maxGeneratorGitHubResponseSize)
	if err != nil {
		return revocation.RemoteFile{}, ErrGitHubRevocationTransport
	}
	remote, ok := decodeGeneratorGitHubContents(body)
	if !ok {
		return revocation.RemoteFile{}, ErrGitHubRevocationTransport
	}
	return remote, nil
}

func (transport *githubRevocationTransport) CompareAndSwap(ctx context.Context, expectedBlobID string, envelope []byte) (revocation.RemoteFile, error) {
	authorization, ok := transport.authorizationHeader()
	if !ok || ctx == nil || len(envelope) == 0 || len(envelope) > revocation.MaxEnvelopeBytes || (expectedBlobID != "" && !validGeneratorGitHubBlobID(expectedBlobID)) {
		return revocation.RemoteFile{}, ErrGitHubRevocationTransport
	}
	payload := struct {
		Message string `json:"message"`
		Content string `json:"content"`
		Branch  string `json:"branch"`
		SHA     string `json:"sha,omitempty"`
	}{
		Message: "Publish Telegram Companion license revocations",
		Content: base64.StdEncoding.EncodeToString(envelope),
		Branch:  generatorRevocationBranch,
		SHA:     expectedBlobID,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return revocation.RemoteFile{}, ErrGitHubRevocationTransport
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, generatorRevocationAPIURL, bytes.NewReader(encoded))
	if err != nil {
		return revocation.RemoteFile{}, ErrGitHubRevocationTransport
	}
	setGeneratorGitHubHeaders(request, authorization)
	request.Header.Set("Content-Type", "application/json")
	if expectedBlobID != "" {
		request.Header.Set("If-Match", `"`+expectedBlobID+`"`)
	}
	response, err := transport.apiClient.Do(request)
	if err != nil {
		return revocation.RemoteFile{}, ErrGitHubRevocationTransport
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict || response.StatusCode == http.StatusPreconditionFailed {
		drainGeneratorGitHubResponse(response.Body)
		return revocation.RemoteFile{}, revocation.ErrCompareAndSwapConflict
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		drainGeneratorGitHubResponse(response.Body)
		return revocation.RemoteFile{}, ErrGitHubRevocationTransport
	}
	body, err := readGeneratorGitHubResponse(response, maxGeneratorGitHubResponseSize)
	if err != nil {
		return revocation.RemoteFile{}, ErrGitHubRevocationTransport
	}
	var result struct {
		Content struct {
			SHA string `json:"sha"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &result); err != nil || !validGeneratorGitHubBlobID(result.Content.SHA) {
		return revocation.RemoteFile{}, ErrGitHubRevocationTransport
	}
	return revocation.RemoteFile{Bytes: append([]byte(nil), envelope...), BlobID: result.Content.SHA}, nil
}

func (transport *githubRevocationTransport) LoadAnonymous(ctx context.Context, blobID string) ([]byte, error) {
	if transport == nil || ctx == nil || !validGeneratorGitHubBlobID(blobID) || !transport.isOpen() {
		return nil, ErrGitHubRevocationTransport
	}
	endpoint := generatorRevocationRawURL + "?revocation_blob=" + url.QueryEscape(blobID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ErrGitHubRevocationTransport
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("Cache-Control", "no-cache")
	response, err := transport.rawClient.Do(request)
	if err != nil {
		return nil, ErrGitHubRevocationTransport
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		drainGeneratorGitHubResponse(response.Body)
		return nil, ErrGitHubRevocationTransport
	}
	body, err := readGeneratorGitHubResponse(response, int64(revocation.MaxEnvelopeBytes))
	if err != nil || len(body) == 0 {
		return nil, ErrGitHubRevocationTransport
	}
	return body, nil
}

func (transport *githubRevocationTransport) Close() {
	if transport == nil {
		return
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	wipeBytes(transport.token)
	transport.token = nil
	transport.closed = true
}

func (transport *githubRevocationTransport) authorizationHeader() (string, bool) {
	if transport == nil {
		return "", false
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.closed || !validGeneratorRevocationCredential(transport.token) {
		return "", false
	}
	return "Bearer " + string(transport.token), true
}

func (transport *githubRevocationTransport) isOpen() bool {
	if transport == nil {
		return false
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return !transport.closed && validGeneratorRevocationCredential(transport.token)
}

func setGeneratorGitHubHeaders(request *http.Request, authorization string) {
	request.Header.Set("Accept", generatorGitHubAccept)
	request.Header.Set("Authorization", authorization)
	request.Header.Set("X-GitHub-Api-Version", generatorGitHubAPIVersion)
	request.Header.Set("Cache-Control", "no-cache")
}

func readGeneratorGitHubResponse(response *http.Response, limit int64) ([]byte, error) {
	if response == nil || response.Body == nil || limit < 1 || response.ContentLength > limit {
		return nil, ErrGitHubRevocationTransport
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(body) == 0 || int64(len(body)) > limit {
		return nil, ErrGitHubRevocationTransport
	}
	return body, nil
}

func drainGeneratorGitHubResponse(body io.Reader) {
	if body != nil {
		_, _ = io.CopyN(io.Discard, body, 4096)
	}
}

func decodeGeneratorGitHubContents(body []byte) (revocation.RemoteFile, bool) {
	var document struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Size     int    `json:"size"`
		SHA      string `json:"sha"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(body, &document); err != nil || document.Type != "file" || document.Encoding != "base64" || document.Size < 1 || document.Size > revocation.MaxEnvelopeBytes || !validGeneratorGitHubBlobID(document.SHA) {
		return revocation.RemoteFile{}, false
	}
	encoded := strings.ReplaceAll(document.Content, "\n", "")
	if encoded == "" || strings.ContainsAny(encoded, "\r\t ") {
		return revocation.RemoteFile{}, false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || base64.StdEncoding.EncodeToString(decoded) != encoded || len(decoded) != document.Size {
		return revocation.RemoteFile{}, false
	}
	return revocation.RemoteFile{Bytes: decoded, BlobID: document.SHA}, true
}

func validGeneratorGitHubBlobID(value string) bool {
	return len(value) == 40 && isLowerHex(value)
}

var _ revocation.PublishTransport = (*githubRevocationTransport)(nil)
