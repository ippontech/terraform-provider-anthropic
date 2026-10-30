// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	provretry "github.com/ippontech/terraform-provider-anthropic/internal/retry"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

func TestMapFileToState(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	file := &anthropic.FileMetadata{
		ID:           "file_abc123",
		CreatedAt:    created,
		Filename:     "document.pdf",
		MimeType:     "application/pdf",
		SizeBytes:    1024,
		Downloadable: false,
	}

	var data FileResourceModel
	mapFileToState(file, &data)

	if got, want := data.ID.ValueString(), "file_abc123"; got != want {
		t.Errorf("ID = %q, want %q", got, want)
	}
	if got, want := data.Filename.ValueString(), "document.pdf"; got != want {
		t.Errorf("Filename = %q, want %q", got, want)
	}
	if got, want := data.MimeType.ValueString(), "application/pdf"; got != want {
		t.Errorf("MimeType = %q, want %q", got, want)
	}
	if got, want := data.SizeBytes.ValueInt64(), int64(1024); got != want {
		t.Errorf("SizeBytes = %d, want %d", got, want)
	}
	if got, want := data.CreatedAt.ValueString(), "2026-01-02T03:04:05Z"; got != want {
		t.Errorf("CreatedAt = %q, want %q", got, want)
	}
	if data.Downloadable.ValueBool() {
		t.Errorf("Downloadable = true, want false")
	}
}

func TestComputeFileHash(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "content.txt")
	content := []byte("hello, files api")
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatalf("WriteFile: %s", err)
	}

	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])

	got, diags := computeFileHash(p)
	if diags.HasError() {
		t.Fatalf("computeFileHash returned errors: %+v", diags)
	}
	if got != want {
		t.Errorf("computeFileHash = %q, want %q", got, want)
	}
}

func TestComputeFileHash_missingFile(t *testing.T) {
	t.Parallel()

	_, diags := computeFileHash(filepath.Join(t.TempDir(), "does-not-exist.txt"))
	if !diags.HasError() {
		t.Fatal("expected an error for a missing file, got none")
	}
}

func TestFilenameValidator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		value     string
		wantError bool
	}{
		{name: "valid simple name", value: "document.pdf", wantError: false},
		{name: "empty", value: "", wantError: true},
		{name: "too long", value: repeatChar('a', 256), wantError: true},
		{name: "forbidden slash", value: "a/b.txt", wantError: true},
		{name: "forbidden backslash", value: `a\b.txt`, wantError: true},
		{name: "forbidden angle bracket", value: "a<b.txt", wantError: true},
		{name: "forbidden colon", value: "a:b.txt", wantError: true},
		{name: "forbidden quote", value: `a"b.txt`, wantError: true},
		{name: "forbidden pipe", value: "a|b.txt", wantError: true},
		{name: "forbidden question mark", value: "a?.txt", wantError: true},
		{name: "forbidden asterisk", value: "a*.txt", wantError: true},
		{name: "control character", value: "a\x01b.txt", wantError: true},
		{name: "max length exactly 255", value: repeatChar('a', 255), wantError: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := validator.StringRequest{
				Path:        path.Root("filename"),
				ConfigValue: types.StringValue(tt.value),
			}
			resp := &validator.StringResponse{}
			filenameValidator{}.ValidateString(context.Background(), req, resp)

			if got := resp.Diagnostics.HasError(); got != tt.wantError {
				t.Errorf("HasError = %v, want %v (diags: %+v)", got, tt.wantError, resp.Diagnostics)
			}
		})
	}
}

