# Test Plan for waitForPodsReady timeout configuration

**Plan Status:** Draft
**Feature:** [OCPSTRAT-3301](https://redhat.atlassian.net/browse/OCPSTRAT-3301)
**Testing Epic:** [OCPKUEUE-722](https://redhat.atlassian.net/browse/OCPKUEUE-722)
**Test Plan Story:** [OCPKUEUE-799](https://redhat.atlassian.net/browse/OCPKUEUE-799)

## Overview

- [References](#references) — KEPs, JIRA tickets, docs, and known bugs
- [Introduction](#introduction) — What the feature is and what changed
- [Test Strategy](#test-strategy) — Upstream vs downstream approach
- [Test Scope](#test-scope) — Upstream and downstream scenarios
- [Out of Scope](#out-of-scope) — What we're not testing and why
- [Target Environments](#target-environments) — OCP versions, architectures, FIPS, disconnected, Hypershift
- [Test Deliverables](#test-deliverables) — PRs and test reports we produce
- [Test Tasks](#test-tasks) — Work breakdown
- [Pass/Fail Criteria](#passfail-criteria) — Exit criteria
- [Risks](#risks) — Blockers and unknowns

## References

| Type | Link |
|------|------|
| Testing Epic | [OCPKUEUE-722](https://redhat.atlassian.net/browse/OCPKUEUE-722) — Testing waitForPodsReady Configuration in Kueue Operator |
| Test Plan Story | [OCPKUEUE-799](https://redhat.atlassian.net/browse/OCPKUEUE-799) — Create waitForPodsReady test plan |
| RFE | [RFE-9252 — Configure WaitForPodsReady Timeout](https://redhat.atlassian.net/browse/RFE-9252) |
| Implementation Story | [OCPKUEUE-700](https://redhat.atlassian.net/browse/OCPKUEUE-700) — Adapt GangScheduling API in the kueue-operator to include timeout field |
| Implementation PR | [#2140](https://github.com/openshift/kueue-operator/pull/2140) — Add waitForPodsReady timeout config |
| Related (future) | OCPKUEUE-701 — Per-workload timeout config (requires upstream KEP) |
| Upstream tests | `test/e2e/sequential/baseline/waitforpodsready_test.go`, `test/integration/singlecluster/scheduler/podsready/scheduler_test.go` |
| Upstream docs | [Setup All-or-nothing with ready Pods](https://kueue.sigs.k8s.io/docs/tasks/manage/setup_wait_for_pods_ready/) |
| Upstream API (v1beta2) | [WaitForPodsReady](https://kueue.sigs.k8s.io/docs/reference/kueue-config.v1beta2/#config-kueue-x-k8s-io-v1beta2-WaitForPodsReady) |
| Bugs | [#14811](https://github.com/kubernetes-sigs/kueue/issues/14811) — recovery eviction leaves quota unreleased; [#14574](https://github.com/kubernetes-sigs/kueue/issues/14574) — StatefulSet empty PodGroup bypasses PodsReadyTimeout eviction |

## Introduction

The `waitForPodsReady` feature provides gang-scheduling semantics: a workload is not treated as running until all of its pods reach the `Ready` state. This prevents partial scheduling, where some pods start and consume quota while their peers stay pending — wasting resources and risking deadlock. To enforce it, Kueue starts a timer when a workload is admitted; if the pods do not all become Ready in time, the workload is evicted and requeued with exponential backoff, freeing its quota.

Two independent timers govern the workload. `timeoutSeconds` (WaitForStart) governs reaching Ready for the first time after admission. `recoveryTimeoutSeconds` (WaitForRecovery) governs recovering after an already-running workload loses readiness (pod crash, node failure, OOM); when omitted it defaults to `timeoutSeconds`, and `0` disables it. A requeuing strategy (retry limit, backoff base/max, time reference) controls how evicted workloads are requeued.

This feature exposes these settings on the Kueue CR under `spec.config.gangScheduling.byWorkload`, with `policy: ByWorkload` as the prerequisite. The operator translates the CR fields into the Kueue controller ConfigMap (for example `retryLimit` → `backoffLimitCount`, `timeReference` → `timestamp`). This plan covers the new fields, their validation, their translation into controller configuration, and end-to-end verification that the resulting admission, eviction, and requeue behavior is correct.

## Test Strategy

- **Upstream:** Controller behavior (timeout enforcement, backoff calculation, requeue and queue-ordering logic) is already covered in `kubernetes-sigs/kueue` by e2e, integration, and unit tests running in upstream CI, so downstream relies on these rather than re-implementing timeout math. The upstream tests are not re-run downstream: they configure the feature by writing the controller ConfigMap directly, bypassing the operator's job (CR validation, reconciliation, ConfigMap generation) — the exact thing downstream must verify — and they use sub-second timeouts (for example `timeout: 10ms`) that downstream CEL validation rejects at its 30-second minimum.
- **Downstream:** Because the operator layer is untested upstream, downstream adds operator-specific coverage across three layers: CEL validation tests (API-level admission of the new CR fields via envtest), unit tests (CR → ConfigMap generation logic), and e2e tests (full path CR → operator reconciliation → ConfigMap → controller behavior, including quota release on eviction). The e2e tests re-verify behavior that upstream also checks, but through the operator-managed path and with CEL-legal values, so they confirm the configuration a user actually sets on the CR produces the expected runtime behavior.

## Test Scope

### Downstream Tests

#### CEL Validation Tests

API-level validation of the new CR fields via envtest. Location: `test/envtest/gangscheduling/kueue_gangscheduling_cel_test.go`.

| ID | Scenario | What It Validates | Status |
|----|----------|-------------------|--------|
| C1 | RequeuingStrategy requires at least one field | MinProperties=1 on RequeuingStrategy → an empty `requeuingStrategy{}` is rejected | Implemented (PR #2140) |
| C2 | backoffBaseSeconds must not exceed backoffMaxSeconds | Cross-field rule `backoffBaseSeconds <= backoffMaxSeconds` | Implemented (PR #2140) |

#### Unit Tests

CR → ConfigMap generation logic. Location: `pkg/configmap/configmap_test.go`.

| ID | Scenario | What It Validates | Status |
|----|----------|-------------------|--------|
| U1 | Sequential gang admission | Policy ByWorkload + Sequential → blockAdmission true, timeout 0s | Implemented (PR #2140) |
| U2 | Requeuing strategy with recovery timeout | Full field mapping including recoveryTimeout and all requeuing fields | Implemented (PR #2140) |
| U3 | Recovery timeout disabled | recoveryTimeoutSeconds 0 → recoveryTimeout 0s | Implemented (PR #2140) |
| U4 | Gang scheduling disabled (policy None) | Policy None → timeout 8760h (1 year), blockAdmission false | Implemented (PR #2140) |
| U5 | Gang scheduling defaults (policy ByWorkloadDefaults) | ByWorkloadDefaults → waitForPodsReady omitted entirely | Implemented (PR #2140) |
| U6 | Custom timeoutSeconds converts to duration | Positive timeoutSeconds → duration in ConfigMap | Proposed |
| U7 | Requeuing strategy with only retryLimit | Only backoffLimitCount emitted; other requeuing fields omitted | Proposed |
| U8 | Requeuing strategy with only timeReference | Only timestamp emitted; other requeuing fields omitted | Proposed |
| U9 | Requeuing strategy without retryLimit | backoffLimitCount omitted (infinite retries) | Proposed |
| U10 | Requeuing strategy with only backoffMaxSeconds | Only backoffMaxSeconds emitted; other requeuing fields omitted | Proposed |

#### E2E Tests

Full path CR → operator reconciliation → ConfigMap → controller behavior. Location: `test/e2e/e2e_waitforpodsready_test.go`. All proposed (to be automated).

| ID | Scenario | What It Validates | Status |
|----|----------|-------------------|--------|
| E1 | Gang timeout lifecycle: evict → release quota → requeue → succeed | A gang Job (parallelism 3) with 2/3 pods Ready and 1 gated is evicted as a whole at `timeoutSeconds` (all-or-nothing). Eviction emits `kueue_evicted_workloads_once_total{reason="PodsReadyTimeout"}` and returns ClusterQueue usage to 0. Once un-gated, the workload becomes Ready on requeue and stays active with `requeueState.count` below `retryLimit`. | Proposed |
| E2 | Readiness lost, recovery disabled → stays admitted, quota retained | With `recoveryTimeoutSeconds: 0`, a workload that reached Ready and then lost readiness triggers no recovery-timeout eviction for the duration of the observation window — it stays admitted and retains its quota throughout. | Proposed |
| E3 | Requeues exhaust retry limit → workload deactivated | An always-failing workload is requeued with exponential backoff (capped by `backoffMaxSeconds`) until `requeueState.count` reaches `retryLimit`, after which the workload is deactivated (`spec.active=false`). | Proposed |
| E4 | Timeout enforced across all workload types | The configured timeout is enforced for every supported workload type: Job, Pod, Deployment, StatefulSet, JobSet, and LeaderWorkerSet. | Proposed |
| E5 | Recovery eviction releases quota | A JobSet (`restartStrategy: Recreate`) reaches Ready, loses readiness, and is evicted at `recoveryTimeoutSeconds` (`underlyingCause=WaitForRecovery`); ClusterQueue usage returns to 0. Regression guard for [#14811](https://github.com/kubernetes-sigs/kueue/issues/14811). | Proposed (blocked on #14811) |

## Out of Scope

- Per-workload timeout configuration — tracked in OCPKUEUE-701, requires upstream KEP
- `blockAdmission` CR input — the operator exposes no user-facing `blockAdmission` field; the value is derived from the gang admission mode (Sequential → true, otherwise false) and its generated form is covered by U1, so there is no separate input to test
- Gang scheduling policy configuration — already covered by the existing operator e2e suite
- Timeout math and backoff calculation — verified by upstream integration/e2e tests

### Scenarios considered and excluded

| Scenario | Reason |
|----------|--------|
| Preemption during timeout window | Upstream controller behavior; high fixture cost; resolved manually (evicted with reason Preempted, start clock keyed to Admitted timestamp) |
| Cohort quota release on timeout eviction | Quota release already asserted downstream in E1; dedicated cohort borrowing case deferred |
| FairSharing usage accounting after eviction | Usage accounting owned by upstream; not operator-specific |
| Priority inversion during backoff | Upstream scheduling behavior; not operator-specific |
| Managed vs unmanaged namespace scoping | Covered by existing operator webhook/namespace-selector tests |
| Maximum timeout workaround (86400s) | Documented workaround; the timeoutSeconds→duration conversion is covered generically by U6, and the max bound is enforced by CEL — no special operator behavior at 86400 |
| Policy None disables end-to-end | Checkable portion covered by unit test U4 |
| timeoutSeconds boundary validation (CEL, e.g. reject 29) | Dropped; the CEL min/max bounds are enforced by the API and exercised implicitly, no dedicated downstream test |

## Target Environments

- Disconnected
- FIPS
- ARCH (x86_64, ARM)
- OCP versions: 4.18, 4.19, 4.20, 4.21, 4.22, 4.23, 5.0
- Hypershift — HCP

## Test Deliverables

- Downstream PRs with unit tests in `kueue-operator` (Prow CI)
- Downstream PRs with e2e tests in `kueue-operator` (Prow CI)
- Test report with information for the Docs team

## Test Tasks

- Test plan creation and review
- Implement downstream unit test suite
- Automate downstream e2e workflow
- Remediate testing defects
- Automate and validate recurring Prow CI executions
- Publish technical documentation and code examples

## Pass/Fail Criteria

- No critical or major defects remain open
- All implemented unit and e2e tests pass consistently on Prow
- The eviction path (E1) confirms quota is released; disabled recovery (E2) confirms quota is retained
- Supporting documentation material for the Docs team is created

## Risks

| Risk | Impact |
|------|--------|
| Upstream bug #14811 — recovery-timeout eviction leaves the quota reservation unreleased | A recovery-evicted workload may leak quota and block later admissions. Guarded downstream by **E5** (JobSet `restartStrategy: Recreate` recovery-eviction → CQ usage returns to 0), which asserts the fixed behavior. Since the bug is still open upstream, E5 is expected to fail on affected operands — keep it skipped/pending, gated on the operand carrying the fix, and enable it once available |
| Upstream bug #14574 — StatefulSet empty PodGroup bypasses PodsReadyTimeout eviction | E4 may fail for the StatefulSet case depending on the operand's Kueue version |
| Eviction reason is shared between start and recovery timeouts | Recovery evictions must be distinguished by `underlyingCause=WaitForRecovery`; this applies to E5 (operator-managed recovery-eviction path, with upstream covering the controller behavior) and to E1's start-eviction assertions |
