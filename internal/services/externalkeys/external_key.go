// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package externalkeys implements the customer-managed encryption key (CMEK)
// configurations of an organization: the anthropic_external_key resource and
// the anthropic_external_key / anthropic_external_keys data sources. All of
// them go through the Admin API client (internal/admin).
package externalkeys

import (
	"context"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// externalKeysPath is the collection endpoint. The GA reference documents it
// without the `?beta=true` flag the pinned SDK's Beta service appends.
const externalKeysPath = "/v1/organizations/external_keys"

// attachmentAttached is the attachment.type of a key some workspace uses (the
// other value is `unattached`). Archived workspaces count, so archiving every
// workspace that uses a key leaves it attached.
const attachmentAttached = "attached"

// externalKeyPath returns the item endpoint. The ID is path-escaped because on
// Claude Platform on AWS it is the KMS key ARN, whose `key/<uuid>` suffix would
// otherwise add a path segment. SDK v1.84.0 escapes it the same way; the
// pinned v1.67.0 does not.
func externalKeyPath(id string) string {
	return externalKeysPath + "/" + url.PathEscape(id)
}

// --- Admin API response types ---

type externalKeyAPIResponse struct {
	ID string `json:"id"`
	// DisplayName is null when none was set; it decodes to "".
	DisplayName    string                       `json:"display_name"`
	Geo            string                       `json:"geo"`
	Attachment     *externalKeyAPIAttachment    `json:"attachment"`
	ProviderConfig externalKeyAPIProviderConfig `json:"provider_config"`
	CreatedAt      string                       `json:"created_at"`
	UpdatedAt      string                       `json:"updated_at"`
}

type externalKeyAPIAttachment struct {
	Type string `json:"type"`
}

// externalKeyAPIProviderConfig flattens the aws / gcp / azure union. The
// deprecated `role_arn` (ignored by the API) is deliberately not decoded.
type externalKeyAPIProviderConfig struct {
	Type     string `json:"type"`
	KMSARN   string `json:"kms_arn"`
	Region   string `json:"region"`
	KeyName  string `json:"key_name"`
	VaultURI string `json:"vault_uri"`
	TenantID string `json:"tenant_id"`
	ClientID string `json:"client_id"`
}

type externalKeysListResponse struct {
	Data     []externalKeyAPIResponse `json:"data"`
	NextPage *string                  `json:"next_page"`
}

// --- Terraform data models ---

// ExternalKeyModel is shared by the resource and the singular data source; the
// plural data source stores the same attributes per list element.
type ExternalKeyModel struct {
	ID             types.String `tfsdk:"id"`
	DisplayName    types.String `tfsdk:"display_name"`
	Geo            types.String `tfsdk:"geo"`
	ProviderConfig types.Object `tfsdk:"provider_config"`
	Attachment     types.String `tfsdk:"attachment"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
}

type externalKeyProviderConfigModel struct {
	Type     types.String `tfsdk:"type"`
	KMSARN   types.String `tfsdk:"kms_arn"`
	Region   types.String `tfsdk:"region"`
	KeyName  types.String `tfsdk:"key_name"`
	VaultURI types.String `tfsdk:"vault_uri"`
	TenantID types.String `tfsdk:"tenant_id"`
	ClientID types.String `tfsdk:"client_id"`
}

var providerConfigAttrTypes = map[string]attr.Type{
	"type":      types.StringType,
	"kms_arn":   types.StringType,
	"region":    types.StringType,
	"key_name":  types.StringType,
	"vault_uri": types.StringType,
	"tenant_id": types.StringType,
	"client_id": types.StringType,
}

var externalKeyAttrTypes = map[string]attr.Type{
	"id":              types.StringType,
	"display_name":    types.StringType,
	"geo":             types.StringType,
	"provider_config": types.ObjectType{AttrTypes: providerConfigAttrTypes},
	"attachment":      types.StringType,
	"created_at":      types.StringType,
	"updated_at":      types.StringType,
}

// Descriptions shared by the resource and the data sources.
const (
	idDescription = "Identifier of the external key: a tagged ID prefixed `ekey_`, or the AWS KMS key ARN for organizations on Claude Platform on AWS. " +
		"This is the value `anthropic_workspace.external_key_id` takes."
	attachmentDescription = "`attached` when at least one workspace, live or archived, encrypts its data with this key; `unattached` otherwise. " +
		"Only an `unattached` key can have its `geo` and `provider_config` changed, or be deleted."
	kmsARNDescription   = "`aws` only. Full ARN of the AWS KMS key (at most 2048 characters). On Claude Platform on AWS the key must be a single-Region key in the organization's own AWS account; alias ARNs are rejected."
	regionDescription   = "`aws` only. AWS region of the key. Derived by the API from `kms_arn` when omitted."
	keyNameDescription  = "`gcp`: full resource name of the Cloud KMS key (`projects/.../locations/.../keyRings/.../cryptoKeys/...`). `azure`: name of the key within the vault."
	vaultURIDescription = "`azure` only. Key Vault data-plane URI: `https://{vault-name}.vault.azure.net` or `https://{hsm-name}.managedhsm.azure.net`."
	tenantIDDescription = "`azure` only. Microsoft Entra (Azure AD) tenant ID of the vault."
	clientIDDescription = "`azure` only. Application (client) ID of a single-tenant app registration in your directory. Omit it to use Anthropic's multitenant app."
)

// mapExternalKeyToModel maps an API external key to the Terraform model. Absent
// strings (null in the payload) become null values.
func mapExternalKeyToModel(ctx context.Context, key *externalKeyAPIResponse) (ExternalKeyModel, diag.Diagnostics) {
	pc := externalKeyProviderConfigModel{
		Type:     tfvalue.StringOrNull(key.ProviderConfig.Type),
		KMSARN:   tfvalue.StringOrNull(key.ProviderConfig.KMSARN),
		Region:   tfvalue.StringOrNull(key.ProviderConfig.Region),
		KeyName:  tfvalue.StringOrNull(key.ProviderConfig.KeyName),
		VaultURI: tfvalue.StringOrNull(key.ProviderConfig.VaultURI),
		TenantID: tfvalue.StringOrNull(key.ProviderConfig.TenantID),
		ClientID: tfvalue.StringOrNull(key.ProviderConfig.ClientID),
	}
	pcObj, diags := types.ObjectValueFrom(ctx, providerConfigAttrTypes, pc)

	attachment := types.StringNull()
	if key.Attachment != nil {
		attachment = tfvalue.StringOrNull(key.Attachment.Type)
	}

	return ExternalKeyModel{
		ID:             types.StringValue(key.ID),
		DisplayName:    tfvalue.StringOrNull(key.DisplayName),
		Geo:            tfvalue.StringOrNull(key.Geo),
		ProviderConfig: pcObj,
		Attachment:     attachment,
		CreatedAt:      tfvalue.StringOrNull(key.CreatedAt),
		UpdatedAt:      tfvalue.StringOrNull(key.UpdatedAt),
	}, diags
}
