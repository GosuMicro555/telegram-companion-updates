package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"telegram-companion/internal/revocation"
)

func TestGitHubRevocationTransportLoadsAuthenticatedExactFile(t *testing.T) {
	token := []byte("github_pat_" + strings.Repeat("a", 82))
	envelope := []byte("signed revocation envelope")
	blobID := strings.Repeat("b", 40)
	api := &http.Client{Transport: generatorRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Scheme != "https" || request.URL.Host != "api.github.com" || request.URL.Path != generatorRevocationContentsPath || request.URL.Query().Get("ref") != generatorRevocationBranch {
			t.Fatalf("authenticated request = %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("Authorization") != "Bearer "+string(token) || request.Header.Get("Accept") != generatorGitHubAccept {
			t.Fatal("authenticated request omitted required safe headers")
		}
		body, _ := json.Marshal(map[string]any{
			"type":     "file",
			"encoding": "base64",
			"size":     len(envelope),
			"sha":      blobID,
			"content":  base64.StdEncoding.EncodeToString(envelope),
		})
		return generatorHTTPResponse(http.StatusOK, body), nil
	})}
	transport := mustGitHubRevocationTransport(t, token, api, noNetworkGeneratorClient(t))
	defer transport.Close()
	remote, err := transport.LoadAuthenticated(context.Background())
	if err != nil {
		t.Fatalf("LoadAuthenticated() error = %v", err)
	}
	if remote.BlobID != blobID || !bytes.Equal(remote.Bytes, envelope) {
		t.Fatalf("LoadAuthenticated() = %#v", remote)
	}
}

func TestGitHubRevocationTransportTreatsOnlyMissingFileAsEmpty(t *testing.T) {
	token := []byte("github_pat_" + strings.Repeat("a", 82))
	for name, status := range map[string]int{"missing": http.StatusNotFound, "server failure": http.StatusInternalServerError} {
		t.Run(name, func(t *testing.T) {
			api := &http.Client{Transport: generatorRoundTripperFunc(func(*http.Request) (*http.Response, error) {
				return generatorHTTPResponse(status, []byte(`{"message":"body must never escape"}`)), nil
			})}
			transport := mustGitHubRevocationTransport(t, token, api, noNetworkGeneratorClient(t))
			defer transport.Close()
			remote, err := transport.LoadAuthenticated(context.Background())
			if status == http.StatusNotFound {
				if err != nil || len(remote.Bytes) != 0 || remote.BlobID != "" {
					t.Fatalf("LoadAuthenticated(missing) = %#v, %v", remote, err)
				}
				return
			}
			if !errors.Is(err, ErrGitHubRevocationTransport) || strings.Contains(err.Error(), "body must never escape") {
				t.Fatalf("LoadAuthenticated(server failure) error = %q", err)
			}
		})
	}
}

