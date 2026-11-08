// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

// The Plugins API's uploads are not idempotent: a repeated create or version
// upload stores a second plugin or version. They go through admin.DoRequest,
// which replays a POST only on a 429 (or when the server says so with
// `x-should-retry: true`, which the API sends on a version upload whose
// registration is pending and must be resent), never on a 5xx or a dropped
// connection.

// getPlugin reads one plugin.
func getPlugin(ctx context.Context, client *admin.Client, pluginID string) (*pluginAPIObject, error) {
	respBody, err := pluginsRequest(ctx, client, http.MethodGet, pluginPath(pluginID), nil, nil)
	if err != nil {
		return nil, err
	}
	return decodePlugin(respBody)
}

// createPlugin uploads a new organization-owned plugin and its first version.
//
// Two failures carry the ID of a plugin the API did store, and get special
// handling:
//   - 503 `registration_pending`: the plugin was created but its skills were
//     not registered with claude.ai. The API asks not to resend the create
//     (it would answer `plugin_name_taken`) but to upload the same files as a
//     version of the stored plugin, which completes it; this does that, then
//     reads the plugin back.
//   - 409 `plugin_name_taken`: a plugin with this name already exists in the
//     marketplace. The error names it, so it can be imported if it is the one
//     this configuration means to manage.
func createPlugin(ctx context.Context, client *admin.Client, src pluginSource, marketplaceID, releaseNotes string) (*pluginAPIObject, error) {
	var fields []formField
	if marketplaceID != "" {
		fields = append(fields, formField{name: "marketplace_id", value: marketplaceID})
	}
	if releaseNotes != "" {
		fields = append(fields, formField{name: "release_notes", value: releaseNotes})
	}
	body, _, err := src.uploadBody(fields...)
	if err != nil {
		return nil, err
	}

	respBody, err := pluginsRequest(ctx, client, http.MethodPost, pluginsPath, nil, body)
	if err != nil {
		status, details := pluginErrorOf(err)
		switch {
		case status == http.StatusServiceUnavailable && details.ErrorCode == "registration_pending" && details.PluginID != "":
			return completePluginRegistration(ctx, client, src, details.PluginID, releaseNotes)
		case status == http.StatusConflict && details.ErrorCode == "plugin_name_taken" && details.PluginID != "":
			return nil, fmt.Errorf("%w\n\nThe marketplace already holds a plugin with this name: %s. If it is the plugin "+
				"this configuration manages, import it with `terraform import`; otherwise change the manifest's name",
				err, details.PluginID)
		}
		return nil, err
	}
	return decodePlugin(respBody)
}

// completePluginRegistration finishes a create the API answered with
// `registration_pending`, as the API documents: by uploading the same files
// again as a version of the plugin it stored.
func completePluginRegistration(ctx context.Context, client *admin.Client, src pluginSource, pluginID, releaseNotes string) (*pluginAPIObject, error) {
	if _, err := uploadPluginVersion(ctx, client, src, pluginID, releaseNotes); err != nil {
		return nil, fmt.Errorf("plugin %s was created but its registration did not complete, and uploading the same files "+
			"as a version to complete it failed: %w. Import the plugin with `terraform import`, then apply again", pluginID, err)
	}
	return getPlugin(ctx, client, pluginID)
}

// servePluginVersion makes versionID the version members are served, which
// pins the plugin to it.
func servePluginVersion(ctx context.Context, client *admin.Client, pluginID, versionID string) (*pluginAPIObject, error) {
	respBody, err := pluginsRequest(ctx, client, http.MethodPost, pluginPath(pluginID), nil,
		map[string]string{"served_version_id": versionID})
	if err != nil {
		status, details := pluginErrorOf(err)
		switch {
		case status == http.StatusConflict && details.ErrorCode == "scan_pending":
			return nil, fmt.Errorf("%w\n\nThe version's content scan is still running: apply again once it has finished", err)
		case status == http.StatusBadRequest && details.ErrorCode == "scan_failed":
			return nil, fmt.Errorf("%w\n\nThe version's content scan failed, so members cannot be served it: choose another version", err)
		}
		return nil, err
	}
	return decodePlugin(respBody)
}

// deletePlugin deletes a plugin and every version it holds. A plugin that is
// already gone is not an error.
func deletePlugin(ctx context.Context, client *admin.Client, pluginID string) error {
	_, err := pluginsRequest(ctx, client, http.MethodDelete, pluginPath(pluginID), nil, nil)
	if err != nil && !admin.IsNotFound(err) {
		return err
	}
	return nil
}

// uploadPluginVersion uploads src as a new version of an existing plugin.
func uploadPluginVersion(ctx context.Context, client *admin.Client, src pluginSource, pluginID, releaseNotes string) (*pluginVersionAPIObject, error) {
	var fields []formField
	if releaseNotes != "" {
		fields = append(fields, formField{name: "release_notes", value: releaseNotes})
	}
	body, _, err := src.uploadBody(fields...)
	if err != nil {
		return nil, err
	}
	return postPluginVersion(ctx, client, pluginID, body)
}

