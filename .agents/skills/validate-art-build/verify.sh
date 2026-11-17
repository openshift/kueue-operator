#!/bin/bash
# Post-install verification: what the ART build actually put on the cluster, and
# whether the artifact's metadata is sane. None of this costs extra runtime -
# it is all API reads against an install that already happened.
# shellcheck shell=bash

verify_install() {
    local ns="$1" csv="$2" evidence_dir="$3"
    local requested="$4" resolved="$5" channel="$6" catalog="$7" max_age="${8:-14}"

    [[ -n "$csv" ]] || die "no CSV name to verify (did install run?)"
    local csv_version cluster_version operator_image operand_image mg_image bundle_image
    csv_version="$(oc -n "$ns" get csv "$csv" -o jsonpath='{.spec.version}')" \
        || die "cannot read CSV ${csv}"
    cluster_version="$(cluster_version)"
    operator_image="$(csv_related_image "$csv" 'kueue-rhel9-operator')"
    operand_image="$(csv_related_image "$csv" 'kueue-rhel9(?!-|/)')"
    bundle_image="$(csv_related_image "$csv" 'kueue-operator-bundle')"
    mg_image="$(csv_related_image "$csv" 'must-gather')"

    cat <<EOF
---- ART build under test ----
cluster version : ${cluster_version}
catalog request : ${requested}
catalog digest  : ${resolved}
channel         : ${channel:-<package default>}
namespace       : ${ns}
CSV             : ${csv}
CSV version     : ${csv_version}
operator image  : ${operator_image:-<unresolved>}
operand image   : ${operand_image:-<unresolved>}
must-gather     : ${mg_image:-<unresolved>}
------------------------------
EOF

    verify_catalog_graph "$catalog" "$channel" "$csv" "$ns"
    verify_csv_metadata "$ns" "$csv"
    verify_related_images "$csv" "$ns"
    verify_image_wiring "$ns" "$operator_image" "$operand_image"
    verify_freshness "$requested" "$max_age"

    mkdir -p "$evidence_dir"
    jq -n \
        --arg requested "$requested" --arg resolved "$resolved" --arg channel "$channel" \
        --arg clusterVersion "$cluster_version" --arg namespace "$ns" --arg csv "$csv" \
        --arg csvVersion "$csv_version" --arg operatorImage "$operator_image" \
        --arg operandImage "$operand_image" --arg bundleImage "$bundle_image" \
        --arg mustGatherImage "$mg_image" \
        --arg capturedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        '{requested: $requested, resolved: $resolved, channel: $channel,
          clusterVersion: $clusterVersion, namespace: $namespace, csv: $csv,
          csvVersion: $csvVersion, operatorImage: $operatorImage,
          operandImage: $operandImage, bundleImage: $bundleImage,
          mustGatherImage: $mustGatherImage, capturedAt: $capturedAt}' \
        >"${evidence_dir}/tested-images.json"
    log "wrote ${evidence_dir}/tested-images.json"
}

