terraform {
  required_version = ">= 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/MemoryStore/anthropic"
      version = "~> 1.0"
    }
  }
}

provider "anthropic" {}