func repeatChar(c byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func TestFilenameValidator_nullAndUnknownIgnored(t *testing.T) {
	t.Parallel()

	for _, v := range []types.String{types.StringNull(), types.StringUnknown()} {
		req := validator.StringRequest{
			Path:        path.Root("filename"),
			ConfigValue: v,
		}
		resp := &validator.StringResponse{}
		filenameValidator{}.ValidateString(context.Background(), req, resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("expected no error for %+v, got %+v", v, resp.Diagnostics)
		}
	}
}

// ============================================================================
// uploadFile
// ============================================================================

// newUploadClient builds a client with the provider's production retry
// policy, as newSDKClient does. Building it with option.WithMaxRetries(0)
// instead used to hide the SDK's own retries, which turned one 5xx into three
// POST /v1/files.
func newUploadClient(t *testing.T, srv *httptest.Server) *anthropic.Client {
	t.Helper()
	c := anthropic.NewClient(append([]option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(srv.URL),
		option.WithAPIKey("test"),
	}, provretry.SDKOptions(provretry.DefaultPolicy)...)...)
	return &c
}

const uploadedFileJSON = `{"id":"file_abc","filename":"f.txt","mime_type":"text/plain","size_bytes":5,"created_at":"2026-01-01T00:00:00Z","downloadable":false,"type":"file"}`

func TestUploadFile_success(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(uploadedFileJSON))
	}))
	defer srv.Close()

	file, err := uploadFile(context.Background(), newUploadClient(t, srv), writeTempFile(t, "hello"), "f.txt", "text/plain", param.Opt[int64]{})
	if err != nil {
		t.Fatalf("uploadFile: %s", err)
	}
	if file.ID != "file_abc" {
		t.Errorf("ID = %q, want file_abc", file.ID)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
}

