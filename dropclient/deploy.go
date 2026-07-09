package dropclient

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Deploy uploads a Source to Cloudflare Drop and returns the public preview URL.
func (c *Client) Deploy(ctx context.Context, source Source, options ...DeployOption) (*DeployResult, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context cannot be nil")
	}
	opts, err := parseDeployOptions(options)
	if err != nil {
		return nil, err
	}
	if !opts.acceptTerms {
		return nil, ErrTermsNotAccepted
	}
	manifest, assets, err := buildManifest(ctx, source)
	if err != nil {
		return nil, err
	}
	scriptName := opts.scriptName
	if scriptName == "" {
		scriptName, err = c.scriptName(ctx)
		if err != nil {
			return nil, err
		}
	}
	compatibilityDate := defaultString(opts.compatibilityDate, DefaultCompatibilityDate)

	challenge, err := c.requestChallenge(ctx)
	if err != nil {
		return nil, err
	}
	solution, err := SolveChallenge(challenge)
	if err != nil {
		return nil, err
	}
	provisioned, err := c.provision(ctx, challenge, solution, opts)
	if err != nil {
		return nil, err
	}
	creds, err := credentialsFromProvision(provisioned)
	if err != nil {
		return nil, err
	}
	session, err := c.startUploadSession(ctx, creds, scriptName, manifest)
	if err != nil {
		return nil, err
	}
	completionJWT := session.JWT
	for _, bucket := range session.Buckets {
		jwt, err := c.uploadAssetBucket(ctx, creds, session.JWT, bucket, assets)
		if err != nil {
			return nil, err
		}
		if jwt != "" {
			completionJWT = jwt
		}
	}
	if completionJWT == "" {
		return nil, fmt.Errorf("asset upload completed without completion token")
	}
	if !opts.skipPreviewAssets {
		if err := c.uploadPreviewAssets(ctx, creds, scriptName, completionJWT, manifest, assets, compatibilityDate); err != nil {
			return nil, err
		}
	}
	if err := c.deployWorker(ctx, creds, scriptName, completionJWT, compatibilityDate); err != nil {
		return nil, err
	}
	if err := c.enableSubdomain(ctx, creds, scriptName); err != nil {
		return nil, err
	}
	subdomain, err := c.getSubdomain(ctx, creds)
	if err != nil {
		return nil, err
	}
	if subdomain == "" {
		return nil, fmt.Errorf("cloudflare returned empty workers.dev subdomain")
	}
	result := &DeployResult{
		URL:           c.publicURL(scriptName, subdomain),
		ScriptName:    scriptName,
		Subdomain:     subdomain,
		ClaimURL:      creds.ClaimURL,
		ExpiresAt:     creds.ExpiresAt,
		ClaimExpires:  creds.ClaimExpiresAt,
		AccountID:     creds.AccountID,
		Manifest:      manifest,
		UploadBuckets: session.Buckets,
	}
	if opts.verifyHTTP {
		access, err := c.WaitUntilAccessible(ctx, result.URL, opts.verifyTimeout)
		if err != nil {
			return nil, err
		}
		result.Access = access
	}
	return result, nil
}

func parseDeployOptions(options []DeployOption) (deployOptions, error) {
	var opts deployOptions
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&opts); err != nil {
			return deployOptions{}, err
		}
	}
	return opts, nil
}

// AcceptTerms acknowledges Cloudflare's terms for this Drop provisioning call.
func AcceptTerms() DeployOption {
	return func(opts *deployOptions) error {
		opts.acceptTerms = true
		return nil
	}
}

// WithScriptName sets the Worker script name used in the final workers.dev URL.
func WithScriptName(value string) DeployOption {
	return func(opts *deployOptions) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("script name cannot be empty")
		}
		opts.scriptName = value
		return nil
	}
}

// WithProvisioningClient overrides the observed Drop provisioning client field.
func WithProvisioningClient(value string) DeployOption {
	return func(opts *deployOptions) error {
		opts.client = value
		return nil
	}
}

// WithProvisioningSource overrides the observed Drop provisioning source field.
func WithProvisioningSource(value string) DeployOption {
	return func(opts *deployOptions) error {
		opts.source = value
		return nil
	}
}

// WithTermsOfServiceURL overrides the terms URL sent to the provisioning endpoint.
func WithTermsOfServiceURL(value string) DeployOption {
	return func(opts *deployOptions) error {
		opts.termsOfService = value
		return nil
	}
}

// WithPrivacyPolicyURL overrides the privacy URL sent to the provisioning endpoint.
func WithPrivacyPolicyURL(value string) DeployOption {
	return func(opts *deployOptions) error {
		opts.privacyPolicy = value
		return nil
	}
}

// WithCompatibilityDate sets the Worker compatibility date.
func WithCompatibilityDate(value string) DeployOption {
	return func(opts *deployOptions) error {
		opts.compatibilityDate = value
		return nil
	}
}

// SkipPreviewAssets skips the observed Drop preview asset mirror request.
func SkipPreviewAssets() DeployOption {
	return func(opts *deployOptions) error {
		opts.skipPreviewAssets = true
		return nil
	}
}

// VerifyAccess makes Deploy poll the returned URL until it is reachable.
func VerifyAccess() DeployOption {
	return func(opts *deployOptions) error {
		opts.verifyHTTP = true
		return nil
	}
}

// WithVerifyTimeout enables access verification with a custom timeout.
func WithVerifyTimeout(value time.Duration) DeployOption {
	return func(opts *deployOptions) error {
		if value <= 0 {
			return fmt.Errorf("verify timeout must be positive")
		}
		opts.verifyHTTP = true
		opts.verifyTimeout = value
		return nil
	}
}

func credentialsFromProvision(result provisionResult) (credentials, error) {
	expiresAt, err := time.Parse(time.RFC3339, result.Account.ExpiresAt)
	if err != nil {
		return credentials{}, fmt.Errorf("parse account expiration: %w", err)
	}
	claimExpiresAt, err := time.Parse(time.RFC3339, result.Claim.ExpiresAt)
	if err != nil {
		return credentials{}, fmt.Errorf("parse claim expiration: %w", err)
	}
	if result.Account.ID == "" || result.Account.APIToken == "" || result.Claim.Token == "" {
		return credentials{}, fmt.Errorf("provisioning response missing temporary credentials")
	}
	return credentials{
		AccountID:      result.Account.ID,
		APIToken:       result.Account.APIToken,
		ClaimToken:     result.Claim.Token,
		ExpiresAt:      expiresAt,
		ClaimURL:       result.Claim.URL,
		ClaimExpiresAt: claimExpiresAt,
	}, nil
}

// WaitUntilAccessible polls a public URL until it returns a 2xx or 3xx status.
func (c *Client) WaitUntilAccessible(ctx context.Context, targetURL string, timeout time.Duration) (*AccessResult, error) {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var lastErr error
	for {
		attemptCtx, attemptCancel := context.WithTimeout(ctx, 10*time.Second)
		access, err := c.checkAccessible(attemptCtx, targetURL)
		attemptCancel()
		if err == nil && access.StatusCode >= 200 && access.StatusCode < 400 {
			return access, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("GET %s returned HTTP %d", targetURL, access.StatusCode)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("site did not become accessible before timeout: %w; last error: %v", ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func (c *Client) checkAccessible(ctx context.Context, targetURL string) (*AccessResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return &AccessResult{
		URL:         targetURL,
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}
