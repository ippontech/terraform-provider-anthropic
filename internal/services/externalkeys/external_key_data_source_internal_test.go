// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package externalkeys

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
)

// readDataSource drives d.Read with a config holding the given top-level
// values (null for every other attribute) and returns the response.
func readDataSource(t *testing.T, d datasource.DataSource, vals map[string]any) *datasource.ReadResponse {
	t.Helper()
	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", schemaResp.Diagnostics)
	}
	objType := schemaResp.Schema.Type().TerraformType(context.Background()).(tftypes.Object)

	req := datasource.ReadRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: objectValue(objType, vals)}}
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	d.Read(context.Background(), req, resp)
	return resp
}

// --- anthropic_external_key ---

func TestExternalKeyDataSource_Read(t *testing.T) {
	var gotPath string
	srv := newKeyServer(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		gotPath = r.URL.EscapedPath()
		_, _ = io.WriteString(w, awsKeyFixture("attached"))
	})

	resp := readDataSource(t, &ExternalKeyDataSource{client: admintest.NewClient(t, srv)}, map[string]any{"id": testKeyID})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	if gotPath != "/v1/organizations/external_keys/"+testKeyID {
		t.Errorf("path = %q", gotPath)
	}

	var data ExternalKeyModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if data.DisplayName.ValueString() != "prod-us-key" || data.Attachment.ValueString() != "attached" {
		t.Errorf("state = %+v", data)
	}
	if got := data.ProviderConfig.Attributes()["kms_arn"]; !got.Equal(types.StringValue(testKMSARN)) {
		t.Errorf("provider_config.kms_arn = %v", got)
	}
}

func TestExternalKeyDataSource_Read_notFound(t *testing.T) {
	srv := newKeyServer(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"External key not found"}}`)
	})

	resp := readDataSource(t, &ExternalKeyDataSource{client: admintest.NewClient(t, srv)}, map[string]any{"id": testKeyID})
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "External Key Not Found" {
		t.Fatalf("diagnostics = %v, want External Key Not Found", resp.Diagnostics)
	}
}
