# Test Case: TLS Security Profile

- **Test file:**
  [`test/e2e/e2e_tls_profile_test.go`](../../e2e/e2e_tls_profile_test.go)
- **Feature:** Central TLS Profile consistency
- **JIRA:** [OCPKUEUE-418](https://redhat.atlassian.net/browse/OCPKUEUE-418)
- **Automation status:** Automated
- **Since release:** 1.4
- **Labels:** `tls-profile`

## Description

OpenShift provides a cluster-wide TLS security profile configured via the
APIServer CR (`apiserver.config.openshift.io/cluster`). The Kueue operator must
read this configuration and propagate the appropriate TLS settings (minimum
version and cipher suites) to the `kueue-manager-config` ConfigMap so that the
Kueue controller manager enforces the cluster's TLS policy.

These tests validate that the operator correctly handles all TLS profile types
(Intermediate, Modern, Old, Custom), including negative cases where the profile
is unsupported or contains invalid ciphers.

## Scenarios

1. [Default Intermediate TLS profile is configured](#1-default-intermediate-tls-profile-is-configured)
2. [Modern TLS profile (TLS 1.3) propagates to operand](#2-modern-tls-profile-tls-13-propagates-to-operand)
3. [Old TLS profile (TLS 1.0) sets Degraded condition](#3-old-tls-profile-tls-10-sets-degraded-condition)
4. [Custom TLS profile propagates to operand](#4-custom-tls-profile-propagates-to-operand)
5. [Custom TLS profile with invalid ciphers emits warning](#5-custom-tls-profile-with-invalid-ciphers-emits-warning)

## Prerequisites

- Kueue operator installed and running
- Access to the APIServer CR (`oc get apiserver cluster`)
- Not running on HyperShift (scenarios 2-5 are skipped on HyperShift because
  APIServer TLS profile mutation is not supported)

## Test Scenarios

### 1. Default Intermediate TLS profile is configured

- **Ginkgo:** `When the default Intermediate TLS profile is configured / should
  have Intermediate TLS settings in the initial operand ConfigMap`

**Setup:**

1. No special setup — this validates the default state after operator install

**Execution:**

1. Verify the `kueue-manager-config` ConfigMap contains Intermediate TLS
   settings:
   ```bash
   oc get configmap kueue-manager-config -n openshift-kueue-operator -o jsonpath='{.data.controller_manager_config\.yaml}'
   # Look for the tls section in the output
   ```
2. Check the `minVersion` value:
   ```bash
   oc get configmap kueue-manager-config -n openshift-kueue-operator -o jsonpath='{.data.controller_manager_config\.yaml}' | grep minVersion
   # Expected: VersionTLS12
   ```
3. Check that cipher suites are present:
   ```bash
   oc get configmap kueue-manager-config -n openshift-kueue-operator -o jsonpath='{.data.controller_manager_config\.yaml}' | grep -A 20 cipherSuites
   # Expected: list of Intermediate-profile cipher suites (non-empty)
   ```

**Expected Result:**

- ConfigMap contains `minVersion: VersionTLS12`
- ConfigMap contains the Intermediate profile cipher suites (non-empty list)

**Cleanup:**

None required.

---

### 2. Modern TLS profile (TLS 1.3) propagates to operand

- **Ginkgo:** `When the cluster TLS profile is set to Modern (TLS 1.3) / should
  propagate TLS 1.3 settings to the operand ConfigMap`
- **Skipped on:** HyperShift clusters

**Setup:**

1. Save the current APIServer TLS profile (to restore later):
   ```bash
   oc get apiserver cluster -o jsonpath='{.spec.tlsSecurityProfile}' > tls-backup.json
   ```

**Execution:**

1. Set the APIServer TLS profile to Modern:
   ```bash
   oc patch apiserver cluster --type=merge -p '{"spec":{"tlsSecurityProfile":{"type":"Modern","modern":{}}}}'
   ```
2. Wait for the operand ConfigMap to reflect TLS 1.3 settings (this may take a
   few minutes as the APIServer rolls out):
   ```bash
   oc get configmap kueue-manager-config -n openshift-kueue-operator -o jsonpath='{.data.controller_manager_config\.yaml}' | grep minVersion
   # Expected: VersionTLS13
   ```
3. Verify cipher suites are empty (TLS 1.3 ciphers are not configurable in Go):
   ```bash
   oc get configmap kueue-manager-config -n openshift-kueue-operator -o jsonpath='{.data.controller_manager_config\.yaml}' | grep -A 5 cipherSuites
   # Expected: empty list or no cipherSuites key
   ```
4. Verify the operand deployment is fully rolled out:
   ```bash
   oc get deployment kueue-controller-manager -n openshift-kueue-operator -o jsonpath='{.status.readyReplicas}/{.status.replicas}'
   # Expected: replicas match (e.g., 1/1)
   ```

**Expected Result:**

- ConfigMap contains `minVersion: VersionTLS13`
- ConfigMap contains empty cipher suites
- Operand deployment is fully rolled out with all replicas ready

**Cleanup:**

```bash
oc patch apiserver cluster --type=merge -p '{"spec":{"tlsSecurityProfile":null}}'
# Wait for ConfigMap to reconcile back to Intermediate settings
```

---

### 3. Old TLS profile (TLS 1.0) sets Degraded condition

- **Ginkgo:** `When the cluster TLS profile is set to Old (TLS 1.0) / should
  set Degraded condition instead of crashing the controller`
- **Skipped on:** HyperShift clusters
- **Type:** Negative test

**Setup:**

1. Save the current APIServer TLS profile (to restore later)

**Execution:**

1. Set the APIServer TLS profile to Old:
   ```bash
   oc patch apiserver cluster --type=merge -p '{"spec":{"tlsSecurityProfile":{"type":"Old","old":{}}}}'
   ```
2. Wait for the operator to report Degraded condition (may take up to 10
   minutes because changing the APIServer TLS profile triggers a
   kube-apiserver rollout):
   ```bash
   oc get kueue cluster -o jsonpath='{range .status.conditions[*]}{.type}={.status} reason={.reason}{"\n"}{end}'
   # Expected: Degraded=True reason=UnsupportedTLSProfile
   ```
3. Verify the operator also reports Available=False:
   ```bash
   oc get kueue cluster -o jsonpath='{.status.conditions[?(@.type=="Available")].status}'
   # Expected: False
   oc get kueue cluster -o jsonpath='{.status.conditions[?(@.type=="Available")].reason}'
   # Expected: UnsupportedTLSProfile
   ```
4. Restore the TLS profile to Intermediate to recover:
   ```bash
   oc patch apiserver cluster --type=merge -p '{"spec":{"tlsSecurityProfile":null}}'
   ```
5. Verify the operator recovers and becomes Available:
   ```bash
   oc get kueue cluster -o jsonpath='{.status.conditions[?(@.type=="Available")].status}'
   # Expected: True
   ```

**Expected Result:**

- Operator sets `Degraded=True` with reason `UnsupportedTLSProfile` (does NOT
  crash)
- Operator sets `Available=False` with reason `UnsupportedTLSProfile`
- After restoring to Intermediate, operator recovers to `Available=True`

**Cleanup:**

```bash
oc patch apiserver cluster --type=merge -p '{"spec":{"tlsSecurityProfile":null}}'
# Wait for operator to recover to Available=True
```

---

### 4. Custom TLS profile propagates to operand

- **Ginkgo:** `When the cluster TLS profile is set to Custom / should propagate
  custom TLS settings to the operand ConfigMap`
- **Skipped on:** HyperShift clusters

**Setup:**

1. Save the current APIServer TLS profile (to restore later)

**Execution:**

1. Set the APIServer TLS profile to Custom with specific ciphers:
   ```bash
   oc patch apiserver cluster --type=merge -p '{
     "spec": {
       "tlsSecurityProfile": {
         "type": "Custom",
         "custom": {
           "ciphers": [
             "ECDHE-ECDSA-AES128-GCM-SHA256",
             "ECDHE-RSA-AES128-GCM-SHA256"
           ],
           "minTLSVersion": "VersionTLS12"
         }
       }
     }
   }'
   ```
2. Wait for the operand ConfigMap to reflect custom TLS settings:
   ```bash
   oc get configmap kueue-manager-config -n openshift-kueue-operator -o jsonpath='{.data.controller_manager_config\.yaml}' | grep minVersion
   # Expected: VersionTLS12
   ```
3. Verify exactly 2 cipher suites are present (matching the 2 OpenSSL ciphers
   mapped to IANA names):
   ```bash
   oc get configmap kueue-manager-config -n openshift-kueue-operator -o jsonpath='{.data.controller_manager_config\.yaml}' | grep -A 5 cipherSuites
   # Expected: 2 cipher suites
   ```

**Expected Result:**

- ConfigMap contains `minVersion: VersionTLS12`
- ConfigMap contains exactly 2 cipher suites (the IANA equivalents of the
  specified OpenSSL ciphers)

**Cleanup:**

```bash
oc patch apiserver cluster --type=merge -p '{"spec":{"tlsSecurityProfile":null}}'
```

---

### 5. Custom TLS profile with invalid ciphers emits warning

- **Ginkgo:** `When the cluster TLS profile is set to Custom with invalid cipher
  suites / should emit an InvalidTLSCipherSuites warning event for unmapped
  ciphers`
- **Skipped on:** HyperShift clusters
- **Type:** Negative test

**Setup:**

1. Save the current APIServer TLS profile (to restore later)

**Execution:**

1. Set the APIServer TLS profile to Custom with a mix of valid and invalid
   ciphers:
   ```bash
   oc patch apiserver cluster --type=merge -p '{
     "spec": {
       "tlsSecurityProfile": {
         "type": "Custom",
         "custom": {
           "ciphers": [
             "ECDHE-RSA-AES128-GCM-SHA256",
             "TLS_ALICE_POLY1305_SHA256",
             "INVALID-CIPHER"
           ],
           "minTLSVersion": "VersionTLS12"
         }
       }
     }
   }'
   ```
2. Wait for the operand ConfigMap to contain only the valid cipher:
   ```bash
   oc get configmap kueue-manager-config -n openshift-kueue-operator -o jsonpath='{.data.controller_manager_config\.yaml}' | grep -A 5 cipherSuites
   # Expected: only 1 cipher suite (the valid one)
   ```
3. Verify that an `InvalidTLSCipherSuites` warning event was emitted:
   ```bash
   oc get events -n openshift-kueue-operator --field-selector reason=InvalidTLSCipherSuites
   # Expected: at least one Warning event with reason=InvalidTLSCipherSuites
   ```

**Expected Result:**

- ConfigMap contains only 1 cipher suite (the valid one:
  `ECDHE-RSA-AES128-GCM-SHA256`)
- Invalid ciphers (`TLS_ALICE_POLY1305_SHA256`, `INVALID-CIPHER`) are silently
  dropped from the ConfigMap
- A `Warning` event with reason `InvalidTLSCipherSuites` is emitted in the
  operator namespace

**Cleanup:**

```bash
oc patch apiserver cluster --type=merge -p '{"spec":{"tlsSecurityProfile":null}}'
```
