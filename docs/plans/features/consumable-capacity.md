# Test Plan for Consumable Capacity (DRA Quota)

**Plan Status:** Draft  
**Feature:** [OCPSTRAT-3317](https://redhat.atlassian.net/browse/OCPSTRAT-3317) — Consumable Capacity (DRA quota-based resource accounting)  
**Testing Epic:** [OCPKUEUE-724](https://redhat.atlassian.net/browse/OCPKUEUE-724)  

## Overview

- [References](#references) — KEPs, JIRA tickets, upstream/downstream PRs, and implementation docs
- [Introduction](#introduction) — Feature overview and scope for Kueue 1.5
- [Test Strategy](#test-strategy) — Upstream vs downstream approach and deduplication
- [Test Scope](#test-scope) — Upstream and downstream test scenarios (required and optional)
- [Out of Scope](#out-of-scope) — What is not being tested and why
- [Target Environments](#target-environments) — OCP versions, Kubernetes 1.36+
- [Test Deliverables](#test-deliverables) — PRs, test reports, and artifacts
- [Test Tasks](#test-tasks) — Execution and validation work
- [Pass/Fail Criteria](#passfail-criteria) — Exit criteria for testing completion
- [Risks](#risks) — Known blockers and unknowns

## References

| Type | Link |
|------|------|
| Testing Epic | [OCPKUEUE-724](https://redhat.atlassian.net/browse/OCPKUEUE-724) |
| CI Integration Story | [OCPKUEUE-847](https://redhat.atlassian.net/browse/OCPKUEUE-847) — Enable upstream DRA Consumable Capacity e2e suite in downstream CI |
| Kubernetes KEP | [KEP-5075: DRA Consumable Capacity](https://github.com/kubernetes/enhancements/tree/master/keps/sig-scheduling/5075-dra-consumable-capacity) |
| Upstream Kueue docs | [Counter-based vs Capacity-based Quota](https://github.com/kubernetes-sigs/kueue/blob/main/site/content/en/docs/concepts/dynamic_resource_allocation.md#counter-based-vs-capacity-based-quota) |
| Upstream Kueue tests | [PR #13274](https://github.com/kubernetes-sigs/kueue/pull/13274) (consumable capacity quota accounting) |
| Upstream Kueue implementation | [PR #13152](https://github.com/kubernetes-sigs/kueue/pull/13152) |
| Downstream operator tests | [PR #2546](https://github.com/openshift/kueue-operator/pull/2546) (operator CRD, config rendering, e2e) |
| Partitionable Devices reference | [PR #2069](https://github.com/openshift/kueue-operator/pull/2069) (operator integration) |
| **Bugs** | [#14541](https://github.com/kubernetes-sigs/kueue/issues/14541) — Configuration accepts DRA capacity qualified names with multiple slashes |

## Introduction

**Consumable Capacity** enables Kueue to enforce quotas on fractional DRA (Dynamic Resource Allocation) device capacity. Previously, Kueue could only track whole-device allocations through the Counter source (Partitionable Devices). With Consumable Capacity, operators can define quota limits per device dimension (e.g., GPU memory, compute units), and Kueue tracks how much capacity each workload consumes, admitting or queueing requests based on available quota.

Administrators configure `capacitySources[]` in `ClusterQueue.spec.resourceGroups` to define which DRA device dimensions are quota-managed. Each `CapacitySource` specifies dimension names, CEL selectors (which devices apply), and how to calculate the charge for a workload. When a workload requests DRA capacity, Kueue calculates its charge against the quota and admits it only if capacity is available. This enables multiple workloads to share capacity on the same device—for example, two jobs splitting a GPU's memory—unlike Counter-based allocations which reserve whole devices.

**Relationship to Partitionable Devices:** Both features use the same "charge a measured quantity, not a device count" principle. Partitionable Devices answers "how much of a *statically-sliced* device (MIG partition) does this workload use?" by reading counters. Consumable Capacity answers "how much of a *dynamically-shared* device does this workload use?" by reading device.Capacity—supporting time-slicing and MPS sharing where device capacity is advertised as fractional dimensions instead of partitions.

**The Three Moving Parts:**

1. **Driver publishes** (in ResourceSlice, per device):
   - `allowMultipleAllocations: true` — device can be shared by multiple independent claims
   - `capacity: { gpu.example.com/memory: { value: 80Gi, requestPolicy: {...} } }` — total capacity and consumption rules
   - `RequestPolicy` — rounding rules: Default (fallback if workload doesn't specify), and either ValidValues (discrete set) or ValidRange (min/max/step)

2. **User asks** (in ResourceClaimTemplate):
   - `capacity.requests: { memory: "20Gi" }` — request 20Gi of the device's memory dimension
   - ℹ️ **Dimension naming:** Capacity dimension names can be bare (`memory`, `compute`) or driver-qualified (`gpu.example.com/memory`). Use the exact name published in the ResourceSlice capacity.

3. **Kueue computes and charges:**
   - Use explicit `capacity.requests` if given; else fall back to `RequestPolicy.Default`; else the full `Capacity.Value`
   - Round the request up per `RequestPolicy` (ValidValues → smallest valid ≥ request; ValidRange with step → Min + n×Step)
   - Take the max across all matched devices, then multiply by workload count
   - Charge the result against the mapped quota resource (e.g., `gpu.memory`). Multiple workloads share that pool—that's fractional sharing.

**Kueue Version:** 1.5 (requires Kubernetes 1.36+, part of DRA beta graduation)

**Supported OCP Versions:** 4.23+ and 5.0+ once both carry Kubernetes 1.36+ | **Unsupported:** 4.22 and earlier (Kubernetes 1.35 and below)

**What Changed:** Kueue now tracks *fractional* capacity usage per dimension (not just whole devices), enforces quota exhaustion/preemption/borrowing per dimension, and exposes capacity metrics alongside traditional resource metrics.

## Test Strategy

**Upstream owns quota semantics.** Upstream Kueue tests (Unit/CEL/Integration) validate charge calculation, quota enforcement, and generic behaviors once—no downstream duplication.

**Downstream owns operator wiring.** Downstream tests validate CRD shapes, config rendering, feature-gate auto-enable, and operator lifecycle—operator-specific concerns only.

**E2E validates both through CI integration.** Rather than re-test upstream quota/preemption/exhaustion scenarios downstream, downstream CI will leverage (pull and run) upstream's 7 E2E tests (`test/e2e/dra/capacity`) to prove the full stack works through the OpenShift operator path. This is tracked in [OCPKUEUE-847](https://redhat.atlassian.net/browse/OCPKUEUE-847).

**Strategy:** Single comprehensive test suite across layers; no duplicate efforts; full end-to-end coverage through both upstream (KIND) and downstream (OCP) CI.

### Upstream Kueue (`kubernetes-sigs/kueue`)

Upstream tests comprehensively validate **generic Kueue scheduling and quota semantics** across the full test pyramid:

**Unit Tests (U1–U3):** Charge calculation, rounding logic, and heterogeneous device handling
**CEL Validation (C1–C9):** Configuration validation for capacity sources and feature-gate dependencies
**Integration Tests (I1–I10):** Quota enforcement, device fallbacks, dynamic device arrival, heterogeneous devices
**E2E Tests (E1–E7):** Full-stack end-to-end on KIND: admission, defaults, rounding, multi-device, quota exhaustion, shared capacity, missing dimensions
**Optional Phase 2 (OPT1–OPT5):** Highest-value deferred semantics and observability scenarios

**PR Status:** [#13274](https://github.com/kubernetes-sigs/kueue/pull/13274) (tests) and [#13152](https://github.com/kubernetes-sigs/kueue/pull/13152) (implementation) in review/pending

### Downstream Operator (`openshift/kueue-operator`)

Downstream tests validate **operator-specific and OpenShift-specific wiring**:

**Unit Tests (U1–U4):** Config rendering, feature-gate wiring, Counter compatibility, and source detection
**CEL Validation (C1–C7):** Downstream API rejects malformed Capacity source configurations before rendering
**Controller Integration Tests (I1–I2):** Dependency/version detection and missing-dependency reporting
**E2E / Manual Tests (E1–E9):** Operator-managed install, live ConfigMap verification, workload charging, count multiplication, inadmissibility, sharing, quota exhaustion, runtime update, and unsupported-version handling
**Optional Phase 2 (OPT1–OPT5):** Lower-priority lifecycle, metrics, and upgrade coverage
**E2E via Upstream Leverage (upstream E1–E7):** Pull upstream `test/e2e/dra/capacity` into downstream CI (OCPKUEUE-847) to prove operator-managed Kueue passes the same quota/admission/sharing tests on OCP

**PR Status:** [#2546](https://github.com/openshift/kueue-operator/pull/2546) implements downstream unit/CEL/controller tests; downstream e2e/manual validation remains tracked in OCPKUEUE-847 and this manual guide.

---

## Test Scope

### Upstream Tests (Kueue 1.5)

#### Unit Tests (Charge Calculation)

[PR #13274](https://github.com/kubernetes-sigs/kueue/pull/13274) — `pkg/dra/capacity_test.go`

| ID | Test | What It Validates |
|----|------|-------------------|
| U1 | TestRoundToValidValues | Rounding to discrete ValidValues set (e.g., 15Gi request → 16Gi valid value) |
| U2 | TestRoundToValidRange | Rounding to ValidRange with step (e.g., 15500Mi request → 16Gi with step=1Gi) |
| U3 | TestComputeCapacityCharge | Heterogeneous devices with different RequestPolicy; charge uses max across devices |

#### CEL Validation Tests

[PR #13274](https://github.com/kubernetes-sigs/kueue/pull/13274) — `pkg/config/validation_test.go` (capacity sources)

| ID | Test Case | What It Validates |
|----|-----------|-------------------|
| C1 | valid capacity source | Capacity source with bare dimension name accepted |
| C2 | valid capacity source with qualified name | Capacity source with driver-qualified name (gpu.example.com/memory) accepted |
| C3 | capacity source with malformed qualified name (more than one slash) | Rejects invalid names with multiple slashes (e.g., `a/b/c`) |
| C4 | capacity source with identifier containing hyphens | Rejects identifier with hyphens (e.g., `gpu.example.com/gpu-memory`; hyphens only allowed in subdomain) |
| C5 | capacity source with identifier containing dots | Rejects identifier with dots (e.g., `gpu.example.com/gpu.memory`; dots only allowed in subdomain) |
| C6 | capacity source with identifier starting with digit | Rejects identifier starting with digit (e.g., `gpu.example.com/123memory`; must start with letter or `_`) |
| C7 | capacity source with invalid DNS subdomain (ends with hyphen) | Rejects malformed subdomain (e.g., `gpu-.example.com/memory`; subdomain can't end with hyphen) |
| C8 | capacity source with CC gate disabled | Rejects Capacity source when feature gate is off |
| C9 | multiple capacity sources (multi-dimension) | Accepts multiple capacity dimensions per DeviceClass |

#### Integration Tests (Quota Semantics)

[PR #13274](https://github.com/kubernetes-sigs/kueue/pull/13274) — `test/integration/singlecluster/controller/dra/dra_cc_test.go`

| ID | Test | What It Validates |
|----|------|-------------------|
| I1 | Should charge explicit capacity request | Charge calculation correctly uses workload's capacity.requests |
| I2 | Should default to max capacity value when no request specified | Falls back to Capacity.Value when capacity.requests omitted |
| I3 | Should default to RequestPolicy.Default when no request specified | Falls back to RequestPolicy.Default when capacity.requests omitted |
| I4 | Should mark workload inadmissible when request exceeds ValidValues | Quota enforcement blocks oversized requests |
| I5 | Should mark workload inadmissible when no devices have capacity dimension | Quota enforcement handles missing dimension gracefully |
| I6 | Should skip device-count charge when capacity sources configured | Backward compatibility: device-count not charged when capacity source active |
| I7 | Should requeue inadmissible workload when ResourceSlice appears | Supports dynamic device arrival (e.g., hot-add GPUs) |
| I8 | Should use max Default across devices with heterogeneous Defaults | Multiple devices with different policies; uses maximum Default |
| I9 | Should use max capacity across multiple devices | Charge calculation handles device heterogeneity correctly |
| I10 | Should mark inadmissible without retry when all policies reject request | Handles edge case where no policy accepts request |

#### E2E Tests (Full Stack)

[PR #13274](https://github.com/kubernetes-sigs/kueue/pull/13274) — `test/e2e/dra/capacity/dra_test.go`

| ID | Test | What It Validates |
|----|------|-------------------|
| E1 | Should admit workload with explicit capacity request | End-to-end quota charging from workload to ClusterQueue |
| E2 | Should default to full device capacity when no request specified | Full stack: default charge calculation end-to-end |
| E3 | Should round up capacity request to ValidRange step | Full stack: rounding applied correctly end-to-end |
| E4 | Should multiply capacity charge by request count | Full stack: multi-device charge multiplication end-to-end |
| E5 | Should not admit workload when capacity charge exceeds quota | Quota exhaustion blocks admission at full stack |
| E6 | Should admit multiple workloads sharing capacity quota | Multiple workloads share capacity; quota tracking accurate |
| E7 | Should mark workload inadmissible when capacity dimension has no matching devices | Handles missing dimension gracefully at full stack |

---

### Downstream Tests (Operator)

Downstream testing focuses on the operator-owned layers: CRD/API validation, CR-to-ConfigMap rendering, feature-gate/dependency wiring, and a small OpenShift smoke path. Upstream owns the detailed scheduler/quota semantics.

OCP coverage is split by Kubernetes version. Do not schedule E1–E8 on 4.18–4.22; those clusters cannot run Consumable Capacity.

| OCP version | Kubernetes | Tests to run |
|-------------|------------|--------------|
| 4.18–4.22 | 1.35 and below (unsupported) | **E9 only** — missing-dependency / fail-closed path |
| 4.23, 5.0+ | 1.36+ (supported) | All other downstream tests (U1–U4, C1–C7, I1–I2, E1–E8) |

#### Unit Tests (Operator Logic / Config Rendering)

Locations:
- `pkg/configmap/configmap_test.go`
- `pkg/apis/kueueoperator/v1/dra_sources_test.go`

| ID | Scenario | What It Validates | Status |
|----|----------|-------------------|--------|
| U1 | Capacity source renders into `kueue-manager-config` | Downstream `type: Capacity` renders to upstream `sources[].capacity` with nested `name`, `driver`, and `deviceSelector.cel.expression` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| U2 | Capacity source enables only the consumable-capacity feature gate | `KueueDRAIntegrationConsumableCapacity` is emitted when a Capacity source is configured and the dependency is available | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| U3 | Counter source path remains independent | Existing Partitionable Devices `type: Counter` rendering and `KueueDRAIntegrationPartitionableDevices` behavior are unchanged | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| U4 | Shared DRA source detection helpers distinguish source types | `HasCapacitySources` and `HasCounterSources` return true only for their matching source type | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |

#### CEL / CRD Validation Tests

Location:
- `test/envtest/consumablecapacity/kueue_cc_cel_test.go`

| ID | Scenario | What It Validates | Status |
|----|----------|-------------------|--------|
| C1 | Valid Capacity source accepted | Generated CRD accepts `sources[].type: Capacity` with `capacity.name`, `capacity.driver`, and CEL `deviceSelector` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| C2 | Mapping without sources remains accepted | Backward compatibility for existing `deviceClassMappings` without explicit `sources` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| C3 | Invalid source type rejected | Source type values outside `Counter` and `Capacity` are rejected | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| C4 | Capacity/Counter union rules enforced | `type: Capacity` requires `capacity` and forbids `counter`; `type: Counter` requires `counter` and forbids `capacity` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| C5 | Capacity required fields and names validated | Empty `capacity.name`, invalid qualified dimension names, and invalid `capacity.driver` values are rejected | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| C6 | Device selector validation enforced | Invalid `deviceSelector.type` and empty CEL expression are rejected | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| C7 | Single source per mapping enforced | More than one source per mapping remains rejected while downstream keeps `MaxItems=1` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |

#### Controller Integration Tests

Location:
- `pkg/operator/target_config_reconciler_test.go`

| ID | Scenario | What It Validates | Status |
|----|----------|-------------------|--------|
| I1 | Kubernetes minor-version / dependency detection works | Kubernetes 1.36+ is treated as supporting default-on DRAConsumableCapacity; unsupported or unparseable versions fail closed | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| I2 | Missing DRAConsumableCapacity dependency reports clearly | Capacity source on an unsupported cluster produces the expected missing-dependency message; Counter-only and no-source configs do not | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |

#### E2E / Manual Tests (OpenShift Operator Path)

Location:
- `test/e2e/e2e_dra_consumable_capacity_test.go`

E2E scenarios (E1–E9) are deferred pending plan approval. Each will be tracked as a separate JIRA story; implementation depends on OCPKUEUE-847 (upstream e2e leverage) and subsequent Phase 2 epics. Run E1–E8 on OCP 4.23 and 5.0+. Run E9 only on OCP 4.18–4.22.

| ID | Scenario | What It Validates | Status |
|----|----------|-------------------|--------|
| E1 | Operator-managed install with Capacity source | PR operator image installs on OpenShift, accepts a Kueue CR with `type: Capacity`, and renders the expected operand ConfigMap | To be automated |
| E2 | Feature gate active in live operand config | Rendered `kueue-manager-config` contains `KueueDRAIntegrationConsumableCapacity: true` and the operand starts successfully | To be automated |
| E3 | Explicit capacity request Job admitted and charged | `dra-example-driver` publishes capacity, Job requests `memory: 20Gi`, Workload is admitted, and `resourceUsage["gpu.memory"] == 20Gi` | To be automated |
| E4 | Capacity request count multiplication | ResourceClaimTemplate uses `count: 2` with `memory: 20Gi`; Workload is admitted and charged `gpu.memory=40Gi` | To be automated |
| E5 | No matching capacity dimension or selector is inadmissible | Workload with a non-matching capacity dimension or device selector gets `QuotaReserved=False`, `Reason=Inadmissible` | To be automated |
| E6 | Multiple workloads share consumable capacity | Two Jobs each request `memory: 20Gi`; both admit and ClusterQueue reservation reaches `gpu.memory=40Gi` | To be automated |
| E7 | Capacity quota exhaustion blocks admission | ClusterQueue quota is lower than the computed capacity charge; workload remains inadmissible/pending | To be automated |
| E8 | Runtime CR update adding Capacity rolls controller | Adding Capacity config to an existing Kueue CR refreshes the ConfigMap and recovers controller readiness | To be automated |
| E9 | Unsupported cluster reports missing dependency | On OCP 4.18–4.22 (Kubernetes <1.36 / unsupported DRAConsumableCapacity), operator reports `Degraded=True`, `Reason=MissingDependencies`, with a clear message | To be automated |

---

## Out of Scope

### Intentionally Excluded Scenarios

| Scenario | Reason |
|----------|--------|
| Exhaustive invalid field combinations | Covered by downstream Capacity source validation (C1–C7); CEL schema catches most invalid states |
| Boundary variant: exact-fit quota admission | Edge case of quota exhaustion path (U2); lower priority |
| Boundary variant: single request > available quota | Covered by negative path in exhaustion (U2) |
| Capacity increase unblocking pending workloads | Lower priority; similar to quota release (U3) |
| Duplicate mappings in dimension spec | Validation detail; include only if schema requires uniqueness |
| Mixed whole-device DRA + consumable capacity in single workload | Lower priority; Counter/Capacity coexistence (U4) is higher value |
| Downstream e2e matrix: borrowing/preemption/fairsharing | These are upstream Kueue semantics (U9, U11); no need to duplicate |
| Downstream e2e: exhaustion and capacity release | Covered upstream (integration/e2e quota tests); downstream smoke test (E3) is sufficient |
| Full uninstall/reinstall cleanup with Capacity CRs | Too broad unless upgrade testing uncovers resource leaks; defer to manual ops |
| ConfigMap update timeout regression guard | Add only if this issue appears for Consumable Capacity; monitor during E2E |
| Generated CRD schema regressions | Covered by downstream CEL/API validation (C1–C7); upstream OpenAPI schema testing handles schema specifics |

---

## Target Environments

- **Disconnected**
- **FIPS**
- **ARCH:** x86_64, ARM
- **OCP Versions:** 4.23+ and 5.0+ once both carry Kubernetes 1.36+ (Kubernetes 1.36+ required; 4.22 and earlier with Kubernetes 1.35 are unsupported)
- **Hypershift:** HCP

---

## Test Deliverables

### Upstream (Kueue) Deliverables

- **PR #13274** (or successor) — Integration tests for quota charging, exhaustion, release, preemption, and coexistence
- **Upstream CI (Prow)** — The upstream e2e tests pulled into downstream should run as a periodic Prow job

### Downstream (Operator) Deliverables

- **PR #2546** (or successor) — Unit tests, CEL/envtest API validation, and controller integration tests for config rendering, feature-gate wiring, and dependency handling
- **E2E tests automated**
- **Downstream PRs with e2e tests in kueue-operator (Prow CI)**
- **Test report with information for the Docs team**

---

## Test Tasks

- [ ] **Test plan creation and review** — finalize scope, upstream/downstream ownership, and deferred optional scenarios.
- [ ] **Implement downstream unit test suite** — one story covering config rendering, feature-gate wiring, Counter compatibility, source detection, CEL/envtest validation, and controller dependency handling.
- [ ] **Automate downstream e2e tests** — create one JIRA story per e2e scenario (E1–E9) and implement in `test/e2e/e2e_dra_consumable_capacity_test.go`.
- [ ] **Remediate testing defects** — fix issues found during manual validation, e2e automation, or CI runs.
- [ ] **Automate and validate recurring Prow CI executions** — wire the upstream DRA capacity e2e coverage into downstream periodic Prow jobs and confirm stable execution.
- [ ] **Write technical documentation and code examples** — provide setup/configuration examples and test results input for the Docs team.

---

## Pass/Fail Criteria

- No critical or major defects remain open for the operator API, ConfigMap rendering, dependency handling, or downstream e2e automation.
- Unit, CEL/envtest, and controller integration tests introduced in [#2546](https://github.com/openshift/kueue-operator/pull/2546) pass consistently.
- A valid Capacity source is accepted by the downstream CRD, and invalid source type, union, required-field, and selector configurations are rejected.
- The operator renders `type: Capacity` to the expected upstream `sources[].capacity` ConfigMap shape and enables `KueueDRAIntegrationConsumableCapacity` only when the cluster dependency is available.
- Existing Counter/Partitionable Devices configuration and feature-gate behavior continue to work after Capacity support is added.
- On unsupported clusters, the operator fails closed with a clear missing-dependency condition instead of silently rendering broken Consumable Capacity configuration.
- Downstream e2e scenarios E1–E9 are automated or explicitly tracked, and the upstream DRA capacity e2e suite pulled into downstream runs successfully as a recurring periodic Prow job.
- Supporting test results and configuration examples are available for the Docs team.

---

## Risks


1. **Kubernetes 1.36 availability** — DRA Consumable Capacity is beta in K8s 1.36. Test clusters must have 1.36+ with DRA beta gate enabled.

2. **DRA scheduler plugin availability** — Tests require functional DRA scheduler; mock drivers are available but may not fully simulate production behavior.

---

## Appendix: References to Deferred Scenarios

### Deferred Upstream Scenarios (Phase 2)

**Optional Tests (OPT1–OPT5):**

| ID | Scenario | Estimated Effort | Value | Why Deferred |
|----|----------|------------------|-------|--------------|
| OPT1 | Borrowing consumable capacity across cohorts | Medium | High | Highest-value quota semantics; defer to phase 2 or bundle with hierarchical cohorts coverage |
| OPT2 | Capacity decrease below current usage safeguard | Low | Medium | Data consistency/safety edge case; validates no unsafe behavior when published capacity shrinks |
| OPT3 | Workload status/events show insufficient capacity clearly | Medium | Medium | UX/support debugging; pending workloads should explain capacity exhaustion clearly |
| OPT4 | Metrics expose capacity reservation/usage | Medium | Medium | Observability/support; tracked separately from quota enforcement |
| OPT5 | Multiple capacity dimensions accounted independently | Medium | High | Multi-dimension behavior; validates dimensions such as `memory` and `compute` do not interfere |

### Deferred Downstream Scenarios (Phase 2)

| ID | Scenario | Estimated Effort | Value | Why Deferred | Status |
|----|----------|------------------|-------|--------------|--------|
| OPT1 | Operator renders Counter + Capacity sources together | Low | Medium | Useful but lower priority than single-source handling | Still needs implementation if requested |
| OPT2 | Runtime CR update removing Capacity config triggers cleanup | Medium | Medium | Important lifecycle but lower priority than initial enable | Still needs implementation |
| OPT3 | Downstream TLS metrics exposes capacity metrics via OpenShift auth | Medium | Medium | Observability for operators; important but not blocking | Still needs implementation |
| OPT4 | Upgrade from non-Capacity version preserves Counter config | Medium | High | Backward compatibility; can be validated post-GA | Still needs implementation |
| OPT5 | Upgrade, then enable Capacity; config renders + operand starts | Medium | High | Phased adoption scenario; lower immediate priority | Still needs implementation |
