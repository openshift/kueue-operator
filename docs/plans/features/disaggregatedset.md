# Test Plan for the DisaggregatedSet integration

**Plan Status:** Draft
**Feature:** [OCPSTRAT-3982](https://redhat.atlassian.net/browse/OCPSTRAT-3982) — DisaggregatedSet integration
**Testing Epic:** [OCPKUEUE-968](https://redhat.atlassian.net/browse/OCPKUEUE-968)
**Test Plan Story:** [OCPKUEUE-989](https://redhat.atlassian.net/browse/OCPKUEUE-989)

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
| Feature | [OCPSTRAT-3982](https://redhat.atlassian.net/browse/OCPSTRAT-3982) — DisaggregatedSet integration |
| Testing Epic | [OCPKUEUE-968](https://redhat.atlassian.net/browse/OCPKUEUE-968) |
| Test Plan Story | [OCPKUEUE-989](https://redhat.atlassian.net/browse/OCPKUEUE-989) |
| KEP-11952 | [kubernetes-sigs/kueue#17500](https://github.com/kubernetes-sigs/kueue/pull/17500) — DisaggregatedSet integration |
| Upstream implementation | [kubernetes-sigs/kueue#17675](https://github.com/kubernetes-sigs/kueue/pull/17675), merge commit [`4f3748d8`](https://github.com/kubernetes-sigs/kueue/commit/4f3748d8f2c8b9fc68d131d6e23d44faf33f77c1) |
| Upstream tests | `pkg/controller/jobs/disaggregatedset/*_test.go`, `test/integration/singlecluster/controller/jobs/disaggregatedset/`, `test/integration/singlecluster/webhook/jobs/disaggregatedset_webhook_test.go`, `test/e2e/singlecluster/extended/disaggregatedset{,_lifecycle}_test.go`, `test/e2e/sequential/extended/disaggregatedset_test.go`, `test/e2e/singlecluster/tasapi/extended/disaggregatedset_test.go` |
| Upstream docs | [Run DisaggregatedSet](https://github.com/kubernetes-sigs/kueue/blob/4f3748d8f2c8b9fc68d131d6e23d44faf33f77c1/site/content/en/docs/tasks/run/disaggregatedset.md) |
| Downstream docs | [`docs/disaggregatedset/`](../../disaggregatedset/README.md) — enablement demo |
| Downstream precedent | SparkApplication opt-in integration ([openshift/kueue-operator@f5067fc0](https://github.com/openshift/kueue-operator/commit/f5067fc034d8e13bc14bf5c5b418179618217619)) |
| Bugs | None known. The upstream guide lists troubleshooting cases: `WorkloadRecreated`, `PodExceedsRoleRequests`, `PodNotAdopted`, scale-up through zero. |

## Introduction

A DisaggregatedSet, served by LeaderWorkerSet as `disaggregatedset.x-k8s.io/v1`, runs several roles of one model server, such as prefill and decode, as replicated slices. Each role becomes a child LeaderWorkerSet. Without the integration, Kueue admits each child LeaderWorkerSet on its own, so one role can run, or be preempted, without its siblings.

The upstream Kueue integration `disaggregatedset.x-k8s.io/disaggregatedset` creates one Workload per slice that holds the PodSets of every role. A role with a leader template and `size > 1` contributes two PodSets, `<role>-leader` and `<role>-worker`. Admission and preemption are therefore atomic per slice: Pods stay gated until their slice Workload is admitted, and preemption evicts a whole slice while the other slices keep running. Atomic admission does not mean that every Pod becomes Ready at the same time. The `DisaggregatedSetIntegration` feature gate is alpha and disabled by default; Kueue does not start when the framework is listed without the gate, and listing the framework enables the `pod` integration. MultiKueue is not supported and its behavior is not defined.

The kueue-operator exposes the integration as the `DisaggregatedSet` value of `spec.config.integrations.frameworks` in the `Kueue` CR. When it is selected, the operator renders the framework and the feature gate into the `kueue-manager-config` ConfigMap, deploys the DisaggregatedSet and Pod webhooks for managed namespaces, and applies the synced RBAC. When the `disaggregatedset.x-k8s.io/v1` API is not served, the operator reports `Degraded=True` with reason `MissingDependencies`; it keeps rendering the framework and the gate, because Kueue defers the controller setup until the API is served.

## Test Strategy

- **Upstream:** [kubernetes-sigs/kueue#17675](https://github.com/kubernetes-sigs/kueue/pull/17675) covers the controller, webhook, PodSet and Pod adoption logic with unit, integration and e2e tests. Downstream does not re-implement this logic. The upstream e2e suites (U5) are a group of scenarios that can be reused in the future through `make e2e-upstream-test`. Today the runner does not select them. A reused upstream case counts as coverage for a downstream scenario only when it makes an equivalent observable assertion, works with the operator-managed configuration, and its JUnit report shows it selected and run, not filtered.
- **Downstream:** Tests in the `kueue-operator` repo cover what upstream cannot, split by layer:
  - CEL validation (envtest) checks how the `Kueue` CR rejects invalid framework lists (C1).
  - Unit tests check the CR → ConfigMap rendering, webhook filtering and namespace selection, RBAC assets and the prerequisite check (T1–T6), and CI `test-generate` checks the generated CRD (T7). T8 (disable after enable) is planned.
  - Planned e2e tests check enablement, admission, preemption, quota release and disablement through the operator-managed deployment on OpenShift (E1–E16). E17 is a planned, non-gating characterization of disabling with live managed DisaggregatedSets. They are not implemented yet; until they run, DisaggregatedSet runtime behavior is not covered by downstream automated tests.
  - Manual tests check the missing-prerequisite recovery and the enablement guide (M1–M2).

  Behavioral scenarios stay planned downstream until an equivalent upstream case has run evidence.

## Test Scope

### Upstream Tests

Each row lists the upstream tests, whether `make e2e-upstream-test` (`upstream/kueue/e2e-test-ocp.sh`) selects them today, and how downstream uses them.

| ID | Upstream tests | What It Validates | Selected by `make e2e-upstream-test` | Downstream use |
|----|----------------|-------------------|--------------------------------------|----------------|
| U1 | Webhook integration tests | Validation of roles, rollout settings, Kueue labels and annotations, size limits, TAS rules, immutable fields | No (integration tests) | Reference; representative downstream cases planned in E9 |
| U2 | Controller integration tests | One Workload per slice, scaling, queue and priority changes, adoption, out-of-sync recovery, deactivation, orphan release | No (integration tests) | Reference |
| U3 | Management integration tests | Default LocalQueue, namespace selector, child LeaderWorkerSet handling | No (integration tests) | Reference; namespace selection planned in E10 |
| U4 | Slice integration tests | Slice readiness with `waitForPodsReady` | No (integration tests) | Reference |
| U5 | e2e suites `DisaggregatedSet integration` and `DisaggregatedSet lifecycle` (`feature:disaggregatedset`, `singlecluster` folder) | Admission, preemption, slice scaling, scale to zero, rollouts, startup policies, deletion, adoption | No: the `singlecluster` label filter does not include `feature:disaggregatedset` | Candidate group for future reuse, case by case, under the rule in the [test strategy](#test-strategy); until then E1–E16 plan the coverage downstream |
| U6 | e2e sequential `DisaggregatedSet rollout with waitForPodsReady` | Rollout without slice eviction | No: the `sequential` folder selects only `feature:admissionfairsharing` | Not reusable as written: it restarts Kueue with a modified configuration, which conflicts with the operator-managed configuration |
| U7 | e2e TAS `TopologyAwareScheduling for DisaggregatedSet` (`area:tas`) | Leader and worker placement, PodSet groups | No: the `singlecluster` filter excludes `area:tas` | Planned downstream in E14–E15 |

### Downstream Tests

The implemented CEL and unit tests (C1, T1–T7) are part of the operator change [openshift/kueue-operator#3072](https://github.com/openshift/kueue-operator/pull/3072), which adds the integration. They are not on this documentation branch, and "Implemented in #3072" refers to that change.

#### CEL Validation Tests

Location: `test/envtest/integrations/kueue_integrations_cel_test.go`.

| ID | Scenario | What It Validates | Status |
|----|----------|-------------------|--------|
| C1 | Invalid frameworks | A duplicated `DisaggregatedSet`, a wrong-case value and an unknown value are rejected | Implemented in #3072 |

The CRD OpenAPI schema enforces the `DisaggregatedSet` enum value and the 19-item list maximum, and T7 checks the generated schema; no separate envtest specs cover them.

#### Unit Tests

| ID | Scenario | What It Validates | Location | Status |
|----|----------|-------------------|----------|--------|
| T1 | Framework and gate rendering | `DisaggregatedSet` renders `disaggregatedset.x-k8s.io/disaggregatedset` and `DisaggregatedSetIntegration: true`, also together with SparkApplication | `pkg/configmap/configmap_test.go` (`TestBuildConfigMap` cases `disaggregated set` and `disaggregated set and spark application`; `TestBuildFeatureGatesForDRASources` case `disaggregatedset framework enables disaggregatedset integration gate`) | Implemented in #3072 |
| T2 | Default off | Without `DisaggregatedSet`, no gate and no framework are rendered, and the outputs of the existing cases are unchanged | `pkg/configmap/configmap_test.go` (`TestBuildFeatureGatesForDRASources` case `frameworks without disaggregatedset do not enable disaggregatedset integration gate`) | Implemented in #3072 |
| T3 | Webhook selection | The DisaggregatedSet and Pod webhooks are kept with the managed-namespace selector when DisaggregatedSet is selected; otherwise the DisaggregatedSet webhooks are removed, and the Pod webhook stays only when another framework that needs it, such as LeaderWorkerSet, is selected | `pkg/webhook/pod_webhook_test.go` (`TestModifyPodBasedValidatingWebhook`, `TestModifyPodBasedMutatingWebhook`) | Implemented in #3072 |
| T4 | Webhook assets | The filtering runs on the bundled webhook configurations without a panic for every framework selection | `pkg/operator/target_config_reconciler_test.go` (`TestPodBasedWebhookAssets`) | Implemented in #3072 |
| T5 | Prerequisite | The DisaggregatedSet API is reported as missing unless discovery serves `disaggregatedset.x-k8s.io/v1` `DisaggregatedSet`; no check when the framework is not selected | `pkg/operator/target_config_reconciler_test.go` (`TestMissingDisaggregatedSetDependencies`) | Implemented in #3072 |
| T6 | RBAC assets | The manager role can read DisaggregatedSets, and the editor and viewer roles carry the batch admin and user aggregation labels | `pkg/operator/target_config_reconciler_test.go` (`TestDisaggregatedSetRBACAssets`) | Implemented in #3072 |
| T7 | Generated artifacts | `make generate manifests regen-crd` leaves no diff; the CRD copies list `DisaggregatedSet` with `maxItems: 19` | CI `test-generate` | Implemented in #3072 |
| T8 | Disable after enable | With the same unrelated settings, render with `DisaggregatedSet`, then remove it and compare the result with the T2 render that never listed it; then add it back and compare the result with the first enabled render. The operator derives the `DisaggregatedSetIntegration` gate only from the frameworks list, so removing the framework also removes the gate; the `Kueue` CR has no separate gate setting | `pkg/configmap/configmap_test.go` | Planned |

#### E2E Tests

These cases are planned and not implemented: the operator change does not include DisaggregatedSet e2e tests, and no CI lane runs them today. They need a lane with a LeaderWorkerSet that serves `disaggregatedset.x-k8s.io/v1` (see [Risks](#risks)). If DisaggregatedSet e2e cases are configured for a lane in the future, a preflight check fails the suite, rather than skipping it, when that lane does not serve the API, so a missing dependency is reported instead of hidden.

**Fixture.** The planned fixture has two slices. Each slice has a `prefill` role with two 200m CPU Pods and a `decode` role with a 100m CPU leader and a 200m CPU worker (64Mi memory each): three PodSets, four Pods, 700m CPU and 256Mi memory per slice. The CPU cut-points are 500m (one role, no slice), 1000m (one slice) and 1400m (both slices). The cases assert Workload conditions, scheduling gates and ClusterQueue status, never simultaneous Pod readiness.

| ID | Scenario | What It Validates | Status |
|----|----------|-------------------|--------|
| E1 | Enablement | The ConfigMap, the webhooks with managed-namespace selectors and a non-degraded `Kueue` CR; the fixture gets two admitted slice Workloads | Planned |
| E2 | Slice identity | Each slice Workload has the PodSets `prefill: 2`, `decode-leader: 1`, `decode-worker: 1` and 700m CPU | Planned |
| E3 | Partial quota | With 500m, no slice reserves quota and every Pod stays gated; raising the quota admits both slices | Planned |
| E4 | One-slice quota | With 1000m, exactly one slice is admitted and the four Pods of the other slice stay gated | Planned |
| E5 | Whole-slice preemption | A higher-priority Job preempts exactly one slice; the other slice keeps its Pods; the slice is re-admitted when the Job is deleted | Planned |
| E6 | Slice scaling | Scaling to three slices adds a gated slice; scaling to one releases the quota of removed slices | Planned |
| E7 | Scale through zero | At zero replicas the slice Workloads are deleted and the quota is released; scaling up admits new slices | Planned |
| E8 | Rollout | A template change that keeps the Pod shape rolls out within the admitted quota and keeps the slice Workloads | Planned |
| E9 | Validation | `maxSurge` above zero, `subRoles`, a queue name in role metadata, and replica or resource changes of a running DisaggregatedSet are rejected | Planned |
| E10 | Unmanaged namespace | A DisaggregatedSet in a namespace without `kueue.openshift.io/managed=true` gets no Workloads and no gates | Planned |
| E11 | No queue name | With `spec.config.workloadManagement.labelPolicy: QueueName`, in a managed namespace without a default LocalQueue, a DisaggregatedSet without a queue name gets no Workloads (`None` renders `manageJobsWithoutQueueName: true`, which manages workloads without a queue name) | Planned |
| E12 | Existing integrations | A Job, a Pod and a LeaderWorkerSet are admitted while the integration is enabled | Planned |
| E13 | Disablement | Following the [documented workflow](https://github.com/kubernetes-sigs/kueue/blob/4f3748d8f2c8b9fc68d131d6e23d44faf33f77c1/site/content/en/docs/tasks/run/disaggregatedset.md#disabling-the-integration), the managed DisaggregatedSets are deleted first. Removing the `DisaggregatedSet` framework from the `Kueue` CR then removes the framework, the gate and the DisaggregatedSet webhooks without degrading the `Kueue` CR | Planned |
| E14 | TAS placement | Every PodSet of a slice gets a topology assignment | Planned |
| E15 | TAS conflicts | TAS annotations combined with a placement policy or the exclusive-topology annotation are rejected | Planned |
| E16 | Quota release | In a ClusterQueue used only by the test, read the CPU and memory reservation of each flavor in the ClusterQueue status (`status.flavorsReservation`). After a slice is removed by scale-down or deletion, the reservation of its flavor eventually equals the sum of the reservations held, when the status is read, by the surviving slices and by every other admitted workload in that flavor, including workloads admitted after the removal. After a slice is preempted (E5), the sum also includes the preemptor. The check compares against that sum, not against a fixed decrease; the reservation is zero only when no workload holds a reservation in that ClusterQueue. It waits for the eventual value, because the Pods of a removed slice can still run for a short time after its quota is released | Planned |
| E17 | Disabling with live managed DisaggregatedSets (characterization) | Outside the documented workflow, in an isolated namespace and ClusterQueue: remove the `DisaggregatedSet` framework while a managed DisaggregatedSet runs, and record the state of its Workloads, Pods and scheduling gates and the `Kueue` CR conditions. The case records the observed behavior and expects neither graceful cleanup nor a rejection; the result decides whether the documentation or the integration needs a follow-up. Non-gating | Planned (characterization) |

#### Manual Tests

| ID | Scenario | What It Validates | Status |
|----|----------|-------------------|--------|
| M1 | Missing prerequisite and recovery | On a cluster without the DisaggregatedSet API, enabling the integration reports `Degraded=True`, reason `MissingDependencies`; installing a LeaderWorkerSet that serves the API clears it without a Kueue restart. Manual, because removing the cluster-scoped CRD on a shared CI cluster deletes every DisaggregatedSet. | Proposed |
| M2 | Enablement guide | The steps and cautions in [`docs/disaggregatedset/`](../../disaggregatedset/README.md) work on a cluster with the verified build combination | Proposed |

## Out of Scope

- MultiKueue with DisaggregatedSet — unsupported upstream and its behavior is not defined; it is recorded as unsupported, not as a passing case or a promised rejection
- Re-testing upstream controller internals beyond the representative downstream cases — covered by U1–U4
- Lifting alpha limitations, cross-role co-location under TAS, inference benchmarking, GPUs or model downloads
- Product support or release policy and the supported version matrix — not set by this test plan

### Scenarios considered and excluded

| Scenario | Reason |
|----------|--------|
| Reproducing the out-of-sync rollout race in CI | Nondeterministic on a real cluster; covered by upstream integration tests. E8 records a `WorkloadRecreated` event as a diagnostic |
| Asserting that physical usage never exceeds the quota after a slice is deleted | Upstream documents a temporary overlap beyond the ClusterQueue quota |
| Enabling the integration by editing the ConfigMap or with a generic feature-gate API | Not a supported operator configuration path |
| Listing the framework without the feature gate | The operator always renders both together |
| DisaggregatedSet-specific checks of Kueue metrics | Kueue metrics are not specific to a workload type, so DisaggregatedSet adds no metric behavior to test; E16 checks the quota through the ClusterQueue status |

## Target Environments

- Disconnected
- FIPS
- ARCH (x86_64, ARM)
- OCP versions: recorded for each validation run
- Hypershift — HCP
- A LeaderWorkerSet installation that serves `disaggregatedset.x-k8s.io/v1`, and an operand built from upstream Kueue at or after `4f3748d8`

## Test Deliverables

- The operator change in `kueue-operator` with the CEL and unit tests (C1, T1–T7), run by Prow CI `test` and `test-generate`
- Later: the planned unit test T8, the downstream e2e tests E1–E17, and a CI configuration change that installs a LeaderWorkerSet serving the DisaggregatedSet API in the lanes that run them
- Later, case by case: reuse of U5 upstream cases through `make e2e-upstream-test` once they make equivalent assertions and have run evidence
- Execution evidence: operator, operand and bundle digests, submodule SHA, OCP version, LeaderWorkerSet version, served DisaggregatedSet CRD version, JUnit reports and run links
- Test report with information for the Docs team

## Test Tasks

- Test plan creation and review
- CEL and unit test automation with the operator change
- Downstream e2e automation (E1–E17), when a LeaderWorkerSet that serves the DisaggregatedSet API is available for CI
- CI configuration for a LeaderWorkerSet that serves the DisaggregatedSet API
- Evaluation of U5 upstream cases for reuse
- Execution, repeated runs of the focused cases to detect timing and cleanup flakes, and evidence collection
- Manual checks M1 and M2

## Pass/Fail Criteria

- No critical or major defects remain open
- C1 and T1–T7 pass in CI `test` and `test-generate`
- T8, once automated, passes in CI `test`
- E1–E16, once automated, pass on a recorded build combination, with JUnit showing them selected and run; a scenario covered by a reused upstream case passes only if that case's JUnit shows it selected and run against the operator-managed deployment
- E17 is non-gating: its observed result is recorded, not judged against an expected outcome
- No test is reported as passing without a run link
- Until E1–E16 run, DisaggregatedSet runtime behavior on OpenShift is recorded as not covered by downstream automated tests

## Risks

| Risk | Impact |
|------|--------|
| The `e2e.patch` carry patch must match the pinned upstream e2e code | `upstream/kueue/build.sh` applies the carry patches before it builds the operand image, so a stale patch breaks the operand build and the upstream runner |
| Every webhook in the synced manifests must be registered in the operator | The webhook filtering panics on an unregistered name; T4 guards the bundled assets |
| A LeaderWorkerSet build that serves `disaggregatedset.x-k8s.io/v1` must be available to the CI lanes | Without it the planned e2e cases and the upstream DisaggregatedSet cases cannot run; the e2e changes must land together with, or after, the CI change |
| Changing the LeaderWorkerSet installed in the existing lanes affects the existing LeaderWorkerSet tests | Regressions in existing coverage must be checked in the CI change |
| Upstream cases may rely on configuration changes that the operator-managed deployment does not allow | Such cases are not counted and the downstream cases cover the scenario |
| The out-of-sync rollout race can recreate a slice Workload | Possible flake in the planned E8; treated as a diagnostic with a `WorkloadRecreated` event |
| Enabling the integration makes existing queue-labeled DisaggregatedSets managed and rolls them out | Test clusters must start clean; documented in `docs/disaggregatedset/` |
| Removing the framework, and with it the gate, while managed DisaggregatedSets exist is outside the documented workflow | The upstream guide ([Disabling the integration](https://github.com/kubernetes-sigs/kueue/blob/4f3748d8f2c8b9fc68d131d6e23d44faf33f77c1/site/content/en/docs/tasks/run/disaggregatedset.md#disabling-the-integration)) says to delete every managed DisaggregatedSet first and recreate them afterwards. E13 and `docs/disaggregatedset/` follow that workflow, and E17 characterizes the other case without assuming its outcome |
