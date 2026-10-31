# Kueue ART Reference

Reference information for kueue ART (Automated Release Tooling) build and release infrastructure.

## Downstream Repos

| Repo | Purpose |
| --- | --- |
| [openshift/kueue-operator](https://github.com/openshift/kueue-operator) | Downstream operator, operand images, and bundle |
| [openshift-priv/kueue-operator](https://github.com/openshift-priv/kueue-operator) | Private mirror used by ART builds |

**Upstream:** [kubernetes-sigs/kueue](https://github.com/kubernetes-sigs/kueue)

## ART & Konflux Dashboards

The Konflux tenant for kueue is `art-kueue-tenant` on `kflux-ocp-p01`.

### Build Dashboards (Konflux)

| Dashboard | URL |
| --- | --- |
| Component builds (1.4) | [Konflux UI](https://konflux.apps.kflux-ocp-p01.7ayg.p1.openshiftapps.com/workspaces/art-kueue/applications/kueue-1-4/activity/pipelineruns) |
| Component builds (1.5) | [Konflux UI](https://konflux.apps.kflux-ocp-p01.7ayg.p1.openshiftapps.com/workspaces/art-kueue/applications/kueue-1-5/activity/pipelineruns) |
| FBC builds (1.4) | [Konflux UI](https://konflux.apps.kflux-ocp-p01.7ayg.p1.openshiftapps.com/workspaces/art-kueue/applications/fbc-kueue-1-4/activity/pipelineruns) |
| FBC builds (1.5) | [Konflux UI](https://konflux.apps.kflux-ocp-p01.7ayg.p1.openshiftapps.com/workspaces/art-kueue/applications/fbc-kueue-1-5/activity/pipelineruns) |
| Releases (1.4) | [Konflux UI](https://konflux.apps.kflux-ocp-p01.7ayg.p1.openshiftapps.com/workspaces/art-kueue/applications/kueue-1-4/releases) |
| Releases (1.5) | [Konflux UI](https://konflux.apps.kflux-ocp-p01.7ayg.p1.openshiftapps.com/workspaces/art-kueue/applications/kueue-1-5/releases) |

### ART Build Dashboard

| Dashboard | URL |
| --- | --- |
| ART Build Health (1.4) | [ART Dashboard](https://art-dash.engineering.redhat.com/build_health/kueue-1.4) |
| ART Build Health (1.5) | [ART Dashboard](https://art-dash.engineering.redhat.com/build_health/kueue-1.5) |
| ART Build History (1.4) | [Build History](https://art-build-history-art-build-history.apps.artc2023.pc3z.p1.openshiftapps.com/?group=kueue-1.4&assembly=stream&engine=konflux&hermetic=both&buildtype=image&buildtype=bundle&buildtype=fbc) |
| ART Build History (1.5) | [Build History](https://art-build-history-art-build-history.apps.artc2023.pc3z.p1.openshiftapps.com/?group=kueue-1.5&assembly=stream&engine=konflux&hermetic=both&buildtype=image&buildtype=bundle&buildtype=fbc) |

> **Tip:** Switch between kueue versions by changing the `group` parameter in the Build History URL (e.g. `group=kueue-1.4` or `group=kueue-1.5`). You can also filter by `outcome`, `dateRange`, and other query parameters.

## Configuration

### ocp-build-data

The ocp-build-data branch for kueue follows the pattern `kueue-<version>`:

| Version | Branch | Link |
| --- | --- | --- |
| 1.4 | `kueue-1.4` | [ocp-build-data](https://github.com/openshift-eng/ocp-build-data/tree/kueue-1.4) |
| 1.5 | `kueue-1.5` | [ocp-build-data](https://github.com/openshift-eng/ocp-build-data/tree/kueue-1.5) |

Key group.yml settings (kueue-1.4):
- **Version:** 1.4.4 (next z-stream)
- **Go version:** 1.26
- **OCP target versions:** 4.18, 4.19, 5.0
- **Architectures:** x86_64, aarch64, ppc64le, s390x

### Image Configs

Images on the `kueue-1.4` branch:

| Image config | Distgit component | Delivery repo |
| --- | --- | --- |
| `kueue-operator` | kueue-rhel9-operator-container | kueue/kueue-rhel9-operator |
| `kueue` | kueue-rhel9-container | kueue/kueue-rhel9 |
| `kueue-must-gather` | kueue-must-gather-rhel9-container | kueue/kueue-must-gather-rhel9 |
| `base-rhel9` | base-rhel9-container | art-core/base-rhel9 |

The bundle image (`kueue-bundle`) is generated automatically by ART and does not have a standalone image config file.

### Branch Targets

Upstream release branches follow the pattern `release-X.Y`:
- kueue-1.4 images target `release-1.4`
- kueue-1.5 images target `release-1.5`

### bundle/image-references

The `bundle/image-references` file maps ART image names to upstream pullspecs:

| Tag | Registry pullspec |
| --- | --- |
| `kueue-rhel9-operator` | `registry.redhat.io/kueue/kueue-rhel9-operator:latest` |
| `kueue-rhel9` | `registry.redhat.io/kueue/kueue-rhel9:latest` |
| `kueue-must-gather-rhel9` | `registry.redhat.io/kueue/kueue-must-gather-rhel9:latest` |

### Konflux Release Configuration

Located in `gitlab.cee.redhat.com/releng/konflux-release-data`:

**ReleasePlanAdmissions** (in `config/kflux-ocp-p01.7ayg.p1/product/ReleasePlanAdmission/art-kueue/`):
- `kueue-advisory-stage-1-4` / `kueue-advisory-prod-1-4` — component advisory releases
- `kueue-advisory-stage-1-5` / `kueue-advisory-prod-1-5` — component advisory releases
- `kueue-fbc-stage-1-4` / `kueue-fbc-prod-1-4` — FBC releases
- `kueue-fbc-stage-1-5` / `kueue-fbc-prod-1-5` — FBC releases
- `kueue-base-images-silent-prod-rhel9` — silent base image rebuild

**EnterpriseContractPolicies** (in `config/kflux-ocp-p01.7ayg.p1/product/EnterpriseContractPolicy/`):
- `registry-art-kueue-prod` / `registry-art-kueue-stage`
- `fbc-art-kueue-prod` / `fbc-art-kueue-stage`

**ReleasePlans** (in `tenants-config/cluster/kflux-ocp-p01/tenants/art-kueue-tenant/`):

| Plan | Application | Environment |
| --- | --- | --- |
| `kueue-advisory-stage-1-4` | `kueue-1-4` | stage |
| `kueue-advisory-prod-1-4` | `kueue-1-4` | prod |
| `kueue-stage-1-4-fbc` | `fbc-kueue-1-4` | stage |
| `kueue-prod-1-4-fbc` | `fbc-kueue-1-4` | prod |

## Konflux Applications

| Application | Purpose | Tenant |
| --- | --- | --- |
| `kueue-1-4` | Operator, operand, must-gather, and bundle builds for 1.4 | `art-kueue-tenant` |
| `kueue-1-5` | Operator, operand, must-gather, and bundle builds for 1.5 | `art-kueue-tenant` |
| `fbc-kueue-1-4` | FBC fragment builds for 1.4 | `art-kueue-tenant` |
| `fbc-kueue-1-5` | FBC fragment builds for 1.5 | `art-kueue-tenant` |

## Contacts

| Role | Contact |
| --- | --- |
| ART team | [#forum-ocp-art](https://redhat-internal.slack.com/archives/CBN38N3MW) |
| Kueue engineering | [#forum-ocp-kueue](https://redhat-internal.slack.com/archives/C07GP6TUXE9) |

## Tooling

### Doozer (ART build tool)

Common commands for kueue builds:

```bash
# Check image configs
doozer --group kueue-1.4 images:print '{name}'

# Rebase images
doozer --group kueue-1.4 images:rebase

# Build images
doozer --group kueue-1.4 images:konflux:build
```

### Elliott (ART advisory tool)

```bash
# Find advisories
elliott --group kueue-1.4 find-builds

# Advisory status
elliott --group kueue-1.4 advisory-info
```

## FBC Images

FBC (File-Based Catalog) fragment images are built by the `art-fbc` application in the `ocp-art-tenant` workspace (not the kueue tenant).

FBC tag pattern: `kueue-<art_version>__v<ocp_version>__kueue-rhel9-operator`

Example: `quay.io/redhat-user-workloads/ocp-art-tenant/art-fbc:kueue-v1.4.3__v4.19__kueue-rhel9-operator`

## Release Process

1. **Version bump**: Update `group.yml` version in the `kueue-<version>` branch of `ocp-build-data`
2. **Build**: ART builds images via Konflux using doozer
3. **Bundle**: ART generates the operator bundle automatically
4. **FBC**: FBC fragment images are built after the bundle
5. **Advisory**: Elliott attaches builds to an advisory (RHBA/RHSA)
6. **Release**: Advisory ships through the release pipeline

## Test Builds

To query recent builds for kueue:

```bash
# Recent image builds
doozer --group kueue-1.4 images:print '{name} {version}-{release}'

# Check build status on ART dashboard
# https://art-dash.engineering.redhat.com/build_health/kueue-1.4
```

## Query Old Objects

To inspect Konflux objects in the kueue tenant:

```bash
# List applications
oc get applications -n art-kueue-tenant

# List components
oc get components -n art-kueue-tenant

# List release plans
oc get releaseplans -n art-kueue-tenant

# List snapshots
oc get snapshots -n art-kueue-tenant --sort-by=.metadata.creationTimestamp
```
