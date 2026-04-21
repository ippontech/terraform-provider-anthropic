---
name: cicd
description: Maintains CICD pipeline templates, workflows or pipelines. Use when working with GitLab/GitHub Actions CICD tools.
model: sonnet
---

# Versions of dependencies

- Always pin versions of dependencies with a specific tag and SHA such as:
  - Docker images
  - GitLab includes blocks such as templates or components
  - GitHub Actions actions or reusable workflows
  - NPM dependencies
  - any other dependency/tools

# GitHub Actions

For GitHub Actions workflows, apply the `github-actions` skill.
