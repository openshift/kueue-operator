#!/bin/bash

# Best-effort cleanup: this runs after the tests have already passed, so nothing here
# may fail the step or block on a finalizer.

# Bounded delete: returns as soon as the objects are gone, but never lets a stuck
# finalizer eat the step timeout. oc's own --timeout covers the wait, so no external
# timeout binary is needed. --ignore-not-found keeps a partially applied deployment
# (e.g. an OLM install) from turning the warning below into a false alarm.
bounded_delete() {
  local limit=$1
  shift
  oc delete --ignore-not-found --timeout="$limit" "$@" || echo "warning: 'oc delete $*' did not finish within $limit" >&2
}

# Delete every object of a Kueue resource type, if that type is still served.
delete_kueue_objects() {
  if oc get crd "$1" >/dev/null 2>&1; then
    bounded_delete 120s "$1" --all -A
  fi
}

delete_kueue_objects workloads.kueue.x-k8s.io
delete_kueue_objects localqueues.kueue.x-k8s.io
delete_kueue_objects clusterqueues.kueue.x-k8s.io
delete_kueue_objects resourceflavors.kueue.x-k8s.io
if oc get crd kueues.kueue.openshift.io >/dev/null 2>&1; then
  oc delete -f test/e2e/bindata/assets/08_kueue_default.yaml --wait=false
fi

# Delete the visibility APIServices before the CRDs: a missing CRD orphans them
# (dead backing service, stale discovery, wedged namespace deletion). They are owned
# by the Kueue CR, so give it a bounded chance to go away first. A stuck finalizer
# must not skip the APIService cleanup, and it must not wedge the CRD deletion
# either, so drop the finalizers once the wait expires.
if oc get kueue.kueue.openshift.io/cluster >/dev/null 2>&1; then
  if ! oc wait --for=delete kueue.kueue.openshift.io/cluster --timeout=120s; then
    echo "warning: Kueue CR cluster still present after 120s, removing its finalizers" >&2
    oc patch kueue.kueue.openshift.io/cluster --type=merge -p '{"metadata":{"finalizers":[]}}' || true
  fi
fi
bounded_delete 120s apiservice v1beta1.visibility.kueue.x-k8s.io v1beta2.visibility.kueue.x-k8s.io

# Bounded: a CRD that still holds a Kueue CR cannot be deleted until that CR is
# gone, and blocking here would consume the step timeout after the tests passed.
bounded_delete 120s -f deploy/crd/
# deploy/ carries the operator Namespace, so this waits on namespace finalization too.
bounded_delete 120s -f deploy/
kueue_crds=$(oc get crds -o name 2>/dev/null | grep kueue || true)
if [ -n "$kueue_crds" ]; then
  # shellcheck disable=SC2086 # word splitting is intended: one CRD name per word
  oc delete $kueue_crds --ignore-not-found --wait=false
fi

# The operator can recreate the APIServices while its pods terminate. Now that the
# CRDs are gone, sweep once more so neither a stale APIService nor a recreating
# operator can wedge the namespace deletion below.
oc delete apiservice v1beta1.visibility.kueue.x-k8s.io v1beta2.visibility.kueue.x-k8s.io --ignore-not-found --wait=false

# Never block on namespace finalization: a stuck namespace must not eat the step timeout.
oc delete -f hack/manifests/cert-manager-rh.yaml --ignore-not-found --wait=false \
  || echo "warning: 'oc delete -f hack/manifests/cert-manager-rh.yaml' failed" >&2
oc delete namespaces cert-manager --ignore-not-found --wait=false \
  || echo "warning: 'oc delete namespaces cert-manager' failed" >&2
