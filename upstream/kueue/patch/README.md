# Patches

## e2e.patch

This patch sets up our e2e tests to skip waiting for operators as the namespace name is different and set up our namespaces. The skipped JobSet and LeaderWorkerSet availability checks include the `feature:disaggregatedset` label, because DisaggregatedSet is served by LeaderWorkerSet. It also uses the OpenShift metrics certificate secret name and skips the configuration change for admission fair sharing as the configuration is managed in the Kueue instance.

It also skips the sequential `DisaggregatedSet rollout with waitForPodsReady` test. That test restarts Kueue with a modified configuration (`UpdateKueueConfigurationAndRestart`), which conflicts with the configuration managed by the Kueue instance.

## test_util_e2e.patch

This patch modifies `test/util/behavioral/namespace.go` to add the `kueue.openshift.io/managed` label to namespaces created in e2e tests and `test/util/behavioral/e2e/metrics.go` to increase the metrics timeout from `LongTimeout` to `VeryLongTimeout`.
