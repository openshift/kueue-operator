# Patches

## e2e.patch

This patch sets up our e2e tests to skip waiting for operators as the namespace name is different and set up our namespaces. It also uses the OpenShift metrics certificate secret name and skips the configuration change for admission fair sharing as the configuration is managed in the Kueue instance.

## test_util_e2e.patch

This patch modifies `test/util/behavioral/namespace.go` to add the `kueue.openshift.io/managed` label to namespaces created in e2e tests and `test/util/behavioral/e2e/metrics.go` to increase the metrics timeout from `LongTimeout` to `VeryLongTimeout`.

