package dropclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDeployUploadsReturnsURLAndVerifiesAccess(t *testing.T) {
	const (
		accountID        = "account-123"
		apiToken         = "temporary-api-token"
		claimToken       = "claim-token"
		uploadJWT        = "upload-jwt"
		completionJWT    = "completion-jwt"
		scriptName       = "drop-test"
		accountSubdomain = "preview-account"
	)
	siteMarker := "mock deployed site"
	siteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html>"+siteMarker)
	}))
	defer siteServer.Close()

	source := FromAssets(
		testAsset("index.html", "<!doctype html>"+siteMarker, "text/html"),
		testAsset("assets/app.js", "console.log('ok')", "application/javascript"),
	)
	manifest, assets, err := buildManifest(t.Context(), source)
	if err != nil {
		t.Fatalf("buildManifest returned error: %v", err)
	}

	var state mockAPIState
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.handle(t, w, r, mockAPIConfig{
			AccountID:        accountID,
			APIToken:         apiToken,
			ClaimToken:       claimToken,
			UploadJWT:        uploadJWT,
			CompletionJWT:    completionJWT,
			Subdomain:        accountSubdomain,
			ExpectedManifest: manifest,
			ExpectedAssets:   assets,
		})
	}))
	defer apiServer.Close()

	client, err := New(
		WithAPIBaseURL(apiServer.URL),
		WithHTTPClient(apiServer.Client()),
		WithScriptNameGenerator(func(context.Context) (string, error) {
			return scriptName, nil
		}),
		WithURLBuilder(func(_, _ string) string {
			return siteServer.URL
		}),
	)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	result, err := client.Deploy(t.Context(), source, AcceptTerms(), VerifyAccess())
	if err != nil {
		t.Fatalf("Deploy returned error: %v", err)
	}
	if result.URL != siteServer.URL {
		t.Fatalf("URL = %q, want %q", result.URL, siteServer.URL)
	}
	if result.ScriptName != scriptName {
		t.Fatalf("ScriptName = %q, want %q", result.ScriptName, scriptName)
	}
	if result.Subdomain != accountSubdomain {
		t.Fatalf("Subdomain = %q, want %q", result.Subdomain, accountSubdomain)
	}
	if result.Access == nil || result.Access.StatusCode != http.StatusOK {
		t.Fatalf("Access = %#v, want HTTP 200", result.Access)
	}
	if result.ClaimURL != "https://dash.cloudflare.com/claim/mock" {
		t.Fatalf("ClaimURL = %q, want mock claim URL", result.ClaimURL)
	}
	if result.ClaimExpires.IsZero() {
		t.Fatal("ClaimExpires is zero")
	}
	state.assertCompleted(t)
}

func TestDeployRequiresTermsAcceptance(t *testing.T) {
	client, err := New()
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Deploy(t.Context(), FromBytes("index.html", []byte("ok")))
	if err == nil {
		t.Fatal("Deploy returned nil error")
	}
	if !errors.Is(err, ErrTermsNotAccepted) {
		t.Fatalf("error = %v, want %v", err, ErrTermsNotAccepted)
	}
}

