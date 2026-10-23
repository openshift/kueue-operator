---
name: offboard-kueue-release
description: Retire an out-of-support kueue-operator release from Prow and Konflux GitOps, verify automatic .tekton cleanup, and provide a user-run live cleanup checklist. Use when asked to drop, remove, retire, or EOL a release.
user-invocable: true
---

# Off-board a kueue-operator release

For release `X.Y`, use branch name `release-X.Y` and Konflux suffix `X-Y`. Prepare one change per affected repo. **Never delete local or remote Git branches during release offboarding**, including release and Konflux bot branches; branch deletion is not part of this workflow.

## 1. Establish eligibility and agree on the plan

- Use the current **Red Hat Kueue Operator lifecycle/support statement**, not README tables, CI jobs, or branch age. Ask for its URL if unavailable. Record the URL, retrieval date, relevant policy text, and target's end-of-support date, including extensions.
- Order operator streams numerically by major/minor (`1.10 > 1.9`), cross-checking branches in `https://github.com/openshift/kueue-operator.git`. Exclude legacy OCP-named branches such as `release-4.19`.
- Refuse `main`, the newest operator stream, and any stream not demonstrably out of support. Warn for the second-newest. Stop on unreadable policy or incomplete/ambiguous ordering; approval cannot bypass eligibility.
- Ask the user for the local checkout paths of `openshift/release` and `konflux-release-data` before accessing either repo. Do not assume personal locations or search for these checkouts automatically. Confirm clean checkouts of this repo and both user-provided paths. Fetch and branch from each `origin/main`, using a name such as `remove-kueue-X-Y`.
- Inventory the changes below, then get approval of the evidence and plan **before local edits**. Release-plan retention/removal and RPA removal are separate decisions.

After editing, show the diff and get separate explicit confirmation before pushing or opening PRs/MRs. Plan approval does not authorize publication or live changes. Live Konflux cleanup is user-run only.

## 2. Remove release-specific configuration

Search for both `release-X.Y` and Konflux names ending in `-X-Y`, matching version boundaries so `1.2` does not match `1.20`. Review every hit; preserve unrelated releases and shared resources.

### Prow — `openshift/release`

