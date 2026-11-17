#!/bin/bash
# validate-art-build - validate a Kueue ART build on a live cluster.
#
# Flow: (optional cert-manager) -> stage IDMS -> ART FBC CatalogSource ->
# Subscription -> verify (images, catalog graph, CSV metadata) -> operator e2e
# subset -> must-gather. Every phase is idempotent and shares state.
#
# Mode A (this script): one ART build tag/digest, fresh install, developer loop.
# Mode B (see SKILL.md): z-stream upgrade validation, QE evidence runbook.
set -Eeuo pipefail

SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${SKILL_DIR}/common.sh"
# shellcheck source=/dev/null
source "${SKILL_DIR}/install.sh"
# shellcheck source=/dev/null
source "${SKILL_DIR}/verify.sh"
# shellcheck source=/dev/null
source "${SKILL_DIR}/e2e.sh"
# shellcheck source=/dev/null
source "${SKILL_DIR}/teardown.sh"

FBC_REPO="quay.io/redhat-user-workloads/ocp-art-tenant/art-fbc"
FBC_TAG=""
FBC_DIGEST=""
CHANNEL=""
OO_INSTALL_NAMESPACE="openshift-kueue-operator"
SUB_NAME="kueue-operator"
CATALOG_NAME="art-kueue"
CATALOG_NAMESPACE="openshift-marketplace"
IDMS_NAME="kueue-stage-registry"
PHASE="all"
E2E_SCOPE="operator"
E2E_SCOPE_REQUESTED=""
# JobSet and LeaderWorkerSet are not part of this setup, so the specs that need
# them are skipped rather than failed.
E2E_SKIP='(?i)jobset|leaderworker'
# Mirrors the label filter of `make e2e-ci-test`; only used when a checkout's Makefile
# predates the GINKGO_EXTRA_ARGS passthrough (release-1.4/1.5 branches).
E2E_CI_LABEL_FILTER='!disruptive && !flaky'
EVIDENCE_DIR=""
EVIDENCE_DIR_EXPLICIT="false"
STATE_DIR=""
STAGE_AUTH=""
SKIP_STAGE_CREDS="false"
WITH_CR="false"
WITH_CERT_MANAGER="false"
TEST_REF=""
AUTO_TEST_REF="true"
TEARDOWN_CERT_MANAGER="false"
FORCE_E2E_CLEANUP="false"
KUEUE_VERSION=""
OCP_VERSION=""
MAX_AGE_DAYS="14"
ASSUME_YES="false"
TEARDOWN_AFTER="false"
CSV_NAME=""

# --- helpers ----------------------------------------------------------------

# --stage-auth accepts a dockerconfigjson, a base64 'user:token' string or a raw
# 'user:token' pair. setup-stage-credentials.sh expects the base64 form.
normalize_stage_auth() {
    local value="$1" decoded

    if [[ -f "$value" ]]; then
        if jq -e . >/dev/null 2>&1 <"$value"; then
            decoded="$(jq -r '.auths["registry.stage.redhat.io"].auth // empty' <"$value")"
            [[ -n "$decoded" ]] || die "${value} has no registry.stage.redhat.io auth entry"
            printf '%s' "$decoded"
            return 0
        fi
        value="$(<"$value")"
    fi

    if decoded="$(printf '%s' "$value" | base64 -d 2>/dev/null)" && [[ "$decoded" == *:* ]]; then
        printf '%s' "$value"
        return 0
    fi
    if [[ "$value" == *:* ]]; then
        printf '%s' "$value" | base64
        return 0
    fi
    die "cannot interpret --stage-auth (expected a file, base64 'user:token', or 'user:token')"
}

