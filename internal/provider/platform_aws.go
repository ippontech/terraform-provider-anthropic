// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicaws "github.com/anthropics/anthropic-sdk-go/aws"
	"github.com/anthropics/anthropic-sdk-go/option"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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

	SharedConfigFiles   types.List   `tfsdk:"shared_config_files"`
	RoleARN             types.String `tfsdk:"role_arn"`
	RoleSessionName     types.String `tfsdk:"role_session_name"`
	RoleExternalID      types.String `tfsdk:"role_external_id"`
	RoleDurationSeconds types.Int64  `tfsdk:"role_duration_seconds"`
}

// awsRole is the role to assume on top of the resolved AWS credentials.
type awsRole struct {
	arn, sessionName, externalID string
	duration                     time.Duration
}

// awsSource says how the credentials are resolved when the block sets
// shared_config_files or role_arn: by this provider, to static credentials the
// SDK client then signs with, instead of by the SDK client's own chain.
type awsSource struct {
	region      string
	configFiles []string
	role        *awsRole
}

const (
	defaultRoleSessionName = "terraform-provider-anthropic"
	// The maximum session duration of a role is 1 to 12 hours; the API
	// accepts 15 minutes at least. The default is long enough for a plan and
	// an apply, because static credentials are not refreshed.
	defaultRoleDuration = time.Hour
	minRoleDuration     = 15 * time.Minute
	maxRoleDuration     = 12 * time.Hour
)

func awsSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional: true,
		Description: "**Experimental.** Reach Claude Platform on AWS instead of the Claude API: the provider configuration (the default one or an alias) then manages the " +
			"Anthropic organization tied to an AWS account, through the aws-external-anthropic gateway of one AWS " +
			"region. Setting the block is what selects the platform; the AWS and ANTHROPIC_AWS_* environment " +
			"variables only fill its attributes. Conflicts with api_key, admin_api_key, auth_token, federation, " +
			"api_federation, cli and google_cloud, and the ANTHROPIC_API_KEY, ANTHROPIC_ADMIN_API_KEY, ANTHROPIC_AUTH_TOKEN and " +
			"ANTHROPIC_BASE_URL variables are ignored: no first-party credential is valid there. workspace_id is " +
			"required. Requests are signed with AWS SigV4 (service aws-external-anthropic), with credentials from " +
			"access_key/secret_key, profile or the AWS default chain, unless api_key is set. A type the gateway does " +
			"not serve fails at plan, or at apply for a data source that Terraform reads only then. Support for this " +
			"platform is experimental: it rests on the SDK's beta AWS client, may change in a minor release, " +
			"is verified mostly with offline tests, and the provider prints a warning saying so.",
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
					"profile, access_key, api_key or an exported ANTHROPIC_AWS_API_KEY, the AWS default credential " +
					"chain applies.",
			},
			"api_key": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "Claude Platform on AWS API key, sent as x-api-key instead of a SigV4 signature: a short-term " +
					"key from AWS's token generator, verified against the real gateway, or a long-term key generated in the " +
					"AWS Console (Claude Platform on AWS > API keys), not verified yet. The principal needs the " +
					"aws-external-anthropic:CallWithBearerToken action. Not a Claude Console key. Conflicts with profile " +
					"and access_key. Can also be set via the " +
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
			"shared_config_files": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Paths of AWS shared configuration files to take credentials from, in place of the default " +
					"~/.aws/config, as the AWS provider's shared_config_files does (together with profile when set). " +
					"HCP Terraform dynamic credentials reach a provider only through the file named by the " +
					"tfc_aws_dynamic_credentials variable, so pass that path here. Conflicts with api_key and access_key.",
			},
			"role_arn": schema.StringAttribute{
				Optional: true,
				Description: "ARN of an IAM role to assume with the credentials resolved from profile, access_key/secret_key " +
					"or the AWS default chain, as the AWS provider's assume_role does. Requests are then signed with the " +
					"role's temporary credentials. Use it when the identity the run starts with (for instance an HCP " +
					"Terraform workload identity role) is not the account that holds the workspace. Conflicts with api_key. " +
					"The credentials are not refreshed.",
			},
			"role_session_name": schema.StringAttribute{
				Optional:    true,
				Description: "Session name of the assumed role, shown in CloudTrail. Defaults to terraform-provider-anthropic. Requires role_arn.",
			},
			"role_external_id": schema.StringAttribute{
				Optional:    true,
				Description: "External ID required by the trust policy of the assumed role. Requires role_arn.",
			},
			"role_duration_seconds": schema.Int64Attribute{
				Optional: true,
				Description: "Duration of the assumed role's session, from 900 to 43200 seconds and within the maximum " +
					"session duration of the role. Defaults to 3600. AWS limits a role assumed from another role's " +
					"credentials (role chaining) to one hour, whatever the role allows. Requires role_arn.",
			},
			"base_url": schema.StringAttribute{
				Optional: true,
				Description: "Gateway base URL, in place of the one derived from region (an AWS PrivateLink endpoint, " +
					"for instance). Can also be set via the ANTHROPIC_AWS_BASE_URL environment variable; ANTHROPIC_BASE_URL " +
					"is not read. User information (user:password@) is sent as a Basic Authorization header and kept out of error messages.",
			},
		},
	}
}

