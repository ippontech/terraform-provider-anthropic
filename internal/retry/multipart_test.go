// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package retry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// noBackoff replaces the package-level backoff function with a no-op for the
// duration of a test, so retry loops complete without real sleeps.
func noBackoff(t *testing.T) {
	t.Helper()
	old := backoff
	backoff = func(_ int) time.Duration { return 0 }
	t.Cleanup(func() { backoff = old })
}

// apiErr builds a minimal *anthropic.Error with the given HTTP status code.
func apiErr(statusCode int) *anthropic.Error {
	req, _ := http.NewRequest(http.MethodPost, "https://api.anthropic.com/test", nil)
	return &anthropic.Error{
		StatusCode: statusCode,
		Request:    req,
		Response:   &http.Response{StatusCode: statusCode},
	}
}

// writeFile creates a file with the given content inside dir and returns its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	return p
}

// ---- NamedReader ----

func TestNamedReader_Filename(t *testing.T) {
	r := NewNamedReader(strings.NewReader("data"), "mydir/SKILL.md")
	if got := r.Filename(); got != "mydir/SKILL.md" {
		t.Errorf("Filename() = %q, want %q", got, "mydir/SKILL.md")
	}
}

func TestNamedReader_Read(t *testing.T) {
	r := NewNamedReader(strings.NewReader("hello"), "dir/file.md")
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("Read content = %q, want %q", string(got), "hello")
	}
}

// ---- MultipartUpload ----

func TestMultipartUpload_SuccessOnFirstAttempt(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "SKILL.md", "content")

	calls := 0
	result, err := MultipartUpload(context.Background(), []string{p}, "mydir", func(_ []io.Reader) (string, error) {
		calls++
		return "ok", nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("result = %q, want %q", result, "ok")
	}
	if calls != 1 {
		t.Errorf("fn called %d times, want 1", calls)
	}
}

func TestMultipartUpload_RetriesOn5xx(t *testing.T) {
	noBackoff(t)
	dir := t.TempDir()
	p := writeFile(t, dir, "SKILL.md", "content")

	calls := 0
	result, err := MultipartUpload(context.Background(), []string{p}, "mydir", func(_ []io.Reader) (string, error) {
		calls++
		if calls < 2 {
			return "", apiErr(500)
		}
		return "ok", nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("result = %q, want %q", result, "ok")
	}
	if calls != 2 {
		t.Errorf("fn called %d times, want 2", calls)
	}
}

func TestMultipartUpload_ExhaustsMaxAttempts(t *testing.T) {
	noBackoff(t)
	dir := t.TempDir()
	p := writeFile(t, dir, "SKILL.md", "content")

	calls := 0
	_, err := MultipartUpload(context.Background(), []string{p}, "mydir", func(_ []io.Reader) (string, error) {
		calls++
		return "", apiErr(503)
	})

	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if calls != 3 {
		t.Errorf("fn called %d times, want 3", calls)
	}
	var apierr *anthropic.Error
	if !errors.As(err, &apierr) || apierr.StatusCode != 503 {
		t.Errorf("expected 503 API error, got %v", err)
	}
}

func TestMultipartUpload_NoRetryOn4xx(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "SKILL.md", "content")

	calls := 0
	_, err := MultipartUpload(context.Background(), []string{p}, "mydir", func(_ []io.Reader) (string, error) {
		calls++
		return "", apiErr(400)
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("fn called %d times, want 1 (no retry on 4xx)", calls)
	}
}

func TestMultipartUpload_NoRetryOnNonAPIError(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "SKILL.md", "content")

	calls := 0
	_, err := MultipartUpload(context.Background(), []string{p}, "mydir", func(_ []io.Reader) (string, error) {
		calls++
		return "", errors.New("connection reset")
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("fn called %d times, want 1 (no retry on non-API error)", calls)
	}
}

func TestMultipartUpload_FileOpenError(t *testing.T) {
	calls := 0
	_, err := MultipartUpload(context.Background(), []string{"/nonexistent/path/SKILL.md"}, "mydir", func(_ []io.Reader) (string, error) {
		calls++
		return "ok", nil
	})

	if err == nil {
		t.Fatal("expected file-open error")
	}
	if !strings.Contains(err.Error(), "unable to open file") {
		t.Errorf("expected 'unable to open file' in error, got: %v", err)
	}
	if calls != 0 {
		t.Errorf("fn called %d times, want 0 (file open failed)", calls)
	}
}

func TestMultipartUpload_ContextCancelledDuringBackoff(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "SKILL.md", "content")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so the backoff select fires immediately on attempt 1

	calls := 0
	_, err := MultipartUpload(ctx, []string{p}, "mydir", func(_ []io.Reader) (string, error) {
		calls++
		return "", apiErr(500) // triggers a retry attempt
	})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if calls != 1 {
		t.Errorf("fn called %d times, want 1 (cancelled before second attempt)", calls)
	}
}

func TestMultipartUpload_FileNamingUseDirName(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "SKILL.md", "content")

	var gotFilename string
	_, _ = MultipartUpload(context.Background(), []string{p}, "myskill", func(files []io.Reader) (string, error) {
		if named, ok := files[0].(interface{ Filename() string }); ok {
			gotFilename = named.Filename()
		}
		return "ok", nil
	})

	want := "myskill/SKILL.md"
	if gotFilename != want {
		t.Errorf("multipart filename = %q, want %q", gotFilename, want)
	}
}

func TestMultipartUpload_FileContentReadable(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "SKILL.md", "---\nname: test\n---\n")

	_, err := MultipartUpload(context.Background(), []string{p}, "mydir", func(files []io.Reader) (string, error) {
		data, err := io.ReadAll(files[0])
		if err != nil {
			return "", err
		}
		return string(data), nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMultipartUpload_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	p1 := writeFile(t, dir, "SKILL.md", "skill content")
	p2 := writeFile(t, dir, "extra.md", "extra content")

	var gotCount int
	_, _ = MultipartUpload(context.Background(), []string{p1, p2}, "mydir", func(files []io.Reader) (string, error) {
		gotCount = len(files)
		return "ok", nil
	})

	if gotCount != 2 {
		t.Errorf("fn received %d files, want 2", gotCount)
	}
}

func TestMultipartUpload_FilesReopenedOnRetry(t *testing.T) {
	noBackoff(t)
	dir := t.TempDir()
	p := writeFile(t, dir, "SKILL.md", "data")

	// Collect all file reads across attempts to confirm fresh opens on each retry.
	var contents []string
	MultipartUpload(context.Background(), []string{p}, "mydir", func(files []io.Reader) (string, error) { //nolint:errcheck
		data, _ := io.ReadAll(files[0])
		contents = append(contents, string(data))
		if len(contents) < 2 {
			return "", apiErr(500)
		}
		return "ok", nil
	})

	for i, c := range contents {
		if c != "data" {
			t.Errorf("attempt %d: file content = %q, want %q", i+1, c, "data")
		}
	}
	if len(contents) != 2 {
		t.Errorf("got %d attempts, want 2", len(contents))
	}
}