# Relative --state-dir/--evidence-dir break once the suite cd's into its worktree.
make_absolute() {
    case "$1" in
        /*) printf '%s' "$1" ;;
        *) printf '%s/%s' "$PWD" "$1" ;;
    esac
}

# --- build selection: version -> tag/channel, prompts, validation ----------

FBC_TAG_PATTERN="kueue-<stream>__v<ocp>__kueue-rhel9-operator"
# Set by resolve_target; initialised here so `set -u` callers are safe.
FBC_REQUESTED="${FBC_REQUESTED:-}"
FBC_RESOLVED="${FBC_RESOLVED:-}"
KUEUE_STREAM="${KUEUE_STREAM:-}"
OCP_TARGET="${OCP_TARGET:-}"
PREDICTED_VERSION="${PREDICTED_VERSION:-}"

# 1.4.6 / v1.4 / 1.4 -> 1.4
normalize_kueue_version() {
    local v="$1"
    v="${v#v}"
    [[ "$v" =~ ^[0-9]+\.[0-9]+ ]] || die "unrecognised Kueue version '${1}' (expected e.g. 1.4 or 1.4.6)"
    printf '%s' "$(cut -d. -f1,2 <<<"$v")"
}

# 5.0.0-0.nightly-... / 4.22.3 -> 5.0 / 4.22
normalize_ocp_version() {
    local v="$1"
    [[ "$v" =~ ^[0-9]+\.[0-9]+ ]] || die "unrecognised OCP version '${1}' (expected e.g. 4.22 or 5.0)"
    printf '%s' "$(cut -d. -f1,2 <<<"$v")"
}

cluster_ocp_version() {
    normalize_ocp_version "$(cluster_version 2>/dev/null || echo '')" 2>/dev/null || true
}

# Stream from a tag: kueue-1.4__v5.0__... -> 1.4
stream_from_tag() {
    local tag="$1"
    [[ "$tag" =~ ^kueue-([0-9]+\.[0-9]+)__ ]] || return 1
    printf '%s' "${BASH_REMATCH[1]}"
}

# OCP version from a tag: kueue-1.4__v5.0__... -> 5.0
ocp_from_tag() {
    local tag="$1"
    [[ "$tag" =~ __v([0-9]+\.[0-9]+)__ ]] || return 1
    printf '%s' "${BASH_REMATCH[1]}"
}

tag_exists() {
    local tag="$1" repo_path count
    repo_path="$(sed -E 's#^[^/]+/##' <<<"${FBC_REPO}")"
    count="$(curl -fsS --max-time 20 \
        "https://quay.io/api/v1/repository/${repo_path}/tag/?specificTag=${tag}" 2>/dev/null \
        | jq -r '[.tags[]? | select(.name=="'"${tag}"'")] | length' 2>/dev/null || echo 0)"
    [[ "${count:-0}" != "0" ]]
}

# Nearby tags for the same stream, so a bad OCP version gets an actionable hint.
suggest_tags() {
    local stream="$1" repo_path
    repo_path="$(sed -E 's#^[^/]+/##' <<<"${FBC_REPO}")"
    curl -fsS --max-time 20 \
        "https://quay.io/api/v1/repository/${repo_path}/tag/?limit=100&filter_tag_name=like:kueue-${stream}__" 2>/dev/null \
        | jq -r '.tags[]?.name' 2>/dev/null \
        | grep -E "^kueue-[0-9.]+__v[0-9.]+__kueue-rhel9-operator$" | sort -u || true
}

# Version the channel currently points at, read from the ART build metadata.
# The FBC tag does not encode a z-stream, but ART publishes per-OCP NVR tags
# (v<ocp>__operator_nvr__kueue-operator-container-<version>-<buildtime>...), which do.
channel_head_version() {
    local stream="$1" ocp="$2" repo_path
    repo_path="$(sed -E 's#^[^/]+/##' <<<"${FBC_REPO}")"
    curl -fsS --max-time 25 \
        "https://quay.io/api/v1/repository/${repo_path}/tag/?limit=100&filter_tag_name=like:v${ocp}__operator_nvr__kueue-operator-container-" 2>/dev/null \
        | jq -r '.tags[]? | "\(.name)\t\(.start_ts)"' 2>/dev/null \
        | sed -E 's/^v[0-9.]+__operator_nvr__kueue-operator-container-([0-9]+\.[0-9]+\.[0-9]+)-[0-9]+\..*/\1/' \
        | awk -F'\t' -v s="$stream" '$1 ~ ("^" s "\\.[0-9]+$") {print $1; exit}' || true
}

prompt_target() {
    local kueue_in

    # Only the Kueue release is asked for: the OCP version is taken from the cluster
    # the user is already logged into.
    while [[ -z "${KUEUE_VERSION}" ]]; do
        printf 'Kueue version (e.g. 1.4, 1.5): ' >&2
        read -r kueue_in || die "no Kueue version given"
        [[ -n "$kueue_in" ]] && KUEUE_VERSION="$(normalize_kueue_version "$kueue_in")"
    done

    if [[ -z "${OCP_VERSION}" ]]; then
        OCP_VERSION="$(cluster_ocp_version)"
        [[ -n "$OCP_VERSION" ]] || die "cannot determine the cluster's OCP version; pass --ocp-version"
        log "OCP version taken from the cluster: ${OCP_VERSION}"
    fi
}

