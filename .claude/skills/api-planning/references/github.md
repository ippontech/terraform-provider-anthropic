# GitHub variant

Read at step 4 of `SKILL.md`, after the user approved the plan. The work is tracked as GitHub issues only, one branch per issue.

## Create the issues and branches

For each planned issue whose GitHub issue does not exist yet, in one parallel message:

1. Create the issue: the GitHub MCP tool `mcp__github__issue_write` (method `create`) when it is available, otherwise:

   ```bash
   body=$(mktemp)
   cat > "$body" <<'ENDBODY'
   <issue body from references/issue-template.md>
   ENDBODY
   gh issue create --repo ippontech/terraform-provider-anthropic --title "<title>" --body-file "$body"
   rm -f "$body"
   ```

2. Create the branch from `main`: the GitHub MCP tool `mcp__github__create_branch` when it is available, otherwise:

   ```bash
   sha=$(gh api repos/ippontech/terraform-provider-anthropic/git/ref/heads/main --jq .object.sha)
   gh api repos/ippontech/terraform-provider-anthropic/git/refs -f ref="refs/heads/<branch>" -f sha="$sha"
   ```

   Creating a branch needs write access to the repository. Without it, skip this step: you create the branch at the implementing agent's commit (see below), and `ship` pushes it to `origin`, which must then be the contributor's fork, its `main` in sync with upstream, since the agent starts from `origin/main`.

GitHub opens a PR only once a branch has a commit, so tell the user: "Branches are created; each PR opens when its first commit is pushed (`/ship`)."

## Start the implementation

- One issue at a time: implement it in the main session with the `terraform-provider` skill.
- Several independent issues: launch one `provider-implementer` agent per issue in one message, passing the issue body and the branch name as the spec. Each agent works in its own worktree and stops at a local commit.

An agent never checks out the issue's branch, so its kept worktree does not hold it: it commits on its worktree's own branch and names that branch and the commit in its report. When the agents report back, create or fast-forward each issue's branch to the reported commit (`git fetch . <commit>:<branch>`, or `git merge --ff-only <commit>` where that branch is checked out). If a fast-forward is refused, do not force it: stop and ask the user, because the issue's branch or its base has moved. Then run the `provider-reviewer` agent on each branch, fix what it finds, and ask the user before pushing; `/ship` then pushes each branch and opens its PR.

## Example

Plan approved for `anthropic_environment` (resource) and `anthropic_environments` (data source): create two issues and the branches `feat/environment-resource` and `feat/environments-data-source` in one message, report the table, then launch two `provider-implementer` agents in parallel with their issue bodies.
