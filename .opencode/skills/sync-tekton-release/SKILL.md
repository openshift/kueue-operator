---
name: sync-tekton-release
description: Use when the user asks to sync, create, or update .tekton/ pipeline YAML files for a new release branch (e.g., release-1.4, release-1.5). Covers syncing Tekton Konflux PipelineRun files from one release version to another while preserving newer task digests and pipeline improvements.
---

# Sync Tekton Release Files

This skill generates `.tekton/*-<NEW_VERSION>-*.yaml` files for a new release branch by taking the corresponding files from a source release branch as the structural template, then applying version substitutions and preserving newer task digests/pipeline improvements.

## When to use

- User asks to sync/create/update Tekton files for a new release
- User asks to create `.tekton/` pipeline files for `release-X.Y` based on `release-A.B`
- User references "tekton files" and a release branch version

## Components

There are 4 components, each with a pull-request and push variant (8 files total):

| Component       | Build type          | Dockerfile          | CEL path filter (push)                  |
|-----------------|---------------------|---------------------|-----------------------------------------|
| kueue-operator  | buildah-remote-oci-ta | Dockerfile         | Negative match (excludes bundle/must-gather paths) |
| kueue-operand   | buildah-remote-oci-ta | Dockerfile.kueue   | Negative match (excludes bundle/must-gather paths) |
| kueue-must-gather | buildah-remote-oci-ta | must-gather/Dockerfile | Positive match (must-gather paths only) |
| kueue-bundle    | buildah-oci-ta (NOT remote) | bundle.Dockerfile | Positive match (bundle paths only) |

## Workflow

### Step 1: Extract source files

Extract the versioned tekton files from the source branch:

```bash
# Example: source is release-1.3, files are *-1-3-*.yaml
git show release-<SOURCE>:.tekton/<component>-<SRC_VER>-pull-request.yaml
git show release-<SOURCE>:.tekton/<component>-<SRC_VER>-push.yaml
```

The version in filenames uses dashes not dots (e.g., `1-3` not `1.3`).

### Step 2: Version substitution

Replace all occurrences:
- `<SRC_VER>` -> `<NEW_VER>` (e.g., `1-3` -> `1-4`) in names, labels, image refs, CEL expressions
- `release-<SOURCE>` -> `release-<TARGET>` (e.g., `release-1.3` -> `release-1.4`) in target branch references

### Step 3: Update task digests

If the user wants to keep newer task digests (from existing files on the target branch or from a reference), replace the old task references with the new ones. The task references follow this pattern:

```yaml
value: quay.io/konflux-ci/tekton-catalog/<task-name>:<version>@sha256:<digest>
```

Common tasks that get updated:
- `task-init`
- `task-git-clone-oci-ta`
- `task-prefetch-dependencies-oci-ta`
- `task-buildah-remote-oci-ta` (operator, operand, must-gather)
- `task-buildah-oci-ta` (bundle only)
- `task-build-image-index`
- `task-source-build-oci-ta`
- `task-deprecated-image-check`
- `task-clair-scan`
- `task-ecosystem-cert-preflight-checks`
- `task-sast-snyk-check-oci-ta`
- `task-clamav-scan`
- `task-sast-shell-check-oci-ta`
- `task-sast-unicode-check-oci-ta`
- `task-apply-tags`
- `task-push-dockerfile-oci-ta`
- `task-rpms-signature-scan`

### Step 4: Set spec.params for hermetic builds (operator and operand only)

The operator and operand pipelines MUST have `hermetic`, `prefetch-input`, and `build-source-image` set at the **`spec.params`** level (the PipelineRun inputs), not just in `pipelineSpec.params` defaults. Without these, hermetic builds run with network isolation but no prefetched RPMs, causing `microdnf install` / `dnf install` to fail.

Add these under `spec.params` (after `dockerfile`) for **operator and operand** pipelines only:

```yaml
spec:
  params:
  # ... git-url, revision, output-image, image-expires-after, build-platforms, dockerfile ...
  - name: hermetic
    value: "true"
  - name: prefetch-input
    value: '{"type": "rpm", "path": "konflux"}'
  - name: build-source-image
    value: "true"
  pipelineSpec:
```

**Bundle and must-gather do NOT get these params** — they match the main branch pattern where these are omitted.

Always cross-reference with the corresponding `main` pipeline files to confirm which components need `spec.params` set.

### Step 5: Apply pipeline improvements

If newer pipeline template features exist, apply them. Common changes include:

1. **New pipeline params** (e.g., `enable-package-registry-proxy`, `sast-target-dirs`) - add to `pipelineSpec.params` section
2. **New task params** - wire new pipeline params into specific tasks (e.g., `enable-package-registry-proxy` into `prefetch-dependencies`, `TARGET_DIRS` into sast tasks)
3. **Removed task params** - when a task version bumps, some params may be removed (e.g., `COMMIT_SHA` and `IMAGE_EXPIRES_AFTER` were removed from `build-image-index` 0.3)
4. **Default value changes** - For release branches, `hermetic` and `build-source-image` defaults in `pipelineSpec.params` MUST be set to `"true"` (they default to `"false"` in main/template pipelines). Always verify and update these after generating files.
5. **Bundle-specific changes** - `prefetch-input` default may differ for bundle components

