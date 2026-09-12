# Kueue 1.5 Release Testing Plan

## Scope

| Feature | JIRA | Status |
|---------|------|--------|
| TBD | TBD | TBD |

**Tracking Epic:** TBD

## Schedule

| Milestone | Target |
|-----------|--------|
| Branching | TBD |
| Testing Start | TBD |
| Release | TBD |

## Test Matrix

**OCP Versions:** 4.19, 4.20, 4.21, 4.22, 4.23
**Variants:** FIPS, Disconnected, arm64, Hypershift (AWS)

## Test Activities

| Activity | JIRA | Cadence |
|----------|------|---------|
| Upgrade testing | TBD | On rebuilds |
| RapidDAST | TBD | Once |
| Regression & New Features | TBD | Daily |
| Bug verification | TBD | On rebuilds |

## Test Cases

Test case docs live in [`test/docs/cases/`](../cases/) — 1:1 with e2e test files.

| Test Case | Doc | Test File |
|-----------|-----|-----------|
| Admission Fair Sharing | [e2e_admission_fair_sharing.md](../cases/e2e_admission_fair_sharing.md) | [e2e_admission_fair_sharing_test.go](../../e2e/e2e_admission_fair_sharing_test.go) |
| DRA | TBD | [e2e_dra_test.go](../../e2e/e2e_dra_test.go) |
| DRA Extended Resources | TBD | [e2e_dra_extended_resources_test.go](../../e2e/e2e_dra_extended_resources_test.go) |
| Gang Scheduling | TBD | [e2e_gangscheduling_test.go](../../e2e/e2e_gangscheduling_test.go) |
| Local Queue Defaulting | TBD | [e2e_local_queue_defaulting_test.go](../../e2e/e2e_local_queue_defaulting_test.go) |
| Managed Jobs NS Selector | TBD | [e2e_managed_jobs_namespace_selector_test.go](../../e2e/e2e_managed_jobs_namespace_selector_test.go) |
| Metrics | TBD | [e2e_metrics_test.go](../../e2e/e2e_metrics_test.go) |
| Operator | TBD | [e2e_operator_test.go](../../e2e/e2e_operator_test.go) |
| Preemption | TBD | [e2e_preemption_test.go](../../e2e/e2e_preemption_test.go) |
| Scheduling Gate | TBD | [e2e_scheduling_gate_test.go](../../e2e/e2e_scheduling_gate_test.go) |
| TLS Profile | [e2e_tls_profile.md](../cases/e2e_tls_profile.md) | [e2e_tls_profile_test.go](../../e2e/e2e_tls_profile_test.go) |
| Visibility On Demand | [e2e_visibility_on_demand.md](../cases/e2e_visibility_on_demand.md) | [e2e_visibility_on_demand_test.go](../../e2e/e2e_visibility_on_demand_test.go) |

## Upgrade Matrix

**OCP Upgrade:** 4.19->4.20, 4.20->4.21, 4.21->4.22, 4.22->4.23
**Kueue Upgrade:** 1.4->1.5 on OCP 4.23 (operator-only + full uninstall)

## DAST APIs

| API Group | Version | Notes |
|-----------|---------|-------|
| kueue.openshift.io | v1 | |
| kueue.x-k8s.io | v1beta1 | May be removed |
| kueue.x-k8s.io | v1beta2 | |
| visibility.kueue.x-k8s.io | v1beta1 | May be removed |
| visibility.kueue.x-k8s.io | v1beta2 | |

## Bugs

- TBD (label: `Kueue-1.5`)

## Risks

- [ ] TBD

## Exit Criteria

- [ ] All in-scope features verified
- [ ] No release-blocking regressions
- [ ] All test activities complete
- [ ] All risks resolved or accepted
