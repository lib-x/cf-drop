# Cloudflare Drop API Spec

This spec was reconstructed from the public Cloudflare Drop page at
`https://www.cloudflare.com/drop/`, its frontend bundle, and Cloudflare's
official Workers Static Assets direct-upload documentation.

Status: observed/internal for provisioning preview endpoints, documented for
Workers assets direct-upload endpoints.

## Official Product Behavior

Cloudflare's July 8, 2026 changelog describes Cloudflare Drop as a way to
deploy a static site without needing a Cloudflare account to get started.
Officially documented behavior:

- Accepted input is a folder or ZIP file of static assets, including static
  HTML, CSS, JavaScript, images, and fonts.
- The upload returns a temporary live preview that stays live for 1 hour.
- During that hour, users can test the site, share the preview URL, or claim
  the deployment to keep it.
- Claiming requires signing in to Cloudflare or creating a Cloudflare account.
  A claimed deployment can be moved into an existing account or a new account.
- Creating a new account requires email verification before continuing.
- After claiming, Cloudflare surfaces follow-up actions: add or buy a domain,
  enable Workers observability, enable Markdown for Agents, and control access
  with Cloudflare Access.

This spec uses the official changelog for product semantics and the observed
frontend/API traffic for implementation details. The provisioning endpoints
below remain observed/internal even though the product behavior is public.

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
10. Return a claim URL that can be used to keep the deployment before it
    expires.

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

## Go SDK Interface Design

The SDK exposes one deployment entrypoint:

```go
client, err := dropclient.New(clientOptions...)
result, err := client.Deploy(ctx, source, deployOptions...)
```

The stable input contract is `Source`:

```go
type Source interface {
    WalkAssets(context.Context, func(Asset) error) error
}

type Asset struct {
    Path        string
    Size        int64
    ContentType string
    Open        func() (io.ReadCloser, error)
}
```

Deploy output includes both user-facing addresses:

| Field | Meaning | Sensitivity |
| --- | --- | --- |
| `URL` | Temporary workers.dev preview URL. | Public preview URL. |
| `ClaimURL` | Cloudflare claim URL for keeping the deployment. | Contains a short-lived claim token. |
| `ClaimExpires` | Claim URL expiration time. | Not secret. |
| `ExpiresAt` | Temporary account credential expiration time. | Not secret by itself. |

Supported constructors:

| Constructor | Use case | Notes |
| --- | --- | --- |
| `FromBytes(path, content, ...)` | generated or in-memory single file | reusable source |
| `FromReader(path, reader, ...)` | streaming one generated file | one-shot source |
| `FromAssets(...)` | custom virtual source | caller controls `Open` |
| `FromFS(fs.FS)` | `embed.FS`, `os.DirFS`, virtual filesystems | regular files only |
| `FromDir(root)` | local output directory | thin wrapper over `os.DirFS` |
| `FromZipFile(path)` | local ZIP archive | enforces observed ZIP size limit |
| `FromZipBytes(content)` | in-memory ZIP archive | reusable source |
| `FromZipReader(reader)` | ZIP stream | buffers up to `MaxZipSize` |
| `FromZipReaderAt(readerAt, size)` | ZIP without buffering | preferred for large ZIP inputs |

### Design Rationale And Tradeoffs

- Use one `Deploy(ctx, Source, ...DeployOption)` method instead of one deploy
  method per input kind. This follows a one-version API shape: new input forms
  can be added as `Source` constructors without multiplying deploy methods.
- Use `fs.FS` as the folder abstraction instead of accepting only local paths.
  This covers `embed.FS`, `os.DirFS`, test filesystems, and virtual filesystems
  with the same validation and upload path.
- Use `io.ReaderAt + size` as the zero-copy ZIP contract because Go's
  `archive/zip` reads the central directory from the end of the archive and
  requires random access. `FromZipReader` is still provided for convenience, but
  it must buffer the stream before it can parse the ZIP.
- Keep `FromReader` for single-file generated content. A plain `io.Reader`
  cannot represent a folder and cannot safely represent ZIP without buffering,
  so it is intentionally scoped to one asset.
- Keep functional options for client and deploy settings. Proxy configuration,
  custom HTTP clients, script naming, compatibility date, and access
  verification are independent concerns; options keep the required path small
  while allowing explicit opt-in behavior.
- Require `AcceptTerms()` as an explicit deploy option. Provisioning sends
  Cloudflare terms and privacy acknowledgement, so the SDK does not hide that
  side effect behind a default.
- Validate at the source boundary before provisioning. Paths are normalized,
  `..` traversal is rejected, only regular `fs.FS` and ZIP entries are used,
  duplicate normalized paths fail, size limits are enforced, and `index.html`
  is required at the root or one top-level directory below root.
- Internally the SDK currently buffers asset bytes and base64 strings while
  building the manifest and multipart requests. That matches the observed Drop
  API, which hashes `base64(fileBytes) + extension` and uploads base64 multipart
  parts. The public `Source` interface keeps the door open for a future
  temp-file or streaming-backed internal implementation without changing
  callers.
- Temporary Cloudflare API tokens and claim tokens are treated as secrets and
  are not logged. Error values include endpoint paths and Cloudflare error
  messages, not bearer token values.

Rejected alternatives:

| Alternative | Reason rejected |
| --- | --- |
| One method per input type | Expands the public surface and makes every new input a new deploy method. |
| Accept only `[]byte` files | Simple, but excludes folders, `embed.FS`, ZIPs, and virtual filesystems. |
| Accept only `io.Reader` | Too weak for folders and ZIP central directory parsing. |
| Accept only local filesystem paths | Excludes embedded and generated content and makes tests harder. |
| Auto-accept terms | Hides a legal/side-effectful provisioning acknowledgement. |

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

The `claim.url` field is the claim address returned by the SDK as
`DeployResult.ClaimURL`. It should be shown to the caller when they need to keep
the deployment, but it should not be written to shared logs because it carries a
short-lived claim token.

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
- The claim address is returned as `DeployResult.ClaimURL`; treat it as secret
  material because it embeds or references the claim token.
- Tests that hit Cloudflare live APIs should be opt-in and should deploy only
  throwaway content.

## References

- Cloudflare Drop page: `https://www.cloudflare.com/drop/`
- Cloudflare Drop changelog, published 2026-07-08:
  `https://developers.cloudflare.com/changelog/post/2026-07-08-cloudflare-drag-and-drop/`
- Cloudflare Workers Static Assets direct upload:
  `https://developers.cloudflare.com/workers/static-assets/direct-upload/`