func TestGitHubRevocationTransportCompareAndSwapUsesExpectedBlob(t *testing.T) {
	token := []byte("github_pat_" + strings.Repeat("c", 82))
	previousBlobID := strings.Repeat("d", 40)
	nextBlobID := strings.Repeat("e", 40)
	envelope := []byte("next signed revocation envelope")
	api := &http.Client{Transport: generatorRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPut || request.URL.Path != generatorRevocationContentsPath || request.URL.RawQuery != "" {
			t.Fatalf("CAS request = %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("If-Match") != `"`+previousBlobID+`"` || request.Header.Get("Authorization") != "Bearer "+string(token) {
			t.Fatal("CAS request omitted expected identity or credential")
		}
		var payload struct {
			Message string `json:"message"`
			Content string `json:"content"`
			Branch  string `json:"branch"`
			SHA     string `json:"sha"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode CAS request: %v", err)
		}
		if payload.Branch != generatorRevocationBranch || payload.SHA != previousBlobID || payload.Content != base64.StdEncoding.EncodeToString(envelope) || strings.Contains(payload.Message, previousBlobID) {
			t.Fatalf("CAS payload = %#v", payload)
		}
		return generatorHTTPResponse(http.StatusOK, []byte(`{"content":{"sha":"`+nextBlobID+`"}}`)), nil
	})}
	transport := mustGitHubRevocationTransport(t, token, api, noNetworkGeneratorClient(t))
	defer transport.Close()
	stored, err := transport.CompareAndSwap(context.Background(), previousBlobID, envelope)
	if err != nil {
		t.Fatalf("CompareAndSwap() error = %v", err)
	}
	if stored.BlobID != nextBlobID || !bytes.Equal(stored.Bytes, envelope) {
		t.Fatalf("CompareAndSwap() = %#v", stored)
	}
	stored.Bytes[0] ^= 0xff
	if bytes.Equal(stored.Bytes, envelope) {
		t.Fatal("CompareAndSwap() returned caller-owned envelope memory")
	}
}

func TestGitHubRevocationTransportClassifiesOnlyDefiniteCASConflicts(t *testing.T) {
	token := []byte("github_pat_" + strings.Repeat("f", 82))
	previousBlobID := strings.Repeat("1", 40)
	for _, status := range []int{http.StatusConflict, http.StatusPreconditionFailed, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			api := &http.Client{Transport: generatorRoundTripperFunc(func(*http.Request) (*http.Response, error) {
				return generatorHTTPResponse(status, []byte(`{"message":"secret response body"}`)), nil
			})}
			transport := mustGitHubRevocationTransport(t, token, api, noNetworkGeneratorClient(t))
			defer transport.Close()
			_, err := transport.CompareAndSwap(context.Background(), previousBlobID, []byte("candidate"))
			if status == http.StatusConflict || status == http.StatusPreconditionFailed {
				if !errors.Is(err, revocation.ErrCompareAndSwapConflict) {
					t.Fatalf("CompareAndSwap(%d) error = %v, want conflict", status, err)
				}
				return
			}
			if !errors.Is(err, ErrGitHubRevocationTransport) || errors.Is(err, revocation.ErrCompareAndSwapConflict) || strings.Contains(err.Error(), "secret response body") {
				t.Fatalf("CompareAndSwap(%d) error = %q", status, err)
			}
		})
	}
}

func TestGitHubRevocationTransportAnonymousReadHasNoCredential(t *testing.T) {
	token := []byte("github_pat_" + strings.Repeat("g", 82))
	blobID := strings.Repeat("2", 40)
	envelope := []byte("anonymous signed envelope")
	raw := &http.Client{Transport: generatorRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Scheme != "https" || request.URL.Host != "raw.githubusercontent.com" || request.URL.Path != generatorRevocationRawPath || request.URL.Query().Get("revocation_blob") != blobID {
			t.Fatalf("anonymous request = %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("Authorization") != "" || request.Header.Get("Cache-Control") != "no-cache" {
			t.Fatal("anonymous request leaked a credential or omitted no-cache")
		}
		return generatorHTTPResponse(http.StatusOK, envelope), nil
	})}
	transport := mustGitHubRevocationTransport(t, token, noNetworkGeneratorClient(t), raw)
	defer transport.Close()
	loaded, err := transport.LoadAnonymous(context.Background(), blobID)
	if err != nil || !bytes.Equal(loaded, envelope) {
		t.Fatalf("LoadAnonymous() = %q, %v", loaded, err)
	}
}

func TestGitHubRevocationAbsenceProbeRequiresExactAnonymousNotFound(t *testing.T) {
	for name, test := range map[string]struct {
		status  int
		absent  bool
		wantErr bool
	}{
		"not found proves absence": {status: http.StatusNotFound, absent: true},
		"existing file":            {status: http.StatusOK},
		"ambiguous server failure": {status: http.StatusInternalServerError, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			raw := &http.Client{Transport: generatorRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet || request.URL.String() != generatorRevocationRawURL {
					t.Fatalf("absence request = %s %s", request.Method, request.URL.String())
				}
				if request.Header.Get("Authorization") != "" || request.Header.Get("Cache-Control") != "no-cache" {
					t.Fatal("absence proof leaked a credential or omitted no-cache")
				}
				return generatorHTTPResponse(test.status, []byte("untrusted remote body")), nil
			})}
			probe, err := newGitHubRevocationAbsenceProbe(raw)
			if err != nil {
				t.Fatalf("newGitHubRevocationAbsenceProbe() error = %v", err)
			}
			absent, err := probe.ProveRemoteAbsent(context.Background())
			if absent != test.absent || (err != nil) != test.wantErr {
				t.Fatalf("ProveRemoteAbsent() = %t, %v", absent, err)
			}
			if err != nil && (!errors.Is(err, ErrGitHubRevocationTransport) || strings.Contains(err.Error(), "untrusted remote body")) {
				t.Fatalf("ProveRemoteAbsent() unsafe error = %q", err)
			}
		})
	}
}

func TestGitHubRevocationAbsenceProbeRejectsSameHostRedirect(t *testing.T) {
	requests := 0
	raw := &http.Client{Transport: generatorRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			if request.URL.String() != generatorRevocationRawURL {
				t.Fatalf("initial absence request = %s", request.URL.String())
			}
			response := generatorHTTPResponse(http.StatusFound, nil)
			response.Header.Set("Location", "https://raw.githubusercontent.com/GosuMicro555/telegram-companion-updates/main/redirected-revocations.tcrev")
			return response, nil
		}
		return generatorHTTPResponse(http.StatusNotFound, []byte("redirected not found")), nil
	})}
	probe, err := newGitHubRevocationAbsenceProbe(raw)
	if err != nil {
		t.Fatalf("newGitHubRevocationAbsenceProbe() error = %v", err)
	}
	absent, err := probe.ProveRemoteAbsent(context.Background())
	if absent || !errors.Is(err, ErrGitHubRevocationTransport) || requests != 1 {
		t.Fatalf("ProveRemoteAbsent(redirect) = %t, %v, requests=%d", absent, err, requests)
	}
}

func TestGitHubRevocationAbsenceProbeFailsClosedOnTransportError(t *testing.T) {
	raw := &http.Client{Transport: generatorRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network diagnostics must stay private")
	})}
	probe, err := newGitHubRevocationAbsenceProbe(raw)
	if err != nil {
		t.Fatalf("newGitHubRevocationAbsenceProbe() error = %v", err)
	}
	absent, err := probe.ProveRemoteAbsent(context.Background())
	if absent || !errors.Is(err, ErrGitHubRevocationTransport) || strings.Contains(err.Error(), "network diagnostics") {
		t.Fatalf("ProveRemoteAbsent() = %t, %q", absent, err)
	}
}

func TestGitHubRevocationInspectorAuthenticatesFixedRemoteManifestWithoutCredential(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x73}, ed25519.SeedSize))
	publicKey := privateKey.Public().(ed25519.PublicKey)
	handle, _ := revocation.DeriveHandle("license-remote-history")
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	envelope, err := revocation.Sign(revocation.Payload{
		Schema:      revocation.ManifestSchema,
		KeyID:       generatorRevocationKeyID,
		Sequence:    7,
		GeneratedAt: now.Format("2006-01-02T15:04:05Z"),
		Entries: []revocation.Entry{{
			Kind:      revocation.EntryKindLicenseIDSHA256,
			Value:     handle.String(),
			RevokedAt: now.Format("2006-01-02T15:04:05Z"),
		}},
	}, privateKey)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	raw := &http.Client{Transport: generatorRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != generatorRevocationRawURL || request.Header.Get("Authorization") != "" || request.Header.Get("Cache-Control") != "no-cache" {
			t.Fatalf("inspection request = %s headers=%v", request.URL.String(), request.Header)
		}
		return generatorHTTPResponse(http.StatusOK, []byte(envelope)), nil
	})}
	probe, err := newGitHubRevocationAbsenceProbe(raw)
	if err != nil {
		t.Fatalf("newGitHubRevocationAbsenceProbe() error = %v", err)
	}
	evidence, err := probe.InspectRemote(context.Background(), publicKey)
	if err != nil || !evidence.Present || evidence.Sequence != 7 || len(evidence.Entries) != 1 || evidence.Entries[0].Value != handle.String() || evidence.ManifestDigest == ([32]byte{}) {
		t.Fatalf("InspectRemote() = %#v, %v", evidence, err)
	}
	wrongKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x31}, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	if _, err := probe.InspectRemote(context.Background(), wrongKey); !errors.Is(err, ErrGitHubRevocationTransport) {
		t.Fatalf("InspectRemote(wrong key) error = %v", err)
	}
}

func TestGitHubRevocationInspectorTreatsOnlyExactNotFoundAsAbsent(t *testing.T) {
	publicKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x19}, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	for name, status := range map[string]int{"absent": http.StatusNotFound, "ambiguous": http.StatusInternalServerError} {
		t.Run(name, func(t *testing.T) {
			probe, _ := newGitHubRevocationAbsenceProbe(&http.Client{Transport: generatorRoundTripperFunc(func(*http.Request) (*http.Response, error) {
				return generatorHTTPResponse(status, []byte("untrusted body")), nil
			})})
			evidence, err := probe.InspectRemote(context.Background(), publicKey)
			if status == http.StatusNotFound {
				if err != nil || evidence.Present || evidence.Entries == nil {
					t.Fatalf("InspectRemote(404) = %#v, %v", evidence, err)
				}
				return
			}
			if !errors.Is(err, ErrGitHubRevocationTransport) || evidence.Present || strings.Contains(err.Error(), "untrusted body") {
				t.Fatalf("InspectRemote(ambiguous) = %#v, %q", evidence, err)
			}
		})
	}
}

func TestGitHubRevocationTransportRejectsCrossHostAndDowngradeRedirects(t *testing.T) {
	token := []byte("github_pat_" + strings.Repeat("h", 82))
	transport := mustGitHubRevocationTransport(t, token, noNetworkGeneratorClient(t), noNetworkGeneratorClient(t))
	defer transport.Close()
	for name, requestURL := range map[string]string{
		"api cross host": "https://example.com/file",
		"api downgrade":  "http://api.github.com/file",
		"raw cross host": "https://example.com/file",
		"raw downgrade":  "http://raw.githubusercontent.com/file",
	} {
		t.Run(name, func(t *testing.T) {
			request, _ := http.NewRequest(http.MethodGet, requestURL, nil)
			check := transport.apiClient.CheckRedirect
			if strings.HasPrefix(name, "raw") {
				check = transport.rawClient.CheckRedirect
			}
			if err := check(request, nil); !errors.Is(err, ErrGitHubRevocationTransport) {
				t.Fatalf("CheckRedirect(%s) error = %v", requestURL, err)
			}
		})
	}
}

func TestGitHubRevocationTransportCloseWipesCredential(t *testing.T) {
	token := []byte("github_pat_" + strings.Repeat("j", 82))
	transport := mustGitHubRevocationTransport(t, token, noNetworkGeneratorClient(t), noNetworkGeneratorClient(t))
	active := transport.token
	transport.Close()
	if !bytes.Equal(active, make([]byte, len(active))) || transport.token != nil {
		t.Fatal("Close() retained GitHub credential bytes")
	}
}

func mustGitHubRevocationTransport(t *testing.T, token []byte, api, raw *http.Client) *githubRevocationTransport {
	t.Helper()
	transport, err := newGitHubRevocationTransport(token, api, raw)
	if err != nil {
		t.Fatalf("newGitHubRevocationTransport() error = %v", err)
	}
	return transport
}

func noNetworkGeneratorClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: generatorRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected network request: %s", request.URL.String())
		return nil, errors.New("unexpected request")
	})}
}

func generatorHTTPResponse(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Header:        make(http.Header),
	}
}

type generatorRoundTripperFunc func(*http.Request) (*http.Response, error)

func (function generatorRoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
