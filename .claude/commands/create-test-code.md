# Create Test Code from Manual Test Case

Generate a Ginkgo E2E test file (`.go`) from a manual test case document
(`.md`), following the patterns and conventions used in the existing test suite.

## Usage

```
/create-test-code test/docs/cases/e2e_dra.md
/create-test-code test/docs/cases/e2e_preemption.md
/create-test-code
```

- Pass the path to a test case markdown file to generate Go test code
- If no argument is provided, the skill lists available `.md` files in
  `test/docs/cases/` and asks you to pick one

## Arguments

- $ARGUMENTS: Path to a test case markdown file (e.g.,
  `test/docs/cases/e2e_dra.md`)

## Process

### Step 1: Validate Input

1. Check that the provided file path exists and ends with `.md`
2. If no argument is provided, list all `.md` files under `test/docs/cases/`.
   Show the first 5 files as numbered options, then add an option "Show all
   files" to display the complete list, and allow the user to type a filename
   directly
3. Determine the output Go file name: derive from the markdown filename by
   adding `_test.go` suffix (e.g., `e2e_dra.md` → `e2e_dra_test.go`)
4. Check if the Go test file already exists in `test/e2e/` — if so, ask the
   user whether to overwrite, merge (add missing scenarios), or skip

### Step 2: Read the Manual Test Case

Read the markdown file and extract:

1. **Feature name and labels** from the header metadata (Test file, Feature,
   Labels fields)
2. **Prerequisites** — these become `BeforeAll` / `BeforeEach` setup
3. **Each scenario** — map to a `When` / `It` block:
   - Scenario title → `When("...", func() {`
   - Ginkgo path (if present) → use the exact `When` and `It` descriptions
   - Setup steps → code before the main assertions
   - Execution steps → the body of the `It` block, wrapped in `By("...")` calls
   - Expected results → `Expect` / `Eventually` / `Consistently` assertions
   - Cleanup steps → `DeferCleanup` calls
4. **Skip conditions** (e.g., "Skipped on: HyperShift") → `Skip("...")` calls
5. **Known issues** — add as comments in the test
6. **Important notes** about global config changes — save/restore patterns

### Step 3: Learn the Repo Patterns

Before generating code, read and understand the existing patterns. This is
critical — the generated code must look like it belongs in this repo.

#### 3a: Read Test Utilities

Read ALL files in `test/e2e/testutils/`:

- `builders.go` — resource builders (NewClusterQueue, NewLocalQueue,
  NewResourceFlavor, etc.). Learn the builder API: which methods are available,
  what they return, how cleanup functions work
- `client.go` — client setup and available client types
- `constants.go` — shared constants (timeouts, labels, namespaces, image refs)
- `utils.go` — helper functions (WaitForAllPodsInNamespaceDeleted,
  IsJobSuspended, IsJobPodRunning, DumpKueueControllerManagerLogs, etc.)

#### 3b: Read Existing Test Files

Read ALL `e2e_*_test.go` files in `test/e2e/` to learn:

1. **Import patterns** — which packages are imported and how they're aliased
   (e.g., `ssv1`, `kueuev1beta2`, `metav1`)
2. **Global variables** — what's available from `e2e_suite_test.go` (e.g.,
   `kubeClient`, `clients`, `visibilityClient`)
3. **Helper functions defined in test files** — functions like
   `enableAdmissionFairSharing`, `createClusterRoleBinding`,
   `checkWorkloadCondition`, `newLongRunningJob`, `applyKueueConfig`. These
   are NOT in testutils but in specific test files. If a needed helper exists
   in another test file, reuse the pattern — do NOT duplicate the function if
   it's in the same package
4. **Resource creation patterns:**
   - Builders: `testutils.NewClusterQueue().WithGenerateName().WithCPU("2").WithMemory("200Mi").WithFlavorName(rf.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)`
   - Raw client: `kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, job, metav1.CreateOptions{})`
   - Namespace creation with labels
5. **Cleanup patterns:**
   - `DeferCleanup(cleanupFunc)` for builder-created resources
   - `DeferCleanup(func(ctx context.Context) { ... })` for inline cleanup
   - Cleanup order: resources deleted in reverse creation order
