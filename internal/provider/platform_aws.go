// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicaws "github.com/anthropics/anthropic-sdk-go/aws"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
)

// awsWorkspaceIDEnv is the workspace variable of Claude Platform on AWS, read
// in place of ANTHROPIC_WORKSPACE_ID when the aws block is set.
const awsWorkspaceIDEnv = "ANTHROPIC_AWS_WORKSPACE_ID"

// awsModel is the provider's aws block.
type awsModel struct {
	Region       types.String `tfsdk:"region"`
	Profile      types.String `tfsdk:"profile"`
	APIKey       types.String `tfsdk:"api_key"`
	AccessKey    types.String `tfsdk:"access_key"`
	SecretKey    types.String `tfsdk:"secret_key"`
	SessionToken types.String `tfsdk:"session_token"`
	BaseURL      types.String `tfsdk:"base_url"`
}

func awsSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional: true,
		Description: "**Beta.** Reach Claude Platform on AWS instead of the Claude API: the provider alias then manages the " +
			"Anthropic organization tied to an AWS account, through the aws-external-anthropic gateway of one AWS " +
			"region. Setting the block is what selects the platform; the AWS and ANTHROPIC_AWS_* environment " +
			"variables only fill its attributes. Conflicts with api_key, admin_api_key, auth_token, federation and " +
			"google_cloud, and the ANTHROPIC_API_KEY, ANTHROPIC_ADMIN_API_KEY, ANTHROPIC_AUTH_TOKEN and " +
			"ANTHROPIC_BASE_URL variables are ignored: no first-party credential is valid there. workspace_id is " +
			"required. Requests are signed with AWS SigV4 (service aws-external-anthropic), with credentials from " +
			"access_key/secret_key, profile or the AWS default chain, unless api_key is set. Resources the gateway " +
			"does not serve fail at plan time. Support for this platform is verified with offline tests only, and " +
			"the provider prints a warning saying so.",
		Attributes: map[string]schema.Attribute{
			"region": schema.StringAttribute{
				Optional: true,
				Description: "AWS region of the gateway endpoint (https://aws-external-anthropic.<region>.api.aws) and of " +
					"the SigV4 signature. A workspace is bound to the region it was created in and is reachable only " +
					"there, so use one provider alias per region. Can also be set via the AWS_REGION or " +
					"AWS_DEFAULT_REGION environment variable.",
			},
			"profile": schema.StringAttribute{
				Optional: true,
				Description: "Named profile of the AWS shared configuration files to take credentials from (SSO, " +
					"credential_process and role assumption included). Conflicts with access_key and api_key. Without " +
					"profile, access_key or api_key, the AWS default credential chain applies.",
			},
			"api_key": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "API key generated in the AWS Console (Claude Platform on AWS > API keys), sent instead of a " +
					"SigV4 signature; the principal needs the aws-external-anthropic:CallWithBearerToken action. Not a " +
					"Claude Console key. Conflicts with profile and access_key. Can also be set via the " +
					"ANTHROPIC_AWS_API_KEY environment variable, which is only read when the block sets no credential.",
			},
			"access_key": schema.StringAttribute{
				Optional:    true,
				Description: "AWS access key ID for SigV4. Requires secret_key; conflicts with profile and api_key.",
			},
			"secret_key": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "AWS secret access key, with access_key.",
			},
			"session_token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "AWS session token of temporary credentials, with access_key and secret_key. Static " +
					"credentials are not refreshed: for a long apply prefer profile or the default chain.",
			},
			"base_url": schema.StringAttribute{
				Optional: true,
				Description: "Gateway base URL, in place of the one derived from region (an AWS PrivateLink endpoint, " +
					"for instance). Can also be set via the ANTHROPIC_AWS_BASE_URL environment variable; ANTHROPIC_BASE_URL " +
					"is not read.",
			},
		},
	}
}

// resolveAWS checks the configuration of a provider alias whose aws block is
// set and returns the SDK's client configuration for it. data.WorkspaceID is
// known: Configure rejects an unknown one first.
func resolveAWS(ctx context.Context, data AnthropicProviderModel) (*anthropicaws.ClientConfig, diag.Diagnostics) {
	diags := firstPartyConflicts(data, "aws", "Claude Platform on AWS with AWS credentials")
	if diags.HasError() {
		return nil, diags
	}

	m, d := awsBlock(ctx, data.AWS)
	diags.Append(d...)
	if diags.HasError() {
		return nil, diags
	}

	diags.Append(checkAWSCredentials(m)...)

	// The gateway needs the header on every inference and resource request,
	// and the SDK refuses to build a client without it, even for an alias that
	// only manages workspaces.
	workspaceID := firstNonEmpty(data.WorkspaceID.ValueString(), os.Getenv(awsWorkspaceIDEnv))
	if workspaceID == "" {
		diags.AddAttributeError(path.Root("workspace_id"), "Missing Workspace",
			"Claude Platform on AWS identifies the workspace of every inference and resource request by the "+
				"anthropic-workspace-id header. Set workspace_id or the "+awsWorkspaceIDEnv+" environment variable "+
				"(ANTHROPIC_WORKSPACE_ID is not read with the aws block). An alias that only manages workspaces "+
				"needs one too: any workspace of the same region.")
	}

	diags.Append(checkAWSRegion(m)...)
	if diags.HasError() {
		return nil, diags
	}

	return &anthropicaws.ClientConfig{
		APIKey:             m.APIKey.ValueString(),
		AWSAccessKey:       m.AccessKey.ValueString(),
		AWSSecretAccessKey: m.SecretKey.ValueString(),
		AWSSessionToken:    m.SessionToken.ValueString(),
		AWSProfile:         m.Profile.ValueString(),
		AWSRegion:          m.Region.ValueString(),
		WorkspaceID:        workspaceID,
		BaseURL:            m.BaseURL.ValueString(),
	}, diags
}

