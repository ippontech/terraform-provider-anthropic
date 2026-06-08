// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package retry provides retry helpers for operations that the Anthropic SDK
// cannot retry automatically.
//
// Multipart file uploads set req.Body without req.GetBody, so the SDK's
// built-in retry logic (which requires a replayable body) is bypassed for
// all 5xx responses. MultipartUpload works around this by re-opening files
// from disk on each attempt.
package retry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
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

// backoff returns the delay before the given retry attempt.
// Replaced by tests to avoid real sleeps.
var backoff = func(attempt int) time.Duration {
	return time.Duration(attempt) * 5 * time.Second
}

// MultipartUpload opens filePaths fresh on each attempt and calls fn with the
// resulting readers, retrying up to 3 times on 5xx API errors with backoff.
//
// Each file's multipart name is `dirName + "/" + <relPath>`, where relPath is
// the file's path relative to bundleRoot in forward-slash form, preserving
// nested subdirectories. dirName is the top-level directory name in the
// upload body and must match the `name` field of the bundle's SKILL.md
// frontmatter.
//
// File-open errors and non-5xx API errors are returned immediately without
// retrying.
func MultipartUpload[T any](ctx context.Context, filePaths []string, bundleRoot, dirName string, fn func([]io.Reader) (T, error)) (T, error) {
	const maxAttempts = 3
	var zero T
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}

		files, openedFiles, err := openFiles(filePaths, bundleRoot, dirName)
		if err != nil {
			return zero, err
		}

		result, err := fn(files)
		closeAll(openedFiles)
		if err == nil {
			return result, nil
		}

		var apierr *anthropic.Error
		if !errors.As(err, &apierr) || apierr.StatusCode < 500 || attempt == maxAttempts-1 {
			return zero, err
		}
	}
	return zero, nil // unreachable
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
		f, err := os.Open(p)
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
