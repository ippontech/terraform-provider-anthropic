// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package retry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

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
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("writeFile mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
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
	result, err := MultipartUpload(context.Background(), []string{p}, dir, "mydir", func(_ []io.Reader) (string, error) {
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

// MultipartUpload makes a single attempt whatever the error: the client's
// retry middleware replays the buffered body on 429, and a second loop here
// would multiply its attempts (nine POSTs for one 5xx before the fix).
func TestMultipartUpload_DoesNotRetry(t *testing.T) {
	for _, status := range []int{429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			dir := t.TempDir()
			p := writeFile(t, dir, "SKILL.md", "content")

			calls := 0
			_, err := MultipartUpload(context.Background(), []string{p}, dir, "mydir", func(_ []io.Reader) (string, error) {
				calls++
				return "", apiErr(status)
			})

			var apierr *anthropic.Error
			if !errors.As(err, &apierr) || apierr.StatusCode != status {
				t.Errorf("err = %v, want the %d API error", err, status)
			}
			if calls != 1 {
				t.Errorf("fn called %d times, want 1", calls)
			}
		})
	}
}

func TestMultipartUpload_NoRetryOn4xx(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "SKILL.md", "content")

	calls := 0
	_, err := MultipartUpload(context.Background(), []string{p}, dir, "mydir", func(_ []io.Reader) (string, error) {
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
	_, err := MultipartUpload(context.Background(), []string{p}, dir, "mydir", func(_ []io.Reader) (string, error) {
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
	_, err := MultipartUpload(context.Background(), []string{"/nonexistent/path/SKILL.md"}, "/nonexistent/path", "mydir", func(_ []io.Reader) (string, error) {
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

func TestMultipartUpload_ContextCancelled(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "SKILL.md", "content")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	_, err := MultipartUpload(ctx, []string{p}, dir, "mydir", func(_ []io.Reader) (string, error) {
		calls++
		return "ok", nil
	})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if calls != 0 {
		t.Errorf("fn called %d times, want 0", calls)
	}
}

func TestMultipartUpload_FileNamingUseDirName(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "SKILL.md", "content")

	var gotFilename string
	_, err := MultipartUpload(context.Background(), []string{p}, dir, "myskill", func(files []io.Reader) (string, error) {
		named, ok := files[0].(interface{ Filename() string })
		if !ok {
			t.Fatalf("reader does not satisfy Filename(): %T", files[0])
		}
		gotFilename = named.Filename()
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "myskill/SKILL.md"
	if gotFilename != want {
		t.Errorf("multipart filename = %q, want %q", gotFilename, want)
	}
}

func TestMultipartUpload_PreservesNestedSubdirectories(t *testing.T) {
	dir := t.TempDir()
	skillPath := writeFile(t, dir, "SKILL.md", "skill")
	refPath := writeFile(t, dir, "references/template.md", "template")

	var gotFilenames []string
	_, err := MultipartUpload(context.Background(), []string{skillPath, refPath}, dir, "myskill", func(files []io.Reader) (string, error) {
		for i, f := range files {
			named, ok := f.(interface{ Filename() string })
			if !ok {
				t.Fatalf("reader[%d] does not satisfy Filename(): %T", i, f)
			}
			gotFilenames = append(gotFilenames, named.Filename())
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantFilenames := []string{
		"myskill/SKILL.md",
		"myskill/references/template.md",
	}
	if len(gotFilenames) != len(wantFilenames) {
		t.Fatalf("got %d filenames, want %d (%v)", len(gotFilenames), len(wantFilenames), gotFilenames)
	}
	for i, want := range wantFilenames {
		if gotFilenames[i] != want {
			t.Errorf("filename[%d] = %q, want %q", i, gotFilenames[i], want)
		}
	}
}

func TestMultipartUpload_RejectsFileOutsideBundleRoot(t *testing.T) {
	outerDir := t.TempDir()
	innerDir := filepath.Join(outerDir, "bundle")
	outsidePath := writeFile(t, outerDir, "outside.md", "x")
	insidePath := writeFile(t, innerDir, "SKILL.md", "s")

	calls := 0
	_, err := MultipartUpload(context.Background(), []string{insidePath, outsidePath}, innerDir, "myskill", func(_ []io.Reader) (string, error) {
		calls++
		return "ok", nil
	})

	if err == nil {
		t.Fatal("expected error when file lies outside bundleRoot")
	}
	if !strings.Contains(err.Error(), "not inside bundle root") {
		t.Errorf("expected 'not inside bundle root' in error, got: %v", err)
	}
	if calls != 0 {
		t.Errorf("fn called %d times, want 0", calls)
	}
}

func TestMultipartUpload_FileContentReadable(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "SKILL.md", "---\nname: test\n---\n")

	_, err := MultipartUpload(context.Background(), []string{p}, dir, "mydir", func(files []io.Reader) (string, error) {
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
	_, err := MultipartUpload(context.Background(), []string{p1, p2}, dir, "mydir", func(files []io.Reader) (string, error) {
		gotCount = len(files)
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotCount != 2 {
		t.Errorf("fn received %d files, want 2", gotCount)
	}
}

func TestMultipartUpload_RejectsBundleRootItselfAsFile(t *testing.T) {
	dir := t.TempDir()

	calls := 0
	_, err := MultipartUpload(context.Background(), []string{dir}, dir, "myskill", func(_ []io.Reader) (string, error) {
		calls++
		return "ok", nil
	})

	if err == nil {
		t.Fatal("expected error when file path equals bundleRoot")
	}
	if !strings.Contains(err.Error(), "not inside bundle root") {
		t.Errorf("expected 'not inside bundle root' in error, got: %v", err)
	}
	if calls != 0 {
		t.Errorf("fn called %d times, want 0", calls)
	}
}