confirm_target() {
    [[ "$ASSUME_YES" == "true" ]] && return 0
    [[ -t 0 ]] || return 0

    cat >&2 <<EOF

  build     : ${FBC_REQUESTED}
  channel   : ${CHANNEL:-<package default>}
  z-stream  : ${PREDICTED_VERSION:-unknown (will be read from the catalog)}
  cluster   : $(oc whoami --show-server 2>/dev/null) ($(cluster_version 2>/dev/null))

Proceed? [Y/n] 
EOF
    local answer
    read -r answer || answer=""
    case "${answer:-y}" in
        y|Y|yes|YES) return 0 ;;
        *) die "aborted by user" ;;
    esac
}

# Sets FBC_REQUESTED, FBC_RESOLVED, CHANNEL, KUEUE_STREAM, OCP_TARGET.
resolve_target() {
    local stream ocp

    if [[ -n "$FBC_DIGEST" ]]; then
        FBC_REQUESTED="${FBC_REPO}@${FBC_DIGEST}"
    elif [[ -z "$FBC_TAG" ]]; then
        if [[ -n "$KUEUE_VERSION" || -n "$OCP_VERSION" ]]; then
            [[ -n "$KUEUE_VERSION" ]] || die "--ocp-version given without --kueue-version"
            KUEUE_VERSION="$(normalize_kueue_version "$KUEUE_VERSION")"
            # OCP version defaults to the cluster the skill is pointed at.
            [[ -n "$OCP_VERSION" ]] || OCP_VERSION="$(cluster_ocp_version)"
            [[ -n "$OCP_VERSION" ]] || die "cannot determine the cluster's OCP version; pass --ocp-version"
            OCP_VERSION="$(normalize_ocp_version "$OCP_VERSION")"
        elif [[ -t 0 ]]; then
            prompt_target
        else
            # No terminal to prompt on: a terse error would hide every option.
            usage >&2
            exit 1
        fi
        FBC_TAG="kueue-${KUEUE_VERSION}__v${OCP_VERSION}__kueue-rhel9-operator"
    fi

    if [[ -n "${FBC_TAG}" ]]; then
        stream="$(stream_from_tag "$FBC_TAG" || true)"
        ocp="$(ocp_from_tag "$FBC_TAG" || true)"
        if [[ -z "$stream" ]]; then
            warn "tag '${FBC_TAG}' does not match ${FBC_TAG_PATTERN}; the channel cannot be derived"
        fi
        FBC_REQUESTED="${FBC_REPO}:${FBC_TAG}"
    fi

    KUEUE_STREAM="${stream:-}"
    OCP_TARGET="${ocp:-}"

    if [[ -n "$OCP_TARGET" ]]; then
        local cluster_ocp
        cluster_ocp="$(cluster_ocp_version)"
        if [[ -n "$cluster_ocp" && "$cluster_ocp" != "$OCP_TARGET" ]]; then
            warn "tag targets OCP ${OCP_TARGET} but the cluster is ${cluster_ocp}; the FBC may not match this cluster"
        fi
    fi

    if [[ -n "$KUEUE_STREAM" && -n "$OCP_TARGET" ]]; then
        PREDICTED_VERSION="$(channel_head_version "$KUEUE_STREAM" "$OCP_TARGET")"
    fi

    if [[ -z "$CHANNEL" && -n "$KUEUE_STREAM" ]]; then
        CHANNEL="stable-v${KUEUE_STREAM}"
        log "channel derived from the tag: ${CHANNEL}"
    fi

    if [[ -z "$FBC_RESOLVED" && "$FBC_REQUESTED" != *"@"* ]]; then
        [[ -n "$FBC_TAG" ]] || die "cannot resolve a tag - pass --fbc-tag or --kueue-version/--ocp-version"
        if ! tag_exists "$FBC_TAG"; then
            local suggestions
            suggestions="$(suggest_tags "${KUEUE_STREAM:-1.5}" | head -8)"
            die "tag '${FBC_TAG}' does not exist in ${FBC_REPO}${suggestions:+ - available for this stream:
${suggestions}}"
        fi
        FBC_RESOLVED="${FBC_REPO}@$(resolve_fbc_digest "$FBC_REQUESTED")"
    else
        FBC_RESOLVED="${FBC_RESOLVED:-$FBC_REQUESTED}"
    fi
}

