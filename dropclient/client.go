package dropclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func New(opts Options) *Client {
	client, err := NewClient(WithOptions(opts))
	if err != nil {
		panic(err)
	}
	return client
}

func NewClient(options ...Option) (*Client, error) {
	var opts Options
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&opts); err != nil {
			return nil, err
		}
	}
	apiBaseURL := strings.TrimRight(opts.APIBaseURL, "/")
	if apiBaseURL == "" {
		apiBaseURL = DefaultAPIBaseURL
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	userAgent := opts.UserAgent
	if userAgent == "" {
		userAgent = "cf-drop-go/0.1"
	}
	workersDevDomain := opts.WorkersDevDomain
	if workersDevDomain == "" {
		workersDevDomain = DefaultWorkersDevDomain
	}
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

func WithOptions(value Options) Option {
	return func(opts *Options) error {
		*opts = value
		return nil
	}
}

func WithAPIBaseURL(value string) Option {
	return func(opts *Options) error {
		opts.APIBaseURL = value
		return nil
	}
}

func WithHTTPClient(value *http.Client) Option {
	return func(opts *Options) error {
		opts.HTTPClient = value
		return nil
	}
}

func WithUserAgent(value string) Option {
	return func(opts *Options) error {
		opts.UserAgent = value
		return nil
	}
}

func WithWorkersDevDomain(value string) Option {
	return func(opts *Options) error {
		opts.WorkersDevDomain = value
		return nil
	}
}

func WithURLBuilder(value func(scriptName, subdomain string) string) Option {
	return func(opts *Options) error {
		opts.URLBuilder = value
		return nil
	}
}

func WithScriptName(value func() (string, error)) Option {
	return func(opts *Options) error {
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

func randomScriptName() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "drop-" + hex.EncodeToString(b[:]), nil
}

func pathEscape(v string) string {
	return url.PathEscape(v)
}
