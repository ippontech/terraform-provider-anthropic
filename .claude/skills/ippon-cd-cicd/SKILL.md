---
name: ippon-cd-cicd
description: Maintains CICD pipeline templates. Use when creating or updating GitLab/GitHub Actions CICD pipelines/workflows/jobs.
model: sonnet
---

# Versions of dependencies

- Always pin versions of dependencies with a specific tag and SHA such as:
  - Docker images
  - GitLab includes blocks such as templates or components
  - GitHub Actions actions or reusable workflows
  - any other dependency/tools needed

# GitHub actions

Refer to the [GitHub Actions workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax) guide if need be.

- Always set up a permission block on each job with least privilege rights
- Always set up a `permissions: {}` at the workflow level to disable permissions for all the available permissions
