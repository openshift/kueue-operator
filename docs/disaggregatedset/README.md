# DisaggregatedSet demo

Sample manifests that enable Kueue's DisaggregatedSet integration through the
kueue-operator and run a DisaggregatedSet on OpenShift.

A DisaggregatedSet, served by LeaderWorkerSet as `disaggregatedset.x-k8s.io/v1`,
runs several roles of one model server, such as prefill and decode, as copies
called slices. With the integration enabled, Kueue creates one Workload per
slice that holds every role of the slice, so it admits and preempts each slice
as a whole: every role of a slice runs, or none of them does. Atomic admission
does not mean that every Pod becomes Ready at the same time.

The integration is **alpha** in Kueue and disabled by default. For the upstream
behavior, see the Kueue guide
[Run DisaggregatedSet](https://github.com/kubernetes-sigs/kueue/blob/4f3748d8f2c8b9fc68d131d6e23d44faf33f77c1/site/content/en/docs/tasks/run/disaggregatedset.md).

This demo describes a setup that depends on releases that are not available
yet. See the [prerequisites](#prerequisites) before you start.

## Prerequisites

- The kueue-operator is installed in a version that supports the
  `DisaggregatedSet` integration, and its `Kueue` CR (`metadata.name: cluster`)
  is `Managed`.
- A LeaderWorkerSet installation that serves the
  `disaggregatedset.x-k8s.io/v1` API. With the Leader Worker Set Operator, this
  requires version 1.1, which is not released yet. Until it is released, the
  Leader Worker Set Operator from the catalog does not serve this API, and you
  cannot run this demo with it. Check that the API is served with:

  ```bash
  oc api-resources --api-group=disaggregatedset.x-k8s.io
  ```

- You're logged in with `oc` as a user that can create cluster-scoped Kueue
  resources (ClusterQueue, ResourceFlavor).

## 1. Check for existing DisaggregatedSets

Kueue manages every DisaggregatedSet that has the `kueue.x-k8s.io/queue-name`
label as soon as the integration is enabled. The next update of such a
DisaggregatedSet rolls out every role, also while it is running, and the new
Pods stay gated until their slice Workload is admitted. List them first:

```bash
oc get disaggregatedsets --all-namespaces -l kueue.x-k8s.io/queue-name
```

## 2. Enable the integration

```bash
oc apply -f 00-kueue-cr.yaml
oc apply -f 01-namespace.yaml
```

`00-kueue-cr.yaml` turns on the `BatchJob` and `DisaggregatedSet` integrations.
If your cluster's `Kueue` CR already exists, add `DisaggregatedSet` to its
`spec.config.integrations.frameworks` instead of applying this file (it will
overwrite your existing `spec.config`):

```bash
oc patch kueue cluster --type=json \
  -p '[{"op":"add","path":"/spec/config/integrations/frameworks/-","value":"DisaggregatedSet"}]'
```

The operator then:

- adds the `disaggregatedset.x-k8s.io/disaggregatedset` framework and enables
  the `DisaggregatedSetIntegration` feature gate in the `kueue-manager-config`
  ConfigMap. Do not edit the ConfigMap directly;
- deploys the DisaggregatedSet webhooks and, because DisaggregatedSet Pods need
  scheduling gates, the Pod webhooks. Like the other webhooks, they only apply
  to namespaces labeled `kueue.openshift.io/managed=true`.

If the `disaggregatedset.x-k8s.io/v1` API is not served, the `Kueue` CR reports
`Degraded=True` with reason `MissingDependencies`. The operator still renders
the framework and the feature gate, and Kueue starts the integration once the
API is served, without a restart. Check the status with:

```bash
oc get kueue cluster -o jsonpath='{.status.conditions[?(@.type=="Degraded")]}' | jq .
```

## 3. Run a DisaggregatedSet

```bash
oc apply -f 02-resourceflavor.yaml
oc apply -f 03-clusterqueue.yaml
oc apply -f 04-localqueue.yaml
oc create -f 05-disaggregatedset.yaml
```

`05-disaggregatedset.yaml` has two slices. Each slice runs a `prefill` role with
two Pods and a `decode` role with a leader and a worker, and requests 700m CPU.
The ClusterQueue has 1 CPU, so Kueue admits one slice and keeps every Pod of
the other slice gated. No role of the second slice runs on its own.

```bash
oc get workloads -n kueue-disaggregatedset-demo
oc get pods -n kueue-disaggregatedset-demo \
  -o custom-columns=NAME:.metadata.name,GATES:.spec.schedulingGates[*].name,PHASE:.status.phase
```

Each Workload has three PodSets: `prefill`, `decode-leader` and
`decode-worker`. Raise the ClusterQueue CPU quota to `1400m` to admit the
second slice.

## 4. Clean up and disable the integration

Delete every DisaggregatedSet that Kueue manages, and wait until its Workloads
and Pods are gone, before you remove `DisaggregatedSet` from the frameworks:

```bash
oc delete -f 05-disaggregatedset.yaml
oc wait --for=delete workloads --all -n kueue-disaggregatedset-demo --timeout=5m
oc delete -f 04-localqueue.yaml -f 03-clusterqueue.yaml -f 02-resourceflavor.yaml
oc delete -f 01-namespace.yaml
```

Then remove `DisaggregatedSet` from `spec.config.integrations.frameworks` in
the `Kueue` CR.

## Limitations and cautions

- The integration is alpha.
- A managed DisaggregatedSet must use `maxSurge: 0`, which is the default.
  Kueue rejects changes to the Pod shape, such as resource requests, and changes
  to the `replicas` of a role while it runs. It also rejects several
  LeaderWorkerSet features, such as `subRoles`. See the support table in the
  upstream guide.
- Set the queue name and the `kueue.x-k8s.io/priority-class` label only on the
  DisaggregatedSet itself, not in role metadata or templates.
- When a slice is deleted, its quota is released at once, before its Pods
  terminate. For a short time, Pods of removed slices and Pods of newly
  admitted Workloads can run together beyond the ClusterQueue quota.
- After you scale every role to zero, wait until the slice Workloads are
  deleted before you scale up again.
- MultiKueue is not supported for DisaggregatedSets. Kueue does not reject such
  a DisaggregatedSet, and the behavior of its slices is not defined.
- With Topology Aware Scheduling, Kueue rejects a role template with TAS
  annotations combined with a `placementPolicy.type` other than `None`, and the
  `leaderworkerset.sigs.k8s.io/exclusive-topology` annotation on such a role.
  These checks look only at the TAS annotations of the role templates: in a
  ClusterQueue that has only TAS flavors, a DisaggregatedSet with a placement
  policy is accepted, and its Pods can stay pending while the slice holds quota.
- A mutating webhook that injects a sidecar makes Pods request more than their
  role template, and they stay gated with a `PodExceedsRoleRequests` event. Add
  the requests of injected containers to the role templates.
