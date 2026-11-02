// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/config"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// defaultBaseURL is the SDK's production default, which a profile's base_url
// is compared against when ANTHROPIC_BASE_URL is unset.
const defaultBaseURL = "https://api.anthropic.com"

// adminScope is the OAuth scope the endpoints behind pd.OAuthClient require.
const adminScope = "org:admin"

// mandatoryRefreshThreshold mirrors the SDK's internal/auth
// MandatoryRefreshThreshold: a token with this little left counts as expired
// and must be refreshed before use. Unexported in the SDK, so copied here.
const mandatoryRefreshThreshold = 30 * time.Second

// profileNamePattern mirrors the SDK's config.validateProfileName, which
// config.LoadProfile applies and readCLIProfile, reading the files itself,
// must apply too: a name with a path separator or ".." would read a file
// outside the profile directories.
var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// cliModel is the provider's cli block.
type cliModel struct {
	Profile        types.String `tfsdk:"profile"`
	OrganizationID types.String `tfsdk:"organization_id"`
	WorkspaceID    types.String `tfsdk:"workspace_id"`
}

func cliSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional: true,
		Description: "Obtain the org:admin OAuth bearer token from a profile written by `ant auth login`, refreshing it " +
			"with the profile's refresh token. Used only when neither `auth_token` nor `federation` supplies the token, " +
			"so the same configuration runs locally as the operator and in CI as a workload. Without `profile`, the " +
			"provider looks for the one org:admin profile that matches `organization_id` (and `workspace_id`, when set).",
		Attributes: map[string]schema.Attribute{
			"profile": schema.StringAttribute{
				Optional:    true,
				Description: "Name of the `ant` profile to use. Can also be set via the ANTHROPIC_PROFILE environment variable.",
			},
			"organization_id": schema.StringAttribute{
				Optional:    true,
				Description: "UUID of the Anthropic organization, used to find the profile when none is named. Can also be set via the ANTHROPIC_ORGANIZATION_ID environment variable.",
			},
			"workspace_id": schema.StringAttribute{
				Optional:    true,
				Validators:  []validator.String{stringvalidator.RegexMatches(workspaceIDPattern, "must be a workspace ID (`wrkspc_` followed by letters and digits)")},
				Description: "Workspace (`wrkspc_...`) the profile was logged in to, used to narrow the search when none is named. Has no environment fallback: ANTHROPIC_WORKSPACE_ID is read by `federation.workspace_id` only.",
			},
		},
	}
}

// cliProfile is one profile on disk: its config, and what its credentials
// file says about the token it holds. The credentials record what the token
// was actually minted for, so they win over the config when both are present.
type cliProfile struct {
	name   string
	config *config.Config
	creds  *config.Credentials
}

func (p cliProfile) organizationID() string {
	if p.creds != nil && p.creds.OrganizationUUID != "" {
		return p.creds.OrganizationUUID
	}
	return p.config.OrganizationID
}

func (p cliProfile) workspaceID() string {
	if p.creds != nil && p.creds.WorkspaceID != "" {
		return p.creds.WorkspaceID
	}
	return p.config.WorkspaceID
}

func (p cliProfile) scope() string {
	if p.creds != nil && p.creds.Scope != "" {
		return p.creds.Scope
	}
	if u := p.config.AuthenticationInfo.UserOAuth; u != nil {
		return u.Scope
	}
	return ""
}

func (p cliProfile) hasAdminScope() bool {
	return slices.Contains(strings.Fields(p.scope()), adminScope)
}