usage() {
    cat <<'HELP_EOF'
validate-art-build - validate a Kueue ART build on a live OpenShift cluster

USAGE
  test-art-build.sh [build selection] [options]

  Installs the operator from an ART FBC catalog (images mirrored from the stage
  registry), verifies what landed on the cluster, runs the operator e2e subset and
  collects must-gather. Everything except the build selection is derived.

EXAMPLES
  # interactive: asks for the Kueue version; the OCP version comes from the cluster
  test-art-build.sh

  # scripted
  test-art-build.sh --kueue-version 1.5
  test-art-build.sh --kueue-version 1.4 --ocp-version 4.22
  test-art-build.sh --fbc-tag kueue-1.5__v4.21__kueue-rhel9-operator

  # fastest form: no e2e, just install + verify + must-gather (~5 min)
  test-art-build.sh --kueue-version 1.5 --e2e none

  # whole CI suite instead of the operator subset (~60 min)
  test-art-build.sh --kueue-version 1.5 --e2e full

  # single phases; they share state, so they can run in any order after install
  test-art-build.sh --kueue-version 1.5 --phase install
  test-art-build.sh --phase verify
  test-art-build.sh --phase teardown

BUILD SELECTION (one of)
  (no flag)              prompt for the Kueue version (interactive terminal only)
  --kueue-version VER    Kueue stream, e.g. 1.5 or 1.4.6 (the z-stream is discovered)
  --ocp-version VER      OCP version, e.g. 5.0 (default: the cluster's version)
  --fbc-tag TAG          explicit art-fbc tag, e.g. kueue-1.4__v4.22__kueue-rhel9-operator
  --fbc-digest DIGEST    pin an immutable catalog digest instead of a floating tag
  --fbc-repo REPO        catalog repo
                         (default: quay.io/redhat-user-workloads/ocp-art-tenant/art-fbc)
  --channel CHANNEL      subscription channel (default: stable-v<stream> from the tag)

CLUSTER AND INSTALL
  --namespace NS         operator namespace (default: openshift-kueue-operator)
  --catalog-name NAME    CatalogSource name (default: art-kueue)
  --idms-name NAME       ImageDigestMirrorSet name (default: kueue-stage-registry)
  --stage-auth VALUE     stage registry credentials: file path, base64 'user:token',
                         or raw 'user:token' (only needed if not already in the pull
                         secret - it is detected automatically)
  --skip-stage-creds     assume registry.stage.redhat.io is already in the pull secret
  --with-cert-manager    install the Red Hat cert-manager operator if absent
  --with-cr              create a default Kueue CR so the operand deploys
  --test-ref REF         run the suite from another branch/tag in a git worktree
                         (default: chosen automatically when the checkout's branch does
                         not match the installed release line)
  --no-auto-test-ref     always use the suite from the current checkout

RUN CONTROL
  --phase PHASE          install | verify | e2e | must-gather | teardown | all
                         (default: all)
  --e2e SCOPE            operator | none | full   (default: operator)
                           operator  18 specs from e2e_operator_test.go: install,
                                     config rendering, webhooks, metrics (~5 min)
                           none      skip the suite (install + verify + must-gather)
                           full      whole e2e-ci-test suite (~60 min)
  --max-age-days N       warn when the floating tag is older than N days (default 14)
  --teardown-after       tear the install down again after 'all'
  --force-e2e-cleanup    remove leftover e2e resources (ClusterQueue test-clusterqueue,
                         ResourceFlavor default) even without proof of a prior run
  --teardown-cert-manager
                         also remove the Red Hat cert-manager operator, but only when
                         this skill installed it
  --evidence-dir DIR     where tested-images.json, JUnit and must-gather go
                         (default: ./art-evidence/<timestamp>)
  --state-dir DIR        phase state (default: $TMPDIR/validate-art-build/<cluster>)
  -y, --yes              skip the confirmation prompt
  -h, --help             this summary

WHAT IT DOES
  optional cert-manager -> stage IDMS (MCP rollout gate) -> tag to digest ->
  CatalogSource -> channel head check -> Subscription -> CSV Succeeded ->
  verify (images, catalog graph, CSV metadata, wiring, freshness) -> e2e ->
  must-gather

PREREQUISITES
  - cluster with kube:admin (no kind/libvirt: no OLM, no IDMS)
  - oc, jq, skopeo (podman only if stage credentials must be added)
  - Red Hat cert-manager operator (--with-cert-manager installs it)
  - a kueue-operator checkout when running e2e (the lanes are Makefile targets)

OUTPUT
  <evidence-dir>/tested-images.json         FBC request + digest, CSV, images, channel
  <evidence-dir>/e2e-operator-junit.xml
  <evidence-dir>/must-gather/

JobSet and LeaderWorkerSet are not part of this setup, so their specs are skipped.
Disruptive specs are excluded and DRA is not exercised.
HELP_EOF
}

while (($#)); do
    case "$1" in
        --fbc-tag) FBC_TAG="$2"; shift 2 ;;
        --kueue-version) KUEUE_VERSION="$2"; shift 2 ;;
        --ocp-version) OCP_VERSION="$2"; shift 2 ;;
        --max-age-days) MAX_AGE_DAYS="$2"; shift 2 ;;
        -y|--yes) ASSUME_YES="true"; shift ;;
        --fbc-digest) FBC_DIGEST="$2"; shift 2 ;;
        --fbc-repo) FBC_REPO="$2"; shift 2 ;;
        --channel) CHANNEL="$2"; shift 2 ;;
        --namespace) OO_INSTALL_NAMESPACE="$2"; shift 2 ;;
        --catalog-name) CATALOG_NAME="$2"; shift 2 ;;
        --idms-name) IDMS_NAME="$2"; shift 2 ;;
        --stage-auth) STAGE_AUTH="$(normalize_stage_auth "$2")"; shift 2 ;;
        --skip-stage-creds) SKIP_STAGE_CREDS="true"; shift ;;
        --with-cert-manager) WITH_CERT_MANAGER="true"; shift ;;
        --with-cr) WITH_CR="true"; shift ;;
        --test-ref) TEST_REF="$2"; shift 2 ;;
        --no-auto-test-ref) AUTO_TEST_REF="false"; shift ;;
        --phase) PHASE="$2"; shift 2 ;;
        --e2e) E2E_SCOPE="$2"; shift 2 ;;
        --teardown-after) TEARDOWN_AFTER="true"; shift ;;
        --force-e2e-cleanup) export FORCE_E2E_CLEANUP="true"; shift ;;
        --teardown-cert-manager) TEARDOWN_CERT_MANAGER="true"; shift ;;
        --evidence-dir) EVIDENCE_DIR="$2"; EVIDENCE_DIR_EXPLICIT="true"; shift 2 ;;
        --state-dir) STATE_DIR="$2"; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) die "unknown argument: $1 (try --help)" ;;
    esac
