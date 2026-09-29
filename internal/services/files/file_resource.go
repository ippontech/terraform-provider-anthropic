// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
	"unicode"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// maxFileSizeBytes is the Files API's documented per-file limit (500 MB).
// See https://platform.claude.com/docs/en/build-with-claude/files#storage-limits.
const maxFileSizeBytes = 500 * 1024 * 1024

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &FileResource{}
var _ resource.ResourceWithImportState = &FileResource{}

func NewFileResource() resource.Resource {
	return &FileResource{}
}

// FileResource defines the resource implementation.
type FileResource struct {
	client *anthropic.Client
}

// FileResourceModel describes the resource data model.
type FileResourceModel struct {
	ID           types.String `tfsdk:"id"`
	SourcePath   types.String `tfsdk:"source_path"`
	SourceHash   types.String `tfsdk:"source_hash"`
	Filename     types.String `tfsdk:"filename"`
	MimeType     types.String `tfsdk:"mime_type"`
	SizeBytes    types.Int64  `tfsdk:"size_bytes"`
	CreatedAt    types.String `tfsdk:"created_at"`
	Downloadable types.Bool   `tfsdk:"downloadable"`
}

func (r *FileResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_file"
}

func (r *FileResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Uploads and manages a file on the Anthropic platform (Files API, beta). " +
			"Files are uploaded once and referenced by `id` from Messages requests. The file's content is " +
			"never stored in Terraform state; only its local path, a content hash used to detect local " +
			"changes, and the metadata returned by the API are tracked.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique file identifier assigned by the API.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"source_path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Local path to the file to upload. Changing this forces a new resource.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"source_hash": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "SHA256 hash (hex-encoded) of the local file's content. Computed automatically from " +
					"`source_path` on every plan, so an out-of-band change to the file's content is detected and " +
					"forces a new resource even though `source_path` itself did not change.",
				PlanModifiers: []planmodifier.String{sourceHashPlanModifier{}, stringplanmodifier.RequiresReplace()},
			},
			"filename": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Filename recorded for the uploaded file. Defaults to the base name of `source_path`. " +
					"1-255 characters; cannot contain `<`, `>`, `:`, `\"`, `|`, `?`, `*`, `\\`, `/`, or Unicode control characters 0-31.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{filenameValidator{}},
			},
			"mime_type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "MIME type of the file. Detected from `source_path` if not set.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"size_bytes": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Size of the file in bytes.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the file was created.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"downloadable": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the file can be downloaded. Always `false` for files uploaded by this resource.",
			},
		},
	}
}

func (r *FileResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*providerdata.ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *providerdata.ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	if !providerrors.RequireResourceAPIClient(pd.Client, &resp.Diagnostics) {
		return
	}

	r.client = pd.Client
}

func (r *FileResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data FileResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sourcePath := data.SourcePath.ValueString()

	info, err := os.Stat(sourcePath)
	if err != nil {
		resp.Diagnostics.AddError("Invalid source_path", fmt.Sprintf("Unable to access file %q: %s", sourcePath, err))
		return
	}
	if info.Size() > maxFileSizeBytes {
		resp.Diagnostics.AddError(
			"File too large",
			fmt.Sprintf("File %q is %d bytes, which exceeds the Files API's 500 MB per-file limit.", sourcePath, info.Size()),
		)
		return
	}

	filename := info.Name()
	if !data.Filename.IsNull() && !data.Filename.IsUnknown() {
		filename = data.Filename.ValueString()
	}

	// The Anthropic SDK cannot retry a streaming multipart body on its own
	// (see internal/retry's package doc), but that package's MultipartUpload
	// helper is shaped for a bundle of files uploaded together under a common
	// directory name (skills), not a single flat file with an explicit
	// filename. Reusing it would force an artificial "dirName/filename"
	// multipart name that does not match this resource's `filename`
	// attribute, so a small local retry loop is used instead.
	file, err := uploadFileWithRetry(ctx, r.client, sourcePath, filename)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to upload file: %s", err))
		return
	}

	hash, diags := computeFileHash(sourcePath)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.SourceHash = types.StringValue(hash)

	mapFileToState(file, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *FileResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data FileResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	file, err := r.client.Beta.Files.GetMetadata(ctx, data.ID.ValueString(), anthropic.BetaFileGetMetadataParams{})
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read file: %s", err))
		return
	}

	mapFileToState(file, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update is a no-op: every mutable-looking attribute is RequiresReplace, and
// the Files API has no update/rename endpoint (files "cannot be modified or
// renamed after upload").
func (r *FileResource) Update(_ context.Context, _ resource.UpdateRequest, _ *resource.UpdateResponse) {
}

func (r *FileResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data FileResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.Beta.Files.Delete(ctx, data.ID.ValueString(), anthropic.BetaFileDeleteParams{})
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			// Already gone — treat as success.
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete file: %s", err))
		return
	}
}

