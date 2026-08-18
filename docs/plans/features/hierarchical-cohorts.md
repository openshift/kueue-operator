# Test Plan for Hierarchical Cohorts

**Plan Status:** Draft
**Feature:** OCPSTRAT-3444 — RHBoK (Kueue): Enable and Support Cohorts
**Testing Epic:** [OCPKUEUE-739](https://redhat.atlassian.net/browse/OCPKUEUE-739)
**Test Plan Story:** [OCPKUEUE-749](https://redhat.atlassian.net/browse/OCPKUEUE-749)

## Overview

- [References](#references) — KEPs, JIRA tickets, docs, and known bugs
- [Introduction](#introduction) — What Hierarchical Cohorts are and what changed
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
| Testing Epic | [OCPKUEUE-739](https://redhat.atlassian.net/browse/OCPKUEUE-739) — Testing Hierarchical Cohorts in Kueue Operator |
| KEP-79 | https://github.com/kubernetes-sigs/kueue/tree/main/keps/79-hierarchical-cohorts |
| Upstream tests | [OCPKUEUE-726](https://redhat.atlassian.net/browse/OCPKUEUE-726) — Upstream Hierarchical Cohort Tests for Kueue |
| Upstream docs | https://kueue.sigs.k8s.io/docs/concepts/cohort/ |
| Downstream tests | [OCPKUEUE-727](https://redhat.atlassian.net/browse/OCPKUEUE-727) — Downstream Hierarchical Cohort Tests for RHBoK |
| Downstream docs | TBD |
| Bugs | [OCPBUGS-99316](https://redhat.atlassian.net/browse/OCPBUGS-99316) — kueue-operator drops `vcohort.kb.io` webhook |

## Introduction

Hierarchical Cohorts introduce a first-class `Cohort` CRD (`kueue.x-k8s.io/v1beta2`, kind: `Cohort`) that allows platform administrators to model multi-level organizational quota hierarchies. ClusterQueues join cohorts, and cohorts can form trees via `spec.parentName`. Resources flow through the hierarchy via borrowing, lending limits, and cohort-level shared quotas (`nominalQuota`).

Previously, cohorts were implicit — two ClusterQueues sharing the same `spec.cohortName` could borrow from each other, but there was no CRD and no hierarchy. This feature makes cohorts explicit and hierarchical.

## Test Strategy

For this feature, the strategy is two-tiered:

- **Upstream:** e2e and integration tests developed and contributed to `kubernetes-sigs/kueue`, running on KIND clusters. One e2e smoke test (T1) proves the full stack on a real cluster; the remaining scenarios run as integration tests to minimize CI cost.
- **Downstream:** operator-specific tests in the `kueue-operator` repo, targeting the Kueue Operator + Operand on OCP (development branch `main`, Kueue 1.5 / RHBoK release). These cover scenarios that only apply to the RHBoK distribution.

## Test Scope

### Upstream Tests ([OCPKUEUE-726](https://redhat.atlassian.net/browse/OCPKUEUE-726))

7 scenarios covering CRD lifecycle and KEP-79 scheduling stories. Following [reviewer feedback on PR #13295](https://github.com/kubernetes-sigs/kueue/pull/13295#discussion_r3661483135), one scenario stays as e2e (smoke test) and the rest move to integration tests to minimize CI cost. Tracked in upstream issue [#13237](https://github.com/kubernetes-sigs/kueue/issues/13237).

**E2e** (in `test/e2e/singlecluster/baseline/hierarchical_cohort_test.go`):

| ID | Scenario | What It Validates |
|----|----------|-------------------|
| T1 | Basic Hierarchical Borrowing | CQ with 0 nominal quota borrows through a parent Cohort CRD hierarchy. Smoke test proving CRD install, webhooks, and borrowing on a real cluster. |

**Integration** (in `test/integration/singlecluster/scheduler/scheduler_test.go`):

| ID | Scenario | What It Validates |
|----|----------|-------------------|
| T2 | Reparenting (Update parentName) | Updating `parentName` dynamically changes which subtree a CQ can borrow from |
| T3 | Delete Mid-Tree Cohort | Admitted workloads survive mid-tree deletion; new borrowing blocked |
| T5 | Hierarchical LendingLimit | `lendingLimit` on Cohort CRD caps sibling borrowing (webhook validation already covered in `cohort_test.go`) |
| T6 | Hierarchical BorrowingLimit (KEP-79 Story 1) | `borrowingLimit:0` enforces one-directional borrowing (webhook validation already covered in `cohort_test.go`) |
| T7 | Isolated Orgs + Burst Queue (KEP-79 Story 2) | Isolated orgs with shared root-level burst queue |

**Blocked on [#13553](https://github.com/kubernetes-sigs/kueue/issues/13553):**

| ID | Scenario | What It Validates |
|----|----------|-------------------|
| T4 | Cycle Detection | Mutual parent cycle halts all admissions. Blocked on controller panic on cohort cycles. Will be added as integration test after the fix lands. |

### Downstream Tests (OCPKUEUE-727)

Operator-specific scenarios that don't exist upstream:

| ID | Scenario | Sub-task | Why downstream-specific |
|----|----------|----------|------------------------|
| D1 | Classical Preemption — Reclaim Succeeds | [OCPKUEUE-727](https://redhat.atlassian.net/browse/OCPKUEUE-727) | Preemption policy (`Classical`) is configured via the operator CR (`spec.config.preemption.preemptionPolicy`). Validates `reclaimWithinCohort: Any` reclaims quota across the cohort tree; `fairSharing` status is nil. |
| D2 | FairSharing Preemption — Weights Protect Borrower | [OCPKUEUE-727](https://redhat.atlassian.net/browse/OCPKUEUE-727) | Preemption policy (`FairSharing`) is configured via the operator CR. Without `reclaimWithinCohort`, weights (3:1) protect the higher-weight borrower from preemption — ML job stays pending. |
| D3 | TLS Metrics — Cohort Hierarchy Metrics | [OCPKUEUE-744](https://redhat.atlassian.net/browse/OCPKUEUE-744) | All 7 hierarchy-specific metrics (`kueue_cohort_subtree_admitted_active_workloads`, `kueue_cohort_subtree_admitted_workloads_total`, `kueue_cohort_subtree_quota`, `kueue_cohort_subtree_resource_reservations`, `kueue_cohort_info`, `kueue_cohort_weighted_share`, `kueue_cluster_queue_info` with `parent_cohort`/`root_cohort`) must surface through the TLS-secured endpoint. |

## Out of Scope

- **Cohort CRD validation logic** — covered by upstream unit tests and webhook tests
- **Multi-cluster cohort scenarios** — out of scope for this release
- **Kueue internal scheduling algorithm** — trusted as upstream-validated; we test behavior, not internals
- **Cohort UI/dashboard** — tracked separately under RHAIRFE-1787

### Downstream scenarios considered and excluded

The following downstream scenarios were evaluated during test planning and excluded from scope:

| Scenario | Reason |
|----------|--------|
| Managed namespace label, LabelPolicy=None, Fair sharing via operator CR, Operator lifecycle (original D1-D4) | Don't interact with cohorts — these operate at webhook/admission level before cohort scheduling is consulted. Already covered by the existing operator e2e suite. |
| Upgrade path — flat cohorts to explicit CRDs, Older version compatibility (original D6-D7) | Cohort CRD was introduced in Kueue v0.9.0 (July 2024); not a new migration concern for this release. |
| DRA + hierarchical cohorts (original D8) | Operator DRA configs (`deviceClassMappings`) don't overlap with cohort scheduling. DRA + Cohorts tests should go upstream (per dev team feedback). Upstream DRA test coverage is being extended in [PR #12883](https://github.com/kubernetes-sigs/kueue/pull/12883). |
| Admission fair sharing + hierarchical cohorts (original D10) | Admission fair sharing (KEP-4136) operates within a single ClusterQueue only, ordering its LocalQueues by historical usage. It has no interaction with cohorts. The cohort-level extension is unimplemented upstream (see [#9734](https://github.com/kubernetes-sigs/kueue/issues/9734)). |

## Target Environments

- Disconnected
- FIPS
- ARCH (x86_64, ARM)
- OCP versions: 4.18, 4.19, 4.20, 4.21, 4.22, 4.23, 5.0
- Hypershift — HCP

## Test Deliverables

- Upstream PRs with e2e and integration tests in `kubernetes-sigs/kueue` (Prow CI)
- Downstream PRs with operator tests in `kueue-operator` (Prow CI)
- Test report with information for Docs team

## Test Tasks

1. Manual exploratory testing during spike ([OCPKUEUE-718](https://redhat.atlassian.net/browse/OCPKUEUE-718))
2. Create and automate upstream tests in `kubernetes-sigs/kueue` repo — e2e smoke test (T1) and integration tests (T2-T7) ([OCPKUEUE-726](https://redhat.atlassian.net/browse/OCPKUEUE-726))
3. Port upstream e2e smoke test to downstream CI ([OCPKUEUE-748](https://redhat.atlassian.net/browse/OCPKUEUE-748))
4. Create downstream-specific automated tests (D1-D2) in `kueue-operator` repo ([OCPKUEUE-727](https://redhat.atlassian.net/browse/OCPKUEUE-727))
5. Fix bugs found during testing (e.g. [OCPBUGS-99316](https://redhat.atlassian.net/browse/OCPBUGS-99316) — webhook bug)
6. Execute automated tests on downstream (Prow) builds regularly (frequency TBD)

## Pass/Fail Criteria

- No critical or major defects remain open
- Administrators can create multi-level cohort hierarchies with correct borrowing, lending, and preemption behavior
- Supporting documentation material for Docs team is created
- Upstream and downstream CI jobs for cohort tests pass consistently (T4 is non-gating until upstream [#13553](https://github.com/kubernetes-sigs/kueue/issues/13553) is resolved)

## Risks

| Risk | Impact |
|------|--------|
| [OCPBUGS-99316](https://redhat.atlassian.net/browse/OCPBUGS-99316) — kueue-operator drops `vcohort.kb.io` webhook | Cohort CRD validation may not work downstream until fixed |
| T4 (Cycle Detection) blocked on upstream [#13553](https://github.com/kubernetes-sigs/kueue/issues/13553) — controller panic on cohort cycles | Cycle detection test cannot be added until the fix lands; gap in upstream test coverage |
| Development scope not fully defined — spike revealed implementation gaps (e.g. webhook bug) beyond the initial assumption of a test-only effort | Test timelines cannot be committed until full implementation scope is aligned |

---

> **Note:** The discussion about this test plan was initiated on [Google Docs](https://docs.google.com/document/d/1EEfUJyYJqrmDoky6B3jL02QUSXjWRpdf7DLOHv_aYQA/edit?tab=t.0).
