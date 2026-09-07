test {
  parallel = true
}

# Admin API resource: no dedicated test organization exists yet (#58), so this
# test plans the public example with a dummy admin key. Configure only needs a
# non-empty credential and a create plan never calls the API (#233).
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "workspace_plan_validates_schema" {
  command = plan

  module {
    source = "./examples/resources/workspace"
  }

  assert {
    condition     = anthropic_workspace.example.name == "Example Workspace"
    error_message = "Expected workspace name to be 'Example Workspace'."
  }

  assert {
    condition     = anthropic_workspace.example.data_residency.workspace_geo == "us"
    error_message = "Expected workspace_geo to be 'us'."
  }

  assert {
    condition     = anthropic_workspace.example.data_residency.default_inference_geo == "global"
    error_message = "Expected default_inference_geo to be 'global'."
  }

  assert {
    condition     = anthropic_workspace.example.data_residency.allowed_inference_geos == tolist(["unrestricted"])
    error_message = "Expected allowed_inference_geos to be ['unrestricted']."
  }
}