// resolveAWS checks the configuration of a provider alias whose aws block is
// set and returns the SDK's client configuration for it. data.WorkspaceID is
// known: Configure rejects an unknown one first.
func resolveAWS(ctx context.Context, data AnthropicProviderModel) (*anthropicaws.ClientConfig, *awsSource, diag.Diagnostics) {
	var diags diag.Diagnostics

	// Configured values, not resolved ones: an Anthropic credential written in
	// the configuration is a mistake to report, even when unknown at plan
	// time, while the same credential exported in the environment is ignored
	// (see ignoredFirstPartyEnvironment).
	for _, other := range []struct {
		name  string
		value attr.Value
	}{
		{"api_key", data.ApiKey},
		{"admin_api_key", data.AdminApiKey},
		{"auth_token", data.AuthToken},
		{"federation", data.Federation},
		{"api_federation", data.APIFederation},
		{"cli", data.CLI},
	} {
		if !other.value.IsNull() {
			diags.AddAttributeError(path.Root("aws"), "Conflicting Platform Configuration",
				fmt.Sprintf("aws and %s cannot both be set: the aws block reaches Claude Platform on AWS with AWS "+
					"credentials, where no Anthropic credential is accepted. Use a separate provider alias for each "+
					"organization.", other.name))
		}
	}
	if diags.HasError() {
		return nil, nil, diags
	}

	if data.AWS.IsUnknown() {
		diags.AddAttributeError(path.Root("aws"), "Unknown AWS Configuration",
			"The aws block depends on a value that is not known until apply. Provider configuration must be known at plan time.")
		return nil, nil, diags
	}
	var m awsModel
	diags.Append(data.AWS.As(ctx, &m, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return nil, nil, diags
	}

	// Unknown is an error, not unset, as for the federation block: read as
	// unset, an unknown region would sign for the AWS_REGION of the machine
	// running the plan, and an unknown credential would fall back to the
	// default chain, another identity.
	attrs := []struct {
		name  string
		value types.String
	}{
		{"region", m.Region},
		{"profile", m.Profile},
		{"api_key", m.APIKey},
		{"access_key", m.AccessKey},
		{"secret_key", m.SecretKey},
		{"session_token", m.SessionToken},
		{"base_url", m.BaseURL},
		{"role_arn", m.RoleARN},
		{"role_session_name", m.RoleSessionName},
		{"role_external_id", m.RoleExternalID},
	}
	for _, a := range attrs {
		if a.value.IsUnknown() {
			diags.AddAttributeError(path.Root("aws").AtName(a.name), "Unknown AWS Configuration",
				fmt.Sprintf("aws.%s depends on a value that is not known until apply. Provider configuration must be known at plan time.", a.name))
		}
	}
	diags.Append(checkBaseURL(path.Root("aws").AtName("base_url"), "aws.base_url", m.BaseURL.ValueString(), awsBaseURLEnv)...)
	if diags.HasError() {
		return nil, nil, diags
	}

	// One credential source, stated: the SDK would otherwise pick by a
	// precedence the configuration does not show.
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

	if m.RoleDurationSeconds.IsUnknown() {
		diags.AddAttributeError(path.Root("aws").AtName("role_duration_seconds"), "Unknown AWS Configuration",
			"aws.role_duration_seconds depends on a value that is not known until apply. Provider configuration must be known at plan time.")
	}
	var configFiles []string
	if m.SharedConfigFiles.IsUnknown() {
		diags.AddAttributeError(path.Root("aws").AtName("shared_config_files"), "Unknown AWS Configuration",
			"aws.shared_config_files depends on a value that is not known until apply. Provider configuration must be known at plan time.")
	} else if !m.SharedConfigFiles.IsNull() {
		diags.Append(m.SharedConfigFiles.ElementsAs(ctx, &configFiles, false)...)
		for _, p := range configFiles {
			if p == "" {
				diags.AddAttributeError(path.Root("aws").AtName("shared_config_files"), "Invalid AWS Configuration",
					"aws.shared_config_files must not contain an empty path.")
			}
		}
		if len(configFiles) > 0 && (m.APIKey.ValueString() != "" || m.AccessKey.ValueString() != "") {
			diags.AddAttributeError(path.Root("aws").AtName("shared_config_files"), "Conflicting AWS Credentials",
				"aws.shared_config_files names where credentials are read from and cannot be combined with aws.api_key or aws.access_key.")
		}
	}
	roleARN := m.RoleARN.ValueString()
	if roleARN == "" {
		for _, a := range []struct {
			name string
			set  bool
		}{
			{"role_session_name", m.RoleSessionName.ValueString() != ""},
			{"role_external_id", m.RoleExternalID.ValueString() != ""},
			{"role_duration_seconds", !m.RoleDurationSeconds.IsNull()},
		} {
			if a.set {
				diags.AddAttributeError(path.Root("aws").AtName(a.name), "Missing Role",
					fmt.Sprintf("aws.%s is only used with aws.role_arn.", a.name))
			}
		}
	} else if m.APIKey.ValueString() != "" {
		diags.AddAttributeError(path.Root("aws").AtName("role_arn"), "Conflicting AWS Credentials",
			"aws.role_arn assumes a role with AWS credentials and cannot be combined with aws.api_key.")
	}
	if n := len(m.RoleSessionName.ValueString()); roleARN != "" && m.RoleSessionName.ValueString() != "" && (n < 2 || n > 64) {
		diags.AddAttributeError(path.Root("aws").AtName("role_session_name"), "Invalid Role Session Name",
			"aws.role_session_name must be between 2 and 64 characters.")
	}
	duration := defaultRoleDuration
	if !m.RoleDurationSeconds.IsNull() && !m.RoleDurationSeconds.IsUnknown() {
		duration = time.Duration(m.RoleDurationSeconds.ValueInt64()) * time.Second
		if duration < minRoleDuration || duration > maxRoleDuration {
			diags.AddAttributeError(path.Root("aws").AtName("role_duration_seconds"), "Invalid Role Duration",
				"aws.role_duration_seconds must be between 900 and 43200 seconds.")
		}
	}

	// The gateway needs the header on every inference and resource request,
	// and the SDK refuses to build a client without it, even for an alias that
	// only manages workspaces.
	workspaceID := data.WorkspaceID.ValueString()
	if workspaceID == "" {
		workspaceID = os.Getenv(awsWorkspaceIDEnv)
		// The attribute's shape is checked at plan time, the variable's here:
		// a stray newline or a wrong value would otherwise reach the header,
		// where net/http rejects every request with an error naming nothing.
		if workspaceID != "" && !workspaceIDPattern.MatchString(workspaceID) {
			diags.AddAttributeError(path.Root("workspace_id"), "Invalid Workspace",
				fmt.Sprintf("The %s environment variable must be a workspace ID (wrkspc_ followed by letters and "+
					"digits), got %q.", awsWorkspaceIDEnv, workspaceID))
		}
	}
	if workspaceID == "" {
		diags.AddAttributeError(path.Root("workspace_id"), "Missing Workspace",
			"Claude Platform on AWS identifies the workspace of every inference and resource request by the "+
				"anthropic-workspace-id header. Set workspace_id or the "+awsWorkspaceIDEnv+" environment variable "+
				"(ANTHROPIC_WORKSPACE_ID is not read with the aws block). A configuration that only manages workspaces "+
				"needs one too: any workspace of the same region.")
	}

	// ANTHROPIC_AWS_API_KEY fills api_key only when the block sets no
	// credential of its own: a configured argument wins over the environment.
	// Decided once and handed to the SDK as is, so that the region check
	// below and the SDK agree on whether requests are signed.
	apiKey := m.APIKey.ValueString()
	if sources == 0 && roleARN == "" && len(configFiles) == 0 {
		apiKey = os.Getenv("ANTHROPIC_AWS_API_KEY")
	}

	// The SDK falls back to these variables itself; checking them here puts
	// the error on the attribute instead of in an SDK message.
	region := firstNonEmpty(m.Region.ValueString(), os.Getenv("AWS_REGION"), os.Getenv("AWS_DEFAULT_REGION"))
	signs := apiKey == ""
	derivesURL := firstNonEmpty(m.BaseURL.ValueString(), os.Getenv(awsBaseURLEnv)) == ""
	if region == "" && (signs || derivesURL) {
		diags.AddAttributeError(path.Root("aws").AtName("region"), "Missing AWS Region",
			"Set aws.region or the AWS_REGION environment variable: it selects the gateway endpoint and the SigV4 "+
				"signing region, and a workspace is reachable only through the region it was created in.")
	}
	if diags.HasError() {
		return nil, nil, diags
	}

	var source *awsSource
	if roleARN != "" || len(configFiles) > 0 {
		source = &awsSource{region: region, configFiles: configFiles}
	}
	if roleARN != "" {
		sessionName := m.RoleSessionName.ValueString()
		if sessionName == "" {
			sessionName = defaultRoleSessionName
		}
		source.role = &awsRole{arn: roleARN, sessionName: sessionName, externalID: m.RoleExternalID.ValueString(), duration: duration}
	}

	return &anthropicaws.ClientConfig{
		APIKey:             apiKey,
		AWSAccessKey:       m.AccessKey.ValueString(),
		AWSSecretAccessKey: m.SecretKey.ValueString(),
		AWSSessionToken:    m.SessionToken.ValueString(),
		AWSProfile:         m.Profile.ValueString(),
		AWSRegion:          m.Region.ValueString(),
		WorkspaceID:        workspaceID,
		BaseURL:            m.BaseURL.ValueString(),
	}, source, diags
}

// resolveAWSCredentials replaces the credentials of cfg with static ones that
// this provider resolves itself, when the block names shared_config_files or
// role_arn: from the files, a profile, access_key/secret_key or the AWS default
// chain, then, with role_arn, from the role assumed with them, as the AWS
// provider's assume_role does. The SDK client takes static credentials and does
// not refresh them. A role assumed from another role's credentials lasts at most
// an hour, whatever role_duration_seconds says. The STS endpoint follows the
// SDK's own AWS_ENDPOINT_URL_STS.
func resolveAWSCredentials(ctx context.Context, cfg *anthropicaws.ClientConfig, src awsSource) error {
	loadOpts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(src.region)}
	if len(src.configFiles) > 0 {
		loadOpts = append(loadOpts, awsconfig.WithSharedConfigFiles(src.configFiles))
	}
	if cfg.AWSProfile != "" {
		loadOpts = append(loadOpts, awsconfig.WithSharedConfigProfile(cfg.AWSProfile))
	}
	if cfg.AWSAccessKey != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AWSAccessKey, cfg.AWSSecretAccessKey, cfg.AWSSessionToken)))
	}
	base, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return fmt.Errorf("load the AWS configuration: %w", err)
	}
	provider := base.Credentials
	if role := src.role; role != nil {
		provider = stscreds.NewAssumeRoleProvider(sts.NewFromConfig(base), role.arn, func(o *stscreds.AssumeRoleOptions) {
			o.RoleSessionName = role.sessionName
			o.Duration = role.duration
			if role.externalID != "" {
				o.ExternalID = awssdk.String(role.externalID)
			}
		})
	}
	creds, err := provider.Retrieve(ctx)
	if err != nil {
		if src.role != nil {
			return fmt.Errorf("assume %s: %w", src.role.arn, err)
		}
		return fmt.Errorf("retrieve the AWS credentials: %w", err)
	}
	cfg.AWSProfile = ""
	cfg.AWSAccessKey = creds.AccessKeyID
	cfg.AWSSecretAccessKey = creds.SecretAccessKey
	cfg.AWSSessionToken = creds.SessionToken
	return nil
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
	baseURL, credentials := platform.BaseURLWithoutUserInfo(cfg.BaseURL, awsBaseURLEnv, true)
	cfg.BaseURL = baseURL
	opts := append([]option.RequestOption{option.WithMiddleware(platform.NotFoundGuard(platform.AWS))}, credentials...)
	opts = append(opts, extra...)
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
			tflog.Debug(ctx, "environment variable ignored by a platform provider configuration",
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
