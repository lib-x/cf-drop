# Cloudflare Drop Go SDK

This repository contains a small Go SDK and API notes for the public
Cloudflare Drop flow at `https://www.cloudflare.com/drop/`.

The provisioning endpoints are observed/internal and can change without
notice. The Workers assets direct-upload endpoints are documented by
Cloudflare.

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
	client, err := dropclient.NewClient(
		dropclient.WithSOCKS5Proxy("127.0.0.1:10808"), // optional
	)
	if err != nil {
		log.Fatal(err)
	}
	result, err := client.DeployFiles(context.Background(), []dropclient.File{
		{
			Path:        "index.html",
			Content:     []byte("<!doctype html><h1>Hello from Drop</h1>"),
			ContentType: "text/html; charset=utf-8",
		},
	}, dropclient.DeployOptions{
		AcceptTerms: true,
		VerifyHTTP:  true,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.URL)
	fmt.Println(result.ClaimURL)
}
```

`AcceptTerms` is required because the Drop API submits Cloudflare's Terms of
Service and Privacy Policy acknowledgement during provisioning.

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
go test ./dropclient -run TestIntegrationDeployFiles -count=1 -v
```

With a local SOCKS5 proxy:

```bash
CLOUDFLARE_DROP_PROXY=socks5://127.0.0.1:10808 \
CLOUDFLARE_DROP_INTEGRATION=1 \
CLOUDFLARE_DROP_ACCEPT_TERMS=1 \
go test ./dropclient -run TestIntegrationDeployFiles -count=1 -v
```

The live test deploys a throwaway `index.html`, returns the workers.dev URL,
and then GETs the URL until the test marker is visible. It is skipped by
default because it creates a temporary public Cloudflare preview and depends on
local DNS/network access to `workers.dev`.

## Files

- `docs/drop-api-spec.md`: human-readable observed API spec.
- `openapi/drop-preview.yaml`: OpenAPI-style observed endpoint sketch.
- `dropclient/`: Go SDK.
