**Note:** This project is under active development. During development, we use container images hosted on [Quay.io](https://quay.io):
- Operator: `quay.io/repository/redhat-user-workloads/kueue-operator-tenant/kueue-operator`
- Operand: `quay.io/repository/redhat-user-workloads/kueue-operator-tenant/kueue-0-11`
# Kueue Operator

[![Go Report Card](https://goreportcard.com/badge/github.com/openshift/kueue-operator)](https://goreportcard.com/report/github.com/openshift/kueue-operator)
[![License](https://img.shields.io/badge/license-Apache--2.0-green)](https://opensource.org/licenses/Apache-2.0)

Kueue Operator provides the ability to deploy kueue using different configurations

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
