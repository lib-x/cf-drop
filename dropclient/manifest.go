package dropclient

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type assetPayload struct {
	Path        string
	Hash        string
	Base64      string
	ContentType string
	Size        int64
}

func BuildManifest(files []File) (Manifest, map[string]assetPayload, error) {
	if len(files) == 0 {
		return nil, nil, ErrNoFiles
	}
	manifest := make(Manifest, len(files))
	assets := make(map[string]assetPayload, len(files))
	for _, file := range files {
		normalized, err := normalizeAssetPath(file.Path)
		if err != nil {
			return nil, nil, err
		}
		encoded := base64.StdEncoding.EncodeToString(file.Content)
		hash := assetHash(encoded, extension(file.Path))
		size := int64(len(file.Content))
		manifest[normalized] = ManifestEntry{Hash: hash, Size: size}
		if _, ok := assets[hash]; !ok {
			contentType := file.ContentType
			if contentType == "" {
				contentType = contentTypeForPath(file.Path)
			}
			assets[hash] = assetPayload{
				Path:        normalized,
				Hash:        hash,
				Base64:      encoded,
				ContentType: contentType,
				Size:        size,
			}
		}
	}
	return manifest, assets, nil
}

func ReadDirectory(root string) ([]File, error) {
	var files []File
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, File{
			Path:        filepath.ToSlash(rel),
			Content:     content,
			ContentType: contentTypeForPath(rel),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func ValidateDropFiles(files []File) error {
	if len(files) == 0 {
		return ErrNoFiles
	}
	if len(files) > MaxFileCount {
		return fmt.Errorf("file count %d exceeds drop limit %d", len(files), MaxFileCount)
	}
	var total int64
	hasIndex := false
	for _, file := range files {
		size := int64(len(file.Content))
		if size > MaxFileSize {
			return fmt.Errorf("%s is %d bytes, exceeds drop max file size %d", file.Path, size, MaxFileSize)
		}
		total += size
		if total > MaxTotalSize {
			return fmt.Errorf("total size %d exceeds drop total size %d", total, MaxTotalSize)
		}
		normalized, err := normalizeAssetPath(file.Path)
		if err != nil {
			return err
		}
		if isAllowedIndexPath(normalized) {
			hasIndex = true
		}
	}
	if !hasIndex {
		return ErrIndexMissing
	}
	return nil
}

func isAllowedIndexPath(path string) bool {
	path = strings.TrimPrefix(filepath.ToSlash(path), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 1 {
		return strings.EqualFold(parts[0], "index.html")
	}
	return len(parts) == 2 && strings.EqualFold(parts[1], "index.html")
}

func assetHash(encodedContent, ext string) string {
	sum := sha256.Sum256([]byte(encodedContent + ext))
	return hex.EncodeToString(sum[:])[:32]
}

func extension(path string) string {
	base := filepath.Base(path)
	idx := strings.LastIndex(base, ".")
	if idx == -1 || idx == len(base)-1 {
		return ""
	}
	return base[idx+1:]
}

func normalizeAssetPath(path string) (string, error) {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" || path == "." || path == "/" {
		return "", fmt.Errorf("invalid asset path %q", path)
	}
	path = strings.TrimPrefix(path, "/")
	parts := strings.Split(path, "/")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", fmt.Errorf("asset path %q escapes root", path)
		}
		clean = append(clean, part)
	}
	if len(clean) == 0 {
		return "", fmt.Errorf("invalid asset path %q", path)
	}
	return "/" + strings.Join(clean, "/"), nil
}

func contentTypeForPath(path string) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return "application/octet-stream"
	}
	if contentType := mime.TypeByExtension(ext); contentType != "" {
		return contentType
	}
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "js", "mjs":
		return "application/javascript"
	case "wasm":
		return "application/wasm"
	case "md":
		return "text/markdown"
	default:
		return "application/octet-stream"
	}
}
