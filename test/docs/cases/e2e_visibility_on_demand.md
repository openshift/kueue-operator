# Test Case: Visibility On Demand

- **Test file:** [`test/e2e/e2e_visibility_on_demand_test.go`](../../e2e/e2e_visibility_on_demand_test.go)
- **Feature:** VisibilityOnDemand
- **JIRA:** TBD
- **Automation status:** Automated
- **Since release:** TBD
- **Labels:** `visibility-on-demand`

## Description

The Visibility On Demand feature provides the ability to query pending workloads through the Kueue Visibility API (`visibility.kueue.x-k8s.io/v1beta2`). These tests validate that the PriorityLevelConfiguration for the visibility API can be modified within allowed bounds, that pending workloads are correctly ordered by priority, that RBAC controls restrict access appropriately across ClusterQueues and LocalQueues, and that the correct FlowSchema and PriorityLevelConfiguration are used for API requests. Tests also cover LeaderWorkerSet (LWS) and JobSet workloads appearing in pending workload lists.

## Scenarios

1. [Allow nominal concurrency shares set to 0](#1-allow-nominal-concurrency-shares-set-to-0)
2. [Allow nominal concurrency shares set to 5](#2-allow-nominal-concurrency-shares-set-to-5)
3. [Reject nominal concurrency shares set to 1](#3-reject-nominal-concurrency-shares-set-to-1)
4. [Reject nominal concurrency shares set to 6](#4-reject-nominal-concurrency-shares-set-to-6)
5. [Non-admin cannot modify nominal concurrency shares](#5-non-admin-cannot-modify-nominal-concurrency-shares)
6. [ClusterQueue pending workloads with admin access and priority ordering](#6-clusterqueue-pending-workloads-with-admin-access-and-priority-ordering)
7. [LocalQueue access scoped to bound namespaces](#7-localqueue-access-scoped-to-bound-namespaces)
8. [PriorityLevelConfiguration and FlowSchema for visibility endpoints](#8-prioritylevelconfiguration-and-flowschema-for-visibility-endpoints)
9. [LWS pending workloads ordered by priority with sequential admission](#9-lws-pending-workloads-ordered-by-priority-with-sequential-admission)
10. [Suspended JobSet shows on pending workloads list](#10-suspended-jobset-shows-on-pending-workloads-list)

## Prerequisites

- Kueue operator is installed and running in `openshift-kueue-operator` namespace
- The `kueue-visibility` PriorityLevelConfiguration exists (created by the operator)
- The `visibility` FlowSchema exists (created by the operator)
- ClusterRoles `kueue-batch-admin-role` and `kueue-batch-user-role` exist
- LeaderWorkerSet CRD is installed (for scenario 9)
- JobSet CRD is installed (for scenario 10)

## Shared Setup (Scenarios 1-5)

These scenarios share setup via `BeforeAll`:

1. Create a namespace with label `kueue.openshift.io/managed: "true"`
2. Create a ClusterQueue, LocalQueue (`test-queue`), and ResourceFlavor
3. Create a RoleBinding granting `kueue-batch-user-role` to the `default` service account in `openshift-kueue-operator` namespace for the test namespace

## Test Scenarios

### 1. Allow nominal concurrency shares set to 0

- **Ginkgo:** `When kueue.openshift.io/allow-nominal-concurrency-shares-update annotation is set to true / It should allow modification of the nominal concurrency shares to 0`
- **Important:** Modifies cluster-wide PriorityLevelConfiguration

**Setup:**

No additional setup beyond shared setup.

**Execution:**

1. Modify the `kueue-visibility` PriorityLevelConfiguration: set `nominalConcurrencyShares` to `0` and add annotation `kueue.openshift.io/allow-nominal-concurrency-shares-update: "true"`

```bash
oc annotate prioritylevelconfiguration kueue-visibility \
  kueue.openshift.io/allow-nominal-concurrency-shares-update=true --overwrite
oc patch prioritylevelconfiguration kueue-visibility --type=merge \
  -p '{"spec":{"limited":{"nominalConcurrencyShares":0}}}'
```

2. Verify that the nominal concurrency shares value is `0`

```bash
oc get prioritylevelconfiguration kueue-visibility \
  -o jsonpath='{.spec.limited.nominalConcurrencyShares}'
# Expected: 0
```

3. Attempt to access pending workloads via the visibility API

```bash
oc get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/namespaces/<namespace>/localqueues/test-queue/pendingworkloads
# Expected: HTTP 429 Too Many Requests (rate limited because concurrency shares = 0)
```

**Expected Result:**

- The PriorityLevelConfiguration is updated to `nominalConcurrencyShares: 0`
- Requests to the visibility API return `429 Too Many Requests` because the concurrency is fully restricted

**Cleanup:**

The operator will reconcile the PriorityLevelConfiguration back to defaults.

---

### 2. Allow nominal concurrency shares set to 5

- **Ginkgo:** `When kueue.openshift.io/allow-nominal-concurrency-shares-update annotation is set to true / It should allow modification of the nominal concurrency shares to 5`
- **Important:** Modifies cluster-wide PriorityLevelConfiguration

**Setup:**

No additional setup beyond shared setup.

**Execution:**

1. Modify the `kueue-visibility` PriorityLevelConfiguration: set `nominalConcurrencyShares` to `5` with the allow annotation

```bash
oc annotate prioritylevelconfiguration kueue-visibility \
  kueue.openshift.io/allow-nominal-concurrency-shares-update=true --overwrite
oc patch prioritylevelconfiguration kueue-visibility --type=merge \
  -p '{"spec":{"limited":{"nominalConcurrencyShares":5}}}'
```

2. Access the pending workloads via the visibility API

```bash
oc get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/namespaces/<namespace>/localqueues/test-queue/pendingworkloads
# Expected: HTTP 200 OK (empty list of pending workloads)
```

3. Access the pending workloads again to confirm continued access

```bash
oc get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/namespaces/<namespace>/localqueues/test-queue/pendingworkloads
# Expected: HTTP 200 OK
```

**Expected Result:**

- The PriorityLevelConfiguration is updated to `nominalConcurrencyShares: 5`
- Requests to the visibility API succeed with HTTP 200

**Cleanup:**

The operator will reconcile the PriorityLevelConfiguration back to defaults.

---

### 3. Reject nominal concurrency shares set to 1

- **Ginkgo:** `When kueue.openshift.io/allow-nominal-concurrency-shares-update annotation is set to true / It should not allow modification of the nominal concurrency shares to 1`
- **Important:** Modifies cluster-wide PriorityLevelConfiguration; operator reconciles it back to default (2)

**Setup:**

No additional setup beyond shared setup.

**Execution:**

1. Modify the `kueue-visibility` PriorityLevelConfiguration: set `nominalConcurrencyShares` to `1` with the allow annotation

```bash
oc annotate prioritylevelconfiguration kueue-visibility \
  kueue.openshift.io/allow-nominal-concurrency-shares-update=true --overwrite
oc patch prioritylevelconfiguration kueue-visibility --type=merge \
  -p '{"spec":{"limited":{"nominalConcurrencyShares":1}}}'
```

2. Wait and verify that the operator reconciles the value back to the default (`2`)

```bash
# Poll until the value reverts
oc get prioritylevelconfiguration kueue-visibility \
  -o jsonpath='{.spec.limited.nominalConcurrencyShares}'
# Expected: 2 (operator reconciles back to default)
```

**Expected Result:**

- The value `1` is outside the allowed range; the operator automatically reverts `nominalConcurrencyShares` to the default value of `2`

**Cleanup:**

No cleanup needed; operator auto-reconciles.

---

### 4. Reject nominal concurrency shares set to 6

- **Ginkgo:** `When kueue.openshift.io/allow-nominal-concurrency-shares-update annotation is set to true / It should not allow modification of the nominal concurrency shares to 6`
- **Important:** Modifies cluster-wide PriorityLevelConfiguration; operator reconciles it back to default (2)

**Setup:**

No additional setup beyond shared setup.

**Execution:**

1. Modify the `kueue-visibility` PriorityLevelConfiguration: set `nominalConcurrencyShares` to `6` with the allow annotation

```bash
oc annotate prioritylevelconfiguration kueue-visibility \
  kueue.openshift.io/allow-nominal-concurrency-shares-update=true --overwrite
oc patch prioritylevelconfiguration kueue-visibility --type=merge \
  -p '{"spec":{"limited":{"nominalConcurrencyShares":6}}}'
```

2. Wait and verify that the operator reconciles the value back to the default (`2`)

```bash
# Poll until the value reverts
oc get prioritylevelconfiguration kueue-visibility \
  -o jsonpath='{.spec.limited.nominalConcurrencyShares}'
# Expected: 2 (operator reconciles back to default)
```

**Expected Result:**

- The value `6` is outside the allowed range; the operator automatically reverts `nominalConcurrencyShares` to the default value of `2`

**Cleanup:**

No cleanup needed; operator auto-reconciles.

---

### 5. Non-admin cannot modify nominal concurrency shares

- **Ginkgo:** `When kueue.openshift.io/allow-nominal-concurrency-shares-update annotation is set to true / It should not allow modification of the nominal concurrency shares if it's not admin`
- **Type:** Negative test

**Setup:**

No additional setup beyond shared setup. The test impersonates the `deployer` service account in the test namespace.

**Execution:**

1. As the `deployer` service account (non-admin), attempt to modify the `kueue-visibility` PriorityLevelConfiguration with `nominalConcurrencyShares` set to `4`

```bash
oc --as=system:serviceaccount:<namespace>:deployer \
  patch prioritylevelconfiguration kueue-visibility --type=merge \
  -p '{"metadata":{"annotations":{"kueue.openshift.io/allow-nominal-concurrency-shares-update":"true"}},"spec":{"limited":{"nominalConcurrencyShares":4}}}'
# Expected: Error - Forbidden
```

**Expected Result:**

- The API returns a `403 Forbidden` error; non-admin users cannot modify PriorityLevelConfigurations

**Cleanup:**

No cleanup needed; the update was rejected.

---

### 6. ClusterQueue pending workloads with admin access and priority ordering

- **Ginkgo:** `When PendingWorkloads list should be checked for a ClusterQueue and LocalQueue / It Should allow admin to access ClusterQueues, deny user access, and order pending workloads by priority`

**Setup:**

1. Create a ResourceFlavor

```bash
cat <<'EOF' | oc apply -f -
apiVersion: kueue.x-k8s.io/v1beta1
kind: ResourceFlavor
metadata:
  generateName: resource-flavor-
EOF
```

2. Create two ClusterQueues (A and B) each with 2 CPU and 1Gi memory quota referencing the ResourceFlavor

```bash
cat <<'EOF' | oc apply -f -
apiVersion: kueue.x-k8s.io/v1beta1
kind: ClusterQueue
metadata:
  generateName: cluster-queue-
spec:
  resourceGroups:
  - coveredResources: ["cpu", "memory"]
    flavors:
    - name: <resource-flavor-name>
      resources:
      - name: cpu
        nominalQuota: "2"
      - name: memory
        nominalQuota: 1Gi
EOF
```

3. Create two namespaces (A and B) with `kueue.openshift.io/managed: "true"` label
4. Create LocalQueue-A in namespace-A pointing to ClusterQueue-A, and LocalQueue-B in namespace-B pointing to ClusterQueue-B
5. Create three PriorityClasses: high (value=100), medium (value=75), low (value=50)
6. Create a ServiceAccount and bind it to `kueue-batch-admin-role` via ClusterRoleBinding
7. Create a ServiceAccount and bind it to `kueue-batch-user-role` via ClusterRoleBinding

**Execution:**

1. Create a blocker job in namespace-A on LocalQueue-A with high priority requesting 2 CPU, 1Gi (consumes all quota)
2. Create three pending jobs in namespace-A on LocalQueue-A: `job-high-a` (high, 1 CPU), `job-medium-a` (medium, 1 CPU), `job-low-a` (low, 1 CPU)
3. Create a blocker job in namespace-B on LocalQueue-B with high priority requesting 2 CPU, 1Gi
4. Create a pending job in namespace-B on LocalQueue-B: `job-high-b` (high, 1 CPU)
5. Wait for blocker job pods to be created in namespace-A
6. Query pending workloads for ClusterQueue-A using the admin visibility client

```bash
oc get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/clusterqueues/<cluster-queue-a>/pendingworkloads
# Expected: 3 items, ordered by priority: 100, 75, 50
```

7. Wait for blocker job pods in namespace-B, then query pending workloads for ClusterQueue-B

```bash
oc get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/clusterqueues/<cluster-queue-b>/pendingworkloads
# Expected: 1 item with priority 100
```

8. Verify all workloads are eventually created
9. Verify pending workloads lists become empty after all workloads complete

```bash
oc get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/clusterqueues/<cluster-queue-a>/pendingworkloads
# Expected: empty items list

oc get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/clusterqueues/<cluster-queue-b>/pendingworkloads
# Expected: empty items list
```

10. Verify an unauthorized user (not-authorized-user) cannot access ClusterQueue pending workloads

```bash
oc --as=system:serviceaccount:<namespace-a>:not-authorized-user \
  get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/clusterqueues/<cluster-queue-a>/pendingworkloads
# Expected: 403 Forbidden
```

11. Verify a user with `kueue-batch-user-role` cannot access ClusterQueue pending workloads

```bash
oc --as=system:serviceaccount:<namespace-a>:<user-sa> \
  get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/clusterqueues/<cluster-queue-a>/pendingworkloads
# Expected: 403 Forbidden
```

**Expected Result:**

- Pending workloads are returned ordered by priority (highest first)
- 3 pending workloads on ClusterQueue-A, 1 on ClusterQueue-B
- All pending workloads lists become empty after jobs complete
- Unauthorized users get `403 Forbidden`
- Users with `kueue-batch-user-role` (not admin) get `403 Forbidden` on ClusterQueue endpoints

**Cleanup:**

```bash
oc delete job job-blocker job-high-a job-medium-a job-low-a -n <namespace-a>
oc delete job job-blocker-b job-high-b -n <namespace-b>
oc delete clusterrolebinding <admin-binding> <user-binding>
oc delete localqueue local-queue-a -n <namespace-a>
oc delete localqueue local-queue-b -n <namespace-b>
oc delete namespace <namespace-a> <namespace-b>
oc delete clusterqueue <cluster-queue-a> <cluster-queue-b>
oc delete resourceflavor <resource-flavor>
oc delete priorityclass <high> <medium> <low>
```

---

### 7. LocalQueue access scoped to bound namespaces

- **Ginkgo:** `When PendingWorkloads list should be checked for a ClusterQueue and LocalQueue / It Should allow access to LocalQueues in bound namespaces and deny access to unbound namespaces`

**Setup:**

1. Create a ResourceFlavor
2. Create a single ClusterQueue with 2 CPU and 1Gi memory quota
3. Create two namespaces (A and B) with `kueue.openshift.io/managed: "true"` label
4. Create LocalQueue-A in namespace-A and LocalQueue-B in namespace-B, both pointing to the same ClusterQueue
5. Create a ServiceAccount in each namespace and bind each to `kueue-batch-user-role` via RoleBinding (namespace-scoped)
6. Create two PriorityClasses: high (value=100) and low (value=50)

**Execution:**

1. Create a blocker job in namespace-A with high priority (2 CPU, 1Gi) and a pending `job-high-a` with low priority (1 CPU)
2. Create a blocker job in namespace-B with high priority (2 CPU, 1Gi) and a pending `job-low-b` with low priority (1 CPU)
3. Wait for blocker job pods, then query pending workloads for LocalQueue-A using user-A's visibility client

```bash
oc --as=system:serviceaccount:<namespace-a>:<sa-a> \
  get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/namespaces/<namespace-a>/localqueues/local-queue-a/pendingworkloads
# Expected: 1 item with priority 50
```

4. Query pending workloads for LocalQueue-B using user-B's visibility client

```bash
oc --as=system:serviceaccount:<namespace-b>:<sa-b> \
  get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/namespaces/<namespace-b>/localqueues/local-queue-b/pendingworkloads
# Expected: 1 item with priority 50
```

5. Verify user-B cannot access LocalQueue-A (cross-namespace)

```bash
oc --as=system:serviceaccount:<namespace-b>:<sa-b> \
  get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/namespaces/<namespace-a>/localqueues/local-queue-a/pendingworkloads
# Expected: 403 Forbidden
```

6. Verify user-A cannot access LocalQueue-B (cross-namespace)

```bash
oc --as=system:serviceaccount:<namespace-a>:<sa-a> \
  get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/namespaces/<namespace-b>/localqueues/local-queue-b/pendingworkloads
# Expected: 403 Forbidden
```

7. Verify pending workloads lists become empty after workloads complete

**Expected Result:**

- Each user can only see pending workloads in their own namespace's LocalQueue
- Cross-namespace access is denied with `403 Forbidden`
- Pending workloads lists become empty after workloads are executed

**Cleanup:**

```bash
oc delete job job-blocker job-high-a -n <namespace-a>
oc delete job job-blocker-b job-low-b -n <namespace-b>
oc delete rolebinding <rb-a> -n <namespace-a>
oc delete rolebinding <rb-b> -n <namespace-b>
oc delete localqueue local-queue-a -n <namespace-a>
oc delete localqueue local-queue-b -n <namespace-b>
oc delete namespace <namespace-a> <namespace-b>
oc delete clusterqueue <cluster-queue>
oc delete resourceflavor <resource-flavor>
oc delete priorityclass <high> <low>
```

---

### 8. PriorityLevelConfiguration and FlowSchema for visibility endpoints

- **Ginkgo:** `When PendingWorkloads Endpoints should be checked / It Should use the correct PriorityLevelConfiguration and FlowSchema for ClusterQueue and LocalQueue`

**Setup:**

1. Create a ResourceFlavor
2. Create a ClusterQueue referencing the ResourceFlavor
3. Create a namespace with `kueue.openshift.io/managed: "true"` label
4. Create a LocalQueue pointing to the ClusterQueue
5. Create a ClusterRoleBinding granting `kueue-batch-admin-role` to the `default` service account

**Execution:**

1. Get the `kueue-visibility` PriorityLevelConfiguration and record its UID

```bash
oc get prioritylevelconfiguration kueue-visibility -o jsonpath='{.metadata.uid}'
```

2. Get the `visibility` FlowSchema and record its UID

```bash
oc get flowschema visibility -o jsonpath='{.metadata.uid}'
```

3. Query the ClusterQueue pending workloads endpoint and inspect the response headers

```bash
# The response should include these headers:
# X-Kubernetes-Pf-Prioritylevel-Uid: <kueue-visibility UID>
# X-Kubernetes-Pf-Flowschema-Uid: <visibility FlowSchema UID>
curl -k -H "Authorization: Bearer <token>" \
  https://<api-server>/apis/visibility.kueue.x-k8s.io/v1beta2/clusterqueues/<cq>/pendingworkloads \
  -D - 2>/dev/null | grep -i 'X-Kubernetes-Pf-'
# Expected: UIDs match kueue-visibility PLC and visibility FlowSchema
```

4. Query the LocalQueue pending workloads endpoint and inspect the response headers

```bash
# The response should include the same headers:
# X-Kubernetes-Pf-Prioritylevel-Uid: <kueue-visibility UID>
# X-Kubernetes-Pf-Flowschema-Uid: <visibility FlowSchema UID>
curl -k -H "Authorization: Bearer <token>" \
  https://<api-server>/apis/visibility.kueue.x-k8s.io/v1beta2/namespaces/<ns>/localqueues/<lq>/pendingworkloads \
  -D - 2>/dev/null | grep -i 'X-Kubernetes-Pf-'
# Expected: UIDs match kueue-visibility PLC and visibility FlowSchema
```

**Expected Result:**

- The `X-Kubernetes-Pf-Prioritylevel-Uid` response header matches the UID of `kueue-visibility` PriorityLevelConfiguration for both ClusterQueue and LocalQueue endpoints
- The `X-Kubernetes-Pf-Flowschema-Uid` response header matches the UID of `visibility` FlowSchema for both endpoints

**Cleanup:**

```bash
oc delete clusterrolebinding <binding>
oc delete localqueue local-queue -n <namespace>
oc delete namespace <namespace>
oc delete clusterqueue <cluster-queue>
oc delete resourceflavor <resource-flavor>
```

---

### 9. LWS pending workloads ordered by priority with sequential admission

- **Ginkgo:** `When PendingWorkloads list should be checked for LWS workloads / It Should show pending LWS workloads ordered by priority and admit them sequentially based on resource availability`

**Setup:**

1. Create a ResourceFlavor
2. Create a ClusterQueue with 400m CPU and 512Mi memory quota
3. Create two namespaces (A and B) with `kueue.openshift.io/managed: "true"` label
4. Create LocalQueue-A in namespace-A and LocalQueue-B in namespace-B, both pointing to the ClusterQueue
5. Create three PriorityClasses: high (100), medium (75), low (50)
6. Create a ServiceAccount in namespace-A with `kueue-batch-admin-role` via ClusterRoleBinding (for ClusterQueue access)
7. Create ServiceAccounts in each namespace with `kueue-batch-user-role` via RoleBindings (for LocalQueue access)

**Execution:**

1. Create a blocker LWS (`lws-blocker-a`) in namespace-A on LocalQueue-A with high priority and size=3 (consumes all quota)
2. Wait for blocker LWS pods to be created (3 pods)

```bash
oc get pods -n <namespace-a> -l leaderworkerset.sigs.k8s.io/name=lws-blocker-a
# Expected: 3 pods
```

3. Create pending LWS workloads: `lws-high-b` (high, size=2) in namespace-B, `lws-medium-a` (medium, size=2) and `lws-low-a` (low, size=2) in namespace-A
4. Verify all pending workloads are created but not admitted
5. Query ClusterQueue pending workloads

```bash
oc get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/clusterqueues/<cq>/pendingworkloads
# Expected: 3 items, priorities: 100, 75, 50
```

6. Query LocalQueue-A pending workloads

```bash
oc --as=system:serviceaccount:<ns-a>:<sa-a> \
  get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/namespaces/<ns-a>/localqueues/local-queue-a/pendingworkloads
# Expected: 2 items, priorities: 75, 50
```

7. Query LocalQueue-B pending workloads

```bash
oc --as=system:serviceaccount:<ns-b>:<sa-b> \
  get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/namespaces/<ns-b>/localqueues/local-queue-b/pendingworkloads
# Expected: 1 item, priority: 100
```

8. Delete the blocker LWS to free resources

```bash
oc delete leaderworkerset lws-blocker-a -n <namespace-a>
```

9. Wait for blocker LWS and its pods to be fully deleted
10. Verify high priority LWS is admitted and pods are running

```bash
oc get pods -n <namespace-b> -l leaderworkerset.sigs.k8s.io/name=lws-high-b -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.phase}{"\n"}{end}'
# Expected: 2 pods in Running state
```

11. Verify medium priority LWS is admitted and pods are running

```bash
oc get pods -n <namespace-a> -l leaderworkerset.sigs.k8s.io/name=lws-medium-a -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.phase}{"\n"}{end}'
# Expected: 2 pods in Running state
```

12. Verify low priority LWS is still pending (insufficient resources)

```bash
oc get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/clusterqueues/<cq>/pendingworkloads
# Expected: 1 item with priority 50
```

13. Delete medium priority LWS to free resources

```bash
oc delete leaderworkerset lws-medium-a -n <namespace-a>
```

14. Wait for medium LWS and its pods to be deleted
15. Verify low priority LWS is admitted and pods are running

```bash
oc get pods -n <namespace-a> -l leaderworkerset.sigs.k8s.io/name=lws-low-a -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.phase}{"\n"}{end}'
# Expected: 2 pods in Running state
```

16. Verify pending workloads list is empty

```bash
oc get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/clusterqueues/<cq>/pendingworkloads
# Expected: empty items list
```

**Expected Result:**

- Pending workloads for LWS are shown in the visibility API, ordered by priority
- After the blocker is removed, workloads are admitted in priority order (high first, then medium)
- Low priority remains pending while insufficient quota, and is admitted after medium is deleted
- Final pending workloads list is empty

**Cleanup:**

```bash
oc delete leaderworkerset lws-high-b -n <namespace-b>
oc delete leaderworkerset lws-low-a -n <namespace-a>
oc delete clusterrolebinding <admin-binding>
oc delete rolebinding <rb-a> -n <namespace-a>
oc delete rolebinding <rb-b> -n <namespace-b>
oc delete localqueue local-queue-a -n <namespace-a>
oc delete localqueue local-queue-b -n <namespace-b>
oc delete namespace <namespace-a> <namespace-b>
oc delete clusterqueue <cluster-queue>
oc delete resourceflavor <resource-flavor>
oc delete priorityclass <high> <medium> <low>
```

---

### 10. Suspended JobSet shows on pending workloads list

- **Ginkgo:** `When JobSet is suspended / It Should show on pending workloads list for local queue and cluster queue`

**Setup:**

1. Create a ResourceFlavor
2. Create a ClusterQueue with 40m CPU and 512Mi memory quota (intentionally low to keep workload pending)
3. Create a namespace with `kueue.openshift.io/managed: "true"` label
4. Create a LocalQueue pointing to the ClusterQueue
5. Create a ServiceAccount with `kueue-batch-admin-role` via ClusterRoleBinding (admin, for ClusterQueue access)
6. Create a ServiceAccount with `kueue-batch-user-role` via RoleBinding (user, for LocalQueue access)

**Execution:**

1. Create a JobSet using `testutils.NewTestResourceBuilder`

```bash
cat <<'EOF' | oc apply -f -
apiVersion: jobset.x-k8s.io/v1alpha2
kind: JobSet
metadata:
  generateName: jobset-
  namespace: <namespace>
  labels:
    kueue.x-k8s.io/queue-name: local-queue
spec:
  suspend: true
  replicatedJobs:
  - name: workers
    replicas: 1
    template:
      spec:
        parallelism: 1
        completions: 1
        template:
          spec:
            restartPolicy: Never
            containers:
            - name: test-container
              image: quay.io/prometheus/busybox:latest
              command: ["sh", "-c", "echo Hello; sleep 10"]
              resources:
                requests:
                  cpu: 100m
                  memory: 128Mi
EOF
```

2. Verify the workload is created but not admitted
3. Query LocalQueue pending workloads using the user visibility client

```bash
oc --as=system:serviceaccount:<namespace>:<user-sa> \
  get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/namespaces/<namespace>/localqueues/local-queue/pendingworkloads
# Expected: 1 pending workload
```

4. Query ClusterQueue pending workloads using the admin visibility client

```bash
oc --as=system:serviceaccount:<namespace>:<admin-sa> \
  get --raw /apis/visibility.kueue.x-k8s.io/v1beta2/clusterqueues/<cluster-queue>/pendingworkloads
# Expected: 1 pending workload
```

**Expected Result:**

- A suspended JobSet generates a workload that appears in both LocalQueue and ClusterQueue pending workloads lists
- 1 pending workload is visible via both the user (LocalQueue) and admin (ClusterQueue) visibility clients

**Cleanup:**

```bash
oc delete jobset <jobset-name> -n <namespace>
oc delete rolebinding <user-rb> -n <namespace>
oc delete clusterrolebinding <admin-binding>
oc delete localqueue local-queue -n <namespace>
oc delete namespace <namespace>
oc delete clusterqueue <cluster-queue>
oc delete resourceflavor <resource-flavor>
```
