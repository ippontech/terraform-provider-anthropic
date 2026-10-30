test {
  parallel = true
}

# The Plugins API is available to Claude Enterprise organizations only, and the
# test organization is a Claude Console one, so this test only plans the public
# example against a dummy admin credential: nothing is uploaded.
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "plugin_plan" {
  command = plan

  module {
    source = "../examples/resources/plugin"
  }

  assert {
    condition     = endswith(anthropic_plugin.example.source_dir, "/sales-toolkit")
    error_message = "Expected source_dir to point at the example plugin's directory."
  }

  assert {
    condition     = anthropic_plugin.example.release_notes == "First release"
    error_message = "Expected release_notes to be 'First release'."
  }

  assert {
    condition     = anthropic_plugin.example.source_archive == null
    error_message = "Expected source_archive to be unset."
  }
}
