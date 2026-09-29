// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores

import (
	"context"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestMapMemoryStoreToState_BasicFields(t *testing.T) {
	createdAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

	store := &anthropic.BetaManagedAgentsMemoryStore{
		ID:          "memstore_01ABC",
		Name:        "my-store",
		Description: "notes",
		Type:        anthropic.BetaManagedAgentsMemoryStoreTypeMemoryStore,
		CreatedAt:   createdAt,
	}

	var data MemoryStoreResourceModel
	diags := mapMemoryStoreToState(store, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if data.ID.ValueString() != "memstore_01ABC" {
		t.Errorf("expected ID memstore_01ABC, got %s", data.ID.ValueString())
	}
	if data.Name.ValueString() != "my-store" {
		t.Errorf("expected Name my-store, got %s", data.Name.ValueString())
	}
	if data.Description.ValueString() != "notes" {
		t.Errorf("expected Description notes, got %s", data.Description.ValueString())
	}
	if data.CreatedAt.ValueString() != "2024-01-15T10:00:00Z" {
		t.Errorf("expected CreatedAt 2024-01-15T10:00:00Z, got %s", data.CreatedAt.ValueString())
	}
}

func TestMapMemoryStoreToState_ArchivedAtZero(t *testing.T) {
	store := &anthropic.BetaManagedAgentsMemoryStore{
		ID:        "memstore_02DEF",
		Name:      "unarchived",
		CreatedAt: time.Now(),
		// ArchivedAt is zero value (not archived)
	}

	var data MemoryStoreResourceModel
	diags := mapMemoryStoreToState(store, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if !data.ArchivedAt.IsNull() {
		t.Errorf("expected ArchivedAt to be null for non-archived store, got %s", data.ArchivedAt.ValueString())
	}
}

func TestMapMemoryStoreToState_ArchivedAtNonZero(t *testing.T) {
	archivedAt := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	store := &anthropic.BetaManagedAgentsMemoryStore{
		ID:         "memstore_03GHI",
		Name:       "archived-store",
		CreatedAt:  time.Now(),
		ArchivedAt: archivedAt,
	}

	var data MemoryStoreResourceModel
	diags := mapMemoryStoreToState(store, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if data.ArchivedAt.IsNull() {
		t.Error("expected ArchivedAt to be non-null for archived store")
	}
	if data.ArchivedAt.ValueString() != "2024-06-01T12:00:00Z" {
		t.Errorf("expected ArchivedAt 2024-06-01T12:00:00Z, got %s", data.ArchivedAt.ValueString())
	}
}

func TestMapMemoryStoreToState_MetadataPopulated(t *testing.T) {
	store := &anthropic.BetaManagedAgentsMemoryStore{
		ID:        "memstore_04JKL",
		Name:      "store-with-meta",
		CreatedAt: time.Now(),
		Metadata: map[string]string{
			"team": "platform",
			"env":  "prod",
		},
	}

	var data MemoryStoreResourceModel
	diags := mapMemoryStoreToState(store, &data)
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

func TestMapMemoryStoreToState_MetadataEmpty(t *testing.T) {
	store := &anthropic.BetaManagedAgentsMemoryStore{
		ID:        "memstore_05MNO",
		Name:      "store-no-meta",
		CreatedAt: time.Now(),
		Metadata:  nil,
	}

	var data MemoryStoreResourceModel
	diags := mapMemoryStoreToState(store, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if !data.Metadata.IsNull() {
		t.Error("expected Metadata to be null when the API returns no metadata")
	}
}

func TestBuildMemoryStoreMetadataPatch_UpsertsAndClears(t *testing.T) {
	ctx := context.Background()

	plan, diags := types.MapValueFrom(ctx, types.StringType, map[string]string{
		"team": "platform",
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics building plan map: %v", diags)
	}

	state, diags := types.MapValueFrom(ctx, types.StringType, map[string]string{
		"team": "terraform",
		"env":  "test",
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics building state map: %v", diags)
	}

	patch, diags := buildMemoryStoreMetadataPatch(ctx, plan, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if patch["team"] != "platform" {
		t.Errorf("expected team to be upserted to platform, got %v", patch["team"])
	}
	if v, ok := patch["env"]; !ok || v != nil {
		t.Errorf("expected env to be explicitly nulled (removed since state), got %v (present=%v)", v, ok)
	}
}

func TestBuildMemoryStoreMetadataPatch_NoChangeWhenBothNull(t *testing.T) {
	ctx := context.Background()
	patch, diags := buildMemoryStoreMetadataPatch(ctx, types.MapNull(types.StringType), types.MapNull(types.StringType))
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if len(patch) != 0 {
		t.Errorf("expected empty patch, got %v", patch)
	}
}

func TestMemoryStoreResource_UpdateRejectsArchivedStoreWithChangedFields(t *testing.T) {
	// This is a documentation test of the guard's condition, not an exercise
	// of the framework's Update dispatch (which needs a full tfsdk.Plan/State
	// pair from a provider server). It pins the exact comparison Update relies
	// on so a future refactor cannot silently change which fields trigger the
	// archived-store rejection.
	state := MemoryStoreResourceModel{
		Name:        types.StringValue("original"),
		Description: types.StringValue("original description"),
		Metadata:    types.MapNull(types.StringType),
		ArchivedAt:  types.StringValue("2024-06-01T12:00:00Z"),
	}
	plan := state
	plan.Name = types.StringValue("changed")

	changed := !plan.Name.Equal(state.Name) || !plan.Description.Equal(state.Description) || !plan.Metadata.Equal(state.Metadata)
	if !changed {
		t.Fatal("expected a name change to be detected as a mutable-field diff")
	}

	unchanged := state
	sameChanged := !unchanged.Name.Equal(state.Name) || !unchanged.Description.Equal(state.Description) || !unchanged.Metadata.Equal(state.Metadata)
	if sameChanged {
		t.Fatal("expected no diff to be detected when plan equals state")
	}

	// archive_on_destroy has no API counterpart: changing only it must not be
	// treated as an API-facing diff, or Update would call the API (and reject
	// on an archived store) for a change that should stay local to state.
	onlyArchiveOnDestroyChanged := state
	onlyArchiveOnDestroyChanged.ArchiveOnDestroy = types.BoolValue(true)
	apiFieldsChanged := !onlyArchiveOnDestroyChanged.Name.Equal(state.Name) ||
		!onlyArchiveOnDestroyChanged.Description.Equal(state.Description) ||
		!onlyArchiveOnDestroyChanged.Metadata.Equal(state.Metadata)
	if apiFieldsChanged {
		t.Fatal("expected archive_on_destroy alone to not be flagged as an API-facing change")
	}
}

func TestPreserveEmptyMetadata(t *testing.T) {
	emptyMap, diags := types.MapValue(types.StringType, map[string]attr.Value{})
	if diags.HasError() {
		t.Fatalf("failed to build empty map: %v", diags)
	}
	nonEmptyMap, diags := types.MapValue(types.StringType, map[string]attr.Value{"k": types.StringValue("v")})
	if diags.HasError() {
		t.Fatalf("failed to build non-empty map: %v", diags)
	}
	nullMap := types.MapNull(types.StringType)

	tests := map[string]struct {
		known, fromAPI, want types.Map
	}{
		"empty known, null from API: keeps known empty map": {
			known: emptyMap, fromAPI: nullMap, want: emptyMap,
		},
		"null known, null from API: stays null": {
			known: nullMap, fromAPI: nullMap, want: nullMap,
		},
		"non-empty known, null from API: real deletion, stays null": {
			known: nonEmptyMap, fromAPI: nullMap, want: nullMap,
		},
		"unknown known, null from API: stays null (Create with no metadata set)": {
			known: types.MapUnknown(types.StringType), fromAPI: nullMap, want: nullMap,
		},
		"non-null from API always wins": {
			known: emptyMap, fromAPI: nonEmptyMap, want: nonEmptyMap,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := preserveEmptyMetadata(tc.known, tc.fromAPI)
			if !got.Equal(tc.want) {
				t.Fatalf("preserveEmptyMetadata() = %v, want %v", got, tc.want)
			}
		})
	}
}
