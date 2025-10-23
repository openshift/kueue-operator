# Release Process

This document describes the process for creating a new release of the kueue-operator.

## Release Steps

### 1. Pin the Git Submodule to Upstream Release Branch

First, update the gitsubmodule to point to the upstream Kueue release branch:

```bash
cd upstream/kueue/src
git fetch origin
git checkout release-X.Y  # Replace X.Y with the target release version (e.g., release-0.14)
cd ../../..
git add upstream/kueue/src
```

### 2. Sync Manifests

Run the sync_manifests script to update the operator's bindata with the manifests from the pinned release:

```bash
./hack/sync_manifests.py --src-dir upstream/kueue/src/config/components
```

This will:
- Generate manifests using kustomize from the upstream source
- Process and split the manifests into separate files
- Update the bindata/assets/kueue-operator directory

### 3. Commit Changes to Main Branch

Commit the submodule pin and manifest changes to the main branch:

```bash
git add bindata/
git commit -m "Release: Pin upstream kueue to release-X.Y and sync manifests

- Update gitsubmodule to point to upstream release-X.Y
- Sync manifests from upstream release branch
- Update bindata assets

```

### 4. Create Release Branch

Create a new release branch for the operator:

```bash
git checkout -b release-X.Y  # Match the upstream release version
git push -u origin release-X.Y
```

### 5. Return to Main and Repin to Upstream Main

Switch back to the main branch and repin the submodule to upstream main:

```bash
git checkout main
cd upstream/kueue/src
git checkout main
git pull origin main
cd ../../..
git add upstream/kueue/src
git commit -m "Repin upstream kueue submodule to main

- Update gitsubmodule to track upstream main branch
- Prepare for next development cycle

git push origin main
```

## Summary

The complete release flow:

1. **Main branch**: Pin submodule to `release-X.Y` → Sync manifests → Commit
2. **Create branch**: Branch from main to create `release-X.Y` operator branch
3. **Main branch**: Repin submodule back to `main` → Commit

This ensures:
- The release branch contains manifests from the upstream release
- The main branch continues tracking upstream development
- Clean separation between release and development versions

## Verification

After completing the release process, verify:

- [ ] The release branch exists and has the submodule pinned to the correct upstream release
- [ ] The main branch has the submodule repinned to upstream main
- [ ] Manifests in bindata/ on the release branch match the upstream release
- [ ] CI/CD pipelines pass on both branches
