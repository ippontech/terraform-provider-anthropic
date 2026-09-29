// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/packages/respjson"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"golang.org/x/text/unicode/norm"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &MemoryResource{}
var _ resource.ResourceWithImportState = &MemoryResource{}

func NewMemoryResource() resource.Resource {
	return &MemoryResource{}
}

// MemoryResource defines the resource implementation.
type MemoryResource struct {
	client *anthropic.Client
}

// MemoryResourceModel describes the resource data model.
type MemoryResourceModel struct {
	ID               types.String `tfsdk:"id"`
	MemoryStoreID    types.String `tfsdk:"memory_store_id"`
	Path             types.String `tfsdk:"path"`
	Content          types.String `tfsdk:"content"`
	ContentSha256    types.String `tfsdk:"content_sha256"`
	ContentSizeBytes types.Int64  `tfsdk:"content_size_bytes"`
	MemoryVersionID  types.String `tfsdk:"memory_version_id"`
	Type             types.String `tfsdk:"type"`
	CreatedAt        types.String `tfsdk:"created_at"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
}

const maxMemoryContentBytes = 102400 // 100 kB

// --- Path validator ---

// memoryPathValidator enforces the memory path shape documented by the API:
// starts with "/", at least one non-empty segment, no "." or ".." segments,
// no empty segments, at most 1024 bytes, no control or format characters
// (including the line/paragraph separators U+2028/U+2029), and NFC-normalized.
type memoryPathValidator struct{}

func (v memoryPathValidator) Description(_ context.Context) string {
	return "must start with '/', contain at least one non-empty segment, be at most 1024 bytes, contain no empty, '.' or '..' segments, " +
		"contain no control or format characters (including U+2028/U+2029), and be NFC-normalized"
}

func (v memoryPathValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v memoryPathValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if err := validateMemoryPath(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid path", err.Error())
	}
}

// lineParagraphSeparators holds U+2028 (LINE SEPARATOR) and U+2029 (PARAGRAPH
// SEPARATOR): both are category Zl/Zp, so unicode.IsControl and the Cf
// (format) category check below don't catch them, but the API rejects them
// alongside true control/format characters.
var lineParagraphSeparators = []rune{' ', ' '}

func validateMemoryPath(p string) error {
	if len(p) > 1024 {
		return fmt.Errorf("path must be at most 1024 bytes, got %d", len(p))
	}
	if !strings.HasPrefix(p, "/") {
		return errors.New("path must start with '/'")
	}
	for _, r := range p {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) || slices.Contains(lineParagraphSeparators, r) {
			return fmt.Errorf("path must not contain control or format characters, found %U", r)
		}
	}
	if !norm.NFC.IsNormalString(p) {
		return errors.New("path must be NFC-normalized")
	}
	segments := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(segments) == 0 {
		return errors.New("path must contain at least one non-empty segment")
	}
	nonEmpty := false
	for _, seg := range segments {
		switch seg {
		case "":
			return errors.New("path must not contain empty segments")
		case ".", "..":
			return errors.New("path must not contain '.' or '..' segments")
		default:
			nonEmpty = true
		}
	}
	if !nonEmpty {
		return errors.New("path must contain at least one non-empty segment")
	}
	return nil
}

// --- Schema ---

func (r *MemoryResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_memory"
}

func (r *MemoryResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages a memory: a single text document at a hierarchical path inside an " +
			"`anthropic_memory_store` (beta). Updates are guarded by an optimistic-concurrency precondition on " +
			"`content_sha256`: if the memory was modified out-of-band since the last read, the API returns a " +
			"`memory_precondition_failed_error` (HTTP 409) and this provider surfaces it as an error rather than " +
			"clobbering the remote content — run `terraform plan`/`refresh` to pick up the new content, then re-apply.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"memory_store_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the memory store this memory belongs to (a `memstore_...` value).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"path": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Hierarchical path of the memory within the store, e.g. `/projects/foo/notes.md`. " +
					"Must start with `/`, contain at least one non-empty segment, and be at most 1024 bytes. Must not " +
					"contain empty, `.` or `..` segments. Case-sensitive and unique within the store. Changing this " +
					"renames the memory in place; the `id` is preserved.",
				Validators: []validator.String{memoryPathValidator{}},
			},
			"content": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "UTF-8 text content of the memory. Maximum 100 kB (102,400 bytes).",
				Validators:          []validator.String{&memoryContentSizeValidator{}},
			},

			// --- Computed ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique identifier for the memory assigned by the API (e.g. `mem_...`). Stable across renames.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"content_sha256": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Lowercase hex SHA-256 digest of the UTF-8 `content` bytes (64 characters). Used as the optimistic-concurrency precondition on update.",
			},
			"content_size_bytes": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Size of `content` in bytes (the UTF-8 plaintext length).",
			},
			"memory_version_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ID of the `memory_version` representing this memory's current content (a `memver_...` value). Changes on every mutation.",
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type. Always `memory`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Creation timestamp (RFC 3339).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Last update timestamp (RFC 3339).",
			},
		},
	}
}

// memoryContentSizeValidator enforces the 100 kB content cap client-side so a
// plan-time error is clearer than the API's 4xx.
type memoryContentSizeValidator struct{}

func (v *memoryContentSizeValidator) Description(_ context.Context) string {
	return fmt.Sprintf("must be at most %d bytes", maxMemoryContentBytes)
}

func (v *memoryContentSizeValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v *memoryContentSizeValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if size := len(req.ConfigValue.ValueString()); size > maxMemoryContentBytes {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid content",
			fmt.Sprintf("content must be at most %d bytes (100 kB), got %d", maxMemoryContentBytes, size),
		)
	}
}

// --- Configure ---

func (r *MemoryResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// --- Create ---

func (r *MemoryResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data MemoryResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaMemoryStoreMemoryNewParams{
		Content: param.NewOpt(data.Content.ValueString()),
		Path:    data.Path.ValueString(),
		View:    anthropic.BetaManagedAgentsMemoryViewFull,
	}

	memory, err := r.client.Beta.MemoryStores.Memories.New(ctx, data.MemoryStoreID.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create memory: %s", err))
		return
	}

	resp.Diagnostics.Append(mapMemoryToState(memory, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *MemoryResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data MemoryResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	memory, err := r.client.Beta.MemoryStores.Memories.Get(ctx, data.ID.ValueString(), anthropic.BetaMemoryStoreMemoryGetParams{
		MemoryStoreID: data.MemoryStoreID.ValueString(),
		View:          anthropic.BetaManagedAgentsMemoryViewFull,
	})
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read memory: %s", err))
		return
	}

	priorContent := data.Content

	resp.Diagnostics.Append(mapMemoryToState(memory, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// With View: full the API always populates content, but guard against a
	// basic-view response (content explicitly null) clobbering state with an
	// empty string.
	if memory.JSON.Content.Raw() == respjson.Null {
		data.Content = priorContent
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *MemoryResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data MemoryResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state MemoryResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaMemoryStoreMemoryUpdateParams{
		MemoryStoreID: state.MemoryStoreID.ValueString(),
		View:          anthropic.BetaManagedAgentsMemoryViewFull,
		Precondition: anthropic.BetaManagedAgentsPreconditionParam{
			Type:          anthropic.BetaManagedAgentsPreconditionTypeContentSha256,
			ContentSha256: param.NewOpt(state.ContentSha256.ValueString()),
		},
	}

	if !data.Content.Equal(state.Content) {
		params.Content = param.NewOpt(data.Content.ValueString())
	}
	if !data.Path.Equal(state.Path) {
		params.Path = param.NewOpt(data.Path.ValueString())
	}

	memory, err := r.client.Beta.MemoryStores.Memories.Update(ctx, state.ID.ValueString(), params)
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 409 && string(apierr.Type()) == memoryPreconditionFailedErrorType {
			resp.Diagnostics.AddError(
				"Memory Modified Out-Of-Band",
				fmt.Sprintf("Memory %q was modified since it was last read (content_sha256 precondition failed). "+
					"Run terraform plan/refresh to pick up the current remote content before re-applying.",
					state.ID.ValueString()),
			)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update memory: %s", err))
		return
	}

	awaitMemoryUpdateVisible(ctx, r.client, state.MemoryStoreID.ValueString(), state.ID.ValueString(), memory.UpdatedAt, memory.ContentSha256, memoryConsistencyTimeout, memoryConsistencyInterval)

	resp.Diagnostics.Append(mapMemoryToState(memory, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// memoryPreconditionFailedErrorType is the API's error type for a failed
// content_sha256 precondition on update, distinguishing it from any other 409
// (e.g. a rename colliding with an existing path) that a generic "modified
// out-of-band" message could not help the operator fix.
const memoryPreconditionFailedErrorType = "memory_precondition_failed_error"

// The production bounds of the read-after-write consistency wait below. They
// are consts, and awaitMemoryUpdateVisible takes them as arguments, so the
// unit tests can shrink the loop to milliseconds without mutating shared
// state — mirrors vaultConsistencyTimeout/Interval in vault_resource.go.
const (
	memoryConsistencyTimeout  = 5 * time.Second
	memoryConsistencyInterval = 200 * time.Millisecond
)

// isTerminalMemoryReadError reports whether a Get failure is one the poll can
// never recover from: the memory (or its store) is gone, or the key no longer
// has access to it. Retrying those until the deadline would stall the apply
// for seconds on a read that will never converge.
func isTerminalMemoryReadError(err error) bool {
	var apierr *anthropic.Error
	if !errors.As(err, &apierr) {
		return false
	}

	switch apierr.StatusCode {
	case 401, 403, 404:
		return true
	default:
		return false
	}
}

// awaitMemoryUpdateVisible polls Get until the stored memory is at least as
// new as writtenAt (the updated_at returned by the write itself) and its
// content_sha256 matches the write response.
//
// This endpoint has not been probed for read-after-write staleness the way
// vaults and WIF were (see CLAUDE.md); the wait is added defensively because
// Update's next apply builds its content_sha256 precondition from whatever
// Terraform's post-apply refresh reads, and a stale read there would turn
// into a spurious 409 on an unrelated next apply, not just a phantom diff.
//
// It is deliberately best-effort: on a read error, a timeout, or a cancelled
// context it returns without reporting a diagnostic. The write has already
// succeeded, so failing the apply here would turn a cosmetic staleness window
// into a hard error.
func awaitMemoryUpdateVisible(ctx context.Context, client *anthropic.Client, memoryStoreID, memoryID string, writtenAt time.Time, writtenSha256 string, timeout, interval time.Duration) {
	deadline := time.Now().Add(timeout)

	for {
		memory, err := client.Beta.MemoryStores.Memories.Get(ctx, memoryID, anthropic.BetaMemoryStoreMemoryGetParams{
			MemoryStoreID: memoryStoreID,
		})
		switch {
		case err == nil:
			if !memory.UpdatedAt.Before(writtenAt) && memory.ContentSha256 == writtenSha256 {
				return
			}
		case isTerminalMemoryReadError(err):
			tflog.Warn(ctx, "memory became unreadable while waiting for the update to be visible; giving up on the consistency wait", map[string]any{
				"memory_id": memoryID,
				"error":     err.Error(),
			})
			return
		}

		if time.Now().After(deadline) {
			tflog.Warn(ctx, "memory update not visible before the consistency timeout; the next plan may show a transient diff or a spurious precondition failure", map[string]any{
				"memory_id": memoryID,
				"timeout":   timeout.String(),
			})
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// --- Delete ---

func (r *MemoryResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data MemoryResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.Beta.MemoryStores.Memories.Delete(ctx, data.ID.ValueString(), anthropic.BetaMemoryStoreMemoryDeleteParams{
		MemoryStoreID: data.MemoryStoreID.ValueString(),
	})
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete memory: %s", err))
	}
}

// --- ImportState ---

func (r *MemoryResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	memoryStoreID, memoryID, err := parseMemoryImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("memory_store_id"), memoryStoreID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), memoryID)...)
}

// parseMemoryImportID splits a composite "<memory_store_id>:<memory_id>"
// import ID into its two parts.
func parseMemoryImportID(id string) (memoryStoreID, memoryID string, err error) {
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected import ID in the form <memory_store_id>:<memory_id>, got %q", id)
	}
	return parts[0], parts[1], nil
}

// ============================================================================
// Helper functions
// ============================================================================

// mapMemoryToState maps the API response into the resource's state model.
func mapMemoryToState(memory *anthropic.BetaManagedAgentsMemory, data *MemoryResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.ID = types.StringValue(memory.ID)
	data.MemoryStoreID = types.StringValue(memory.MemoryStoreID)
	data.Path = types.StringValue(memory.Path)
	data.Content = types.StringValue(memory.Content)
	data.ContentSha256 = types.StringValue(memory.ContentSha256)
	data.ContentSizeBytes = types.Int64Value(memory.ContentSizeBytes)
	data.MemoryVersionID = types.StringValue(memory.MemoryVersionID)
	data.Type = types.StringValue(string(memory.Type))
	data.CreatedAt = types.StringValue(memory.CreatedAt.Format(time.RFC3339))
	data.UpdatedAt = types.StringValue(memory.UpdatedAt.Format(time.RFC3339))

	return diags
}
