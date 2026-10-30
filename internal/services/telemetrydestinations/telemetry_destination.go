// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package telemetrydestinations implements the organization's OpenTelemetry
// destinations, the OTLP/HTTP collectors the API exports Messages API and
// Managed Agents traces to: the anthropic_telemetry_destination resource. It
// goes through the Admin API client (internal/admin) with the
// `telemetry-destinations-2026-08-11` beta header.
package telemetrydestinations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// telemetryDestinationsBeta is the `anthropic-beta` value every telemetry
// destination request must carry.
const telemetryDestinationsBeta = "telemetry-destinations-2026-08-11"

// telemetryDestinationsPath is the collection endpoint.
const telemetryDestinationsPath = "/v1/organizations/telemetry_destinations"

// telemetryDestinationsPageLimit is the largest page a destination's
// workspace list serves.
const telemetryDestinationsPageLimit = "1000"

// Values of the API's unions, as the reference spells them.
const (
	stateEnabled  = "enabled"
	stateDisabled = "disabled"

	workspacesAll     = "all"
	workspacesInclude = "include"
	workspacesExclude = "exclude"

	contentModeMetadataOnly = "metadata_only"
	contentModeContent      = "content"
)

// maxWorkspaceIDsPerRequest is the API's cap on workspace_ids in one create or
// update request, and so on the resource's workspace_ids.
const maxWorkspaceIDsPerRequest = 100

// telemetryDestinationPath returns the item endpoint, with the ID path-escaped.
func telemetryDestinationPath(id string) string {
	return telemetryDestinationsPath + "/" + url.PathEscape(id)
}

// doTelemetryRequest sends one Admin API request with the beta header these
// endpoints require.
func doTelemetryRequest(ctx context.Context, client *admin.Client, method, path string, body any) ([]byte, error) {
	return client.DoRequest(ctx, method, path, body, admin.WithBeta(telemetryDestinationsBeta))
}

// --- Admin API response types ---

type telemetryDestinationAPIResponse struct {
	ID             string                                `json:"id"`
	DisplayName    *string                               `json:"display_name"`
	EndpointURL    string                                `json:"endpoint_url"`
	HeaderNames    []string                              `json:"header_names"`
	State          telemetryDestinationAPIState          `json:"state"`
	Traces         telemetryDestinationAPITraces         `json:"traces"`
	Workspaces     telemetryDestinationAPIWorkspaces     `json:"workspaces"`
	DeliveryHealth telemetryDestinationAPIDeliveryHealth `json:"delivery_health"`
	CreatedAt      string                                `json:"created_at"`
	UpdatedAt      string                                `json:"updated_at"`
}

// telemetryDestinationAPIState flattens the enabled / disabled union; a
// disabled destination carries why (caller, sustained_failure,
// invalid_address, redirect).
type telemetryDestinationAPIState struct {
	Type   string `json:"type"`
	Reason *struct {
		Type string `json:"type"`
	} `json:"reason"`
}

type telemetryDestinationAPITraces struct {
	ContentMode     string  `json:"content_mode"`
	Enabled         bool    `json:"enabled"`
	EndpointURL     *string `json:"endpoint_url"`
	MaxContentBytes *int64  `json:"max_content_bytes"`
}

// telemetryDestinationAPIWorkspaces is the scope as reads return it: the type
// only. The workspace list itself comes from GET …/{id}/workspaces.
type telemetryDestinationAPIWorkspaces struct {
	Type string `json:"type"`
}

type telemetryDestinationAPIDeliveryHealth struct {
	LastError     *string `json:"last_error"`
	LastErrorAt   *string `json:"last_error_at"`
	LastSuccessAt *string `json:"last_success_at"`
}

type telemetryDestinationWorkspacesPage struct {
	Data []struct {
		WorkspaceID string `json:"workspace_id"`
	} `json:"data"`
	NextPage *string `json:"next_page"`
}

// --- Terraform data models ---

// TelemetryDestinationModel holds the attributes read back from the API,
// which the resource copies into its own model. Header values never appear:
// the API stores them encrypted and returns only their names.
type TelemetryDestinationModel struct {
	ID             types.String `tfsdk:"id"`
	DisplayName    types.String `tfsdk:"display_name"`
	EndpointURL    types.String `tfsdk:"endpoint_url"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	DisabledReason types.String `tfsdk:"disabled_reason"`
	Workspaces     types.Object `tfsdk:"workspaces"`
	Traces         types.Object `tfsdk:"traces"`
	HeaderNames    types.Set    `tfsdk:"header_names"`
	DeliveryHealth types.Object `tfsdk:"delivery_health"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
}

type workspacesModel struct {
	Type         types.String `tfsdk:"type"`
	WorkspaceIDs types.Set    `tfsdk:"workspace_ids"`
}

type tracesModel struct {
	ContentMode     types.String `tfsdk:"content_mode"`
	Enabled         types.Bool   `tfsdk:"enabled"`
	EndpointURL     types.String `tfsdk:"endpoint_url"`
	MaxContentBytes types.Int64  `tfsdk:"max_content_bytes"`
}

var workspacesAttrTypes = map[string]attr.Type{
	"type":          types.StringType,
	"workspace_ids": types.SetType{ElemType: types.StringType},
}

var tracesAttrTypes = map[string]attr.Type{
	"content_mode":      types.StringType,
	"enabled":           types.BoolType,
	"endpoint_url":      types.StringType,
	"max_content_bytes": types.Int64Type,
}

var deliveryHealthAttrTypes = map[string]attr.Type{
	"last_error":      types.StringType,
	"last_error_at":   types.StringType,
	"last_success_at": types.StringType,
}