### Step 6: Preserve structural elements from source

These elements should match the source release structure:

- **CEL expressions**: Complex `on-cel-expression` with `files.all.exists(...)` path filters
  - Operator pull-request and push: both use `files.all.exists(...)` with negative path matching (triggers on everything EXCEPT bundle/must-gather paths)
  - Operand/must-gather pull-request: simple `event == "pull_request" && target_branch == "release-X.Y"` (no path filter)
  - Operand/must-gather push: include `files.all.exists(...)` with path filters
  - Bundle pull-request and push: both use `files.all.exists(...)` with positive path matching
- **`creationTimestamp:`** field in metadata
- **Multi-arch platforms**: `linux/x86_64`, `linux/s390x`, `linux/arm64`, `linux/ppc64le`
- **Single-line description strings** (not wrapped)

## Key structural patterns

### CEL expression patterns

**operator/operand push** (negative match - triggers on everything EXCEPT bundle/must-gather):
```yaml
pipelinesascode.tekton.dev/on-cel-expression: event == "push" && target_branch == "release-X.Y" && files.all.exists(path, !path.matches('upstream/kueue/src|bundle/|bundle.Dockerfile|.tekton/kueue-bundle-*|bundle.developer.Dockerfile|must-gather/|.tekton/kueue-must-gather-*|related_images.json|related_images.developer.json|bundle/manifests/kueue-operator.clusterserviceversion.yaml'))
```

**must-gather push** (positive match):
```yaml
pipelinesascode.tekton.dev/on-cel-expression: event == "push" && target_branch == "release-X.Y" && files.all.exists(path, path.matches('must-gather/|must-gather/Dockerfile|.tekton/kueue-must-gather-<VER>-*'))
```

**bundle pull-request** (positive match):
```yaml
pipelinesascode.tekton.dev/on-cel-expression: event == "pull_request" && target_branch == "release-X.Y" && files.all.exists(path, path.matches('bundle/|bundle.Dockerfile|.tekton/kueue-bundle-<VER>-*|related_images.json'))
```

**bundle push** (positive match):
```yaml
pipelinesascode.tekton.dev/on-cel-expression: event == "push" && target_branch == "release-X.Y" && files.all.exists(path, path.matches('bundle/|bundle.Dockerfile|.tekton/kueue-bundle-<VER>-*|related_images.json'))
```

**operator pull-request** (negative match - same pattern as push):
```yaml
pipelinesascode.tekton.dev/on-cel-expression: event == "pull_request" && target_branch == "release-X.Y" && files.all.exists(path, !path.matches('upstream/kueue/src|bundle/|bundle.Dockerfile|bundle.developer.Dockerfile|.tekton/kueue-bundle-*|must-gather/|.tekton/kueue-must-gather-*|related_images.json|related_images.developer.json|bundle/manifests/kueue-operator.clusterserviceversion.yaml'))
```

### Service account naming

```yaml
taskRunTemplate:
  serviceAccountName: build-pipeline-<component>-<VER>
```

### Labels

```yaml
labels:
  appstudio.openshift.io/application: kueue-operator-<VER>
  appstudio.openshift.io/component: <component>-<VER>
```

### Output image

```yaml
# Pull request:
output-image: quay.io/redhat-user-workloads/kueue-operator-tenant/<component>-<VER>:on-pr-{{revision}}
# Push:
output-image: quay.io/redhat-user-workloads/kueue-operator-tenant/<component>-<VER>:{{revision}}
```

## Verification

After generating files, verify:

1. Each file has the correct CEL expression pattern
2. `creationTimestamp:` is present
3. Multi-arch platforms are listed
4. Task digests are updated (if newer versions were requested)
5. New pipeline params are properly wired into tasks
6. Removed task params are gone
7. Bundle files use `buildah-oci-ta` (not `buildah-remote-oci-ta`)
8. Service account names, labels, and output images use the correct version
9. `hermetic` default is `"true"` (not `"false"`)
10. `build-source-image` default is `"true"` (not `"false"`)
11. **Operator and operand** files have `hermetic`, `prefetch-input`, and `build-source-image` in `spec.params` (not just `pipelineSpec.params`)

```bash
# Quick validation across all files
for f in .tekton/kueue-*-<VER>-*.yaml; do
    name=$(basename "$f")
    proxy_count=$(grep -c 'enable-package-registry-proxy' "$f" 2>/dev/null)
    target_dirs=$(grep -c 'TARGET_DIRS' "$f" 2>/dev/null)
    cel=$(grep 'on-cel-expression' "$f")
    creation=$(grep -c 'creationTimestamp' "$f")
    hermetic=$(grep -A2 'name: hermetic' "$f" | grep -oP 'default: "\K[^"]+' | head -1)
    source_image=$(grep -A2 'name: build-source-image' "$f" | grep -oP 'default: "\K[^"]+' | head -1)
    echo "$name: proxy=$proxy_count target_dirs=$target_dirs creation=$creation hermetic=$hermetic build-source-image=$source_image"
    echo "  CEL: $cel"
done
```