- Delete target-release configs and all `__*` variants under `ci-operator/config/{openshift,openshift-priv}/kueue-operator/`.
- Delete target-release generated jobs under `ci-operator/jobs/{openshift,openshift-priv}/kueue-operator/` (presubmits, periodics, and any other matching jobs).
- Check `core-services/prow/02_config/` and `clusters/app.ci/publicize` for literal target-branch references. Leave repo-wide plugin, prow, Jira, LGTM, publicize, and private whitelist entries alone unless they explicitly name the branch.
- Do not hand-prune `core-services/sanitize-prow-jobs/_config.yaml`: dispatcher assignments are automatic ([background](https://github.com/openshift/release/pull/87395)). Inspect and report any generated changes there.
- Run `make jobs` (or `make ci-operator-config jobs`); check for residual target references in the config/job directories. Explain unavailable verification and review unexpected generated changes.

Commit: `Remove kueue-operator release-X.Y from prow (out of support)`.

### Konflux GitOps — `konflux-release-data`

Tenant root: `tenants-config/cluster/stone-prd-rh01/tenants/kueue-operator-tenant/`.

- Remove production and release-specific dev-bundle ProjectDevelopmentStreams from `kueue/_base/projectl.konflux.dev/projectdevelopmentstreams/` and their entries in `kueue/_base/kustomization.yaml`. Discover by metadata and branch/template values: dev directories may use dots (`kueue-operator-dev-1.4`) rather than hyphens. Keep the shared `kueue-operator-dev-template`.
- Inventory standalone `kueue-operator-X-Y-{stage,prod}-release-plan.yaml` files. Remove only with approval, including kustomization entries and generated copies. Otherwise record exact retained paths/references; a retained plan referencing an application blocks that application's live deletion.
- List stage/prod ReleasePlanAdmission (RPA) paths under `config/`. Keep them unless the user separately approves removal.
- Regenerate corresponding files under `tenants-config/auto-generated/`. Read the scripts, then use `tenants-config/build-manifests.sh` or tenant-scoped `build-single.sh` (`get-kustomize.sh` if needed). If regeneration cannot run, remove only the corresponding generated files manually and disclose it.
- Check `constraints/product/kueue-operator.yaml` and `prodsec/kueue-operator.yaml`; report per-release entries without changing them.
- Search tenant source/generated manifests for remaining target references; allow only explicitly retained release-plan paths/references. Separately verify RPA diffs match the decision. Missing GitOps streams do **not** prove live streams are absent.

Commit: `remove kueue-operator-X-Y`. This repo uses GitLab; provide the branch name and let the user open the MR.

### Operator — `openshift/kueue-operator`

- Treat `.tekton` cleanup as verification, not a required manual deletion commit. Konflux opens purge PRs to remove component pipelines after live component deletion; this happened for production and dev-bundle pipelines on `release-1.3` ([history](https://github.com/openshift/kueue-operator/commits/release-1.3/.tekton)). Target-release pipelines may never have existed on `main`.
- Before live cleanup, inspect target production/dev-bundle pipelines on `release-X.Y` and `main` where present. Record their `appstudio.openshift.io/application` and `appstudio.openshift.io/component` labels. If already purged, recover labels from the last pre-purge revision or inspect live component/application metadata in the user-run checklist; do not infer ownership from filenames. Preserve `main` dev-bundle pipelines and unrelated releases.
- After live cleanup, verify Konflux purge PRs have merged and removed only the target pipelines from the branches where they existed. Report missing automatic cleanup for investigation rather than creating a duplicate manual deletion PR.
- Update the README Releases row only if it exists. Check `Makefile` and `.github` for explicit target references. There is no release `baseBranches` list in the current `renovate.json`; no Renovate edit is required.

Create an operator change only for actual remaining references, with a commit describing those edits; otherwise report a no-op.

## 3. User-run live Konflux cleanup

Provide this checklist with actual names substituted. Run **only after Prow, Konflux GitOps, and any manual operator changes have merged and GitOps has synced**. Automatic Konflux pipeline purge PRs follow live component deletion; do not wait for them before starting this checklist. The agent must not execute it.

1. Confirm the Konflux cluster (`oc whoami --show-server`) and namespace `kueue-operator-tenant`. Locate production application `kueue-operator-X-Y` and dev application `kueue-operator-dev-X-Y`, checking recorded pipeline labels.
2. Wait for in-flight builds/releases and resolve remaining dependencies, including integration scenarios and retained release plans. Do not delete an application referenced by a retained plan.
3. List live streams, even if no GitOps source existed:
   ```bash
   oc -n kueue-operator-tenant get projectdevelopmentstreams.projctl.konflux.dev
   ```
   Inspect target streams with `get <name> -o yaml`, confirming names and branch/template values. With explicit approval, delete only confirmed target production/dev streams and wait for deletion. Skip only confirmed absence—not permission, discovery, or connection errors. Never delete shared projects/templates. Recheck applications/components after stream deletion; owned resources may already be gone.
4. Delete remaining target components, then applications. Expected production components: `kueue-operator-X-Y`, `kueue-bundle-X-Y`, `kueue-must-gather-X-Y`, and, where present, `kueue-operand-X-Y`. Also remove `kueue-bundle-dev-X-Y` from whichever target application owns it. Use recorded labels, not naming assumptions. Preserve `kueue-bundle-dev-main` and other releases.
5. Review and merge Konflux pipeline purge PRs for the deleted production/dev-bundle components, then verify `.tekton` cleanup as described above. Close only obsolete dependency-update PRs (for example, periodic MintMaker updates) for those components, not the purge PRs. After the next controller/GitOps sync, confirm streams, applications, and components stay absent. If anything returns, identify its reconciler/source before retrying.

Never touch `art-kueue-tenant`, `kueue-workloads-tenant`, or shared `file-based-catalog-v*` applications.

## 4. Report

Report each repo's branch, deleted/edited file counts and notable paths, verification results/limitations, and open decisions or dependencies. Merge order: Prow and Konflux GitOps first, any manual operator reference updates next, then user-run live cleanup followed by automatic Konflux pipeline purge PRs. Include the filled-in checklist above.

Leave all Git branches intact, including `openshift/release-X.Y`, `konflux/references/release-X.Y`, and `konflux/mintmaker/release-X.Y/*`. Do not propose branch deletion as an offboarding step or follow-up.
