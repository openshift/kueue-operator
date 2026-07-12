---
name: operator-upgrade-test
description: End-to-end Kueue operator upgrade test on OpenShift. Provisions a GCP cluster, builds an FBC catalog, runs both upgrade scenarios (default + full uninstall), checks config migration, and produces a results document.
---

# Operator Upgrade Test

## Context

Kueue does not support seamless in-place upgrades. The upgrade path is: uninstall the old version, then install the new one. This test verifies that user-created Kueue resources (ClusterQueues, LocalQueues, ResourceFlavors, workloads) survive the uninstall/reinstall cycle and remain functional under the target version.

Two uninstall methods are tested:
- **Scenario A (Default uninstall)**: Remove the operator only (Subscription + CSV). The Kueue operand (controller, webhooks) stays running. Resources remain fully active.
- **Scenario B (Full uninstall)**: Delete the Kueue CR first (triggers operand cleanup via finalizer), then remove the operator. The controller is gone but CRDs and user resources persist in etcd.

Both scenarios then install the target version and verify that all pre-existing resources are recognized, workloads are re-admitted, and new resources can be created.

## Interaction Model

ALL user input, credential checks, and confirmations happen in Phase 0. After Phase 0 completes, the test runs unattended through Phases 1-8 with no further user interaction. Phase 9 (cluster cleanup) is the only other interactive phase — it asks whether to destroy the provisioned cluster. If an error occurs mid-run, log it in the results document and continue to the next phase where possible.

## Variables

All values below are collected in Phase 0 and referenced as `<variable-name>` throughout. Nothing is hardcoded — every path, credential, and parameter comes from user input or auto-detection confirmed by the user.

| Variable | Collected In | Description |
|----------|-------------|-------------|
| `<cluster-name>` | 0c | Short name for the cluster |
| `<region>` | 0c | GCP region |
| `<gcp-project>` | 0c | GCP project ID (auto-detected) |
| `<ssh-public-key>` | 0c | SSH public key content |
| `<ocp-version>` | 0b | Full OCP version (e.g., `4.22.2`) |
| `<ocp-minor>` | 0b | OCP minor version (e.g., `4.22`) |
| `<source-version>` | 0e | Source Kueue version |
| `<target-version>` | 0e | Target Kueue version |
| `<source-bundle-image>` | 0e | OLM bundle image ref for source |
| `<target-bundle-image>` | 0e | OLM bundle image ref for target |
| `<quay-image>` | 0f | FBC catalog image destination |
| `<pull-secret-path>` | 0d | Path to pull-secret file |
| `<sa-key-path>` | 0g | Path to GCP service account key |
| `<work-dir>` | 0c | Working directory for all test artifacts |
| `<base-domain>` | 0c | Cluster base domain |
| `<worker-type>` | 0c | GCP instance type for workers |
| `<master-type>` | 0c | GCP instance type for masters |

---

## Phase 0: Gather ALL Parameters, Credentials, and Confirmations

This is the ONLY interactive phase. Collect everything needed before starting execution.

### 0a. Check for Existing Cluster

Run `oc get clusterversion 2>/dev/null`. If a cluster is reachable:
- Show the cluster version and API server URL
- Use `AskUserQuestion`: **"A cluster is already reachable. Use this cluster, or provision a new one?"**
  - **Use existing cluster** — skip cluster provisioning in Phase 1
  - **Provision new cluster** — proceed with cluster provisioning parameters below

If no cluster is reachable, proceed with provisioning parameters.

### 0b. OCP Version

Use `AskUserQuestion`: **"Which OCP version?"** — e.g., `4.22`.

Determine the latest z-stream by fetching the directory listing from `https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp/` and finding the highest z-stream for the chosen minor version. Store as `<ocp-version>`.

### 0c. Cluster Provisioning Parameters (skip if using existing cluster)

Ask using `AskUserQuestion`:
1. **Cluster name** — short name, stored as `<cluster-name>`
2. **GCP region** — stored as `<region>` (suggest `us-west1` as a common choice)
3. **Base domain** — stored as `<base-domain>` (suggest `gcp.devcluster.openshift.com` as a common choice)
4. **Working directory** — stored as `<work-dir>` (suggest `~/kueue-upgrade-test`). All artifacts go here: openshift-install binary, cluster config, kubeconfig, and results document.
5. **Worker instance type** — stored as `<worker-type>` (suggest `e2-standard-4`)
6. **Master instance type** — stored as `<master-type>` (suggest `e2-standard-8`)

