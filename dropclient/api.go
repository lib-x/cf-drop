package dropclient

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

type cloudflareError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type envelope[T any] struct {
	Success  bool              `json:"success"`
	Result   T                 `json:"result"`
	Errors   []cloudflareError `json:"errors"`
	Messages []any             `json:"messages"`
}

type apiError struct {
	Method string
	Path   string
	Status int
	Errors []cloudflareError
	Body   string
}

func (e *apiError) Error() string {
	if len(e.Errors) > 0 {
		parts := make([]string, 0, len(e.Errors))
		for _, err := range e.Errors {
			if err.Code != 0 {
				parts = append(parts, fmt.Sprintf("%d: %s", err.Code, err.Message))
			} else {
				parts = append(parts, err.Message)
			}
		}
		return fmt.Sprintf("cloudflare api %s %s failed (%d): %s", e.Method, e.Path, e.Status, strings.Join(parts, "; "))
	}
	body := strings.TrimSpace(e.Body)
	if len(body) > 500 {
		body = body[:500] + "..."
	}
	return fmt.Sprintf("cloudflare api %s %s failed (%d): %s", e.Method, e.Path, e.Status, body)
}

type challengeResult struct {
	ChallengeToken string `json:"challengeToken"`
	Seed           string `json:"seed"`
	K              int    `json:"k"`
	G              int    `json:"g"`
	S              int    `json:"s"`
	ExpiresAt      int64  `json:"expiresAt"`
}

type provisionResult struct {
	Account struct {
		ID        string `json:"id"`
		APIToken  string `json:"apiToken"`
		ExpiresAt string `json:"expiresAt"`
	} `json:"account"`
	Claim struct {
		Token     string `json:"token"`
		URL       string `json:"url"`
		ExpiresAt string `json:"expiresAt"`
	} `json:"claim"`
}

type credentials struct {
	AccountID      string
	APIToken       string
	ClaimToken     string
	ExpiresAt      time.Time
	ClaimURL       string
	ClaimExpiresAt time.Time
}

type uploadSessionResult struct {
	JWT     string     `json:"jwt"`
	Buckets [][]string `json:"buckets"`
}

type uploadAssetsResult struct {
	JWT string `json:"jwt"`
}

type subdomainResult struct {
	Subdomain string `json:"subdomain"`
}

func (c *Client) requestChallenge(ctx context.Context) (challengeResult, error) {
	var out envelope[challengeResult]
	err := c.doJSON(ctx, http.MethodPost, "/provisioning/previews/challenge", "", map[string]any{}, &out)
	return out.Result, err
}

func (c *Client) provision(ctx context.Context, challenge challengeResult, solution powSolution, opts deployOptions) (provisionResult, error) {
	body := map[string]any{
		"client":               defaultString(opts.client, DefaultClientName),
		"source":               defaultString(opts.source, DefaultSource),
		"termsOfService":       defaultString(opts.termsOfService, DefaultTermsOfService),
		"privacyPolicy":        defaultString(opts.privacyPolicy, DefaultPrivacyPolicy),
		"acceptTermsOfService": "yes",
		"challengeToken":       challenge.ChallengeToken,
		"solution":             solution,
	}
	var out envelope[provisionResult]
	err := c.doJSON(ctx, http.MethodPost, "/provisioning/previews", "", body, &out)
	return out.Result, err
}

func (c *Client) startUploadSession(ctx context.Context, creds credentials, scriptName string, manifest Manifest) (uploadSessionResult, error) {
	path := fmt.Sprintf("/accounts/%s/workers/scripts/%s/assets-upload-session", pathEscape(creds.AccountID), pathEscape(scriptName))
	var out envelope[uploadSessionResult]
	err := c.doJSON(ctx, http.MethodPost, path, creds.APIToken, map[string]any{"manifest": manifest}, &out)
	return out.Result, err
}

func (c *Client) uploadAssetBucket(ctx context.Context, creds credentials, uploadJWT string, bucket []string, assets map[string]assetPayload) (string, error) {
	fields := make(map[string]multipartField, len(bucket))
	for _, hash := range bucket {
		asset, ok := assets[hash]
		if !ok {
			return "", fmt.Errorf("bucket references unknown asset hash %q", hash)
		}
		fields[hash] = multipartField{
			FileName:    hash,
			ContentType: asset.ContentType,
			Content:     []byte(asset.Base64),
		}
	}
	path := fmt.Sprintf("/accounts/%s/workers/assets/upload?base64=true", pathEscape(creds.AccountID))
	var out envelope[uploadAssetsResult]
	err := c.doMultipart(ctx, http.MethodPost, path, uploadJWT, "", fields, &out)
	return out.Result.JWT, err
}

