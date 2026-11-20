# Test Plan for the DisaggregatedSet integration

**Plan Status:** Draft
**Story:** OCPKUEUE-989

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
| Story | OCPKUEUE-989 |
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

- **Upstream:** [kubernetes-sigs/kueue#17675](https://github.com/kubernetes-sigs/kueue/pull/17675) covers the controller, webhook, PodSet and Pod adoption logic with unit, integration and e2e tests. Downstream does not re-implement this logic. Upstream e2e cases are reused through `make e2e-upstream-test` where they run against the operator-managed deployment. An upstream case counts as coverage for a downstream scenario only when it makes the same observable assertion, works with the operator-managed configuration, and its JUnit report shows it selected and run, not filtered.
- **Downstream:** Operator-specific tests in the `kueue-operator` repo cover what upstream cannot: the `Kueue` CR schema, CR → ConfigMap rendering, webhook filtering and namespace selection, RBAC assets, prerequisite status, and enablement and disablement through the operator-managed deployment on OpenShift. Behavioral scenarios are also automated downstream until an upstream equivalent has run evidence. E2e cases use a small, deterministic CPU and memory fixture with explicit quota cut-points, and assert Workload conditions, scheduling gates and ClusterQueue usage, never simultaneous Pod readiness.

**Fixture.** The e2e DisaggregatedSet fixture has two slices. Each slice has a `prefill` role with two 200m CPU Pods and a `decode` role with a 100m CPU leader and a 200m CPU worker (64Mi memory each): three PodSets, four Pods and 700m CPU per slice. The CPU cut-points are 500m (one role, no slice), 1000m (one slice) and 1400m (both slices).

## Test Scope

### Upstream Tests

| ID | Scenario | What It Validates | Downstream use |
|----|----------|-------------------|----------------|
| U1 | Webhook integration tests | Validation of roles, rollout settings, Kueue labels and annotations, size limits, TAS rules, immutable fields | Reference; representative downstream cases in E9 |
| U2 | Controller integration tests | One Workload per slice, scaling, queue and priority changes, adoption, out-of-sync recovery, deactivation, orphan release | Reference |
| U3 | Management integration tests | Default LocalQueue, namespace selector, child LeaderWorkerSet handling | Reference; namespace selection re-checked in E10 |
| U4 | Slice integration tests | Slice readiness with `waitForPodsReady` | Reference |
| U5 | e2e `DisaggregatedSet integration` and `DisaggregatedSet lifecycle` (`feature:disaggregatedset`) | Admission, preemption, slice scaling, scale to zero, rollouts, startup policies, deletion, adoption | Run by `make e2e-upstream-test` in the `singlecluster` folder (R1) |
| U6 | e2e sequential `DisaggregatedSet rollout with waitForPodsReady` | Rollout without slice eviction | Skipped by the `e2e.patch` carry patch: it restarts Kueue with a modified configuration, which conflicts with the operator-managed configuration (R3) |
| U7 | e2e TAS `TopologyAwareScheduling for DisaggregatedSet` (`area:tas`) | Leader and worker placement, PodSet groups | Not wired into the downstream runner; covered downstream by E14–E15 |

### Downstream Tests

#### CEL Validation Tests

Location: `test/envtest/integrations/kueue_integrations_cel_test.go`.

| ID | Scenario | What It Validates | Status |
|----|----------|-------------------|--------|
| C1 | DisaggregatedSet framework | `[DisaggregatedSet]` and all 19 frameworks are accepted | Implemented |
| C2 | Invalid frameworks | A duplicate, a wrong-case and an unknown value are rejected, and 20 items exceed `maxItems: 19` | Implemented |

#### Unit Tests

| ID | Scenario | What It Validates | Location | Status |
|----|----------|-------------------|----------|--------|
| T1 | Framework and gate rendering | `DisaggregatedSet` renders `disaggregatedset.x-k8s.io/disaggregatedset` and `DisaggregatedSetIntegration: true`, also together with SparkApplication | `pkg/configmap/configmap_test.go` | Implemented |
| T2 | Default off | Without `DisaggregatedSet`, no gate and no framework are rendered and existing outputs are unchanged | `pkg/configmap/configmap_test.go` | Implemented |
| T3 | Webhook selection | The DisaggregatedSet and Pod webhooks are kept with the managed-namespace selector when DisaggregatedSet is selected; otherwise the DisaggregatedSet webhooks are removed, and the Pod webhook stays only when another framework that needs it, such as LeaderWorkerSet, is selected | `pkg/webhook/pod_webhook_test.go` | Implemented |
| T4 | Webhook assets | The filtering runs on the bundled webhook configurations without a panic for every framework selection | `pkg/operator/target_config_reconciler_test.go` | Implemented |
| T5 | Prerequisite | The DisaggregatedSet API is reported as missing unless discovery serves `disaggregatedset.x-k8s.io/v1` `DisaggregatedSet`; no check when the framework is not selected | `pkg/operator/target_config_reconciler_test.go` | Implemented |
| T6 | RBAC assets | The manager role can read DisaggregatedSets, and the editor and viewer roles carry the batch admin and user aggregation labels | `pkg/operator/target_config_reconciler_test.go` | Implemented |
| T7 | Generated artifacts | `make generate manifests regen-crd` leaves no diff; the CRD copies list `DisaggregatedSet` with `maxItems: 19` | CI `test-generate` | Implemented |

#### E2E Tests

Location: `test/e2e/e2e_disaggregatedset_test.go`, label `disaggregatedset` (and `tas` for E14–E15), in `make e2e-ci-test`. The suite fails, rather than skips, when the DisaggregatedSet API is not served.

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
| E11 | No queue name | A DisaggregatedSet without a queue name gets no Workloads | Planned |
| E12 | Existing integrations | A Job, a Pod and a LeaderWorkerSet are admitted while the integration is enabled | Planned |
| E13 | Disablement | Removing `DisaggregatedSet` after the DisaggregatedSets are deleted removes the framework, the gate and the DisaggregatedSet webhooks without degrading the `Kueue` CR | Planned |
| E14 | TAS placement | Every PodSet of a slice gets a topology assignment | Planned |
| E15 | TAS conflicts | TAS annotations combined with a placement policy or the exclusive-topology annotation are rejected | Planned |

#### Upstream Suite

| ID | Scenario | What It Validates | Status |
|----|----------|-------------------|--------|
| R1 | U5 in the `singlecluster` folder | The runner adds `DisaggregatedSet` to the `Kueue` CR and selects `feature:disaggregatedset`; JUnit lists the cases as run | Planned |
| R2 | U7 TAS cases | Not wired; E14–E15 cover TAS downstream | Not planned |
| R3 | U6 sequential rollout | Skipped by the carry patch | Implemented (skip) |

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

## Target Environments

- Disconnected
- FIPS
- ARCH (x86_64, ARM)
- OCP versions: recorded for each validation run
- Hypershift — HCP
- A LeaderWorkerSet installation that serves `disaggregatedset.x-k8s.io/v1`, and an operand built from upstream Kueue at or after `4f3748d8`

## Test Deliverables

- Downstream PRs in `kueue-operator` (Prow CI): the operator change with CEL and unit tests, and the e2e tests with the upstream runner wiring
- A CI configuration change that installs a LeaderWorkerSet serving the DisaggregatedSet API in the e2e lanes
- Execution evidence: operator, operand and bundle digests, submodule SHA, OCP version, LeaderWorkerSet version, served DisaggregatedSet CRD version, JUnit reports and run links
- Test report with information for the Docs team

## Test Tasks

- Test plan creation and review
- CEL and unit test automation with the operator change
- Downstream e2e automation and upstream runner wiring
- CI configuration for a LeaderWorkerSet that serves the DisaggregatedSet API
- Execution, repeated runs of the focused cases to detect timing and cleanup flakes, and evidence collection
- Manual checks M1 and M2

## Pass/Fail Criteria

- No critical or major defects remain open
- C1–C2 and T1–T7 pass in CI `test` and `test-generate`
- E1–E15 pass on a recorded build combination, with JUnit showing them selected and run; a scenario covered by an upstream case passes only if that case's JUnit shows it selected and run against the operator-managed deployment
- No test is reported as passing without a run link
- Non-gating: R2 (not wired) and R3 (skipped)

## Risks

| Risk | Impact |
|------|--------|
| The `e2e.patch` carry patch must match the pinned upstream e2e code | `upstream/kueue/build.sh` applies the carry patches before it builds the operand image, so a stale patch breaks the operand build and the upstream runner |
| Every webhook in the synced manifests must be registered in the operator | The webhook filtering panics on an unregistered name; T4 guards the bundled assets |
| A LeaderWorkerSet build that serves `disaggregatedset.x-k8s.io/v1` must be available to the CI lanes | Without it the e2e cases and the upstream DisaggregatedSet cases cannot run; the e2e and runner changes must land together with, or after, the CI change |
| Changing the LeaderWorkerSet installed in the existing lanes affects the existing LeaderWorkerSet tests | Regressions in existing coverage must be checked in the CI change |
| Upstream cases may rely on configuration changes that the operator-managed deployment does not allow | Such cases are not counted and the downstream cases cover the scenario |
| The out-of-sync rollout race can recreate a slice Workload | Possible E8 flake; treated as a diagnostic with a `WorkloadRecreated` event |
| Enabling the integration makes existing queue-labeled DisaggregatedSets managed and rolls them out | Test clusters must start clean; documented in `docs/disaggregatedset/` |