Auto-detect and display (confirm with user):
- **GCP project** → `<gcp-project>`: Run `gcloud config get-value project`
- **SSH public key** → `<ssh-public-key>`: Look for `~/.ssh/id_rsa.pub`, `~/.ssh/id_ed25519.pub`, or ask user for path

### 0d. Pull Secret

Use `AskUserQuestion`: **"Path to your pull-secret file?"** — store as `<pull-secret-path>`.

Validate the file exists and contains valid JSON.

### 0e. Kueue Versions and Bundle Images

Use `AskUserQuestion` to collect version info:
1. **Source Kueue version** (e.g., `1.3.1`) — stored as `<source-version>`
2. **Target Kueue version** (e.g., `1.4.0`) — stored as `<target-version>`

Then auto-discover the bundle images:

**For released versions** — clone the kueue-fbc repo to a temp directory and look up from there:
```bash
KUEUE_FBC_DIR=$(mktemp -d)/kueue-fbc
git clone --depth 1 https://github.com/openshift/kueue-fbc "$KUEUE_FBC_DIR"
grep -A1 "Image:" "$KUEUE_FBC_DIR/v<ocp-minor>/catalog-template.yaml"
```
Do NOT use `opm render` for discovery — it pulls images and needs container registry auth that may not be configured for `opm`.

**For unreleased/staging versions** — query the quay.io API:
```bash
curl -s "https://quay.io/api/v1/repository/redhat-user-workloads/kueue-operator-tenant/kueue-bundle-<major>-<minor>/tag/?limit=10&onlyActiveTags=true" | python3 -m json.tool
```
Pick the latest tag's `manifest_digest` to form the image ref: `quay.io/redhat-user-workloads/kueue-operator-tenant/kueue-bundle-<major>-<minor>@sha256:<digest>`.

Present the discovered images to the user for confirmation rather than asking them to provide raw image references. If auto-discovery fails, fall back to asking the user directly. Store as `<source-bundle-image>` and `<target-bundle-image>`.

### 0f. FBC Image Destination

Use `AskUserQuestion`: **"Where should the FBC catalog image be pushed? (must be an existing quay.io repository)"** — store as `<quay-image>`.

### 0g. Credential and Prerequisite Checks

Run all checks NOW, before starting any work. If any fail, ask the user to fix them interactively before proceeding.

**quay.io login + push access**:
```bash
podman login --get-login quay.io 2>/dev/null
```
If not authenticated, tell the user: **"You need to log in to quay.io. Please run: `! podman login quay.io`"** and wait for them to complete it. Re-check until authenticated.

After login succeeds, verify the target repository from Phase 0f exists:
```bash
podman search <quay-image-without-tag> --list-tags --limit 1 2>&1
```
If the repo does not exist (returns "repository not found"), tell the user: **"The repository `<quay-image>` does not exist. Please create it at https://quay.io/new/ and then confirm."** Wait for the user to confirm, then re-verify.

**GCP service account key** (if provisioning a cluster):
```bash
gcloud auth list --filter=status:ACTIVE --format="value(account)" 2>/dev/null
```
If no active account, tell the user: **"You need to authenticate to GCP. Please run: `! gcloud auth login`"** and wait.

Ask: **"Path to your GCP service account key for openshift-install?"** — store as `<sa-key-path>`. Check common locations (`$GOOGLE_APPLICATION_CREDENTIALS`, `~/.gcp/osServiceAccount.json`) and suggest if found.

Validate the key is functional:
```bash
gcloud auth activate-service-account --key-file="<sa-key-path>" 2>&1
```
If the key is invalid (expired, revoked, or service account deleted), offer to create a new one:
```bash
PROJECT=<gcp-project>
SA_NAME="openshift-install"
SA_EMAIL="${SA_NAME}@${PROJECT}.iam.gserviceaccount.com"
gcloud iam service-accounts create $SA_NAME --display-name="OpenShift Installer" 2>/dev/null || true
for ROLE in compute.admin dns.admin iam.securityAdmin iam.serviceAccountAdmin iam.serviceAccountKeyAdmin iam.serviceAccountUser storage.admin; do
  gcloud projects add-iam-policy-binding $PROJECT --member="serviceAccount:$SA_EMAIL" --role="roles/$ROLE" --condition=None --quiet
done
gcloud iam service-accounts keys create <sa-key-path> --iam-account=$SA_EMAIL
```
Always use `--condition=None` with `gcloud projects add-iam-policy-binding` — projects with conditional IAM policies reject bindings without it.

