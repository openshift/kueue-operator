# Storage version migration fixtures

These are Kueue CustomResourceDefinitions exactly as earlier operator releases
installed them. They are copied unchanged from `bindata/assets/kueue-operator/crds/`
on the release branches. The tests use them to build the persisted state of a
cluster that was upgraded from those releases.

| Directory | Source | Operator | Kueue CRD versions |
|-----------|--------|----------|--------------------|
| `release-1.2/` | `release-1.2` at `efbdc49b97055825c4705b7c8e12a6cfc5e50cf6` | 1.2.0 | `v1beta1` (storage) only |
| `release-1.5/` | `release-1.5` at `b3d374fb954fd78335801ecbd50ca0db99c59f1c` | 1.5.1 | `v1beta1` (served), `v1beta2` (storage) |

The `release-1.5` ClusterQueue, LocalQueue and Workload CRDs use webhook
conversion. The AdmissionCheck and ResourceFlavor CRDs use no conversion.

The Workload CRD of release 1.2 is not copied, to keep the fixtures small. The
tests derive its persisted-as-v1beta1 stage from `release-1.5/workloads.yaml` by
keeping only the `v1beta1` version and marking it as the storage version.
