/*
Copyright 2025.

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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ssv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
)

var _ = Describe("Gangscheduling", Label("gangscheduling"), Ordered, func() {
	var (
		initialKueueInstance *ssv1.Kueue
		gangLocalQueueName   = "local-queue"
	)

	When("Policy is ByWorkload and Admission is Sequential", func() {
		BeforeAll(func(ctx context.Context) {
			By("Saving initial Kueue configuration")
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
			initialKueueInstance = kueueInstance.DeepCopy()

			By("Configuring Kueue with gangScheduling: policy=ByWorkload, admission=Sequential")
			byWorkload := ssv1.ByWorkload{Admission: ssv1.GangSchedulingWorkloadAdmissionSequential}
			applyKueueConfig(ctx, makeGangSchedulingConfig(initialKueueInstance.Spec.Config.Integrations, byWorkload), kubeClient)
		})

		AfterAll(func(ctx context.Context) {
			By("Restoring initial Kueue configuration")
			applyKueueConfig(ctx, initialKueueInstance.Spec.Config, kubeClient)
		})

		It("should apply all-or-nothing admission for workloads", func(ctx context.Context) {
			_, namespace := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
				"gangscheduling-", gangLocalQueueName,
				func(cq *testutils.ClusterQueueWrapper) {
					cq.WithCPU("500m").WithMemory("512Mi")
				})

			By("Admitting a Job that consumes partial quota")
			job1, err := createJobGang(ctx, "job-1", namespace.Name, gangLocalQueueName, "250m", "128Mi", 1)
			Expect(err).NotTo(HaveOccurred(), "Failed to create first job")
			defer testutils.CleanUpJob(ctx, kubeClient, job1.Namespace, job1.Name)

			By("Verifying first job workload is created and admitted")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespace.Name, string(job1.UID))

			By("Creating a gang job (parallelism=2) that exceeds remaining quota")
			job2, err := createJobGang(ctx, "job-gang", namespace.Name, gangLocalQueueName, "150m", "128Mi", 2)
			Expect(err).NotTo(HaveOccurred(), "Failed to create gang job")
			defer testutils.CleanUpJob(ctx, kubeClient, job2.Namespace, job2.Name)

			By("Verifying the gang job workload is created but NOT admitted (not enough quota for all pods)")
			verifyWorkloadCreatedNotAdmitted(clients.UpstreamKueueClient, namespace.Name, job2.UID)

			// Verify workload stays NOT admitted while first job is running
			Consistently(func() bool {
				workloads, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace.Name).List(ctx, metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred(), "Failed to list workloads")
				for _, wl := range workloads.Items {
					for _, ownerRef := range wl.OwnerReferences {
						if ownerRef.UID == job2.UID {
							return !apimeta.IsStatusConditionTrue(wl.Status.Conditions, kueuev1beta2.WorkloadAdmitted)
						}
					}
				}
				return true
			}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(BeTrue(), "Gang job should stay NOT admitted while first job consumes quota")

			By("Waiting for first job to complete and free up quota")
			Eventually(func() error {
				j, err := kubeClient.BatchV1().Jobs(namespace.Name).Get(ctx, job1.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				if j.Status.Succeeded >= 1 || j.Status.CompletionTime != nil {
					return nil
				}
				return fmt.Errorf("job %s not completed yet, succeeded: %d", job1.Name, j.Status.Succeeded)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "First job should complete")

			By("Verifying the second gang job is now admitted after quota is freed (all pods admitted together)")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespace.Name, string(job2.UID))

		})

		It("should admit workloads sequentially even when quota is available", func(ctx context.Context) {
			_, namespace := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
				"sequential-", gangLocalQueueName,
				func(cq *testutils.ClusterQueueWrapper) {
					cq.WithCPU("500m").WithMemory("512Mi")
				})

			By("Creating the first gang job (parallelism=2) with delayed pod readiness")
			createdJob1, err := createJobGang(ctx, "job-sequential-1", namespace.Name, gangLocalQueueName, "100m", "100Mi", 2)
			Expect(err).NotTo(HaveOccurred(), "Failed to create first gang job")
			defer testutils.CleanUpJob(ctx, kubeClient, createdJob1.Namespace, createdJob1.Name)

			By("Verifying first gang job workload is admitted")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespace.Name, string(createdJob1.UID))

			By("Creating the second gang job (parallelism=2) while first gang job pods are not yet ready")
			createdJob2, err := createJobGang(ctx, "job-sequential-2", namespace.Name, gangLocalQueueName, "100m", "100Mi", 2)
			Expect(err).NotTo(HaveOccurred(), "Failed to create second gang job")
			defer testutils.CleanUpJob(ctx, kubeClient, createdJob2.Namespace, createdJob2.Name)

			By("Verifying second gang job is NOT admitted because sequential admission waits for first gang job pods to be ready")
			verifyWorkloadCreatedNotAdmitted(clients.UpstreamKueueClient, namespace.Name, createdJob2.UID)

			// Consistently verify workload stays NOT admitted while first gang job pods are not ready
			Consistently(func() bool {
				workloads, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace.Name).List(ctx, metav1.ListOptions{})
				if err != nil {
					return true
				}
				for _, wl := range workloads.Items {
					for _, ownerRef := range wl.OwnerReferences {
						if ownerRef.UID == createdJob2.UID {
							return !apimeta.IsStatusConditionTrue(wl.Status.Conditions, kueuev1beta2.WorkloadAdmitted)
						}
					}
				}
				return true
			}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(BeTrue(), "Second gang job should stay NOT admitted during sequential admission while first job pods are not ready")

			By("Waiting for first gang job pods to complete")
			Eventually(func() bool {
				job, err := kubeClient.BatchV1().Jobs(namespace.Name).Get(ctx, createdJob1.Name, metav1.GetOptions{})
				if err != nil {
					return false
				}
				return job.Status.Succeeded >= 2
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "First gang job pods should complete")

			By("Verifying second gang job is now admitted after first gang job pods became ready")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespace.Name, string(createdJob2.UID))
		})
	})

	When("Policy is ByWorkload and Admission is Parallel", func() {
		BeforeAll(func(ctx context.Context) {
			By("Saving initial Kueue configuration")
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
			initialKueueInstance = kueueInstance.DeepCopy()

			By("Configuring Kueue with gangScheduling: policy=ByWorkload, admission=Parallel")
			byWorkload := ssv1.ByWorkload{Admission: ssv1.GangSchedulingWorkloadAdmissionParallel}
			applyKueueConfig(ctx, makeGangSchedulingConfig(initialKueueInstance.Spec.Config.Integrations, byWorkload), kubeClient)
		})

		AfterAll(func(ctx context.Context) {
			By("Restoring initial Kueue configuration")
			applyKueueConfig(ctx, initialKueueInstance.Spec.Config, kubeClient)
		})

		It("should admit workloads in parallel without waiting for pods to be ready", func(ctx context.Context) {
			_, namespace := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
				"parallel-", gangLocalQueueName,
				func(cq *testutils.ClusterQueueWrapper) {
					cq.WithCPU("500m").WithMemory("512Mi")
				})

			By("Creating the first gang job (parallelism=2) with delayed pod readiness")
			createdJob1, err := createJobGang(ctx, "job-parallel-1", namespace.Name, gangLocalQueueName, "100m", "100Mi", 2)
			Expect(err).NotTo(HaveOccurred(), "Failed to create first gang job")
			defer testutils.CleanUpJob(ctx, kubeClient, createdJob1.Namespace, createdJob1.Name)

			By("Verifying first gang job workload is admitted")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespace.Name, string(createdJob1.UID))

			By("Creating the second gang job (parallelism=2) while first gang job pods are not yet ready")
			createdJob2, err := createJobGang(ctx, "job-parallel-2", namespace.Name, gangLocalQueueName, "100m", "100Mi", 2)
			Expect(err).NotTo(HaveOccurred(), "Failed to create second gang job")
			defer testutils.CleanUpJob(ctx, kubeClient, createdJob2.Namespace, createdJob2.Name)

			By("Verifying second gang job is admitted immediately despite first gang job pods not being ready (parallel admission)")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespace.Name, string(createdJob2.UID))

			By("Verifying both gang jobs are admitted together without waiting for pod readiness")
			workloads, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace.Name).List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())

			admittedCount := 0
			for _, wl := range workloads.Items {
				if apimeta.IsStatusConditionTrue(wl.Status.Conditions, kueuev1beta2.WorkloadAdmitted) {
					admittedCount++
				}
			}
			Expect(admittedCount).To(Equal(2), "Both gang jobs should be admitted together with parallel admission")
		})
	})

	When("Policy is ByWorkload with timeout and retry limit", func() {
		var (
			cq        *kueuev1beta2.ClusterQueue
			namespace *corev1.Namespace
		)
		BeforeAll(func(ctx context.Context) {
			By("Saving initial Kueue configuration")
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
			initialKueueInstance = kueueInstance.DeepCopy()

			By("Configuring Kueue with gangScheduling: policy=ByWorkload, timeoutSeconds=30, retryLimit=1")
			byWorkload := ssv1.ByWorkload{
				TimeoutSeconds: 30,
				RequeuingStrategy: ssv1.RequeuingStrategy{
					RetryLimit:         1,
					BackoffBaseSeconds: 30,
					BackoffMaxSeconds:  30,
				},
			}
			applyKueueConfig(ctx, makeGangSchedulingConfig(initialKueueInstance.Spec.Config.Integrations, byWorkload), kubeClient)

			By("Creating curl pod to scrape metrics")
			curlPod := testutils.MakeCurlMetricsPod(testutils.OperatorNamespace)
			podCleanupFn, err := testutils.CreatePod(kubeClient, curlPod.Obj())
			Expect(err).NotTo(HaveOccurred(), "failed to create curl metrics pod")
			DeferCleanup(podCleanupFn)

			Eventually(func() error {
				pod, err := kubeClient.CoreV1().Pods(testutils.OperatorNamespace).Get(ctx, "curl-metrics-test", metav1.GetOptions{})
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

		BeforeEach(func(ctx context.Context) {
			cq, namespace = testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
				"timeout-eviction-", gangLocalQueueName,
				func(cq *testutils.ClusterQueueWrapper) {
					cq.WithCPU("500m").WithMemory("1Gi")
				})
		})

		It("should evict failed job workload and make workload ready and stay admitted", func(ctx context.Context) {
			// Pods have a readiness probe that checks for /tmp/ready.
			// First attempt: file absent → probe fails → pods never Ready → PodsReadyTimeout → eviction.
			// Second attempt: we exec `touch /tmp/ready` into the new pods → probe passes → job completes.
			By("Creating gang job with a readiness probe gated on /tmp/ready")
			job, err := createReadinessProbeGangJob(ctx, "job-gang-timeout", namespace.Name,
				gangLocalQueueName, "100m", "128Mi", 2)
			Expect(err).NotTo(HaveOccurred(), "Failed to create gang job")
			defer testutils.CleanUpJob(ctx, kubeClient, job.Namespace, job.Name)

			By("Verifying gang job workload is admitted (first attempt, /tmp/ready absent → pods not Ready)")
			evictedWorkloadName := verifyWorkloadCreated(clients.UpstreamKueueClient, namespace.Name, string(job.UID))

			By("Waiting for workload to be evicted with PodsReadyTimeout reason (timeoutSeconds=30)")
			waitForWorkloadEvictedByPodsReadyTimeout(ctx, namespace.Name, evictedWorkloadName)

			By("Verifying kueue_evicted_workloads_once_total metric is present with PodsReadyTimeout reason")
			findGangSchedulingMetric(ctx, "kueue_evicted_workloads_once_total", map[string]string{
				"cluster_queue":    cq.Name,
				"priority_class":   "",
				"reason":           "PodsReadyTimeout",
				"replica_role":     "leader",
				"underlying_cause": "WaitForStart",
			})

			By("Verifying ClusterQueue CPU reservation returns to 0 after eviction")
			Eventually(func(g Gomega) {
				cqObj, err := clients.UpstreamKueueClient.KueueV1beta2().ClusterQueues().Get(ctx, cq.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				for _, flavor := range cqObj.Status.FlavorsReservation {
					for _, res := range flavor.Resources {
						if res.Name == corev1.ResourceCPU {
							g.Expect(res.Total.IsZero()).To(BeTrue(),
								"ClusterQueue CPU reservation should be 0 after eviction, got %s", res.Total.String())
						}
					}
				}
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(),
				"ClusterQueue usage should return to 0 after eviction")

			By("Waiting for workload to be re-admitted on requeue (after backoff)")
			checkWorkloadCondition(ctx, namespace.Name, string(job.UID), kueuev1beta2.WorkloadAdmitted, "admitted on requeue backoff")

			By("Verifying spec.active remains true and requeueState.count reached retryLimit")
			wl, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace.Name).Get(ctx, evictedWorkloadName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ptr.Deref(wl.Spec.Active, true)).To(BeTrue(),
				"spec.active should remain true (workload should not be deactivated)")
			Expect(wl.Status.RequeueState).ToNot(BeNil(),
				"requeueState should be populated after eviction and requeue")
			Expect(ptr.Deref(wl.Status.RequeueState.Count, 0)).To(Equal(int32(1)),
				"requeueState.count should equal retryLimit=1 after the first requeue")

			// After re-admission, Kueue un-gates the new pods. We exec `touch /tmp/ready` into each
			// running pod. The readiness probe passes, the `until` loop in the main container exits 0,
			// and Kueue sees all pods as Ready/Succeeded before the second 30 s timeout fires.
			By("Waiting for new job pods to be running after re-admission, then signalling readiness via exec")
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(namespace.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("batch.kubernetes.io/job-name=%s", job.Name),
				})
				if err != nil {
					return fmt.Errorf("listing pods: %w", err)
				}
				var running []corev1.Pod
				for _, p := range pods.Items {
					if p.Status.Phase == corev1.PodRunning && p.DeletionTimestamp == nil {
						running = append(running, p)
					}
				}
				if len(running) < 2 {
					return fmt.Errorf("only %d/2 new pods running", len(running))
				}
				for _, p := range running {
					if _, _, execErr := Kexecute(ctx, clients.RestConfig, kubeClient,
						namespace.Name, p.Name, "test-container",
						[]string{"touch", "/tmp/ready"}); execErr != nil {
						return fmt.Errorf("touch /tmp/ready failed in pod %s: %w", p.Name, execErr)
					}
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.DeletionPoll).Should(Succeed(),
				"should touch /tmp/ready in all running pods before second timeout fires")

			By("Waiting for the job to complete successfully (readiness probe unblocked by exec)")
			checkWorkloadCondition(ctx, namespace.Name, string(job.UID), kueuev1beta2.WorkloadFinished, "gang-timeout")
		})

		It("should deactivate workload after requeues exhaust the retry limit", func(ctx context.Context) {
			// Create a never-ready job — readiness probe checks /tmp/ready, which we never create.
			// The workload will be evicted at every admission until retryLimit is exhausted.
			By("Creating gang job that will never become ready")
			job, err := createReadinessProbeGangJob(ctx, "job-retry-exhaust", namespace.Name,
				gangLocalQueueName, "100m", "128Mi", 2)
			Expect(err).NotTo(HaveOccurred(), "Failed to create never-ready gang job")
			defer testutils.CleanUpJob(ctx, kubeClient, job.Namespace, job.Name)

			By("Verifying workload is created and admitted (first attempt)")
			wlName := verifyWorkloadCreated(clients.UpstreamKueueClient, namespace.Name, string(job.UID))

			By("Waiting for workload to be deactivated after exhausting retryLimit")
			Eventually(func(g Gomega) {
				wl, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace.Name).Get(ctx, wlName, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())

				// Terminal condition: spec.active=false
				g.Expect(ptr.Deref(wl.Spec.Active, true)).To(BeFalse(),
					"spec.active should be false after retryLimit exhausted")

				// Verify workload is not admitted after deactivation.
				g.Expect(apimeta.IsStatusConditionTrue(wl.Status.Conditions, kueuev1beta2.WorkloadAdmitted)).To(BeFalse(),
					"WorkloadAdmitted should be False after deactivation")

				// Verify the Evicted condition reason indicates deactivation due to requeuing limit.
				cond := apimeta.FindStatusCondition(wl.Status.Conditions, kueuev1beta2.WorkloadEvicted)
				g.Expect(cond).NotTo(BeNil(), "WorkloadEvicted condition should exist")
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue), "WorkloadEvicted should be True")
				g.Expect(cond.Reason).To(ContainSubstring(kueuev1beta2.WorkloadDeactivated),
					"Evicted condition reason should indicate Deactivated")
				g.Expect(cond.Reason).To(ContainSubstring(kueuev1beta2.WorkloadRequeuingLimitExceeded),
					"Evicted condition reason should indicate RequeuingLimitExceeded")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(),
				"workload should be deactivated after exhausting retryLimit (expected ~90s)")

			By("Verifying LocalQueue eviction metric reports deactivation due to requeuing limit")
			findGangSchedulingMetric(ctx, "kueue_local_queue_evicted_workloads_total", map[string]string{
				"name":             gangLocalQueueName,
				"namespace":        namespace.Name,
				"priority_class":   "",
				"reason":           "Deactivated",
				"replica_role":     "leader",
				"underlying_cause": "RequeuingLimitExceeded",
			})

			By("Verifying ClusterQueue CPU reservation returns to 0 after deactivation")
			Eventually(func(g Gomega) {
				cqObj, err := clients.UpstreamKueueClient.KueueV1beta2().ClusterQueues().Get(ctx, cq.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				for _, flavor := range cqObj.Status.FlavorsReservation {
					for _, res := range flavor.Resources {
						if res.Name == corev1.ResourceCPU {
							g.Expect(res.Total.IsZero()).To(BeTrue(),
								"ClusterQueue CPU reservation should be 0 after deactivation, got %s", res.Total.String())
						}
					}
				}
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(),
				"ClusterQueue usage should return to 0 after workload deactivation")
		})
		It("should evict statefulset workload when pods do not become ready within PodsReadyTimeout", func(ctx context.Context) {
			createdStatefulSet, err := createReadinessProbeStatefulSet(ctx, "statefulset-gang-timeout",
				namespace.Name, gangLocalQueueName, 2)
			Expect(err).NotTo(HaveOccurred(), "Failed to create statefulset")
			DeferCleanup(func() {
				_ = genericClient.Delete(ctx, createdStatefulSet)
			})

			By("Verifying the statefulset workload is admitted (/tmp/ready absent → pods not Ready)")
			workloadName := verifyWorkloadCreated(clients.UpstreamKueueClient, namespace.Name, string(createdStatefulSet.UID))

			By("Waiting for both pods to run, then signalling readiness on only one replica (the other stays unready)")
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(namespace.Name).List(ctx, metav1.ListOptions{
					LabelSelector: "app=test-statefulset",
				})
				if err != nil {
					return fmt.Errorf("listing pods: %w", err)
				}
				var running []corev1.Pod
				for _, p := range pods.Items {
					if p.Status.Phase == corev1.PodRunning && p.DeletionTimestamp == nil {
						running = append(running, p)
					}
				}
				if len(running) < 2 {
					return fmt.Errorf("only %d/2 pods running", len(running))
				}
				if _, _, execErr := Kexecute(ctx, clients.RestConfig, kubeClient,
					namespace.Name, running[0].Name, "test-container",
					[]string{"touch", "/tmp/ready"}); execErr != nil {
					return fmt.Errorf("touch /tmp/ready failed in pod %s: %w", running[0].Name, execErr)
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.DeletionPoll).Should(Succeed(),
				"should mark one of the two running pods ready")

			By("Verifying the workload stays PodsReady=False while one replica remains unready")
			Consistently(func() bool {
				wl, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace.Name).Get(ctx, workloadName, metav1.GetOptions{})
				if err != nil {
					return false
				}
				cond := apimeta.FindStatusCondition(wl.Status.Conditions, kueuev1beta2.WorkloadPodsReady)
				return cond != nil && cond.Status == metav1.ConditionFalse
			}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(BeTrue(),
				"workload should consistently remain PodsReady=False while one StatefulSet pod is unready")

			By("Waiting for workload to be evicted with PodsReadyTimeout reason (timeoutSeconds=30)")
			waitForWorkloadEvictedByPodsReadyTimeout(ctx, namespace.Name, workloadName)

			By("Verifying kueue_evicted_workloads_once_total metric is present with PodsReadyTimeout reason")
			findEvictionMetric(ctx, cq)
		})

	})

})

func findEvictionMetric(ctx context.Context, cq *kueuev1beta2.ClusterQueue) {
	Eventually(func() error {
		metricsOutput, _, err := Kexecute(ctx, clients.RestConfig, kubeClient,
			testutils.OperatorNamespace, "curl-metrics-test", "curl-metrics",
			[]string{
				"/bin/sh", "-c",
				fmt.Sprintf(
					"curl --fail --silent --show-error --cacert /etc/kueue/metrics/certs/ca.crt -H \"Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)\" https://kueue-controller-manager-metrics-service.%s.svc.cluster.local:8443/metrics",
					testutils.OperatorNamespace,
				),
			})
		if err != nil {
			return fmt.Errorf("exec into curl pod failed: %w", err)
		}

		parser := expfmt.NewTextParser(model.UTF8Validation)
		metricFamilies, err := parser.TextToMetricFamilies(strings.NewReader(string(metricsOutput)))
		if err != nil {
			return fmt.Errorf("failed to parse Prometheus metrics: %w", err)
		}

		return findMetricWithLabels(metricFamilies, "kueue_evicted_workloads_once_total", map[string]string{
			"cluster_queue":    cq.Name,
			"priority_class":   "",
			"reason":           "PodsReadyTimeout",
			"underlying_cause": "WaitForStart",
		})
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "kueue_evicted_workloads_once_total should be present")
}

// createJobGang creates a job with an init container that delays pod readiness by 10 seconds.
// Parallelism specifies how many pods should run in parallel (for gang scheduling tests).
// CPU and memory specify the resource requests per pod.
func createJobGang(ctx context.Context, name, namespace, queueName, cpu, memory string, parallelism int32) (*batchv1.Job, error) {
	builder := testutils.NewTestResourceBuilder(namespace, queueName)
	job := builder.NewJob()
	job.Name = name
	job.Labels[testutils.QueueLabel] = queueName
	job.Spec.Parallelism = ptr.To(parallelism)
	job.Spec.Completions = ptr.To(parallelism)
	job.Spec.Template.Spec.InitContainers = []corev1.Container{
		{
			Name:    "delay-ready",
			Image:   "quay.io/prometheus/busybox:latest",
			Command: []string{"sh", "-c", "echo 'Delaying readiness...'; sleep 30"},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				},
			},
		},
	}
	job.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "echo 'Running main container'"}
	job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse(cpu)
	job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = resource.MustParse(memory)

	return kubeClient.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
}

// createReadinessProbeStatefulSet creates a StatefulSet whose pods block on /tmp/ready:
//   - The main container loops until /tmp/ready exists, then sleeps so the pod stays Running.
//   - A readiness probe passes only once /tmp/ready exists.
//
// Pods are managed in parallel so all replicas start together for gang scheduling.
func createReadinessProbeStatefulSet(ctx context.Context, name, namespace, queueName string, replicas int32) (*appsv1.StatefulSet, error) {
	builder := testutils.NewTestResourceBuilder(namespace, queueName)
	sts := builder.NewStatefulSet()
	sts.Name = name
	sts.Spec.Replicas = ptr.To(replicas)
	sts.Spec.PodManagementPolicy = appsv1.ParallelPodManagement

	container := &sts.Spec.Template.Spec.Containers[0]
	container.Command = []string{"sh", "-c", "until test -f /tmp/ready; do sleep 1; done; sleep 3600"}
	container.ReadinessProbe = &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			Exec: &corev1.ExecAction{
				Command: []string{"test", "-f", "/tmp/ready"},
			},
		},
	}
	return kubeClient.AppsV1().StatefulSets(namespace).Create(ctx, sts, metav1.CreateOptions{})
}

// waitForWorkloadEvictedByPodsReadyTimeout polls until the named workload reports
// Evicted=True with reason PodsReadyTimeout, failing if it does not evict in time.
func waitForWorkloadEvictedByPodsReadyTimeout(ctx context.Context, namespace, workloadName string) {
	Eventually(func() error {
		wl, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace).Get(ctx, workloadName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		cond := apimeta.FindStatusCondition(wl.Status.Conditions, kueuev1beta2.WorkloadEvicted)
		if cond == nil || cond.Status != metav1.ConditionTrue {
			return fmt.Errorf("workload not yet evicted")
		}
		if cond.Reason != kueuev1beta2.WorkloadEvictedByPodsReadyTimeout {
			return fmt.Errorf("unexpected eviction reason: %s (want %s)", cond.Reason, kueuev1beta2.WorkloadEvictedByPodsReadyTimeout)
		}
		return nil
	}, 40*time.Second, testutils.OperatorPoll).Should(Succeed(),
		"workload should be evicted due to PodsReadyTimeout")
}

// createReadinessProbeGangJob creates a gang job (parallelism pods) where each pod:
//   - Runs a main container that loops until /tmp/ready exists, then exits 0 (job completes).
//   - Has a readiness probe that passes only when /tmp/ready exists.
func createReadinessProbeGangJob(ctx context.Context, name, namespace, queueName, cpu, memory string, parallelism int32) (*batchv1.Job, error) {
	builder := testutils.NewTestResourceBuilder(namespace, queueName)
	job := builder.NewJob()
	job.Name = name
	job.Labels[testutils.QueueLabel] = queueName
	job.Spec.Parallelism = ptr.To(parallelism)
	job.Spec.Completions = ptr.To(parallelism)
	// Loop until /tmp/ready appears, then exit 0 so the pod succeeds and the job completes.
	job.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c",
		"until test -f /tmp/ready; do sleep 1; done; echo 'ready file found, completing'"}
	job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse(cpu)
	job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = resource.MustParse(memory)
	job.Spec.Template.Spec.Containers[0].ReadinessProbe = &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			Exec: &corev1.ExecAction{
				Command: []string{"test", "-f", "/tmp/ready"},
			},
		},
	}
	return kubeClient.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
}

func findGangSchedulingMetric(ctx context.Context, metricName string, expectedLabels map[string]string) {
	Eventually(func() error {
		metricsOutput, _, err := Kexecute(ctx, clients.RestConfig, kubeClient,
			testutils.OperatorNamespace, "curl-metrics-test", "curl-metrics",
			[]string{
				"/bin/sh", "-c",
				fmt.Sprintf(
					"curl --fail --silent --show-error --max-time 10 --cacert /etc/kueue/metrics/certs/ca.crt -H \"Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)\" https://kueue-controller-manager-metrics-service.%s.svc.cluster.local:8443/metrics",
					testutils.OperatorNamespace,
				),
			})
		if err != nil {
			return fmt.Errorf("exec into curl pod failed: %w", err)
		}

		parser := expfmt.NewTextParser(model.UTF8Validation)
		metricFamilies, err := parser.TextToMetricFamilies(strings.NewReader(string(metricsOutput)))
		if err != nil {
			return fmt.Errorf("failed to parse Prometheus metrics: %w", err)
		}

		return findMetricWithLabels(metricFamilies, metricName, expectedLabels)
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), fmt.Sprintf("%s should be present", metricName))
}

// makeGangSchedulingConfig creates a KueueConfiguration with ByWorkload gang scheduling policy.
func makeGangSchedulingConfig(integrations ssv1.Integrations, byWorkload ssv1.ByWorkload) ssv1.KueueConfiguration {
	return ssv1.KueueConfiguration{
		Integrations: integrations,
		GangScheduling: ssv1.GangScheduling{
			Policy:     ssv1.GangSchedulingPolicyByWorkload,
			ByWorkload: &byWorkload,
		},
	}
}