**opm availability**:
```bash
which opm && opm version
```
If not found, tell the user: **"opm is required for FBC generation. Please install it (v1.47.0+) and re-run."** Do not proceed without it.

**podman availability + machine running**:
```bash
which podman
podman info --format '{{.Host.RemoteSocket.Path}}' 2>/dev/null
```
If not found, tell the user to install it (`brew install podman`). If `podman info` fails, the podman machine is not running — tell the user: **"Podman machine is not running. Please run: `! podman machine start`"** and wait. Cross-platform builds (`--platform=linux/amd64`) require the machine.

**cert-manager, JobSet, LeaderWorkerSet** (cluster-dependent — skip if provisioning a new cluster):

If using an existing cluster, check now:
```bash
oc get crd certificates.cert-manager.io 2>/dev/null
oc get crd jobsets.jobset.x-k8s.io 2>/dev/null
oc get crd leaderworkersets.leaderworkerset.x-k8s.io 2>/dev/null
```
Note which are missing. Whether checked now or not, Phase 3a-ii will install any that are absent — no user action needed.

### 0h. Kueue CR Schema Strategy

Do NOT hardcode Kueue CR schemas — they vary across versions and the CRD is the source of truth.

After installing each version (Phase 3a for source, Phase 6c for target), dynamically introspect the CRD to discover required fields and valid enum values:
```bash
oc explain kueue.spec.config --recursive
oc explain kueue.spec.config.workloadManagement
oc explain kueue.spec.config.gangScheduling
oc explain kueue.spec.config.preemption
oc explain kueue.spec.config.integrations
oc explain kueue.spec.config.admissionFairSharing 2>/dev/null
```

Build the Kueue CR dynamically from the `oc explain` output. Use safe defaults for required fields (e.g., `gangScheduling.policy: None`, enum values as reported by `oc explain`).

Key schema differences discovered between 1.3.1 and 1.4.0:
- 1.3.1 uses `workloadManagement.labelSelector` (matchExpressions)
- 1.4.0 replaces it with `workloadManagement.labelPolicy` (enum: `QueueName`, `None`, `""`)
- 1.3.1 does not have `admissionFairSharing` in the CRD
- 1.4.0 adds `admissionFairSharing` with `configuration` (Default/Custom) + `custom` sub-object
- Both versions use PascalCase framework names: `BatchJob`, `Pod`, `Deployment`, `StatefulSet`, `JobSet`, `LeaderWorkerSet`

### 0i. Final Summary and Confirmation

Present a complete summary of everything that will happen, showing all collected variables:

```
=== Kueue Upgrade Test Configuration ===

Cluster:          <cluster-name> (GCP <region>) [or "existing cluster"]
OCP Version:      <ocp-version>
Source Kueue:     <source-version>
  Bundle:         <source-bundle-image>
Target Kueue:     <target-version>
  Bundle:         <target-bundle-image>
FBC Image:        <quay-image>
Pull Secret:      <pull-secret-path>
GCP Project:      <gcp-project>
GCP SA Key:       <sa-key-path>
Work dir:         <work-dir>

Credentials:
  quay.io login:  OK
  GCP auth:       OK [or N/A]
  GCP SA key:     OK [or N/A]
  opm:            OK (v<version>)
  podman:         OK (machine running)

Phases to execute (unattended):
  1. Provision GCP cluster [or skip]
  2. Build and apply FBC catalog
  3. Install source version + create 11 test workloads
  4. Scenario A: default uninstall -> upgrade -> verify
  5. Cleanup Scenario A -> re-setup
  6. Scenario B: full uninstall -> upgrade -> verify
  7. Config migration check
  8. Generate results document
  9. Cluster cleanup (interactive)

Estimated time: ~90 minutes (with cluster provisioning) / ~30 minutes (existing cluster)
```

Use `AskUserQuestion`: **"Ready to start? This will run unattended until completion."**

**After the user confirms, do NOT ask any more questions. All remaining phases run autonomously.**

---

## Phase 1: Provision GCP Cluster (UNATTENDED)

**Skip if using an existing cluster.**

### 1a. Download openshift-install

Always download fresh with `curl` — never reuse browser-downloaded tarballs (they have macOS quarantine attributes that silently block execution).