# 1. Catalog graph: the channels ART ships, the default channel, and the
# replaces/skips chain that an upgrade depends on.
verify_catalog_graph() {
    local catalog="$1" channel="$2" csv="$3" ns="$4"
    local manifest default_channel channels replaces

    manifest="$(oc -n openshift-marketplace get packagemanifest \
        -l "catalog=${catalog}" -o json 2>/dev/null \
        | jq -c '[.items[] | select(.metadata.name=="kueue-operator")][0] // empty' || true)"
    if [[ -z "$manifest" ]]; then
        warn "catalog graph: no PackageManifest for kueue-operator in ${catalog}"
        return 0
    fi

    default_channel="$(jq -r '.status.defaultChannel // empty' <<<"$manifest" || true)"
    channels="$(jq -r '[.status.channels[].name] | join(" ")' <<<"$manifest" || true)"
    log "catalog graph: default channel '${default_channel}', channels: ${channels}"

    [[ -n "$default_channel" ]] || warn "catalog graph: PackageManifest has no defaultChannel"
    if [[ -n "$channel" && " $channels " != *" $channel "* ]]; then
        warn "catalog graph: requested channel '${channel}' is not offered by ${catalog}"
    fi
    # The release line the installed CSV belongs to must be a real channel, must be
    # the default one, and must list this very CSV. Derives everything from the CSV,
    # so a 1.4.6 build is validated the same way as a 1.5.1 one.
    local csv_version expected_channel channel_csv
    csv_version="$(oc -n "$ns" get csv "$csv" -o jsonpath='{.spec.version}' 2>/dev/null || true)"
    expected_channel="stable-v$(cut -d. -f1,2 <<<"${csv_version}")"
    if [[ -n "$csv_version" ]]; then
        if [[ " $channels " == *" $expected_channel "* ]]; then
            log "catalog graph: ${csv} ($csv_version) belongs to ${expected_channel}, which this catalog offers"
        else
            warn "catalog graph: ${expected_channel} (for CSV ${csv_version}) is missing from the catalog"
        fi
        if [[ -n "$default_channel" && "$default_channel" != "$expected_channel" ]]; then
            warn "catalog graph: default channel is ${default_channel}, expected ${expected_channel} (expected: the FBC defaults to the newest line)"
        fi
        channel_csv="$(jq -r --arg c "$expected_channel" \
            '[.status.channels[]? | select(.name==$c) | .currentCSV] | join(",")' <<<"$manifest")"
        if [[ -n "$channel_csv" ]]; then
            if [[ "$channel_csv" == *"$csv"* ]]; then
                log "catalog graph: ${expected_channel} head is ${channel_csv}"
            else
                warn "catalog graph: ${expected_channel} head is ${channel_csv}, installed CSV is ${csv}"
            fi
        fi
    fi

    local arches=() arch
    for arch in amd64 arm64 ppc64le s390x; do
        [[ "$(jq -r --arg a "operatorframework.io/arch.${arch}" '.metadata.labels[$a] // empty' <<<"$manifest")" == "supported" ]] \
            && arches+=("$arch")
    done
    if ((${#arches[@]} == 4)); then
        log "catalog graph: multi-arch supported (amd64 arm64 ppc64le s390x)"
    else
        warn "catalog graph: architectures marked supported: ${arches[*]:-none}"
    fi

    replaces="$(oc -n "$ns" get csv "$csv" -o jsonpath='{.spec.replaces}' 2>/dev/null || true)"
    if [[ -n "$replaces" ]]; then
        log "catalog graph: ${csv} replaces ${replaces}"
    else
        log "catalog graph: ${csv} has no replaces (first entry in its channel)"
    fi
}

# 2. CSV metadata an ART bundle is expected to carry.
verify_csv_metadata() {
    local ns="$1" csv="$2" display missing=() ann
    display="$(oc -n "$ns" get csv "$csv" -o jsonpath='{.spec.displayName}' || true)"
    log "csv metadata: displayName '${display}'"

    for ann in features.operators.openshift.io/disconnected \
               features.operators.openshift.io/fips-compliant \
               operatorframework.io/properties \
               operators.openshift.io/valid-subscription; do
        if [[ "$(oc -n "$ns" get csv "$csv" -o json | jq -r --arg a "$ann" '.metadata.annotations[$a] // empty')" == "" ]]; then
            missing+=("$ann")
        fi
    done
    if ((${#missing[@]})); then
        warn "csv metadata: missing annotation(s): ${missing[*]}"
    else
        log "csv metadata: standard operator annotations present"
    fi

    log "csv metadata: installModes=$(oc -n "$ns" get csv "$csv" -o json 2>/dev/null \
        | jq -r '[.spec.installModes[]? | select(.supported) | .type] | join(",")')"
}

# 3. relatedImages must carry all four ART images, all under the kueue namespace.
verify_related_images() {
    local csv="$1" ns="$2" names missing=() name image
    names="$(oc -n "$ns" get csv "$csv" -o json 2>/dev/null \
        | jq -r '.spec.relatedImages[]?.name' | tr '\n' ' ' || true)"
    for name in kueue-rhel9-operator kueue-rhel9 kueue-must-gather-rhel9; do
        [[ " $names " == *" $name "* ]] || missing+=("$name")
    done
    if ((${#missing[@]})); then
        warn "relatedImages: missing entries: ${missing[*]} (present: ${names:-none})"
    else
        log "relatedImages: operator, operand and must-gather present"
    fi

    while read -r image; do
        [[ -z "$image" ]] && continue
        [[ "$image" == registry.redhat.io/kueue/* ]] \
            || warn "relatedImages: ${image} is outside registry.redhat.io/kueue"
    done < <(oc -n "$ns" get csv "$csv" -o json | jq -r '.spec.relatedImages[]?.image')
}

# 4. The running deployments must use the CSV's digests, not a dev override.
verify_image_wiring() {
    local ns="$1" operator_image="$2" operand_image="$3"
    local running
    running="$(oc -n "$ns" get deployment openshift-kueue-operator \
        -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || true)"
    if [[ -n "$running" && -n "$operator_image" ]]; then
        if [[ "$running" == "$operator_image" ]]; then
            log "image wiring: operator deployment uses the CSV digest"
        else
            warn "image wiring: operator deployment image ${running} != CSV ${operator_image}"
        fi
    fi
    running="$(oc -n "$ns" get deployment kueue-controller-manager \
        -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || true)"
    if [[ -z "$running" ]]; then
        # Fresh install: the operand only exists once a Kueue CR does (the suite
        # creates it), so this is deferred rather than skipped.
        log "image wiring: operand not deployed yet (no Kueue CR) - re-checked after the suite"
    elif [[ -n "$operand_image" ]]; then
        if [[ "$running" == "$operand_image" ]]; then
            log "image wiring: operand deployment uses the CSV digest"
        else
            warn "image wiring: operand deployment image ${running} != CSV ${operand_image}"
        fi
    fi
}

# 5. Freshness: a floating ART tag that has not moved in a while means the run is
# green but not testing a recent build. Best-effort (needs the quay tag API).
verify_freshness() {
    local requested="$1" tag repo_path start_ts age_days max_age="${2:-14}"

    [[ "$requested" == *"@"* ]] && return 0        # pinned on purpose
    tag="${requested##*:}"
    repo_path="$(sed -E 's#^[^/]+/##' <<<"${requested%%:*}")"

    start_ts="$(curl -fsS --max-time 20 \
        "https://quay.io/api/v1/repository/${repo_path}/tag/?specificTag=${tag}" 2>/dev/null \
        | jq -r '.tags[0].start_ts // empty' 2>/dev/null || true)"
    [[ -n "$start_ts" ]] || { log "freshness: tag age unavailable (quay API)"; return 0; }

    age_days="$(( ( $(date -u +%s) - start_ts ) / 86400 ))"
    if ((age_days > max_age)); then
        warn "freshness: ${tag} was last rebuilt ${age_days} days ago (limit ${max_age}) - confirm ART is still producing builds"
    else
        log "freshness: ${tag} rebuilt ${age_days} day(s) ago"
    fi
}
