# Create Release Test Plan

Generate a release testing plan document for a Kueue operator release, following
the project's test plan model. Supports both a full narrative format and a light
checklist format.

## Usage

```
/create-release-plan 1.5
/create-release-plan 1.5 full
/create-release-plan 1.5 light
```

- Pass a version number to generate the release testing plan
- Optionally specify `full` or `light` format (defaults to generating both)
- The skill will ask for missing information (features, dates, OCP versions)

## Arguments

- $ARGUMENTS: Release version (e.g., `1.5`) and optionally the format
  (`full` or `light`). Examples:
  - `1.5` — generates both full and light versions
  - `1.5 full` — generates only the full version
  - `1.5 light` — generates only the light version

## Process

### Step 1: Gather Release Information

Ask the user for the following information (skip any they've already provided):

1. **Release version** (from argument, e.g., `1.5`)
2. **Previous version** (default: decrement minor version, e.g., `1.4`)
3. **Target release date or sprint** (e.g., "Sprint 295" or "2026-09-15")
4. **In-scope features** — list of features with JIRA links. For each:
   - Feature name
   - JIRA link (OCPSTRAT-* or OCPKUEUE-*)
5. **Out-of-scope features** — list with reasons (e.g., "Dev Preview", "Deferred")
6. **OCP version range** — min and max (e.g., 4.19 through 4.23)
7. **Format** — full, light, or both (from argument or ask)

### Step 2: Scan the Repository

Automatically gather information from the repo:

1. **Test files:** List all `test/e2e/e2e_*_test.go` files for the test cases
   traceability table
2. **Existing test case docs:** Check `test/docs/cases/` for which `.md` files
   already exist (link them) vs. mark as "TBD"
3. **Previous release plans:** Check `test/docs/plans/` for existing plans to
   maintain consistency
4. **CI job patterns:** Derive Prow job names from the repo org/name and the
   release version using the naming convention:
   ```
   periodic-ci-openshift-kueue-operator-release-{VERSION}-test-e2e-{OCP_VERSION}
   ```

### Step 3: Calculate Schedule

Using the formulas:

```
ReleaseStartTestingDate = ReleaseDate - TestingDuration - ReleaseWorkBuffer
BranchingStarts = ReleaseStartTestingDate - 1 week
```

Ask the user for `TestingDuration` and `ReleaseWorkBuffer` if not known from
previous plans. Check existing plans in `test/docs/plans/` for precedent values.

Account for adjustments the user mentions (holidays, PTO, company events).

### Step 4: Build the OCP Upgrade Matrix

From the OCP version range, generate:

**OCP Upgrade pairs:**
```
OCP {MIN} + Product {VERSION} -> OCP {MIN+1}
OCP {MIN+1} + Product {VERSION} -> OCP {MIN+2}
...
OCP {MAX-1} + Product {VERSION} -> OCP {MAX}
```

**Product version upgrade:**
```
OCP {MAX}: Product {VERSION-1} -> Product {VERSION}
  - Operator-only uninstall
  - Full operator + operand uninstall
```

### Step 5: Build the DAST API List

Check the repo for CRD definitions or ask the user:

- `kueue.openshift.io` API versions
- `kueue.x-k8s.io` API versions
- `visibility.kueue.x-k8s.io` API versions
- Any new or deprecated API versions for this release

If previous release plans exist, use their DAST API list as a starting point and
ask if anything changed.

### Step 6: Generate the Document(s)

Create files in `test/docs/plans/`:

- Full format: `kueue-{VERSION}-full.md`
- Light format: `kueue-{VERSION}-light.md`

#### Full Format Structure

```markdown
# Release Kueue {VERSION} Testing Plan

## 1. Introduction

### 1.1. Overview

This document outlines the testing strategy, objectives, and procedures for the
Kueue {VERSION} release. The goal is to ensure the quality, stability, and
performance of new features and to verify that existing functionality remains
regression-free.

### 1.2. Scope

**In Scope**

- <feature> - [JIRA-ID](https://redhat.atlassian.net/browse/JIRA-ID)
...

**Out of Scope**

- <feature> - <reason>
...

### 1.3. Key Features

- <bullet list of headline features>

## 2. Release Testing Strategy

### 2.1. Schedule

| Milestone | Date / Sprint | Notes |
|-----------|---------------|-------|
| Branching | ... | Release branch created |
| Testing Start | ... | Environments provisioned |
| Week 1: Core Testing | ... | Regression, features, DAST, bugs |
| Week 2: Upgrade | ... | OCP + Kueue upgrade matrix |
| Release Work | ... | Artifacts, sign-off |
| Release | ... | Published |

### 2.2. Test Types

- **Integration Tests:** ...
- **Feature Tests:** ...
- **Regression Tests:** ...
- **Bug Verification:** ...
- **Upgrade Tests:** ...
- **Security Tests (DAST):** ...

### 2.3. Test Environments

- **CI/CD:** Prow Periodics
- **Staging Clusters:**
  - OCP versions
  - FIPS
  - Disconnected
  - Multi-ARCH
  - Hypershift on AWS

## 3. Test Areas & Test Cases

**Tracking Epic:** <epic name> -
[JIRA-ID](https://redhat.atlassian.net/browse/JIRA-ID)

### 3.1. Key Test Activities

| Activity | JIRA | Cadence | Est. Duration |
|----------|------|---------|---------------|
| Upgrade testing | [JIRA-ID](link) | Re-execute on rebuilds | ~3 days |
| RapidDAST | [JIRA-ID](link) | Once unless API changes | ~2 days |
| Regression & New Features | [JIRA-ID](link) | Daily on new builds | ~1-1.5 days |
| Bug verification | [JIRA-ID](link) | On new builds as needed | ~2 days |

### 3.2. Test Cases

<traceability table linking test case docs to Go files>

## 4. Test Details

### 4.1. Feature Test Execution

<spike -> scenarios -> manual -> automation workflow>

### 4.2. Release Test Execution

<CI periodic jobs list, upgrade jobs, manual testing areas>

### 4.3. Upgrade Plan

<OCP upgrade matrix + product version upgrade with uninstall scenarios>

### 4.4. Security Scanning (DAST APIs)

<API groups/versions table>

## 5. Bug Tracking

### 5.1. Bugs to Be Tested

- TBD (label: `Kueue-{VERSION}`)

### 5.2. Bugs Found During Release Testing (Out of Scope)

- TBD

## 6. Risks

| Risk | JIRA | Impact | Mitigation |
|------|------|--------|------------|
...

## 7. Exit Criteria

The Kueue {VERSION} release will be considered ready when:

- All in-scope features are verified
- No open release-blocking regressions
- All release test activities are complete
- All identified risks are resolved or accepted
```

#### Light Format Structure

```markdown
# Kueue {VERSION} Release Testing Plan

## Scope

| Feature | JIRA | Status |
|---------|------|--------|
...

**Tracking Epic:** [JIRA-ID](link)

## Schedule

| Milestone | Target |
|-----------|--------|
...

## Test Matrix

**OCP Versions:** ...
**Variants:** FIPS, Disconnected, arm64, Hypershift (AWS)

## Test Activities

| Activity | JIRA | Cadence |
|----------|------|---------|
...

## Test Cases

<traceability table>

## Upgrade Matrix

**OCP Upgrade:** ...
**Kueue Upgrade:** ...

## DAST APIs

| API Group | Version | Notes |
|-----------|---------|-------|
...

## Bugs

- TBD (label: `Kueue-{VERSION}`)

## Risks

- [ ] <risk> — [JIRA-ID](link)
...

## Exit Criteria

- [ ] All in-scope features verified
- [ ] No release-blocking regressions
- [ ] All test activities complete
- [ ] All risks resolved or accepted
```

### Step 7: Present Results

Show the user:
1. The path(s) to the created file(s)
2. A summary: number of features in scope, OCP version range, number of test
   cases linked vs. TBD
3. Remind them to:
   - Create the tracking epic and child JIRA items if not yet done
   - Fill in the bugs section once bugs are labeled
   - Update risks as they're identified
4. Ask if they want to modify anything

## Important Guidelines

- **All JIRA references must be clickable links** — use the format
  `[JIRA-ID](https://redhat.atlassian.net/browse/JIRA-ID)`
- **Test case docs that don't exist yet must be marked as "TBD"** — not fake
  filenames
- **Test case docs that exist must be linked** — check `test/docs/cases/`
- **OCP version matrix must be complete** — every supported version pair in the
  upgrade plan
- **CI job names follow the naming convention** — derive from repo org/name
- **Reuse patterns from existing plans** — if previous release plans exist in
  `test/docs/plans/`, follow their patterns for consistency
- **Do NOT hardcode durations** — ask the user or derive from previous plans
- **Schedule formulas produce estimates** — the user should confirm dates
- **Do NOT carry over content from previous plans when the user says TBD** — if
  the user chooses TBD for any section (features, risks, out-of-scope, etc.),
  leave it as TBD. Never pre-fill with data from a prior release plan unless the
  user explicitly asks to reuse it
