package dropclient

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"path"
	"strings"
)

type assetPayload struct {
	Path        string
	Hash        string
	Base64      string
	ContentType string
	Size        int64
}

func BuildManifest(ctx context.Context, source Source) (Manifest, error) {
	manifest, _, err := buildManifest(ctx, source)
	return manifest, err
}

func buildManifest(ctx context.Context, source Source) (Manifest, map[string]assetPayload, error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("context cannot be nil")
	}
	if source == nil {
		return nil, nil, ErrNoAssets
	}
	manifest := make(Manifest)
	assets := make(map[string]assetPayload)
	var count int
	var total int64
	hasIndex := false
	err := source.WalkAssets(ctx, func(asset Asset) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		normalized, err := normalizeAssetPath(asset.Path)
		if err != nil {
			return err
		}
		if _, exists := manifest[normalized]; exists {
			return fmt.Errorf("duplicate asset path %q", normalized)
		}
		if asset.Open == nil {
			return fmt.Errorf("asset %q cannot be opened", normalized)
		}
		if asset.Size >= 0 && asset.Size > MaxFileSize {
			return fmt.Errorf("%s is %d bytes, exceeds drop max file size %d", normalized, asset.Size, MaxFileSize)
		}
		reader, err := asset.Open()
		if err != nil {
			return fmt.Errorf("open asset %s: %w", normalized, err)
		}
		content, readErr := io.ReadAll(io.LimitReader(reader, MaxFileSize+1))
		closeErr := reader.Close()
		if readErr != nil {
			return fmt.Errorf("read asset %s: %w", normalized, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close asset %s: %w", normalized, closeErr)
		}
		size := int64(len(content))
		if size > MaxFileSize {
			return fmt.Errorf("%s is %d bytes, exceeds drop max file size %d", normalized, size, MaxFileSize)
		}
		if asset.Size >= 0 && asset.Size != size {
			return fmt.Errorf("%s declared size %d but read %d bytes", normalized, asset.Size, size)
		}
		count++
		if count > MaxAssetCount {
			return fmt.Errorf("asset count %d exceeds drop limit %d", count, MaxAssetCount)
		}
		total += size
		if total > MaxTotalSize {
			return fmt.Errorf("total size %d exceeds drop total size %d", total, MaxTotalSize)
		}
		encoded := base64.StdEncoding.EncodeToString(content)
		hash := assetHash(encoded, extension(normalized))
		manifest[normalized] = ManifestEntry{Hash: hash, Size: size}
		if _, ok := assets[hash]; !ok {
			contentType := asset.ContentType
			if contentType == "" {
				contentType = contentTypeForPath(normalized)
			}
			assets[hash] = assetPayload{
				Path:        normalized,
				Hash:        hash,
				Base64:      encoded,
				ContentType: contentType,
				Size:        size,
			}
		}
		if isAllowedIndexPath(normalized) {
			hasIndex = true
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if count == 0 {
		return nil, nil, ErrNoAssets
	}
	if !hasIndex {
		return nil, nil, ErrIndexMissing
	}
	return manifest, assets, nil
}

func isAllowedIndexPath(assetPath string) bool {
	assetPath = strings.TrimPrefix(assetPath, "/")
	parts := strings.Split(assetPath, "/")
	if len(parts) == 1 {
		return strings.EqualFold(parts[0], "index.html")
	}
	return len(parts) == 2 && strings.EqualFold(parts[1], "index.html")
}

func assetHash(encodedContent, ext string) string {
	sum := sha256.Sum256([]byte(encodedContent + ext))
	return hex.EncodeToString(sum[:])[:32]
}

func extension(assetPath string) string {
	base := path.Base(assetPath)
	idx := strings.LastIndex(base, ".")
	if idx == -1 || idx == len(base)-1 {
		return ""
	}
	return base[idx+1:]
}

func normalizeAssetPath(assetPath string) (string, error) {
	assetPath = strings.TrimSpace(strings.ReplaceAll(assetPath, "\\", "/"))
	if assetPath == "" || assetPath == "." || assetPath == "/" {
		return "", fmt.Errorf("invalid asset path %q", assetPath)
	}
	assetPath = strings.TrimPrefix(assetPath, "/")
	clean := make([]string, 0, strings.Count(assetPath, "/")+1)
	for part := range strings.SplitSeq(assetPath, "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", fmt.Errorf("asset path %q escapes root", assetPath)
		}
		if strings.ContainsRune(part, 0) {
			return "", fmt.Errorf("asset path %q contains NUL byte", assetPath)
		}
		clean = append(clean, part)
	}
	if len(clean) == 0 {
		return "", fmt.Errorf("invalid asset path %q", assetPath)
	}
	return "/" + strings.Join(clean, "/"), nil
}

func contentTypeForPath(assetPath string) string {
	ext := path.Ext(assetPath)
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