// postPluginVersion sends an already-built version upload.
//
// A 503 `registration_pending` comes with `x-should-retry: true`, so the
// admin client resends the upload; each attempt stores a version. When the
// last attempt is still pending, the error names the version the API stored,
// which stays in the plugin's history: the API cannot delete one version.
func postPluginVersion(ctx context.Context, client *admin.Client, pluginID string, body admin.RawBody) (*pluginVersionAPIObject, error) {
	respBody, err := pluginsRequest(ctx, client, http.MethodPost, pluginPath(pluginID, "versions"), nil, body)
	if err != nil {
		status, details := pluginErrorOf(err)
		if status == http.StatusServiceUnavailable && details.ErrorCode == "registration_pending" && details.PluginVersionID != "" {
			return nil, fmt.Errorf("%w\n\nThe API stored this upload as version %s of plugin %s, but its registration "+
				"was still pending after the retries. That version stays in the plugin's version history, and "+
				"`served_version_id` on `anthropic_plugin` can serve it once its registration completes. Applying "+
				"again uploads another version", err, details.PluginVersionID, pluginID)
		}
		return nil, err
	}
	return decodePluginVersion(respBody)
}

// getPluginVersion reads one version of a plugin.
func getPluginVersion(ctx context.Context, client *admin.Client, pluginID, versionID string) (*pluginVersionAPIObject, error) {
	respBody, err := pluginsRequest(ctx, client, http.MethodGet, pluginPath(pluginID, "versions", versionID), nil, nil)
	if err != nil {
		return nil, err
	}
	return decodePluginVersion(respBody)
}

// --- Installation settings ---

// pluginSettingLocks serializes the installation-setting writes this provider
// sends for one plugin, keyed by plugin ID. The API handles a plugin's
// concurrent setting writes one after another and can answer some of them with
// a 503 instead of applying them, so it asks clients to send them one at a
// time; Terraform applies independent resources in parallel, so an
// organization-wide setting and a group setting of the same plugin would
// otherwise race. (The 503 carries `x-should-retry: true`, which
// admin.DoRequest honours, for writes from outside this provider.)
var pluginSettingLocks sync.Map

func lockPluginSettings(pluginID string) (unlock func()) {
	mu, _ := pluginSettingLocks.LoadOrStore(pluginID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	return mu.(*sync.Mutex).Unlock
}

// setPluginInstallationSetting creates or changes the setting a plugin holds
// for target (`organization` or an `rbac_group_` ID). Writing the value the
// target already holds changes nothing, so the call is safe to repeat.
func setPluginInstallationSetting(ctx context.Context, client *admin.Client, pluginID, target, preference string) (*pluginInstallationSettingAPIObject, error) {
	defer lockPluginSettings(pluginID)()
	respBody, err := pluginsRequest(ctx, client, http.MethodPost, pluginPath(pluginID, "installation_settings", target), nil,
		map[string]string{"installation_preference": preference})
	if err != nil {
		return nil, err
	}
	var s pluginInstallationSettingAPIObject
	if err := json.Unmarshal(respBody, &s); err != nil {
		return nil, fmt.Errorf("unable to parse the installation setting in the response: %w", err)
	}
	return &s, nil
}

// removePluginInstallationSetting removes the setting a plugin holds for
// target. A setting that is already gone (404) is not an error.
func removePluginInstallationSetting(ctx context.Context, client *admin.Client, pluginID, target string) error {
	defer lockPluginSettings(pluginID)()
	_, err := pluginsRequest(ctx, client, http.MethodDelete, pluginPath(pluginID, "installation_settings", target), nil, nil)
	if err != nil && !admin.IsNotFound(err) {
		return err
	}
	return nil
}

// findPluginInstallationSetting returns the setting a plugin holds for target,
// or nil when it holds none. There is no endpoint for one setting: it pages
// the plugin's settings, filtered by the target's type.
func findPluginInstallationSetting(ctx context.Context, client *admin.Client, pluginID, target string) (*pluginInstallationSettingAPIObject, error) {
	targetType := "rbac_group"
	if target == "organization" {
		targetType = "organization"
	}
	var found *pluginInstallationSettingAPIObject
	err := listPluginsPages(ctx, client, pluginPath(pluginID, "installation_settings"),
		url.Values{"target_type": {targetType}}, 100, func(s pluginInstallationSettingAPIObject) bool {
			if s.Target.pathSegment() == target {
				found = &s
				return false
			}
			return true
		})
	return found, err
}

// --- Marketplaces ---

func marketplacePath(marketplaceID string) string {
	return pluginMarketplacesPath + "/" + url.PathEscape(marketplaceID)
}

func getPluginMarketplace(ctx context.Context, client *admin.Client, marketplaceID string) (*pluginMarketplaceAPIObject, error) {
	respBody, err := pluginsRequest(ctx, client, http.MethodGet, marketplacePath(marketplaceID), nil, nil)
	if err != nil {
		return nil, err
	}
	return decodeMarketplace(respBody)
}

// setMarketplaceDefault sets the default installation setting of one of the
// organization's marketplaces (a member's personal marketplace answers 403).
// Setting the value already set changes nothing.
func setMarketplaceDefault(ctx context.Context, client *admin.Client, marketplaceID, preference string) (*pluginMarketplaceAPIObject, error) {
	respBody, err := pluginsRequest(ctx, client, http.MethodPost, marketplacePath(marketplaceID), nil,
		map[string]string{"default_installation_preference": preference})
	if err != nil {
		return nil, err
	}
	return decodeMarketplace(respBody)
}

func decodeMarketplace(body []byte) (*pluginMarketplaceAPIObject, error) {
	var m pluginMarketplaceAPIObject
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("unable to parse the plugin marketplace in the response: %w", err)
	}
	return &m, nil
}

func decodePlugin(body []byte) (*pluginAPIObject, error) {
	var p pluginAPIObject
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("unable to parse the plugin in the response: %w", err)
	}
	return &p, nil
}

func decodePluginVersion(body []byte) (*pluginVersionAPIObject, error) {
	var v pluginVersionAPIObject
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("unable to parse the plugin version in the response: %w", err)
	}
	return &v, nil
}
