# Release Kueue 1.4 Testing Plan

## 1. Introduction

### 1.1. Overview

This document outlines the testing strategy, objectives, and procedures for the
Kueue 1.4 release. The goal is to ensure the quality, stability, and performance
of new features and to verify that existing functionality remains regression-free.

### 1.2. Scope

**In Scope**

- [Kueue Operator] Central TLS Profile consistency -
  [OCPKUEUE-418](https://redhat.atlassian.net/browse/OCPKUEUE-418)
- DRA Attribute-Based GPU with RHBOK (upstream work in 4.21) -
  [OCPSTRAT-2380](https://redhat.atlassian.net/browse/OCPSTRAT-2380)
- [GA] Admission Fair Sharing (Kueue) Integration for Multi-Tenant Resource
  Fairness - [OCPSTRAT-2588](https://redhat.atlassian.net/browse/OCPSTRAT-2588)

**Out of Scope**

- Spark Operator Integration - PR: kubernetes-sigs/kueue#7268
- Elastic Job Support for Ray in Kueue - kubernetes-sigs/kueue#8651
- RHBoK (Kueue) - integration with autoscaler to scale/shrink nodes via
  ProvisioningRequest (Dev Preview) -
  [OCPSTRAT-2105](https://redhat.atlassian.net/browse/OCPSTRAT-2105)

### 1.3. Key Features

- TLS
- Kueue + DRA
- Admission Fair Sharing

## 2. Release Testing Strategy

### 2.1. Schedule

| Milestone | Date / Sprint | Notes |
|-----------|---------------|-------|
| Branching | Sprint 288 | Release branch created |
| Testing Start | Sprint 289 | Environments provisioned |
| Week 1: Core Testing | Sprint 289 | Regression, features, DAST, bugs |
| Week 2: Upgrade | Sprint 289 | OCP + Kueue upgrade matrix |
| Release Work | Sprint 290 | Artifacts, sign-off |
| Release | Sprint 290 | Published |

### 2.2. Test Types

- **Integration Tests:** Verify interactions between Kueue components and
  Kubernetes
- **Feature Tests:** Ensure new/modified features match technical requirements
- **Regression Tests:** Ensure existing functionality is not broken
- **Bug Verification:** Ensure all tracked bugs are fixed
- **Upgrade Tests:** Ensure upgrade works from the previous version
- **Security Tests (DAST):** Identify and mitigate vulnerabilities (RapidDAST)

### 2.3. Test Environments

- **CI/CD:** Prow Periodics
- **Staging Clusters:**
  - OCP: 4.18, 4.19, 4.20, 4.21, 4.22
  - FIPS
  - Disconnected
  - Multi-ARCH (x86_64, arm64)
  - Hypershift on AWS

## 3. Test Areas & Test Cases

**Tracking Epic:** Kueue 1.4 Release Testing -
[OCPKUEUE-667](https://redhat.atlassian.net/browse/OCPKUEUE-667)

### 3.1. Key Test Activities

| Activity | JIRA | Cadence | Est. Duration |
|----------|------|---------|---------------|
| Upgrade testing | [OCPKUEUE-668](https://redhat.atlassian.net/browse/OCPKUEUE-668) | Re-execute on rebuilds | ~3 days |
| RapidDAST | [OCPKUEUE-669](https://redhat.atlassian.net/browse/OCPKUEUE-669) | Once unless API changes | ~2 days |
| Regression & New Features Testing | [OCPKUEUE-670](https://redhat.atlassian.net/browse/OCPKUEUE-670) | Daily on new builds | ~1-1.5 days |
| Bug verification | [OCPKUEUE-671](https://redhat.atlassian.net/browse/OCPKUEUE-671) | On new builds as needed | ~2 days |

### 3.2. Test Cases

Test case documentation is maintained in [`test/docs/cases/`](../cases/) with a
1:1 mapping to the e2e test files in [`test/e2e/`](../../e2e/). Each test case
doc includes manual steps, prerequisites, and a link back to the automated Go
test file.

| Test Case | Doc | Test File |
|-----------|-----|-----------|
| Admission Fair Sharing | [e2e_admission_fair_sharing.md](../cases/e2e_admission_fair_sharing.md) | [e2e_admission_fair_sharing_test.go](../../e2e/e2e_admission_fair_sharing_test.go) |
| DRA | TBD | [e2e_dra_test.go](../../e2e/e2e_dra_test.go) |
| DRA Extended Resources | TBD | [e2e_dra_extended_resources_test.go](../../e2e/e2e_dra_extended_resources_test.go) |
| Gang Scheduling | TBD | [e2e_gangscheduling_test.go](../../e2e/e2e_gangscheduling_test.go) |
| Local Queue Defaulting | TBD | [e2e_local_queue_defaulting_test.go](../../e2e/e2e_local_queue_defaulting_test.go) |
| Managed Jobs Namespace Selector | TBD | [e2e_managed_jobs_namespace_selector_test.go](../../e2e/e2e_managed_jobs_namespace_selector_test.go) |
| Metrics | TBD | [e2e_metrics_test.go](../../e2e/e2e_metrics_test.go) |
| Operator | TBD | [e2e_operator_test.go](../../e2e/e2e_operator_test.go) |
| Preemption | TBD | [e2e_preemption_test.go](../../e2e/e2e_preemption_test.go) |
| Scheduling Gate | TBD | [e2e_scheduling_gate_test.go](../../e2e/e2e_scheduling_gate_test.go) |
| TLS Profile | [e2e_tls_profile.md](../cases/e2e_tls_profile.md) | [e2e_tls_profile_test.go](../../e2e/e2e_tls_profile_test.go) |
| Visibility On Demand | [e2e_visibility_on_demand.md](../cases/e2e_visibility_on_demand.md) | [e2e_visibility_on_demand_test.go](../../e2e/e2e_visibility_on_demand_test.go) |

## 4. Test Details

### 4.1. Feature Test Execution

Per-feature testing workflow:

1. **Spike** — Deep dive into requirements/design to understand scope and
   testing areas
2. **Test Scenario Creation** — Comprehensive test cases covering functional,
   non-functional, and edge cases
3. **Manual Test Execution** — Validate core functionality, ensure basic
   stability, open bugs and enhancements
4. **Test Automation** — Implement automated tests for critical paths and
   regression coverage

### 4.2. Release Test Execution

**CI Periodic Jobs:**

- periodic-ci-openshift-kueue-operator-release-1.4-test-e2e-4-18
- periodic-ci-openshift-kueue-operator-release-1.4-test-e2e-4-19
- periodic-ci-openshift-kueue-operator-release-1.4-test-e2e-4-20
- periodic-ci-openshift-kueue-operator-release-1.4-test-e2e-4-21
- periodic-ci-openshift-kueue-operator-release-1.4-test-e2e-4-22
- periodic-ci-openshift-kueue-operator-release-1.4-test-e2e-4-20-disconnected
- periodic-ci-openshift-kueue-operator-release-1.4-test-e2e-4-20-fips
- periodic-ci-openshift-kueue-operator-release-1.4-test-e2e-4-20-arm64
- periodic-ci-openshift-kueue-operator-release-1.4-test-e2e-4-20-hypershift

**Upgrade Jobs:**

- Upgrade-from-4.18-e2e-upgrade-4-18-to-4-19-kueue-1-4
- Upgrade-from-4.19-e2e-upgrade-4-19-to-4-20-kueue-1-4
- Upgrade-from-4.20-e2e-upgrade-4-20-to-4-21-kueue-1-4
- Upgrade-from-4.20-e2e-upgrade-4-21-to-4-22-kueue-1-4
- Upgrade-from-Kueue-1.3-to-Kueue-1.4 (does not exist yet)

**Manual Testing:**

- UI verification (2 OCP versions)

### 4.3. Upgrade Plan

**OCP Upgrade:** latest version of Kueue is installed and OCP version is
upgraded.

- OCP 4.18 Kueue 1.4 -> OCP 4.19
- OCP 4.19 Kueue 1.4 -> OCP 4.20
- OCP 4.20 Kueue 1.4 -> OCP 4.21
- OCP 4.21 Kueue 1.4 -> OCP 4.22

**Kueue Upgrade** (seamless upgrade not supported):

- **Operator-Only Uninstall:** Verify resources (ResourceFlavors, ClusterQueues,
  LocalQueues) are preserved after operator removal.
- **Full Operator + Operand Uninstall:** Verify complete removal works cleanly.
  For now resources are still kept (same behavior as Operator-only uninstall).
- Both tests executed on: OCP 4.22 Kueue 1.3 -> Kueue 1.4

### 4.4. Security Scanning (DAST APIs)

Depending on Kueue upstream version we're adopting, v1beta1 may or may not be
supported.

| API Group | Version | Notes |
|-----------|---------|-------|
| kueue.openshift.io | v1 | |
| kueue.x-k8s.io | v1beta1 | May be removed |
| kueue.x-k8s.io | v1beta2 | |
| visibility.kueue.x-k8s.io | v1beta1 | May be removed |
| visibility.kueue.x-k8s.io | v1beta2 | |

Results uploaded to ProdSec folder.

## 5. Bug Tracking

### 5.1. Bugs to Be Tested

- TBD (label: `Kueue-1.4`)

### 5.2. Bugs Found During Release Testing (Out of Scope)

- TBD

## 6. Risks

| Risk | JIRA | Impact | Mitigation |
|------|------|--------|------------|
| Upgrade tests not fully automated | [OCPKUEUE-427](https://redhat.atlassian.net/browse/OCPKUEUE-427) | Manual effort during testing | Prioritize automation before testing starts |
| Kueue + DRA integration uncertainty | [OCPKUEUE-573](https://redhat.atlassian.net/browse/OCPKUEUE-573) | Feature may not be testable | Early spike, fallback to partial coverage |

## 7. Exit Criteria

The Kueue 1.4 release will be considered ready when:

- All in-scope features are verified
- No open release-blocking regressions
- All release test activities (upgrade, security, regression, bug verification)
  are complete
- All identified risks are resolved or accepted