// awsBlock reads the aws block, which may be unknown neither as a whole nor
// in any attribute.
func awsBlock(ctx context.Context, block types.Object) (awsModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	var m awsModel
	if block.IsUnknown() {
		diags.AddAttributeError(path.Root("aws"), "Unknown AWS Configuration",
			"The aws block depends on a value that is not known until apply. Provider configuration must be known at plan time.")
		return m, diags
	}
	diags.Append(block.As(ctx, &m, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return m, diags
	}

	// Unknown is an error, not unset, as for the federation block: read as
	// unset, an unknown region would sign for the AWS_REGION of the machine
	// running the plan, and an unknown credential would fall back to the
	// default chain, another identity.
	diags.Append(unknownBlockAttributes("aws", "Unknown AWS Configuration", []namedString{
		{"region", m.Region},
		{"profile", m.Profile},
		{"api_key", m.APIKey},
		{"access_key", m.AccessKey},
		{"secret_key", m.SecretKey},
		{"session_token", m.SessionToken},
		{"base_url", m.BaseURL},
	})...)
	return m, diags
}

// checkAWSCredentials requires one credential source, stated: the SDK would
// otherwise pick by a precedence the configuration does not show. An access
// key goes with its secret key, and a session token with both.
func checkAWSCredentials(m awsModel) diag.Diagnostics {
	var diags diag.Diagnostics
	sources := 0
	for _, set := range []bool{m.APIKey.ValueString() != "", m.AccessKey.ValueString() != "" || m.SecretKey.ValueString() != "", m.Profile.ValueString() != ""} {
		if set {
			sources++
		}
	}
	if sources > 1 {
		diags.AddAttributeError(path.Root("aws"), "Conflicting AWS Credentials",
			"Set at most one of aws.api_key, aws.access_key/aws.secret_key and aws.profile.")
	}
	if (m.AccessKey.ValueString() == "") != (m.SecretKey.ValueString() == "") {
		diags.AddAttributeError(path.Root("aws"), "Incomplete AWS Credentials",
			"aws.access_key and aws.secret_key must be set together.")
	}
	if m.SessionToken.ValueString() != "" && m.AccessKey.ValueString() == "" {
		diags.AddAttributeError(path.Root("aws").AtName("session_token"), "Incomplete AWS Credentials",
			"aws.session_token is only used with aws.access_key and aws.secret_key.")
	}
	return diags
}

// checkAWSRegion requires a region whenever the client signs requests or
// derives the gateway URL from it. The SDK falls back to these variables
// itself; checking them here puts the error on the attribute instead of in an
// SDK message.
func checkAWSRegion(m awsModel) diag.Diagnostics {
	var diags diag.Diagnostics
	region := firstNonEmpty(m.Region.ValueString(), os.Getenv("AWS_REGION"), os.Getenv("AWS_DEFAULT_REGION"))
	signs := m.APIKey.ValueString() == "" && os.Getenv("ANTHROPIC_AWS_API_KEY") == ""
	derivesURL := firstNonEmpty(m.BaseURL.ValueString(), os.Getenv("ANTHROPIC_AWS_BASE_URL")) == ""
	if region == "" && (signs || derivesURL) {
		diags.AddAttributeError(path.Root("aws").AtName("region"), "Missing AWS Region",
			"Set aws.region or the AWS_REGION environment variable: it selects the gateway endpoint and the SigV4 "+
				"signing region, and a workspace is reachable only through the region it was created in.")
	}
	return diags
}

// newAWSClient builds the single SDK client of a Claude Platform on AWS alias.
// Credentials are resolved now (shared files, SSO, STS or IMDS for the default
// chain), so a missing identity fails at Configure rather than mid-apply.
//
// aws.NewClient returns options, not a client: they become one through
// anthropic.NewClient, and the marker option is what keeps the first-party
// environment out of it. Without it, DefaultClientOptions would add the
// x-api-key of an exported ANTHROPIC_API_KEY to every signed request, and
// ANTHROPIC_BASE_URL (or an ant profile's base URL) would replace the gateway.
// ANTHROPIC_BASE_URL is deliberately not re-applied as newSDKClient does for
// first party; the SDK's own ANTHROPIC_AWS_BASE_URL still is.
//
// extra are request options for tests (an HTTP client); they go before the
// SDK's SigV4 middleware, which signs whatever they change.
func newAWSClient(ctx context.Context, cfg anthropicaws.ClientConfig, extra ...option.RequestOption) (*anthropic.Client, error) {
	opts := append([]option.RequestOption{option.WithMiddleware(platform.NotFoundGuard(platform.AWS))}, extra...)
	awsClient, err := anthropicaws.NewClient(ctx, cfg, opts...)
	if err != nil {
		return nil, err
	}
	client := anthropic.NewClient(append([]option.RequestOption{option.WithoutEnvironmentDefaults()}, awsClient.Options...)...)
	return &client, nil
}

// ignoredFirstPartyEnvironment logs the first-party variables a platform alias
// does not read. A debug line, not a warning: a CI job that manages both a
// first-party and a platform organization exports them for the first-party
// alias, and the SDKs' platform clients ignore them the same way.
func ignoredFirstPartyEnvironment(ctx context.Context, p platform.Platform) {
	for _, name := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_ADMIN_API_KEY", "ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_BASE_URL", "ANTHROPIC_WORKSPACE_ID",
	} {
		if os.Getenv(name) != "" {
			tflog.Debug(ctx, "environment variable ignored by a platform provider alias",
				map[string]any{"variable": name, "platform": string(p)})
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
