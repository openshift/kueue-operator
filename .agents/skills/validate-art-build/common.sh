#!/bin/bash
# Shared helpers for the validate-art-build skill.
# Sourced by test-art-build.sh; not meant to be executed directly.

# shellcheck shell=bash

# Human-readable output goes to stderr so command substitution (digest resolve,
# apply results) never captures log lines.
log()  { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*" >&2; }
warn() { printf '[%s] WARNING: %s\n' "$(date +%H:%M:%S)" "$*" >&2; }
die()  { printf '[%s] ERROR: %s\n' "$(date +%H:%M:%S)" "$*" >&2; exit 1; }

require_cmds() {
    local missing=()
    for c in "$@"; do
        command -v "$c" >/dev/null 2>&1 || missing+=("$c")
    done
    ((${#missing[@]} == 0)) || die "missing required command(s): ${missing[*]}"
}

# Fail immediately when the kubeconfig points nowhere: every phase needs the API,
# and a dead context otherwise surfaces as a confusing mid-run error.
require_reachable_cluster() {
    local ctx server
    ctx="$(oc config current-context 2>/dev/null || echo '<none>')"
    server="$(oc config view --minify -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null || true)"
    if ! oc whoami --request-timeout=15s >/dev/null 2>&1; then
        die "cluster is not reachable (context '${ctx}'${server:+, ${server}}) - check your login/VPN, or run 'oc login' and retry"
    fi
}

require_cluster() {
    oc whoami >/dev/null 2>&1 || die "not logged in to a cluster (oc whoami failed)"
    oc auth can-i create subscriptions.operators.coreos.com \
        -n "${OO_INSTALL_NAMESPACE}" >/dev/null 2>&1 \
        || die "insufficient permissions in ${OO_INSTALL_NAMESPACE} (need subscription create)"
}

cluster_version() {
    oc get clusterversion version -o jsonpath='{.status.desired.version}'
}

# State is shared between phases (install -> verify -> lane -> must-gather).
state_file() { printf '%s/env' "${STATE_DIR}"; }

save_state() {
    local key="$1" value="$2"
    local f
    f="$(state_file)"
    touch "$f"
    # Replace an existing key, otherwise append.
    if grep -q "^${key}=" "$f"; then
        local tmp
        tmp="$(mktemp)"
        grep -v "^${key}=" "$f" >"$tmp" || true
        printf '%s=%s\n' "$key" "$value" >>"$tmp"
        mv "$tmp" "$f"
    else
        printf '%s=%s\n' "$key" "$value" >>"$f"
    fi
}

# Only KEY=value lines are honoured: the state file is read, never executed, so a
# file planted in a predictable location cannot run code with the user's session.
load_state() {
    local f key value
    f="$(state_file)"
    [[ -f "$f" ]] || return 0
    while IFS='=' read -r key value; do
        [[ "$key" =~ ^[A-Z][A-Z0-9_]*$ ]] || continue
        export "${key}=${value}"
    done <"$f"
}

# CSV lookup helper: prints the CSV name installed by our subscription.
installed_csv() {
    local csv
    csv="$(oc -n "${OO_INSTALL_NAMESPACE}" get subscription "${SUB_NAME}" \
        -o jsonpath='{.status.installedCSV}' 2>/dev/null || true)"
    # Fall back to any kueue CSV in the namespace (e.g. installed by run-bundle).
    if [[ -z "$csv" ]]; then
        csv="$(oc -n "${OO_INSTALL_NAMESPACE}" get csv -o name 2>/dev/null \
            | grep -i 'kueue-operator' | head -1 | cut -d/ -f2 || true)"
    fi
    printf '%s' "$csv"
}

# Image pinned in the installed CSV's relatedImages. Matches by image reference,
# not by name: ART renamed the key between 1.5.0 ("must-gather") and 1.5.1
# ("kueue-must-gather-rhel9").
csv_related_image() {
    local csv="$1" pattern="$2"
    oc -n "${OO_INSTALL_NAMESPACE}" get csv "${csv}" -o json 2>/dev/null \
        | jq -r --arg p "$pattern" \
            '.spec.relatedImages[]? | select(.image | test($p)) | .image' | head -1
}
