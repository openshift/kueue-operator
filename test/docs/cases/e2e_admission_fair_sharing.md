# Test Case: Admission Fair Sharing

- **Test file:**
  [`test/e2e/e2e_admission_fair_sharing_test.go`](../../e2e/e2e_admission_fair_sharing_test.go)
- **Feature:** Admission Fair Sharing
- **JIRA:** [OCPSTRAT-2588](https://redhat.atlassian.net/browse/OCPSTRAT-2588)
- **Automation status:** Automated
- **Since release:** 1.4
- **Labels:** `admission-fair-sharing`

## Description

Admission Fair Sharing enables usage-based scheduling across LocalQueues within
a ClusterQueue. Instead of FIFO ordering, Kueue tracks historical resource
consumption per queue and prioritizes queues that have consumed fewer resources
relative to their weight. This ensures fair distribution of cluster capacity
among competing tenants.

These tests validate that the fair sharing mechanism correctly prioritizes
workloads based on queue weights, resource usage history, and configurable
resource scoring weights.

## Scenarios

1. [Higher-weight LocalQueue gets priority](#1-higher-weight-localqueue-gets-priority)
2. [Sampling interval controls lastUpdate cadence](#2-sampling-interval-controls-lastupdate-cadence)
3. [Visibility API reflects usage-based ordering](#3-visibility-api-reflects-usage-based-ordering)
4. [Resource weights customize scoring](#4-resource-weights-customize-scoring)

## Prerequisites

- Kueue operator installed and running
- AdmissionFairSharing enabled on the Kueue operand CR with:
  - `usageHalfLifeTimeSeconds: 60`
  - `usageSamplingIntervalSeconds: 5`
- Namespace with the `openshift-managed` label
- RBAC for visibility API access (scenario 3 only)

## Test Scenarios

### 1. Higher-weight LocalQueue gets priority

- **Ginkgo:** `When LocalQueues have different FairSharing weight values /
  should prioritize the higher-weight LocalQueue when quota frees up`

**Setup:**

1. Create a ResourceFlavor
2. Create a ClusterQueue with 2 CPUs, 200Mi memory, scope
   `UsageBasedAdmissionFairSharing`
3. Create a namespace with the `openshift-managed` label
4. Create LocalQueue `lq-heavy` with `fairSharingWeight: 1`
5. Create LocalQueue `lq-light` with `fairSharingWeight: 2`

**Execution:**

1. Submit `job-heavy-1` on `lq-heavy` requesting 1 CPU, 50Mi memory
2. Verify `job-heavy-1` workload is admitted:
   ```bash
   oc get workloads -n <namespace> -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.conditions[?(@.type=="Admitted")].status}{"\n"}{end}'
   # Expected: job-heavy-1 workload shows Admitted=True
   ```
3. Verify `job-heavy-1` pod is running:
   ```bash
   oc get pods -n <namespace> -l job-name=job-heavy-1 -o jsonpath='{.items[0].status.phase}'
   # Expected: Running
   ```
4. Submit `job-light-1` on `lq-light` requesting 1 CPU, 50Mi memory
5. Verify `job-light-1` workload is admitted and pod is running (same commands
   as steps 2-3)
6. Verify both LocalQueues show `consumedResources.cpu > 0`:
   ```bash
   oc get localqueue lq-heavy -n <namespace> -o jsonpath='{.status.fairSharing.admissionFairSharingStatus.consumedResources.cpu}'
   oc get localqueue lq-light -n <namespace> -o jsonpath='{.status.fairSharing.admissionFairSharingStatus.consumedResources.cpu}'
   # Expected: both values > 0
   ```
7. Submit `job-heavy-2` on `lq-heavy` requesting 800m CPU, 100Mi memory
8. Submit `job-light-2` on `lq-light` requesting 800m CPU, 50Mi memory
9. Verify both pending jobs are suspended:
   ```bash
   oc get job job-heavy-2 -n <namespace> -o jsonpath='{.spec.suspend}'
   oc get job job-light-2 -n <namespace> -o jsonpath='{.spec.suspend}'
   # Expected: both return true
   ```
10. Wait ~30 seconds and verify both jobs remain suspended (same command as
    step 9)
11. Delete `job-heavy-1` to free 1 CPU

**Expected Result:**

- `job-light-2` (lq-light, weight=2) is admitted:
  ```bash
  oc get job job-light-2 -n <namespace> -o jsonpath='{.spec.suspend}'
  # Expected: false
  ```
- `job-heavy-2` (lq-heavy, weight=1) remains suspended:
  ```bash
  oc get job job-heavy-2 -n <namespace> -o jsonpath='{.spec.suspend}'
  # Expected: true
  ```

**Cleanup:**

```bash
oc delete jobs --all -n <namespace>
oc delete localqueue lq-heavy lq-light -n <namespace>
oc delete clusterqueue <cq-name>
oc delete resourceflavor <rf-name>
oc delete namespace <namespace>
```

---

### 2. Sampling interval controls lastUpdate cadence

- **Ginkgo:** `When usageSamplingIntervalSeconds controls lastUpdate cadence /
  should advance lastUpdate timestamps at approximately the configured sampling
  interval`

**Setup:**

1. Create a ResourceFlavor
2. Create a ClusterQueue with 2 CPUs, 200Mi memory, scope
   `UsageBasedAdmissionFairSharing`
3. Create a namespace with the `openshift-managed` label
4. Create LocalQueue `lq-sampling`

**Execution:**

1. Submit `job-sampling` on `lq-sampling` requesting 1 CPU, 50Mi memory
2. Verify the workload is admitted and the pod is running:
   ```bash
   oc get workloads -n <namespace> -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.conditions[?(@.type=="Admitted")].status}{"\n"}{end}'
   # Expected: Admitted=True
   oc get pods -n <namespace> -l job-name=job-sampling -o jsonpath='{.items[0].status.phase}'
   # Expected: Running
   ```
3. Wait for the initial `lastUpdate` timestamp to appear:
   ```bash
   oc get localqueue lq-sampling -n <namespace> -o jsonpath='{.status.fairSharing.admissionFairSharingStatus.lastUpdate}'
   # Expected: non-empty timestamp
   ```
4. Record the `lastUpdate` timestamp
5. Wait ~5-10 seconds, then check that `lastUpdate` has advanced (same command
   as step 3) — record the new value
6. Repeat step 5 two more times (3 consecutive intervals total)
7. Calculate the interval between each pair of consecutive timestamps

**Expected Result:**

- Each interval between consecutive `lastUpdate` timestamps is approximately
  equal to the configured `usageSamplingIntervalSeconds` (5s), within a
  tolerance of -2s / +5s

**Cleanup:**

```bash
oc delete job job-sampling -n <namespace>
oc delete localqueue lq-sampling -n <namespace>
oc delete clusterqueue <cq-name>
oc delete resourceflavor <rf-name>
oc delete namespace <namespace>
```

---

### 3. Visibility API reflects usage-based ordering

- **Ginkgo:** `When VisibilityOnDemand reflects usage-based ordering / should
  report pending workloads in usage-based order, not FIFO`

**Setup:**

1. Create a ClusterRoleBinding for visibility API access (bind `default` SA in
   the operator namespace to `kueue-batch-admin-role`)
2. Create a ResourceFlavor
3. Create a ClusterQueue with 2 CPUs, 2Gi memory, scope
   `UsageBasedAdmissionFairSharing`
4. Create a namespace with the `openshift-managed` label
5. Create LocalQueue `lq1` (will accumulate usage)
6. Create LocalQueue `lq2` (will stay at zero usage)

**Execution:**

1. Submit `job1` on `lq1` requesting 1 CPU, 200Mi
2. Submit `job2` on `lq1` requesting 1 CPU, 200Mi
3. Verify both jobs are admitted and running:
   ```bash
   oc get workloads -n <namespace> -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.conditions[?(@.type=="Admitted")].status}{"\n"}{end}'
   # Expected: both workloads show Admitted=True
   ```
4. Verify ClusterQueue is saturated (2/2 CPU used):
   ```bash
   oc get clusterqueue <cq-name> -o jsonpath='{.status.flavorsUsage[0].resources[?(@.name=="cpu")].total}'
   # Expected: 2 (or equivalent)
   ```
5. Verify `lq1` has accumulated usage (`consumedResources.cpu > 0`):
   ```bash
   oc get localqueue lq1 -n <namespace> -o jsonpath='{.status.fairSharing.admissionFairSharingStatus.consumedResources.cpu}'
   # Expected: > 0
   ```
6. Verify `lq2` has zero CPU usage:
   ```bash
   oc get localqueue lq2 -n <namespace> -o jsonpath='{.status.fairSharing.admissionFairSharingStatus.consumedResources.cpu}'
   # Expected: 0
   ```
7. Submit `job3` on `lq1` (high-usage queue) requesting 1 CPU, 200Mi
8. Submit `job4` on `lq2` (zero-usage queue) requesting 1 CPU, 200Mi
9. Verify both `job3` and `job4` are suspended:
   ```bash
   oc get job job3 -n <namespace> -o jsonpath='{.spec.suspend}'
   oc get job job4 -n <namespace> -o jsonpath='{.spec.suspend}'
   # Expected: both true
   ```
10. Query the VisibilityOnDemand API and verify usage-based ordering:
    ```bash
    oc get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/clusterqueues/<cq-name>/pendingworkloads
    # Expected ordering:
    #   Position 0 (positionInClusterQueue: 0): job4 workload (lq2, zero usage)
    #   Position 1 (positionInClusterQueue: 1): job3 workload (lq1, high usage)
    ```
11. Delete `job1` to free 1 CPU

**Expected Result:**

- `job4` (lq2, position 0) is admitted:
  ```bash
  oc get job job4 -n <namespace> -o jsonpath='{.spec.suspend}'
  # Expected: false
  ```
- `job3` (lq1, position 1) remains suspended:
  ```bash
  oc get job job3 -n <namespace> -o jsonpath='{.spec.suspend}'
  # Expected: true
  ```

**Cleanup:**

```bash
oc delete jobs --all -n <namespace>
oc delete localqueue lq1 lq2 -n <namespace>
oc delete clusterqueue <cq-name>
oc delete resourceflavor <rf-name>
oc delete clusterrolebinding test-visibility
oc delete namespace <namespace>
```

---

### 4. Resource weights customize scoring

- **Ginkgo:** `When resourceWeights customizes which resources drive fair
  sharing scoring / should deprioritize the queue with higher weighted CPU usage
  when cpu weight is 10 and memory weight is 0`
- **Known issue:** Upstream bug kubernetes-sigs/kueue#10434 (OCPBUGS-85113) —
  memory resourceWeights are counted in raw bytes instead of GiB, so non-zero
  memory weights produce unintuitive scores. Workaround: set memory weight to
  "0".
- **Important:** This scenario reconfigures the global Kueue operand CR with
  different values (`usageHalfLifeTimeSeconds: 10`,
  `usageSamplingIntervalSeconds: 1`). If running scenarios out of order, ensure
  the Kueue config is set correctly before each scenario. The automated test
  saves and restores the original config via `DeferCleanup`.

**Setup:**

1. Save the current Kueue operand config (to restore later)
2. Configure Kueue operand with AdmissionFairSharing:
   - `usageHalfLifeTimeSeconds: 10`
   - `usageSamplingIntervalSeconds: 1`
   - `resourceWeights: [{name: cpu, weight: "10"}, {name: memory, weight: "0"}]`
3. Verify `kueue-manager-config` ConfigMap reflects the settings:
   ```bash
   oc get configmap kueue-manager-config -n openshift-kueue-operator -o jsonpath='{.data.controller_manager_config\.yaml}'
   # Expected: contains usageHalfLifeTime: 10s, usageSamplingInterval: 1s,
   #           cpu: 10, memory: 0
   ```
4. Create a ResourceFlavor
5. Create a ClusterQueue with 2 CPUs, 2Gi memory, scope
   `UsageBasedAdmissionFairSharing`
6. Create a namespace with the `openshift-managed` label
7. Create LocalQueue `lq-cpu-heavy` with `fairSharingWeight: 1`
8. Create LocalQueue `lq-cpu-light` with `fairSharingWeight: 1`

**Execution:**

1. Submit `job-heavy` on `lq-cpu-heavy` requesting 1500m CPU, 500Mi memory
2. Submit `job-light` on `lq-cpu-light` requesting 500m CPU, 1Mi memory
3. Verify both jobs are admitted and running:
   ```bash
   oc get workloads -n <namespace> -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.conditions[?(@.type=="Admitted")].status}{"\n"}{end}'
   # Expected: both workloads show Admitted=True
   oc get pods -n <namespace> -l job-name=job-heavy -o jsonpath='{.items[0].status.phase}'
   oc get pods -n <namespace> -l job-name=job-light -o jsonpath='{.items[0].status.phase}'
   # Expected: both Running
   ```
4. Verify both LocalQueues show `consumedResources.cpu > 0`:
   ```bash
   oc get localqueue lq-cpu-heavy -n <namespace> -o jsonpath='{.status.fairSharing.admissionFairSharingStatus.consumedResources.cpu}'
   oc get localqueue lq-cpu-light -n <namespace> -o jsonpath='{.status.fairSharing.admissionFairSharingStatus.consumedResources.cpu}'
   # Expected: both > 0
   ```
5. Submit `job-heavy-pending` on `lq-cpu-heavy` requesting 1500m CPU, 500Mi
6. Submit `job-light-pending` on `lq-cpu-light` requesting 1500m CPU, 1Mi
7. Verify both pending jobs are suspended:
   ```bash
   oc get job job-heavy-pending -n <namespace> -o jsonpath='{.spec.suspend}'
   oc get job job-light-pending -n <namespace> -o jsonpath='{.spec.suspend}'
   # Expected: both true
   ```
8. Wait ~30 seconds and verify both jobs remain suspended (same command as
   step 7)
9. Delete `job-heavy` to free 1500m CPU

**Expected Result:**

- `job-light-pending` (lq-cpu-light, lower weighted CPU usage) is admitted:
  ```bash
  oc get job job-light-pending -n <namespace> -o jsonpath='{.spec.suspend}'
  # Expected: false
  ```
- `job-heavy-pending` (lq-cpu-heavy, higher weighted CPU usage) remains
  suspended:
  ```bash
  oc get job job-heavy-pending -n <namespace> -o jsonpath='{.spec.suspend}'
  # Expected: true
  ```
- Memory usage does not affect the scoring (memory weight is 0)

**Cleanup:**

```bash
oc delete jobs --all -n <namespace>
oc delete localqueue lq-cpu-heavy lq-cpu-light -n <namespace>
oc delete clusterqueue <cq-name>
oc delete resourceflavor <rf-name>
oc delete namespace <namespace>
oc apply -f kueue-config-backup.yaml
```
