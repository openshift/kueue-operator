#!/bin/bash
# Teardown: remove everything this skill created so the next run starts clean.
#
# Order matters: the Kueue CR is deleted FIRST, while the operator is still running
# and can execute its finalizer. Deleting the CSV first leaves a CR stuck on a
# finalizer (nothing is left to remove it) and the operand deployment behind.
# shellcheck shell=bash

# Polls until <kind> objects are gone. Prints progress, gives up (with a warning)
# rather than hanging: cleanup must never block a re-run indefinitely.
wait_absent() {
    local kind="$1" ns="${2:-}" timeout="${3:-120}" i out count
    for ((i = 0; i < timeout; i += 5)); do
        # A failed list is not evidence of absence: retry instead of counting it as 0.
        if [[ -n "$ns" ]]; then
            out="$(oc -n "$ns" get "$kind" -o name --ignore-not-found 2>/dev/null)" || { sleep 5; continue; }
        else
            out="$(oc get "$kind" -o name --ignore-not-found 2>/dev/null)" || { sleep 5; continue; }
        fi
        count="$(printf '%s' "$out" | grep -c . || true)"
        [[ "${count:-0}" == "0" ]] && return 0
        sleep 5
    done
    warn "${kind} still present after ${timeout}s"
    return 1
}

# Polls until a single named object is gone.
wait_object_absent() {
    local kind="$1" name="$2" ns="${3:-}" timeout="${4:-120}" i out
    for ((i = 0; i < timeout; i += 5)); do
        if [[ -n "$ns" ]]; then
            out="$(oc -n "$ns" get "$kind" "$name" -o name --ignore-not-found 2>/dev/null)" || { sleep 5; continue; }
        else
            out="$(oc get "$kind" "$name" -o name --ignore-not-found 2>/dev/null)" || { sleep 5; continue; }
        fi
        [[ -z "$out" ]] && return 0
        sleep 5
    done
    return 1
}

teardown_install() {
    local ns="$1" sub="$2" catalog="$3" catalog_ns="$4" idms="$5"
    local csv cr

    # 1. Operator-owned resources first, while the operator can still reconcile.
    if oc get crd kueues.kueue.openshift.io >/dev/null 2>&1; then
        # (CRD may disappear mid-teardown; every lookup below tolerates that)
        if [[ -n "$(oc get kueue -o name 2>/dev/null)" ]]; then
            log "deleting Kueue CR(s) - operator is still running so finalizers can complete"
            oc delete kueue --all --ignore-not-found --wait=false >/dev/null 2>&1 || true
            if ! wait_absent kueue "" 120; then
                log "clearing stuck finalizers on the remaining Kueue CR(s)"
                for cr in $(oc get kueue -o name 2>/dev/null || true); do
                    oc patch "$cr" --type=merge -p '{"metadata":{"finalizers":null}}' >/dev/null 2>&1 || true
                done
                wait_absent kueue "" 60 || true
            fi
        fi
        if oc -n "$ns" get deployment kueue-controller-manager >/dev/null 2>&1; then
            log "waiting for the operand deployment to be removed"
            if ! wait_object_absent deployment kueue-controller-manager "$ns" 180; then
                warn "operand deployment still present - deleting it directly"
                oc -n "$ns" delete deployment kueue-controller-manager --ignore-not-found --wait=false >/dev/null 2>&1 || true
            fi
        fi
    fi

    # 2. OLM objects.
    csv="$(oc -n "$ns" get subscription "$sub" -o jsonpath='{.status.installedCSV}' 2>/dev/null || true)"
    log "deleting Subscription ${sub}"
    oc -n "$ns" delete subscription "$sub" --ignore-not-found --wait=false >/dev/null 2>&1 || true
    if [[ -n "$csv" ]]; then
        log "deleting CSV ${csv}"
        oc -n "$ns" delete csv "$csv" --ignore-not-found --wait=false >/dev/null 2>&1 || true
    fi
    for extra in $(oc -n "$ns" get csv -o name 2>/dev/null | grep -i kueue-operator || true); do
        log "deleting leftover ${extra}"
        oc -n "$ns" delete "$extra" --ignore-not-found --wait=false >/dev/null 2>&1 || true
    done
    # Only plans that carry kueue CSVs - a shared --namespace may host other operators.
    for ip in $(oc -n "$ns" get installplan -o json 2>/dev/null \
        | jq -r '.items[] | select([.spec.clusterServiceVersionNames[]? | startswith("kueue-operator.")] | any) | .metadata.name' 2>/dev/null || true); do
        oc -n "$ns" delete installplan "$ip" --ignore-not-found --wait=false >/dev/null 2>&1 || true
    done

    log "deleting CatalogSource ${catalog} in ${catalog_ns}"
    oc -n "$catalog_ns" delete catalogsource "$catalog" --ignore-not-found --wait=false >/dev/null 2>&1 || true

    # 3. Mirror policy last: removing it keeps every node pulling kueue images from
    # the stage registry until MCO rolls, which silently changes the next run.
    if oc get imagedigestmirrorset "$idms" >/dev/null 2>&1; then
        log "deleting ImageDigestMirrorSet ${idms}"
        mcp_snapshot || true
        oc delete imagedigestmirrorset "$idms" --ignore-not-found --wait=false >/dev/null 2>&1 || true
        mcp_wait_rollout 45m \
            || warn "MCP rollout after IDMS removal did not complete; nodes may still resolve kueue images via the stage mirror"
    fi

    log "teardown complete: Kueue CR, Subscription, CSV(s), CatalogSource and IDMS removed"
}

# Removes the Red Hat cert-manager operator, but only when this skill installed it:
# on a cluster where it was already present it is not ours to delete.
teardown_cert_manager() {
    local ns="$1" installed_by_skill="$2"

    if [[ "$installed_by_skill" != "true" ]]; then
        log "cert-manager left in place (it was not installed by this skill)"
        return 0
    fi
    [[ -n "$ns" ]] || { warn "cert-manager namespace unknown; skipping its removal"; return 0; }

    log "removing the cert-manager operator this skill installed (${ns})"
    oc -n "$ns" delete subscription openshift-cert-manager-operator --ignore-not-found --wait=false >/dev/null 2>&1 || true
    for csv in $(oc -n "$ns" get csv -o name 2>/dev/null | grep -i cert-manager-operator || true); do
        oc -n "$ns" delete "$csv" --ignore-not-found --wait=false >/dev/null 2>&1 || true
    done
    wait_absent csv "$ns" 120 || warn "cert-manager CSVs are still present in ${ns}"
    log "cert-manager operator removed; its CRDs and operand namespace are left to the cluster"
}
