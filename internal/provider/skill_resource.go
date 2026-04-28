// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// namedReader wraps an io.Reader with an explicit multipart filename.
// The Anthropic SDK encoder checks for Filename() before falling back to
// path.Base(Name()), so this lets us send "dirname/file.md" instead of just "file.md".
// The API requires every file to be inside a named top-level folder.
type namedReader struct {
	io.Reader
	filename string
}

func (r namedReader) Filename() string { return r.filename }

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &SkillResource{}
var _ resource.ResourceWithImportState = &SkillResource{}

func NewSkillResource() resource.Resource {
	return &SkillResource{}
}

// SkillResource defines the resource implementation.
type SkillResource struct {
	client *anthropic.Client
}

// SkillResourceModel describes the resource data model.
type SkillResourceModel struct {
	ID            types.String `tfsdk:"id"`
	DisplayTitle  types.String `tfsdk:"display_title"`
	Files         types.List   `tfsdk:"files"`
	ForceDestroy  types.Bool   `tfsdk:"force_destroy"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
	LatestVersion types.String `tfsdk:"latest_version"`
	Source        types.String `tfsdk:"source"`
}

func (r *SkillResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skill"
}

func (r *SkillResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages a custom Skill on the Anthropic platform (beta). " +
			"Skills are uploaded from local files and can be attached to Managed Agents.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique skill identifier assigned by the API.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"display_title": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Human-readable display title for the skill. Not included in the prompt sent to the model.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"files": schema.ListAttribute{
				Required:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Local file paths to upload for the skill. Must include a `SKILL.md` file at the root of the directory.",
				PlanModifiers:       []planmodifier.List{listplanmodifier.RequiresReplace()},
				Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
			},
			"force_destroy": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
				MarkdownDescription: "When `true`, all skill versions are deleted before the skill is destroyed, " +
					"allowing Terraform to remove a skill that still has versions. " +
					"Defaults to `false`. Set to `true` only if you intend for all versions to be permanently deleted.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ISO 8601 timestamp of when the skill was created.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ISO 8601 timestamp of when the skill was last updated.",
			},
			"latest_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The latest version identifier for the skill.",
			},
			"source": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Source of the skill. For user-created skills this is `\"custom\"`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *SkillResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	r.client = pd.Client
}

func (r *SkillResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data SkillResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Open each file.
	var filePaths []string
	resp.Diagnostics.Append(data.Files.ElementsAs(ctx, &filePaths, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	files := make([]io.Reader, 0, len(filePaths))
	openedFiles := make([]*os.File, 0, len(filePaths))
	defer func() {
		for _, f := range openedFiles {
			_ = f.Close()
		}
	}()

	// The API requires every file to live inside a named top-level directory
	// (e.g. "myskill/SKILL.md"). We derive that directory name from the common
	// parent of the provided paths.
	dirName := filepath.Base(filepath.Dir(filePaths[0]))

	for _, p := range filePaths {
		f, err := os.Open(p)
		if err != nil {
			resp.Diagnostics.AddError("File Open Error", fmt.Sprintf("Unable to open file %q: %s", p, err))
			return
		}
		openedFiles = append(openedFiles, f)
		files = append(files, namedReader{Reader: f, filename: dirName + "/" + filepath.Base(p)})
	}

	params := anthropic.BetaSkillNewParams{
		Files: files,
	}

	if !data.DisplayTitle.IsNull() && !data.DisplayTitle.IsUnknown() {
		params.DisplayTitle = anthropic.String(data.DisplayTitle.ValueString())
	}

	skill, err := r.client.Beta.Skills.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create skill: %s", err))
		return
	}

	mapSkillNewResponseToState(skill, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *SkillResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data SkillResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	skill, err := r.client.Beta.Skills.Get(ctx, data.ID.ValueString(), anthropic.BetaSkillGetParams{})
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read skill: %s", err))
		return
	}

	mapSkillGetResponseToState(skill, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update is a no-op because all attributes are ForceNew.
func (r *SkillResource) Update(_ context.Context, _ resource.UpdateRequest, _ *resource.UpdateResponse) {
}

func (r *SkillResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data SkillResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	skillID := data.ID.ValueString()

	// The API requires all versions to be deleted before the skill itself can be deleted.
	// Only do this when force_destroy = true; otherwise surface the API error so the
	// user is aware that versions exist and must be removed explicitly.
	if data.ForceDestroy.ValueBool() {
		iter := r.client.Beta.Skills.Versions.ListAutoPaging(ctx, skillID, anthropic.BetaSkillVersionListParams{})
		for iter.Next() {
			v := iter.Current()
			_, err := r.client.Beta.Skills.Versions.Delete(ctx, v.Version, anthropic.BetaSkillVersionDeleteParams{SkillID: skillID})
			if err != nil {
				var apierr *anthropic.Error
				if errors.As(err, &apierr) && apierr.StatusCode == 404 {
					continue
				}
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete skill version %s: %s", v.Version, err))
				return
			}
		}
		if err := iter.Err(); err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list skill versions for deletion: %s", err))
			return
		}
	}

	_, err := r.client.Beta.Skills.Delete(ctx, skillID, anthropic.BetaSkillDeleteParams{})
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			// Already gone — treat as success.
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete skill: %s", err))
		return
	}
}

func (r *SkillResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

func mapSkillNewResponseToState(skill *anthropic.BetaSkillNewResponse, data *SkillResourceModel) {
	data.ID = types.StringValue(skill.ID)
	data.CreatedAt = types.StringValue(skill.CreatedAt)
	data.UpdatedAt = types.StringValue(skill.UpdatedAt)
	data.LatestVersion = types.StringValue(skill.LatestVersion)
	data.Source = types.StringValue(skill.Source)

	// Only adopt display_title from the API if the user explicitly set it in config;
	// otherwise leave the plan/state value (null) to avoid drift on subsequent plans.
	if !data.DisplayTitle.IsNull() {
		if skill.DisplayTitle != "" {
			data.DisplayTitle = types.StringValue(skill.DisplayTitle)
		} else {
			data.DisplayTitle = types.StringNull()
		}
	}
	// Do NOT update Files — the API does not return file paths (they are ForceNew).
}

func mapSkillGetResponseToState(skill *anthropic.BetaSkillGetResponse, data *SkillResourceModel) {
	// Detect import context: during a fresh import the state has only the ID,
	// so created_at is null. We use this to decide whether to always populate
	// display_title from the API (import) or only when the user has it configured
	// (normal read — to avoid unintended plan drift).
	isImport := data.CreatedAt.IsNull()

	data.ID = types.StringValue(skill.ID)
	data.CreatedAt = types.StringValue(skill.CreatedAt)
	data.UpdatedAt = types.StringValue(skill.UpdatedAt)
	data.LatestVersion = types.StringValue(skill.LatestVersion)
	data.Source = types.StringValue(skill.Source)

	if !data.DisplayTitle.IsNull() || isImport {
		if skill.DisplayTitle != "" {
			data.DisplayTitle = types.StringValue(skill.DisplayTitle)
		} else {
			data.DisplayTitle = types.StringNull()
		}
	}
	// Do NOT update Files — the API does not return file paths (they are ForceNew).
}
