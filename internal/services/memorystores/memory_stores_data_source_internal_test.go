// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores

import (
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestMapMemoryStoreToListObject(t *testing.T) {
	store := &anthropic.BetaManagedAgentsMemoryStore{
		ID:          "memstore_01ABC",
		Name:        "my-store",
		Description: "notes",
		CreatedAt:   time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
		Metadata:    map[string]string{"team": "platform"},
	}

	obj, diags := mapMemoryStoreToListObject(store)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if obj.IsNull() || obj.IsUnknown() {
		t.Fatal("expected a known, non-null object value")
	}
}

func TestMapMemoryStoreToListObject_ArchivedAtZero(t *testing.T) {
	store := &anthropic.BetaManagedAgentsMemoryStore{
		ID:        "memstore_02DEF",
		Name:      "unarchived",
		CreatedAt: time.Now(),
	}

	obj, diags := mapMemoryStoreToListObject(store)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if obj.IsNull() {
		t.Fatal("expected a non-null object value")
	}
}