func (c *Client) uploadPreviewAssets(ctx context.Context, creds credentials, scriptName, completionJWT string, manifest Manifest, assets map[string]assetPayload, compatibilityDate string) error {
	metadata, err := json.Marshal(workerMetadata(completionJWT, compatibilityDate))
	if err != nil {
		return err
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	fields := map[string]multipartField{
		"metadata": {Content: metadata},
		"manifest": {Content: manifestJSON},
	}
	for hash, asset := range assets {
		fields[hash] = multipartField{
			FileName:    hash,
			ContentType: asset.ContentType,
			Content:     []byte(asset.Base64),
		}
	}
	path := fmt.Sprintf("/provisioning/previews/accounts/%s/scripts/%s/assets?base64=true", pathEscape(creds.AccountID), pathEscape(scriptName))
	return c.doMultipart(ctx, http.MethodPost, path, "", creds.ClaimToken, fields, nil)
}

func (c *Client) deployWorker(ctx context.Context, creds credentials, scriptName, completionJWT, compatibilityDate string) error {
	metadata, err := json.Marshal(workerMetadata(completionJWT, compatibilityDate))
	if err != nil {
		return err
	}
	fields := map[string]multipartField{
		"metadata": {Content: metadata},
	}
	path := fmt.Sprintf("/accounts/%s/workers/scripts/%s", pathEscape(creds.AccountID), pathEscape(scriptName))
	return c.doMultipart(ctx, http.MethodPut, path, creds.APIToken, "", fields, nil)
}

func (c *Client) enableSubdomain(ctx context.Context, creds credentials, scriptName string) error {
	path := fmt.Sprintf("/accounts/%s/workers/scripts/%s/subdomain", pathEscape(creds.AccountID), pathEscape(scriptName))
	var out envelope[json.RawMessage]
	return c.doJSON(ctx, http.MethodPost, path, creds.APIToken, map[string]bool{"enabled": true}, &out)
}

func (c *Client) getSubdomain(ctx context.Context, creds credentials) (string, error) {
	path := fmt.Sprintf("/accounts/%s/workers/subdomain", pathEscape(creds.AccountID))
	var out envelope[subdomainResult]
	err := c.doJSON(ctx, http.MethodGet, path, creds.APIToken, nil, &out)
	return out.Result.Subdomain, err
}

func (c *Client) doJSON(ctx context.Context, method, path, bearer string, body any, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := c.newRequest(ctx, method, c.endpoint(path), &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return c.do(req, method, path, out)
}

type multipartField struct {
	FileName    string
	ContentType string
	Content     []byte
}

func (c *Client) doMultipart(ctx context.Context, method, path, bearer, claimToken string, fields map[string]multipartField, out any) error {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for name, field := range fields {
		if field.FileName == "" {
			part, err := writer.CreateFormField(name)
			if err != nil {
				return err
			}
			if _, err := part.Write(field.Content); err != nil {
				return err
			}
			continue
		}
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, escapeQuotes(name), escapeQuotes(field.FileName)))
		if field.ContentType != "" {
			header.Set("Content-Type", field.ContentType)
		}
		part, err := writer.CreatePart(header)
		if err != nil {
			return err
		}
		if _, err := part.Write(field.Content); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	req, err := c.newRequest(ctx, method, c.endpoint(path), &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if claimToken != "" {
		req.Header.Set("X-Claim-Token", claimToken)
	}
	return c.do(req, method, path, out)
}

func (c *Client) do(req *http.Request, method, path string, out any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeAPIError(method, path, resp.StatusCode, body)
	}
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode cloudflare response: %w", err)
	}
	if ok, errs := envelopeStatus(out); !ok {
		return &apiError{Method: method, Path: path, Status: resp.StatusCode, Errors: errs, Body: string(body)}
	}
	return nil
}

func decodeAPIError(method, path string, status int, body []byte) error {
	var env envelope[json.RawMessage]
	if json.Unmarshal(body, &env) == nil && (len(env.Errors) > 0 || !env.Success) {
		return &apiError{Method: method, Path: path, Status: status, Errors: env.Errors, Body: string(body)}
	}
	return &apiError{Method: method, Path: path, Status: status, Body: string(body)}
}

func envelopeStatus(out any) (bool, []cloudflareError) {
	raw, err := json.Marshal(out)
	if err != nil {
		return true, nil
	}
	var env struct {
		Success bool              `json:"success"`
		Errors  []cloudflareError `json:"errors"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return true, nil
	}
	return env.Success, env.Errors
}

func defaultString(v, fallback string) string {
	return cmp.Or(v, fallback)
}

func escapeQuotes(s string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, "\\\"").Replace(s)
}
