// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

// workspaceMemberAPIResponse models the JSON returned by the Admin API
// single-member endpoints (POST/GET/POST update on /v1/organizations/workspaces/{id}/members[/{user_id}]).
// It is shared by the workspace_member resource, the workspace_member data source,
// and their unit tests.
type workspaceMemberAPIResponse struct {
	Type          string `json:"type"`
	UserID        string `json:"user_id"`
	WorkspaceRole string `json:"workspace_role"`
}