done

case "$PHASE" in install|verify|e2e|must-gather|teardown|all) ;; *) die "invalid --phase: ${PHASE}" ;; esac
E2E_SCOPE_REQUESTED="$E2E_SCOPE"
case "$E2E_SCOPE" in operator|none|full) ;; *) die "invalid --e2e: ${E2E_SCOPE}" ;; esac

CLUSTER_DOMAIN="$(oc get dns cluster -o jsonpath='{.spec.baseDomain}' 2>/dev/null || echo unclustered)"
STATE_DIR="${STATE_DIR:-${TMPDIR:-/tmp}/validate-art-build/${CLUSTER_DOMAIN}}"

# One install, one evidence directory: reuse the path saved by the first phase so a
# later --phase verify/e2e/must-gather does not scatter the run across directories.
if [[ -z "$EVIDENCE_DIR" && -f "$(state_file)" ]]; then
    EVIDENCE_DIR="$(grep -E '^EVIDENCE_DIR=' "$(state_file)" 2>/dev/null | cut -d= -f2- || true)"
fi
[[ -n "$EVIDENCE_DIR" ]] || EVIDENCE_DIR="${PWD}/art-evidence/$(date +%Y%m%d-%H%M%S)"
[[ -n "$STATE_DIR" ]] && STATE_DIR="$(make_absolute "$STATE_DIR")"
[[ -n "$EVIDENCE_DIR" ]] && EVIDENCE_DIR="$(make_absolute "$EVIDENCE_DIR")"
mkdir -p "$STATE_DIR" "$EVIDENCE_DIR"
[[ "$EVIDENCE_DIR_EXPLICIT" == "true" ]] || save_state EVIDENCE_DIR "$EVIDENCE_DIR"
log "evidence: ${EVIDENCE_DIR}"
chmod 700 "$STATE_DIR" 2>/dev/null || warn "could not restrict permissions on ${STATE_DIR}"