6. **Assertion patterns:**
   - `Eventually(func() bool { ... }, timeout, poll).Should(BeTrue(), "message")`
   - `Eventually(func(g Gomega) { g.Expect(...) }, timeout, poll).Should(Succeed())`
   - `Consistently(func() bool { ... }, timeout, poll).Should(BeTrue())`
   - `Expect(err).NotTo(HaveOccurred(), "message")`
7. **By() step descriptions** — how they're worded (e.g., "Creating Resource
   Flavor", "Verifying Job1 workload is admitted")
8. **Describe/When/It structure:**
   - `Describe("Feature Name", Label("label"), Ordered, func() {`
   - `When("context description", func() {`
   - `It("should do something", func(ctx context.Context) {`
9. **JustAfterEach for debug logging:**
   - `JustAfterEach(func(ctx context.Context) { testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 500) })`
10. **Config save/restore patterns:**
    - Save in `BeforeAll` or at the start of an `It`
    - Restore via `DeferCleanup`

### Step 4: Map Manual Steps to Go Code

For each scenario in the markdown:

1. **Setup steps** → translate to resource creation code:
   - "Create a ResourceFlavor" → use `testutils.NewResourceFlavor()` builder
   - "Create a ClusterQueue with 2 CPUs..." → use
     `testutils.NewClusterQueue().WithCPU("2").WithMemory("200Mi")...`
   - "Create a namespace with the openshift-managed label" → use
     `kubeClient.CoreV1().Namespaces().Create(...)` with label map
   - "Create LocalQueue X with fairSharingWeight: Y" → use
     `testutils.NewLocalQueue(...).WithFairSharingWeight("Y")...`
   - "Save the current Kueue operand config" → get + save + DeferCleanup
     restore

2. **Action steps** (plain text in the markdown) → translate to API calls:
   - "Submit job X on queue Y requesting Z CPU" → create a Job using the
     pattern from existing test files (check for helpers like
     `newLongRunningJob`)
   - "Delete job X" → `kubeClient.BatchV1().Jobs(ns).Delete(...)`
   - "Set the APIServer TLS profile to Modern" → use
     `updateAPIServerTLSProfile` if it exists, otherwise create the function

3. **Verification steps** (with `oc` commands in the markdown) → translate to
   assertions:
   - "Verify workload is admitted" → `checkWorkloadCondition(ctx, ns, uid, kueuev1beta2.WorkloadAdmitted, name)` if the helper exists, otherwise write an `Eventually` block
   - "Verify job is suspended" → `testutils.IsJobSuspended(ctx, kubeClient, ns, name)`
   - "Verify pod is running" → `testutils.IsJobPodRunning(ctx, kubeClient, ns, name)`
   - "Wait for consumedResources.cpu > 0" → `Eventually` with client get +
     status check
   - "Verify Degraded condition" → `Eventually` checking
     `kueueInstance.Status.Conditions`

4. **Cleanup steps** → translate to `DeferCleanup`:
   - Builder-created resources: use the cleanup function returned by
     `CreateWithObject`
   - Namespaces: `DeferCleanup(func(ctx context.Context) { kubeClient.CoreV1().Namespaces().Delete(...) })`
   - Config restore: `DeferCleanup(func(ctx context.Context) { applyKueueConfig(ctx, savedConfig, kubeClient) })`

### Step 5: Generate the Go File

Create the file at `test/e2e/<filename>_test.go`.

Structure:

```go
/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
...
*/

package e2e

import (
    // standard library imports
    // third-party imports (ginkgo, gomega, openshift APIs)
    // internal imports (testutils, kueue APIs)
    // kubernetes imports
)

var _ = Describe("<Feature Name>", Label("<labels>"), Ordered, func() {
    // shared variables
    var (
        ...
    )

    JustAfterEach(func(ctx context.Context) {
        testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 500)
    })

    BeforeAll(func(ctx context.Context) {
        // shared setup from prerequisites
    })

    When("<context from scenario>", func() {
        It("<expected behavior>", func(ctx context.Context) {
            By("<step description>")
            // step code

            By("<step description>")
            // step code
            ...
        })
    })

    // repeat for each scenario
})

// helper functions at the bottom of the file
```

