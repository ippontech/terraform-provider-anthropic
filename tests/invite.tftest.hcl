test {
  parallel = true
}

# Admin API resource: creating a real invite sends an actual email and there is
# no dedicated test organization yet (#58), so this test only plans the public
# example against a dummy admin credential.
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "invite_plan" {
  command = plan

  module {
    source = "../examples/resources/invite"
  }

  assert {
    condition     = anthropic_invite.example.email == "new-teammate@example.com"
    error_message = "Expected invite email to be 'new-teammate@example.com'."
  }

  assert {
    condition     = anthropic_invite.example.role == "developer"
    error_message = "Expected invite role to be 'developer'."
  }
}
