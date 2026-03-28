# Analyze Prow CI Job Failures

Analyze failing Prow CI jobs on a PR by digging into the must-gather artifacts to find the root cause from operator and operand logs.

## Process

1. **Identify the PR:**
   - Use `gh pr list --head <current-branch>` to find the PR for the current branch
   - If no PR is found, ask the user for the PR number

2. **List failing jobs:**
   - Run `gh pr checks <PR_NUMBER> --json name,state,link --jq '.[] | select(.state == "FAILURE")'`
   - Display the failing jobs to the user
   - Pick one failing job to analyze (prefer downstream jobs as they are closer to the shipped product)

3. **Fetch the e2e test build log:**
   - Navigate to the job's GCS artifacts at: `https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/test-platform-results/pr-logs/pull/openshift_kueue-operator/<PR>/<JOB_NAME>/<JOB_ID>/artifacts/<TEST_STEP>/e2e-kueue/build-log.txt`
   - The `<TEST_STEP>` matches the job name (e.g., `test-e2e-downstream-4-21`)
   - Look for the initial failure: BeforeSuite failures, test assertion errors, timeouts, panics

4. **Navigate to the kueue-must-gather artifacts:**
   - The must-gather is at: `artifacts/<TEST_STEP>/kueue-must-gather/artifacts/must-gather/`
   - Inside there is a directory with a long image SHA name — navigate into it
   - Then navigate into the `kueue/` subdirectory

5. **Analyze the operator logs:**
   - Fetch `logs-operator-openshift-kueue-operator-*.log` files (there may be multiple, one per replica)
   - Search for: `error`, `fail`, `panic`, `warning`, `Panic observed`, `unregistered`
   - Pay attention to events after the Kueue CR is created (look for "Creating Kueue instance" or finalizer creation)
   - Note any panics — these are critical and will prevent the operator from completing reconciliation

6. **Check the kueue-controller-manager deployment:**
   - Fetch `deployment-kueue-controller-manager.yaml`
   - If this file is empty (0 bytes), the deployment was never created — the issue is in the operator, not the operand
   - If the deployment exists, check its status conditions (Available, Progressing) and replica counts

7. **Check the Kueue CR status:**
   - Fetch `kueue.yaml` to see the Kueue custom resource
   - Check the `status` section for conditions and error messages
   - Check the `spec` section to understand what integrations/frameworks are configured

8. **Check the operator deployment:**
   - Fetch `deployment-openshift-kueue-operator.yaml`
   - Verify the operator image, environment variables (especially `RELATED_IMAGE_OPERAND_IMAGE`), and status

9. **Cross-reference with code changes:**
   - Use `git log` to identify the commits on the PR branch
   - Look at the changed files to understand what was modified
   - Check if new integrations, frameworks, or API fields were added
   - Verify that all supporting code paths were updated (types, configmap, webhook mappings, tests)

10. **Summarize findings:**
    - Present the root cause clearly
    - Show the relevant log lines or error messages
    - Point to the exact file and line in the codebase where the issue originates
    - Suggest a fix if possible

## Key Artifacts Reference

The GCS base URL pattern is:
```
https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/test-platform-results/pr-logs/pull/openshift_kueue-operator/<PR>/<FULL_JOB_NAME>/<JOB_ID>/
```

### Important files in kueue-must-gather:
| File | Purpose |
|------|---------|
| `logs-operator-openshift-kueue-operator-*.log` | Operator pod logs (check for errors, panics) |
| `deployment-kueue-controller-manager.yaml` | Operand deployment (empty = never created) |
| `deployment-openshift-kueue-operator.yaml` | Operator deployment (image, env vars, status) |
| `kueue.yaml` | Kueue CR spec and status |
| `validatingwebhookconfiguration.yaml` | Webhook config (check for missing webhooks) |
| `mutatingwebhookconfiguration.yaml` | Mutating webhook config |

### Common failure patterns:
| Symptom | Likely Cause |
|---------|-------------|
| `Panic observed: unregistered framework` | New framework added but not registered in `pkg/webhook/pod_webhook.go` `webhooksList` map |
| `kueue pod failed to be ready` (timeout) | Operator failed to create or configure the controller-manager deployment |
| `deployment-kueue-controller-manager.yaml` is empty | Operator panicked or errored before creating the deployment |
| `operator configuration not found` | Kueue CR not yet created (normal during startup, error if persistent) |
| `ImagePullBackOff` or `ErrImagePull` | Wrong image reference in operator deployment or RELATED_IMAGE env var |

## Tips

- Use `WebFetch` for fetching artifact pages and log files from GCS — curl may have issues with very long URLs
- The operator runs 2 replicas — check both log files, but typically only the leader has meaningful reconciliation logs
- The shorter operator log file is usually the non-leader replica (only startup + leader election logs)
- If the e2e build log shows `BeforeSuite` failure with all specs skipped, the issue is in operator/operand startup, not in test logic
