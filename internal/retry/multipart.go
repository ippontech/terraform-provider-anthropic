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
// The Anthropic SDK encoder checks for Filename() before falling back to
// the struct field name, which is required for the API to accept the upload.
type NamedReader struct {
	io.Reader
	name string
}

// NewNamedReader returns a NamedReader that reports filename as its multipart name.
func NewNamedReader(r io.Reader, filename string) NamedReader {
	return NamedReader{Reader: r, name: filename}
}

// Filename satisfies the SDK's optional naming interface.
func (r NamedReader) Filename() string { return r.name }

// backoff returns the delay before the given retry attempt.
// Replaced by tests to avoid real sleeps.
var backoff = func(attempt int) time.Duration {
	return time.Duration(attempt) * 5 * time.Second
}

// MultipartUpload opens filePaths fresh on each attempt and calls fn with the
// resulting readers, retrying up to 3 times on 5xx API errors with backoff.
//
// dirName is prepended to each file's base name in the multipart body (the
// Anthropic API requires all files to live inside a named top-level directory,
// and that name must match the `name` field in SKILL.md).
//
// File-open errors and non-5xx API errors are returned immediately without
// retrying.
func MultipartUpload[T any](ctx context.Context, filePaths []string, dirName string, fn func([]io.Reader) (T, error)) (T, error) {
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

		files, openedFiles, err := openFiles(filePaths, dirName)
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

func openFiles(filePaths []string, dirName string) ([]io.Reader, []*os.File, error) {
	files := make([]io.Reader, 0, len(filePaths))
	opened := make([]*os.File, 0, len(filePaths))
	for _, p := range filePaths {
		f, err := os.Open(p)
		if err != nil {
			closeAll(opened)
			return nil, nil, fmt.Errorf("unable to open file %q: %w", p, err)
		}
		opened = append(opened, f)
		files = append(files, NewNamedReader(f, dirName+"/"+filepath.Base(p)))
	}
	return files, opened, nil
}

func closeAll(files []*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}