The URL path (`x86_64` vs `aarch64`) determines the **release payload architecture** (target cluster), not the binary architecture (local machine). For amd64 GCP clusters on Apple Silicon Macs, use the `x86_64` path with `mac-arm64` binary:

```bash
mkdir -p <work-dir>/<cluster-name>
curl -fSL -o /tmp/openshift-install-mac-arm64-<ocp-version>.tar.gz \
  "https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp/<ocp-version>/openshift-install-mac-arm64.tar.gz"
curl -fSL -o /tmp/sha256sum-<ocp-version>.txt \
  "https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp/<ocp-version>/sha256sum.txt"
# Verify checksum before extracting
grep "openshift-install-mac-arm64.tar.gz" /tmp/sha256sum-<ocp-version>.txt | \
  sed "s|openshift-install-mac-arm64.tar.gz|/tmp/openshift-install-mac-arm64-<ocp-version>.tar.gz|" | \
  shasum -a 256 -c -
tar xzf /tmp/openshift-install-mac-arm64-<ocp-version>.tar.gz -C <work-dir>/<cluster-name>/
xattr -cr <work-dir>/<cluster-name>/openshift-install
```

If checksum verification fails, abort and report the mismatch — do not extract or execute the binary.

Verify the binary works (should complete in <5 seconds):
```bash
<work-dir>/<cluster-name>/openshift-install version
```
If this hangs, the binary has quarantine issues — re-download with `curl`.

### 1b. Generate install-config.yaml

Create `<work-dir>/<cluster-name>/install-config.yaml`:

```yaml
additionalTrustBundlePolicy: Proxyonly
apiVersion: v1
baseDomain: <base-domain>
compute:
- architecture: amd64
  hyperthreading: Enabled
  name: worker
  platform:
    gcp:
      type: <worker-type>
      osDisk:
        diskSizeGB: 128
      onHostMaintenance: Terminate
      zones:
      - <region>-b
  replicas: 1
controlPlane:
  architecture: amd64
  hyperthreading: Enabled
  name: master
  platform:
    gcp:
      type: <master-type>
      zones:
      - <region>-b
  replicas: 3
metadata:
  name: <cluster-name>
networking:
  clusterNetwork:
  - cidr: 10.128.0.0/14
    hostPrefix: 23
  machineNetwork:
  - cidr: 10.0.0.0/16
  networkType: OVNKubernetes
  serviceNetwork:
  - 172.30.0.0/16
platform:
  gcp:
    projectID: <gcp-project>
    region: <region>
publish: External
pullSecret: '<pull-secret-content>'
sshKey: |
  <ssh-public-key>
```

Save a backup as `install-config.yaml.bak`.

### 1c. Create Cluster

Set the GCP credentials for openshift-install:
```bash
export GOOGLE_APPLICATION_CREDENTIALS=<sa-key-path>
```

Run in the foreground (takes 30-45 min):
```bash
<work-dir>/<cluster-name>/openshift-install create cluster \
  --dir=<work-dir>/<cluster-name> --log-level=info
```

After starting, verify within 30 seconds that `.openshift_install.log` exists and has content. If the log file remains empty after 60 seconds, the binary may be blocked by macOS Gatekeeper — check `xattr` on the binary and re-download if needed.

Log progress updates to the conversation every 5 minutes by tailing the install log. Filter out lines containing `pullSecret`, `sshKey`, or `password` to avoid exposing credentials.

### 1d. Set KUBECONFIG

```bash
export KUBECONFIG=<work-dir>/<cluster-name>/auth/kubeconfig
oc get clusterversion
oc get nodes
```

If cluster creation fails, log the error in the results document and abort.

---

## Phase 2: Build and Apply FBC (UNATTENDED)

### 2a. Clone kueue-fbc Repository

Clone fresh to a temp directory (the repo is small, takes seconds):
```bash
KUEUE_FBC_DIR=$(mktemp -d)/kueue-fbc
git clone --depth 1 https://github.com/openshift/kueue-fbc "$KUEUE_FBC_DIR"
```

### 2b. Update catalog-template.yaml

Edit `$KUEUE_FBC_DIR/v<ocp-minor>/catalog-template.yaml`. Add/update bundle image entries for both versions under `Stable.Bundles`:
```yaml
  - Image: <source-bundle-image>
  - Image: <target-bundle-image>
```

### 2c. Generate and Build FBC

