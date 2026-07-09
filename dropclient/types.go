package dropclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

const (
	// DefaultAPIBaseURL is the Cloudflare v4 API base used by Drop.
	DefaultAPIBaseURL        = "https://api.cloudflare.com/client/v4"
	DefaultWorkersDevDomain  = "workers.dev"
	DefaultClientName        = "web"
	DefaultSource            = "drop"
	DefaultTermsOfService    = "https://www.cloudflare.com/terms/"
	DefaultPrivacyPolicy     = "https://www.cloudflare.com/privacypolicy/"
	DefaultCompatibilityDate = "2025-05-19"
)

const (
	// MaxFileSize is the observed per-file limit enforced by the Drop frontend.
	MaxFileSize   = 25 * 1024 * 1024
	MaxZipSize    = 25 * 1024 * 1024
	MaxTotalSize  = 100 * 1024 * 1024
	MaxAssetCount = 1999
	UnknownSize   = -1
)

var (
	// ErrTermsNotAccepted is returned unless Deploy is called with AcceptTerms.
	ErrTermsNotAccepted = errors.New("cloudflare drop terms must be accepted explicitly")
	ErrNoAssets         = errors.New("no assets provided")
	ErrIndexMissing     = errors.New("index.html is required")
	ErrZipTooLarge      = errors.New("zip archive exceeds cloudflare drop size limit")
)

type clientOptions struct {
	APIBaseURL       string
	HTTPClient       *http.Client
	UserAgent        string
	WorkersDevDomain string
	URLBuilder       func(scriptName, subdomain string) string
	ScriptName       func(context.Context) (string, error)
}

// ClientOption configures a Client.
type ClientOption func(*clientOptions) error

// Client uploads static assets through the observed Cloudflare Drop flow.
type Client struct {
	apiBaseURL       string
	httpClient       *http.Client
	userAgent        string
	workersDevDomain string
	urlBuilder       func(scriptName, subdomain string) string
	scriptName       func(context.Context) (string, error)
}

// Source is a deployable collection of static assets.
type Source interface {
	WalkAssets(context.Context, func(Asset) error) error
}

// SourceFunc adapts a function to Source.
type SourceFunc func(context.Context, func(Asset) error) error

// WalkAssets calls f.
func (f SourceFunc) WalkAssets(ctx context.Context, yield func(Asset) error) error {
	return f(ctx, yield)
}

// Asset is one file-like object in a Source.
type Asset struct {
	Path        string
	Size        int64
	ContentType string
	Open        func() (io.ReadCloser, error)
}

// AssetOption configures an Asset constructor.
type AssetOption func(*assetOptions)

type assetOptions struct {
	ContentType string
	Size        int64
}

type deployOptions struct {
	acceptTerms       bool
	scriptName        string
	client            string
	source            string
	termsOfService    string
	privacyPolicy     string
	compatibilityDate string
	skipPreviewAssets bool
	verifyHTTP        bool
	verifyTimeout     time.Duration
}

// DeployOption configures one Deploy call.
type DeployOption func(*deployOptions) error

// ManifestEntry is a Cloudflare Workers assets manifest entry.
type ManifestEntry struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

// Manifest maps normalized asset paths to Cloudflare asset metadata.
type Manifest map[string]ManifestEntry

// DeployResult contains the public preview URL and temporary claim metadata.
type DeployResult struct {
	// URL is the public temporary workers.dev preview URL.
	URL string
	// ScriptName is the generated or caller-provided Worker script name.
	ScriptName string
	// Subdomain is the temporary account workers.dev subdomain.
	Subdomain string
	// ClaimURL is the Cloudflare URL used to claim and keep the deployment.
	ClaimURL string
	// ExpiresAt is the temporary account credential expiration time.
	ExpiresAt time.Time
	// ClaimExpires is the claim URL expiration time.
	ClaimExpires  time.Time
	AccountID     string
	Manifest      Manifest
	UploadBuckets [][]string
	Access        *AccessResult
}

// AccessResult is returned when Deploy verifies the public URL.
type AccessResult struct {
	URL         string
	StatusCode  int
	ContentType string
}