phase_install() {
    require_reachable_cluster
    require_cmds oc jq skopeo
    require_cluster

    local requested resolved stage_present
    resolve_target
    requested="$FBC_REQUESTED"
    resolved="$FBC_RESOLVED"

    if [[ "$WITH_CERT_MANAGER" == "true" ]]; then
        if install_rh_cert_manager; then
            save_state CERT_MANAGER_INSTALLED_BY_SKILL "false"
        else
            save_state CERT_MANAGER_INSTALLED_BY_SKILL "true"
        fi
        save_state CERT_MANAGER_NAMESPACE "$CM_NS"
    elif ! check_cert_manager; then
        if [[ -t 0 && "$ASSUME_YES" != "true" ]]; then
            local answer
            printf 'Install the Red Hat cert-manager operator now? [Y/n] ' >&2
            read -r answer || answer=""
            case "${answer:-y}" in
                y|Y|yes|YES) install_rh_cert_manager ;;
                *) warn "continuing without cert-manager" ;;
            esac
        fi
        # The operator deploys but its operand never comes up without cert-manager
        # (event CertManagerMissing), so a run with e2e is wasted work.
        if [[ "$E2E_SCOPE" != "none" ]] && ! cm_installed_rh >/dev/null; then
            die "cert-manager is not installed - rerun with --with-cert-manager, or use --e2e none to install without it"
        fi
    fi

    confirm_target
    log "testing ${requested} (channel ${CHANNEL:-<package default>})"

    stage_present="$(oc get secret/pull-secret -n openshift-config -o jsonpath='{.data.\.dockerconfigjson}' \
        | base64 -d | jq -r '.auths | keys[]' | grep -cx 'registry.stage.redhat.io' || true)"
    if [[ "$SKIP_STAGE_CREDS" == "true" ]]; then
        log "skipping stage credential setup (--skip-stage-creds)"
    elif [[ "$stage_present" != "0" ]]; then
        log "registry.stage.redhat.io credentials already in the global pull secret"
    else
        [[ -n "$STAGE_AUTH" ]] || die "no stage credentials in the pull secret; pass --stage-auth or --skip-stage-creds"
        require_cmds podman
        mcp_snapshot || true
        "${SKILL_DIR}/setup-stage-credentials.sh" "$STAGE_AUTH"
        mcp_wait_rollout 45m || die "MCP rollout did not complete after the pull-secret change"
    fi

    mcp_snapshot || true
    if apply_idms "$IDMS_NAME"; then
        mcp_wait_rollout 45m || die "MCP rollout did not complete after the IDMS change"
    else
        log "IDMS unchanged - no rollout to wait for"
    fi

    create_catalog_source "$CATALOG_NAME" "$resolved" "$CATALOG_NAMESPACE"
    wait_catalog_ready "$CATALOG_NAME" "$CATALOG_NAMESPACE"
    wait_packagemanifest "$CATALOG_NAME" "$CATALOG_NAMESPACE"

    local head
    head="$(oc -n "$CATALOG_NAMESPACE" get packagemanifest -l "catalog=${CATALOG_NAME}" -o json 2>/dev/null \
        | jq -r --arg c "${CHANNEL}" \
            '[.items[] | select(.metadata.name=="kueue-operator")][0].status.channels[]?
             | select(.name==$c) | .currentCSV' 2>/dev/null || true)"
    if [[ -n "$head" ]]; then
        log "catalog channel ${CHANNEL:-<default>} head: ${head}"
        if [[ -n "${PREDICTED_VERSION:-}" && "$head" != *"${PREDICTED_VERSION}"* ]]; then
            warn "channel head is ${head}, but the ART metadata suggested ${PREDICTED_VERSION}"
        fi
    fi

    log "creating namespace ${OO_INSTALL_NAMESPACE}"
    oc create namespace "$OO_INSTALL_NAMESPACE" 2>/dev/null || true
    oc label namespace "$OO_INSTALL_NAMESPACE" openshift.io/cluster-monitoring=true --overwrite >/dev/null
    create_operator_group "$OO_INSTALL_NAMESPACE"
    create_subscription "$OO_INSTALL_NAMESPACE" "$SUB_NAME" "$CATALOG_NAME" "$CATALOG_NAMESPACE" "$CHANNEL"

    wait_csv_succeeded "$OO_INSTALL_NAMESPACE" "$SUB_NAME" 900 "$head"
    wait_operator_ready "$OO_INSTALL_NAMESPACE" 600

    if [[ "$WITH_CR" == "true" ]]; then
        create_default_cr "$OO_INSTALL_NAMESPACE"
        wait_operator_ready "$OO_INSTALL_NAMESPACE" 600
    fi

    save_state FBC_REQUESTED "$requested"
    save_state FBC_RESOLVED "$resolved"
    save_state CHANNEL "$CHANNEL"
    save_state CSV_NAME "$CSV_NAME"
    save_state INSTALL_NAMESPACE "$OO_INSTALL_NAMESPACE"
    save_state CLUSTER_VERSION "$(cluster_version)"
    save_state CLUSTER_SERVER "$(oc whoami --show-server)"
    save_state KUEUE_STREAM "${KUEUE_STREAM:-}"
    save_state OCP_TARGET "${OCP_TARGET:-}"

    save_state PREDICTED_VERSION "${PREDICTED_VERSION:-}"
    log "install complete: ${CSV_NAME} from ${resolved}"
}

