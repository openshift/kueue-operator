---
name: lgtm-konflux-prs
description: Posts /lgtm comments on all open pull requests authored by the Konflux bot in openshift/kueue-operator, skipping PRs already labeled lgtm. Use when asked to LGTM or add /lgtm to Konflux PRs.
compatibility: Requires Bash and an authenticated GitHub CLI (gh) with permission to comment on the target repository.
---

# LGTM Konflux PRs

Add a comment containing exactly `/lgtm` to open Konflux pull requests.

## Scope

- Default to `openshift/kueue-operator`; use a different repository only if the user explicitly requests it. Always pass the repository explicitly rather than relying on the checkout's fork or default remote.
- Match the exact author identity: `red-hat-konflux[bot]` in the REST API (`app/red-hat-konflux` in `gh pr list`). Do not match titles, branches, or arbitrary bot accounts.
- Include all target branches. Do not filter by CI status unless the user asks.
- Only post `/lgtm`. Do not post `/approve`, submit GitHub approval reviews, merge PRs, or change repository files.
- Run the posting step only when the user asks to perform the action. Creating or editing this skill does not authorize posting comments.

## Process

### 1. Verify authentication and list candidates

Run the following in Bash, setting `repo` to the explicitly requested repository when applicable:

```bash
set -euo pipefail
repo='openshift/kueue-operator'
gh auth status --hostname github.com

gh api --hostname github.com --paginate "repos/$repo/pulls?state=open&per_page=100" \
  --jq '.[] | select(.user.login == "red-hat-konflux[bot]") | [.number, .title, .html_url, (if any(.labels[]; .name == "lgtm") then "already lgtm" else "needs /lgtm" end)] | @tsv'
```

Pagination is required so the operation covers every open PR, not just the first page. If there are no matches, report that and stop. Briefly announce the target repository and candidate count before posting; an explicit user request to add `/lgtm` is sufficient authorization without a second confirmation.

### 2. Post comments

Fetch the matching PR numbers, then recheck each PR's current state, author, and labels immediately before commenting. Skip closed/merged PRs and PRs already carrying the `lgtm` label.

```bash
set -euo pipefail
repo='openshift/kueue-operator'
prs=$(gh api --hostname github.com --paginate "repos/$repo/pulls?state=open&per_page=100" \
  --jq '.[] | select(.user.login == "red-hat-konflux[bot]") | .number')

for pr in $prs; do
  eligible=$(gh api --hostname github.com "repos/$repo/pulls/$pr" \
    --jq '.state == "open" and .user.login == "red-hat-konflux[bot]" and (any(.labels[]; .name == "lgtm") | not)')
  if [[ "$eligible" != 'true' ]]; then
    printf 'Skipped #%s (no longer eligible or already labeled lgtm)\n' "$pr"
    continue
  fi
  gh pr comment "$pr" --repo "https://github.com/$repo" --body '/lgtm'
done
```

Keep the same repository in both steps. Record the returned comment URLs as evidence of successful posts. Stop on errors and report partial progress rather than claiming all PRs were handled. Do not blindly retry a comment request with an uncertain outcome: check the PR's recent comments first to avoid duplicate posts.

The `lgtm` label can be applied asynchronously by Prow. A successful comment does not guarantee that Prow has accepted it or that the PR can merge. If rerunning immediately after a previous attempt, inspect recent comments before reposting to avoid duplicates while label processing is pending.

### 3. Summarize

Report the number of comments successfully posted, the repository, and any skipped PRs or failures. If all matching PRs were already labeled `lgtm`, say that no new comments were needed. Do not claim to have reviewed the diffs or verified CI merely because the comments were posted.