### Step 6: Ensure Test Case Doc Exists in the Repo

After generating the Go file, check if a corresponding test case markdown doc
exists at `test/docs/cases/`:

1. Derive the expected doc path from the Go filename:
   `e2e_dra_er_alpha2_test.go` → `test/docs/cases/e2e_dra_er_alpha2.md`
2. If the doc already exists, skip this step
3. If it does NOT exist, generate it using the same model as `/create-test-case`:
   - Use the input markdown as the source for scenarios, steps, and
     prerequisites
   - Adapt it to the repo's test case doc format (header metadata, description,
     scenarios list, setup/execution/expected result/cleanup per scenario)
   - Follow the Option B pattern: `oc` commands only on verification steps
   - Place it at `test/docs/cases/<name>.md`
4. Update any test plan files in `test/docs/plans/` that have a traceability
   table — if the test case was listed as "TBD", update it with a link to the
   new doc

This ensures both the Go code and its manual test case doc are always in sync
inside the repo, regardless of where the source markdown came from.

### Step 7: Verify the Generated Code

After generating:

1. Check that the file compiles:
   ```bash
   cd test/e2e && go vet ./...
   ```
2. If there are compilation errors, fix them by:
   - Checking import paths
   - Verifying builder method names exist in `testutils/builders.go`
   - Verifying helper function signatures match
3. Report any functions that need to be created (not found in existing code)

### Step 8: Present Results

Show the user:
1. The path to the created file
2. A summary: number of scenarios generated, any helper functions that were
   reused from other test files, any new helpers that need to be created
3. If new helper functions are needed, suggest where to put them (in the test
   file itself or in `testutils/`)
4. Ask if they want to review or modify anything

## Important Guidelines

- **Match existing code style exactly** — indentation, naming, import ordering,
  error message format must match what exists in the repo
- **Reuse helpers, don't duplicate** — if `checkWorkloadCondition` exists in
  another test file in the same package, call it directly (same package = no
  import needed). Do NOT copy-paste the function
- **Reuse builder patterns** — always use `testutils.New*()` builders when
  available instead of raw API calls
- **Use the same timeouts** — `testutils.OperatorReadyTime`,
  `testutils.OperatorPoll`, `testutils.ConsistentlyTimeout`, etc.
- **Use the same image refs** — `testutils.GetContainerImageForWorkloads()`
  for workload containers
- **Every By() step must match a step in the markdown** — the Go code and the
  manual doc should be traceable to each other
- **Include the copyright header** — use the same license header as other test
  files
- **Do NOT invent scenarios** — only generate code for scenarios in the markdown
- **If a helper function doesn't exist and is needed**, create it at the bottom
  of the test file (not in testutils) unless it would be useful across multiple
  test files

## Kueue E2E Test Patterns (Mandatory)

These patterns are the project's established conventions. All generated code
MUST follow them.

### Kueue Config Updates

Use `applyKueueConfig(ctx, config, kubeClient)` to update the Kueue instance
config. It handles fetching, updating, waiting for deployment resource version
change, and waiting for readiness. Do NOT manually patch + wait for deployment.

In `AfterAll` or `DeferCleanup`, call `applyKueueConfig` with the saved initial
config — no need to patch-remove fields first. For robust restoration, use
`restoreKueueConfig` which includes conflict retry via `Eventually`.

### Job Suspension Checks

Use `testutils.IsJobSuspended(ctx, kubeClient, namespace, jobName)`. Do NOT
fetch Workloads and iterate OwnerReferences.

```go
// Verify a job stays pending
Consistently(func() bool {
    return testutils.IsJobSuspended(ctx, kubeClient, ns, name)
}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(BeTrue())

// Verify a job gets admitted
Eventually(func() bool {
    return !testutils.IsJobSuspended(ctx, kubeClient, ns, name)
}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())
```

### Cleanup with DeferCleanup

