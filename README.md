# Kueue Operator

Kueue Operator provides the ability to deploy kueue using different configurations

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

1. Run `make deploy-ocp` to deploy the operator using the $OPERATOR_IMAGE and $KUEUE_IMAGE for operator and operand, respectively.

1. Run `make undeploy-ocp` to remove operator from ocp cluster

### Operator Bundle Development

1. Login into podman and have a repository created for the operator bundle.

1. Set BUNDLE_IMAGE to point to your repostory and a tag of choice

1. Run `make bundle-generate` to generate the bundle manifests

1. Run `make bundle-build` to build the `bundle.Dockerfile`.

1. Run `make bundle-push` to push the bundle image to your repository.

1. Run `operator sdk run bundle --namespace OPERATOR_NAMESPACE ${BUNDLE_IMAGE}`
to deploy operator to OPERATOR_NAMESPACE

### Local Development

1. make

1. `oc apply -f deploy/`

1. `oc apply -f deploy/crd`

1. hack/run-locally.sh

1. Optionally run `oc apply -f deploy/examples/job.yaml`

## Sample CR

```yaml
apiVersion: operator.openshift.io/v1alpha1
kind: Kueue
metadata:
  labels:
    app.kubernetes.io/name: kueue-operator
    app.kubernetes.io/managed-by: kustomize
  name: cluster
  namespace: openshift-kueue-operator
spec:
  image: "RHEL-Released-Image"
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
