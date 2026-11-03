// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
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

// caCertificatePEMMaxBytes is the API's limit on ca_certificate_pem.
const caCertificatePEMMaxBytes = 8192

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &TunnelCertificateResource{}
var _ resource.ResourceWithImportState = &TunnelCertificateResource{}

func NewTunnelCertificateResource() resource.Resource {
	return &TunnelCertificateResource{}
}

// TunnelCertificateResource defines the resource implementation.
type TunnelCertificateResource struct {
	client *providerdata.OAuthClient
}

// TunnelCertificateResourceModel describes the resource data model.
type TunnelCertificateResourceModel struct {
	ID               types.String `tfsdk:"id"`
	TunnelID         types.String `tfsdk:"tunnel_id"`
	CACertificatePEM types.String `tfsdk:"ca_certificate_pem"`
	Fingerprint      types.String `tfsdk:"fingerprint"`
	CreatedAt        types.String `tfsdk:"created_at"`
	ExpiresAt        types.String `tfsdk:"expires_at"`
}

// --- Schema ---

func (r *TunnelCertificateResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tunnel_certificate"
}

func (r *TunnelCertificateResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Registers a public CA certificate on an MCP tunnel (research preview). Anthropic verifies the server " +
			"certificate your proxy presents for the inner TLS session against the tunnel's registered CAs, and does not connect " +
			"to a tunnel until at least one is registered. A tunnel holds at most two non-archived certificates, so a new CA can " +
			"be registered next to the current one for a zero-downtime rotation.\n\n" +
			"There is no update or delete endpoint: changing the certificate replaces this resource, and destroying it " +
			"**archives** the certificate (the record is kept, the CA is no longer trusted). Archiving the last certificate is " +
			"allowed and stops the tunnel's MCP traffic until a new one is added.\n\n" +
			"Requires an OAuth bearer token with the `workspace:manage_tunnels` scope (`auth_token` or `federation`); API keys are " +
			"not accepted on this endpoint.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"tunnel_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the tunnel to register the certificate on (`tnl_...`). Changing this forces replacement.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ca_certificate_pem": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "PEM-encoded X.509 CA certificate: exactly one certificate, no private-key material, at most " +
					"8 KB. The API also requires a critical `BasicConstraints` with `CA:TRUE`, a `SubjectKeyIdentifier`, a `KeyUsage` " +
					"including `keyCertSign`, a current validity period, and an RSA ≥ 2048-bit or ECDSA ≥ P-256 key with a SHA-256 or " +
					"stronger signature. The API never returns the PEM, so the value comes from the configuration; a change that keeps " +
					"the same certificate (same DER `fingerprint`, e.g. re-wrapped lines) is applied in place, any other change forces " +
					"replacement.",
				Validators:    []validator.String{caCertificatePEMValidator{}},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(caCertificateChanged, caCertificateChangedDescription, caCertificateChangedDescription)},
			},

			// --- Computed ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique certificate identifier assigned by the API (`tcrt_...`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fingerprint": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Lowercase hex SHA-256 fingerprint of the certificate's DER encoding.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Registration timestamp (RFC 3339).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"expires_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Expiry of the certificate (RFC 3339), or null if it does not expire.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// --- Configure ---

func (r *TunnelCertificateResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *TunnelCertificateResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TunnelCertificateResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Not retried, like the tunnel create: replaying a registration that had
	// landed would take one of the tunnel's two certificate slots behind
	// Terraform's back.
	cert, err := r.client.Beta.Tunnels.Certificates.New(ctx, data.TunnelID.ValueString(), anthropic.BetaTunnelCertificateNewParams{
		CACertificatePEM: data.CACertificatePEM.ValueString(),
	}, option.WithMaxRetries(0))
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to register tunnel certificate: %s", err))
		return
	}

	mapTunnelCertificateToState(cert, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *TunnelCertificateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TunnelCertificateResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cert, err := r.client.Beta.Tunnels.Certificates.Get(ctx, data.ID.ValueString(), anthropic.BetaTunnelCertificateGetParams{
		TunnelID: data.TunnelID.ValueString(),
	})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tunnel certificate: %s", err))
		return
	}

	// An archived certificate is no longer trusted and cannot be restored:
	// treat it as gone so the next plan registers the CA again (archiving
	// the tunnel archives its certificates too).
	if !cert.ArchivedAt.IsZero() {
		tflog.Warn(ctx, "tunnel certificate was archived outside Terraform; removing it from state", map[string]any{
			"tunnel_id":      cert.TunnelID,
			"certificate_id": cert.ID,
			"archived_at":    cert.ArchivedAt.String(),
		})
		resp.State.RemoveResource(ctx)
		return
	}

	mapTunnelCertificateToState(cert, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

// Update is only reached when ca_certificate_pem changes to text encoding the
// same certificate (see caCertificateChanged), or when an imported
// certificate first receives its PEM from configuration. Nothing changes
// server-side, so the plan is stored as is.
func (r *TunnelCertificateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data TunnelCertificateResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Delete (always archives; there is no delete endpoint) ---

func (r *TunnelCertificateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data TunnelCertificateResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.Beta.Tunnels.Certificates.Archive(ctx, data.ID.ValueString(), anthropic.BetaTunnelCertificateArchiveParams{
		TunnelID: data.TunnelID.ValueString(),
	})
	if err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to archive tunnel certificate: %s", err))
	}
}

// --- ImportState ---

func (r *TunnelCertificateResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	tunnelID, certificateID, err := parseTunnelCertificateImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("tunnel_id"), tunnelID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), certificateID)...)
}

