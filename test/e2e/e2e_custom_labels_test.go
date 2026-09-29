/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ssv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Custom Labels", Label("custom-labels"), Ordered, func() {
	var (
		initialKueueInstance *ssv1.Kueue
		curlPod              *corev1.Pod
	)

	BeforeAll(func(ctx context.Context) {
		By("Saving initial Kueue configuration")
		kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
		initialKueueInstance = kueueInstance.DeepCopy()

		By("Applying Kueue configuration with custom metric labels")
		newConfig := initialKueueInstance.Spec.Config
		newConfig.ControllerManager = &ssv1.ControllerManager{
			Metrics: &ssv1.ControllerMetrics{
				CustomLabels: []ssv1.ControllerMetricsCustomLabel{
					{
						Name:           "tenant_id",
						SourceKind:     ssv1.SourceKindClusterQueue,
						SourceLabelKey: "foo.io/tenant",
					},
					{
						Name:                "cost_center",
						SourceKind:          ssv1.SourceKindClusterQueue,
						SourceAnnotationKey: "foo.com/cost-center",
					},
					{
						Name:           "tenant_id2",
						SourceKind:     ssv1.SourceKindLocalQueue,
						SourceLabelKey: "foo.io/tenant2",
					},
					{
						Name:           "org_unit",
						SourceKind:     ssv1.SourceKindCohort,
						SourceLabelKey: "foo.io/org-unit",
					},
				},
			},
		}
		applyKueueConfig(ctx, newConfig, kubeClient)

		By("Waiting for Kueue configuration to be applied")
		Eventually(func(g Gomega) {
			ki, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(ki.Spec.Config.ControllerManager).ToNot(BeNil())
			g.Expect(ki.Spec.Config.ControllerManager.Metrics).ToNot(BeNil())
			g.Expect(ki.Spec.Config.ControllerManager.Metrics.CustomLabels).To(HaveLen(4))
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(),
			"Kueue configuration with custom labels should be applied")

		By("Creating curl pod to scrape metrics")
		curlPodWrapper := testutils.MakeCurlMetricsPod(testutils.OperatorNamespace)
		var cleanupCurlPod func()
		cleanupCurlPod, err = testutils.CreatePod(kubeClient, curlPodWrapper.Obj())
		Expect(err).NotTo(HaveOccurred(), "failed to create curl metrics pod")
		DeferCleanup(cleanupCurlPod)
		curlPod = curlPodWrapper.Obj()

		By("Waiting for curl pod to be running")
		Eventually(func() error {
			pod, err := kubeClient.CoreV1().Pods(testutils.OperatorNamespace).Get(ctx, curlPod.Name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get curl pod: %w", err)
			}
			if pod.Status.Phase != corev1.PodRunning {
				return fmt.Errorf("curl pod not running yet, phase: %s", pod.Status.Phase)
			}
			return nil
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "curl metrics pod should be running")
	})

	AfterAll(func(ctx context.Context) {
		By("Restoring initial Kueue configuration")
		applyKueueConfig(ctx, initialKueueInstance.Spec.Config, kubeClient)
	})

	When("Custom metric labels are configured with source label and annotation", func() {
		It("should expose custom metric labels on ClusterQueue, LocalQueue and Cohort metrics", func(ctx context.Context) {
			By("Creating a Cohort with foo.io/org-unit label")
			cohortBuilder := testutils.NewCohort("").
				WithGenerateName().WithLabel("foo.io/org-unit", "platform")
			cohort, cleanupCohort, err := cohortBuilder.CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCohort)

			By("Creating ResourceFlavor")
			rf, cleanupRF, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupRF)

			By("Creating ClusterQueue with foo.io/tenant label and foo.com/cost-center annotation, linked to the Cohort")
			cq, cleanupCQ, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithFlavorName(rf.Name).
				WithCPU("500m").
				WithMemory("512Mi").
				WithCohort(cohort.Name).
				WithLabel("foo.io/tenant", "tenant-1").
				WithAnnotation("foo.com/cost-center", "cost-center-1").
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			By("Creating a namespace and LocalQueue with foo.io/tenant2 label")
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "custom-labels-",
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lqWrapper := testutils.NewLocalQueue(ns.Name, "custom-labels-lq").WithClusterQueue(cq.Name).
				WithLabel("foo.io/tenant2", "tenant-2")
			_, cleanupLQ, err := lqWrapper.CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Submitting a job to generate resource-usage metrics")
			builder := testutils.NewTestResourceBuilder(ns.Name, "custom-labels-lq")
			job := builder.NewJob()
			job.Labels[testutils.QueueLabel] = "custom-labels-lq"
			job, err = kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, job.Namespace, job.Name)
			verifyWorkloadCreated(clients.UpstreamKueueClient, ns.Name, string(job.UID))

			curlCmd := fmt.Sprintf(
				"curl -s --cacert /etc/kueue/metrics/certs/ca.crt "+
					"-H \"Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)\" "+
					"https://kueue-controller-manager-metrics-service.%s.svc.cluster.local:8443/metrics",
				testutils.OperatorNamespace,
			)

			By("Verifying custom_tenant_id and custom_cost_center appear in kueue_cluster_queue_nominal_quota")
			Eventually(func() error {
				metricsOutput, _, err := Kexecute(ctx, clients.RestConfig, kubeClient,
					testutils.OperatorNamespace, curlPod.Name, "curl-metrics",
					[]string{"/bin/sh", "-c", curlCmd})
				if err != nil {
					return fmt.Errorf("curl failed: %w", err)
				}

				parser := expfmt.NewTextParser(model.UTF8Validation)
				metricFamilies, err := parser.TextToMetricFamilies(strings.NewReader(string(metricsOutput)))
				if err != nil {
					return fmt.Errorf("failed to parse Prometheus metrics: %w", err)
				}

				expectedMetrics := []struct {
					name   string
					labels map[string]string
				}{
					{
						name: "kueue_cluster_queue_nominal_quota",
						labels: map[string]string{
							"cluster_queue":      cq.Name,
							"cohort":             cohort.Name,
							"custom_tenant_id":   "tenant-1",
							"custom_cost_center": "cost-center-1",
						},
					},
					{
						name: "kueue_local_queue_resource_usage",
						labels: map[string]string{
							"custom_tenant_id2": "tenant-2",
						},
					},
					{
						name: "kueue_cluster_queue_resource_usage",
						labels: map[string]string{
							"cluster_queue":      cq.Name,
							"cohort":             cohort.Name,
							"custom_cost_center": "cost-center-1",
							"custom_tenant_id":   "tenant-1",
						},
					},
					{
						name: "kueue_cohort_subtree_quota",
						labels: map[string]string{
							"custom_org_unit": "platform",
						},
					},
				}

				for _, expected := range expectedMetrics {
					if err := findMetricWithLabels(metricFamilies, expected.name, expected.labels); err != nil {
						return err
					}
				}
				return nil
			}, testutils.MetricsReadyTime, testutils.MetricsPoll).Should(Succeed(),
				"metrics should contain custom labels")
		})
	})
})
