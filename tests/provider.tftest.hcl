# Tests for the provider configuration example.
# Verifies that the provider initializes successfully with valid credentials.

test {
  parallel = true
}

run "provider_initializes_successfully" {
  module {
    source = "./examples/provider"
  }
}
