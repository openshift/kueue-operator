# Test Plan for DRA Consumable Capacity

**Plan Status:** Draft
**Feature:** DRA Consumable Capacity support in the Kueue Operator
**Testing Epic:** [OCPKUEUE-812](https://redhat.atlassian.net/browse/OCPKUEUE-812)
**API Design / Implementation Story:** [OCPKUEUE-813](https://redhat.atlassian.net/browse/OCPKUEUE-813)
**Research Spike:** [OCPKUEUE-826](https://redhat.atlassian.net/browse/OCPKUEUE-826)

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
| Testing Epic | [OCPKUEUE-812](https://redhat.atlassian.net/browse/OCPKUEUE-812) — Consumable Capacity downstream testing |
| API Design / Implementation Story | [OCPKUEUE-813](https://redhat.atlassian.net/browse/OCPKUEUE-813) — Design the Kueue operator API for consumable capacity |
| Research Spike | [OCPKUEUE-826](https://redhat.atlassian.net/browse/OCPKUEUE-826) — Consumable Capacity research |
| Kueue KEP | [KEP-2941: Dynamic Resource Allocation in Kueue](https://github.com/kubernetes-sigs/kueue/tree/main/keps/2941-DRA) |
| Kubernetes KEP | [KEP-5075: DRA Consumable Capacity](https://github.com/kubernetes/enhancements/tree/master/keps/sig-scheduling/5075-dra-consumable-capacity) |
| Upstream issue | [kubernetes-sigs/kueue#11726](https://github.com/kubernetes-sigs/kueue/issues/11726) — Consumable Capacity support |
| Upstream e2e tests | [`test/e2e/dra/capacity/dra_test.go`](https://github.com/kubernetes-sigs/kueue/blob/main/test/e2e/dra/capacity/dra_test.go) |
| Upstream integration tests | [`test/integration/singlecluster/controller/dra/dra_cc_test.go`](https://github.com/kubernetes-sigs/kueue/blob/main/test/integration/singlecluster/controller/dra/dra_cc_test.go) |
| Upstream config validation | [`pkg/config/validation_test.go`](https://github.com/kubernetes-sigs/kueue/blob/main/pkg/config/validation_test.go) |
| Upstream DRA docs | [Dynamic Resource Allocation](https://kueue.sigs.k8s.io/docs/concepts/dynamic_resource_allocation/), [Set up DRA](https://kueue.sigs.k8s.io/docs/tasks/manage/setup_dra/), [Run workloads with DRA](https://kueue.sigs.k8s.io/docs/tasks/run/dra/) |
| Downstream precedent | [openshift/kueue-operator#1978](https://github.com/openshift/kueue-operator/pull/1978) — Partitionable Devices API support |
| Downstream implementation PR | [openshift/kueue-operator#2546](https://github.com/openshift/kueue-operator/pull/2546) — Add DRA consumable capacity source support |
| Known bugs | None known at time of writing |

## Introduction

DRA Consumable Capacity lets Kueue account quota by a consumable device capacity dimension instead of by whole-device count. This is intended for DRA drivers that publish `ResourceSlice` devices with `allowMultipleAllocations: true` and a `capacity` entry such as GPU memory or compute. A workload can request a portion of that capacity through `ResourceClaimTemplate.spec.spec.devices.requests[].capacity.requests`, and Kueue charges the corresponding ClusterQueue resource by the resolved capacity request.

Upstream Kueue added this behavior in v0.19 behind the `KueueDRAIntegrationConsumableCapacity` feature gate. The downstream operator change in [#2546](https://github.com/openshift/kueue-operator/pull/2546) exposes this through the existing operator API path, `spec.config.resources.deviceClassMappings[].sources[]`, by adding a new `type: Capacity` source variant parallel to the existing `type: Counter` source used for Partitionable Devices.

The operator's responsibility is to validate the downstream `Kueue` CR shape, render the requested capacity source into the operand `kueue-manager-config`, enable the Kueue consumable-capacity feature gate only when the Kubernetes dependency is available, and report a clear missing-dependency condition when a user configures Capacity on an unsupported cluster.

## Test Strategy

- **Upstream:** Kueue owns the scheduler and DRA accounting semantics. Upstream e2e and integration tests cover capacity request resolution, `RequestPolicy` defaults and rounding, quota charging, quota exhaustion, ResourceSlice behavior, and feature-gate validation. Downstream should rely on these tests for the detailed scheduler matrix rather than duplicating it.
- **Downstream:** The Kueue Operator owns the downstream CRD/API, CR-to-ConfigMap translation, feature-gate wiring, missing-dependency reporting, generated CRD/bundle schema, and OpenShift-specific installation path. Downstream tests focus on those layers: unit tests, envtest CRD validation, and a small live e2e proposal to prove the operator-managed path works on OpenShift.

## Test Scope

### Upstream Tests

Upstream coverage is the source of truth for Kueue's generic Consumable Capacity behavior.

| ID | Scenario | What It Validates |
|----|----------|-------------------|
| T1 | Explicit capacity request | Workload with an explicit capacity request is charged that requested capacity rather than whole-device count |
| T2 | Default capacity charge | Missing `capacity.requests` uses `RequestPolicy.Default` or the device capacity value as defined by upstream behavior |
| T3 | ValidRange rounding | Fractional requests are rounded up according to the device `RequestPolicy.ValidRange.step` |
| T4 | Request count multiplication | Charge is multiplied by the DRA request `count` |
| T5 | Capacity quota exhaustion | Workload remains pending/inadmissible when the computed capacity charge exceeds ClusterQueue quota |
| T6 | Multiple workloads share a capacity pool | Several workloads can be admitted against the same capacity pool until quota is exhausted |
| T7 | No matching devices | A workload is marked inadmissible when the capacity source selector matches no usable devices |
| T8 | Advanced RequestPolicy and ResourceSlice behavior | Integration tests cover valid values, heterogeneous devices/defaults, rejected policies, ResourceSlice appearance, and conservative max-charge behavior |
| T9 | Upstream config validation | Upstream rejects capacity sources when `KueueDRAIntegrationConsumableCapacity` is not enabled and validates feature-gate dependencies |

### Downstream Tests

Downstream coverage is split by level: unit tests for operator logic, envtest for generated CRD validation, and proposed e2e/manual tests for operator-managed OpenShift behavior.

#### Unit Tests

| ID | Scenario | What It Validates | Location | Status |
|----|----------|-------------------|----------|--------|
| U1 | Capacity source renders into the operand ConfigMap | Operator `type: Capacity` renders to upstream `sources[].capacity` with nested `name`, `driver`, and `deviceSelector.cel.expression`; `KueueDRAIntegrationConsumableCapacity` is emitted when the dependency is available | `pkg/configmap/configmap_test.go` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| U2 | DRA source feature-gate separation | `Capacity` sources enable only `KueueDRAIntegrationConsumableCapacity`; `Counter` sources enable only `KueueDRAIntegrationPartitionableDevices`; mappings without sources enable neither | `pkg/configmap/configmap_test.go` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| U3 | Shared DRA source detection helpers | `HasCapacitySources` and `HasCounterSources` identify only their matching source type and return false for mappings without sources | `pkg/apis/kueueoperator/v1/types_test.go` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| U4 | Kubernetes minor-version detection | `isKubernetesMinorAtLeast` treats Kubernetes 1.36+ as supporting default-on Consumable Capacity and fails closed on unparseable minor strings | `pkg/operator/target_config_reconciler_test.go` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| U5 | Missing Consumable Capacity dependency | Capacity source with `DRAConsumableCapacity` unavailable returns the clear missing-dependency message; Capacity with dependency available, Counter-only, and no-source configs do not | `pkg/operator/target_config_reconciler_test.go` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |

#### CEL / CRD Validation Tests

| ID | Scenario | What It Validates | Location | Status |
|----|----------|-------------------|----------|--------|
| C1 | Valid Capacity source accepted | A valid `type: Capacity` source is accepted by the generated Kueue CRD | `test/envtest/consumablecapacity/kueue_cc_cel_test.go` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| C2 | Mapping without sources accepted | Existing `deviceClassMappings` without `sources` remain valid for backward compatibility | `test/envtest/consumablecapacity/kueue_cc_cel_test.go` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| C3 | Invalid source type rejected | Source type values outside `Counter` and `Capacity` are rejected | `test/envtest/consumablecapacity/kueue_cc_cel_test.go` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| C4 | Capacity union rules enforced | `type: Capacity` without `capacity`, `type: Capacity` with `counter`, and `type: Counter` with `capacity` are rejected | `test/envtest/consumablecapacity/kueue_cc_cel_test.go` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| C5 | Capacity required fields validated | Empty `capacity.name` and invalid `capacity.driver` are rejected | `test/envtest/consumablecapacity/kueue_cc_cel_test.go` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| C6 | Device selector validation | Invalid `deviceSelector.type` and empty CEL expression are rejected | `test/envtest/consumablecapacity/kueue_cc_cel_test.go` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |
| C7 | Single source per mapping | More than one source remains rejected while downstream keeps `MaxItems=1` | `test/envtest/consumablecapacity/kueue_cc_cel_test.go` | Implemented in [#2546](https://github.com/openshift/kueue-operator/pull/2546) |

#### E2E Tests

| ID | Scenario | What It Validates | Status |
|----|----------|-------------------|--------|
| E1 | Operator-managed positive smoke with `dra-example-driver` | On a cluster where Kubernetes `DRAConsumableCapacity` is available, a `Kueue` CR with `type: Capacity` renders `KueueDRAIntegrationConsumableCapacity: true`; a simple Job with `capacity.requests.memory: 20Gi` is admitted and charged against the mapped quota resource (for example `gpu.memory`) rather than device count | Proposed |
| E2 | Unsupported-cluster negative smoke | On OCP 4.22 / Kubernetes 1.35 where `DRAConsumableCapacity` is unavailable, a valid Capacity source CR is accepted but the operator reports `Degraded=True`, `Reason=MissingDependencies`, and the ConfigMap does not enable `KueueDRAIntegrationConsumableCapacity` | Proposed |
| E3 | Runtime config update smoke | Adding a Capacity source to the `Kueue` CR triggers operator reconciliation, ConfigMap refresh, controller rollout, and readiness recovery | Proposed |

#### Manual / Exploratory Tests

| ID | Scenario | What It Validates | Status |
|----|----------|-------------------|--------|
| M1 | OCP 4.22 FeatureGate exploration | Confirms whether `TechPreviewNoUpgrade` exposes `DRAConsumableCapacity`. Initial validation on 4.22 nightly showed `DRAPartitionableDevices` enabled under TechPreview but no `DRAConsumableCapacity` entry in FeatureGate status | Done manually during planning |
| M2 | Full upstream-style Consumable Capacity flow | Use `dra-example-driver` with `gpuAllowMultipleAllocations=true` to run the upstream seven-case behavior manually through the operator CR once a suitable Kubernetes 1.36+ or explicitly-enabled cluster is available | Proposed |

## Out of Scope

- Detailed capacity charge calculation, `RequestPolicy` rounding, heterogeneous devices, ResourceSlice dynamics, and quota exhaustion matrix — covered upstream by `test/e2e/dra/capacity` and `test/integration/singlecluster/controller/dra/dra_cc_test.go`.
- Downstream preemption, cohorts, FairSharing, MultiKueue, and workload-type matrix for Consumable Capacity — generic Kueue scheduler behavior; add upstream coverage if gaps are found.
- Real NVIDIA GPU Operator time-slicing or MPS validation — valuable platform integration coverage, but not required for the initial operator API PR. The first downstream e2e can use `dra-example-driver` to avoid hardware dependency.
- Mixed MIG/Partitionable Devices plus Consumable Capacity on real GPUs — deferred until vendor-driver support and product scope are confirmed.
- Upgrade/downgrade with active Consumable Capacity workloads — deferred unless the release requires explicit upgrade coverage for this alpha path.

### Scenarios considered and excluded

| Scenario | Reason |
|----------|--------|
| Exhaustive invalid CRD field combinations | Covered by representative envtest validation cases C3-C7 |
| Multiple Capacity sources per mapping | Downstream currently keeps `MaxItems=1`; C7 validates rejection |
| Counter and Capacity in the same mapping | Rejected by `MaxItems=1`; mixed-source scheduling behavior is out of scope until the API allows it |
| Downstream duplicate of all seven upstream e2e cases | Duplicates upstream scheduler coverage; downstream keeps only a positive operator-managed smoke proposal |
| ConfigMap update timeout regression guard | Add only if Consumable Capacity reveals a ConfigMap propagation regression |

## Target Environments

- OCP versions:
  - OCP 4.21 and below: negative/unsupported behavior only if DRA APIs or `DRAConsumableCapacity` are unavailable.
  - OCP 4.22 / Kubernetes 1.35: negative/unsupported behavior expected unless `DRAConsumableCapacity` is explicitly available. TechPreview validation on a 4.22 nightly did not show `DRAConsumableCapacity` enabled.
  - Kubernetes 1.36+ / future OCP release carrying Kubernetes 1.36+: positive smoke target because `DRAConsumableCapacity` is beta/default-on upstream.
- Architecture: x86_64 primary; ARM smoke only if suitable DRA test environment is available.
- Disconnected: no Consumable Capacity-specific network dependency beyond images used for e2e; use mirrored `dra-example-driver` images if e2e is enabled.
- FIPS: no feature-specific cryptographic path; standard operator FIPS validation is sufficient.
- Hypershift: no special CC behavior identified; e2e can be considered after base operator-managed DRA support is stable.
- Feature-specific requirements: Kubernetes DRA `resource.k8s.io/v1` APIs, ResourceSlice support, and a DRA driver that publishes devices with `allowMultipleAllocations` and capacity `RequestPolicy` data.

## Test Deliverables

- Downstream implementation PR [#2546](https://github.com/openshift/kueue-operator/pull/2546) with unit and envtest coverage for the operator API and ConfigMap rendering.
- Proposed downstream e2e PR for E1/E2/E3 once a suitable positive Consumable Capacity test environment is available.
- Manual validation notes for OCP 4.22 FeatureGate behavior and any future positive cluster validation.
- Documentation input for configuring `spec.config.resources.deviceClassMappings[].sources[].capacity` and the unsupported-cluster condition message.

## Test Tasks

- Create and review this test plan.
- Implement operator unit tests for Capacity ConfigMap rendering, feature-gate separation, source detection, Kubernetes minor detection, and missing-dependency logic.
- Implement envtest CRD validation for the `type: Capacity` API shape.
- Validate the operator behavior on OCP 4.22 where `DRAConsumableCapacity` is unavailable.
- Identify or provision a Kubernetes 1.36+ / suitable OCP environment for positive e2e validation with `dra-example-driver`.
- Implement the downstream e2e smoke proposal once the positive environment is available.
- Track bugs or follow-up tasks found during manual and e2e validation.

## Pass/Fail Criteria

- No critical or major defects remain open for the operator API, ConfigMap rendering, or missing-dependency behavior.
- Unit tests and envtest validation introduced for [#2546](https://github.com/openshift/kueue-operator/pull/2546) pass consistently.
- A valid Capacity source is accepted by the downstream CRD and invalid union/required-field combinations are rejected.
- On unsupported clusters, the operator does not silently render a broken CC feature-gate configuration and reports a clear missing dependency.
- On supported clusters, the proposed e2e smoke verifies the operator-managed path from Kueue CR to capacity-charged workload admission.

## Risks

| Risk | Impact |
|------|--------|
| OCP 4.22 / Kubernetes 1.35 does not expose `DRAConsumableCapacity` through `TechPreviewNoUpgrade` | Positive downstream e2e may require a Kubernetes 1.36+ or custom-enabled cluster rather than current OCP 4.22 |
| `dra-example-driver` image or SCC requirements differ across OCP versions | E2E setup may need OpenShift-specific SCC/image mirroring steps and could be unsuitable for all CI lanes |
| Consumable Capacity is upstream alpha in Kueue v0.19 | API/feature-gate details may change; test plan and operator API may need revision as upstream graduates the feature |
| Real vendor GPU sharing support may differ from `dra-example-driver` | Synthetic e2e proves operator/Kueue wiring but not NVIDIA time-slicing or MPS product behavior |
| DRA feature-gate detection differs between upstream Kubernetes and OpenShift FeatureGate status | Unsupported-cluster behavior must fail closed with a clear condition rather than assuming support |