```bash
cd "$KUEUE_FBC_DIR" && ./generate-fbc.sh
# Verify the generated catalog (catalog.json is JSONL format, not standard JSON)
opm validate "$KUEUE_FBC_DIR/v<ocp-minor>/catalog/" 2>&1 || echo "Validation failed"
cd "$KUEUE_FBC_DIR/v<ocp-minor>"
podman build --platform=linux/amd64 -f Containerfile.catalog -t <quay-image> .
podman push <quay-image>
```

### 2d. Apply CatalogSource

```bash
oc apply -f - <<'EOF'
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: kueue-upgrade-catalog
  namespace: openshift-marketplace
spec:
  sourceType: grpc
  image: <quay-image>
  displayName: Kueue Upgrade Test
  publisher: Test
EOF
```

### 2e. Apply ImageDigestMirrorSet (if needed)

Only needed for unreleased versions whose bundle images use `quay.io/redhat-user-workloads`. Released versions use `registry.redhat.io` and need no mirroring. Create mirrors for each unreleased version:

```bash
oc apply -f - <<'EOF'
apiVersion: config.openshift.io/v1
kind: ImageDigestMirrorSet
metadata:
  name: kueue-digest-mirrorset
spec:
  imageDigestMirrors:
    - mirrors:
        - quay.io/redhat-user-workloads/kueue-operator-tenant/kueue-operator-<branch>
      source: registry.redhat.io/kueue/kueue-rhel9-operator
    - mirrors:
        - quay.io/redhat-user-workloads/kueue-operator-tenant/kueue-operand-<branch>
      source: registry.redhat.io/kueue/kueue-rhel9
    - mirrors:
        - quay.io/redhat-user-workloads/kueue-operator-tenant/kueue-must-gather-<branch>
      source: registry.redhat.io/kueue/kueue-must-gather-rhel9
EOF
```

Where `<branch>` = target version with dots replaced by dashes (e.g., `1-4` for `1.4.0`).

### 2f. Wait for CatalogSource READY

Poll until READY (timeout 5 minutes):
```bash
oc get catalogsource kueue-upgrade-catalog -n openshift-marketplace \
  -o jsonpath='{.status.connectionState.lastObservedState}'
```

---

## Phase 3: Install Source Version + Create Test Resources (UNATTENDED)

### 3a. Install Source Version

```bash
oc create namespace openshift-kueue-operator 2>/dev/null || true

oc apply -f - <<'EOF'
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: kueue-operator
  namespace: openshift-kueue-operator
spec: {}
EOF

oc apply -f - <<'EOF'
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: kueue-operator
  namespace: openshift-kueue-operator
spec:
  channel: stable-v<source-major>.<source-minor>
  name: kueue-operator
  source: kueue-upgrade-catalog
  sourceNamespace: openshift-marketplace
  installPlanApproval: Automatic
EOF

sleep 30
oc wait csv -n openshift-kueue-operator \
  -l operators.coreos.com/kueue-operator.openshift-kueue-operator="" \
  --for=jsonpath='{.status.phase}'=Succeeded --timeout=5m
```

### 3a-ii. Install Prerequisites on Cluster

Before creating the Kueue CR, ensure cert-manager is installed (required by the operator):
```bash
if ! oc get crd certificates.cert-manager.io 2>/dev/null; then
  oc apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml
  oc wait deployment cert-manager -n cert-manager --for=condition=Available --timeout=3m
fi
```

Install JobSet and LeaderWorkerSet operators if missing:
```bash
# JobSet
if ! oc get crd jobsets.jobset.x-k8s.io 2>/dev/null; then
  oc apply --server-side -f https://github.com/kubernetes-sigs/jobset/releases/latest/download/manifests.yaml
fi
# LeaderWorkerSet
if ! oc get crd leaderworkersets.leaderworkerset.x-k8s.io 2>/dev/null; then
  oc apply --server-side -f https://github.com/kubernetes-sigs/lws/releases/latest/download/manifests.yaml
  # On single-worker clusters, patch resource requests to avoid scheduling failures
  oc patch deployment lws-controller-manager -n lws-system --type=json \
    -p='[{"op":"replace","path":"/spec/template/spec/containers/0/resources/requests/cpu","value":"100m"},
         {"op":"replace","path":"/spec/template/spec/containers/0/resources/requests/memory","value":"128Mi"}]' 2>/dev/null || true
  oc scale deployment lws-controller-manager -n lws-system --replicas=1 2>/dev/null || true
fi
```

