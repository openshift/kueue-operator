# Test Plan for [Feature Name]

**Plan Status:** Draft
**Feature:** [OCPSTRAT-XXXX] — [Feature title]
**Testing Epic:** [OCPKUEUE-XXX](https://redhat.atlassian.net/browse/OCPKUEUE-XXX)

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
| Testing Epic | [OCPKUEUE-XXX](https://redhat.atlassian.net/browse/OCPKUEUE-XXX) |
| KEP | _link to upstream KEP if applicable_ |
| Upstream tests | _JIRA story + upstream PR/issue link_ |
| Upstream docs | _link to upstream documentation_ |
| Downstream tests | _JIRA story link_ |
| Downstream docs | TBD |
| Bugs | _known bugs affecting this feature_ |

## Introduction

_Brief description of the feature: what it does, what CRDs or APIs are involved, and what changed from the previous behavior. Keep it to 2-3 paragraphs — enough context for someone unfamiliar with the feature to understand the test plan._

## Test Strategy

_Describe the testing approach. For Kueue features, this is typically two-tiered:_

- **Upstream:** _What tests are contributed to the upstream repo? What type (e2e, integration, unit)? What do they cover? Are there pre-existing upstream tests that already cover related functionality and can be extended or reused?_
- **Downstream:** _What operator-specific tests are needed? Why can't these be covered upstream? Are there pre-existing downstream tests that can be leveraged to avoid duplicating effort?_

## Test Scope

### Upstream Tests

_List upstream test scenarios with IDs, descriptions, and what each validates._

| ID | Scenario | What It Validates |
|----|----------|-------------------|
| T1 | _Scenario name_ | _What it proves_ |
| T2 | _Scenario name_ | _What it proves_ |

### Downstream Tests

_List downstream-specific scenarios. Include JIRA sub-tasks and explain why each is downstream-specific._

| ID | Scenario | Sub-task | Why downstream-specific |
|----|----------|----------|------------------------|
| D1 | _Scenario name_ | _JIRA link_ | _Why this can't be tested upstream_ |
| D2 | _Scenario name_ | _JIRA link_ | _Why this can't be tested upstream_ |

## Out of Scope

_List what is explicitly not being tested and why._

- _Item_ — _reason_
- _Item_ — _reason_

### Scenarios considered and excluded

_If downstream scenarios were evaluated and removed during planning, document them here with rationale for traceability._

| Scenario | Reason |
|----------|--------|
| _Scenario name_ | _Why it was excluded_ |

## Target Environments

- Disconnected
- FIPS
- ARCH (x86_64, ARM)
- OCP versions: _list applicable versions_
- Hypershift — HCP
- _Any feature-specific environment requirements_

## Test Deliverables

- _Upstream PRs with tests in `kubernetes-sigs/kueue` (Prow CI)_
- _Downstream PRs with operator tests in `kueue-operator` (Prow CI)_
- _Test report with information for Docs team_

## Test Tasks

_List testing-related work items for this feature. These are tasks focused on test planning, authoring, and validation — distinct from the implementation stories used to build the feature itself. Typical tasks include:_

- _Exploratory testing and spike investigations_
- _Test plan creation and review_
- _Upstream test automation (e2e, integration, unit)_
- _Downstream test automation (e2e, integration, unit)_
- _Additional validation (acceptance criteria, edge cases, environment-specific checks)_

1. _Task description ([JIRA link](https://redhat.atlassian.net/browse/OCPKUEUE-XXX))_
2. _Task description ([JIRA link](https://redhat.atlassian.net/browse/OCPKUEUE-XXX))_
3. _Task description ([JIRA link](https://redhat.atlassian.net/browse/OCPKUEUE-XXX))_

## Pass/Fail Criteria

- No critical or major defects remain open
- _Feature-specific acceptance criteria_
- Upstream and downstream CI jobs pass consistently
- _Note any non-gating items and why_

## Risks

| Risk | Impact |
|------|--------|
| _Description of risk_ | _Impact on testing or release_ |
