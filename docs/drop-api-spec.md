# Cloudflare Drop API Spec

This spec was reconstructed from the public Cloudflare Drop page at
`https://www.cloudflare.com/drop/`, its frontend bundle, and Cloudflare's
official Workers Static Assets direct-upload documentation.

Status: observed/internal for provisioning preview endpoints, documented for
Workers assets direct-upload endpoints.

## Scope

Cloudflare Drop deploys a static HTML/CSS/JS site to a temporary Workers
preview. The frontend flow is:

1. Validate files locally.
2. Request and solve a proof-of-work challenge.
3. Provision a temporary account token and claim token.
4. Build an assets manifest.
5. Start a Workers assets upload session.
6. Upload required asset buckets.
7. Mirror assets to the preview provisioning endpoint.
8. Create/update an assets-only Worker script.
9. Enable the workers.dev subdomain and return the public URL.

## Client-Side Limits

The Drop frontend applies these checks before upload:

| Rule | Value |
| --- | --- |
| Single file max | 25 MiB |
| ZIP max | 25 MiB |
| Folder file count | Less than 2000 files |
| Total upload size | Less than 100 MiB |
| Required entry | `index.html` at root or one top-level directory below root |

The Go SDK mirrors these as validation helpers. Server-side limits may differ.

## URL Bases

| Name | Default |
| --- | --- |
| Cloudflare API base | `https://api.cloudflare.com/client/v4` |
| Public result URL | `https://{scriptName}.{subdomain}.workers.dev` |

The frontend supports build-time overrides, but production Drop uses the
Cloudflare API base above.

## Authentication Model

No user Cloudflare account token is required. The frontend gets temporary
credentials through `POST /provisioning/previews` after solving the challenge.

Temporary credentials observed:

```json
{
  "account": {
    "id": "temporary account id",
    "apiToken": "temporary bearer token",
    "expiresAt": "RFC3339 timestamp"
  },
  "claim": {
    "token": "preview claim token",
    "url": "claim URL",
    "expiresAt": "RFC3339 timestamp"
  }
}
```

SDKs must treat `apiToken` and `claim.token` as secrets. Do not log them.

## Proof Of Work

Endpoint:

```http
POST /provisioning/previews/challenge
Content-Type: application/json

{}
```

Response body:

```json
{
  "success": true,
  "result": {
    "challengeToken": "jwt-like token",
    "seed": "base64url-encoded 32-byte seed",
    "k": 1000,
    "g": 2000,
    "s": 16,
    "expiresAt": 1783585975
  },
  "errors": [],
  "messages": []
}
```

Observed solver:

1. Base64url-decode `seed`; it must decode to 32 bytes.
2. Compute `checkpoint[0] = SHA256(seed)`.
3. For `j = 0..k-1`, compute SHA256 over the previous digest `g` times and
   append the resulting digest.
4. Concatenate all 32-byte checkpoints and standard-base64 encode them.
5. Send `{ "checkpoints": "<base64>" }` as `solution`.

The frontend rejects challenges where `k * g > 64000000`.

## Provision Temporary Preview

Endpoint:

```http
POST /provisioning/previews
Content-Type: application/json
```

Request body:

```json
{
  "client": "web",
  "source": "drop",
  "termsOfService": "https://www.cloudflare.com/terms/",
  "privacyPolicy": "https://www.cloudflare.com/privacypolicy/",
  "acceptTermsOfService": "yes",
  "challengeToken": "...",
  "solution": {
    "checkpoints": "..."
  }
}
```

This endpoint is observed/internal. It returns the temporary account and claim
credentials used by the remaining calls.

## Asset Manifest

Manifest shape:

```json
{
  "/index.html": {
    "hash": "32 lowercase hex characters",
    "size": 128
  }
}
```

Observed hash algorithm:

```text
hash = first 32 hex chars of SHA256(base64(fileBytes) + fileExtensionWithoutDot)
```

The extension is the substring after the final `.` in the file name. Files with
identical hashes are uploaded only once.

## Start Workers Assets Upload Session

Documented Cloudflare Workers Static Assets API.

```http
POST /accounts/{accountId}/workers/scripts/{scriptName}/assets-upload-session
Authorization: Bearer {temporaryApiToken}
Content-Type: application/json

{
  "manifest": {
    "/index.html": {
      "hash": "...",
      "size": 128
    }
  }
}
```

Response result:

```json
{
  "jwt": "upload token or completion token",
  "buckets": [
    ["hashA", "hashB"]
  ]
}
```

If `buckets` is empty, `jwt` is already the completion token.

## Upload Workers Assets

Documented Cloudflare Workers Static Assets API.

```http
POST /accounts/{accountId}/workers/assets/upload?base64=true
Authorization: Bearer {uploadJwt}
Content-Type: multipart/form-data
```

Each multipart part:

| Field | Value |
| --- | --- |
| form field name | asset hash |
| filename | asset hash |
| content type | MIME type inferred from original file name |
| content | base64-encoded file bytes |

The final successful upload response contains `result.jwt`, the completion
token used by script deployment.

## Upload Preview Assets

Observed/internal Drop-specific endpoint:

```http
POST /provisioning/previews/accounts/{accountId}/scripts/{scriptName}/assets?base64=true
X-Claim-Token: {claimToken}
Content-Type: multipart/form-data
```

Multipart fields:

| Field | Type | Description |
| --- | --- | --- |
| `metadata` | string | JSON worker metadata with `assets.jwt` set to completion token |
| `manifest` | string | JSON asset manifest |
| `{hash}` | file | Base64-encoded asset content |

The Drop frontend does not consume a response body; any non-2xx response is an
upload failure.

## Deploy Assets-Only Worker

Observed Drop request:

```http
PUT /accounts/{accountId}/workers/scripts/{scriptName}
Authorization: Bearer {temporaryApiToken}
Content-Type: multipart/form-data
```

Multipart field:

```json
{
  "metadata": {
    "compatibility_date": "2025-05-19",
    "assets": {
      "jwt": "{completionJwt}",
      "config": {
        "not_found_handling": "single-page-application"
      }
    },
    "bindings": [
      {
        "name": "ASSETS",
        "type": "assets"
      }
    ]
  }
}
```

The frontend sends metadata only; it does not upload a JavaScript module part.

## Enable And Resolve workers.dev

```http
POST /accounts/{accountId}/workers/scripts/{scriptName}/subdomain
Authorization: Bearer {temporaryApiToken}
Content-Type: application/json

{"enabled": true}
```

```http
GET /accounts/{accountId}/workers/subdomain
Authorization: Bearer {temporaryApiToken}
```

Response result contains:

```json
{
  "subdomain": "account-subdomain"
}
```

The final URL is:

```text
https://{scriptName}.{subdomain}.workers.dev
```

## Local Browser State

The Drop frontend stores successful sites in localStorage under:

```text
summon_site:{siteId}
```

where `siteId` is the first label of the final hostname. This is browser UI
state only and is not required by the API flow.

## Security Notes

- This flow depends on internal provisioning preview endpoints and can change
  without notice.
- Temporary API and claim tokens are short-lived secrets.
- The SDK requires an explicit `AcceptTerms` flag before it calls the
  provisioning endpoint.
- Tests that hit Cloudflare live APIs should be opt-in and should deploy only
  throwaway content.

## References

- Cloudflare Drop page: `https://www.cloudflare.com/drop/`
- Cloudflare Workers Static Assets direct upload:
  `https://developers.cloudflare.com/workers/static-assets/direct-upload/`
