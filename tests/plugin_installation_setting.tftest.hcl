test {
  parallel = true
}

# The Plugins API is available to Claude Enterprise organizations only, and the
# test organization is a Claude Console one, so this test only plans the public
# example against a dummy admin credential.
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "plugin_installation_setting_plan" {
  command = plan

  module {
    source = "../examples/resources/plugin_installation_setting"
  }

  assert {
    condition     = anthropic_plugin_installation_setting.pilot.target == "rbac_group_01F3xQqQzXyWvUtSrQpOnMlK"
    error_message = "Expected the pilot setting to target the RBAC group."
  }

  assert {
    condition     = anthropic_plugin_installation_setting.pilot.installation_preference == "auto_install"
    error_message = "Expected the pilot group to get auto_install."
  }

  assert {
    condition     = anthropic_plugin_installation_setting.organization.target == "organization"
    error_message = "Expected the second setting to target the whole organization."
  }
}
