# Test Plan for Per-namespace LocalQueue Defaulting Configuration

**Plan Status:** Draft
**Feature:** [OCPSTRAT-3302](https://redhat.atlassian.net/browse/OCPSTRAT-3302) — RHBoK: Promote LocalQueueDefaulting to per-namespace configuration in the Kueue Configuration API
**Testing Epic:** [OCPKUEUE-786](https://redhat.atlassian.net/browse/OCPKUEUE-786)

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
| Feature | [OCPSTRAT-3302](https://redhat.atlassian.net/browse/OCPSTRAT-3302) |
| Dev Epic | [OCPKUEUE-732](https://redhat.atlassian.net/browse/OCPKUEUE-732) |
| Testing Epic | [OCPKUEUE-786](https://redhat.atlassian.net/browse/OCPKUEUE-786) |
| KEP | [KEP-11520](https://github.com/kubernetes-sigs/kueue/pull/13782) |
| Upstream Implementation PR | [#13783](https://github.com/kubernetes-sigs/kueue/pull/13783) |
| Upstream Issue | [#11520](https://github.com/kubernetes-sigs/kueue/issues/11520) |
| Related Bug Fix | [#13375](https://github.com/kubernetes-sigs/kueue/pull/13375) — Defaulting webhook respects managedJobsNamespaceSelector |
| Upstream Tests | [OCPKUEUE-787](https://redhat.atlassian.net/browse/OCPKUEUE-787) |
| Downstream Tests | [OCPKUEUE-788](https://redhat.atlassian.net/browse/OCPKUEUE-788) |

## Introduction

LocalQueue defaulting automatically assigns `kueue.x-k8s.io/queue-name: default` to workloads submitted without a queue-name label, whenever a LocalQueue named `default` exists in a managed namespace. Previously, there was no way to control this behavior per namespace. Administrators who wanted Kueue to manage workloads with explicit queue names in a namespace but not auto-default unlabeled workloads had no option.

This feature adds a `localQueueDefaultingNamespaceSelector` field to the Kueue Configuration API, gated behind the `LocalQueueDefaultingPerNamespace` feature gate (Beta, on by default in v0.20). When configured, only workloads in namespaces matching this selector will have the default queue label injected. When the selector is nil, existing behavior is preserved.

The feature applies to all integrations. Most integrations go through `ApplyDefaultLocalQueue`, but the Pod webhook has its own inline defaulting logic with the same selector check applied.

## Test Strategy

- **Upstream:** Unit tests cover `ApplyDefaultLocalQueue` and Pod webhook defaulting scenarios. E2e tests cover the full flow with `localQueueDefaultingNamespaceSelector` configured, testing both Job and Pod workloads across managed, unmanaged, and non-defaulting namespaces. Integration tests are not feasible because the webhook test suite's `managerSetup` creates its own queue manager internally, preventing pre-population with a `default` LocalQueue.
- **Downstream:** Everything is tested upstream. Downstream carries upstream e2e tests and only adds tests for operator-specific configuration rendering.

## Test Scope

### Upstream Tests

#### Unit Tests

| ID | Scenario | What It Validates |
|----|----------|-------------------|
| U1 | Managed namespace with defaulting label gets default queue label | Selector match allows defaulting |
| U2 | Managed namespace without defaulting label does not get default queue label | Selector mismatch blocks defaulting |
| U3 | Unmanaged namespace with defaulting label does not get default queue label | managedJobsNamespaceSelector blocks before our selector |
| U4 | Feature gate disabled ignores the selector | Backward compatibility |
| U5 | Configuration validation: valid selector | Selector syntax accepted |
| U6 | Configuration validation: nil selector | Nil selector accepted |
| U7 | Configuration validation: selector matching prohibited namespace | Selector rejected |
| U8 | Pod webhook: managed namespace with defaulting label gets default queue label | Pod webhook inline defaulting respects selector |
| U9 | Pod webhook: managed namespace without defaulting label does not get default queue label | Pod webhook selector mismatch blocks defaulting |
| U10 | Pod webhook: feature gate disabled ignores the selector | Pod webhook backward compatibility |

#### E2e Tests (Baseline)

| ID | Scenario | What It Validates |
|----|----------|-------------------|
| E1 | Job in managed namespace with defaulting label gets default queue label and is admitted | Full defaulting and admission flow |
| E2 | Job in managed namespace without defaulting label does not get default queue label | Selector blocks defaulting for Jobs |
| E3 | Job in unmanaged namespace does not get default queue label | managedJobsNamespaceSelector interaction |
| E4 | Pod in managed namespace with defaulting label gets default queue label | Pod webhook defaulting works |
| E5 | Pod in managed namespace without defaulting label does not get default queue label | Pod webhook selector blocks defaulting |
| E6 | Job with feature gate disabled and selector configured gets default queue label | Backward compatibility end-to-end |

#### E2e Tests (Extended)

| ID | Scenario | What It Validates |
|----|----------|-------------------|
| X1 | Deployment in managed namespace with defaulting label gets default queue label | Defaulting works for Deployment integration |
| X2 | Deployment in managed namespace without defaulting label does not get default queue label | Selector blocks defaulting for Deployments |
| X3 | StatefulSet in managed namespace with defaulting label gets default queue label | Defaulting works for StatefulSet integration |
| X4 | StatefulSet in managed namespace without defaulting label does not get default queue label | Selector blocks defaulting for StatefulSets |
| X5 | LeaderWorkerSet in managed namespace with defaulting label gets default queue label | Defaulting works for LWS integration |
| X6 | LeaderWorkerSet in managed namespace without defaulting label does not get default queue label | Selector blocks defaulting for LWS |
| X7 | MatchExpressions selector with In operator | Selector formats beyond matchLabels work |
| X8 | Add defaulting label to namespace, submit new job | Defaulting activates for new workloads after label is added |
| X9 | Remove defaulting label from namespace, submit new job | Defaulting stops for new workloads after label is removed |

### Downstream Tests

| ID | Scenario | Why downstream-specific |
|----|----------|------------------------|
| D1 | Operator renders localQueueDefaultingNamespaceSelector into controller manager config | Operator-specific config rendering |
| D2 | Carry upstream e2e tests on OpenShift | Ensure upstream tests work downstream |

## Out of Scope

- Performance testing of namespace label lookups in the webhook path
- Testing with kueue-populator (independent feature)
- Multi-cluster / MultiKueue interaction with the selector

### Scenarios considered and excluded

| Scenario | Reason |
|----------|--------|
| Multiple ClusterQueues with different default LocalQueues | The selector controls whether defaulting happens, not which queue is selected. Orthogonal to this feature. |

## Target Environments

- OCP versions: 4.19+
- Architectures: x86_64, ARM
- FIPS: Configuration API change, no crypto impact
- Disconnected: No external dependencies
- Hypershift / HCP: Standard Kueue deployment

## Test Deliverables

- Upstream PR with unit and e2e tests in `kubernetes-sigs/kueue` ([#13783](https://github.com/kubernetes-sigs/kueue/pull/13783))
- Downstream PR with operator tests in `openshift/kueue-operator`
- This test plan document

## Test Tasks

1. Upstream unit and e2e test automation ([OCPKUEUE-787](https://redhat.atlassian.net/browse/OCPKUEUE-787))
2. Downstream e2e test automation ([OCPKUEUE-788](https://redhat.atlassian.net/browse/OCPKUEUE-788))
3. Test plan creation and review ([OCPKUEUE-786](https://redhat.atlassian.net/browse/OCPKUEUE-786))

## Pass/Fail Criteria

- All upstream unit tests pass (U1-U10)
- All upstream baseline e2e tests pass (E1-E6)
- All downstream tests pass (D1-D2)
- No critical or major defects remain open
- Upstream and downstream CI jobs pass consistently
- Feature gate disabled behavior is unchanged from prior release

## Risks

| Risk | Impact |
|------|--------|
| Pod webhook has separate inline defaulting logic from ApplyDefaultLocalQueue | If the two paths diverge, workloads of different types would see inconsistent defaulting behavior. Mitigated by unit tests covering both paths identically. |
| Integration tests not feasible due to webhook test infrastructure limitations | Gap in test coverage between unit and e2e. Mitigated by comprehensive e2e tests. |