### 3b. Create Kueue CR (Source Version Schema)

First, introspect the CRD to determine the correct schema (see Phase 0h). Build the CR from `oc explain` output. Save a copy of the spec for config migration comparison in Phase 7.

Apply the Kueue CR. If it fails validation, read the error message to identify missing/changed fields, adjust the CR YAML, and retry — do not ask the user. Repeat up to 3 times. If it still fails after 3 attempts, log the error in the results document and continue without the CR (mark Kueue CR creation as FAIL).

Wait for controller deployment to appear and become available:
```bash
# Wait for the operator to create the controller deployment (may take 30-60s)
timeout 120 bash -c 'until oc get deployment kueue-controller-manager -n openshift-kueue-operator 2>/dev/null; do sleep 5; done'
oc wait deployment kueue-controller-manager -n openshift-kueue-operator \
  --for=condition=Available --timeout=5m
```

### 3c. Create Test Resources

Create namespace, queue resources, and workloads as defined below. Use `oc apply` for all resources.

**Namespace**: `upgrade-test` with label `kueue.openshift.io/managed: "true"`

**Queue resources**:
- ResourceFlavor: `upgrade-test-flavor` (default)
- ClusterQueues: `upgrade-test-cq` (4 CPU/8Gi), `preemption-cq-a` (500m/1Gi, cohort: upgrade-cohort), `preemption-cq-b` (500m/1Gi, cohort: upgrade-cohort), `afs-cq` (1 CPU/2Gi)
- LocalQueues: `upgrade-test-lq`, `preemption-lq-a`, `preemption-lq-b`, `afs-lq-heavy` (weight:1), `afs-lq-light` (weight:2)

**Workloads** (11 total, all using `quay.io/openshift/origin-cli:latest`, `sleep 7200`, 50m CPU, 128Mi memory, proper security context):

| Type | Name | Queue |
|------|------|-------|
| Job | `upgrade-test-job` | `upgrade-test-lq` |
| Pod | `upgrade-test-pod` | `upgrade-test-lq` |
| Deployment | `upgrade-test-deploy` | `upgrade-test-lq` |
| StatefulSet | `upgrade-test-ss` | `upgrade-test-lq` |
| JobSet | `upgrade-test-jobset` | `upgrade-test-lq` |
| LeaderWorkerSet | `upgrade-test-lws` | `upgrade-test-lq` |
| Job | `preempt-job-a1` | `preemption-lq-a` |
| Job | `preempt-job-a2` | `preemption-lq-a` |
| Job | `preempt-job-b1` | `preemption-lq-b` |
| Job | `afs-job-heavy` | `afs-lq-heavy` |
| Job | `afs-job-light` | `afs-lq-light` |

### 3d. Verify and Record Baseline

Wait for workloads to be admitted (poll for 5 minutes). Record:
- CSV version
- Workload admission status
- Pod status
- Queue resource counts

---

## Phase 4: Scenario A — Default Uninstall -> Upgrade (UNATTENDED)

### 4a. Default Uninstall

```bash
oc delete subscription kueue-operator -n openshift-kueue-operator
oc delete csv -n openshift-kueue-operator \
  -l operators.coreos.com/kueue-operator.openshift-kueue-operator=""
```

### 4b. Post-Uninstall Verification

Run each check, compare expected vs actual, record PASS/FAIL:

| Check | Expected |
|-------|----------|
| Operator pods | Gone |
| Controller pods | Still running |
| Kueue CR | Present |
| CRDs | All present |
| ClusterQueues (4) | Intact |
| LocalQueues (5) | Intact |
| ResourceFlavors | Present |
| Workloads | Admitted |
| Pods | Running |

### 4c. Install Target Version

```bash
oc apply -f - <<'EOF'
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: kueue-operator
  namespace: openshift-kueue-operator
spec:
  channel: stable-v<target-major>.<target-minor>
  name: kueue-operator
  source: kueue-upgrade-catalog
  sourceNamespace: openshift-marketplace
  installPlanApproval: Automatic
EOF

# Wait for the new CSV to appear before waiting for Succeeded (oc wait with a label selector
# returns immediately if no matching resource exists yet)
sleep 30
oc wait csv -n openshift-kueue-operator \
  -l operators.coreos.com/kueue-operator.openshift-kueue-operator="" \
  --for=jsonpath='{.status.phase}'=Succeeded --timeout=5m
```

