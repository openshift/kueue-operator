# Topology Aware Scheduling (TAS) demo

Sample manifests that walk through Kueue's Topology Aware Scheduling feature
on OpenShift, mirroring the scenarios covered in
[`test/e2e/e2e_tas_test.go`](../../test/e2e/e2e_tas_test.go). Two independent
demos are included:

- **`hostname/`** — a single-level `kubernetes.io/hostname` topology. Every
  worker node is one topology domain. Shows Job, Pod, JobSet, and Deployment
  workloads all getting admitted with a `TopologyAssignment`.
- **`datacenter/`** — a 3-level `block` / `rack` / `hostname` topology
  simulating a real datacenter layout. Shows the difference between a
  *required* topology (must land in the same block) and a *preferred* one
  (try to land in the same rack, but admit anyway if it can't).

## Prerequisites

- The kueue-operator is installed and its `Kueue` CR (`metadata.name:
  cluster`) is `Managed`.
- You're logged in with `oc` as a user that can label nodes and create
  cluster-scoped Kueue resources (ClusterQueue, ResourceFlavor, Topology).
- `hostname/` needs at least 1 worker node; `datacenter/` needs at least 2
  (4 shows every block/rack combination).

## 1. Enable the integrations used by this demo

```bash
oc apply -f 00-kueue-cr.yaml
oc apply -f 01-namespace.yaml
```

`00-kueue-cr.yaml` turns on the `BatchJob`, `Pod`, `Deployment`, and `JobSet`
integrations. If your cluster's `Kueue` CR already exists, merge these
frameworks into it instead of applying this file (it will overwrite your
existing `spec.config`).

## 2. Run the hostname demo

```bash
cd hostname
./00-label-nodes.sh
oc apply -f 01-topology.yaml
oc apply -f 02-resourceflavor.yaml
oc apply -f 03-clusterqueue.yaml
oc apply -f 04-localqueue.yaml

oc create -f 05-job.yaml
oc create -f 06-pod.yaml
oc create -f 07-jobset.yaml
oc create -f 08-deployment.yaml
```

Resources with `generateName` (the workloads) are created with `oc create`,
not `oc apply`, since `apply` doesn't handle generated names well.

Watch the workloads get admitted and their `TopologyAssignment` populated:

```bash
oc get workloads -n kueue-tas-demo
oc get workload <name> -n kueue-tas-demo -o jsonpath='{.status.admission.podSetAssignments[*].topologyAssignment}' | jq .
```

For the Pod and Deployment examples, confirm the scheduling gate was removed
and a `nodeSelector` was injected:

```bash
oc get pods -n kueue-tas-demo -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.schedulingGates}{"\t"}{.spec.nodeSelector}{"\n"}{end}'
```

Clean up:

```bash
oc delete -f 08-deployment.yaml -f 07-jobset.yaml -f 06-pod.yaml -f 05-job.yaml --ignore-not-found
oc delete -f 04-localqueue.yaml -f 03-clusterqueue.yaml -f 02-resourceflavor.yaml -f 01-topology.yaml
./99-unlabel-nodes.sh
cd ..
```

## 3. Run the datacenter demo

```bash
cd datacenter
./00-label-nodes.sh
oc apply -f 01-topology.yaml
oc apply -f 02-resourceflavor.yaml
oc apply -f 03-clusterqueue.yaml
oc apply -f 04-localqueue.yaml

oc create -f 05-job-block-required.yaml
oc create -f 06-job-rack-preferred.yaml
```

Check the `TopologyAssignment.levels` on each workload — the block-required
job pins both pods to the same `cloud.provider.com/topology-block` value,
while the rack-preferred job tries for the same
`cloud.provider.com/topology-rack` but stays admitted even if it can't:

```bash
oc get workloads -n kueue-tas-demo -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.admission.podSetAssignments[*].topologyAssignment.levels}{"\n"}{end}'
```

Clean up:

```bash
oc delete -f 06-job-rack-preferred.yaml -f 05-job-block-required.yaml --ignore-not-found
oc delete -f 04-localqueue.yaml -f 03-clusterqueue.yaml -f 02-resourceflavor.yaml -f 01-topology.yaml
./99-unlabel-nodes.sh
cd ..
```

## Tear down

```bash
oc delete -f 01-namespace.yaml
```

(Leave `00-kueue-cr.yaml` alone unless you want to disable the integrations
it enabled.)
