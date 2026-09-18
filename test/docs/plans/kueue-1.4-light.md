# Kueue 1.4 Release Testing Plan

## Scope

| Feature | JIRA | Status |
|---------|------|--------|
| TLS Profile consistency | [OCPKUEUE-418](https://redhat.atlassian.net/browse/OCPKUEUE-418) | In Scope |
| DRA Attribute-Based GPU | [OCPSTRAT-2380](https://redhat.atlassian.net/browse/OCPSTRAT-2380) | In Scope |
| Admission Fair Sharing (GA) | [OCPSTRAT-2588](https://redhat.atlassian.net/browse/OCPSTRAT-2588) | In Scope |
| Spark Operator Integration | kubernetes-sigs/kueue#7268 | Out of Scope |
| Elastic Job for Ray | kubernetes-sigs/kueue#8651 | Out of Scope |
| ProvisioningRequest (Dev Preview) | [OCPSTRAT-2105](https://redhat.atlassian.net/browse/OCPSTRAT-2105) | Out of Scope |

## Schedule

| Milestone | Target |
|-----------|--------|
| Branching | Sprint 288 |
| Testing Start | Sprint 289 |
| Release | Sprint 290 |

## Test Matrix

**OCP Versions:** 4.18, 4.19, 4.20, 4.21, 4.22
**Variants:** FIPS, Disconnected, arm64, Hypershift (AWS)

**Tracking Epic:**
[OCPKUEUE-667](https://redhat.atlassian.net/browse/OCPKUEUE-667)

## Test Activities

| Activity | JIRA | Cadence |
|----------|------|---------|
| Upgrade testing | [OCPKUEUE-668](https://redhat.atlassian.net/browse/OCPKUEUE-668) | On rebuilds |
| RapidDAST | [OCPKUEUE-669](https://redhat.atlassian.net/browse/OCPKUEUE-669) | Once |
| Regression & New Features | [OCPKUEUE-670](https://redhat.atlassian.net/browse/OCPKUEUE-670) | Daily |
| Bug verification | [OCPKUEUE-671](https://redhat.atlassian.net/browse/OCPKUEUE-671) | On rebuilds |

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

**OCP Upgrade:** 4.18->4.19, 4.19->4.20, 4.20->4.21, 4.21->4.22
**Kueue Upgrade:** 1.3->1.4 on OCP 4.22 (operator-only + full uninstall)

## DAST APIs

| API Group | Version | Notes |
|-----------|---------|-------|
| kueue.openshift.io | v1 | |
| kueue.x-k8s.io | v1beta1 | May be removed |
| kueue.x-k8s.io | v1beta2 | |
| visibility.kueue.x-k8s.io | v1beta1 | May be removed |
| visibility.kueue.x-k8s.io | v1beta2 | |

## Bugs

- TBD (label: `Kueue-1.4`)

## Risks

- [ ] Upgrade tests not fully automated — [OCPKUEUE-427](https://redhat.atlassian.net/browse/OCPKUEUE-427)
- [ ] Kueue + DRA integration uncertainty — [OCPKUEUE-573](https://redhat.atlassian.net/browse/OCPKUEUE-573)

## Exit Criteria

- [ ] All in-scope features verified
- [ ] No release-blocking regressions
- [ ] All test activities complete
- [ ] All risks resolved or accepted
