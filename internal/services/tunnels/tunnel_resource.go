// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// tunnelTokenRotationReason is recorded by the API, for audit, with every
// rotation the provider triggers.
const tunnelTokenRotationReason = "token_rotation changed in the Terraform configuration"

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &TunnelResource{}
var _ resource.ResourceWithImportState = &TunnelResource{}

func NewTunnelResource() resource.Resource {
	return &TunnelResource{}
}

// TunnelResource defines the resource implementation.
type TunnelResource struct {
	client *providerdata.OAuthClient
}

// TunnelResourceModel describes the resource data model.
type TunnelResourceModel struct {
	ID            types.String `tfsdk:"id"`
	DisplayName   types.String `tfsdk:"display_name"`
	Domain        types.String `tfsdk:"domain"`
	CreatedAt     types.String `tfsdk:"created_at"`
	TokenRotation types.String `tfsdk:"token_rotation"`
	Token         types.String `tfsdk:"token"`
	TokenID       types.String `tfsdk:"token_id"`
}

// --- Schema ---

func (r *TunnelResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tunnel"
}

func (r *TunnelResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages an MCP tunnel (research preview): an Anthropic-assigned domain through which " +
			"Managed Agents sessions and the Messages API reach MCP servers inside a private network over an outbound-only " +
			"connection. The tunnel rejects MCP traffic until at least one `anthropic_tunnel_certificate` is registered on it.\n\n" +
			"There is no update or delete endpoint: changing `display_name` replaces the tunnel (a new domain is allocated), and " +
			"destroying it **archives** it, which is irreversible: its certificates are archived with it, its domain is retired " +
			"and never reused, and its token stops working.\n\n" +
			"Requires an OAuth bearer token with the `workspace:manage_tunnels` scope (`auth_token` or `federation`); API keys are " +
			"not accepted on this endpoint.",
		Attributes: map[string]schema.Attribute{
			// --- Optional ---
			"display_name": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Human-readable name for the tunnel, 1-255 characters. The API cannot rename a tunnel, so changing " +
					"this forces replacement: a new tunnel with a new `domain` and `token`, and the old one archived.",
				Validators:    []validator.String{stringvalidator.LengthBetween(1, 255)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"token_rotation": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Arbitrary change-detection value, like the setup component's `--token-version`. Changing it from " +
					"one non-null value to another rotates the tunnel token in place (`rotate_token`): the old token stops working for " +
					"new connections, established ones are not severed, and a restarted connector must use the new `token`. The first " +
					"value set (at creation, on import, or added to an existing tunnel) is recorded without rotating, and removing it " +
					"does not rotate either.",
			},

			// --- Computed ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique tunnel identifier assigned by the API (`tnl_...`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"domain": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Anthropic-assigned hostname for the tunnel. MCP server URLs whose host is a subdomain of this " +
					"value (`<route>.<domain>`) are routed through the tunnel. Globally unique and never reused.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Creation timestamp (RFC 3339).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"token": schema.StringAttribute{
				Computed:  true,
				Sensitive: true,
				MarkdownDescription: "The connector token `cloudflared` runs the tunnel with. Treat it as a credential: it is stored " +
					"in the Terraform state. Revealed once at creation (and once on import), then kept from state; it changes only " +
					"when `token_rotation` triggers a rotation. A rotation made outside Terraform is not detected.",
				PlanModifiers: []planmodifier.String{tokenFollowsRotation()},
			},
			"token_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Stable, non-secret identifier of the current token value. Changes when the token is rotated.",
				PlanModifiers:       []planmodifier.String{tokenFollowsRotation()},
			},
		},
	}
}

// --- Configure ---

func (r *TunnelResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

	if !providerrors.RequireOAuthResourceClient(pd.OAuthClient, &resp.Diagnostics) {
		return
	}

	r.client = pd.OAuthClient
}

// --- Create ---