// A 5xx is not retried: the file may already have been created, and a
// replay would upload a duplicate. The server asks for a 1ms retry delay, so
// a retrying client would be quick to show up here.
func TestUploadFile_serverErrorNotRetried(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("retry-after-ms", "1")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"boom"}}`))
	}))
	defer srv.Close()

	_, err := uploadFile(context.Background(), newUploadClient(t, srv), writeTempFile(t, "hello"), "f.txt", "text/plain", param.Opt[int64]{})
	if err == nil {
		t.Fatal("expected an error, got none")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1 (5xx must not be retried)", got)
	}
}

// A 429 is retried by the client, with the same multipart body.
func TestUploadFile_rateLimitedThenSucceeds(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	var bodies [2]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if b, err := io.ReadAll(r.Body); err == nil && n <= 2 {
			bodies[n-1] = string(b)
		}
		if n == 1 {
			w.Header().Set("retry-after-ms", "10")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(uploadedFileJSON))
	}))
	defer srv.Close()

	file, err := uploadFile(context.Background(), newUploadClient(t, srv), writeTempFile(t, "hello"), "f.txt", "text/plain", param.Opt[int64]{})
	if err != nil {
		t.Fatalf("uploadFile: %s", err)
	}
	if file.ID != "file_abc" {
		t.Errorf("ID = %q, want file_abc", file.ID)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2 (one 429, one success)", got)
	}
	if !strings.Contains(bodies[1], "hello") || bodies[0] != bodies[1] {
		t.Errorf("retried body differs or lacks the file content:\n%q\n%q", bodies[0], bodies[1])
	}
}

// ============================================================================
// detectMimeType
// ============================================================================

func TestDetectMimeType(t *testing.T) {
	t.Parallel()

	if got, want := detectMimeType("document.pdf"), "application/pdf"; got != want {
		t.Errorf("detectMimeType(document.pdf) = %q, want %q", got, want)
	}
	if got, want := detectMimeType("no-extension"), "application/octet-stream"; got != want {
		t.Errorf("detectMimeType(no-extension) = %q, want %q", got, want)
	}
}

func TestUploadFile_missingFile(t *testing.T) {
	t.Parallel()

	client := anthropic.NewClient(option.WithAPIKey("test"))
	_, err := uploadFile(context.Background(), &client, filepath.Join(t.TempDir(), "nope.txt"), "f.txt", "text/plain", param.Opt[int64]{})
	if err == nil {
		t.Fatal("expected an error for a missing source file, got none")
	}
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "src.txt")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %s", err)
	}
	return p
}

// ============================================================================
// sourceHashPlanModifier
// ============================================================================

func TestSourceHashPlanModifier_computesHashFromSourcePath(t *testing.T) {
	t.Parallel()

	content := "hello, files api"
	p := writeTempFile(t, content)
	sum := sha256.Sum256([]byte(content))
	wantHash := hex.EncodeToString(sum[:])

	resp := runSourceHashPlanModifier(t, p, types.StringUnknown())

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %+v", resp.Diagnostics)
	}
	if got := resp.PlanValue.ValueString(); got != wantHash {
		t.Errorf("PlanValue = %q, want %q", got, wantHash)
	}
}

func TestSourceHashPlanModifier_respectsExplicitConfigValue(t *testing.T) {
	t.Parallel()

	p := writeTempFile(t, "hello")
	resp := runSourceHashPlanModifier(t, p, types.StringValue("pinned-hash"))

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %+v", resp.Diagnostics)
	}
	// The modifier must leave PlanValue exactly as the framework seeded it
	// (the config value) rather than overwriting it with a computed hash.
	if got := resp.PlanValue.ValueString(); got != "pinned-hash" {
		t.Errorf("PlanValue = %q, want unmodified \"pinned-hash\"", got)
	}
}

func TestSourceHashPlanModifier_detectsOutOfBandContentChange(t *testing.T) {
	t.Parallel()

	p := writeTempFile(t, "original content")
	sum1 := sha256.Sum256([]byte("original content"))
	hash1 := hex.EncodeToString(sum1[:])

	resp1 := runSourceHashPlanModifier(t, p, types.StringUnknown())
	if got := resp1.PlanValue.ValueString(); got != hash1 {
		t.Fatalf("initial PlanValue = %q, want %q", got, hash1)
	}

	if err := os.WriteFile(p, []byte("changed content"), 0o600); err != nil {
		t.Fatalf("WriteFile: %s", err)
	}
	sum2 := sha256.Sum256([]byte("changed content"))
	hash2 := hex.EncodeToString(sum2[:])

	resp2 := runSourceHashPlanModifier(t, p, types.StringUnknown())
	if got := resp2.PlanValue.ValueString(); got != hash2 {
		t.Errorf("PlanValue after edit = %q, want %q", got, hash2)
	}
	if hash1 == hash2 {
		t.Fatal("test setup error: both contents hashed the same")
	}
}

func TestSourceHashPlanModifier_missingFileAtPlanTimeIsIgnored(t *testing.T) {
	t.Parallel()

	resp := runSourceHashPlanModifier(t, filepath.Join(t.TempDir(), "does-not-exist.txt"), types.StringUnknown())

	if resp.Diagnostics.HasError() {
		t.Errorf("expected no diagnostics for a missing file at plan time, got %+v", resp.Diagnostics)
	}
	if !resp.PlanValue.IsNull() && !resp.PlanValue.IsUnknown() {
		t.Errorf("PlanValue = %q, want untouched (null/unknown)", resp.PlanValue.ValueString())
	}
}

// runSourceHashPlanModifier builds a minimal tfsdk.Plan for FileResource with
// source_path set to sourcePath and invokes sourceHashPlanModifier against it,
// simulating the source_hash attribute's ConfigValue.
func runSourceHashPlanModifier(t *testing.T, sourcePath string, configValue types.String) *planmodifier.StringResponse {
	t.Helper()

	r := &FileResource{}
	obj := schematest.ResourceObjectType(t, r)
	vals := schematest.NullValues(t, r)
	vals["source_path"] = tftypes.NewValue(tftypes.String, sourcePath)
	vals["source_hash"] = tftypes.NewValue(tftypes.String, nil)

	plan := tfsdk.Plan{
		Raw:    tftypes.NewValue(obj, vals),
		Schema: schematest.ResourceSchema(t, r),
	}

	req := planmodifier.StringRequest{
		Path:        path.Root("source_hash"),
		ConfigValue: configValue,
		Plan:        plan,
	}
	resp := &planmodifier.StringResponse{PlanValue: configValue}
	sourceHashPlanModifier{}.PlanModifyString(context.Background(), req, resp)
	return resp
}
