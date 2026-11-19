#!/bin/bash
# Install path: cert-manager (if needed), stage registry mirroring, ART FBC
# catalog, and the OLM Subscription that installs the operator.
# Sourced by test-art-build.sh; not meant to be executed directly.
# shellcheck shell=bash


# MachineConfigPool rollout gating for pull-secret / IDMS changes.
#
# A pull-secret update and an IDMS creation both trigger an MCO rollout, but a
# pool can still report Updated=True from an earlier change. Two protections:
#   1. snapshot the pools before the change (mcp_snapshot), so "did it start" is
#      observable;
#   2. require at least one non-settled observation before accepting settled,
#      with a grace period for the case where the change needs no rollout.
# Mirrors the gating in the merge-stage-registry-credentials CI step, which is
# stricter than the best-effort `oc wait --for=condition=Updated` used by
# kueue-operator-enable-catalogsource-art.

# shellcheck shell=bash

MCP_SNAPSHOT_DIR=""

# True when every pool reports a finished rollout with all machines updated.
mcp_settled() {
    oc get mcp -o json 2>/dev/null | jq -e '
        (.items | length) > 0 and
        all(.items[];
            (any(.status.conditions[]?; .type=="Updated" and .status=="True")) and
            (any(.status.conditions[]?; .type=="Updating" and .status=="True") | not) and
            (.status.updatedMachineCount == .status.machineCount)
        )' >/dev/null 2>&1
}

mcp_summary() {
    oc get mcp -o json 2>/dev/null | jq -r '
        .items[] | "  \(.metadata.name): updated=\(.status.updatedMachineCount)/\(.status.machineCount) updating=\([.status.conditions[]?|select(.type=="Updating")|.status]|join(","))"
    ' || true
}

mcp_snapshot() {
    MCP_SNAPSHOT_DIR="${STATE_DIR}/mcp-snapshot-$(date +%s)"
    mkdir -p "$MCP_SNAPSHOT_DIR"

    local pools
    pools="$(oc get mcp -o jsonpath='{.items[*].metadata.name}')" || true
    if [[ -z "$pools" ]]; then
        warn "cluster has no MachineConfigPools (not an OCP node cluster?) - skipping rollout gate"
        return 1
    fi

    local p
    for p in $pools; do
        oc get mcp "$p" -o jsonpath='{.status.configuration.name}' \
            >"${MCP_SNAPSHOT_DIR}/${p}" 2>/dev/null || true
    done
    log "snapshot taken of pools: ${pools}"
    return 0
}

mcp_wait_rollout() {
    local timeout="${1:-45m}" timeout_s deadline saw_rollout="false" last_log=0 settled_since=""

    # Clusters without MCO (kind, libvirt guests) have no pools to wait for, but a
    # failed API call must not be mistaken for one of those clusters.
    local pools
    if ! pools="$(oc get mcp -o jsonpath='{.items[*].metadata.name}')"; then
        warn "could not list MachineConfigPools; waiting anyway"
    elif [[ -z "$pools" ]]; then
        log "no MachineConfigPools on this cluster - skipping the rollout gate"
        return 0
    fi
    timeout_s="$(awk -v t="$timeout" 'BEGIN { if (t ~ /h$/) { sub(/h$/,"",t); print t*3600 } else { sub(/m$/,"",t); print t*60 } }')"
    deadline=$((SECONDS + timeout_s))

    # MCO needs a moment to react; an immediate poll still sees the pre-change state.
    sleep 30

    while ((SECONDS < deadline)); do
        if mcp_settled; then
            if [[ "$saw_rollout" == "true" ]]; then
                mcp_summary | while read -r line; do log "$line"; done
                log "all MachineConfigPools rolled out"
                return 0
            fi
            [[ -z "$settled_since" ]] && settled_since=$SECONDS
            # Settled but no rollout seen yet: either MCO has not reacted or the
            # change needs no rollout (e.g. an identical IDMS re-applied). Accept
            # after a grace period instead of burning the whole timeout.
            if ((SECONDS - settled_since >= 300)); then
                log "pools stayed settled for 5m - the change required no rollout"
                return 0
            fi
            if ((SECONDS - last_log > 60)); then
                log "pools still report settled; waiting for the rollout to start"
                last_log=$SECONDS
            fi
        else
            saw_rollout="true"
            settled_since=""
        fi

        if ((SECONDS - last_log > 60)); then
            mcp_summary | while read -r line; do log "$line"; done
            last_log=$SECONDS
        fi
        sleep 15
    done

    mcp_summary | while read -r line; do log "$line"; done
    warn "MCP rollout did not finish within ${timeout}"
    return 1
}