create_default_cr() {
    local ns="$1" repo_root
    repo_root="$(cd "${SKILL_DIR}/../../.." && pwd)"
    if [[ -f "${repo_root}/test/e2e/bindata/assets/08_kueue_default.yaml" ]]; then
        log "applying the repo's default Kueue CR"
        sed "s|namespace: openshift-kueue-operator|namespace: ${ns}|" \
            "${repo_root}/test/e2e/bindata/assets/08_kueue_default.yaml" | oc apply -f - >/dev/null
    else
        log "applying a minimal Kueue CR"
        oc apply -f - >/dev/null <<EOF
apiVersion: kueue.openshift.io/v1
kind: Kueue
metadata:
  name: cluster
  namespace: ${ns}
spec:
  managementState: Managed
  config:
    integrations:
      frameworks:
      - BatchJob
      - Pod
EOF
    fi
}

phase_verify() {
    require_reachable_cluster
    load_state
    [[ -n "${FBC_RESOLVED:-}" ]] || die "no saved state - run --phase install first"
    verify_install "$OO_INSTALL_NAMESPACE" "${CSV_NAME:-}" "$EVIDENCE_DIR" \
        "${FBC_REQUESTED:-}" "${FBC_RESOLVED:-}" "${CHANNEL:-}" "$CATALOG_NAME" "$MAX_AGE_DAYS"
}

phase_e2e() {
    require_reachable_cluster
    load_state
    # The state file records the previous run's scope; the current request wins.
    E2E_SCOPE="${E2E_SCOPE_REQUESTED:-$E2E_SCOPE}"
    if [[ "$E2E_SCOPE" == "none" ]]; then
        log "--e2e none: skipping the test suite"
        return 0
    fi

    local repo_root current_server
    repo_root="$(cd "${SKILL_DIR}/../../.." && pwd)"
    [[ -f "${repo_root}/Makefile" ]] || die "not running inside the kueue-operator repo (${repo_root})"

    current_server="$(oc whoami --show-server 2>/dev/null || echo unknown)"
    if [[ -n "${CLUSTER_SERVER:-}" && "$current_server" != "$CLUSTER_SERVER" ]]; then
        die "current cluster (${current_server}) is not the one installed on (${CLUSTER_SERVER}); re-run with the right KUBECONFIG or --phase install"
    fi

    check_cert_manager || true

    # Interrupted runs leave the suite's own resources behind and its BeforeAll
    # then fails with AlreadyExists - retries cannot fix that.
    prepare_e2e_env

    # The suite and the build must belong to the same release line. When they do not,
    # pick the matching branch automatically instead of asking the user for a flag.
    local test_dir="$repo_root" effective_ref="$TEST_REF" checkout_stream auto_selected="false"
    checkout_stream="$(test_code_stream "$repo_root")"
    if [[ -z "$effective_ref" && "$AUTO_TEST_REF" == "true" \
          && -n "${KUEUE_STREAM:-}" && "$checkout_stream" != "$KUEUE_STREAM" ]]; then
        effective_ref="release-${KUEUE_STREAM}"
        auto_selected="true"
        log "test code is ${checkout_stream} but the build is ${KUEUE_STREAM}.x -> using ${effective_ref}"
    fi

    if [[ -n "$effective_ref" ]]; then
        test_dir="${STATE_DIR}/worktree-${effective_ref//\//-}"
        if [[ ! -d "$test_dir" ]]; then
            log "creating worktree for test code at ${effective_ref}"
            # Fork remotes usually lack release-* branches; the upstream does.
            git -C "$repo_root" fetch origin "$effective_ref" --quiet 2>/dev/null || true
            git -C "$repo_root" fetch upstream "$effective_ref" --quiet 2>/dev/null || true
            git -C "$repo_root" worktree add -f "$test_dir" "origin/${effective_ref}" >/dev/null 2>&1 \
                || git -C "$repo_root" worktree add -f "$test_dir" "upstream/${effective_ref}" >/dev/null 2>&1 \
                || git -C "$repo_root" worktree add -f "$test_dir" "$effective_ref" >/dev/null 2>&1 \
                || { if [[ "$auto_selected" == "true" ]]; then
                         warn "cannot check out ${effective_ref}; running the suite from the current checkout instead"
                         test_dir="$repo_root"
                     else
                         die "cannot create a worktree for ${effective_ref}"
                     fi; }
        fi
        log "running the suite from ${test_dir} (${effective_ref})"
    else
        warn_stream_mismatch "$test_dir" "${KUEUE_STREAM:-}"
    fi

    mkdir -p "$EVIDENCE_DIR"
    export ARTIFACT_DIR="$EVIDENCE_DIR"

    local ginkgo_bin="${repo_root}/bin/ginkgo"
    [[ -x "$ginkgo_bin" ]] || ( cd "$repo_root" && make ginkgo >/dev/null )
    export KUEUE_MUST_GATHER_IMAGE="${MUST_GATHER_IMAGE:-$(csv_related_image "${CSV_NAME:-}" 'must-gather')}"
    [[ -n "$KUEUE_MUST_GATHER_IMAGE" ]] && log "KUEUE_MUST_GATHER_IMAGE=${KUEUE_MUST_GATHER_IMAGE}"

    case "$E2E_SCOPE" in
        operator)
            # The operator-level surface: install, rendered config, webhook,
            # namespace opt-in, metrics. Disruptive specs tear the CR down, so
            # they stay out; JobSet/LWS are not part of this setup.
            log "running the e2e_operator_test.go suite"
            ( cd "$test_dir" && \
              "$ginkgo_bin" --keep-going --flake-attempts=2 --timeout=60m \
                --label-filter='!disruptive && !flaky' --skip="$E2E_SKIP" \
                --focus-file='e2e_operator_test.go' \
                --junit-report="${ARTIFACT_DIR}/e2e-operator-junit.xml" \
                --no-color -v ./test/e2e/... )
            ;;
        full)
            # Uses the repo's own CI target, so the filter/timeout cannot drift from CI.
            # --skip goes through the Makefile's GINKGO_EXTRA_ARGS passthrough.
            log "running the full operator e2e suite (CI filter + JobSet/LWS skipped)"
            if grep -q 'GINKGO_EXTRA_ARGS' "$test_dir/Makefile" 2>/dev/null; then
                ( cd "$test_dir" && make e2e-ci-test GINKGO_EXTRA_ARGS="--skip=${E2E_SKIP}" )
            else
                # release-1.4/1.5 Makefiles predate the passthrough: run ginkgo directly
                # with the same flags so the skip still applies.
                warn "this checkout's Makefile has no GINKGO_EXTRA_ARGS - running ginkgo directly with the CI filter"
                mkdir -p "$ARTIFACT_DIR"
                ( cd "$test_dir" && \
                  "$ginkgo_bin" --keep-going --flake-attempts=3 --timeout=120m \
                    --label-filter="${E2E_CI_LABEL_FILTER}" --skip="$E2E_SKIP" \
                    --junit-report="${ARTIFACT_DIR}/e2e-junit.xml" --no-color -v ./test/e2e/... )
            fi
            ;;
    esac
    # The suite creates the Kueue CR, so the operand deployment only exists now.
    # Completing the deferred wiring check here is the whole point of running e2e.
    if [[ -n "${CSV_NAME:-}" ]]; then
        log "re-checking operator/operand image wiring after the suite"
        verify_image_wiring "$OO_INSTALL_NAMESPACE" \
            "$(csv_related_image "$CSV_NAME" 'kueue-rhel9-operator')" \
            "$(csv_related_image "$CSV_NAME" 'kueue-rhel9(?!-|/)')" \
            2>&1 | tee "${EVIDENCE_DIR}/post-e2e-wiring.log"
    fi

    save_state E2E_SCOPE "$E2E_SCOPE"
}

