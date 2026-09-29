test {
  parallel = true
}

# Applies for real (apply is the default command): deployments, like vaults,
# are free to manage and only billed at session runtime, and the agent and
# environment they bind to (anthropic_agent / anthropic_environment) already
# have working full-CRUD examples of their own. Torn down afterwards; destroy
# always archives (there is no hard-delete endpoint for deployments).
run "deployment_apply" {
  module { source = "../examples/resources/deployment" }

  assert {
    condition     = output.deployment_id != ""
    error_message = "Expected deployment_id output to be set."
  }

  assert {
    condition     = startswith(anthropic_deployment.minimal.id, "depl_")
    error_message = "Expected deployment id to start with \"depl_\"."
  }

  assert {
    condition     = anthropic_deployment.minimal.status == "active"
    error_message = "Expected a freshly created, unpaused deployment to have status == \"active\"."
  }

  assert {
    condition     = anthropic_deployment.scheduled.schedule.expression == "0 9 * * 1-5"
    error_message = "Expected scheduled deployment's schedule.expression to round-trip."
  }

  assert {
    condition     = anthropic_deployment.scheduled.metadata["team"] == "platform"
    error_message = "Expected scheduled deployment's metadata.team to round-trip."
  }
}