func (r *FileResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// uploadFileWithRetry opens sourcePath fresh on each attempt and uploads it,
// retrying up to 3 times with a short backoff on 5xx API errors — multipart
// uploads set req.Body without req.GetBody, so the SDK's own retry logic
// (which requires a replayable body) never fires for them.
func uploadFileWithRetry(ctx context.Context, client *anthropic.Client, sourcePath, filename string) (*anthropic.BetaFileMetadata, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 5 * time.Second):
			}
		}

		f, err := os.Open(sourcePath)
		if err != nil {
			return nil, fmt.Errorf("unable to open file %q: %w", sourcePath, err)
		}

		named := namedReader{Reader: f, name: filename}
		file, err := client.Beta.Files.Upload(ctx, anthropic.BetaFileUploadParams{File: named})
		_ = f.Close()
		if err == nil {
			return file, nil
		}

		var apierr *anthropic.Error
		if !errors.As(err, &apierr) || apierr.StatusCode < 500 {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

// namedReader wraps an io.Reader with an explicit multipart filename. The SDK
// encoder picks a multipart filename from a `Filename() string` method before
// falling back to `Name() string` or the struct field name, so this reports
// the resource's desired filename rather than the local path's base name.
type namedReader struct {
	io.Reader
	name string
}

func (r namedReader) Filename() string { return r.name }

var _ interface{ Filename() string } = namedReader{}

// computeFileHash returns the hex-encoded SHA256 hash of the file at path.
func computeFileHash(path string) (string, diag.Diagnostics) {
	var diags diag.Diagnostics

	f, err := os.Open(path)
	if err != nil {
		diags.AddError("Invalid source_path", fmt.Sprintf("Unable to open file %q: %s", path, err))
		return "", diags
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		diags.AddError("Invalid source_path", fmt.Sprintf("Unable to read file %q: %s", path, err))
		return "", diags
	}

	return hex.EncodeToString(h.Sum(nil)), diags
}

func mapFileToState(file *anthropic.BetaFileMetadata, data *FileResourceModel) {
	data.ID = types.StringValue(file.ID)
	data.Filename = types.StringValue(file.Filename)
	data.MimeType = types.StringValue(file.MimeType)
	data.SizeBytes = types.Int64Value(file.SizeBytes)
	data.CreatedAt = types.StringValue(file.CreatedAt.Format(time.RFC3339))
	data.Downloadable = types.BoolValue(file.Downloadable)
}

// ============================================================================
// source_hash plan modifier
// ============================================================================

// sourceHashPlanModifier keeps source_hash in sync with the actual content of
// source_path on every plan, unless the user has pinned an explicit value in
// config. Without this, an unconfigured Computed attribute would only be
// (re)computed once and then held via UseStateForUnknown, so a local edit to
// the file's content at the same path would go undetected.
type sourceHashPlanModifier struct{}

func (m sourceHashPlanModifier) Description(_ context.Context) string {
	return "Computes the SHA256 hash of the file at source_path so local content changes are detected."
}

func (m sourceHashPlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m sourceHashPlanModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	// Resource is being destroyed: nothing to compute, and source_path may
	// already be gone from the plan.
	if req.Plan.Raw.IsNull() {
		return
	}

	// The user pinned an explicit value in config: respect it as-is.
	if !req.ConfigValue.IsNull() && !req.ConfigValue.IsUnknown() {
		return
	}

	var sourcePath types.String
	diags := req.Plan.GetAttribute(ctx, path.Root("source_path"), &sourcePath)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || sourcePath.IsNull() || sourcePath.IsUnknown() {
		return
	}

	hash, hashDiags := computeFileHash(sourcePath.ValueString())
	if hashDiags.HasError() {
		// Surface nothing here: source_path itself is validated at apply
		// time in Create/Read, and a missing file at plan time (e.g. it is
		// created by an earlier step in the same apply) should not fail
		// planning.
		return
	}

	resp.PlanValue = types.StringValue(hash)
}

// ============================================================================
// filename validator
// ============================================================================

// filenameValidator enforces the Files API's documented filename rules:
// 1-255 characters, and none of the characters forbidden by the API.
type filenameValidator struct{}

func (v filenameValidator) Description(_ context.Context) string {
	return "filename must be 1-255 characters and must not contain <, >, :, \", |, ?, *, \\, /, or Unicode control characters 0-31"
}

func (v filenameValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v filenameValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	value := req.ConfigValue.ValueString()
	if len(value) < 1 || len(value) > 255 {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid filename", fmt.Sprintf("filename must be 1-255 characters, got %d", len(value)))
		return
	}

	const forbidden = "<>:\"|?*\\/"
	for _, r := range value {
		if unicode.IsControl(r) || containsRune(forbidden, r) {
			resp.Diagnostics.AddAttributeError(
				req.Path,
				"Invalid filename",
				fmt.Sprintf("filename must not contain %q or Unicode control characters 0-31, got %q", forbidden, value),
			)
			return
		}
	}
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}