// resolveCLI returns the profile's config, ready for option.WithConfig, or nil
// when the cli block is absent.
func resolveCLI(ctx context.Context, obj types.Object) (*config.Config, diag.Diagnostics) {
	var diags diag.Diagnostics
	if obj.IsNull() {
		return nil, diags
	}
	if obj.IsUnknown() {
		diags.AddAttributeError(path.Root("cli"), "Unknown CLI Configuration",
			"The cli block depends on a value that is not known until apply. Provider configuration must be known at plan time.")
		return nil, diags
	}

	var m cliModel
	diags.Append(obj.As(ctx, &m, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return nil, diags
	}

	diags.Append(rejectUnknownAttributes("cli", "Unknown CLI Configuration", []namedAttribute{
		{"profile", m.Profile},
		{"organization_id", m.OrganizationID},
		{"workspace_id", m.WorkspaceID},
	})...)
	if diags.HasError() {
		return nil, diags
	}

	dir := config.DefaultDir()
	if dir == "" {
		diags.AddAttributeError(path.Root("cli"), "No Anthropic Config Directory",
			"Set ANTHROPIC_CONFIG_DIR, or HOME so the default ~/.config/anthropic can be found.")
		return nil, diags
	}

	name := resolveCredential(m.Profile, "ANTHROPIC_PROFILE")
	orgID := resolveCredential(m.OrganizationID, "ANTHROPIC_ORGANIZATION_ID")
	// No environment fallback: ANTHROPIC_WORKSPACE_ID is the SDKs' federation
	// variable, often exported for other tools, and as a filter it would only
	// hide the operator's org:admin login.
	workspaceID := m.WorkspaceID.ValueString()

	var p *cliProfile
	var err error
	if name != "" {
		p, err = readCLIProfile(dir, name)
		if errors.Is(err, os.ErrNotExist) {
			err = fmt.Errorf("profile %q does not exist in %s. %s", name, dir, loginHint(name))
		}
		// Only the configured organization is checked against a named
		// profile: ANTHROPIC_ORGANIZATION_ID is often left exported from a
		// federation setup and must not reject the profile the operator chose.
		if configured := m.OrganizationID.ValueString(); err == nil && configured != "" && p.organizationID() != "" && p.organizationID() != configured {
			err = fmt.Errorf("profile %q is for organization %s, not %s", name, p.organizationID(), configured)
		}
	} else {
		p, err = findCLIProfile(dir, orgID, workspaceID)
	}
	if err == nil {
		err = checkCLIProfile(p)
	}
	if err != nil {
		diags.AddAttributeError(path.Root("cli"), "Unusable Anthropic CLI Profile", err.Error())
		return nil, diags
	}

	// A profile named in the configuration is the operator's choice, so a
	// missing scope is only a warning. One taken from ANTHROPIC_PROFILE is
	// usually their everyday login, exported for other tools, and would make
	// every request fail.
	if !p.hasAdminScope() {
		detail := fmt.Sprintf("Profile %q was granted scope %q, but the endpoints that use this token require %s. %s",
			p.name, p.scope(), adminScope, loginHint(p.name))
		if !m.Profile.IsNull() {
			diags.AddAttributeWarning(path.Root("cli"), "Anthropic CLI Profile Lacks org:admin", detail)
		} else {
			diags.AddAttributeError(path.Root("cli"), "Unusable Anthropic CLI Profile",
				detail+" It came from ANTHROPIC_PROFILE: name an org:admin profile with cli.profile, or unset the variable so the provider can find one by organization.")
			return nil, diags
		}
	}

	// The profile's base_url was checked against the provider's own, and its
	// workspace_id must not become a header: an org:admin token is not bound
	// to a workspace, and the provider's other clients never send one.
	cfg := *p.config
	cfg.BaseURL = ""
	cfg.WorkspaceID = ""
	return &cfg, diags
}

// findCLIProfile returns the one org:admin user_oauth profile for orgID, and
// for workspaceID when it is set. Profiles that cannot be read are skipped:
// they are someone else's problem until they are the only candidate.
func findCLIProfile(dir, orgID, workspaceID string) (*cliProfile, error) {
	if orgID == "" {
		return nil, errors.New("name a profile with cli.profile or ANTHROPIC_PROFILE, or set cli.organization_id or " +
			"ANTHROPIC_ORGANIZATION_ID so the provider can find one")
	}

	names, err := config.ListProfiles(dir)
	if err != nil {
		return nil, fmt.Errorf("list profiles in %s: %w", dir, err)
	}

	var matches []*cliProfile
	var unusable []error
	for _, name := range names {
		p, err := readCLIProfile(dir, name)
		if err != nil || p.config.AuthenticationInfo.Type != config.AuthenticationTypeUserOAuth {
			continue
		}
		if p.organizationID() != orgID || !p.hasAdminScope() {
			continue
		}
		if workspaceID != "" && p.workspaceID() != workspaceID {
			continue
		}
		// A stale login beside a working one is common after logging in
		// again under another name; it should not make discovery ambiguous.
		if err := checkCLIProfile(p); err != nil {
			unusable = append(unusable, err)
			continue
		}
		matches = append(matches, p)
	}

	target := "organization " + orgID
	if workspaceID != "" {
		target += " and workspace " + workspaceID
	}
	switch len(matches) {
	case 0:
		if len(unusable) > 0 {
			return nil, fmt.Errorf("no usable org:admin profile in %s is for %s: %w", dir, target, errors.Join(unusable...))
		}
		return nil, fmt.Errorf("no org:admin profile in %s is for %s. %s", dir, target, loginHint("admin"))
	case 1:
		return matches[0], nil
	}
	found := make([]string, len(matches))
	for i, p := range matches {
		found[i] = p.name
	}
	return nil, fmt.Errorf("%d org:admin profiles are for %s (%s). Name one with cli.profile or ANTHROPIC_PROFILE",
		len(matches), target, strings.Join(found, ", "))
}

// checkCLIProfile rejects a profile the SDK could not authenticate with, so
// the failure names the profile and the fix at configure time instead of
// surfacing on the first request.
func checkCLIProfile(p *cliProfile) error {
	if t := p.config.AuthenticationInfo.Type; t != config.AuthenticationTypeUserOAuth {
		return fmt.Errorf("profile %q authenticates with %s, not an interactive login; configure the federation block instead", p.name, t)
	}

	providerBaseURL := os.Getenv("ANTHROPIC_BASE_URL")
	if providerBaseURL == "" {
		providerBaseURL = defaultBaseURL
	}
	if b := p.config.BaseURL; b != "" && strings.TrimRight(b, "/") != strings.TrimRight(providerBaseURL, "/") {
		return fmt.Errorf("profile %q targets %s but the provider targets %s; set ANTHROPIC_BASE_URL to match or use another profile",
			p.name, b, providerBaseURL)
	}

	if p.creds == nil || p.creds.AccessToken == "" {
		return fmt.Errorf("profile %q is not logged in. %s", p.name, loginHint(p.name))
	}
	hasClientID := p.config.AuthenticationInfo.UserOAuth != nil && p.config.AuthenticationInfo.UserOAuth.ClientID != ""
	// The SDK refuses this shape on every request, refresh needed or not.
	if hasClientID && p.creds.RefreshToken == "" {
		return fmt.Errorf("profile %q has a client_id but no refresh token. %s", p.name, loginHint(p.name))
	}
	expired := p.creds.ExpiresAt != nil && time.Until(*p.creds.ExpiresAt) <= mandatoryRefreshThreshold
	canRefresh := p.creds.RefreshToken != "" && hasClientID
	if expired && !canRefresh {
		return fmt.Errorf("profile %q's login has expired and cannot be refreshed. %s", p.name, loginHint(p.name))
	}
	return nil
}

// readCLIProfile reads a profile without the SDK's environment fill-in
// (config.LoadProfile copies ANTHROPIC_ORGANIZATION_ID into a profile that
// lacks one, which would make every profile match). A missing credentials
// file is not an error here; it means the profile is logged out.
func readCLIProfile(dir, name string) (*cliProfile, error) {
	if !profileNamePattern.MatchString(name) {
		return nil, fmt.Errorf("profile name %q may contain only letters, digits, '_', '.' and '-'", name)
	}
	data, err := os.ReadFile(config.ProfilePath(dir, name))
	if err != nil {
		return nil, err
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse profile %q: %w", name, err)
	}
	if cfg.AuthenticationInfo == nil {
		return nil, fmt.Errorf("profile %q has no authentication", name)
	}
	if cfg.AuthenticationInfo.CredentialsPath == "" {
		cfg.AuthenticationInfo.CredentialsPath = config.ProfileCredentialsPath(dir, name)
	}

	p := &cliProfile{name: name, config: &cfg}
	data, err = os.ReadFile(cfg.AuthenticationInfo.CredentialsPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return p, nil
	case err != nil:
		return nil, fmt.Errorf("read credentials for profile %q: %w", name, err)
	}
	var creds config.Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parse credentials for profile %q: %w", name, err)
	}
	p.creds = &creds
	return p, nil
}

func loginHint(profile string) string {
	return fmt.Sprintf("Run `ant auth login --profile %s --scope %s`.", profile, adminScope)
}
