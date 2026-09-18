# Create Test Case Documentation

Generate a markdown test case document from a Ginkgo E2E test file, following the
project's test documentation model. The generated document serves as a manual
fallback for when automation cannot be executed.

## Usage

```
/create-test-case test/e2e/e2e_dra_test.go
/create-test-case test/e2e/e2e_preemption_test.go
/create-test-case
```

- Pass the path to a Go test file to generate its test case doc
- If no argument is provided, the skill lists all available test files and asks
  you to pick one

## Arguments

- $ARGUMENTS: Path to a Go test file (e.g., `test/e2e/e2e_dra_test.go`)

## Process

### Step 1: Validate Input

1. Check that the provided file path exists and ends with `_test.go`
2. If no argument is provided, run
   `find test/e2e -maxdepth 1 -name "e2e_*_test.go" | sort` to get all test
   files (exclude `e2e_suite_test.go`). Show the first 5 files as numbered
   options. After the list, always show a line like
   "... and N more files. Type 'all' to see the full list, or type a filename."
   where N is the count of remaining files. Mark files that already have a doc
   in `test/docs/cases/` with "(doc exists)"
3. Check if a test case doc already exists in `test/docs/cases/` for this file —
   if so, ask the user whether to overwrite or skip

### Step 2: Analyze the Go Test File

Read the entire test file and extract:

1. **Describe block:** the top-level `Describe("Name", Label("..."), ...)` —
   this gives the feature name and labels
2. **When/It blocks:** each `When` + `It` pair is one test scenario. Extract:
   - The `When` description (context)
   - The `It` description (expected behavior)
   - The full Ginkgo path: `When ... / It ...`
3. **By() steps:** these are the execution steps within each `It` block. Each
   `By("...")` call maps to one manual step
4. **BeforeAll / BeforeEach:** these are shared prerequisites or setup that
   applies to all scenarios
5. **DeferCleanup calls:** these tell you what resources need cleanup. Extract
   the resource type and name pattern
6. **Skip conditions:** look for `Skip("...")` calls — these indicate
   environment constraints (e.g., HyperShift not supported)
7. **Comments referencing bugs:** look for comments mentioning upstream issues,
   OCPBUGS, or known limitations
8. **Helper functions:** check functions called within tests to understand what
   setup they perform (e.g., `enableAdmissionFairSharing`, `createClusterRoleBinding`)
9. **JIRA references:** check imports, comments, and related files for JIRA
   ticket references (OCPSTRAT-*, OCPKUEUE-*, OCPBUGS-*)

### Step 3: Classify Each Step

For each `By()` step in each scenario, classify it as:

- **Action step** — creates, submits, deletes, or modifies a resource. These
  are written as plain text instructions (no `oc` commands). Examples:
  - "Submit job X on queue Y requesting Z CPU"
  - "Delete job X to free quota"
  - "Set the APIServer TLS profile to Modern"

- **Verification step** — checks, verifies, waits for, or validates a
  condition. These include `oc` commands with expected output. Examples:
  - "Verify workload is admitted" → include `oc get workloads` command
  - "Wait for consumedResources.cpu > 0" → include `oc get localqueue` command
  - "Check Degraded condition" → include `oc get kueue cluster` command

The rule: if the step contains `Expect`, `Eventually`, `Consistently`,
`Should`, or `ShouldNot`, it's a verification step and needs an `oc` command.

### Step 4: Determine Verification Commands

For common verification patterns, use these `oc` commands:

