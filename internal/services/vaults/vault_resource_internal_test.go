// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
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

// newStaleThenFreshVaultServer serves the vault as it was before the write for
// the first staleReads Get calls, then as it is after. It reproduces the
// read-after-write window measured against the live vaults API.
func newStaleThenFreshVaultServer(t *testing.T, vaultID string, before, after time.Time, staleReads int) (*httptest.Server, *int) {
	t.Helper()

	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		gets++
		updated := after
		if gets <= staleReads {
			updated = before
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"id":           vaultID,
			"type":         "vault",
			"display_name": "probe",
			"metadata":     map[string]string{},
			"created_at":   before.Format(time.RFC3339Nano),
			"updated_at":   updated.Format(time.RFC3339Nano),
			"archived_at":  nil,
		}); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &gets
}

func newTestVaultClient(t *testing.T, srv *httptest.Server) *anthropic.Client {
	t.Helper()

	c := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
	return &c
}

// TestAwaitVaultUpdateVisible_pollsUntilFresh is the regression test for the
// flaky TestAccVaultResource_update: the vaults API can answer a Get with the
// pre-update object for up to ~1s after a successful write, which made the
// post-apply refresh read stale values and the next plan show a phantom diff.
func TestAwaitVaultUpdateVisible_pollsUntilFresh(t *testing.T) {
	before := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	after := before.Add(time.Second)

	srv, gets := newStaleThenFreshVaultServer(t, "vlt_01ABC", before, after, 3)
	client := newTestVaultClient(t, srv)

	awaitVaultUpdateVisible(context.Background(), client, "vlt_01ABC", after, 2*time.Second, time.Millisecond)

	if *gets != 4 {
		t.Errorf("Get calls = %d, want 4 (three stale reads then the fresh one)", *gets)
	}
}

// A Get that already reflects the write must not be polled a second time.
func TestAwaitVaultUpdateVisible_returnsImmediatelyWhenFresh(t *testing.T) {
	before := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	after := before.Add(time.Second)

	srv, gets := newStaleThenFreshVaultServer(t, "vlt_01ABC", before, after, 0)
	client := newTestVaultClient(t, srv)

	awaitVaultUpdateVisible(context.Background(), client, "vlt_01ABC", after, 2*time.Second, time.Millisecond)

	if *gets != 1 {
		t.Errorf("Get calls = %d, want 1", *gets)
	}
}

// An updated_at equal to the write timestamp counts as visible: the comparison
// must not require a strictly newer value, or the loop would always time out.
func TestAwaitVaultUpdateVisible_equalTimestampCountsAsVisible(t *testing.T) {
	writtenAt := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	srv, gets := newStaleThenFreshVaultServer(t, "vlt_01ABC", writtenAt, writtenAt, 0)
	client := newTestVaultClient(t, srv)

	awaitVaultUpdateVisible(context.Background(), client, "vlt_01ABC", writtenAt, 200*time.Millisecond, time.Millisecond)

	if *gets != 1 {
		t.Errorf("Get calls = %d, want 1", *gets)
	}
}

// The wait is best-effort: a vault that never converges must return at the
// timeout rather than hang or surface an error, because the write itself already
// succeeded.
func TestAwaitVaultUpdateVisible_givesUpAtTimeout(t *testing.T) {
	before := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	after := before.Add(time.Second)

	// staleReads far beyond what the timeout allows: it never becomes visible.
	srv, gets := newStaleThenFreshVaultServer(t, "vlt_01ABC", before, after, 1_000_000)
	client := newTestVaultClient(t, srv)

	start := time.Now()
	awaitVaultUpdateVisible(context.Background(), client, "vlt_01ABC", after, 120*time.Millisecond, 10*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("took %s, want it to give up near the 120ms timeout", elapsed)
	}
	if *gets < 2 {
		t.Errorf("Get calls = %d, want it to have retried at least once", *gets)
	}
}

// A cancelled context must abort the wait promptly.
func TestAwaitVaultUpdateVisible_honoursContextCancellation(t *testing.T) {
	before := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	after := before.Add(time.Second)

	srv, _ := newStaleThenFreshVaultServer(t, "vlt_01ABC", before, after, 1_000_000)
	client := newTestVaultClient(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	awaitVaultUpdateVisible(ctx, client, "vlt_01ABC", after, 10*time.Second, 50*time.Millisecond)

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s, want an immediate return on a cancelled context", elapsed)
	}
}

// newFailingVaultServer answers every Get with status, so the poll can never
// converge.
func newFailingVaultServer(t *testing.T, status int) (*httptest.Server, *int) {
	t.Helper()

	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		gets++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := w.Write([]byte(`{"type":"error","error":{"type":"not_found_error","message":"not found"}}`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &gets
}

// A vault deleted out-of-band (or a key that lost access to it) answers every
// Get with a terminal status. Polling it to the deadline would stall the apply
// for seconds on a read that can never converge, so the wait must bail out on
// the first such answer.
func TestAwaitVaultUpdateVisible_stopsOnTerminalReadError(t *testing.T) {
	for _, status := range []int{401, 403, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv, gets := newFailingVaultServer(t, status)
			client := newTestVaultClient(t, srv)

			writtenAt := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

			start := time.Now()
			awaitVaultUpdateVisible(context.Background(), client, "vlt_01ABC", writtenAt, 10*time.Second, 50*time.Millisecond)

			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("took %s, want an immediate return on a terminal read error", elapsed)
			}
			if *gets != 1 {
				t.Errorf("Get calls = %d, want 1 (no retry on a terminal status)", *gets)
			}
		})
	}
}

// A non-terminal failure says nothing about whether the vault will become
// visible, so it must keep polling until the deadline like a stale read does.
// 422 is used because the SDK client does not retry it internally.
func TestAwaitVaultUpdateVisible_keepsPollingOnOtherReadErrors(t *testing.T) {
	srv, gets := newFailingVaultServer(t, 422)
	client := newTestVaultClient(t, srv)

	writtenAt := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	start := time.Now()
	awaitVaultUpdateVisible(context.Background(), client, "vlt_01ABC", writtenAt, 120*time.Millisecond, 10*time.Millisecond)

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s, want it to give up near the 120ms timeout", elapsed)
	}
	if *gets < 2 {
		t.Errorf("Get calls = %d, want it to have retried at least once", *gets)
	}
}
