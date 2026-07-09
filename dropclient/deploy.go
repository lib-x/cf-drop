package dropclient

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

func (c *Client) DeployDirectory(ctx context.Context, dir string, opts DeployOptions) (*DeployResult, error) {
	files, err := ReadDirectory(dir)
	if err != nil {
		return nil, err
	}
	return c.deployFiles(ctx, files, opts)
}

func (c *Client) deployFiles(ctx context.Context, files []File, opts DeployOptions) (*DeployResult, error) {
	if !opts.AcceptTerms {
		return nil, ErrTermsNotAccepted
	}
	if !opts.SkipValidation {
		if err := ValidateDropFiles(files); err != nil {
			return nil, err
		}
	}
	manifest, assets, err := BuildManifest(files)
	if err != nil {
		return nil, err
	}
	scriptName := opts.ScriptName
	if scriptName == "" {
		scriptName, err = c.scriptName()
		if err != nil {
			return nil, err
		}
	}
	compatibilityDate := defaultString(opts.CompatibilityDate, DefaultCompatibilityDate)

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
	if !opts.SkipPreviewAssets {
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
	if opts.VerifyHTTP {
		access, err := c.WaitUntilAccessible(ctx, result.URL, opts.VerifyTimeout)
		if err != nil {
			return nil, err
		}
		result.Access = access
	}
	return result, nil
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
