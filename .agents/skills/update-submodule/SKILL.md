---
name: update-submodule
description: Updates the upstream Kueue submodule with make submodule-workflow, verifies the changes, and prepares a commit and pull request summary with the new SHA and included commits. Use when asked to update or bump the Kueue submodule.
user-invocable: true
---

# Update Submodule

Update the submodule and create a PR with a descriptive summary of the changes.

## Process

### 1. Update the submodule

Update submodule by running `make submodule-workflow`.

### 2. Verify and regenerate carry patches

After updating the submodule, verify that all carry patches in `upstream/kueue/patch/`
still apply cleanly to the updated upstream code:

```bash
cd upstream/kueue/src
for p in ../patch/*.patch; do
  echo "Checking $(basename "$p")..."
  git apply --check "$p"
done
```

Note: check each patch individually so all failures are reported, not just the first.

If any patch fails to apply (context conflicts due to upstream code changes), regenerate
it. Carry patches contain OCP-specific modifications to upstream e2e test files (e.g.,
adding managed labels, commenting out unavailable operators, adjusting cert names). To
regenerate a failing patch:

1. Read the failing patch to understand its intended OCP modifications.
2. Read the corresponding upstream files in `upstream/kueue/src/` to see the new code.
3. Apply the same OCP modifications to the new upstream code.
4. Generate a fresh unified diff using `git diff` from the submodule directory.
5. Replace the old patch file and verify with `git apply --check`.

This is a common occurrence because upstream refactors (renaming packages, moving utility
functions, reformatting code) change the context lines that patches depend on, even when
the lines being modified are unchanged.

### 3. Commit and push

Verify the changes, then commit and push a PR with a description that includes the
updated submodule commit SHA and a commit list summarizing the changes. If carry patches
were regenerated, note that in the commit message.

