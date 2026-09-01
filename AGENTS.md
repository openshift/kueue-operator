# CLAUDE.md

## Project Overview

Kubernetes operator that manages the Kueue job queue controller on OpenShift. Built on controller-runtime with openshift/library-go. The operator's CRD is a cluster-scoped singleton `Kueue` resource (metadata.name must be `cluster`).

- **Module:** `github.com/openshift/kueue-operator`
- **Go version:** 1.25.0
- **Build flags:** `-tags strictfipsruntime`

## Common Commands

```bash
make build                    # Compile operator binary
make test-unit                # Unit tests (./pkg/...)
make lint                     # golangci-lint (30m timeout)
make lint-fix                 # golangci-lint with auto-fix
make generate                 # Regenerate manifests, deepcopy, clients
```

### E2E Tests

```bash
make test-e2e                 # All non-disruptive e2e tests (3 flake retries)
make test-e2e-disruptive      # Disruptive e2e tests only
make e2e-ci-test              # CI: excludes disruptive & flaky
make e2e-ci-test-dra          # CI: DRA tests only (label: dra)
make e2e-upstream-test        # Upstream Kueue e2e suite on the current OpenShift cluster
```

Operator e2e tests use Ginkgo v2 + Gomega with label-based filtering (`Label("operator")`, `Label("disruptive")`, `Label("dra")`, `Label("flaky")`). `make e2e-upstream-test` runs the upstream suite from the `upstream/kueue` submodule.

## Project Structure

| Directory | Purpose |
|-----------|---------|
| `cmd/kueue-operator/` | Entry point (`main.go`) |
| `pkg/apis/kueueoperator/v1/` | CRD type definitions |
| `pkg/operator/` | Main reconciler (`target_config_reconciler.go`) |
| `pkg/generated/` | Auto-generated clientsets, informers, listers |
| `pkg/webhook/` | Admission webhooks |
| `deploy/` | Kubernetes YAML manifests (roles, deployments, CRDs) |
| `test/e2e/` | E2E test suite |
| `test/e2e/testutils/` | Shared test builders and utilities |
| `upstream/` | Kueue upstream submodule |
| `hack/` | Utility scripts (codegen, deployment) |

## E2E Test Conventions

### Reuse TestResourceBuilder for workload objects

All e2e tests that create workload objects (Job, Pod, JobSet, StatefulSet, Deployment, LeaderWorkerSet) must use `testutils.TestResourceBuilder` from `test/e2e/testutils/builders.go`. Do not construct raw struct literals for these types.

```go
builder := testutils.NewTestResourceBuilder(namespace.Name, queueName)
job := builder.NewJob()
pod := builder.NewPod()
jobSet := builder.NewJobSet()
```

The builder sets standard defaults: security context, container image, resource requests, restart policy, and queue labels.

### Adding feature-specific fields

When a test needs extra fields (e.g., TAS annotations, DRA resource claims), create a small helper function in the test file that calls the builder and layers on the extra fields. Follow the pattern from `e2e_dra_test.go`:

```go
func newTASJob(builder *testutils.TestResourceBuilder, queueName, annotationKey, level string) *batchv1.Job {
    job := builder.NewJob()
    job.Labels[testutils.QueueLabel] = queueName
    job.Spec.Template.ObjectMeta.Annotations = map[string]string{
        annotationKey: level,
    }
    return job
}
```

Then modify fields like `Parallelism`, `Completions`, or `GenerateName` at the call site.

### Note on queue labels

`NewJob()` only auto-sets the queue label when `namespace == "kueue-managed-test"`. For dynamic namespaces, set it explicitly: `job.Labels[testutils.QueueLabel] = queueName`.

### Kueue resource wrappers

Use the fluent wrapper builders from `test/e2e/testutils/utils.go` for Kueue resources:

```go
topology, cleanup, err := testutils.NewTopology().WithGenerateName().
    WithLevels([]string{...}).
    CreateWithObject(ctx, kueueClient)

rf, cleanup, err := testutils.NewResourceFlavor().WithGenerateName().
    WithNodeLabel(key, value).
    WithTopologyName(name).
    CreateWithObject(ctx, kueueClient)

cq, cleanup, err := testutils.NewClusterQueue().WithGenerateName().
    WithCPU("1").WithMemory("1Gi").
    WithFlavorName(name).
    CreateWithObject(ctx, kueueClient)

lq, cleanup, err := testutils.NewLocalQueue(ns, name).
    WithClusterQueue(cqName).
    CreateWithObject(ctx, kueueClient)
```

### Namespace creation

Use `testutils.CreateNamespace(kubeClient, ns)` instead of writing custom namespace creation functions. Set the managed label via `testutils.OpenShiftManagedLabel`:

```go
ns := &corev1.Namespace{
    ObjectMeta: metav1.ObjectMeta{
        GenerateName: "test-prefix-",
        Labels: map[string]string{
            testutils.OpenShiftManagedLabel: "true",
        },
    },
}
cleanup, err := testutils.CreateNamespace(kubeClient, ns)
```

### Test file naming

E2E test files follow the pattern `e2e_<feature>_test.go` in `test/e2e/`.

## Linting

Uses golangci-lint v2 with `.golangci.yml`. Key linters: govet, staticcheck, unused, ineffassign, misspell. Separate API linter config in `.golangci-kal.yml` (run via `make lint-api`).

Test paths (`test/e2e/*`) have relaxed rules: dupl, lll, and staticcheck are excluded.
