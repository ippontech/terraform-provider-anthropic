// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestMapVaultResponseToState_BasicFields(t *testing.T) {
	createdAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	vault := &anthropic.BetaManagedAgentsVault{
		ID:          "vlt_01ABC",
		DisplayName: "my-vault",
		Type:        anthropic.BetaManagedAgentsVaultTypeVault,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
		Metadata:    nil,
	}

	var data VaultResourceModel
	diags := mapVaultResponseToState(vault, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if data.ID.ValueString() != "vlt_01ABC" {
		t.Errorf("expected ID vlt_01ABC, got %s", data.ID.ValueString())
	}
	if data.DisplayName.ValueString() != "my-vault" {
		t.Errorf("expected DisplayName my-vault, got %s", data.DisplayName.ValueString())
	}
	if data.Type.ValueString() != "vault" {
		t.Errorf("expected Type vault, got %s", data.Type.ValueString())
	}
	if data.CreatedAt.ValueString() != "2024-01-15T10:00:00Z" {
		t.Errorf("expected CreatedAt 2024-01-15T10:00:00Z, got %s", data.CreatedAt.ValueString())
	}
	if data.UpdatedAt.ValueString() != "2024-01-15T11:00:00Z" {
		t.Errorf("expected UpdatedAt 2024-01-15T11:00:00Z, got %s", data.UpdatedAt.ValueString())
	}
}

func TestMapVaultResponseToState_ArchivedAtZero(t *testing.T) {
	vault := &anthropic.BetaManagedAgentsVault{
		ID:          "vlt_02DEF",
		DisplayName: "unarchived",
		Type:        anthropic.BetaManagedAgentsVaultTypeVault,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		// ArchivedAt is zero value (not archived)
	}

	var data VaultResourceModel
	diags := mapVaultResponseToState(vault, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if !data.ArchivedAt.IsNull() {
		t.Errorf("expected ArchivedAt to be null for non-archived vault, got %s", data.ArchivedAt.ValueString())
	}
}

func TestMapVaultResponseToState_ArchivedAtNonZero(t *testing.T) {
	archivedAt := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	vault := &anthropic.BetaManagedAgentsVault{
		ID:          "vlt_03GHI",
		DisplayName: "archived-vault",
		Type:        anthropic.BetaManagedAgentsVaultTypeVault,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		ArchivedAt:  archivedAt,
	}

	var data VaultResourceModel
	diags := mapVaultResponseToState(vault, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if data.ArchivedAt.IsNull() {
		t.Error("expected ArchivedAt to be non-null for archived vault")
	}
	if data.ArchivedAt.ValueString() != "2024-06-01T12:00:00Z" {
		t.Errorf("expected ArchivedAt 2024-06-01T12:00:00Z, got %s", data.ArchivedAt.ValueString())
	}
}

func TestMapVaultResponseToState_MetadataPopulated(t *testing.T) {
	vault := &anthropic.BetaManagedAgentsVault{
		ID:          "vlt_04JKL",
		DisplayName: "vault-with-meta",
		Type:        anthropic.BetaManagedAgentsVaultTypeVault,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Metadata: map[string]string{
			"team": "platform",
			"env":  "prod",
		},
	}

	var data VaultResourceModel
	diags := mapVaultResponseToState(vault, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if data.Metadata.IsNull() {
		t.Fatal("expected Metadata to be non-null")
	}

	elements := data.Metadata.Elements()
	if len(elements) != 2 {
		t.Fatalf("expected 2 metadata entries, got %d", len(elements))
	}

	teamVal, ok := elements["team"]
	if !ok {
		t.Fatal("expected metadata key 'team'")
	}
	if teamVal.(types.String).ValueString() != "platform" {
		t.Errorf("expected metadata team=platform, got %s", teamVal.(types.String).ValueString())
	}
}

func TestMapVaultResponseToState_MetadataEmpty(t *testing.T) {
	vault := &anthropic.BetaManagedAgentsVault{
		ID:          "vlt_05MNO",
		DisplayName: "vault-no-meta",
		Type:        anthropic.BetaManagedAgentsVaultTypeVault,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Metadata:    nil,
	}

	var data VaultResourceModel
	diags := mapVaultResponseToState(vault, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if !data.Metadata.IsNull() {
		t.Errorf("expected Metadata to be null when API returns nil, got %v", data.Metadata)
	}
}