func TestIntegrationDeploy(t *testing.T) {
	if os.Getenv("CLOUDFLARE_DROP_INTEGRATION") != "1" {
		t.Skip("set CLOUDFLARE_DROP_INTEGRATION=1 and CLOUDFLARE_DROP_ACCEPT_TERMS=1 to run live Cloudflare Drop upload")
	}
	if os.Getenv("CLOUDFLARE_DROP_ACCEPT_TERMS") != "1" {
		t.Skip("set CLOUDFLARE_DROP_ACCEPT_TERMS=1 to acknowledge Cloudflare terms for live Drop upload")
	}

	marker := "cloudflare-drop-go-integration-" + time.Now().UTC().Format("20060102150405")
	client, err := clientForLiveIntegration()
	if err != nil {
		t.Fatalf("build live client: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()

	source := FromBytes(
		"index.html",
		[]byte("<!doctype html><title>Drop SDK</title><main>"+marker+"</main>"),
		WithContentType("text/html; charset=utf-8"),
	)
	result, err := client.Deploy(ctx, source, AcceptTerms(), WithVerifyTimeout(2*time.Minute))
	if err != nil {
		t.Fatalf("live Deploy returned error: %v", err)
	}
	body := fetchBodyWithRetry(t, ctx, client.httpClient, result.URL)
	if !strings.Contains(body, marker) {
		t.Fatalf("deployed page body does not contain marker %q; body prefix: %q", marker, truncate(body, 300))
	}
	if result.ClaimURL == "" {
		t.Fatal("claim URL is empty")
	}
	if result.ClaimExpires.IsZero() {
		t.Fatal("claim expiration is zero")
	}
	t.Logf("deployed URL: %s", result.URL)
}

type mockAPIConfig struct {
	AccountID        string
	APIToken         string
	ClaimToken       string
	UploadJWT        string
	CompletionJWT    string
	Subdomain        string
	ExpectedManifest Manifest
	ExpectedAssets   map[string]assetPayload
}

type mockAPIState struct {
	mu              sync.Mutex
	challengeSeen   bool
	provisionSeen   bool
	sessionSeen     bool
	assetUploadSeen bool
	previewSeen     bool
	deploySeen      bool
	subdomainSet    bool
	subdomainRead   bool
}

func (s *mockAPIState) handle(t *testing.T, w http.ResponseWriter, r *http.Request, cfg mockAPIConfig) {
	t.Helper()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/provisioning/previews/challenge":
		s.mu.Lock()
		s.challengeSeen = true
		s.mu.Unlock()
		seed := make([]byte, sha256.Size)
		writeJSON(t, w, http.StatusOK, envelope[challengeResult]{
			Success: true,
			Result: challengeResult{
				ChallengeToken: "challenge-token",
				Seed:           base64.RawURLEncoding.EncodeToString(seed),
				K:              1,
				G:              1,
				S:              16,
				ExpiresAt:      time.Now().Add(time.Minute).Unix(),
			},
		})
	case r.Method == http.MethodPost && r.URL.Path == "/provisioning/previews":
		var req struct {
			AcceptTermsOfService string      `json:"acceptTermsOfService"`
			ChallengeToken       string      `json:"challengeToken"`
			Solution             powSolution `json:"solution"`
		}
		readJSON(t, r, &req)
		if req.AcceptTermsOfService != "yes" || req.ChallengeToken == "" || req.Solution.Checkpoints == "" {
			http.Error(w, "bad provision request", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.provisionSeen = true
		s.mu.Unlock()
		writeJSON(t, w, http.StatusOK, envelope[provisionResult]{
			Success: true,
			Result:  provisionResponse(cfg),
		})
	case r.Method == http.MethodPost && r.URL.Path == "/accounts/"+cfg.AccountID+"/workers/scripts/drop-test/assets-upload-session":
		requireBearer(t, w, r, cfg.APIToken)
		var req struct {
			Manifest Manifest `json:"manifest"`
		}
		readJSON(t, r, &req)
		if len(req.Manifest) != len(cfg.ExpectedManifest) {
			t.Errorf("manifest length = %d, want %d", len(req.Manifest), len(cfg.ExpectedManifest))
		}
		hashes := manifestHashes(req.Manifest)
		s.mu.Lock()
		s.sessionSeen = true
		s.mu.Unlock()
		writeJSON(t, w, http.StatusOK, envelope[uploadSessionResult]{
			Success: true,
			Result: uploadSessionResult{
				JWT:     cfg.UploadJWT,
				Buckets: [][]string{hashes},
			},
		})
	case r.Method == http.MethodPost && r.URL.Path == "/accounts/"+cfg.AccountID+"/workers/assets/upload":
		requireBearer(t, w, r, cfg.UploadJWT)
		if r.URL.Query().Get("base64") != "true" {
			http.Error(w, "missing base64=true", http.StatusBadRequest)
			return
		}
		assertAssetParts(t, r, cfg.ExpectedAssets)
		s.mu.Lock()
		s.assetUploadSeen = true
		s.mu.Unlock()
		writeJSON(t, w, http.StatusCreated, envelope[uploadAssetsResult]{
			Success: true,
			Result:  uploadAssetsResult{JWT: cfg.CompletionJWT},
		})
	case r.Method == http.MethodPost && r.URL.Path == "/provisioning/previews/accounts/"+cfg.AccountID+"/scripts/drop-test/assets":
		if r.Header.Get("X-Claim-Token") != cfg.ClaimToken {
			http.Error(w, "bad claim token", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("base64") != "true" {
			http.Error(w, "missing base64=true", http.StatusBadRequest)
			return
		}
		form := parseMultipart(t, r)
		if !strings.Contains(form.Value["metadata"][0], cfg.CompletionJWT) {
			t.Errorf("preview metadata does not include completion jwt")
		}
		if len(form.Value["manifest"]) != 1 {
			t.Errorf("preview manifest field missing")
		}
		s.mu.Lock()
		s.previewSeen = true
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPut && r.URL.Path == "/accounts/"+cfg.AccountID+"/workers/scripts/drop-test":
		requireBearer(t, w, r, cfg.APIToken)
		form := parseMultipart(t, r)
		if !strings.Contains(form.Value["metadata"][0], cfg.CompletionJWT) {
			t.Errorf("deploy metadata does not include completion jwt")
		}
		s.mu.Lock()
		s.deploySeen = true
		s.mu.Unlock()
		writeJSON(t, w, http.StatusOK, envelope[json.RawMessage]{Success: true})
	case r.Method == http.MethodPost && r.URL.Path == "/accounts/"+cfg.AccountID+"/workers/scripts/drop-test/subdomain":
		requireBearer(t, w, r, cfg.APIToken)
		s.mu.Lock()
		s.subdomainSet = true
		s.mu.Unlock()
		writeJSON(t, w, http.StatusOK, envelope[json.RawMessage]{Success: true})
	case r.Method == http.MethodGet && r.URL.Path == "/accounts/"+cfg.AccountID+"/workers/subdomain":
		requireBearer(t, w, r, cfg.APIToken)
		s.mu.Lock()
		s.subdomainRead = true
		s.mu.Unlock()
		writeJSON(t, w, http.StatusOK, envelope[subdomainResult]{
			Success: true,
			Result:  subdomainResult{Subdomain: cfg.Subdomain},
		})
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.String(), http.StatusNotFound)
	}
}

func (s *mockAPIState) assertCompleted(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.challengeSeen || !s.provisionSeen || !s.sessionSeen || !s.assetUploadSeen || !s.previewSeen || !s.deploySeen || !s.subdomainSet || !s.subdomainRead {
		t.Fatalf(
			"incomplete mock flow: challenge=%t provision=%t session=%t assetUpload=%t preview=%t deploy=%t subdomainSet=%t subdomainRead=%t",
			s.challengeSeen,
			s.provisionSeen,
			s.sessionSeen,
			s.assetUploadSeen,
			s.previewSeen,
			s.deploySeen,
			s.subdomainSet,
			s.subdomainRead,
		)
	}
}

func provisionResponse(cfg mockAPIConfig) provisionResult {
	var result provisionResult
	result.Account.ID = cfg.AccountID
	result.Account.APIToken = cfg.APIToken
	result.Account.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	result.Claim.Token = cfg.ClaimToken
	result.Claim.URL = "https://dash.cloudflare.com/claim/mock"
	result.Claim.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	return result
}

func manifestHashes(manifest Manifest) []string {
	set := map[string]bool{}
	for _, entry := range manifest {
		set[entry.Hash] = true
	}
	return slices.Sorted(maps.Keys(set))
}

func assertAssetParts(t *testing.T, r *http.Request, expected map[string]assetPayload) {
	t.Helper()
	form := parseMultipart(t, r)
	for hash, asset := range expected {
		files := form.File[hash]
		if len(files) != 1 {
			t.Fatalf("file part for hash %s count = %d, want 1", hash, len(files))
		}
		got := readMultipartFile(t, files[0])
		if string(got) != asset.Base64 {
			t.Fatalf("part %s body = %q, want %q", hash, got, asset.Base64)
		}
	}
}

func parseMultipart(t *testing.T, r *http.Request) *multipart.Form {
	t.Helper()
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		t.Fatalf("ParseMultipartForm returned error: %v", err)
	}
	return r.MultipartForm
}

func readMultipartFile(t *testing.T, header *multipart.FileHeader) []byte {
	t.Helper()
	file, err := header.Open()
	if err != nil {
		t.Fatalf("open multipart file: %v", err)
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read multipart file: %v", err)
	}
	return content
}

func requireBearer(t *testing.T, w http.ResponseWriter, r *http.Request, token string) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer "+token {
		http.Error(w, "bad authorization", http.StatusUnauthorized)
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
}

func readJSON(t *testing.T, r *http.Request, target any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		t.Fatalf("decode request JSON: %v", err)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatalf("encode response JSON: %v", err)
	}
}

func writeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func testAsset(path, content, contentType string) Asset {
	data := []byte(content)
	return Asset{
		Path:        path,
		Size:        int64(len(data)),
		ContentType: contentType,
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(data)), nil
		},
	}
}