# FBC resolution + CatalogSource bring-up.
#
# ART pre-release digests are not on registry.redhat.io yet, so the FBC's
# registry.redhat.io/kueue references are redirected to registry.stage.redhat.io
# with an IDMS. The catalog is then served from a CatalogSource built on the
# resolved art-fbc digest.

# shellcheck shell=bash

# Resolve the floating art-fbc tag to an immutable digest. Prints the digest.
resolve_fbc_digest() {
    local ref="$1" digest=""
    log "resolving ${ref} to a digest"
    digest="$(oc image info --show-multiarch "$ref" -o json 2>/dev/null \
        | jq -r 'if type=="array" then .[0].listDigest else .digest end')" || true
    if [[ -z "$digest" || "$digest" == "null" ]]; then
        warn "oc image info could not resolve ${ref}; falling back to skopeo"
        digest="$(skopeo inspect --no-tags "docker://${ref}" 2>/dev/null | jq -r '.Digest // ""')" || true
    fi
    [[ -n "$digest" && "$digest" != "null" ]] || die "could not resolve ${ref} to a digest"
    printf '%s' "$digest"
}

apply_idms() {
    local name="$1" out
    log "applying ImageDigestMirrorSet ${name}"
    out="$(oc apply -f - 2>&1 <<EOF
apiVersion: config.openshift.io/v1
kind: ImageDigestMirrorSet
metadata:
  name: ${name}
spec:
  imageDigestMirrors:
  - source: registry.redhat.io/kueue
    mirrors:
    - registry.stage.redhat.io/kueue
EOF
)" || die "failed to apply IDMS ${name}: ${out}"
    printf '%s
' "$out" >&2
    # "unchanged" means no MCO rollout will follow.
    [[ "$out" == *unchanged* ]] && return 1
    return 0
}

create_catalog_source() {
    local name="$1" image="$2" ns="${3:-openshift-marketplace}"
    log "creating CatalogSource ${name} in ${ns} -> ${image}"
    oc apply -f - >/dev/null <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: ${name}
  namespace: ${ns}
spec:
  sourceType: grpc
  image: ${image}
  displayName: Kueue ART FBC Operator Catalog
  publisher: grpc
  updateStrategy:
    registryPoll:
      interval: 10m0s
EOF
}

wait_catalog_ready() {
    local name="$1" ns="${2:-openshift-marketplace}" timeout="${3:-600}" i state
    log "waiting for CatalogSource ${name} to become READY (max ${timeout}s)"
    for ((i = 0; i < timeout; i += 15)); do
        state="$(oc -n "$ns" get catalogsource "$name" \
            -o jsonpath='{.status.connectionState.lastObservedState}' 2>/dev/null || true)"
        [[ "$state" == "READY" ]] && { log "CatalogSource READY after ${i}s"; return 0; }
        if [[ "$state" == "TRANSIENT_FAILURE" || "$state" == "ERROR" ]]; then
            oc -n "$ns" get pods -l "olm.catalogSource=${name}" 2>/dev/null || true
            warn "CatalogSource state: ${state} (still waiting)"
        fi
        sleep 15
    done
    oc -n "$ns" get pods -l "olm.catalogSource=${name}" -o wide 2>/dev/null || true
    die "CatalogSource ${name} did not become READY within ${timeout}s"
}