Prefer Ginkgo's `DeferCleanup` over Go's `defer`. Always register
`DeferCleanup` immediately after capturing the state you need to restore,
BEFORE any assertions that could fail. If `DeferCleanup` is registered after
a failing assertion, cleanup never runs and state leaks.

```go
// Good — cleanup registered before assertions
savedConfig := kueueInstance.Spec.Config
DeferCleanup(func(ctx context.Context) {
    applyKueueConfig(ctx, savedConfig, kubeClient)
})
applyKueueConfig(ctx, desiredConfig, kubeClient)
Eventually(...).Should(Succeed())

// Bad — cleanup registered after assertions
applyKueueConfig(ctx, desiredConfig, kubeClient)
Eventually(...).Should(Succeed()) // if this fails, DeferCleanup never registers
DeferCleanup(func(ctx context.Context) { ... })
```

### Cleanup Function Nil-Guards

When cleanup functions are declared as `var` and assigned in
`BeforeEach`/`BeforeAll`, always nil-check before calling in
`AfterEach`/`AfterAll`. If setup fails partway, unassigned cleanup functions
will be nil and panic.

```go
AfterEach(func(ctx context.Context) {
    deleteNamespace(ctx, ns)
    if cleanupLQ != nil { cleanupLQ() }
    if cleanupCQ != nil { cleanupCQ() }
    if cleanupRF != nil { cleanupRF() }
})
```

### Always Capture Cleanup Functions

Never discard cleanup functions returned by resource creation helpers. Every
test in the repo captures and defers/calls them.

```go
// Good
lq, cleanupLQ, err := testutils.NewLocalQueue(ns.Name, "lq").
    WithClusterQueue(cq.Name).
    CreateWithObject(ctx, clients.UpstreamKueueClient)

// Bad — cleanup discarded
lq, _, err := testutils.NewLocalQueue(ns.Name, "lq").
    WithClusterQueue(cq.Name).
    CreateWithObject(ctx, clients.UpstreamKueueClient)
```

### Test Resource Isolation

Create test resources (ResourceFlavor, ClusterQueue, Namespace, LocalQueue)
inside each `It` block rather than in `BeforeAll`/`BeforeEach`. This keeps
tests self-contained and avoids shared state issues.

### Timeout Constants

Use `testutils` constants — never hardcode durations:

- `testutils.ConsistentlyTimeout` / `testutils.ConsistentlyPoll` — short
  consistency checks
- `testutils.ConsistentlyLongTimeout` / `testutils.ConsistentlyLongPoll` —
  longer consistency checks
- `testutils.OperatorReadyTime` / `testutils.OperatorPoll` — Eventually checks

### Descriptive Failure Messages

Always include a descriptive failure message in
`Expect(err).NotTo(HaveOccurred())` calls:

```go
// Good
Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")

// Bad
Expect(err).NotTo(HaveOccurred())
```

### Error Variable Reuse

Reuse a single `err` variable across all resource creation calls. Do NOT
introduce `err2`, `err3`, etc.

```go
var err error
ns, err = kubeClient.CoreV1().Namespaces().Create(...)
Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

rf, cleanupRF, err = testutils.NewResourceFlavor()...
Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
```

### Reuse Existing Job Helpers

Do not create duplicate job builder functions. If a parameterized job builder
already exists in the same file or package (e.g.,
`newLongRunningJob(name, namespace, queueName, cpu, memory)`), reuse it.

### Job Deletion — Inline Delete

Do not create custom job deletion helpers. Use inline delete with
`DeletePropagationBackground` and let `Eventually` handle waiting:

```go
err = kubeClient.BatchV1().Jobs(ns.Name).Delete(ctx, job.Name, metav1.DeleteOptions{
    PropagationPolicy: ptr.To(metav1.DeletePropagationBackground),
})
Expect(err).NotTo(HaveOccurred(), "Failed to delete job")
```

### Describe Labels

Use kebab-case for `Label()` values. Include `"operator"` only if needed
(like DRA tests):

```go
Label("admission-fair-sharing")
Label("operator", "dra", "dra-extended-resources")
```