| What to verify | Command |
|---------------|---------|
| Workload admitted | `oc get workloads -n <namespace> -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.conditions[?(@.type=="Admitted")].status}{"\n"}{end}'` |
| Job suspended | `oc get job <name> -n <namespace> -o jsonpath='{.spec.suspend}'` |
| Pod running | `oc get pods -n <namespace> -l job-name=<name> -o jsonpath='{.items[0].status.phase}'` |
| LocalQueue usage | `oc get localqueue <name> -n <namespace> -o jsonpath='{.status.fairSharing.admissionFairSharingStatus.consumedResources.cpu}'` |
| Pending workloads | `oc get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/clusterqueues/<name>/pendingworkloads` |
| ConfigMap content | `oc get configmap <name> -n <namespace> -o jsonpath='{.data.controller_manager_config\.yaml}'` |
| Operator conditions | `oc get kueue cluster -o jsonpath='{range .status.conditions[*]}{.type}={.status} reason={.reason}{"\n"}{end}'` |
| Deployment ready | `oc get deployment <name> -n <namespace> -o jsonpath='{.status.readyReplicas}/{.status.replicas}'` |
| Events | `oc get events -n <namespace> --field-selector reason=<Reason>` |
| DRA resource usage | `oc get workload -n <namespace> -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{range .status.admission.podSetAssignments[*]}{range $k,$v := .resourceUsage}  {$k}={$v}{"\n"}{end}{end}'` |

If the test creates Kubernetes resources inline (ResourceFlavor, ClusterQueue,
LocalQueue, ResourceClaimTemplate, etc.), include the YAML manifests in the
setup steps using heredoc format:

```bash
cat <<'EOF' | oc apply -f -
apiVersion: ...
kind: ...
...
EOF
```

### Step 5: Generate the Markdown

Create the file at `test/docs/cases/<filename>.md` where `<filename>` is the Go
file name without the `_test.go` suffix (e.g., `e2e_dra.md` from
`e2e_dra_test.go`).

Use this structure:

```markdown
# Test Case: <Feature Name>

- **Test file:** [`test/e2e/<filename>_test.go`](../../e2e/<filename>_test.go)
- **Feature:** <feature name from Describe block>
- **JIRA:** <JIRA link if found, otherwise "TBD">
- **Automation status:** Automated
- **Since release:** <version if known, otherwise "TBD">
- **Labels:** `<labels from Describe block>`

## Description

<2-3 sentences explaining what the feature does and what these tests validate.
Derive this from the Describe block name and the test scenarios.>

## Scenarios

1. [Scenario title](#1-scenario-title-slug)
2. [Scenario title](#2-scenario-title-slug)
...

## Prerequisites

<Extract from BeforeAll, imports, and skip conditions. Include:>
- Required operator/component state
- Required cluster configuration
- Environment constraints (e.g., "Not supported on HyperShift")
- RBAC requirements

## Test Scenarios

### 1. <Short scenario title>

- **Ginkgo:** `When <context> / <It description>`
<If there's a skip condition, add:>
- **Skipped on:** <condition>
<If it's a negative test, add:>
- **Type:** Negative test
<If there's a known issue, add:>
- **Known issue:** <bug reference and description>
<If the scenario changes global config, add:>
- **Important:** <warning about side effects>

**Setup:**

<Numbered list of setup steps. Plain text for actions.>

**Execution:**

<Numbered list of execution steps. Plain text for actions, oc commands for
verification steps. Each verification step includes:>
```bash
<oc command>
# Expected: <what to look for>
```

**Expected Result:**

<Bulleted list of expected outcomes. Include oc commands for verification.>

**Cleanup:**

```bash
<oc delete commands in reverse creation order>
```

---

<Repeat for each scenario>
```

### Step 6: Update Test Plan References

After creating the test case doc:

1. Check if `test/docs/plans/` contains any test plan files
2. If test plans exist, look for the test cases traceability table
3. If the new test case is listed as "TBD", update it with a link to the new doc
4. If the test case is not listed at all, inform the user they may want to add it

### Step 7: Present Results

Show the user:
1. The path to the created file
2. A summary of what was generated (number of scenarios, any skipped conditions,
   any known issues found)
3. Ask if they want to review or modify anything

## Important Guidelines

- **Do NOT invent test scenarios** — only document what exists in the Go file
- **Do NOT add steps not in the Go file** — the doc must match the automation
- **Action steps = plain text, verification steps = oc commands** — this is the
  consistent pattern
- **Include cleanup for every scenario** — derived from DeferCleanup calls
- **Flag global config changes** — if a scenario modifies cluster-wide config
  (APIServer CR, Kueue operand), add an Important note
- **Preserve the Ginkgo path exactly** — it's the traceability link to the code
- **Include YAML manifests** when tests create resources inline — someone running
  manually needs the actual YAML, not just "create a ClusterQueue"
