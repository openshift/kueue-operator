**Note:** This project is under active development. During development, we use container images hosted on [Quay.io](https://quay.io):
- Operator: `quay.io/repository/redhat-user-workloads/kueue-operator-tenant/kueue-operator`
- Operand: `quay.io/repository/redhat-user-workloads/kueue-operator-tenant/kueue-0-11`
# Kueue Operator

[![Go Report Card](https://goreportcard.com/badge/github.com/openshift/kueue-operator)](https://goreportcard.com/report/github.com/openshift/kueue-operator)
[![License](https://img.shields.io/badge/license-Apache--2.0-green)](https://opensource.org/licenses/Apache-2.0)

Kueue Operator provides the ability to deploy kueue using different configurations

## Reporting Issues and Requesting Features

This project tracks bugs and feature requests through Red Hat Jira. GitHub Issues on this repository are not actively monitored.

- **Bug reports:** File bugs in the [OCPBUGS](https://issues.redhat.com/projects/OCPBUGS) project with the component set to **Kueue**.
- **Feature requests / enhancements:** File RFEs in the [RFE](https://issues.redhat.com/projects/RFE) project.

## Dependencies

The Kueue Operator needs CertManager installed to operate correctly

## Releases

| ko version   | ocp version         |kueue version  | k8s version | golang |
| ------------ | ------------------- |---------------| ----------- | ------ |
| 1.0.0        | 4.19 - 4.20         |0.11.z         | 1.32        | 1.23   |

Kueue releases around 6 times a year.
For the latest Openshift version, we will take the latest version that was build with that underlying
Kubernetes version.

See [Kueue Release](https://github.com/kubernetes-sigs/kueue/blob/main/RELEASE.md) for more details
on the Kueue release policy.

## Deploy the Operator

### Quick Development Operator

1. Login into podman and have a repository created.

1. Set OPERATOR_IMAGE to point to your repostory ie export OPERATOR_IMAGE=quay.io/testrepo/kueue-operator:test

1. Build operator image: `make operator-build`

1. Push operator image to repository: `make operator-push`

1. Set $KUEUE_IMAGE to point to kueue operand image

1. Run `make deploy-cert-manager` to deploy OperatorGroup and Subscription in cert-manager-operator namespace.

1. Run `make deploy-ocp` to deploy the operator using the $OPERATOR_IMAGE and $KUEUE_IMAGE for operator and operand, respectively.

1. Run `make undeploy-ocp` to remove operator from ocp cluster

### Operator Bundle Development

1. Login into podman and have a repository created for the operator bundle.

1. Set BUNDLE_IMAGE to point to your repostory and a tag of choice

1. Run `make bundle-generate` to generate the bundle manifests

1. Run `make bundle-build` to build the `bundle.Dockerfile`.

1. Run `make bundle-push` to push the bundle image to your repository.

1. Run `make deploy-cert-manager` to deploy OperatorGroup and Subscription in cert-manager-operator namespace.

1. Set OPERATOR_NAMESPACE, i.e, "kueue-operator"

1. Run `oc new-project $OPERATOR_NAMESPACE` to create a namespace for the operaotr 

1. Run `operator-sdk run bundle --namespace $OPERATOR_NAMESPACE ${BUNDLE_IMAGE}`
to deploy operator to $OPERATOR_NAMESPACE

### Local Development

1. make

1. `oc apply -f deploy/`

1. `oc apply -f deploy/crd`

1. hack/run-locally.sh

1. Optionally run `oc apply -f deploy/examples/job.yaml`

## Sample CR

```yaml
apiVersion: kueue.openshift.io/v1
kind: Kueue
metadata:
  labels:
    app.kubernetes.io/name: kueue-operator
    app.kubernetes.io/managed-by: kustomize
  name: cluster
  namespace: openshift-kueue-operator
spec:
  config:
    integrations:
      frameworks:
      - "batch/job" 
```

### E2E Test

1. Set kubeconfig to point to a OCP cluster
1. Set OPERATOR_IMAGE to point to your operator image
1. Set KUEUE_IMAGE to point to your kueue image you want to test
1. make deploy-cert-manager test-e2e


### Enable Webhooks on Opt-In Namespaces

The Kueue Operator implements an opt-in webhook mechanism to ensure targeted enforcement of Kueue policies. To enable the validating and mutating webhooks for a specific namespace, use the following label:

```sh
oc label namespace <namespace> kueue.openshift.io/managed=true
```

This label instructs the Kueue Operator that the namespace should be managed by its webhook admission controllers. As a result, any Kueue resources within that namespace will be properly validated and mutated.

## Upgrading from Releases That Served the Kueue v1beta1 API

The Kueue CRDs that this operator installs only define `v1beta2`. The Kubernetes API server refuses to update a CRD so that it drops a version still listed in the CRD's `status.storedVersions`, and objects still stored in a dropped version could no longer be read. Kueue CRDs installed by an earlier release that served `v1beta1` may still list `v1beta1` there.

Complete the storage migration of the Kueue CRDs before upgrading, while the release that serves `v1beta1` is still installed and its Kueue operand is running. With OLM, the Manual install plan approval for the operator's Subscription keeps the upgrade from being installed before the migration is complete.

1. List the stored versions of the Kueue CRDs:

   ```sh
   oc get crd -o custom-columns=NAME:.metadata.name,STORED:.status.storedVersions | grep kueue.x-k8s.io
   ```

1. For each CRD that still lists `v1beta1`, check that its storage version is already `v1beta2`:

   ```sh
   oc get crd <name> -o jsonpath='{.spec.versions[?(@.storage==true)].name}'
   ```

   If it is still `v1beta1`, objects cannot be moved to `v1beta2` yet: first upgrade to a release whose Kueue CRDs serve `v1beta1` and store `v1beta2`, and let it update the CRDs.

1. Rewrite every object of the CRD so that it is stored as `v1beta2`, for example with Kueue's `hack/migrate-to-v1beta2.sh` (in `upstream/kueue/src/hack/`).

1. Once every object of the CRD has been rewritten, remove `v1beta1` from its stored versions:

   ```sh
   oc patch crd <name> --subresource=status --type=merge -p '{"status":{"storedVersions":["v1beta2"]}}'
   ```

See [Upgrade existing objects to a new stored version](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definition-versioning/#upgrade-existing-objects-to-a-new-stored-version) in the Kubernetes documentation.

The operator does not rewrite objects or change `status.storedVersions` itself. Before it changes any operand resource, it checks the installed counterpart of each Kueue CRD that it applies, reading it from the API server. A CRD that passed the check is not read again until the operator restarts; a CRD that failed is read again on every retry. If an installed CRD still lists a version that the new CRD drops, the operator applies no Kueue CRD, leaves the Kueue operand and its configuration unchanged, logs the error, emits a `StoredVersionsCheckFailed` warning event that names the CRD and the versions to migrate, and retries. The Kueue CR status is not updated for this failure; check the events in the operator's namespace:

```sh
oc get events -n openshift-kueue-operator --field-selector reason=StoredVersionsCheckFailed
```

The operator proceeds once the storage migration has been completed.

## Git Submodule Management

This project uses a git submodule to track the upstream Kueue repository. The submodule is located in the `upstream/kueue` directory and is used to synchronize manifests and configurations.

### Updating the Git Submodule

To update the git submodule to the latest commit:

```sh
# Update the submodule to the latest commit on its tracked branch
git submodule update --remote upstream/kueue/src

# Commit the submodule update
git add upstream/kueue/src
git commit -m "Update upstream/kueue submodule"
```

### Changing the Submodule Branch

To change which branch the submodule tracks:

```sh
# Navigate to the submodule directory
cd upstream/kueue/src

# Switch to the desired branch
git checkout <branch-name>

# Return to the main repository root
cd ../..

# Update the submodule configuration to track the new branch
git config -f .gitmodules submodule.upstream/kueue.branch <branch-name>

# Update the submodule to the latest commit on the new branch
git submodule update --remote upstream/kueue

# Commit the changes
git add .gitmodules upstream/kueue
git commit -m "Update submodule to track <branch-name> branch"
```

### Tracking Main Branch

To configure the submodule to track the main branch:

```sh
# Set the submodule to track the main branch
git config -f .gitmodules submodule.upstream/kueue.branch main

# Update to the latest commit on main
git submodule update --remote upstream/kueue

# Commit the configuration change
git add .gitmodules upstream/kueue/src
git commit -m "Configure submodule to track main branch"
```

### Synchronizing Manifests from Submodule

After updating the git submodule, you should synchronize the manifests using:

```sh
hack/sync_manifests.py --src-dir upstream/kueue/src/config/default/
```

This command processes the manifests from the submodule and prepares them for use by the operator.

## RPM Lockfile Management

The project uses RPM lockfiles in the `konflux/` directory to ensure reproducible hermetic builds. These lockfiles specify exact RPM versions and checksums for all dependencies across multiple architectures.

### Updating RPM Lockfiles

To regenerate the RPM lockfile after modifying `konflux/rpms.in.yaml`:

```sh
podman run --rm \
  -v $HOME/.docker:/root/.docker \
  -v "$(pwd):/source:Z" \
  registry.ci.openshift.org/ocp/builder:rhel-9-enterprise-base-multi-openshift-4.19 bash -c "
cd /source && \
dnf install -y pip skopeo jq && \
pip install --user 'https://github.com/konflux-ci/rpm-lockfile-prototype/archive/refs/tags/\$(curl -s https://api.github.com/repos/konflux-ci/rpm-lockfile-prototype/releases/latest | jq -r .tag_name).tar.gz' && \
/root/.local/bin/rpm-lockfile-prototype -f Dockerfile.ci.kueue konflux/rpms.in.yaml
"
```

This command:
- Uses the OpenShift enterprise base image with multi-architecture support
- Downloads the latest `rpm-lockfile-prototype` tool
- Generates `konflux/rpms.lock.yaml` with exact versions and checksums for all architectures (x86_64, aarch64, ppc64le, s390x)
- Uses `Dockerfile.ci.kueue` as the base to determine available RPM repositories

After regenerating, commit both `konflux/rpms.in.yaml` and `konflux/rpms.lock.yaml`.

## Manifest Synchronization

The `hack/sync_manifests.py` script is used to synchronize Kueue manifests from upstream releases or local development builds into the operator's bindata directory. This script handles the downloading, processing, and organization of Kueue manifests for operator deployment.

### Usage

```sh
# Sync latest Kueue release
./hack/sync_manifests.py

# Sync specific version
./hack/sync_manifests.py 0.7.1

# Use local kustomize build
./hack/sync_manifests.py --src-dir path/to/kustomize/source

# Specify custom bindata directory
./hack/sync_manifests.py --bindata-dir custom/path
```

### Features

- **Automatic Latest Version**: Fetches the latest Kueue release from GitHub if no version is specified
- **Local Development**: Supports building manifests from local kustomize sources using `--src-dir`
- **OpenShift Integration**: Automatically updates namespaces from `kueue-system` to `openshift-kueue-operator`
- **Webhook Configuration**: Updates webhook configurations for OpenShift namespace conventions
- **Image Parameterization**: Replaces deployment images with `${IMAGE}` placeholder for operator templating
- **Network Policy Labels**: Adds required OpenShift labels for network policies
- **Organized Output**: Separates manifests into appropriate files (CRDs, ClusterRoles, etc.) in the bindata directory

The script processes the following Kubernetes resources:
- Custom Resource Definitions (CRDs)
- ClusterRoles and ClusterRoleBindings
- Roles and RoleBindings
- Services and ServiceAccounts
- Deployments
- Webhook Configurations (Validating/Mutating)
- API Services
