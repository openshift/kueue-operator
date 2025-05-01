# Kueue Operator

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

### FBC generation

Each OCP version we support will have a dir under fbc/, i.e. fbc/v4.18.  The directories will include:
- a container file for the fbc image
- a catalog template file
- a catalog/kueue-operator dir with the actual fbc fragment

On release of a new Kueue Operator, the template files for each supported OCP veersion will need additional entries for the new images in the appropriate channels.  Then the fragement will need to be regenerated with the following command:

opm alpha render-template basic <ocp_version>/catalog-template.json [--migrate-level=bundle-object-to-csv-metadata] > <ocp_version>/catalog/kueue-operator/catalog.json

NOTE: Starting with OCP 4.17 you need the --migrate-level=bundle-object-to-csv-metadata flag. For rendering to older versions of OCP, simply omit the flag.

Note: You need opm version 1.47.0 or higher

You can read more at [Konflux example repo](https://github.com/konflux-ci/olm-operator-konflux-sample/blob/main/docs/konflux-onboarding.md#building-a-file-based-catalog)

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

### Local Development with Kind Cluster
Run your changes against a cluster using `go run`

1. create a kind cluster:
unset KUBECONFIG environment variable, the cluster access configuration is stored in ${HOME}/.kube/config
```
unset KUBECONFIG
kind create cluster --name=kueue-debug
```

You can use any existing cluster you have

2. create the namespace from where the operator will run:
```
export KUBECONFIG=${HOME}/.kube/config
# make sure you are at the root of the kueue operator repo
kubectl create -f deploy/01_namespace.yaml
```

3. create the CRD
```
kubectl create -f deploy/crd/kueue-operator.crd.yaml
```

4. install cert manager
```
helm install  --repo https://charts.jetstack.io  --version v1.16.3 --create-namespace --namespace cert-manager --wait --set crds.enabled=true cert-manager cert-manager 
```

4. create the custom resource
```
❯ kubectl create -f - <<EOF
apiVersion: operator.openshift.io/v1alpha1
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
      - "BatchJob"
EOF
```

5. install monitoring CRD
```
kubectl create -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/refs/heads/main/example/prometheus-operator-crd/monitoring.coreos.com_servicemonitors.yaml
```

6. Run the operator
```
go run cmd/kueue-operator/main.go operator --kubeconfig=${HOME}/.kube/config --namespace=openshift-kueue-operator
``
