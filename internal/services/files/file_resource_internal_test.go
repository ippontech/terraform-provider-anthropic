// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestMapFileToState(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	file := &anthropic.BetaFileMetadata{
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
