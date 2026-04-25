test {
  parallel = true
}

run "workspace_plan_validates_schema" {
  command  = plan
  parallel = true

  module {
    source = "./tests/fixtures/workspace_plan"
  }

  assert {
    condition     = output.workspace_name == "plan-test-workspace"
    error_message = "Expected workspace name to be 'plan-test-workspace'."
  }

  assert {
    condition     = output.workspace_geo == "us"
    error_message = "Expected workspace_geo to be 'us'."
  }

  assert {
    condition     = output.default_inference_geo == "global"
    error_message = "Expected default_inference_geo to be 'global'."
  }

  assert {
    condition     = output.allowed_inference_geos == tolist(["unrestricted"])
    error_message = "Expected allowed_inference_geos to be ['unrestricted']."
  }
}