# PackageManifest for our catalog must exist before a Subscription can resolve.
# Select by the catalog label: several catalogs can serve the same package name.
wait_packagemanifest() {
    local source="$1" ns="${2:-openshift-marketplace}" timeout="${3:-300}" i
    log "waiting for PackageManifest kueue-operator from ${source}"
    for ((i = 0; i < timeout; i += 10)); do
        if oc -n "$ns" get packagemanifest -l "catalog=${source}" -o name 2>/dev/null \
            | grep -q 'kueue-operator$'; then
            return 0
        fi
        sleep 10
    done
    oc -n "$ns" get packagemanifest -l "catalog=${source}" -o name 2>/dev/null || true
    die "PackageManifest kueue-operator not served by ${source} within ${timeout}s"
}

# OLM installation: OperatorGroup + Subscription, CSV wait, deployment readiness.
# shellcheck shell=bash

create_operator_group() {
    local ns="$1"
    if oc -n "$ns" get operatorgroup -o name 2>/dev/null | grep -q .; then
        log "OperatorGroup already present in ${ns}"
        return 0
    fi
    log "creating OperatorGroup in ${ns}"
    oc apply -f - >/dev/null <<EOF
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: kueue-operator-og
  namespace: ${ns}
spec: {}
EOF
}

create_subscription() {
    local ns="$1" sub="$2" source="$3" source_ns="$4" channel="$5"
    local channel_line=""
    [[ -n "$channel" ]] && channel_line="  channel: ${channel}"

    log "creating Subscription ${sub} (catalog ${source}, channel '${channel:-<default>}')"
    oc apply -f - >/dev/null <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: ${sub}
  namespace: ${ns}
spec:
  name: kueue-operator
  source: ${source}
  sourceNamespace: ${source_ns}
${channel_line}
EOF
}

# Waits for the CSV installed by the subscription and sets CSV_NAME.
# expect_csv (optional) is the channel head resolved from the catalog: requiring it
# stops a re-run from reporting the previous, already-Succeeded CSV.
wait_csv_succeeded() {
    local ns="$1" sub="$2" timeout="${3:-900}" expect_csv="${4:-}" i csv phase reason
    log "waiting for CSV to reach Succeeded (max ${timeout}s)"
    for ((i = 0; i < timeout; i += 15)); do
        csv="$(oc -n "$ns" get subscription "$sub" -o jsonpath='{.status.installedCSV}' 2>/dev/null || true)"
        if [[ -n "$csv" ]]; then
            phase="$(oc -n "$ns" get csv "$csv" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
            if [[ -n "$expect_csv" && "$csv" != "$expect_csv" ]]; then
                log "CSV ${csv} is installed, waiting for ${expect_csv} (${i}s)"
                sleep 15
                continue
            fi
            case "$phase" in
                Succeeded)
                    log "CSV ${csv} Succeeded after ${i}s"
                    # shellcheck disable=SC2034  # consumed by test-art-build.sh
                    CSV_NAME="$csv"
                    return 0
                    ;;
                Failed)
                    reason="$(oc -n "$ns" get csv "$csv" -o jsonpath='{.status.reason}' 2>/dev/null || true)"
                    oc -n "$ns" get csv "$csv" -o yaml 2>/dev/null | tail -30
                    die "CSV ${csv} failed: ${reason:-unknown reason}"
                    ;;
                *) log "CSV ${csv}: ${phase:-pending} (${i}s)" ;;
            esac
        fi
        sleep 15
    done

    oc -n "$ns" get subscription,csv,installplan 2>/dev/null || true
    die "CSV for ${sub} did not reach Succeeded within ${timeout}s"
}

# Waits for the operator deployment and, once a Kueue CR exists, the operand.
wait_operator_ready() {
    local ns="$1" timeout="${2:-600}"
    if oc -n "$ns" get deployment openshift-kueue-operator >/dev/null 2>&1; then
        log "waiting for deployment/openshift-kueue-operator"
        oc -n "$ns" rollout status deployment/openshift-kueue-operator --timeout="${timeout}s" >/dev/null \
            || warn "openshift-kueue-operator rollout did not report success"
    else
        warn "deployment/openshift-kueue-operator not found in ${ns}"
    fi

    if oc -n "$ns" get deployment kueue-controller-manager >/dev/null 2>&1; then
        log "waiting for deployment/kueue-controller-manager"
        oc -n "$ns" wait --for=condition=Available deployment/kueue-controller-manager \
            --timeout="${timeout}s" >/dev/null || warn "kueue-controller-manager not Available"
    else
        log "no Kueue CR yet - operand deployment not created (expected until a CR exists)"
    fi
}

