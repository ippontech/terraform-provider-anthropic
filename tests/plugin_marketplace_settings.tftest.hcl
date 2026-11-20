test {
  parallel = true
}

# The Plugins API is available to Claude Enterprise organizations only, and the
# test organization is a Claude Console one, so this test only plans the public
# example against a dummy admin credential.
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "plugin_marketplace_settings_plan" {
  command = plan

  module {
    source = "../examples/resources/plugin_marketplace_settings"
  }

  assert {
    condition     = anthropic_plugin_marketplace_settings.engineering_tools.marketplace_id == "marketplace_01VbNcMxZaSdFgHjKlQwErTy"
    error_message = "Expected marketplace_id to be the example's marketplace."
  }

  assert {
    condition     = anthropic_plugin_marketplace_settings.engineering_tools.default_installation_preference == "available"
    error_message = "Expected the default installation setting to be 'available'."
  }
}
