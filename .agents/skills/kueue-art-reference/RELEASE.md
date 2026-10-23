# Kueue Release Process

## Overview

This document describes the ART-managed release process for kueue operator.

## Stage Release (release-from-fbc pipeline)

The `release-from-fbc` Tekton pipeline triggers a stage release from FBC images.

1. Get FBC pullspecs using `get-fbc-images.sh`
2. Check integration tests using `check-its.sh`
3. Create a PipelineRun from `release-from-fbc.yaml` template:
   - Set `group` to the ocp-build-data group (e.g. `kueue-1.4`)
   - Set `assembly` to the version (e.g. `1.4.3`)
   - Set `fbc-pullspecs` to the comma-separated FBC pullspecs
   - Set `dry-run` to `true` first, then `false` for actual release
4. Apply in the `art-kueue-tenant` namespace on `kflux-ocp-p01`

```bash
oc apply -f release-from-fbc.yaml
```

## Production Release (trigger-prod-release pipeline)

1. Create an `ocp-shipment-data` MR with the release configuration
2. Create a PipelineRun from `trigger-prod-release.yaml` template:
   - Set `mr-url` to the ocp-shipment-data MR URL
   - Set `dry-run` to `true` first, then `false` for actual release
3. Apply in the `art-kueue-tenant` namespace on `kflux-ocp-p01`

```bash
oc apply -f trigger-prod-release.yaml
```

## Stage Testing

To test stage images on a cluster:

1. Set up stage registry credentials: `setup-stage-credentials.sh <auth-token>`
2. Apply the IDMS to mirror stage images: `oc apply -f idms.yaml`
3. Apply the CatalogSource: update `catalog-source.yaml` with the FBC image and apply it
4. Install the operator from the `art-kueue` catalog

## Checking Integration Tests

Before releasing, verify Konflux integration tests passed:

```bash
./check-its.sh <fbc-pullspec1>,<fbc-pullspec2>,...
```

## Inspecting FBC Content

To inspect the content of an FBC image (related images, bundles, NVRs):

```bash
./get-fbc-content.sh <fbc-pullspec>
```

## Patch Release (z-stream)

For a patch release (e.g. 1.4.3 → 1.4.4):

1. Update `group.yml` version in the `kueue-<version>` branch of `ocp-build-data`
2. Ensure upstream branch targets are correct
3. Run builds via ART
4. Follow the stage → prod release flow above

## Major/Minor Release (y-stream)

For a new minor version (e.g. 1.4 → 1.5):

1. Create a new `kueue-<version>` branch in `ocp-build-data`
2. Create Konflux applications: `kueue-<dashed-version>` and `fbc-kueue-<dashed-version>`
3. Create ReleasePlans, RPAs, and EC policies for the new version
4. Update `ocp-shipment-data` with the new ReleasePlan mappings
5. Follow the standard release flow