func fetchBodyWithRetry(t *testing.T, ctx context.Context, client *http.Client, targetURL string) string {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(90 * time.Second)
	}
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if err != nil {
			t.Fatalf("build GET request: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(2 * time.Second)
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(2 * time.Second)
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			return string(body)
		}
		lastErr = errorsForStatus(resp.StatusCode, body)
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("GET %s did not succeed before deadline: %v", targetURL, lastErr)
	return ""
}

func errorsForStatus(status int, body []byte) error {
	return &apiError{Method: http.MethodGet, Path: "site", Status: status, Body: string(body)}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func clientForLiveIntegration() (*Client, error) {
	proxyURL := os.Getenv("CLOUDFLARE_DROP_PROXY")
	if proxyURL == "" {
		proxyURL = os.Getenv("HTTPS_PROXY")
	}
	if proxyURL == "" {
		proxyURL = os.Getenv("ALL_PROXY")
	}
	if proxyURL == "" {
		return New()
	}
	return New(WithProxyURL(proxyURL))
}

func TestNewSupportsFunctionalOptionsAndProxy(t *testing.T) {
	proxyServer := newMinimalHTTPProxy(t)
	defer proxyServer.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "through proxy")
	}))
	defer target.Close()

	client, err := New(
		WithProxyURL(proxyServer.URL()),
		WithAPIBaseURL("http://api.example.test"),
	)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := client.httpClient.Get(target.URL)
	if err != nil {
		t.Fatalf("proxied GET returned error: %v", err)
	}
	_ = resp.Body.Close()
	if got := proxyServer.RequestCount(); got == 0 {
		t.Fatal("proxy did not receive any requests")
	}
}

type minimalHTTPProxy struct {
	server *httptest.Server
	count  int
	mu     sync.Mutex
}

func newMinimalHTTPProxy(t *testing.T) *minimalHTTPProxy {
	t.Helper()
	proxy := &minimalHTTPProxy{}
	proxy.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxy.mu.Lock()
		proxy.count++
		proxy.mu.Unlock()
		if r.URL.Scheme == "" || r.URL.Host == "" {
			http.Error(w, "expected absolute-form proxy request", http.StatusBadRequest)
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		req.Header = r.Header.Clone()
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	return proxy
}

func (p *minimalHTTPProxy) Close() {
	p.server.Close()
}

func (p *minimalHTTPProxy) RequestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.count
}

func (p *minimalHTTPProxy) URL() string {
	return p.server.URL
}

func TestWithSOCKS5ProxyRejectsUnavailableProxyAtRequestTime(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	client, err := New(WithSOCKS5Proxy(addr))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.httpClient.Get("https://example.com")
	if err == nil {
		t.Fatal("GET through unavailable SOCKS5 proxy returned nil error")
	}
	if !strings.Contains(err.Error(), "proxyconnect") && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want proxy connection failure", err)
	}
}