func (r *TunnelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TunnelResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaTunnelNewParams{}
	if !data.DisplayName.IsNull() && !data.DisplayName.IsUnknown() {
		params.DisplayName = param.NewOpt(data.DisplayName.ValueString())
	}

	// Creation allocates a fresh hostname and is not idempotent. The SDK
	// would retry this POST on a 408, 409, 5xx or dropped connection, any of
	// which may follow a create that landed, and each duplicate is an
	// unmanaged tunnel holding one of the organization's 10 active-tunnel
	// slots. So no retry at all: a failed apply is re-run by the operator,
	// who can check the Console's MCP tunnels list first.
	tunnel, err := r.client.Beta.Tunnels.New(ctx, params, option.WithMaxRetries(0))
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create tunnel: %s", err))
		return
	}
	mapTunnelToState(tunnel, &data)

	token, err := r.client.Beta.Tunnels.RevealToken(ctx, tunnel.ID, anthropic.BetaTunnelRevealTokenParams{})
	if err != nil {
		// The tunnel exists: save it, so Terraform marks it tainted and the
		// next apply archives it and creates a replacement, rather than
		// losing track of it (creation is not idempotent).
		data.Token = types.StringNull()
		data.TokenID = types.StringNull()
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf(
			"Tunnel %s was created, but its token could not be revealed: %s. The tunnel is saved in state as tainted, so the next apply archives it and creates a new one.",
			tunnel.ID, err))
		return
	}
	mapTunnelTokenToState(token, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *TunnelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TunnelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tunnel, err := r.client.Beta.Tunnels.Get(ctx, data.ID.ValueString(), anthropic.BetaTunnelGetParams{})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tunnel: %s", err))
		return
	}

	// Archival is irreversible: an archived tunnel has no token, rejects all
	// traffic and can never come back. Treat it as gone, so the next plan
	// re-provisions a fresh tunnel (the documented response to a tunnel
	// archived after a suspected compromise) instead of keeping a dead one.
	if !tunnel.ArchivedAt.IsZero() {
		tflog.Warn(ctx, "tunnel was archived outside Terraform; removing it from state", map[string]any{
			"tunnel_id":   tunnel.ID,
			"archived_at": tunnel.ArchivedAt.String(),
		})
		resp.State.RemoveResource(ctx)
		return
	}

	mapTunnelToState(tunnel, &data)

	// The token is never re-revealed on refresh: the API logs every reveal
	// as a tunnel_token_revealed compliance event, so revealing per plan
	// would flood the audit log. The only reveal here is the one an import
	// needs, when state has no token yet. Its failure is a warning, not an
	// error: an error would fail every plan, including the one that replaces
	// a tunnel Create left tainted after its own reveal failed.
	if data.Token.IsNull() || data.Token.IsUnknown() {
		token, err := r.client.Beta.Tunnels.RevealToken(ctx, tunnel.ID, anthropic.BetaTunnelRevealTokenParams{})
		if err != nil {
			resp.Diagnostics.AddWarning("Tunnel Token Not Revealed",
				fmt.Sprintf("Unable to reveal the token of tunnel %s: %s. token stays null; the next refresh tries again.", tunnel.ID, err))
			data.Token = types.StringNull()
			data.TokenID = types.StringNull()
		} else {
			mapTunnelTokenToState(token, &data)
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

// Update is only reached through token_rotation (display_name forces
// replacement and every other attribute is Computed). It rotates the token
// when tokenRotationRequested says so and otherwise only records the new
// token_rotation value.
func (r *TunnelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TunnelResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state TunnelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if tokenRotationRequested(plan.TokenRotation, state.TokenRotation) {
		token, err := r.client.Beta.Tunnels.RotateToken(ctx, state.ID.ValueString(), anthropic.BetaTunnelRotateTokenParams{
			Reason: param.NewOpt(tunnelTokenRotationReason),
		})
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to rotate tunnel token: %s", err))
			return
		}
		mapTunnelTokenToState(token, &plan)
	} else {
		plan.Token = state.Token
		plan.TokenID = state.TokenID
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// --- Delete (always archives; there is no delete endpoint) ---

func (r *TunnelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data TunnelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Archiving an already-archived tunnel returns it unchanged, so a retried
	// destroy is safe; a 404 means it is gone already.
	_, err := r.client.Beta.Tunnels.Archive(ctx, data.ID.ValueString(), anthropic.BetaTunnelArchiveParams{})
	if err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to archive tunnel: %s", err))
	}
}

// --- ImportState ---

func (r *TunnelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// mapTunnelToState maps the API response into the resource's state model.
// The token attributes and token_rotation are left untouched: the tunnel
// object carries neither.
func mapTunnelToState(tunnel *anthropic.BetaTunnel, data *TunnelResourceModel) {
	data.ID = types.StringValue(tunnel.ID)
	data.DisplayName = tfvalue.StringOrNull(tunnel.DisplayName)
	data.Domain = types.StringValue(tunnel.Domain)
	data.CreatedAt = tfvalue.TimeOrNull(tunnel.CreatedAt)
}

// mapTunnelTokenToState records a revealed or rotated token.
func mapTunnelTokenToState(token *anthropic.BetaTunnelToken, data *TunnelResourceModel) {
	data.Token = types.StringValue(token.TunnelToken)
	data.TokenID = types.StringValue(token.ID)
}

// tokenRotationRequested reports whether moving token_rotation from state to
// plan must rotate the token. Only a change between two non-null values
// rotates: a first value (set on an imported tunnel, say) is a baseline, and
// rotating there would silently break the running connector; removing the
// value is not a request either. An unknown planned value against a
// non-null state may turn out different, so it counts as a request.
func tokenRotationRequested(plan, state types.String) bool {
	if state.IsNull() || state.IsUnknown() || plan.IsNull() {
		return false
	}
	if plan.IsUnknown() {
		return true
	}
	return plan.ValueString() != state.ValueString()
}

// tokenFollowsRotation is the plan modifier on token and token_id: it keeps
// the state value (like UseStateForUnknown) unless the plan rotates the
// token, in which case the value stays unknown until apply.
func tokenFollowsRotation() planmodifier.String {
	return tokenFollowsRotationModifier{}
}

type tokenFollowsRotationModifier struct{}

func (m tokenFollowsRotationModifier) Description(_ context.Context) string {
	return "Keeps the prior state value unless token_rotation changes to a different non-null value."
}

func (m tokenFollowsRotationModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m tokenFollowsRotationModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	// Creation (no prior state) and destruction (no plan) keep the default.
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	if !req.PlanValue.IsUnknown() {
		return
	}

	var planRotation, stateRotation types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("token_rotation"), &planRotation)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("token_rotation"), &stateRotation)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if tokenRotationRequested(planRotation, stateRotation) {
		return
	}
	resp.PlanValue = req.StateValue
}

// isNotFound reports whether err is an API 404.
func isNotFound(err error) bool {
	var apierr *anthropic.Error
	return errors.As(err, &apierr) && apierr.StatusCode == http.StatusNotFound
}
