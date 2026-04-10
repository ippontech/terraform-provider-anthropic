---
name: ippon-cd-security
description: Security best practices for Claude Code configuration and CI/CD pipelines. Use when reviewing or modifying Claude settings, permissions, secrets handling, or security-sensitive configuration.
model: sonnet
---

# Claude Code permissions

The `.claude/settings.local.json` file controls which tools Claude Code can run without interactive approval. Over-broad permissions are a security risk: they allow Claude to make outbound network requests, exfiltrate data, or download and execute arbitrary scripts without user confirmation.

## Rules

- **Never pre-authorize broad network access.** Do not add `Bash(curl:*)`, `Bash(wget:*)`, or `WebFetch(domain:*)` entries. These allow outbound requests to arbitrary URLs and should always require interactive approval.
- **Do not pre-authorize raw content hosts.** Entries like `WebFetch(domain:raw.githubusercontent.com)` allow fetching arbitrary code from public repositories. Approve such fetches interactively and on a case-by-case basis.
- **Apply least privilege.** Only pre-authorize tools and domains that are genuinely needed for the project's routine operations (e.g., `Bash(go test:*)` for a Go project). If in doubt, leave it out and let the user approve interactively.
- **Review existing permissions before adding new ones.** When asked to add a permission, check whether it is overly broad and suggest a narrower alternative if so.
