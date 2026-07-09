package dropclient

import (
	"errors"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func TestBuildManifestHashes(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		content string
		want    ManifestEntry
	}{
		{
			name:    "text file",
			path:    "file.txt",
			content: "hello",
			want:    ManifestEntry{Hash: "129d0bf9c674d4cc340cf5f8feeb9f36", Size: 5},
		},
		{
			name:    "html file",
			path:    "index.html",
			content: "<h1>Hi</h1>",
			want:    ManifestEntry{Hash: "337ed087de5d5b2753ad3a2e8a3d81db", Size: 11},
		},
		{
			name:    "css file",
			path:    "style.css",
			content: "body{}",
			want:    ManifestEntry{Hash: "f78a1c20d726372960baaeb9d0aacb0c", Size: 6},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := FromBytes(tt.path, []byte(tt.content))
			if tt.path != "index.html" {
				source = FromAssets(
					testAsset("index.html", "<!doctype html>", "text/html"),
					testAsset(tt.path, tt.content, contentTypeForPath(tt.path)),
				)
			}
			manifest, assets, err := buildManifest(t.Context(), source)
			if err != nil {
				t.Fatalf("buildManifest returned error: %v", err)
			}
			got := manifest["/"+tt.path]
			if got != tt.want {
				t.Fatalf("manifest entry = %#v, want %#v", got, tt.want)
			}
			asset, ok := assets[tt.want.Hash]
			if !ok {
				t.Fatalf("asset payload for hash %s missing", tt.want.Hash)
			}
			if asset.Size != tt.want.Size {
				t.Fatalf("asset size = %d, want %d", asset.Size, tt.want.Size)
			}
		})
	}
}

func TestBuildManifestValidation(t *testing.T) {
	tests := []struct {
		name         string
		source       Source
		wantErr      error
		wantContains string
	}{
		{
			name: "accepts root index",
			source: FromBytes(
				"index.html",
				[]byte("<!doctype html>"),
				WithContentType("text/html; charset=utf-8"),
			),
		},
		{
			name:   "accepts one-level index",
			source: FromBytes("site/index.html", []byte("<!doctype html>")),
		},
		{
			name:    "rejects missing index",
			source:  FromBytes("style.css", []byte("body{}")),
			wantErr: ErrIndexMissing,
		},
		{
			name:    "rejects empty input",
			source:  FromAssets(),
			wantErr: ErrNoAssets,
		},
		{
			name: "rejects path traversal",
			source: FromBytes(
				"../index.html",
				[]byte("<!doctype html>"),
			),
			wantContains: "escapes root",
		},
		{
			name: "rejects duplicate normalized paths",
			source: FromAssets(
				testAsset("index.html", "<!doctype html>", "text/html"),
				testAsset("/index.html", "<!doctype html>", "text/html"),
			),
			wantContains: "duplicate asset path",
		},
		{
			name: "rejects declared file size above limit",
			source: FromAssets(Asset{
				Path: "index.html",
				Size: MaxFileSize + 1,
				Open: func() (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader("<!doctype html>")), nil
				},
			}),
			wantContains: "exceeds drop max file size",
		},
		{
			name: "rejects declared size mismatch",
			source: FromAssets(Asset{
				Path: "index.html",
				Size: 999,
				Open: func() (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader("<!doctype html>")), nil
				},
			}),
			wantContains: "declared size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildManifest(t.Context(), tt.source)
			if tt.wantErr == nil && tt.wantContains == "" {
				if err != nil {
					t.Fatalf("BuildManifest returned error: %v", err)
				}
				return
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("BuildManifest error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantContains != "" && (err == nil || !strings.Contains(err.Error(), tt.wantContains)) {
				t.Fatalf("BuildManifest error = %v, want message containing %q", err, tt.wantContains)
			}
		})
	}
}

func TestFromFSBuildsSortedManifest(t *testing.T) {
	source := FromFS(fstest.MapFS{
		"index.html":     {Data: []byte("<!doctype html><h1>ok</h1>")},
		"assets/app.js":  {Data: []byte("console.log('ok')")},
		"assets/app.css": {Data: []byte("body{}")},
	})

	manifest, err := BuildManifest(t.Context(), source)
	if err != nil {
		t.Fatalf("BuildManifest returned error: %v", err)
	}
	gotPaths := slices.Sorted(maps.Keys(manifest))
	wantPaths := []string{"/assets/app.css", "/assets/app.js", "/index.html"}
	if !slices.Equal(gotPaths, wantPaths) {
		t.Fatalf("paths = %#v, want %#v", gotPaths, wantPaths)
	}
}
