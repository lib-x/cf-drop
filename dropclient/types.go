package dropclient

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const (
	DefaultAPIBaseURL        = "https://api.cloudflare.com/client/v4"
	DefaultWorkersDevDomain  = "workers.dev"
	DefaultClientName        = "web"
	DefaultSource            = "drop"
	DefaultTermsOfService    = "https://www.cloudflare.com/terms/"
	DefaultPrivacyPolicy     = "https://www.cloudflare.com/privacypolicy/"
	DefaultCompatibilityDate = "2025-05-19"
)

const (
	MaxFileSize  = 25 * 1024 * 1024
	MaxTotalSize = 100 * 1024 * 1024
	MaxFileCount = 1999
)

var (
	ErrTermsNotAccepted = errors.New("cloudflare drop terms must be accepted explicitly")
	ErrNoFiles          = errors.New("no files provided")
	ErrIndexMissing     = errors.New("index.html is required")
)

type Options struct {
	APIBaseURL       string
	HTTPClient       *http.Client
	UserAgent        string
	WorkersDevDomain string
	URLBuilder       func(scriptName, subdomain string) string
	ScriptName       func() (string, error)
}

type Option func(*Options) error

type Client struct {
	apiBaseURL       string
	httpClient       *http.Client
	userAgent        string
	workersDevDomain string
	urlBuilder       func(scriptName, subdomain string) string
	scriptName       func() (string, error)
}

type DeployOptions struct {
	AcceptTerms       bool
	ScriptName        string
	Client            string
	Source            string
	TermsOfService    string
	PrivacyPolicy     string
	CompatibilityDate string
	SkipPreviewAssets bool
	SkipValidation    bool
	VerifyHTTP        bool
	VerifyTimeout     time.Duration
}

type File struct {
	Path        string
	Content     []byte
	ContentType string
}

type ManifestEntry struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

type Manifest map[string]ManifestEntry

type DeployResult struct {
	URL           string
	ScriptName    string
	Subdomain     string
	ClaimURL      string
	ExpiresAt     time.Time
	ClaimExpires  time.Time
	AccountID     string
	Manifest      Manifest
	UploadBuckets [][]string
	Access        *AccessResult
}

type AccessResult struct {
	URL         string
	StatusCode  int
	ContentType string
}

func (c *Client) DeployFiles(ctx context.Context, files []File, opts DeployOptions) (*DeployResult, error) {
	return c.deployFiles(ctx, files, opts)
}
