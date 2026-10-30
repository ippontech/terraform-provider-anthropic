// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/googlecloud"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	// googleCloudWorkspaceIDEnv is the workspace variable of Claude Models on
	// Google Cloud, read in place of ANTHROPIC_WORKSPACE_ID when the
	// google_cloud block is set.
	googleCloudWorkspaceIDEnv = "ANTHROPIC_GOOGLE_CLOUD_WORKSPACE_ID"
	// googleCloudScope is the OAuth scope the gateway requires.
	googleCloudScope = "https://www.googleapis.com/auth/cloud-platform"
)

// googleCloudCredentialTypes are the credential files `credentials` accepts.
// google.CredentialsFromJSON also takes an OAuth client configuration, which
// would start an interactive flow, so the type is checked first, as Google
// asks of any credential configuration a program did not create itself.
var googleCloudCredentialTypes = []string{
	"service_account", "authorized_user", "external_account",
	"external_account_authorized_user", "impersonated_service_account",
}

// googleCloudModel is the provider's google_cloud block.
type googleCloudModel struct {
	Project     types.String `tfsdk:"project"`
	Location    types.String `tfsdk:"location"`
	Credentials types.String `tfsdk:"credentials"`
	AccessToken types.String `tfsdk:"access_token"`
	BaseURL     types.String `tfsdk:"base_url"`
}

func googleCloudSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional: true,
		Description: "**Beta.** Reach Claude Models on Google Cloud (Anthropic managed) instead of the Claude API: the provider alias " +
			"then manages the Anthropic organization tied to a Google Cloud project, through claude.googleapis.com. " +
			"Setting the block is what selects the platform; the ANTHROPIC_GOOGLE_CLOUD_* and GOOGLE_CLOUD_PROJECT " +
			"environment variables only fill its attributes. Conflicts with api_key, admin_api_key, auth_token, " +
			"federation and aws, and the first-party ANTHROPIC_* variables are ignored. Requests carry a Google " +
			"access token (cloud-platform scope) from credentials, access_token or Application Default Credentials. " +
			"workspace_id goes into the request URL, never into a header, and may be left unset only for an alias " +
			"limited to organization routes. Resources the gateway does not serve fail at plan time. Support for " +
			"this platform is verified with offline tests only, and the provider prints a warning saying so.",
		Attributes: map[string]schema.Attribute{
			"project": schema.StringAttribute{
				Optional: true,
				Description: "Google Cloud project whose Anthropic organization the alias manages. Can also be set via " +
					"the ANTHROPIC_GOOGLE_CLOUD_PROJECT or GOOGLE_CLOUD_PROJECT environment variable; otherwise the " +
					"project of the credentials, when they carry one.",
			},
			"location": schema.StringAttribute{
				Optional: true,
				Description: "Gateway location. Defaults to `global`, which the documentation recommends; it does not " +
					"pin where inference runs (use inference_geo for that). Can also be set via the " +
					"ANTHROPIC_GOOGLE_CLOUD_LOCATION environment variable.",
			},
			"credentials": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "Contents of a Google credential file: a service account key, an authorized user, an " +
					"external account (Workload Identity Federation) or an impersonated service account. Tokens are " +
					"refreshed as they expire. Conflicts with access_token. Without either, Application Default " +
					"Credentials apply (GOOGLE_APPLICATION_CREDENTIALS, gcloud's application default login, the " +
					"metadata server of the workload).",
			},
			"access_token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "A Google OAuth 2.0 access token with the cloud-platform scope, for instance " +
					"data.google_client_config.this.access_token. It is not refreshed and typically expires after " +
					"one hour, so a longer apply fails: prefer credentials or Application Default Credentials. " +
					"Conflicts with credentials.",
			},
			"base_url": schema.StringAttribute{
				Optional: true,
				Description: "Base URL for every route, in place of the ones derived from project, location and " +
					"workspace (the workspace endpoint, and the organization endpoint for /v1/organizations routes). " +
					"Can also be set via the ANTHROPIC_GOOGLE_CLOUD_BASE_URL environment variable; ANTHROPIC_BASE_URL is " +
					"not read.",
			},
		},
	}
}

// googleCloudSettings is the google_cloud block resolved for the SDK.
type googleCloudSettings struct {
	config googlecloud.ClientConfig
	// workspace reports whether a workspace is configured. Without one, only
	// organization routes can be served.
	workspace bool
}

