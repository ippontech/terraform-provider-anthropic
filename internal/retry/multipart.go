// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package retry holds the provider's retry policy for SDK requests
// (Middleware, installed on every client by SDKOptions) and the multipart
// bundle helper MultipartUpload.
package retry

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// NamedReader wraps an io.Reader with an explicit multipart filename.
//
// The SDK encoder picks a multipart filename from `Filename() string`,
// `Name() string` (path.Base'd), then the struct field name — in that
// order. We embed io.Reader (not *os.File) so the concrete file's
// Name() is not promoted; otherwise the SDK's path.Base fallback would
// flatten the bundle layout. The compile-time assertion below locks
// that contract.
type NamedReader struct {
	io.Reader
	name string
}

// NewNamedReader returns a NamedReader that reports filename as its multipart name.
func NewNamedReader(r io.Reader, filename string) NamedReader {
	return NamedReader{Reader: r, name: filename}
}

func (r NamedReader) Filename() string { return r.name }

var _ interface{ Filename() string } = NamedReader{}

// MultipartUpload opens filePaths and calls fn once with the resulting
// readers, closing the files afterwards.
//
// Each file's multipart name is `dirName + "/" + <relPath>`, where relPath is
// the file's path relative to bundleRoot in forward-slash form, preserving
// nested subdirectories. dirName is the top-level directory name in the
// upload body and must match the `name` field of the bundle's SKILL.md
// frontmatter.
//
// It does not retry: the SDK buffers the multipart body, so the client's
// Middleware replays the upload, only on 429, 529 or a connection error
// before it got a connection, since the POST creates an object. A retry
// loop here would multiply the client's attempts (it used to retry 5xx
// three times on top of the SDK's three attempts: nine POSTs).
func MultipartUpload[T any](ctx context.Context, filePaths []string, bundleRoot, dirName string, fn func([]io.Reader) (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	files, openedFiles, err := openFiles(filePaths, bundleRoot, dirName)
	if err != nil {
		return zero, err
	}
	defer closeAll(openedFiles)

	return fn(files)
}

func openFiles(filePaths []string, bundleRoot, dirName string) ([]io.Reader, []*os.File, error) {
	files := make([]io.Reader, 0, len(filePaths))
	opened := make([]*os.File, 0, len(filePaths))
	for _, p := range filePaths {
		rel, err := filepath.Rel(bundleRoot, p)
		if err != nil {
			closeAll(opened)
			return nil, nil, fmt.Errorf("unable to compute path of %q relative to bundle root %q: %w", p, bundleRoot, err)
		}
		// IsLocal also returns true for ".", which means the file path equals
		// bundleRoot and is nonsensical as an upload — reject it explicitly.
		if rel == "." || !filepath.IsLocal(rel) {
			closeAll(opened)
			return nil, nil, fmt.Errorf("file %q is not inside bundle root %q (relative path: %q)", p, bundleRoot, rel)
		}
		f, err := os.Open(p) //nolint:gosec // G304: a file of the configured skill bundle, checked to be under bundleRoot above
		if err != nil {
			closeAll(opened)
			return nil, nil, fmt.Errorf("unable to open file %q: %w", p, err)
		}
		opened = append(opened, f)
		// The API requires forward slashes; normalise so Windows-built
		// binaries do not emit backslash names.
		uploadName := dirName + "/" + filepath.ToSlash(rel)
		files = append(files, NewNamedReader(f, uploadName))
	}
	return files, opened, nil
}

func closeAll(files []*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}
