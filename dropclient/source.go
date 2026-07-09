package dropclient

import (
	"archive/zip"
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"sync"
)

// WithContentType sets the MIME type for an asset constructor.
func WithContentType(value string) AssetOption {
	return func(opts *assetOptions) {
		opts.ContentType = value
	}
}

// WithSize sets the expected byte size for an asset constructor.
func WithSize(value int64) AssetOption {
	return func(opts *assetOptions) {
		opts.Size = value
	}
}

// FromAssets returns a Source from caller-provided assets.
func FromAssets(assets ...Asset) Source {
	copied := slices.Clone(assets)
	return SourceFunc(func(ctx context.Context, yield func(Asset) error) error {
		for _, asset := range copied {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if err := yield(asset); err != nil {
				return err
			}
		}
		return nil
	})
}

// FromBytes returns a reusable single-asset Source backed by memory.
func FromBytes(assetPath string, content []byte, options ...AssetOption) Source {
	opts := parseAssetOptions(options)
	copied := bytes.Clone(content)
	if opts.Size == UnknownSize {
		opts.Size = int64(len(copied))
	}
	return FromAssets(Asset{
		Path:        assetPath,
		Size:        opts.Size,
		ContentType: opts.ContentType,
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(copied)), nil
		},
	})
}

// FromReader returns a single-asset Source backed by a one-shot reader.
func FromReader(assetPath string, reader io.Reader, options ...AssetOption) Source {
	opts := parseAssetOptions(options)
	var mu sync.Mutex
	consumed := false
	return SourceFunc(func(ctx context.Context, yield func(Asset) error) error {
		if reader == nil {
			return fmt.Errorf("reader cannot be nil")
		}
		mu.Lock()
		if consumed {
			mu.Unlock()
			return fmt.Errorf("reader source for %q has already been consumed", assetPath)
		}
		consumed = true
		mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		return yield(Asset{
			Path:        assetPath,
			Size:        opts.Size,
			ContentType: opts.ContentType,
			Open: func() (io.ReadCloser, error) {
				return io.NopCloser(reader), nil
			},
		})
	})
}

// FromFS returns a Source from any fs.FS.
func FromFS(fsys fs.FS) Source {
	return fsSource{fsys: fsys}
}

// FromDir returns a Source backed by os.DirFS(root).
func FromDir(root string) Source {
	return FromFS(os.DirFS(root))
}

// FromZipFile reads a ZIP file and returns it as a Source.
func FromZipFile(filename string) (Source, error) {
	info, err := os.Stat(filename)
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxZipSize {
		return nil, ErrZipTooLarge
	}
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return FromZipBytes(content)
}

// FromZipReader buffers a ZIP stream and returns it as a Source.
func FromZipReader(reader io.Reader) (Source, error) {
	if reader == nil {
		return nil, fmt.Errorf("zip reader cannot be nil")
	}
	content, err := io.ReadAll(io.LimitReader(reader, MaxZipSize+1))
	if err != nil {
		return nil, err
	}
	if len(content) > MaxZipSize {
		return nil, ErrZipTooLarge
	}
	return FromZipBytes(content)
}

// FromZipBytes returns a ZIP archive as a reusable Source.
func FromZipBytes(content []byte) (Source, error) {
	if len(content) > MaxZipSize {
		return nil, ErrZipTooLarge
	}
	copied := bytes.Clone(content)
	reader := bytes.NewReader(copied)
	zr, err := zip.NewReader(reader, int64(len(copied)))
	if err != nil {
		return nil, err
	}
	return zipSource{reader: zr}, nil
}

// FromZipReaderAt returns a ZIP archive Source without buffering the archive.
// The reader must remain usable while the Source is deployed.
func FromZipReaderAt(reader io.ReaderAt, size int64) (Source, error) {
	if reader == nil {
		return nil, fmt.Errorf("zip reader cannot be nil")
	}
	if size < 0 {
		return nil, fmt.Errorf("zip size cannot be negative")
	}
	if size > MaxZipSize {
		return nil, ErrZipTooLarge
	}
	zr, err := zip.NewReader(reader, size)
	if err != nil {
		return nil, err
	}
	return zipSource{reader: zr}, nil
}

func parseAssetOptions(options []AssetOption) assetOptions {
	opts := assetOptions{Size: UnknownSize}
	for _, option := range options {
		if option != nil {
			option(&opts)
		}
	}
	return opts
}

type fsSource struct {
	fsys fs.FS
}

func (s fsSource) WalkAssets(ctx context.Context, yield func(Asset) error) error {
	if s.fsys == nil {
		return fmt.Errorf("fs cannot be nil")
	}
	return fs.WalkDir(s.fsys, ".", func(assetPath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
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
		pathForOpen := assetPath
		return yield(Asset{
			Path:        pathForOpen,
			Size:        info.Size(),
			ContentType: contentTypeForPath(pathForOpen),
			Open: func() (io.ReadCloser, error) {
				return s.fsys.Open(pathForOpen)
			},
		})
	})
}

type zipSource struct {
	reader *zip.Reader
}

func (s zipSource) WalkAssets(ctx context.Context, yield func(Asset) error) error {
	if s.reader == nil {
		return fmt.Errorf("zip reader cannot be nil")
	}
	files := make([]*zip.File, 0, len(s.reader.File))
	for _, file := range s.reader.File {
		if zipFileIsRegular(file) {
			files = append(files, file)
		}
	}
	slices.SortFunc(files, func(a, b *zip.File) int {
		return cmp.Compare(a.Name, b.Name)
	})
	for _, file := range files {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		zf := file
		if err := yield(Asset{
			Path:        zf.Name,
			Size:        int64(zf.UncompressedSize64),
			ContentType: contentTypeForPath(zf.Name),
			Open:        zf.Open,
		}); err != nil {
			return err
		}
	}
	return nil
}

func zipFileIsRegular(file *zip.File) bool {
	if file == nil || file.FileInfo().IsDir() {
		return false
	}
	mode := file.FileInfo().Mode()
	return mode&fs.ModeType == 0 || mode.IsRegular()
}
