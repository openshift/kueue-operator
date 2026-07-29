# Test Case: DRA Extended Resources Alpha-2

- **Test file:**
  [`test/e2e/e2e_dra_er_alpha2_test.go`](../../e2e/e2e_dra_er_alpha2_test.go)
- **Feature:** DRA Extended Resources — Late DeviceClass Creation
- **JIRA:** [OCPKUEUE-657](https://redhat.atlassian.net/browse/OCPKUEUE-657)
- **KEP:** [2941-DRA — Late DeviceClass Creation](https://github.com/kubernetes-sigs/kueue/tree/main/keps/2941-DRA#late-deviceclass-creation)
- **Automation status:** Partial (S1, S2 automated)
- **Since release:** TBD
- **Labels:** `dra`, `dra-extended-resources`

## Description

When a DeviceClass has `extendedResourceName` set, Kueue uses the DRA path to
manage GPU workloads instead of the traditional extended resource path. These
tests validate Kueue's behavior when the DeviceClass lifecycle changes relative
to workload submission — specifically what happens when no DeviceClass exists,
when one is created late, when one is deleted, and when its
`extendedResourceName` is updated.

This is a stateful test sequence: S1 and S2 share state (the workload created
in S1 persists into S2 where the DeviceClass creation triggers DRA activation).
S3 and S4 are independent but require a DeviceClass to exist at the start.

## Scenarios

1. [No DeviceClass — workload uses non-DRA path](#1-no-deviceclass--workload-uses-non-dra-path)
2. [Late DeviceClass creation — DRA activation via scheduler](#2-late-deviceclass-creation--dra-activation-via-scheduler)
3. [DeviceClass deleted — pending workloads not requeued](#3-deviceclass-deleted--pending-workloads-not-requeued)
4. [Update extendedResourceName — affected workloads not requeued](#4-update-extendedresourcename--affected-workloads-not-requeued)

## Prerequisites

- OpenShift cluster with DRA feature gates enabled (`DynamicResourceAllocation`,
  `DRAResourceClaimDeviceStatus`, `DRAExtendedResource`)
- NVIDIA GPU nodes available (e.g., Tesla T4)
- NVIDIA DRA driver installed (ResourceSlices available for `gpu.nvidia.com`)
- Kueue operator installed and running
- DeviceClass `gpu.nvidia.com` must NOT exist at the start of S1 (delete it if
  present)
- Kueue operand should NOT have `deviceClassMappings` configured

Verify prerequisites:

```bash
oc get featuregate cluster -o yaml | grep DRAExtendedResource
# Expected: DRAExtendedResource in the enabled list

oc get resourceslices -o jsonpath='{range .items[?(@.spec.driver=="gpu.nvidia.com")]}{.metadata.name}{"\n"}{end}'
# Expected: at least one ResourceSlice from gpu.nvidia.com

oc get kueue cluster -o jsonpath='{.spec.config.resources}'
# Expected: empty (no deviceClassMappings)
```

## Shared Setup

Create the test namespace and Kueue resources used by all scenarios:

```bash
oc create namespace kueue-er-alpha2
oc label namespace kueue-er-alpha2 kueue.openshift.io/managed="true"

cat <<'EOF' | oc apply -f -
apiVersion: kueue.x-k8s.io/v1beta2
kind: ResourceFlavor
metadata:
  name: gpu-nodes
spec:
  nodeLabels:
    nvidia.com/gpu.present: "true"
---
apiVersion: kueue.x-k8s.io/v1beta2
kind: ClusterQueue
metadata:
  name: gpu-cluster-queue
spec:
  namespaceSelector: {}
  resourceGroups:
  - coveredResources: [cpu, memory, nvidia.com/gpu]
    flavors:
    - name: gpu-nodes
      resources:
      - name: cpu
        nominalQuota: 4
      - name: memory
        nominalQuota: "4Gi"
      - name: nvidia.com/gpu
        nominalQuota: 2
---
apiVersion: kueue.x-k8s.io/v1beta2
kind: LocalQueue
metadata:
  name: test-queue
  namespace: kueue-er-alpha2
spec:
  clusterQueue: gpu-cluster-queue
EOF
```

## Test Scenarios

### 1. No DeviceClass — workload uses non-DRA path

- **Ginkgo:** `When no DeviceClass with extendedResourceName exists / should
  admit workload via non-DRA extended resource path but leave pod pending`
- **Type:** Negative test — validates fallback behavior when DRA is not
  configured
- **Important:** S1 and S2 are sequential — the workload created here persists
  into S2.

**Setup:**

1. Delete DeviceClass `gpu.nvidia.com` if it exists

**Execution:**

1. Verify DeviceClass does not exist:
   ```bash
   oc get deviceclass gpu.nvidia.com
   # Expected: NotFound
   ```
2. Submit a job requesting `nvidia.com/gpu: 1`:
   ```bash
   cat <<'EOF' | oc apply -f -
   apiVersion: batch/v1
   kind: Job
   metadata:
     name: s1-no-deviceclass
     namespace: kueue-er-alpha2
     labels:
       kueue.x-k8s.io/queue-name: test-queue
   spec:
     template:
       spec:
         restartPolicy: Never
         securityContext:
           seccompProfile:
             type: RuntimeDefault
         containers:
         - name: test
           image: registry.k8s.io/e2e-test-images/agnhost:2.53
           command: ["/bin/sh"]
           args: ["-c", "sleep 60"]
           resources:
             requests:
               cpu: "100m"
               memory: "100Mi"
               nvidia.com/gpu: 1
             limits:
               nvidia.com/gpu: 1
   EOF
   ```
3. Verify the workload is admitted (non-DRA extended resource path):
   ```bash
   oc get workloads -n kueue-er-alpha2 -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.conditions[?(@.type=="Admitted")].status}{"\n"}{end}'
   # Expected: Admitted=True (Kueue treats nvidia.com/gpu as a regular ER)
   ```
4. Verify the job is unsuspended:
   ```bash
   oc get job s1-no-deviceclass -n kueue-er-alpha2 -o jsonpath='{.spec.suspend}'
   # Expected: false
   ```
5. Verify the pod stays Pending (GPU only available via DRA, no device plugin):
   ```bash
   oc get pods -n kueue-er-alpha2 -l job-name=s1-no-deviceclass -o jsonpath='{.items[0].status.phase}'
   # Expected: Pending (consistently — no device plugin advertises nvidia.com/gpu)
   ```
6. Verify no ResourceClaim was created (non-DRA path):
   ```bash
   oc get resourceclaim -n kueue-er-alpha2
   # Expected: No resources found
   ```
7. Verify ClusterQueue shows nvidia.com/gpu quota consumed:
   ```bash
   oc get clusterqueue gpu-cluster-queue -o jsonpath='{.status.flavorsUsage}'
   # Expected: nvidia.com/gpu total=1
   ```

**Expected Result:**

- Workload is admitted via non-DRA extended resource path (Admitted=True)
- Job is unsuspended
- Pod stays Pending (no device plugin for nvidia.com/gpu)
- No ResourceClaim created
- ClusterQueue quota consumed (nvidia.com/gpu=1)

**Cleanup:**

Do NOT clean up — S2 depends on the workload created here.

---

### 2. Late DeviceClass creation — DRA activation via scheduler

- **Ginkgo:** `When DeviceClass is created after workload submission / should
  transition pod to running via scheduler-level DRA activation`
- **Important:** Depends on S1 — the workload from S1 must still be pending.
  Kueue does NOT requeue the workload; the DRA path activates at the scheduler
  level after ~2 min backoff.

**Setup:**

1. Verify the workload from S1 is still present and the pod is Pending

**Execution:**

1. Verify DeviceClass does not exist:
   ```bash
   oc get deviceclass gpu.nvidia.com
   # Expected: NotFound
   ```
2. Create DeviceClass with `extendedResourceName`:
   ```bash
   cat <<'EOF' | oc apply -f -
   apiVersion: resource.k8s.io/v1
   kind: DeviceClass
   metadata:
     name: gpu.nvidia.com
   spec:
     extendedResourceName: nvidia.com/gpu
     selectors:
     - cel:
         expression: "device.driver == 'gpu.nvidia.com' && device.attributes['gpu.nvidia.com'].type == 'gpu'"
   EOF
   ```
3. Verify DeviceClass was created:
   ```bash
   oc get deviceclass gpu.nvidia.com -o jsonpath='{.spec.extendedResourceName}'
   # Expected: nvidia.com/gpu
   ```
4. Wait ~2 minutes for scheduler backoff retry, then verify the pod transitions
   to Running:
   ```bash
   oc get pods -n kueue-er-alpha2 -l job-name=s1-no-deviceclass -o jsonpath='{.items[0].status.phase}'
   # Expected: Running (after scheduler retry with new DeviceClass)
   ```
5. Verify a ResourceClaim was created (DRA path activated by scheduler):
   ```bash
   oc get resourceclaim -n kueue-er-alpha2
   # Expected: ResourceClaim exists with STATE=allocated,reserved
   ```

**Expected Result:**

- DeviceClass creation does NOT trigger Kueue-level requeue (workload was
  already admitted in S1)
- Scheduler-level DRA activation creates a ResourceClaim after backoff retry
- Pod transitions from Pending to Running (~2 min delay)
- ResourceClaim is allocated and reserved

**Cleanup:**

```bash
oc delete job s1-no-deviceclass -n kueue-er-alpha2
# Wait for workload and CQ usage to drain
sleep 10
oc get workloads -n kueue-er-alpha2
# Expected: no workloads
oc get clusterqueue gpu-cluster-queue -o jsonpath='{.status.flavorsUsage}'
# Expected: nvidia.com/gpu total=0
```

---

### 3. DeviceClass deleted — pending workloads not requeued

- **Ginkgo:** TBD (not yet automated)
- **Type:** Negative test — validates that Kueue does NOT react to DeviceClass
  deletion
- **Known issue:** Kueue does not watch for DeviceClass deletion events. KEP
  2941-DRA "Late DeviceClass Creation" only covers creation, not deletion.

**Setup:**

1. Verify DeviceClass `gpu.nvidia.com` exists with
   `extendedResourceName: nvidia.com/gpu`

**Execution:**

1. Submit two jobs to exhaust GPU quota (2 GPUs):
   ```bash
   cat <<'EOF' | oc apply -f -
   apiVersion: batch/v1
   kind: Job
   metadata:
     name: s3-fill-gpu-1
     namespace: kueue-er-alpha2
     labels:
       kueue.x-k8s.io/queue-name: test-queue
   spec:
     template:
       spec:
         restartPolicy: Never
         securityContext:
           seccompProfile:
             type: RuntimeDefault
         containers:
         - name: test
           image: registry.k8s.io/e2e-test-images/agnhost:2.53
           command: ["/bin/sh"]
           args: ["-c", "sleep 900"]
           resources:
             requests:
               cpu: "100m"
               memory: "100Mi"
               nvidia.com/gpu: 1
             limits:
               nvidia.com/gpu: 1
   ---
   apiVersion: batch/v1
   kind: Job
   metadata:
     name: s3-fill-gpu-2
     namespace: kueue-er-alpha2
     labels:
       kueue.x-k8s.io/queue-name: test-queue
   spec:
     template:
       spec:
         restartPolicy: Never
         securityContext:
           seccompProfile:
             type: RuntimeDefault
         containers:
         - name: test
           image: registry.k8s.io/e2e-test-images/agnhost:2.53
           command: ["/bin/sh"]
           args: ["-c", "sleep 900"]
           resources:
             requests:
               cpu: "100m"
               memory: "100Mi"
               nvidia.com/gpu: 1
             limits:
               nvidia.com/gpu: 1
   EOF
   ```
2. Verify both jobs are admitted and pods are running:
   ```bash
   oc get workloads -n kueue-er-alpha2
   # Expected: both admitted
   oc get pods -n kueue-er-alpha2
   # Expected: both Running
   ```
3. Verify CQ usage shows quota exhausted:
   ```bash
   oc get clusterqueue gpu-cluster-queue -o jsonpath='{.status.flavorsUsage}'
   # Expected: nvidia.com/gpu total=2
   ```
4. Submit a third job requesting 1 GPU (will be pending — quota full):
   ```bash
   cat <<'EOF' | oc apply -f -
   apiVersion: batch/v1
   kind: Job
   metadata:
     name: s3-pending-job
     namespace: kueue-er-alpha2
     labels:
       kueue.x-k8s.io/queue-name: test-queue
   spec:
     template:
       spec:
         restartPolicy: Never
         securityContext:
           seccompProfile:
             type: RuntimeDefault
         containers:
         - name: test
           image: registry.k8s.io/e2e-test-images/agnhost:2.53
           command: ["/bin/sh"]
           args: ["-c", "sleep 60"]
           resources:
             requests:
               cpu: "100m"
               memory: "100Mi"
               nvidia.com/gpu: 1
             limits:
               nvidia.com/gpu: 1
   EOF
   ```
5. Verify third job is pending (not admitted):
   ```bash
   oc get workloads -n kueue-er-alpha2
   # Expected: s3-pending-job NOT admitted
   ```
6. Record the pending workload's `resourceVersion` and `lastTransitionTime`:
   ```bash
   oc get workload -n kueue-er-alpha2 -o custom-columns='NAME:.metadata.name,RESOURCE_VERSION:.metadata.resourceVersion,LAST_TRANSITION:.status.conditions[?(@.type=="QuotaReserved")].lastTransitionTime'
   ```
7. Delete DeviceClass `gpu.nvidia.com`
8. Wait ~15 seconds, then check if the pending workload was requeued:
   ```bash
   oc get workload -n kueue-er-alpha2 -o custom-columns='NAME:.metadata.name,RESOURCE_VERSION:.metadata.resourceVersion,LAST_TRANSITION:.status.conditions[?(@.type=="QuotaReserved")].lastTransitionTime'
   # Expected (known issue): resourceVersion and lastTransitionTime UNCHANGED
   ```
9. Check controller logs for requeue activity:
   ```bash
   oc logs -n openshift-kueue-operator deployment/kueue-controller-manager --since=5m | grep -i "requeue\|deviceclass\|extended.resource"
   # Expected (known issue): NO requeue entries
   ```

**Expected Result:**

- **Known issue:** DeviceClass deletion does NOT trigger requeue of pending
  workloads
- Pending workload `resourceVersion` and `lastTransitionTime` remain unchanged
- Running jobs are not affected
- No controller log entries for DeviceClass deletion

**Cleanup:**

```bash
oc delete job s3-fill-gpu-1 s3-fill-gpu-2 s3-pending-job -n kueue-er-alpha2
sleep 15
# Recreate DeviceClass for S4
cat <<'EOF' | oc apply -f -
apiVersion: resource.k8s.io/v1
kind: DeviceClass
metadata:
  name: gpu.nvidia.com
spec:
  extendedResourceName: nvidia.com/gpu
  selectors:
  - cel:
      expression: "device.driver == 'gpu.nvidia.com' && device.attributes['gpu.nvidia.com'].type == 'gpu'"
EOF
```

---

### 4. Update extendedResourceName — affected workloads not requeued

- **Ginkgo:** TBD (not yet automated)
- **Type:** Negative test — validates that Kueue does NOT react to DeviceClass
  updates
- **Known issue:** Combined with S3, only DeviceClass creation triggers
  scheduler-level DRA activation. Updates and deletions are not watched by
  Kueue.

**Setup:**

1. Verify DeviceClass `gpu.nvidia.com` exists with
   `extendedResourceName: nvidia.com/gpu`

**Execution:**

1. Submit a job requesting 2 GPUs to exhaust quota:
   ```bash
   cat <<'EOF' | oc apply -f -
   apiVersion: batch/v1
   kind: Job
   metadata:
     name: s4-fill-gpu
     namespace: kueue-er-alpha2
     labels:
       kueue.x-k8s.io/queue-name: test-queue
   spec:
     template:
       spec:
         restartPolicy: Never
         securityContext:
           seccompProfile:
             type: RuntimeDefault
         containers:
         - name: test
           image: registry.k8s.io/e2e-test-images/agnhost:2.53
           command: ["/bin/sh"]
           args: ["-c", "sleep 900"]
           resources:
             requests:
               cpu: "100m"
               memory: "100Mi"
               nvidia.com/gpu: 2
             limits:
               nvidia.com/gpu: 2
   EOF
   ```
2. Verify the job is admitted and CQ quota is exhausted:
   ```bash
   oc get workloads -n kueue-er-alpha2
   # Expected: admitted
   oc get clusterqueue gpu-cluster-queue -o jsonpath='{.status.flavorsUsage}'
   # Expected: nvidia.com/gpu total=2
   ```
3. Submit a GPU-pending job (quota full):
   ```bash
   cat <<'EOF' | oc apply -f -
   apiVersion: batch/v1
   kind: Job
   metadata:
     name: s4-gpu-pending
     namespace: kueue-er-alpha2
     labels:
       kueue.x-k8s.io/queue-name: test-queue
   spec:
     template:
       spec:
         restartPolicy: Never
         securityContext:
           seccompProfile:
             type: RuntimeDefault
         containers:
         - name: test
           image: registry.k8s.io/e2e-test-images/agnhost:2.53
           command: ["/bin/sh"]
           args: ["-c", "sleep 60"]
           resources:
             requests:
               cpu: "100m"
               memory: "100Mi"
               nvidia.com/gpu: 1
             limits:
               nvidia.com/gpu: 1
   EOF
   ```
4. Submit a CPU-only pending job (cpu quota full):
   ```bash
   cat <<'EOF' | oc apply -f -
   apiVersion: batch/v1
   kind: Job
   metadata:
     name: s4-cpu-only
     namespace: kueue-er-alpha2
     labels:
       kueue.x-k8s.io/queue-name: test-queue
   spec:
     template:
       spec:
         restartPolicy: Never
         securityContext:
           seccompProfile:
             type: RuntimeDefault
         containers:
         - name: test
           image: registry.k8s.io/e2e-test-images/agnhost:2.53
           command: ["/bin/sh"]
           args: ["-c", "sleep 60"]
           resources:
             requests:
               cpu: "4"
               memory: "100Mi"
   EOF
   ```
5. Verify both pending jobs are not admitted:
   ```bash
   oc get workloads -n kueue-er-alpha2
   # Expected: s4-gpu-pending and s4-cpu-only NOT admitted
   ```
6. Record `resourceVersion` and `lastTransitionTime` for both pending workloads:
   ```bash
   oc get workload -n kueue-er-alpha2 -o custom-columns='NAME:.metadata.name,RESOURCE_VERSION:.metadata.resourceVersion,LAST_TRANSITION:.status.conditions[?(@.type=="QuotaReserved")].lastTransitionTime'
   ```
7. Update DeviceClass `extendedResourceName` to a different value:
   ```bash
   oc patch deviceclass gpu.nvidia.com --type='merge' -p '{"spec":{"extendedResourceName":"nvidia.com/gpu-updated"}}'
   ```
8. Verify the update was applied:
   ```bash
   oc get deviceclass gpu.nvidia.com -o jsonpath='{.spec.extendedResourceName}'
   # Expected: nvidia.com/gpu-updated
   ```
9. Wait ~30 seconds, then check if workloads were requeued:
   ```bash
   oc get workload -n kueue-er-alpha2 -o custom-columns='NAME:.metadata.name,RESOURCE_VERSION:.metadata.resourceVersion,LAST_TRANSITION:.status.conditions[?(@.type=="QuotaReserved")].lastTransitionTime'
   # Expected (known issue): BOTH unchanged — neither GPU nor CPU workload requeued
   ```
10. Check controller logs:
    ```bash
    oc logs -n openshift-kueue-operator deployment/kueue-controller-manager --since=5m | grep -i "requeue\|deviceclass\|extended.resource"
    # Expected (known issue): NO requeue entries
    ```

**Expected Result:**

- **Known issue:** DeviceClass update does NOT trigger targeted requeue
- GPU-pending workload `resourceVersion` and `lastTransitionTime` unchanged
- CPU-only workload also unchanged (correctly — but for the wrong reason, since
  neither was requeued)

**Cleanup:**

```bash
oc delete job s4-fill-gpu s4-gpu-pending s4-cpu-only -n kueue-er-alpha2
sleep 15
# Restore extendedResourceName to original value
oc patch deviceclass gpu.nvidia.com --type='merge' -p '{"spec":{"extendedResourceName":"nvidia.com/gpu"}}'
```

## Full Cleanup

```bash
oc delete namespace kueue-er-alpha2
oc delete clusterqueue gpu-cluster-queue
oc delete resourceflavor gpu-nodes
oc patch deviceclass gpu.nvidia.com --type='merge' -p '{"spec":{"extendedResourceName":"nvidia.com/gpu"}}'
```