# cert-manager is the only prerequisite this skill installs or expects.
#
# It must be the Red Hat build (package openshift-cert-manager-operator from
# redhat-operators), not the community cert-manager operator. Both can be
# present in a catalog, and the community one leaves the operator without the
# webhooks it needs.

# shellcheck shell=bash

# Where the operator gets installed when we install it. When it is already there it
# may live in a different namespace (e.g. cert-manager-operator), so detection reads
# the namespace from the Subscription instead of assuming one.
CM_NS="openshift-cert-manager-operator"
CM_PACKAGE="openshift-cert-manager-operator"
CM_CHANNEL="stable-v1"

cm_installed_rh() {
    local ns csv
    read -r ns csv < <(oc get sub -A -o json 2>/dev/null | jq -r '
        .items[] | select(.spec.name=="openshift-cert-manager-operator")
        | "\(.metadata.namespace) \(.status.installedCSV // "")"' | head -1) || return 1
    [[ -n "${ns:-}" && -n "${csv:-}" ]] || return 1
    [[ "$(oc -n "$ns" get csv "$csv" -o jsonpath='{.status.phase}' 2>/dev/null)" == "Succeeded" ]] || return 1
    CM_NS="$ns"
    printf '%s' "$csv"
}

cm_community_present() {
    oc get sub -A -o json 2>/dev/null | jq -e '
        .items[] | select(.spec.name=="cert-manager" and (.spec.source|test("community")))' >/dev/null 2>&1
}

check_cert_manager() {
    local csv
    if csv="$(cm_installed_rh)"; then
        log "cert-manager: Red Hat operator present (${csv})"
        return 0
    fi
    if cm_community_present; then
        die "only the community cert-manager operator is installed; this skill needs the Red Hat build (${CM_PACKAGE} from redhat-operators). Rerun with --with-cert-manager after removing the community operator."
    fi
    warn "Red Hat cert-manager operator not detected; the operator webhooks and the e2e suite will fail"
    return 1
}

# Returns 0 when the operator was already present, 1 when this call installed it.
install_rh_cert_manager() {
    local csv

    if csv="$(cm_installed_rh)"; then
        log "cert-manager: Red Hat operator already installed (${csv})"
        return 0
    fi
    if cm_community_present; then
        die "community cert-manager operator is installed; remove it before installing ${CM_PACKAGE}"
    fi

    log "installing Red Hat cert-manager operator into ${CM_NS} (channel ${CM_CHANNEL})"
    oc create namespace "$CM_NS" 2>/dev/null || true
    oc apply -f - >/dev/null <<EOF
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: cert-manager-operator-og
  namespace: ${CM_NS}
spec:
  targetNamespaces:
  - ${CM_NS}
  upgradeStrategy: Default
---
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: ${CM_PACKAGE}
  namespace: ${CM_NS}
spec:
  channel: ${CM_CHANNEL}
  name: ${CM_PACKAGE}
  installPlanApproval: Automatic
  source: redhat-operators
  sourceNamespace: openshift-marketplace
EOF

    local i
    for ((i = 0; i < 600; i += 15)); do
        csv="$(oc -n "$CM_NS" get sub "$CM_PACKAGE" -o jsonpath='{.status.installedCSV}' 2>/dev/null || true)"
        if [[ -n "$csv" && "$(oc -n "$CM_NS" get csv "$csv" -o jsonpath='{.status.phase}' 2>/dev/null || true)" == "Succeeded" ]]; then
            log "cert-manager CSV ${csv} Succeeded"
            oc -n cert-manager wait --for=condition=Available deployment --all --timeout=300s >/dev/null 2>&1 \
                || warn "cert-manager operand deployments not all Available yet"
            return 1
        fi
        sleep 15
    done
    die "Red Hat cert-manager operator did not install within 10m"
}
