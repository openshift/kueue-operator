---
name: bump-kueue-operator-version
description: Bumps the kueue-operator release version and supported OpenShift versions across bundle metadata, labels, and README documentation, then regenerates the bundle. Use when preparing a new kueue-operator release or changing its supported OCP versions.
user-invocable: true
---

# Bump kueue-operator version

Bumps the kueue-operator version in all the necessary files.

## Process

1. **Show to the user the current kueue-operator version defined in the files in the bundle folder**

2. **Ask the user the value of the new kueue-operator version using semver format and give examples**

3. **Ask the user the values of the OpenShift versions that the new kueue-operator version should support**

4. **Update any files that mentions the current kueue-operator version with the new kueue-operator version**

5. **Update any files that mentions the OpenShift versions with the new OpenShift version to be supported**

6. **Add the new kueue-operator and the OpenShift version to be supported in the README.md**

7. **Update the README.md with the go version define in the go.mod file**

8. **Update the kueue version in the README.md  with the branch defined in the .gitmodules file**

9. **Update the cpe LABEL with the new kueue-operator version**

10. **Run the command make bundle-generate**