phase_must_gather() {
    require_reachable_cluster
    load_state
    local csv="" mg="" dest=""
    csv="${CSV_NAME:-}"
    [[ -n "$csv" ]] || die "no saved CSV - run --phase install first"
    mg="$(csv_related_image "$csv" 'must-gather' || true)"
    [[ -n "$mg" ]] || die "no must-gather image in CSV ${csv} relatedImages"

    dest="${EVIDENCE_DIR}/must-gather"
    log "collecting must-gather with ${mg}"
    oc adm must-gather --image="$mg" --dest-dir="$dest" --timeout=20m
    save_state MUST_GATHER_IMAGE "$mg"
    save_state MUST_GATHER_DIR "$dest"
    log "must-gather written to ${dest}"
}

phase_teardown() {
    require_reachable_cluster
    load_state
    teardown_install "$OO_INSTALL_NAMESPACE" "$SUB_NAME" "$CATALOG_NAME" "$CATALOG_NAMESPACE" "$IDMS_NAME"
    if [[ "$TEARDOWN_CERT_MANAGER" == "true" ]]; then
        teardown_cert_manager "${CERT_MANAGER_NAMESPACE:-}" "${CERT_MANAGER_INSTALLED_BY_SKILL:-false}"
    fi
}

case "$PHASE" in
    install) phase_install ;;
    verify) phase_verify ;;
    e2e) phase_e2e ;;
    must-gather) phase_must_gather ;;
    teardown) phase_teardown ;;
    all)
        phase_install
        phase_verify
        phase_e2e
        phase_must_gather || warn "must-gather phase failed; install, verify and e2e are still valid"
        if [[ "$TEARDOWN_AFTER" == "true" ]]; then phase_teardown; fi
        ;;
esac
