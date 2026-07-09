package dropclient

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// New creates a Cloudflare Drop client.
func New(options ...ClientOption) (*Client, error) {
	var opts clientOptions
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&opts); err != nil {
			return nil, err
		}
	}
	apiBaseURL := cmp.Or(strings.TrimRight(opts.APIBaseURL, "/"), DefaultAPIBaseURL)
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout:   60 * time.Second,
			Transport: defaultTransport(),
		}
	}
	userAgent := cmp.Or(opts.UserAgent, "cf-drop-go/0.1")
	workersDevDomain := cmp.Or(opts.WorkersDevDomain, DefaultWorkersDevDomain)
	scriptName := opts.ScriptName
	if scriptName == nil {
		scriptName = randomScriptName
	}
	return &Client{
		apiBaseURL:       apiBaseURL,
		httpClient:       httpClient,
		userAgent:        userAgent,
		workersDevDomain: workersDevDomain,
		urlBuilder:       opts.URLBuilder,
		scriptName:       scriptName,
	}, nil
}

// WithAPIBaseURL overrides the Cloudflare API base URL.
func WithAPIBaseURL(value string) ClientOption {
	return func(opts *clientOptions) error {
		opts.APIBaseURL = value
		return nil
	}
}

// WithHTTPClient uses a caller-provided HTTP client.
func WithHTTPClient(value *http.Client) ClientOption {
	return func(opts *clientOptions) error {
		if value == nil {
			return fmt.Errorf("http client cannot be nil")
		}
		opts.HTTPClient = value
		return nil
	}
}

// WithUserAgent overrides the SDK User-Agent header.
func WithUserAgent(value string) ClientOption {
	return func(opts *clientOptions) error {
		opts.UserAgent = value
		return nil
	}
}

// WithWorkersDevDomain overrides the workers.dev suffix used to build result URLs.
func WithWorkersDevDomain(value string) ClientOption {
	return func(opts *clientOptions) error {
		opts.WorkersDevDomain = value
		return nil
	}
}

// WithURLBuilder overrides result URL construction.
func WithURLBuilder(value func(scriptName, subdomain string) string) ClientOption {
	return func(opts *clientOptions) error {
		if value == nil {
			return fmt.Errorf("url builder cannot be nil")
		}
		opts.URLBuilder = value
		return nil
	}
}

// WithScriptNameGenerator overrides random Drop script name generation.
func WithScriptNameGenerator(value func(context.Context) (string, error)) ClientOption {
	return func(opts *clientOptions) error {
		if value == nil {
			return fmt.Errorf("script name generator cannot be nil")
		}
		opts.ScriptName = value
		return nil
	}
}

func (c *Client) endpoint(path string) string {
	return c.apiBaseURL + path
}

func (c *Client) publicURL(scriptName, subdomain string) string {
	if c.urlBuilder != nil {
		return c.urlBuilder(scriptName, subdomain)
	}
	return fmt.Sprintf("https://%s.%s.%s", scriptName, subdomain, c.workersDevDomain)
}

func (c *Client) newRequest(ctx context.Context, method, rawURL string, body *bytes.Buffer) (*http.Request, error) {
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body.Bytes())
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	return req, nil
}

func randomScriptName(context.Context) (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "drop-" + hex.EncodeToString(b[:]), nil
}

func pathEscape(v string) string {
	return url.PathEscape(v)
}