// mapTelemetryDestinationToModel maps an API destination and its workspace
// list to TelemetryDestinationModel. workspaceIDs is ignored for type `all`,
// which has no list; an empty list maps to null, as the resource's
// workspace_ids is either omitted or non-empty.
func mapTelemetryDestinationToModel(ctx context.Context, d *telemetryDestinationAPIResponse, workspaceIDs []string) (TelemetryDestinationModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	ids := types.SetNull(types.StringType)
	if d.Workspaces.Type != workspacesAll && len(workspaceIDs) > 0 {
		v, dd := types.SetValueFrom(ctx, types.StringType, workspaceIDs)
		diags.Append(dd...)
		ids = v
	}
	workspaces, dd := types.ObjectValue(workspacesAttrTypes, map[string]attr.Value{
		"type":          types.StringValue(d.Workspaces.Type),
		"workspace_ids": ids,
	})
	diags.Append(dd...)

	maxBytes := types.Int64Null()
	if d.Traces.MaxContentBytes != nil {
		maxBytes = types.Int64Value(*d.Traces.MaxContentBytes)
	}
	traces, dd := types.ObjectValue(tracesAttrTypes, map[string]attr.Value{
		"content_mode":      types.StringValue(d.Traces.ContentMode),
		"enabled":           types.BoolValue(d.Traces.Enabled),
		"endpoint_url":      stringPtrOrNull(d.Traces.EndpointURL),
		"max_content_bytes": maxBytes,
	})
	diags.Append(dd...)

	health, dd := types.ObjectValue(deliveryHealthAttrTypes, map[string]attr.Value{
		"last_error":      stringPtrOrNull(d.DeliveryHealth.LastError),
		"last_error_at":   stringPtrOrNull(d.DeliveryHealth.LastErrorAt),
		"last_success_at": stringPtrOrNull(d.DeliveryHealth.LastSuccessAt),
	})
	diags.Append(dd...)

	names := d.HeaderNames
	if names == nil {
		names = []string{}
	}
	headerNames, dd := types.SetValueFrom(ctx, types.StringType, names)
	diags.Append(dd...)

	disabledReason := types.StringNull()
	if d.State.Type != stateEnabled && d.State.Reason != nil {
		disabledReason = tfvalue.StringOrNull(d.State.Reason.Type)
	}

	return TelemetryDestinationModel{
		ID:             types.StringValue(d.ID),
		DisplayName:    stringPtrOrNull(d.DisplayName),
		EndpointURL:    types.StringValue(d.EndpointURL),
		Enabled:        types.BoolValue(d.State.Type == stateEnabled),
		DisabledReason: disabledReason,
		Workspaces:     workspaces,
		Traces:         traces,
		HeaderNames:    headerNames,
		DeliveryHealth: health,
		CreatedAt:      tfvalue.StringOrNull(d.CreatedAt),
		UpdatedAt:      tfvalue.StringOrNull(d.UpdatedAt),
	}, diags
}

// stringPtrOrNull maps a nullable API string; "" maps to null too.
func stringPtrOrNull(s *string) types.String {
	if s == nil {
		return types.StringNull()
	}
	return tfvalue.StringOrNull(*s)
}

// getTelemetryDestination reads one destination.
func getTelemetryDestination(ctx context.Context, client *admin.Client, id string) (*telemetryDestinationAPIResponse, error) {
	respBytes, err := doTelemetryRequest(ctx, client, "GET", telemetryDestinationPath(id), nil)
	if err != nil {
		return nil, err
	}
	var d telemetryDestinationAPIResponse
	if err := json.Unmarshal(respBytes, &d); err != nil {
		return nil, fmt.Errorf("parse telemetry destination response: %w", err)
	}
	return &d, nil
}

// listTelemetryDestinationWorkspaceIDs pages through a destination's
// workspace list. Reads return only the scope's type, so this list endpoint is
// the only way to read workspace_ids back. A destination of type `all` has no
// list: callers skip the call for it.
func listTelemetryDestinationWorkspaceIDs(ctx context.Context, client *admin.Client, id string) ([]string, error) {
	var ids []string
	page := ""
	for {
		q := url.Values{}
		q.Set("limit", telemetryDestinationsPageLimit)
		if page != "" {
			q.Set("page", page)
		}
		respBytes, err := doTelemetryRequest(ctx, client, "GET", telemetryDestinationPath(id)+"/workspaces?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var p telemetryDestinationWorkspacesPage
		if err := json.Unmarshal(respBytes, &p); err != nil {
			return nil, fmt.Errorf("parse telemetry destination workspaces response: %w", err)
		}
		for _, e := range p.Data {
			ids = append(ids, e.WorkspaceID)
		}
		if p.NextPage == nil || *p.NextPage == "" || *p.NextPage == page {
			return ids, nil
		}
		page = *p.NextPage
	}
}

// readTelemetryDestination reads a destination and, unless it covers every
// workspace, its workspace list. When the list fails it returns the
// destination with the error, so that a caller can tell a 404 on the list
// from a destination that is gone.
func readTelemetryDestination(ctx context.Context, client *admin.Client, id string) (*telemetryDestinationAPIResponse, []string, error) {
	d, err := getTelemetryDestination(ctx, client, id)
	if err != nil {
		return nil, nil, err
	}
	if d.Workspaces.Type == workspacesAll {
		return d, nil, nil
	}
	ids, err := listTelemetryDestinationWorkspaceIDs(ctx, client, d.ID)
	if err != nil {
		return d, nil, fmt.Errorf("list the workspaces of telemetry destination %s: %w", d.ID, err)
	}
	return d, ids, nil
}
