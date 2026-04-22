// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &SkillVersionResource{}
var _ resource.ResourceWithImportState = &SkillVersionResource{}

func NewSkillVersionResource() resource.Resource {
	return &SkillVersionResource{}
}

// SkillVersionResource defines the resource implementation.
type SkillVersionResource struct {
	client *anthropic.Client
}

// SkillVersionResourceModel describes the resource data model.
type SkillVersionResourceModel struct {
	ID          types.String `tfsdk:"id"`
	SkillID     types.String `tfsdk:"skill_id"`
	Files       types.List   `tfsdk:"files"`
	Version     types.String `tfsdk:"version"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Directory   types.String `tfsdk:"directory"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

func (r *SkillVersionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skill_version"
}

func (r *SkillVersionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages a Skill Version on the Anthropic platform (beta). " +
			"A Skill Version is created by uploading local files to an existing Skill.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique skill version identifier assigned by the API.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"skill_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the Skill that this version belongs to.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"files": schema.ListAttribute{
				Required:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Local file paths to upload for the skill version. Must include a `SKILL.md` file at the root of the directory.",
				PlanModifiers:       []planmodifier.List{listplanmodifier.RequiresReplace()},
			},
			"version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Version identifier for the skill, expressed as a Unix epoch timestamp (e.g., `\"1759178010641129\"`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Human-readable name of the skill version, extracted from the `SKILL.md` file.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"description": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Description of the skill version, extracted from the `SKILL.md` file.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"directory": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Top-level directory name extracted from the uploaded files.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ISO 8601 timestamp of when the skill version was created.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *SkillVersionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*anthropic.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *anthropic.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	r.client = client
}

func (r *SkillVersionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data SkillVersionResourceModel
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

	for _, p := range filePaths {
		f, err := os.Open(p)
		if err != nil {
			resp.Diagnostics.AddError("File Open Error", fmt.Sprintf("Unable to open file %q: %s", p, err))
			return
		}
		openedFiles = append(openedFiles, f)
		files = append(files, f)
	}

	skillVersion, err := r.client.Beta.Skills.Versions.New(ctx, data.SkillID.ValueString(), anthropic.BetaSkillVersionNewParams{
		Files: files,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create skill version: %s", err))
		return
	}

	mapSkillVersionNewResponseToState(skillVersion, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *SkillVersionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data SkillVersionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Version (not ID) is the SDK lookup key; this is what ImportState populates.
	skillVersion, err := r.client.Beta.Skills.Versions.Get(ctx, data.Version.ValueString(), anthropic.BetaSkillVersionGetParams{
		SkillID: data.SkillID.ValueString(),
	})
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read skill version: %s", err))
		return
	}

	mapSkillVersionGetResponseToState(skillVersion, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update is a no-op because all attributes are ForceNew.
func (r *SkillVersionResource) Update(_ context.Context, _ resource.UpdateRequest, _ *resource.UpdateResponse) {
}

func (r *SkillVersionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data SkillVersionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.Beta.Skills.Versions.Delete(ctx, data.Version.ValueString(), anthropic.BetaSkillVersionDeleteParams{
		SkillID: data.SkillID.ValueString(),
	})
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			// Already gone — treat as success.
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete skill version: %s", err))
		return
	}
}

func (r *SkillVersionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("Invalid import ID", "Expected format: <skill_id>/<version>")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("skill_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("version"), parts[1])...)
}

// ============================================================================
// Helper functions
// ============================================================================

func mapSkillVersionNewResponseToState(sv *anthropic.BetaSkillVersionNewResponse, data *SkillVersionResourceModel) {
	data.ID = types.StringValue(sv.ID)
	data.SkillID = types.StringValue(sv.SkillID)
	data.Version = types.StringValue(sv.Version)
	data.Name = types.StringValue(sv.Name)
	data.Description = types.StringValue(sv.Description)
	data.Directory = types.StringValue(sv.Directory)
	data.CreatedAt = types.StringValue(sv.CreatedAt)
	// Do NOT update Files — the API does not return file paths (they are ForceNew).
}

func mapSkillVersionGetResponseToState(sv *anthropic.BetaSkillVersionGetResponse, data *SkillVersionResourceModel) {
	data.ID = types.StringValue(sv.ID)
	data.SkillID = types.StringValue(sv.SkillID)
	data.Version = types.StringValue(sv.Version)
	data.Name = types.StringValue(sv.Name)
	data.Description = types.StringValue(sv.Description)
	data.Directory = types.StringValue(sv.Directory)
	data.CreatedAt = types.StringValue(sv.CreatedAt)
	// Do NOT update Files — the API does not return file paths (they are ForceNew).
}