### 4d. Post-Upgrade Verification

Run checks and record:
- CSV version = target
- Existing resources preserved
- Existing workloads still admitted
- Create `post-upgrade-job` on `upgrade-test-lq` → verify Admitted
- Create `post-upgrade-pod` on `upgrade-test-lq` → verify Admitted
- Create new `post-upgrade-flavor`, `post-upgrade-cq`, `post-upgrade-lq` → verify created
- Create `new-queue-job` on `post-upgrade-lq` → verify Admitted

---

## Phase 5: Cleanup Scenario A -> Re-setup (UNATTENDED)

```bash
oc delete jobs --all -n upgrade-test
oc delete pods -l kueue.x-k8s.io/queue-name -n upgrade-test
oc delete deployments --all -n upgrade-test
oc delete statefulsets --all -n upgrade-test
oc delete jobsets --all -n upgrade-test 2>/dev/null
oc delete leaderworkersets --all -n upgrade-test 2>/dev/null
oc delete localqueue --all -n upgrade-test
oc delete clusterqueue --all
oc delete resourceflavor --all
oc delete kueue cluster
oc delete subscription kueue-operator -n openshift-kueue-operator
oc delete csv -n openshift-kueue-operator \
  -l operators.coreos.com/kueue-operator.openshift-kueue-operator=""
oc delete namespace upgrade-test
```

Wait for namespace deletion to complete before re-running Phase 3:
```bash
oc wait --for=delete namespace/upgrade-test --timeout=2m 2>/dev/null || true
```
Then re-run Phase 3 (install source + create resources).

---

## Phase 6: Scenario B — Full Uninstall -> Upgrade (UNATTENDED)

### 6a. Full Uninstall

```bash
oc delete kueue cluster
oc wait kueue cluster --for=delete --timeout=3m 2>/dev/null || true
oc delete subscription kueue-operator -n openshift-kueue-operator
oc delete csv -n openshift-kueue-operator \
  -l operators.coreos.com/kueue-operator.openshift-kueue-operator=""
```

### 6b. Post-Uninstall Verification

| Check | Expected |
|-------|----------|
| Operator pods | Gone |
| Controller pods | Gone |
| Kueue CR | NotFound |
| CRDs | All present |
| ClusterQueues | Intact (in etcd) |
| LocalQueues | Intact |
| ResourceFlavors | Present |
| Workloads | Present (no controller) |
| Pods | Running (no controller) |

### 6c. Install Target Version + New Kueue CR

Install target version subscription (with a 30-second sleep before `oc wait csv` to let OLM create it), wait for CSV. Then introspect the target version's CRD (see Phase 0h) and create the Kueue CR using the **target version's schema**. The schema may differ from the source version — do not reuse the source CR without validation. If the CR fails validation, read the error, adjust, and retry up to 3 times without asking the user.

Wait for controller deployment to appear and become available:
```bash
timeout 120 bash -c 'until oc get deployment kueue-controller-manager -n openshift-kueue-operator 2>/dev/null; do sleep 5; done'
oc wait deployment kueue-controller-manager -n openshift-kueue-operator \
  --for=condition=Available --timeout=5m
```

### 6d. Post-Upgrade Verification

Same checks as Phase 4d:
- Existing resources recognized by new controller
- Existing workloads re-admitted
- New workload on existing queue: admitted
- New resources (use `v1beta2` API if available): created
- New workload on new queue: admitted

---

## Phase 7: Config Migration Check (UNATTENDED)

### 7a. Schema Comparison

1. Save the source Kueue CR to a temp file
2. With the target version installed, attempt dry-run apply:
   ```bash
   oc apply --dry-run=server -f /tmp/source-kueue-cr.yaml 2>&1
   ```
3. Record validation errors as schema migration findings

### 7b. API Version Deprecation Check

```bash
oc apply --dry-run=server -f - <<'EOF'
apiVersion: kueue.x-k8s.io/v1beta1
kind: ResourceFlavor
metadata:
  name: test-deprecation
EOF
```

Record any deprecation warnings.

### 7c. Document Findings

Compile all schema changes, deprecations, and controller behavior differences into the results document.

---

## Phase 8: Generate Results Document (UNATTENDED)

Create `<work-dir>/results/kueue-upgrade-test-<source-version>-to-<target-version>-results.md`:

