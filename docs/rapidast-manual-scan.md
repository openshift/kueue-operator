# Manual RapiDAST Security Scan for Kueue Operator

This guide covers how to manually run RapiDAST (DAST) scans against the Kueue Operator APIs on an OpenShift cluster and upload results to ProdSec central storage.

## Prerequisites

### OpenShift Cluster

An OpenShift cluster with kueue-operator installed. You will need cluster admin access to retrieve the API server URL and a bearer token.

### Python 3

Python 3 must be installed on your machine. RapiDAST uses a Python virtual environment (`venv`) to manage its dependencies.

Verify Python 3 is available:

```bash
python3 --version
```

If not installed:
- **macOS**: `brew install python3` or download from [python.org](https://www.python.org/downloads/)
- **Fedora/RHEL**: `sudo dnf install python3 python3-pip`

### ZAP (Zed Attack Proxy)

Download and install [ZAP](https://www.zaproxy.org/download/).

On macOS, the default executable path is `/Applications/ZAP.app/Contents/Java/zap.sh`. On Linux, it's typically available as `zap.sh` on your `$PATH` after installation.

### ProdSec GCS Bucket Access

Scan results are uploaded to the ProdSec centralized Google Cloud Storage bucket. The Kueue project folder is:

- **Bucket**: `secaut-bucket`
- **Folder**: `kueue/` (with subfolders per release version, e.g. `kueue/1.4/`, `kueue/1.5/`)
- **Browse**: https://console.cloud.google.com/storage/browser/secaut-bucket/kueue

You need a **service account key file** to upload results. If you don't have one, see [Requesting GCS Access](#requesting-gcs-access).

## APIs to Scan

Run one scan per API. The following APIs must be scanned each release:

| API Group | Version |
|-----------|---------|
| `kueue.openshift.io` | `v1` |
| `kueue.x-k8s.io` | `v1beta1` |
| `kueue.x-k8s.io` | `v1beta2` |
| `visibility.kueue.x-k8s.io` | `v1beta1` |
| `visibility.kueue.x-k8s.io` | `v1beta2` |

To verify which APIs are available on your cluster:

```bash
oc api-versions | grep -E 'kueue|visibility'
```

Check with the team whether new APIs were introduced in the current release. If so, add them to the scan list above and create an additional config file for each new API.

## Installation

Clone the RapiDAST repository and set up the Python virtual environment:

```bash
git clone https://github.com/RedHatProductSecurity/rapidast.git
cd rapidast
python3 -m venv venv
source venv/bin/activate
pip install -U pip
pip install -r requirements.txt
```

You must activate the virtual environment (`source venv/bin/activate`) every time you open a new terminal before running scans.

References:
- [RapiDAST User Guide](https://github.com/RedHatProductSecurity/rapidast/blob/development/docs/USER-GUIDE.md)
- [macOS-specific notes](https://github.com/RedHatProductSecurity/rapidast/blob/development/docs/USER-GUIDE.md#os-support)

## Configuration

### 1. Gather cluster credentials

Get the API server URL and bearer token from your cluster:

```bash
# API server URL
oc whoami --show-server
# Example: https://api.mycluster.gcp.devcluster.openshift.com:6443

# Bearer token
oc whoami -t
# Example: sha256~abc123...
```

You can also get the token from the OpenShift web console: click your username in the top right, select **Copy login command**, and copy the token from the displayed `oc login` command.

### 2. Create a scan config file

Inside the `rapidast/` directory, create a folder for the release version (e.g., `kueue15/`) and one YAML config file per API to scan.

Example config for `kueue.openshift.io/v1`:

```yaml
config:
  configVersion: 6

  # Upload results to ProdSec central storage
  googleCloudStorage:
    keyFile: "/path/to/rapidast-sa-kueue_key.json"
    bucketName: "secaut-bucket"
    directory: "kueue/<version>"  # e.g. "kueue/1.5"

application:
  shortName: "kueue-operator"
  url: "<API_SERVER_URL>"  # e.g. https://api.mycluster.gcp.devcluster.openshift.com:6443

general:
  authentication:
    type: "http_header"
    parameters:
      name: "Authorization"
      value: "Bearer <BEARER_TOKEN>"  # replace with your token

  container:
    type: "none"

scanners:
  zap:
    apiScan:
      apis:
        apiUrl: "<API_SERVER_URL>/openapi/v3/apis/kueue.openshift.io/v1"

    passiveScan:
      disabledRules: "2,10015,10024,10027,10054,10096,10109,10112"

    activeScan:
      policy: "API-scan-minimal"

    container:
      parameters:
        image: "ghcr.io/zaproxy/zaproxy:stable"
        executable: "/Applications/ZAP.app/Contents/Java/zap.sh"  # macOS path; adjust for Linux

    miscOptions:
      additionalAddons: "ascanrulesBeta"
```

Create one config file per API, changing only the `apiUrl` value:

| Config file name | `apiUrl` path |
|-----------------|---------------|
| `kueue.openshift.io-v1.yaml` | `/openapi/v3/apis/kueue.openshift.io/v1` |
| `kueue.x-k8s.io-v1beta1.yaml` | `/openapi/v3/apis/kueue.x-k8s.io/v1beta1` |
| `kueue.x-k8s.io-v1beta2.yaml` | `/openapi/v3/apis/kueue.x-k8s.io/v1beta2` |
| `visibility.kueue.x-k8s.io-v1beta1.yaml` | `/openapi/v3/apis/visibility.kueue.x-k8s.io/v1beta1` |
| `visibility.kueue.x-k8s.io-v1beta2.yaml` | `/openapi/v3/apis/visibility.kueue.x-k8s.io/v1beta2` |

## Running the Scans

Make sure the venv is activated, then run one scan at a time (each scan takes several minutes):

```bash
cd rapidast
source venv/bin/activate

./rapidast.py --config kueue15/kueue.openshift.io-v1.yaml
./rapidast.py --config kueue15/kueue.x-k8s.io-v1beta1.yaml
./rapidast.py --config kueue15/kueue.x-k8s.io-v1beta2.yaml
./rapidast.py --config kueue15/visibility.kueue.x-k8s.io-v1beta1.yaml
./rapidast.py --config kueue15/visibility.kueue.x-k8s.io-v1beta2.yaml
```

Results are saved locally under `results/kueue-operator/`. If `googleCloudStorage` is configured in the YAML, results are also automatically uploaded to the GCS bucket.

## Requesting GCS Access

To upload results to ProdSec central storage, you need a service account key file. Follow these steps to request one:

1. Go to the [secaut-bucket-access](https://gitlab.cee.redhat.com/product-security/secaut/secaut-bucket-access) GitLab repo
2. Fork the repo and update `config.yaml` with:
   ```yaml
   folder: "kueue"
   requestor: "your-email@redhat.com"
   group: "aos-node@redhat.com"
   ```
3. Submit a Merge Request to the `main` branch
4. The Security Automation team reviews and merges the MR
5. Once merged, a service account (`rapidast-sa-kueue`) is created and the JSON key file is emailed to the `requestor` address
6. Store the key file securely -- it cannot be recovered after sending

**Important notes:**

- The `requestor` field is only the email destination for the key file. It does **not** grant browser access to the GCS bucket.
- To get browser access (view results in the GCS console), you **must** set the `group` field to a valid Google Group email. Anyone in that group will have access to browse the folder.
- If you forget the `group` field, you can still upload results via RapiDAST (using the key file), but you won't be able to view them in the browser. Submit a follow-up MR to add the group.

Reference: [Exporting RapiDAST scan results to ProdSec central storage](https://redhat.atlassian.net/wiki/spaces/PRODSEC/pages/289244711)

## GCS Folder Structure

Results must follow this structure, with one subfolder per release version:

```
secaut-bucket/
  kueue/
    1.3/
      <scan results>
    1.4/
      <scan results>
    1.5/
      <scan results>
```

Set `config.googleCloudStorage.directory` in your config YAML to `kueue/<version>` (e.g., `kueue/1.5`).

The folder was originally named `kueue-1` and was renamed to `kueue` with version subfolders (see [MR !78](https://gitlab.cee.redhat.com/product-security/secaut/secaut-bucket-access/-/merge_requests/78)). After the rename, a new service account key (`rapidast-sa-kueue`) was generated and the old one (`rapidast-sa-kueue-1`) stopped working. If you still have the old key, replace it with the new one and update the `keyFile` path in all your config files.

## After the Scan

1. Optionally, attach the config YAML files and scan logs to the release DAST JIRA story (e.g., `OCPKUEUE-669`) -- not required since results are uploaded to GCS, but good practice for traceability
2. Verify results are visible in the [GCS bucket](https://console.cloud.google.com/storage/browser/secaut-bucket/kueue)
3. Tag ProdSec in the JIRA story to request triage review
4. ProdSec will review findings and create an SDLC ticket if any issues need attention (previous scans were triaged as false positives -- see [SDLC-10656](https://redhat.atlassian.net/browse/SDLC-10656))

## Konflux Integration

For automated DAST in CI pipelines, see the [Konflux RapiDAST integration docs](https://konflux.pages.redhat.com/docs/users/testing/integration/third-parties/rapidast.html).
