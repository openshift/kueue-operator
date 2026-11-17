#!/bin/bash
# Preparation and environment hygiene for the operator e2e suite.
#
# The suite is not idempotent against its own leftovers: it creates the
# namespaces kueue-managed-test / kueue-unmanaged-test, a ClusterQueue
# test-clusterqueue and a ResourceFlavor named default, and its BeforeAll fails
# with AlreadyExists if any of them survived an interrupted run. Retries
# (--flake-attempts) cannot recover from that, so the leftovers are removed first.
# shellcheck shell=bash

E2E_NAMESPACES="kueue-managed-test kueue-unmanaged-test"

# Release line of the test code in a checkout: "1.4" on release-1.4, "main" elsewhere.
test_code_stream() {
    local repo="$1" branch
    branch="$(git -C "$repo" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")"
    if [[ "$branch" =~ ^release-([0-9]+\.[0-9]+)$ ]]; then
        printf '%s' "${BASH_REMATCH[1]}"
    else
        printf 'main'
    fi
}

# The installed build and the test code must belong to the same release line:
# expectations differ (e.g. webhook reinvocationPolicy on the job webhook is
# IfNeeded on 1.5+ and Never on 1.4), which looks like a build failure but is a
# test/artifact mismatch.
warn_stream_mismatch() {
    local repo="$1" build_stream="${2:-}" test_stream
    test_stream="$(test_code_stream "$repo")"
    [[ -d "$repo/.git" ]] || return 0
    if [[ -z "$build_stream" || "$build_stream" == "$test_stream" ]]; then
        return 0
    fi
    warn "test code is '${test_stream}' but the installed build is ${build_stream}.x - expectations can differ."
    warn "rerun with --test-ref release-${build_stream} to test the matching branch."
}

prepare_e2e_env() {
    local ns remaining="" i
    # "default" and "test-clusterqueue" are plausible names for real objects, so they
    # are only removed when a leftover suite namespace proves a previous run existed,
    # or when the user asks for it explicitly.
    local owned="false"
    for ns in $E2E_NAMESPACES; do
        oc get namespace "$ns" >/dev/null 2>&1 && owned="true"
    done
    [[ "${FORCE_E2E_CLEANUP:-false}" == "true" ]] && owned="true"

    if oc get crd kueues.kueue.openshift.io >/dev/null 2>&1; then
        for obj in "clusterqueue/test-clusterqueue" "resourceflavor/default"; do
            local kind="${obj%%/*}" name="${obj##*/}"
            oc get "$kind" "$name" >/dev/null 2>&1 || continue
            if [[ "$owned" == "true" ]]; then
                log "e2e prep: deleting leftover ${kind}/${name}"
                oc delete "$kind" "$name" --ignore-not-found --wait=false >/dev/null 2>&1 || true
            else
                warn "e2e prep: ${kind}/${name} exists and no leftover suite namespace was found - leaving it (use --force-e2e-cleanup to remove)"
            fi
        done
    fi

    for ns in $E2E_NAMESPACES; do
        if oc get namespace "$ns" >/dev/null 2>&1; then
            log "e2e prep: deleting leftover namespace ${ns}"
            oc delete namespace "$ns" --ignore-not-found --wait=false >/dev/null 2>&1 || true
        fi
    done

    # Namespaces take a while to terminate; the suite recreates them immediately,
    # so waiting for the deletion to finish is what makes the run repeatable.
    for ((i = 0; i < 180; i += 5)); do
        remaining=""
        for ns in $E2E_NAMESPACES; do
            oc get namespace "$ns" >/dev/null 2>&1 && remaining="${remaining} ${ns}"
        done
        [[ -z "$remaining" ]] && break
        sleep 5
    done
    if [[ -n "$remaining" ]]; then
        warn "e2e prep: namespaces still terminating:${remaining} - the suite may fail in BeforeAll"
    else
        log "e2e prep: environment clean"
    fi
}
