# Test Plan for Topology Aware Scheduling (TAS)

**Plan Status:** Draft
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

TAS reached beta in upstream Kueue v0.14 and is enabled by default — there is no Kueue feature-gate flag an admin needs to flip, and correspondingly the kueue-operator has no CRD field or config knob for it (`buildFeatureGates()` in `pkg/config/configmap.go` only toggles DRA, Spark, and short-workload-name gates). The operator's role is limited to shipping the `Topology` CRD (`bindata/assets/kueue-operator/crds/crd-topologies.kueue.x-k8s.io-v1beta2.yaml`) and its RBAC, and deploying a kueue-controller-manager build where TAS is already active. Several TAS sub-features (balanced placement, node-failure replacement, multi-layer topology, elastic workload slices, etc.) remain alpha and off by default upstream and are not exercised by the operator either.

Because TAS's scheduling logic, node/topology caching, and admission-check interactions are all internal to the vendored kueue-controller-manager, the bulk of correctness testing belongs upstream. The downstream job is to confirm the operator wires TAS up correctly on a real OpenShift cluster (CRD/RBAC delivery, restricted-SCC nodes, real multi-node topology, and OpenShift's supported job integrations — Job, JobSet, Pod/Pod groups, Deployment).

## Test Strategy

- **Upstream:** Kueue's own suite is the primary source of TAS correctness coverage and is extensive: `pkg/cache/scheduler` and `pkg/scheduler` unit tests exercise the TAS flavor/node cache and flavor-assigner in isolation; `test/integration/singlecluster/tas/tas_test.go` (~94 `It`s, envtest + Kind) covers admission, topology CRD/RF validation, node lifecycle (failure, taints, affinity, provisioning), preemption interaction, multiple flavors, and fragmentation. On top of that, a dedicated real-cluster suite at `test/e2e/tas/{baseline,extended}` already exercises rank-ordering placement and required/preferred topology admission across essentially every job kind Kueue supports — Job, Pod group, StatefulSet, JobSet, AppWrapper, MPIJob, PyTorchJob, RayJob, TrainJob, and LeaderWorkerSet — plus a `Hotswap` suite covering node failure/taint replacement within a domain. `test/e2e/multikueue/baseline/tas_test.go` covers MultiKueue routing. Given how much of the operator's supported job-integration surface is already covered end-to-end upstream, downstream TAS tests should focus on what upstream genuinely can't observe (operator-delivered CRDs/RBAC, OCP-only workload shapes like Deployment, operator lifecycle/reconciliation) rather than re-proving rank-ordering placement per job kind.
- **Downstream:** `test/e2e/e2e_tas_test.go` (added in [#1888](https://github.com/openshift/kueue-operator/pull/1888)) already covers the golden path against a live OpenShift cluster: hostname-level TAS across Job, JobSet, Pod, Pod group, and Deployment (single/multi-replica), plus a two/three-level datacenter topology (block/rack) verifying required vs. preferred placement and domain co-location. This plan extends that baseline to close the remaining gaps that are genuinely operator-specific (RBAC/CRD delivery, upgrade/reconciliation behavior, Hypershift) rather than adding per-job-kind rank-ordering tests that upstream already covers.

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
| T15 | Hotswap (e2e, `baseline/hotswap_test.go`, `Ordered`) | A failed/tainted node within a domain is replaced in place (NoExecute+tolerationSeconds, NoSchedule, NotReady), or the workload is evicted when no replacement is possible |

These tests are not run due to a reliance on kind clusters but they provide confidence in the feature.

### Downstream Tests

| ID | Scenario | Sub-task | Why downstream-specific |
|----|----------|----------|------------------------|
| D1 | Job/JobSet/Pod/Pod-group/Deployment admission via TAS with hostname topology (`test/e2e/e2e_tas_test.go`) | [#1888](https://github.com/openshift/kueue-operator/pull/1888) | Deployment has no upstream TAS coverage at all (not a native Kueue job integration — exercised only via the operator's Pod-group builder). Job/JobSet/Pod/Pod-group are already covered upstream in `test/e2e/tas`; keeping a thin admission smoke test here confirms the operator's deployed kueue-controller-manager image and restricted-SCC nodes behave the same way on real OCP, not new coverage |
| D2 | Required (block) and preferred (rack) topology admission on a 3-level datacenter topology | [#1888](https://github.com/openshift/kueue-operator/pull/1888) | Confirms the operator-shipped `Topology`/`ResourceFlavor` CRDs and webhook validation behave correctly with custom (non-hostname) topology levels on real labeled nodes |
| D3 | Two-level topology (block/rack, no hostname) co-location verification | [#1888](https://github.com/openshift/kueue-operator/pull/1888) | Validates domain co-location logic end-to-end against real node topology, complementing upstream's Kind-based equivalent with an OCP-specific network/CNI environment |
| D4 | Topology CRD and RBAC (`topology_editor_role`, `topology_viewer_role`) are present after operator install/upgrade | _TBD_ | Delivery of CRDs/RBAC is entirely the operator's responsibility — not exercised by upstream at all |
| D5 | LeaderWorkerSet, StatefulSet admission via TAS with hostname topology (`test/e2e/e2e_tas_test.go`) | _TBD_ | Smoke test verifying same test cases as D1 |

## Out of Scope

- Upstream scheduler-internals coverage (TAS node cache, flavor-assigner, balanced placement, elastic workload slices, fragmentation, multi-layer topology, resource transformation) — thoroughly covered by [T4–T10, T12](#upstream-tests); no OpenShift-specific divergence expected.
- Rank-ordering placement per job kind (Job, Pod group, StatefulSet, JobSet, AppWrapper, MPIJob, PyTorchJob, RayJob, TrainJob, LeaderWorkerSet) — thoroughly covered upstream by [T13–T14](#upstream-tests) in `test/e2e/tas/{baseline,extended}`. Downstream keeps only a thin admission smoke test per kind (D1) rather than re-testing rank-ordering logic itself.
- Alpha TAS sub-features off by default upstream (`TASProfileMixed`, `TASProfileBestFit`, `TASBalancedPlacement`, `TASReplaceNode*`, `TASMultiLayerTopology`, `TASRespectNodeAffinityPreferred`, `TASHandleOverlappingFlavors`) — the operator does not enable or expose these gates, so there is nothing downstream to validate until/unless the operator adds a knob for them.
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
- Hypershift — HCP: already covered by the `test-e2e-4-22-hypershift` periodic (runs `make e2e-ci-test` against a 2-node HCP guest cluster); confirmed all D1–D3 TAS specs pass there on `main`. Needs backport to older release branches (see [Risks](#risks))
- Requires clusters with ≥2–4 labelable worker nodes to exercise multi-domain (block/rack) scenarios; single-node/SNO clusters can only exercise the single-hostname-domain path

## Test Deliverables

- Downstream PRs adding D4 to `test/e2e/e2e_tas_test.go` in `kueue-operator` (Prow CI, `Label("tas")`)
- Backport of `test/e2e/e2e_tas_test.go` to any release branch that doesn't yet have it, so that branch's `test-e2e-4-22-hypershift` periodic picks up TAS coverage automatically
- Test report summarizing pass/fail per scenario ID above, for the Docs team and OCPKUEUE epic

## Test Tasks

Testing-related work items for this feature — test planning, authoring, and validation, distinct from the implementation stories used to build TAS itself:

- Exploratory testing on real multi-zone/multi-rack OCP clusters to validate node-labeling assumptions behind D2/D3
- Test plan review and JIRA sub-task creation for D4
- Downstream test automation for D4 (CRD/RBAC delivery)
- Confirm/track backport of `e2e_tas_test.go` into active release branches lagging `main` (e.g. release-1.4)

## Pass/Fail Criteria

- No critical or major defects remain open against D1–D4
- All existing `test/e2e/e2e_tas_test.go` scenarios (D1–D3) continue to pass on every supported OCP version in the support matrix, including the `test-e2e-4-22-hypershift` HCP lane
- Upstream (`make e2e-upstream-test`) and downstream (`make e2e-ci-test`) TAS-labeled CI jobs pass consistently, without relying on the `flaky` label

## Risks

| Risk | Impact |
|------|--------|
| `test/e2e/e2e_tas_test.go` only exists on `main` (confirmed absent on `release-1.4`, e.g. via `test-e2e-4-22-hypershift` junit showing zero `[tas]` specs) | Older/active release branches get no TAS coverage — including no Hypershift/HCP signal — until the file is backported; must not assume "the periodic passed" implies TAS ran without checking the junit for that branch |
| Upstream TAS sub-features (balanced placement, node replacement, multi-layer topology) could graduate to beta/GA and become enabled by default in a future kueue bump | Would require re-scoping several "out of scope" items back into downstream coverage |
| Multi-node topology tests (D2/D3) require clusters with enough labelable worker nodes | CI lanes limited to 1–2 nodes (e.g. some SNO/compact configs) can't exercise block/rack co-location and would need to skip or use a reduced topology; the Hypershift lane's `HYPERSHIFT_NODE_COUNT: "2"` is sufficient for D1–D3 as written today but leaves the 4-node block/rack matrix under-exercised |
| No dedicated MultiKueue downstream test plan yet | Blocks scoping T11-equivalent downstream coverage; tracked as excluded above until MultiKueue has its own plan |
| No real hardware cluster | Design partners engaged (llm-d / training) |
