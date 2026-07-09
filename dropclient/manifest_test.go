package dropclient

import (
	"errors"
	"reflect"
	"testing"
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
			manifest, assets, err := BuildManifest([]File{{Path: tt.path, Content: []byte(tt.content)}})
			if err != nil {
				t.Fatalf("BuildManifest returned error: %v", err)
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

func TestValidateDropFiles(t *testing.T) {
	tests := []struct {
		name    string
		files   []File
		wantErr error
	}{
		{
			name: "accepts root index",
			files: []File{
				{Path: "index.html", Content: []byte("<!doctype html>")},
			},
		},
		{
			name: "accepts one-level index",
			files: []File{
				{Path: "site/index.html", Content: []byte("<!doctype html>")},
			},
		},
		{
			name: "rejects missing index",
			files: []File{
				{Path: "style.css", Content: []byte("body{}")},
			},
			wantErr: ErrIndexMissing,
		},
		{
			name:    "rejects empty input",
			wantErr: ErrNoFiles,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDropFiles(tt.files)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateDropFiles returned error: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateDropFiles error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestReadDirectory(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<!doctype html><h1>ok</h1>")
	writeTestFile(t, dir, "assets/app.js", "console.log('ok')")

	files, err := ReadDirectory(dir)
	if err != nil {
		t.Fatalf("ReadDirectory returned error: %v", err)
	}
	gotPaths := []string{files[0].Path, files[1].Path}
	wantPaths := []string{"assets/app.js", "index.html"}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("paths = %#v, want %#v", gotPaths, wantPaths)
	}
}
