package dropclient

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFromDirBuildsManifest(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<!doctype html><h1>ok</h1>")
	writeTestFile(t, dir, "assets/app.js", "console.log('ok')")

	manifest, err := BuildManifest(t.Context(), FromDir(dir))
	if err != nil {
		t.Fatalf("BuildManifest returned error: %v", err)
	}
	gotPaths := slices.Sorted(maps.Keys(manifest))
	wantPaths := []string{"/assets/app.js", "/index.html"}
	if !slices.Equal(gotPaths, wantPaths) {
		t.Fatalf("paths = %#v, want %#v", gotPaths, wantPaths)
	}
}

func TestFromReaderBuildsManifestOnce(t *testing.T) {
	source := FromReader(
		"index.html",
		strings.NewReader("<!doctype html><h1>reader</h1>"),
		WithContentType("text/html; charset=utf-8"),
	)
	manifest, err := BuildManifest(t.Context(), source)
	if err != nil {
		t.Fatalf("BuildManifest returned error: %v", err)
	}
	if _, ok := manifest["/index.html"]; !ok {
		t.Fatalf("manifest missing /index.html: %#v", manifest)
	}
	_, err = BuildManifest(t.Context(), source)
	if err == nil || !strings.Contains(err.Error(), "already been consumed") {
		t.Fatalf("second BuildManifest error = %v, want consumed reader error", err)
	}
}

func TestZipSourcesBuildManifest(t *testing.T) {
	archive := makeZip(t, map[string]string{
		"index.html":     "<!doctype html><h1>zip</h1>",
		"assets/app.css": "body{}",
		"assets/app.js":  "console.log('zip')",
	})
	tests := []struct {
		name string
		open func() (Source, error)
	}{
		{
			name: "bytes",
			open: func() (Source, error) {
				return FromZipBytes(archive)
			},
		},
		{
			name: "reader",
			open: func() (Source, error) {
				return FromZipReader(bytes.NewReader(archive))
			},
		},
		{
			name: "readerAt",
			open: func() (Source, error) {
				return FromZipReaderAt(bytes.NewReader(archive), int64(len(archive)))
			},
		},
		{
			name: "file",
			open: func() (Source, error) {
				filename := writeTempZip(t, archive)
				return FromZipFile(filename)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, err := tt.open()
			if err != nil {
				t.Fatalf("open source returned error: %v", err)
			}
			manifest, err := BuildManifest(t.Context(), source)
			if err != nil {
				t.Fatalf("BuildManifest returned error: %v", err)
			}
			gotPaths := slices.Sorted(maps.Keys(manifest))
			wantPaths := []string{"/assets/app.css", "/assets/app.js", "/index.html"}
			if !slices.Equal(gotPaths, wantPaths) {
				t.Fatalf("paths = %#v, want %#v", gotPaths, wantPaths)
			}
		})
	}
}

func TestZipSourceRejectsTraversalWhenManifestBuilds(t *testing.T) {
	archive := makeZip(t, map[string]string{
		"../index.html": "<!doctype html><h1>bad</h1>",
	})
	source, err := FromZipBytes(archive)
	if err != nil {
		t.Fatalf("FromZipBytes returned error: %v", err)
	}
	_, err = BuildManifest(t.Context(), source)
	if err == nil || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("BuildManifest error = %v, want path traversal error", err)
	}
}

func TestZipSourcesRejectOversizedArchive(t *testing.T) {
	oversized := make([]byte, MaxZipSize+1)
	_, err := FromZipBytes(oversized)
	if !errors.Is(err, ErrZipTooLarge) {
		t.Fatalf("FromZipBytes error = %v, want %v", err, ErrZipTooLarge)
	}
	_, err = FromZipReader(bytes.NewReader(oversized))
	if !errors.Is(err, ErrZipTooLarge) {
		t.Fatalf("FromZipReader error = %v, want %v", err, ErrZipTooLarge)
	}
	_, err = FromZipReaderAt(bytes.NewReader(oversized), int64(len(oversized)))
	if !errors.Is(err, ErrZipTooLarge) {
		t.Fatalf("FromZipReaderAt error = %v, want %v", err, ErrZipTooLarge)
	}
	filename := writeTempZip(t, oversized)
	_, err = FromZipFile(filename)
	if !errors.Is(err, ErrZipTooLarge) {
		t.Fatalf("FromZipFile error = %v, want %v", err, ErrZipTooLarge)
	}
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		part, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := io.WriteString(part, files[name]); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func writeTempZip(t *testing.T, content []byte) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "site.zip")
	if err := os.WriteFile(filename, content, 0o644); err != nil {
		t.Fatalf("write temp zip: %v", err)
	}
	return filename
}
