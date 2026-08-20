# Test Plan for Topology Aware Scheduling (TAS)

**Plan Status:** In progress (aligned with the vendored upstream Kueue source at `0ef77c8f`, `v0.20.0-devel-349-g0ef77c8fa`)
**Feature:** [OCPSTRAT-3258] — Topology Aware Scheduling
**Testing Epic:** [OCPKUEUE-649](https://redhat.atlassian.net/browse/OCPKUEUE-649)

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
| Testing Epic | [OCPKUEUE-649](https://redhat.atlassian.net/browse/OCPKUEUE-649) |
| KEP | [2724-topology-aware-scheduling](https://github.com/kubernetes-sigs/kueue/tree/main/keps/2724-topology-aware-scheduling) (beta since Kueue v0.14) |
| Upstream tests | [`test/integration/singlecluster/tas`](https://github.com/kubernetes-sigs/kueue/tree/main/test/integration/singlecluster/tas), [`test/e2e/tas`](https://github.com/kubernetes-sigs/kueue/tree/main/test/e2e/tas) (`baseline/`: Job, Pod group, StatefulSet, Hotswap; `extended/`: JobSet, AppWrapper, MPIJob, PyTorchJob, RayJob, TrainJob, LeaderWorkerSet), [`test/e2e/singlecluster/baseline/tas_test.go`](https://github.com/kubernetes-sigs/kueue/blob/main/test/e2e/singlecluster/baseline/tas_test.go), [`test/e2e/multikueue/baseline/tas_test.go`](https://github.com/kubernetes-sigs/kueue/blob/main/test/e2e/multikueue/baseline/tas_test.go) |
| Downstream tests | [openshift/kueue-operator#1888](https://github.com/openshift/kueue-operator/pull/1888) "add tas downstream tests" (`test/e2e/e2e_tas_test.go`) |
| Downstream docs | [openshift/kueue-operator#2282](https://github.com/openshift/kueue-operator/pull/2282) "add TAS demo for quick examples" |
| Bugs | None known at time of writing |

## Introduction

Topology Aware Scheduling (TAS) lets Kueue place a workload's Pods with awareness of the physical/network topology of the cluster (e.g. datacenter → block → rack → hostname), instead of leaving bin-packing entirely to kube-scheduler. Cluster admins define a `Topology` CRD describing an ordered list of node-label levels, then reference it from a `ResourceFlavor` via `spec.topologyName`. Workload authors opt in per PodSet with the `kueue.x-k8s.io/podset-required-topology` or `kueue.x-k8s.io/podset-preferred-topology` annotations (and the newer `podset-slice-required-topology*` annotations for sub-group co-location), naming the topology level their Pods must (or should, best-effort) share.

TAS reached beta in upstream Kueue v0.14 and is enabled by default. The operator builds and ships the vendored upstream source at `0ef77c8f` (v0.20.0-devel); the `sigs.k8s.io/kueue v0.19.0` entry in the operator's `go.mod` is a library dependency and does not identify the operand build. There is no operator TAS switch to test. The operator's responsibility is to ship the `Topology` CRD (`bindata/assets/kueue-operator/crds/crd-topologies.kueue.x-k8s.io-v1beta2.yaml`), its RBAC, and a controller-manager build with TAS enabled. In this upstream source, the Mixed TAS profile, failed-node replacement, elastic workload slices, node-taint replacement, multi-layer topology, and overlapping-flavor handling are enabled by default (beta); elastic workload slices with TAS, balanced placement, and the fixed-time NotReady replacement gate remain alpha or disabled by default. The upstream source also includes TAS cache feature gates and validation for feature-gate dependencies.

Because TAS's scheduling logic, node/topology caching, and admission-check interactions are all internal to the vendored kueue-controller-manager, the bulk of correctness testing belongs upstream. The downstream job is to confirm the operator wires TAS up correctly on a real OpenShift cluster (CRD/RBAC delivery, restricted-SCC nodes, real multi-node topology, and OpenShift's supported job integrations — Job, JobSet, Pod/Pod groups, Deployment).

## Test Strategy

- **Upstream:** Kueue's own suite is the primary source of TAS correctness coverage: `pkg/cache/scheduler` and `pkg/scheduler` unit tests exercise the TAS flavor/node cache and flavor-assigner; `test/integration/singlecluster/tas/tas_test.go` and `tas_preserve_flavor_scan_progress_test.go` cover admission, topology and ResourceFlavor validation, node lifecycle, preemption, multiple flavors, affinity/provisioning, fragmentation, and scheduler-progress preservation. The dedicated real-cluster suite at `test/e2e/tas/{baseline,extended}` covers rank-ordering and required/preferred topology for Job, Pod group, StatefulSet, JobSet, AppWrapper, MPIJob, PyTorchJob, RayJob, TrainJob, and LeaderWorkerSet. Hotswap is in `test/e2e/tas/baseline/hotswap_test.go`, and `test/e2e/multikueue/baseline/tas_test.go` covers MultiKueue routing. The current source also enables additional beta TAS behavior by default, including elastic workload slices, node-taint replacement, multi-layer topology, overlapping-flavor handling, and TAS cache optimizations. Downstream testing therefore focuses on operator delivery, OpenShift workload integrations, and real-cluster topology behavior rather than duplicating upstream rank-ordering tests.
- **Downstream:** `test/e2e/e2e_tas_test.go` (added in [#1888](https://github.com/openshift/kueue-operator/pull/1888)) covers the current golden path against a live OpenShift cluster: hostname-level TAS across Job, JobSet, Pod, Pod group, Deployment (single/multi-replica), StatefulSet, and LeaderWorkerSet; a three-level datacenter topology with required/preferred placement; a two-level topology verifying co-location; and operator-delivered CRD/RBAC. The remaining downstream gap is upgrade/reconciliation validation.

## Test Scope

### Upstream Tests

| ID | Scenario | What It Validates |
|----|----------|-------------------|
| T1 | Job/Pod/Pod-group admission via TAS (e2e, singlecluster) | End-to-end admission on a real cluster with `TopologyAssignment` populated |
| T2 | Required topology domain fit/no-fit (integration) | Workload is admitted only when a domain satisfies the required level; otherwise stays pending |
| T3 | Topology/ResourceFlavor CRUD & validation (integration + webhook) | Create/update/delete rules on `Topology` and TAS `ResourceFlavor` (immutable `topologyName`, node-label deletion rules) |
| T4 | Node lifecycle: failure, replacement, taints, NotReady handling | `TopologyAssignment` and `UnhealthyNodes` are updated/evicted correctly as nodes fail, get tainted, or recover |
| T5 | Node affinity / provisioning integration | Admission respects preferred/required node affinity and waits correctly with `ProvisioningRequest` |
| T6 | Preemption interaction (within ClusterQueue and Cohort) | TAS workloads correctly preempt/borrow capacity |
| T7 | Multiple TAS ResourceFlavors / shared nodes | Correct flavor selection and usage accounting when several TAS flavors overlap |
| T8 | Node resource fragmentation | Scheduler avoids/handles fragmented capacity (e.g. GPU-count edge cases) |
| T9 | Elastic workload slices (`ElasticJobsViaWorkloadSlices`) | Scale-up keeps pods in the same topology domain |
| T10 | Multi-layer topology constraints | Multiple simultaneous constraint layers are honored |
| T11 | MultiKueue + TAS (e2e) | Implicit TAS, preferred-topology routing, and required hostname topology across MultiKueue worker clusters |
| T12 | Resource transformation + TAS | Quota sharing across flavors via resource transformation still respects topology |
| T13 | Rank-ordering placement per job kind (e2e, `test/e2e/tas/baseline` + `extended`) | Job, Pod group, StatefulSet, JobSet, AppWrapper, MPIJob, PyTorchJob, RayJob, and TrainJob all place pods matching their rank/index ordering across topology domains, including podset-slice (two-level and slice-only) scheduling for JobSet/TrainJob |
| T14 | LeaderWorkerSet + TAS (e2e, `extended/leaderworkerset_test.go`) | Leader/worker grouping — with and without explicit grouping annotation, and with matching/differing leader-vs-worker resource requests — is placed correctly by rank |
| T15 | Hotswap (e2e, `test/e2e/tas/baseline/hotswap_test.go`, `Ordered`) | A failed/tainted node within a domain is replaced in place (NoExecute+tolerationSeconds, NoSchedule, NotReady), or the workload is evicted when no replacement is possible |
| T16 | TAS feature-gate dependency validation (unit, `pkg/config/validation_test.go`) | Invalid combinations of TAS, profile, failed-node replacement, balanced-placement, node-taint replacement, multi-layer, and overlapping-flavor gates are rejected with actionable errors |
| T17 | TAS cache behavior and scan progress preservation (integration/unit) | Cache reuse and flavor-scan progress remain correct when topology assignments or scheduler snapshots change |

The integration suite uses envtest/Kind, while the TAS e2e suites run against dedicated real clusters; downstream OCP tests complement rather than replace this coverage. The exact upstream source revision under test is the vendored submodule commit documented at the top of this plan.

### Downstream Tests

| ID | Scenario | Sub-task | Why downstream-specific |
|----|----------|----------|------------------------|
| D1 | Job/JobSet/Pod/Pod-group/Deployment/StatefulSet/LeaderWorkerSet admission via TAS with hostname topology (`test/e2e/e2e_tas_test.go`) | [#1888](https://github.com/openshift/kueue-operator/pull/1888) | Confirms the deployed controller-manager, enabled integrations, and restricted-SCC nodes admit the currently covered downstream workload shapes. Deployment remains the operator-specific case; the other integrations are smoke coverage on real OCP. |
| D2 | Required (block) and preferred (rack) topology admission on a 3-level datacenter topology | [#1888](https://github.com/openshift/kueue-operator/pull/1888) | Confirms the operator-shipped `Topology`/`ResourceFlavor` CRDs and webhook validation behave correctly with custom (non-hostname) topology levels on real labeled nodes |
| D3 | Two-level topology (block/rack, no hostname) co-location verification | [#1888](https://github.com/openshift/kueue-operator/pull/1888) | Validates domain co-location logic end-to-end against real node topology, complementing upstream's Kind-based equivalent with an OCP-specific network/CNI environment |
| D4 | Topology CRD and RBAC (`kueue-topology-editor-role`, `kueue-topology-viewer-role`) are present after operator install | `test/e2e/e2e_tas_test.go` | Verifies the operator-delivered CRD is established and the editor/viewer ClusterRoles grant expected topology verbs. Upgrade persistence remains a follow-up lifecycle check. |
| D5 | LeaderWorkerSet and StatefulSet admission via TAS with hostname topology (`test/e2e/e2e_tas_test.go`) | `test/e2e/e2e_tas_test.go` | Implemented in the hostname topology group; LWS verifies both leader and worker PodSets, while StatefulSet verifies ungating and readiness. |

## Out of Scope

- Upstream scheduler-internals coverage (TAS node/cache behavior, flavor-assigner, balanced placement, elastic workload slices, fragmentation, multi-layer topology, resource transformation, scan-progress preservation, and feature-gate dependency validation) — covered by [T4–T10, T12, T16–T17](#upstream-tests); no OpenShift-specific divergence expected.
- Rank-ordering placement per job kind (Job, Pod group, StatefulSet, JobSet, AppWrapper, MPIJob, PyTorchJob, RayJob, TrainJob, LeaderWorkerSet) — thoroughly covered upstream by [T13–T14](#upstream-tests) in `test/e2e/tas/{baseline,extended}`. Downstream keeps only a thin admission smoke test per kind (D1) rather than re-testing rank-ordering logic itself.
- TAS features that remain alpha or disabled by default in the vendored source (`ElasticJobsViaWorkloadSlicesWithTAS`, `TASBalancedPlacement`, and `TASReplaceNodeDueToNotReadyOverFixedTime`) — the operator does not enable or expose these gates, so there is nothing downstream to validate until/unless the operator adds a knob for them. The base elastic-workload-slice feature, Mixed TAS profile, failed-node replacement, node-taint replacement, multi-layer topology, and overlapping-flavor handling are beta/default and are covered upstream.
- Node-failure / replacement / `ProvisioningRequest` / Hotswap scenarios (T4–T5, T15) — no OCP-specific node-lifecycle or autoscaler-integration behavior identified that upstream's Kind-based tests wouldn't already surface.

### Scenarios considered and excluded

| Scenario | Reason |
|----------|--------|
| MultiKueue + TAS on OCP | The kueue-operator does not yet have dedicated MultiKueue e2e coverage beyond a CRD-existence check; combining it with TAS is premature until MultiKueue itself has a downstream test plan |
| FIPS-specific TAS behavior | TAS is pure scheduling/placement logic with no cryptographic code path; `strictfipsruntime` build tag has no interaction with topology assignment |

## Target Environments

- Disconnected — not expected to differ (no external network dependency in TAS itself); smoke-test only
- FIPS — smoke-test only, no TAS-specific crypto path (see [Out of Scope](#out-of-scope))
- ARCH: x86_64 (primary); ARM smoke test if available
- OCP versions: current supported operator release stream (match `kueue-operator` support matrix)
- Hypershift — HCP: covered by the `test-e2e-4-22-hypershift` periodic (runs `make e2e-ci-test` against a 2-node HCP guest cluster); D1–D5 are expected to run there on `main`. Needs backport to older release branches (see [Risks](#risks))
- Requires clusters with ≥2–4 labelable worker nodes to exercise multi-domain (block/rack) scenarios; single-node/SNO clusters can only exercise the single-hostname-domain path

## Test Deliverables

- `test/e2e/e2e_tas_test.go` covers D1–D5 in Prow CI under `Label("tas")`; D4 and D5 are implemented in the current test file.
- Backport of `test/e2e/e2e_tas_test.go` to any release branch that doesn't yet have it, so that branch's `test-e2e-4-22-hypershift` periodic picks up TAS coverage automatically
- Test report summarizing pass/fail per scenario ID above, for the Docs team and OCPKUEUE epic

## Test Tasks

Testing-related work items for this feature — test planning, authoring, and validation, distinct from the implementation stories used to build TAS itself:

- Exploratory testing on real multi-zone/multi-rack OCP clusters to validate node-labeling assumptions behind D2/D3
- Test plan review and JIRA sub-task tracking for the remaining upgrade/reconciliation check in D4
- Run the TAS-labeled downstream suite against supported OCP and Hypershift environments
- Confirm/track backport of `e2e_tas_test.go` into active release branches lagging `main` (e.g. release-1.4)

## Pass/Fail Criteria

- No critical or major defects remain open against D1–D5
- All existing `test/e2e/e2e_tas_test.go` scenarios (D1–D5) pass on every supported OCP version in the support matrix, including the `test-e2e-4-22-hypershift` HCP lane
- Upstream (`make e2e-upstream-test`) and downstream (`make e2e-ci-test`) TAS-labeled CI jobs pass consistently, without relying on the `flaky` label

## Risks

| Risk | Impact |
|------|--------|
| `test/e2e/e2e_tas_test.go` only exists on `main` (confirmed absent on `release-1.4`, e.g. via `test-e2e-4-22-hypershift` junit showing zero `[tas]` specs) | Older/active release branches get no TAS coverage — including no Hypershift/HCP signal — until the file is backported; must not assume "the periodic passed" implies TAS ran without checking the junit for that branch |
| The vendored upstream source may change independently of the operator's `sigs.k8s.io/kueue` library dependency, or currently alpha TAS features may graduate | Revalidate this plan, especially the default feature-gate assumptions and upstream test inventory, whenever the `upstream/kueue/src` submodule is updated |
| Multi-node topology tests (D2/D3) require clusters with enough labelable worker nodes | CI lanes limited to 1–2 nodes (e.g. some SNO/compact configs) can't exercise the full block/rack matrix and would need to skip or use a reduced topology; the Hypershift lane's `HYPERSHIFT_NODE_COUNT: "2"` is sufficient for the current D1–D5 scenarios but leaves the 4-node block/rack matrix under-exercised |
| No dedicated MultiKueue downstream test plan yet | Blocks scoping T11-equivalent downstream coverage; tracked as excluded above until MultiKueue has its own plan |
| No real hardware cluster | Design partners engaged (llm-d / training) |