```markdown
# Kueue Upgrade Test Results: <source-version> -> <target-version>

**Cluster**: `<cluster-name>` (OCP <ocp-version>, GCP)
**Date**: <date>
**Catalog**: `kueue-upgrade-catalog` (`<quay-image>`)

## Test Environment
- OpenShift <ocp-version> cluster
- Custom CatalogSource with channels stable-v<source> and stable-v<target>

## Resources Created Under <source-version>

### Queue Resources
[Table: Resource | Name | Details]

### Workloads
[Table: Type | Name | Queue | CPU Request]

## Scenario A: Default Uninstall (Operator Only)

### Post-Uninstall
[Table: Check | Expected | Actual | Result (PASS/FAIL)]

### Post-Upgrade
[Table: Check | Expected | Actual | Result (PASS/FAIL)]

### New Resources on Target Version
[Table: Test | Result | Details]

### Scenario A Summary: [PASS/FAIL]

## Scenario B: Full Uninstall (CR + Operator)

### Post-Uninstall
[Table: Check | Expected | Actual | Result (PASS/FAIL)]

### Post-Upgrade
[Table: Check | Expected | Actual | Result (PASS/FAIL)]

### New Resources on Target Version
[Table: Test | Result | Details]

### Scenario B Summary: [PASS/FAIL]

## Config Migration Findings
- Kueue CR schema changes
- API version deprecation warnings
- Controller behavior differences

## Notable Observations
[Any unexpected behavior, warnings, or timing issues observed during the test]

## Overall Result: [PASS/FAIL]
```

Display the results file path and a brief summary to the user.

---

## Phase 9: Cluster Cleanup (INTERACTIVE)

**Skip if using a pre-existing cluster (not provisioned in Phase 1).**

This is the only interactive phase after Phase 0. After displaying the results summary, ask the user whether to destroy the cluster.

Use `AskUserQuestion`: **"The upgrade test is complete. Do you want to destroy the provisioned cluster?"**
- **Yes, destroy the cluster** — run `openshift-install destroy cluster`. This removes all GCP resources (VMs, network, DNS, etc.).
- **No, keep the cluster running** — skip destruction. Remind the user of the cluster's KUBECONFIG path and that GCP resources will continue incurring cost.

If the user chooses to destroy:
```bash
export GOOGLE_APPLICATION_CREDENTIALS=<sa-key-path>
<work-dir>/<cluster-name>/openshift-install destroy cluster \
  --dir=<work-dir>/<cluster-name> --log-level=info
```

This typically takes 5-10 minutes. Run in the foreground and report completion.

If the service account was auto-created in Phase 0d, also clean it up:
```bash
gcloud iam service-accounts delete "${SA_NAME}@${PROJECT}.iam.gserviceaccount.com" --quiet 2>/dev/null || true
```

---

## Error Handling (applies to all unattended phases)

When an error occurs during unattended execution:
1. **Log the error** with full command output into the results document
2. **Mark the check as FAIL** in the results table
3. **Continue to the next step/phase** where possible — do not abort the entire run
4. Only abort if a blocking dependency fails (e.g., cluster creation failure blocks all subsequent phases, CSV never succeeds blocks upgrade tests)

| Error | Cause | Action |
|-------|-------|--------|
| `opm: command not found` | OPM not installed | Caught in Phase 0g — will not reach here |
| `podman: command not found` | Podman not installed | Caught in Phase 0g |
| Cluster creation failure | GCP quota/auth/capacity | Log error, abort (blocking) |
| CatalogSource not READY after 5m | Image pull failure | Log error, abort (blocking) |
| CSV stuck in Pending after 5m | InstallPlan issue | Log error, mark FAIL, try to continue |
| Kueue CR validation error | Schema mismatch | Log error, try target schema, record as migration finding |
| JobSet/LWS creation fails | Operators not installed | Log error, skip those workload types, continue |
| Workload not admitted after 5m | Controller issue | Log as FAIL, continue |

## Prerequisites (all validated in Phase 0g)

- `oc` CLI
- `podman` with linux/amd64 build support (machine must be running)
- `opm` v1.47.0+
- `gcloud` CLI with active authentication (if provisioning)
- Valid GCP service account key (if provisioning)
- quay.io account with push access to the target repository (repo must exist)
- Pull secret file
- cert-manager on the cluster (installed automatically if missing)
- JobSet and LeaderWorkerSet operators on the cluster (installed automatically if missing)
