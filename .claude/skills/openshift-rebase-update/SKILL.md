---
name: openshift-rebase-update
description: Bumps openshift/api, openshift/library-go, openshift/client-go, and other openshift/* Go module dependencies in kueue-operator to their latest commits, re-vendors, regenerates code/manifests, and runs the full verification suite (build, vet, unit tests, lint). Use whenever asked to "rebase", "bump openshift deps", "update openshift/api", or similar dependency-refresh requests for this repo.
---

# OpenShift Dependency Rebase/Update

Updates this operator's `openshift/*` Go module dependencies to their latest
upstream commits and verifies nothing broke. Mirrors the manual workflow used
for bumping `openshift/api`.

## Which modules this covers

Check `go.mod` for the current set — typically:

- `github.com/openshift/api`
- `github.com/openshift/library-go`
- `github.com/openshift/client-go`
- `github.com/openshift/build-machinery-go`
- `github.com/openshift/jobset-operator`
- `github.com/openshift/lws-operator`

```bash
grep -n 'github.com/openshift/' go.mod
```

**Important:** `go.mod` may contain a `replace` directive pointing an
`openshift/*` module at a personal fork (e.g. a fork used to pick up an
unmerged upstream PR). Check for these before bumping:

```bash
grep -n '^replace' go.mod
```

If a target module is `replace`d by a fork, decide whether to:
- leave the replace as-is (fork still needed, e.g. PR not yet merged upstream), or
- point the replace at a newer fork commit / remove it if the PR has since merged into `openshift/library-go` proper.

Do not silently drop a `replace` directive without confirming the underlying
PR has actually merged upstream.

## Steps

1. **Identify target modules.** Default to updating all `github.com/openshift/*`
   direct dependencies unless the user specifies a subset.

2. **Bump each module to latest:**

   ```bash
   go get github.com/openshift/api@latest
   go get github.com/openshift/library-go@latest
   go get github.com/openshift/client-go@latest
   # ... repeat for other requested openshift/* modules
   ```

   `go get @latest` follows the module's default branch tip (or a `replace`
   target's branch tip if one is set). This will also pull in any required
   transitive bumps (e.g. `k8s.io/api`, `k8s.io/apimachinery`).

3. **Tidy and re-vendor:**

   ```bash
   go mod tidy
   go mod vendor
   ```

4. **Check for accidental submodule drift.** `upstream/kueue/src` is a git
   submodule and can show as modified even when unrelated to this task
   (e.g. from a prior `git submodule update` elsewhere). Restore it unless the
   task explicitly wants it moved:

   ```bash
   git status --short upstream/kueue/src
   git submodule update upstream/kueue/src   # if unexpectedly dirty
   ```

5. **Regenerate manifests/generated code**, since API type changes can affect
   CRDs, deepcopy, clients, listers, informers:

   ```bash
   make generate
   ```

   Review `git status --short` for changes under `manifests/`,
   `pkg/generated/`, and `pkg/apis/.../zz_generated*.go`.

6. **Run the full verification suite:**

   ```bash
   go build ./...
   go vet ./...
   make test-unit
   make lint
   ```

   Fix any compile errors from upstream API changes (renamed fields, removed
   types, changed signatures) before proceeding. Common sources of breakage:
   - `library-go` helper function signature changes
   - `openshift/api` type/field renames or removals (check `zz_generated.deepcopy.go` diffs and callers)
   - `client-go`/`applyconfiguration` generated code needing regeneration (step 5)

7. **Review the diff** before considering the task done:

   ```bash
   git status --short | grep -v '^ M vendor/'   # non-vendor changes worth a close look
   git diff go.mod go.sum
   ```

   Expect `go.mod`/`go.sum` version bumps, vendor tree churn (many files, this
   is normal and doesn't need individual review), and possibly `manifests/`
   or `pkg/generated/` diffs from regeneration. Flag anything unexpected
   (e.g. unrelated submodule pointer changes, unrelated untracked files) to
   the user rather than committing them.

## Notes

- `go get @latest` for pseudo-versioned modules (no tags) resolves to the
  latest commit on the module's default branch as of that moment — safe to
  re-run to pick up newer commits later.
- If `go build`/`make generate` fails with "inconsistent vendoring", it means
  `go.mod` and `vendor/modules.txt` disagree; re-run `go mod vendor`.
- Do not hand-edit files under `vendor/`; always regenerate via `go mod vendor`.
- `make lint` uses golangci-lint v2 (30m timeout) — expect it to download the
  pinned binary into `bin/` on first run.