// resolveGoogleCloud checks the configuration of a provider alias whose
// google_cloud block is set and returns the SDK's client configuration for
// it. data.WorkspaceID is known: Configure rejects an unknown one first.
func resolveGoogleCloud(ctx context.Context, data AnthropicProviderModel) (*googleCloudSettings, diag.Diagnostics) {
	var diags diag.Diagnostics

	// Configured values: see resolveAWS. aws is checked by Configure.
	for _, other := range []struct {
		name  string
		value attr.Value
	}{
		{"api_key", data.ApiKey},
		{"admin_api_key", data.AdminApiKey},
		{"auth_token", data.AuthToken},
		{"federation", data.Federation},
	} {
		if !other.value.IsNull() {
			diags.AddAttributeError(path.Root("google_cloud"), "Conflicting Platform Configuration",
				fmt.Sprintf("google_cloud and %s cannot both be set: the google_cloud block reaches Claude Models on "+
					"Google Cloud with Google credentials, where no Anthropic credential is accepted. Use a separate "+
					"provider alias for each organization.", other.name))
		}
	}
	if diags.HasError() {
		return nil, diags
	}

	if data.GoogleCloud.IsUnknown() {
		diags.AddAttributeError(path.Root("google_cloud"), "Unknown Google Cloud Configuration",
			"The google_cloud block depends on a value that is not known until apply. Provider configuration must be known at plan time.")
		return nil, diags
	}
	var m googleCloudModel
	diags.Append(data.GoogleCloud.As(ctx, &m, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return nil, diags
	}

	// Unknown is an error, not unset: an unknown access_token would fall back
	// to Application Default Credentials, another identity, and an unknown
	// project to the environment's.
	for _, a := range []struct {
		name  string
		value types.String
	}{
		{"project", m.Project},
		{"location", m.Location},
		{"credentials", m.Credentials},
		{"access_token", m.AccessToken},
		{"base_url", m.BaseURL},
	} {
		if a.value.IsUnknown() {
			diags.AddAttributeError(path.Root("google_cloud").AtName(a.name), "Unknown Google Cloud Configuration",
				fmt.Sprintf("google_cloud.%s depends on a value that is not known until apply. Provider configuration must be known at plan time.", a.name))
		}
	}
	if diags.HasError() {
		return nil, diags
	}

	settings := &googleCloudSettings{config: googlecloud.ClientConfig{
		Project:  m.Project.ValueString(),
		Location: m.Location.ValueString(),
		BaseURL:  m.BaseURL.ValueString(),
	}}

	switch {
	case m.Credentials.ValueString() != "" && m.AccessToken.ValueString() != "":
		diags.AddAttributeError(path.Root("google_cloud"), "Conflicting Google Cloud Credentials",
			"Set at most one of google_cloud.credentials and google_cloud.access_token.")
		return nil, diags
	case m.AccessToken.ValueString() != "":
		settings.config.TokenSource = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: m.AccessToken.ValueString(), TokenType: "Bearer"})
	case m.Credentials.ValueString() != "":
		creds, err := googleCredentialsFromJSON(ctx, m.Credentials.ValueString())
		if err != nil {
			diags.AddAttributeError(path.Root("google_cloud").AtName("credentials"), "Invalid Google Cloud Credentials", err.Error())
			return nil, diags
		}
		settings.config.TokenSource = creds.TokenSource
		// The SDK takes the project from ADC only when it resolves ADC itself.
		if settings.config.Project == "" && os.Getenv("ANTHROPIC_GOOGLE_CLOUD_PROJECT") == "" && os.Getenv("GOOGLE_CLOUD_PROJECT") == "" {
			settings.config.Project = creds.ProjectID
		}
	}

	// The workspace goes into the URL. Unset, the SDK still serves the
	// organization routes, so it is optional here and the registry refuses
	// the types that need it (gate.go).
	settings.config.WorkspaceID = data.WorkspaceID.ValueString()
	if settings.config.WorkspaceID == "" {
		settings.config.WorkspaceID = os.Getenv(googleCloudWorkspaceIDEnv)
	}
	settings.workspace = settings.config.WorkspaceID != ""

	return settings, diags
}

// googleCredentialsFromJSON parses a credential file of an accepted type. The
// type is checked against googleCloudCredentialTypes, then passed to the
// typed loader, which refuses a file whose type differs from it.
func googleCredentialsFromJSON(ctx context.Context, contents string) (*google.Credentials, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(contents), &head); err != nil {
		return nil, fmt.Errorf("google_cloud.credentials is not a JSON credential file: %w", err)
	}
	if !slices.Contains(googleCloudCredentialTypes, head.Type) {
		return nil, fmt.Errorf("google_cloud.credentials has type %q; accepted types: %v", head.Type, googleCloudCredentialTypes)
	}
	return google.CredentialsFromJSONWithType(ctx, []byte(contents), google.CredentialsType(head.Type), googleCloudScope)
}

// newGoogleCloudClient builds the single SDK client of a Claude Models on
// Google Cloud alias. Application Default Credentials, when used, are found
// now (the metadata server is probed if no file is configured), so a missing
// identity fails at Configure.
//
// As for AWS, the options become a client through anthropic.NewClient with
// the marker option, which keeps the first-party environment out:
// googlecloud.NewClient builds services, not an *anthropic.Client. The SDK
// derives the base URL per request: the workspace endpoint, or the
// organization endpoint for /v1/organizations routes. No option here sets a
// base URL or an anthropic-workspace-id header, which this gateway forbids.
//
// extra are request options for tests (an HTTP client).
func newGoogleCloudClient(ctx context.Context, cfg googlecloud.ClientConfig, extra ...option.RequestOption) (*anthropic.Client, error) {
	gc, err := googlecloud.NewClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	opts := append([]option.RequestOption{option.WithoutEnvironmentDefaults()}, gc.Options...)
	opts = append(opts, option.WithMiddleware(platform.NotFoundGuard(platform.GoogleCloud)))
	client := anthropic.NewClient(append(opts, extra...)...)
	return &client, nil
}