// parseTunnelCertificateImportID splits a composite
// "<tunnel_id>:<certificate_id>" import ID into its two parts.
func parseTunnelCertificateImportID(id string) (tunnelID, certificateID string, err error) {
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected import ID in the form <tunnel_id>:<certificate_id>, got %q", id)
	}
	return parts[0], parts[1], nil
}

// ============================================================================
// Helper functions
// ============================================================================

// mapTunnelCertificateToState maps the API response into the resource's
// state model. ca_certificate_pem is left as configured: the API never
// returns it.
func mapTunnelCertificateToState(cert *anthropic.BetaTunnelCertificate, data *TunnelCertificateResourceModel) {
	data.ID = types.StringValue(cert.ID)
	data.TunnelID = types.StringValue(cert.TunnelID)
	data.Fingerprint = types.StringValue(cert.Fingerprint)
	data.CreatedAt = tfvalue.TimeOrNull(cert.CreatedAt)
	data.ExpiresAt = tfvalue.TimeOrNull(cert.ExpiresAt)
}

// decodeSingleCertificatePEM returns the DER bytes of the one CERTIFICATE
// block in s, or an error describing why s is not exactly that.
func decodeSingleCertificatePEM(s string) ([]byte, error) {
	block, rest := pem.Decode([]byte(s))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	if block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("found a %q PEM block, expected CERTIFICATE (the value must not contain private-key material)", block.Type)
	}
	if next, _ := pem.Decode(rest); next != nil {
		return nil, fmt.Errorf("found more than one PEM block, expected exactly one certificate")
	}
	return block.Bytes, nil
}

// pemFingerprint returns the lowercase hex SHA-256 of the certificate's DER
// encoding, the same value the API reports as fingerprint.
func pemFingerprint(s string) (string, error) {
	der, err := decodeSingleCertificatePEM(s)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:]), nil
}

const caCertificateChangedDescription = "Replaces the certificate unless the new PEM encodes the same certificate (same fingerprint)."

// caCertificateChanged decides whether a ca_certificate_pem change replaces
// the certificate. The PEM is compared by what it encodes, not by its text:
// the API only keeps the DER fingerprint, so this is also what lets an
// imported certificate (no PEM in state) adopt its configured PEM in place.
// An unknown or unparsable value is treated as a different certificate.
func caCertificateChanged(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
	resp.RequiresReplace = true
	if req.PlanValue.IsUnknown() || req.PlanValue.IsNull() {
		return
	}

	var stateFingerprint types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("fingerprint"), &stateFingerprint)...)
	if resp.Diagnostics.HasError() || stateFingerprint.IsNull() || stateFingerprint.IsUnknown() {
		return
	}

	planned, err := pemFingerprint(req.PlanValue.ValueString())
	if err != nil {
		return
	}
	resp.RequiresReplace = !strings.EqualFold(planned, stateFingerprint.ValueString())
}

// caCertificatePEMValidator checks the shape constraints the API documents
// and that can be checked without parsing the certificate: size, exactly one
// CERTIFICATE block, no other PEM block (a private key in particular). The
// X.509 requirements (CA flag, key usage, algorithms, validity) are left to
// the API, which reports them precisely.
type caCertificatePEMValidator struct{}

func (v caCertificatePEMValidator) Description(_ context.Context) string {
	return fmt.Sprintf("must be a single PEM-encoded certificate of at most %d bytes", caCertificatePEMMaxBytes)
}

func (v caCertificatePEMValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v caCertificatePEMValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := req.ConfigValue.ValueString()

	if len(value) > caCertificatePEMMaxBytes {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid CA certificate",
			fmt.Sprintf("ca_certificate_pem is %d bytes; the API accepts at most %d.", len(value), caCertificatePEMMaxBytes))
		return
	}
	if _, err := decodeSingleCertificatePEM(value); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid CA certificate",
			fmt.Sprintf("ca_certificate_pem must hold exactly one PEM-encoded certificate: %s.", err))
	}
}
