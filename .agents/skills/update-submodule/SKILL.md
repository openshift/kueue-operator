---
name: update-submodule
description: Updates the upstream Kueue submodule with make submodule-workflow, verifies the changes, and prepares a commit and pull request summary with the new SHA and included commits. Use when asked to update or bump the Kueue submodule.
user-invocable: true
---

# Update Submodule

Update the submodule and create a PR with a descriptive summary of the changes.

## Process

Update submodule by running `make submodule-workflow`.

Verify the changes, then commit and push a PR with a description that includes the updated submodule commit SHA and a commit list summarizing the changes.

