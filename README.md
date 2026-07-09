# Cloudflare Drop Go SDK

This repository contains a small Go SDK and API notes for the public
Cloudflare Drop flow at `https://www.cloudflare.com/drop/`.

The provisioning endpoints are observed/internal and can change without
notice. The Workers assets direct-upload endpoints are documented by
Cloudflare.

Cloudflare's July 8, 2026 changelog describes Drop as a folder/ZIP upload flow
for static HTML, CSS, JavaScript, images, and fonts. The deployment starts as a
temporary live preview that stays available for 1 hour; use the returned claim
address to keep the deployment in a Cloudflare account.

## Usage

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/lib-x/cf-drop/dropclient"
)

func main() {
	client, err := dropclient.New(
		dropclient.WithSOCKS5Proxy("127.0.0.1:10808"), // optional
	)
	if err != nil {
		log.Fatal(err)
	}
	source := dropclient.FromBytes(
		"index.html",
		[]byte("<!doctype html><h1>Hello from Drop</h1>"),
		dropclient.WithContentType("text/html; charset=utf-8"),
	)
	result, err := client.Deploy(
		context.Background(),
		source,
		dropclient.AcceptTerms(),
		dropclient.VerifyAccess(),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("preview:", result.URL)
	fmt.Println("claim:", result.ClaimURL)
}
```

`AcceptTerms` is required because the Drop API submits Cloudflare's Terms of
Service and Privacy Policy acknowledgement during provisioning. `ClaimURL` is
the address used to claim and keep the deployment; it contains a temporary
claim token and should be handled as a secret in logs and telemetry.

`DeployResult` returns both addresses:

| Field | Meaning |
| --- | --- |
| `URL` | Temporary public workers.dev preview URL. |
| `ClaimURL` | Cloudflare claim address used to keep the deployment. |
| `ClaimExpires` | Expiration time for the claim address. |
| `ExpiresAt` | Expiration time for the temporary account credentials. |

## Input Sources

The SDK deploys any `dropclient.Source`.

```go
// Single generated file.
source := dropclient.FromBytes("index.html", html)

// Folder or embedded filesystem.
source = dropclient.FromDir("./dist")
source = dropclient.FromFS(os.DirFS("./dist"))

// ZIP archive.
source, err = dropclient.FromZipFile("site.zip")
source, err = dropclient.FromZipReader(reader)
source, err = dropclient.FromZipReaderAt(readerAt, size)

// Custom virtual source.
source = dropclient.FromAssets(dropclient.Asset{
	Path: "index.html",
	Size: int64(len(html)),
	Open: func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(html)), nil
	},
})
```

`FromReader` is available for a single one-shot asset stream. ZIP streams need
either `io.ReaderAt` plus size, or buffering through `FromZipReader`, because
Go's `archive/zip` needs random access to the central directory.

## Tests

Default tests run locally and include a fake Cloudflare API plus a fake public
site server:

```bash
go test ./...
```

Live integration test:

```bash
CLOUDFLARE_DROP_INTEGRATION=1 \
CLOUDFLARE_DROP_ACCEPT_TERMS=1 \
go test ./dropclient -run TestIntegrationDeploy -count=1 -v
```

With a local SOCKS5 proxy:

```bash
CLOUDFLARE_DROP_PROXY=socks5://127.0.0.1:10808 \
CLOUDFLARE_DROP_INTEGRATION=1 \
CLOUDFLARE_DROP_ACCEPT_TERMS=1 \
go test ./dropclient -run TestIntegrationDeploy -count=1 -v
```

The live test deploys a throwaway `index.html`, returns the workers.dev URL,
checks that the claim URL is returned, and then GETs the preview URL until the
test marker is visible. It is skipped by default because it creates a temporary
public Cloudflare preview and depends on local DNS/network access to
`workers.dev`.

## Files

- `docs/drop-api-spec.md`: human-readable observed API spec.
- `openapi/drop-preview.yaml`: OpenAPI-style observed endpoint sketch.
- `dropclient/`: Go SDK.
