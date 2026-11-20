test {
  parallel = true
}

# The Plugins API is available to Claude Enterprise organizations only, and the
# test organization is a Claude Console one, so this test only plans the public
# example against a dummy admin credential: nothing is uploaded.
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "plugin_version_plan" {
  command = plan

  module {
    source = "../examples/resources/plugin_version"
  }

  # The content hash is computed from the local files during the plan, which
  # is what lets an edit to them force a new version.
  assert {
    condition     = can(regex("^[0-9a-f]{64}$", anthropic_plugin_version.example.source_hash))
    error_message = "Expected source_hash to be the SHA-256 of the example plugin, known at plan time."
  }

  assert {
    condition     = anthropic_plugin_version.example.release_notes == "Adds the account research skill."
    error_message = "Expected release_notes to be 'Adds the account research skill.'."
  }
}
