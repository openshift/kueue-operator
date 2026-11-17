---
name: validate-art-build
description: Validate a Kueue ART build on a live OpenShift cluster - stage IDMS, ART FBC CatalogSource, OLM Subscription, metadata and image verification, operator e2e subset, must-gather, teardown. Use when checking whether an art-fbc build installs and works, reproducing the ART e2e jobs locally, or validating a build before/after a change.
---

# Validate an ART build

| Mode | Question it answers | Status |
| --- | --- | --- |
| **A - fresh-install validation** | Does this ART build install, render the right config, and behave? | implemented (`test-art-build.sh`) |
| **B - z-stream upgrade validation** | Does upgrading X → Y through the ART FBC preserve state and behavior? | runbook below, scripts pending |

Mode A is the default developer loop. Mode B is the QE release runbook.

## Why this exists

The ART e2e consumption lives in CI
([openshift/release #88692](https://github.com/openshift/release/pull/88692)) and runs the
whole suite nightly. Locally the question is narrower: **does this ART artifact install and
behave**, i.e. does the FBC resolve, does the bundle install, does the CSV point at the
right digests, does the operator render the CR, do webhooks and metrics work. Scheduling
semantics are upstream kueue behavior and are covered by the ART periodics on the same
build.

## Prerequisites

* A real OCP cluster with `kube:admin` (no kind/libvirt: no OLM, no IDMS).
* `oc`, `jq`, `skopeo` (`podman` only if stage credentials must be added).
* `registry.stage.redhat.io` credentials in the cluster pull secret - detected
  automatically, no action when present.
* **Red Hat cert-manager operator** (package `openshift-cert-manager-operator` from
  `redhat-operators`). This is the only operator prerequisite. The community
  `cert-manager` operator is not equivalent and the skill refuses to run against it;
  `--with-cert-manager` installs the RH one. Without it, `install` stops immediately
  unless `--e2e none` was passed, because the operand never comes up (`CertManagerMissing`).
* JobSet, LeaderWorkerSet and DRA are **not** part of this setup: the specs that need them
  are skipped (see below).

Every phase first checks that the cluster is reachable and fails immediately with the
kubeconfig context/server when it is not, instead of failing mid-run.

## Quick start

Nothing is version-specific: you give a Kueue version and an OCP version (or a tag),
and the skill derives the rest.

```bash
# no flags, interactive: asks only for the Kueue version (OCP comes from the cluster)
./test-art-build.sh

# scripted equivalent
./test-art-build.sh --kueue-version 1.4 --ocp-version 5.0
./test-art-build.sh --kueue-version 1.5            # OCP version defaults to the cluster's

# explicit tag or pinned digest if you want full control
./test-art-build.sh --fbc-tag kueue-1.4__v4.22__kueue-rhel9-operator
./test-art-build.sh --fbc-digest sha256:2cd0ce69...

# partial runs
./test-art-build.sh --kueue-version 1.5 --phase install
./test-art-build.sh --phase verify
./test-art-build.sh --kueue-version 1.5 --e2e none     # install + verify + must-gather only
./test-art-build.sh --kueue-version 1.5 --e2e full     # whole CI suite (~60 min)
./test-art-build.sh --phase teardown
```

Interactive flow (`./test-art-build.sh` with no build flags):

```
Kueue version (e.g. 1.4, 1.5): 1.4

  build     : quay.io/redhat-user-workloads/ocp-art-tenant/art-fbc:kueue-1.4__v5.0__kueue-rhel9-operator
  channel   : stable-v1.4
  z-stream  : 1.4.4
  cluster   : https://api.<cluster>:6443 (5.0.0-0.nightly-...)

Proceed? [Y/n]
```

Accepts `1.4`, `1.4.6` or `v1.4`; the tag only carries the stream, so the z-stream is
predicted from ART's build metadata, confirmed against the catalog's channel head before
the Subscription is created, and asserted from the installed CSV afterwards. The tag is checked against `art-fbc`
before anything is created; a wrong OCP version lists the tags that do exist for that
stream.

Artifacts land in `--evidence-dir` (default `./art-evidence/<timestamp>`, reused by every
later phase of the same install):
`tested-images.json`, `e2e-operator-junit.xml`, `must-gather/`. Phase state is kept in
`$TMPDIR/validate-art-build/<cluster-domain>/env`, so phases can run separately.

## What is derived vs given

| Value | Source |
| --- | --- |
| stream (`1.4`, `1.5`) | `--kueue-version`, or the prompt, or the tag |
| OCP version | the cluster you are logged into; `--ocp-version` overrides, and a tag's `__v<ocp>` is cross-checked against it |
| channel | `stable-v<stream>`; `--channel` overrides; otherwise the package default |
| z-stream | predicted from ART's per-OCP NVR tags (shown at the prompt), then read authoritatively from the catalog channel head just before the Subscription, and finally asserted from the installed CSV |
| freshness limit | `--max-age-days` (default 14) |
| test code | the checkout, or automatically `release-<stream>` in a worktree when the branch doesn't match the build (`--test-ref` overrides, `--no-auto-test-ref` disables) |

Nothing in the code names a Kueue release, so a `1.4.6` validation is the same command
shape as a `1.5.1` one.

## Phases

| Phase | Does | Time |
| --- | --- | --- |
| `install` | optional cert-manager → stage-creds check → IDMS + MCP gate → tag→digest → CatalogSource → Subscription → CSV `Succeeded` | ~5 min (MCP rollout dominates) |
| `verify` | images, catalog graph, CSV metadata, relatedImages, image wiring, freshness → `tested-images.json` | seconds |
| `e2e` | `--e2e operator` (default): 18 specs from `e2e_operator_test.go` (17 pass, 1 skips); `--e2e none`; `--e2e full`: whole operator suite with the CI label filter. Cleans the suite's leftovers first. | **4m44s–5m51s measured** / 0 / ~60 min |
| `must-gather` | `oc adm must-gather` with the CSV-pinned image | ~1 min |
| `teardown` | Subscription, CSV, CatalogSource, IDMS, Kueue CR; waits for the rollout. `--teardown-cert-manager` also removes cert-manager, but only if this skill installed it | ~5 min |

Default `--phase all` runs install → verify → e2e (`operator`) → must-gather.

## What `verify` checks

1. **Catalog graph** - the channel derived from the installed CSV's version exists, is the
   default, and its head is that CSV; `replaces` chain; multi-arch labels
   (amd64/arm64/ppc64le/s390x).
2. **CSV metadata** - displayName, `features.operators.openshift.io/*`,
   `operatorframework.io/properties`, `operators.openshift.io/valid-subscription`,
   install modes.
3. **relatedImages** - operator, operand and must-gather present, all under
   `registry.redhat.io/kueue/` (the bundle is the CSV itself, not a related image).
4. **Image wiring** - the running deployments use exactly the CSV digests (no dev override).
   The operand only exists once a Kueue CR does, so on a fresh install this is deferred and
   re-checked right after the suite creates the CR (result in `post-e2e-wiring.log`).
5. **Freshness** - warns when the floating tag has not been rebuilt in `--max-age-days`
   (default 14) days.

`--e2e operator` calls `bin/ginkgo` directly (explicit focus-file/filter, no store
dependency); `--e2e full` goes through the repo's `make e2e-ci-test` with
`GINKGO_EXTRA_ARGS="--skip=…"`, so the CI filter and timeout can never drift from what CI
runs. That passthrough is the only repo change the skill requires.

## What the default e2e covers

`--focus-file=e2e_operator_test.go --label-filter='!disruptive && !flaky' --skip='(?i)jobset|leaderworker'`
→ 18 specs: operator and kueue pods ready, operator status/Degraded, priority classes, no
v1alpha CRDs, webhook readiness and reinvocation, deny-all network policy, rendered
`kueue-config-map`, DRA `deviceClassMappings` (with and without DRA APIs), namespace opt-in
for jobs/pods/deployments/statefulsets, and the TLS metrics endpoint.

Disruptive specs (which delete the Kueue CR) are excluded by design.

## Where the digests come from

Everything tested is ART output, at two levels:

1. the **catalog index**: `quay.io/redhat-user-workloads/ocp-art-tenant/art-fbc:<tag>`
   resolved to `@sha256:…` by the script;
2. the **images** the FBC points at: `registry.redhat.io/kueue/...@sha256:…`, taken from
   the installed CSV's `relatedImages`. Pre-release digests are not on
   `registry.redhat.io` yet, so the IDMS redirects pulls to `registry.stage.redhat.io`.

Nothing resolves through Konflux in this path.

## Expected numbers (so a short run isn't mistaken for a broken one)

| Suite / branch | Specs |
| --- | --- |
| `e2e_operator_test.go` subset (this skill's default) | 18 selected; 17 pass and 1 skips on clusters where DRA APIs are available |
| full CI suite, `release-1.5` / `main` | ~93 executed of 151 declared |
| full CI suite, `release-1.4` | ~62 fresh cluster, ~80 upgraded |
| upstream operand suite (`e2e-upstream-test`, not used here) | 69 |

Skipped on purpose: 13 JobSet/LeaderWorkerSet specs (of which 9 fail without those
operators), all `dra*` specs, and everything labelled `disruptive`.

## Gotchas

* **Stage credentials must be in the pull secret before the CatalogSource pod starts**,
  otherwise bundle unpack fails with `manifest unknown`.
* **Pull-secret changes and IDMS changes both trigger MCO rollouts**, and a pool can still
  report `Updated=True` from an earlier change - `install.sh` (`mcp_wait_rollout`) waits for the rollout to
  actually complete (the gating `merge-stage-registry-credentials` uses).
* **The must-gather `relatedImages` key changed in 1.5.1** (`must-gather` →
  `kueue-must-gather-rhel9`): always match by image reference.
* **The FBC references two namespaces**; only `registry.redhat.io/kueue` is mirrored.
* **Test code and build must be the same release line.** Expectations change between
  releases - e.g. 1.4.4 leaves `reinvocationPolicy: Never` on the `mjob.kb.io` webhook while
  1.5+ sets `IfNeeded`, so `main`'s tests against a 1.4 build fail deterministically (two
  attempts, no flake). The e2e phase warns when the installed stream differs from the
  checkout's branch, and `--test-ref release-1.4` runs the suite from that branch in a
  worktree.
* **The suite is not idempotent against its own leftovers.** An interrupted run leaves
  `kueue-managed-test`, `kueue-unmanaged-test`, `ClusterQueue test-clusterqueue` and
  `ResourceFlavor default` behind; the next run's `BeforeAll` then fails with
  `AlreadyExists`, and `--flake-attempts` cannot recover from that. The e2e phase deletes
  them (and waits for the namespaces to actually terminate) before starting.
* **A stale kubeconfig context breaks individual specs** (they may build their own client),
  so the e2e phase refuses to run if the current API server differs from the one recorded
  at install.
* **`run-bundle` installs are not OLM installs** - remove a run-bundle subscription,
  CSV, its catalog and any Konflux dev IDMS before validating the ART path.
* **`e2e-ci-test` needs `ARTIFACT_DIR`** or its JUnit path becomes `/e2e-junit.xml`; the
  skill exports it.

## Mode B - z-stream upgrade validation (runbook)

Not scripted yet; tracked in OCPKUEUE-967.

1. Channel-head check: the FBC carries both `kueue-operator.vX.Y.Z` entries and the target
   `replaces`/`skips` the source.
2. Baseline: install the source version from the ART FBC, create the workloads/resources
   that must survive, capture pre-upgrade evidence.
3. Upgrade: `installPlanApproval: Manual` (stage-only bundles are not auto-approved),
   approve the InstallPlan, wait for the CSV replacement.
4. Verify: resources persist, API version unchanged, AFS/state intact, post-upgrade probes.
5. Lanes: unit, operator e2e on the upgraded cluster.
6. Record results in the QE ticket template.

## Files

Flat layout, matching `kueue-art-reference` (no `lib/`; each script is sourced by the
entry point, and OLM manifests are heredocs inside the script that applies them):

```
test-art-build.sh        entry point: CLI, version->tag/channel resolution, prompts, phases
common.sh                logging, phase state, CSV/related-image helpers
install.sh               cert-manager, stage IDMS + MCP gate, FBC catalog, Subscription
verify.sh                images, catalog graph, CSV metadata, wiring, freshness
e2e.sh                   suite leftovers cleanup, stream pairing, run
teardown.sh              CR-first cleanup, non-blocking deletes, finalizer fallback
setup-stage-credentials.sh  pull-secret merge (copied from kueue-art-reference)
```

`setup-stage-credentials.sh` is the only file borrowed from another skill, kept as a copy
because skills in this repo are self-contained; the shared `lib/` idea is tracked in
OCPKUEUE-967.

## Validated on

2026-11-16/17, OCP 5.0.0-0.nightly (cluster `anahas5016`),
`kueue-1.5__v5.0__kueue-rhel9-operator` (`art-fbc@sha256:8939d6de…`): CSV
`kueue-operator.v1.5.1` installed by OLM in 15 s, must-gather collected from
`kueue-must-gather-rhel9@sha256:e01face…` (the digest the CI rehearsal used). Default
`--e2e operator` run: 18 specs selected, 17 passed in **4m44s** (one skips when DRA APIs are present).
Same cluster, `--kueue-version 1.4 --test-ref release-1.4`: 1.4.4 installed, 17 selected,
16 passed in **6m26s**. Confirms the pairing rule: `main`'s suite against 1.4.4 fails the
webhook-reinvocation spec deterministically, `release-1.4`'s suite passes. Full CI suite on the same build:
91/93 passed, the 2 failures environmental (stale kubeconfig context; a flake), both green
on rerun. End-to-end default run on a fresh cluster: ~12 min (install ~5 min incl. MCP
rollout, verify seconds, e2e 5 min, must-gather ~1 min).
