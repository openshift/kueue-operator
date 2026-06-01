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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ssv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
)

var _ = Describe("Admission Fair Sharing", Label("admission-fair-sharing"), Ordered, func() {
	var (
		labelKey                           = testutils.OpenShiftManagedLabel
		labelValue                         = trueLabelValue
		usageHalfLifeTimeSeconds     int32 = 60
		usageSamplingIntervalSeconds int32 = 5
	)

	JustAfterEach(func(ctx context.Context) {
		testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 500)
	})

	BeforeAll(func(ctx context.Context) {
		enableAdmissionFairSharing(ctx, usageHalfLifeTimeSeconds, usageSamplingIntervalSeconds)
	})

	When("LocalQueues have different FairSharing weight values", func() {

		It("should prioritize the higher-weight LocalQueue when quota frees up", func(ctx context.Context) {
			By("Creating Resource Flavor")
			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
			DeferCleanup(cleanupResourceFlavor)

			By("Creating ClusterQueue with 2 CPUs and UsageBasedAdmissionFairSharing")
			clusterQueue, cleanupClusterQueue, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithCPU("2").
				WithMemory("200Mi").
				WithFlavorName(resourceFlavor.Name).
				WithAdmissionScope(kueuev1beta2.UsageBasedAdmissionFairSharing).
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue")
			DeferCleanup(cleanupClusterQueue)

			By("Creating namespace")
			namespace, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "afs-weights-",
					Labels: map[string]string{
						labelKey: labelValue,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")
			DeferCleanup(func(ctx context.Context) {
				By(fmt.Sprintf("Deleting namespace %s", namespace.Name))
				err := kubeClient.CoreV1().Namespaces().Delete(ctx, namespace.Name, metav1.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred(), "Failed to delete namespace")
				testutils.WaitForAllPodsInNamespaceDeleted(ctx, clients.GenericClient, namespace)
			})

			By("Creating LocalQueue 'heavy' with weight=1")
			lqHeavy, cleanupLQHeavy, err := testutils.NewLocalQueue(namespace.Name, "lq-heavy").
				WithClusterQueue(clusterQueue.Name).
				WithFairSharingWeight("1").
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue heavy")
			DeferCleanup(cleanupLQHeavy)

			By("Creating LocalQueue 'light' with weight=2")
			lqLight, cleanupLQLight, err := testutils.NewLocalQueue(namespace.Name, "lq-light").
				WithClusterQueue(clusterQueue.Name).
				WithFairSharingWeight("2").
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue light")
			DeferCleanup(cleanupLQLight)

			By("Creating Job1 on lq-heavy consuming 1 CPU (long-running)")
			job1, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, newLongRunningJob("job-heavy-1", namespace.Name, lqHeavy.Name, "1", "50Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create job on heavy queue")

			By("Verifying Job1 workload is admitted")
			checkWorkloadCondition(ctx, namespace.Name, string(job1.UID), kueuev1beta2.WorkloadAdmitted, "job-heavy-1")

			By("Creating Job2 on lq-light consuming 1 CPU (long-running)")
			job2, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, newLongRunningJob("job-light-1", namespace.Name, lqLight.Name, "1", "50Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create job on light queue")

			By("Verifying Job2 workload is admitted")
			checkWorkloadCondition(ctx, namespace.Name, string(job2.UID), kueuev1beta2.WorkloadAdmitted, "job-light-1")

			By("Waiting for Job1 and Job2 pods to be running")
			Eventually(func() bool {
				return testutils.IsJobPodRunning(ctx, kubeClient, namespace.Name, "job-heavy-1")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "Job1 pod should be running")
			Eventually(func() bool {
				return testutils.IsJobPodRunning(ctx, kubeClient, namespace.Name, "job-light-1")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "Job2 pod should be running")

			By("Waiting for both LocalQueues to accumulate usage (consumedResources.cpu > 0)")
			Eventually(func() bool {
				lq, err := clients.UpstreamKueueClient.KueueV1beta2().LocalQueues(namespace.Name).Get(ctx, "lq-heavy", metav1.GetOptions{})
				if err != nil || lq.Status.FairSharing == nil || lq.Status.FairSharing.AdmissionFairSharingStatus == nil {
					return false
				}
				cpu := lq.Status.FairSharing.AdmissionFairSharingStatus.ConsumedResources[corev1.ResourceCPU]
				return cpu.Cmp(resource.MustParse("0")) > 0
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "lq-heavy should have consumedResources.cpu > 0")
			Eventually(func() bool {
				lq, err := clients.UpstreamKueueClient.KueueV1beta2().LocalQueues(namespace.Name).Get(ctx, "lq-light", metav1.GetOptions{})
				if err != nil || lq.Status.FairSharing == nil || lq.Status.FairSharing.AdmissionFairSharingStatus == nil {
					return false
				}
				cpu := lq.Status.FairSharing.AdmissionFairSharingStatus.ConsumedResources[corev1.ResourceCPU]
				return cpu.Cmp(resource.MustParse("0")) > 0
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "lq-light should have consumedResources.cpu > 0")

			By("Creating Job3 on lq-heavy (pending, no quota available)")
			job3, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, newLongRunningJob("job-heavy-2", namespace.Name, lqHeavy.Name, "800m", "100Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create pending job on heavy queue")

			By("Creating Job4 on lq-light (pending, no quota available)")
			job4, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, newLongRunningJob("job-light-2", namespace.Name, lqLight.Name, "800m", "50Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create pending job on light queue")

			By("Waiting for both pending jobs to be Suspended")
			Eventually(func(g Gomega) {
				g.Expect(testutils.IsJobSuspended(ctx, kubeClient, namespace.Name, job3.Name)).To(BeTrue(),
					"Job3 on lq-heavy should be suspended")
				g.Expect(testutils.IsJobSuspended(ctx, kubeClient, namespace.Name, job4.Name)).To(BeTrue(),
					"Job4 on lq-light should be suspended")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Both pending jobs should become suspended")

			By("Verifying both pending jobs remain suspended (ClusterQueue is full)")
			Consistently(func(g Gomega) {
				g.Expect(testutils.IsJobSuspended(ctx, kubeClient, namespace.Name, job3.Name)).To(BeTrue(),
					"Job3 on lq-heavy should remain suspended")
				g.Expect(testutils.IsJobSuspended(ctx, kubeClient, namespace.Name, job4.Name)).To(BeTrue(),
					"Job4 on lq-light should remain suspended")
			}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(Succeed(), "Pending jobs should stay suspended while CQ is full")

			By("Deleting Job1 (heavy) to free 1 CPU")
			err = kubeClient.BatchV1().Jobs(namespace.Name).Delete(ctx, job1.Name, metav1.DeleteOptions{
				PropagationPolicy: ptr.To(metav1.DeletePropagationBackground),
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to delete job1")

			By("Verifying Job4 (light, weight=2) wins admission")
			Eventually(func() bool {
				return !testutils.IsJobSuspended(ctx, kubeClient, namespace.Name, job4.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "Job4 on lq-light (weight=2) should be admitted")

			By("Verifying Job3 (heavy, weight=1) remains suspended while Job4 runs")
			Consistently(func() bool {
				return testutils.IsJobSuspended(ctx, kubeClient, namespace.Name, job3.Name)
			}, testutils.ConsistentlyLongTimeout, testutils.ConsistentlyLongPoll).Should(BeTrue(), "Job3 on lq-heavy (weight=1) should remain suspended")
		})
	})

	When("usageSamplingIntervalSeconds controls lastUpdate cadence", func() {

		It("should advance lastUpdate timestamps at approximately the configured sampling interval", func(ctx context.Context) {
			By("Creating Resource Flavor")
			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
			DeferCleanup(cleanupResourceFlavor)

			By("Creating ClusterQueue with UsageBasedAdmissionFairSharing")
			clusterQueue, cleanupClusterQueue, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithCPU("2").
				WithMemory("200Mi").
				WithFlavorName(resourceFlavor.Name).
				WithAdmissionScope(kueuev1beta2.UsageBasedAdmissionFairSharing).
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue")
			DeferCleanup(cleanupClusterQueue)

			By("Creating namespace")
			namespace, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "afs-sampling-",
					Labels: map[string]string{
						labelKey: labelValue,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")
			DeferCleanup(func(ctx context.Context) {
				By(fmt.Sprintf("Deleting namespace %s", namespace.Name))
				err := kubeClient.CoreV1().Namespaces().Delete(ctx, namespace.Name, metav1.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred(), "Failed to delete namespace")
				testutils.WaitForAllPodsInNamespaceDeleted(ctx, clients.GenericClient, namespace)
			})

			By("Creating LocalQueue")
			lq, cleanupLQ, err := testutils.NewLocalQueue(namespace.Name, "lq-sampling").
				WithClusterQueue(clusterQueue.Name).
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue")
			DeferCleanup(cleanupLQ)

			By("Creating a long-running job to generate usage")
			job, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, newLongRunningJob("job-sampling", namespace.Name, lq.Name, "1", "50Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create job")

			By("Verifying job workload is admitted")
			checkWorkloadCondition(ctx, namespace.Name, string(job.UID), kueuev1beta2.WorkloadAdmitted, "job-sampling")

			By("Waiting for job pod to be running")
			Eventually(func() bool {
				return testutils.IsJobPodRunning(ctx, kubeClient, namespace.Name, "job-sampling")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "Job pod should be running")

			By("Waiting for initial lastUpdate to appear on LocalQueue status")
			var firstUpdate metav1.Time
			Eventually(func() bool {
				lqStatus, err := clients.UpstreamKueueClient.KueueV1beta2().LocalQueues(namespace.Name).Get(ctx, lq.Name, metav1.GetOptions{})
				if err != nil || lqStatus.Status.FairSharing == nil || lqStatus.Status.FairSharing.AdmissionFairSharingStatus == nil {
					return false
				}
				firstUpdate = lqStatus.Status.FairSharing.AdmissionFairSharingStatus.LastUpdate
				return !firstUpdate.IsZero()
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "LocalQueue should have a non-zero lastUpdate")

			By("Collecting 3 consecutive lastUpdate timestamps to measure sampling cadence")
			const numSamples = 3
			expectedInterval := time.Duration(usageSamplingIntervalSeconds) * time.Second
			timestamps := []metav1.Time{firstUpdate}

			for i := 0; i < numSamples; i++ {
				previousUpdate := timestamps[len(timestamps)-1]
				var nextUpdate metav1.Time
				Eventually(func(g Gomega) {
					lqStatus, err := clients.UpstreamKueueClient.KueueV1beta2().LocalQueues(namespace.Name).Get(ctx, lq.Name, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred(), "Failed to get LocalQueue status")
					g.Expect(lqStatus.Status.FairSharing).NotTo(BeNil(), "FairSharing status should not be nil")
					g.Expect(lqStatus.Status.FairSharing.AdmissionFairSharingStatus).NotTo(BeNil(), "AdmissionFairSharingStatus should not be nil")
					nextUpdate = lqStatus.Status.FairSharing.AdmissionFairSharingStatus.LastUpdate
					g.Expect(nextUpdate.Time.After(previousUpdate.Time)).To(BeTrue(), "lastUpdate should advance beyond the previous timestamp")
				}, testutils.OperatorReadyTime, testutils.ConsistentlyPoll).Should(Succeed(), fmt.Sprintf("lastUpdate should advance beyond %v", previousUpdate.Time.Format(time.RFC3339)))
				timestamps = append(timestamps, nextUpdate)
			}

			By("Verifying all intervals are approximately equal to the configured sampling interval")
			for i := 1; i < len(timestamps); i++ {
				interval := timestamps[i].Time.Sub(timestamps[i-1].Time)
				By(fmt.Sprintf("Interval %d: %v (from %v to %v)", i, interval, timestamps[i-1].Time.Format(time.RFC3339), timestamps[i].Time.Format(time.RFC3339)))
				Expect(interval).To(BeNumerically(">=", expectedInterval-2*time.Second),
					fmt.Sprintf("Interval %d (%v) should be at least %v", i, interval, expectedInterval-2*time.Second))
				Expect(interval).To(BeNumerically("<=", expectedInterval+5*time.Second),
					fmt.Sprintf("Interval %d (%v) should be at most %v", i, interval, expectedInterval+5*time.Second))
			}
		})
	})

	When("VisibilityOnDemand reflects usage-based ordering", func() {

		It("should report pending workloads in usage-based order, not FIFO", func(ctx context.Context) {
			By("Creating RBAC for visibility API access")
			cleanupCRB, err := createClusterRoleBinding(ctx, "default", testutils.OperatorNamespace, "kueue-batch-admin-role")
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster role binding")
			DeferCleanup(cleanupCRB)

			By("Creating Resource Flavor")
			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
			DeferCleanup(cleanupResourceFlavor)

			By("Creating ClusterQueue with 2 CPUs and UsageBasedAdmissionFairSharing")
			clusterQueue, cleanupClusterQueue, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithCPU("2").
				WithMemory("2Gi").
				WithFlavorName(resourceFlavor.Name).
				WithAdmissionScope(kueuev1beta2.UsageBasedAdmissionFairSharing).
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue")
			DeferCleanup(cleanupClusterQueue)

			By("Creating namespace")
			namespace, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "afs-visibility-",
					Labels: map[string]string{
						labelKey: labelValue,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")
			DeferCleanup(func(ctx context.Context) {
				By(fmt.Sprintf("Deleting namespace %s", namespace.Name))
				err := kubeClient.CoreV1().Namespaces().Delete(ctx, namespace.Name, metav1.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred(), "Failed to delete namespace")
				testutils.WaitForAllPodsInNamespaceDeleted(ctx, clients.GenericClient, namespace)
			})

			By("Creating LocalQueue lq1 (will accumulate usage)")
			lq1, cleanupLQ1, err := testutils.NewLocalQueue(namespace.Name, "lq1").
				WithClusterQueue(clusterQueue.Name).
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue lq1")
			DeferCleanup(cleanupLQ1)

			By("Creating LocalQueue lq2 (will stay at zero usage)")
			lq2, cleanupLQ2, err := testutils.NewLocalQueue(namespace.Name, "lq2").
				WithClusterQueue(clusterQueue.Name).
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue lq2")
			DeferCleanup(cleanupLQ2)

			By("Saturating the ClusterQueue with 2 jobs from lq1 (2/2 CPU)")
			job1, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, newLongRunningJob("job1", namespace.Name, lq1.Name, "1", "200Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create job1")

			job2, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, newLongRunningJob("job2", namespace.Name, lq1.Name, "1", "200Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create job2")

			By("Verifying both jobs are admitted")
			checkWorkloadCondition(ctx, namespace.Name, string(job1.UID), kueuev1beta2.WorkloadAdmitted, "job1")
			checkWorkloadCondition(ctx, namespace.Name, string(job2.UID), kueuev1beta2.WorkloadAdmitted, "job2")

			By("Waiting for both job pods to be running")
			Eventually(func() bool {
				return testutils.IsJobPodRunning(ctx, kubeClient, namespace.Name, "job1")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "job1 pod should be running")
			Eventually(func() bool {
				return testutils.IsJobPodRunning(ctx, kubeClient, namespace.Name, "job2")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "job2 pod should be running")

			By("Waiting for lq1 to accumulate usage (consumedResources.cpu > 0)")
			Eventually(func() bool {
				lqStatus, err := clients.UpstreamKueueClient.KueueV1beta2().LocalQueues(namespace.Name).Get(ctx, lq1.Name, metav1.GetOptions{})
				if err != nil || lqStatus.Status.FairSharing == nil || lqStatus.Status.FairSharing.AdmissionFairSharingStatus == nil {
					return false
				}
				cpu := lqStatus.Status.FairSharing.AdmissionFairSharingStatus.ConsumedResources[corev1.ResourceCPU]
				return cpu.Cmp(resource.MustParse("0")) > 0
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "lq1 should have consumedResources.cpu > 0")

			By("Verifying lq2 has zero usage")
			Eventually(func(g Gomega) {
				lq2Status, err := clients.UpstreamKueueClient.KueueV1beta2().LocalQueues(namespace.Name).Get(ctx, lq2.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get lq2 status")
				g.Expect(lq2Status.Status.FairSharing).NotTo(BeNil(), "lq2 FairSharing status should not be nil")
				g.Expect(lq2Status.Status.FairSharing.AdmissionFairSharingStatus).NotTo(BeNil(), "lq2 AdmissionFairSharingStatus should not be nil")
				cpu := lq2Status.Status.FairSharing.AdmissionFairSharingStatus.ConsumedResources[corev1.ResourceCPU]
				g.Expect(cpu.Cmp(resource.MustParse("0"))).To(Equal(0), "lq2 should have zero CPU usage")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "lq2 should have zero CPU usage")

			By("Creating job3 on lq1 FIRST (high-usage queue, should get lower priority)")
			job3, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, newLongRunningJob("job3", namespace.Name, lq1.Name, "1", "200Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create job3")

			By("Creating job4 on lq2 SECOND (zero-usage queue, should get higher priority)")
			job4, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, newLongRunningJob("job4", namespace.Name, lq2.Name, "1", "200Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create job4")

			By("Verifying both pending jobs are suspended")
			Eventually(func(g Gomega) {
				g.Expect(testutils.IsJobSuspended(ctx, kubeClient, namespace.Name, job3.Name)).To(BeTrue(), "job3 should be suspended")
				g.Expect(testutils.IsJobSuspended(ctx, kubeClient, namespace.Name, job4.Name)).To(BeTrue(), "job4 should be suspended")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Both pending jobs should be suspended")

			By("Querying VisibilityOnDemand API and verifying usage-based ordering")
			Eventually(func(g Gomega) {
				pendingWorkloads, err := visibilityClient.ClusterQueues().GetPendingWorkloadsSummary(ctx, clusterQueue.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get pending workloads summary")
				g.Expect(pendingWorkloads.Items).To(HaveLen(2), "Expected 2 pending workloads")

				g.Expect(string(pendingWorkloads.Items[0].LocalQueueName)).To(Equal(lq2.Name),
					"Position 0 should be job4 (lq2, zero usage)")
				g.Expect(pendingWorkloads.Items[0].PositionInClusterQueue).To(Equal(int32(0)))
				g.Expect(string(pendingWorkloads.Items[1].LocalQueueName)).To(Equal(lq1.Name),
					"Position 1 should be job3 (lq1, high usage)")
				g.Expect(pendingWorkloads.Items[1].PositionInClusterQueue).To(Equal(int32(1)))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Pending workloads should reflect usage-based ordering")

			By("Deleting job1 to free 1 CPU and verify admission matches visibility ordering")
			err = kubeClient.BatchV1().Jobs(namespace.Name).Delete(ctx, job1.Name, metav1.DeleteOptions{
				PropagationPolicy: ptr.To(metav1.DeletePropagationBackground),
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to delete job1")

			By("Verifying job4 (lq2, position 0) gets admitted")
			Eventually(func() bool {
				return !testutils.IsJobSuspended(ctx, kubeClient, namespace.Name, job4.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "job4 (lq2, zero usage) should be admitted first")

			By("Verifying job3 (lq1, position 1) remains suspended")
			Consistently(func() bool {
				return testutils.IsJobSuspended(ctx, kubeClient, namespace.Name, job3.Name)
			}, testutils.ConsistentlyLongTimeout, testutils.ConsistentlyLongPoll).Should(BeTrue(), "job3 (lq1, high usage) should remain suspended")
		})
	})

	// Upstream bug: kubernetes-sigs/kueue#10434 (OCPBUGS-85113)
	// Memory resourceWeights are counted in raw bytes instead of GiB, so non-zero
	// memory weights produce unintuitive scores. Setting memory weight to "0"
	// sidesteps this and isolates CPU as the sole scoring factor.
	When("resourceWeights customizes which resources drive fair sharing scoring", func() {

		It("should deprioritize the queue with higher weighted CPU usage when cpu weight is 10 and memory weight is 0", func(ctx context.Context) {
			By("Overriding Kueue config with resourceWeights (cpu: 10, memory: 0) and fast decay")
			enableAdmissionFairSharing(ctx, 10, 1,
				ssv1.ResourceWeight{Name: "cpu", Weight: "10"},
				ssv1.ResourceWeight{Name: "memory", Weight: "0"},
			)

			By("Creating Resource Flavor")
			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
			DeferCleanup(cleanupResourceFlavor)

			By("Creating ClusterQueue with 2 CPUs and UsageBasedAdmissionFairSharing")
			clusterQueue, cleanupClusterQueue, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithCPU("2").
				WithMemory("2Gi").
				WithFlavorName(resourceFlavor.Name).
				WithAdmissionScope(kueuev1beta2.UsageBasedAdmissionFairSharing).
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue")
			DeferCleanup(cleanupClusterQueue)

			By("Creating namespace")
			namespace, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "afs-resource-weights-",
					Labels: map[string]string{
						labelKey: labelValue,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")
			DeferCleanup(func(ctx context.Context) {
				By(fmt.Sprintf("Deleting namespace %s", namespace.Name))
				err := kubeClient.CoreV1().Namespaces().Delete(ctx, namespace.Name, metav1.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred(), "Failed to delete namespace")
				testutils.WaitForAllPodsInNamespaceDeleted(ctx, clients.GenericClient, namespace)
			})

			By("Creating LocalQueue 'lq-cpu-heavy' with weight=1")
			lqCPUHeavy, cleanupLQCPUHeavy, err := testutils.NewLocalQueue(namespace.Name, "lq-cpu-heavy").
				WithClusterQueue(clusterQueue.Name).
				WithFairSharingWeight("1").
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue lq-cpu-heavy")
			DeferCleanup(cleanupLQCPUHeavy)

			By("Creating LocalQueue 'lq-cpu-light' with weight=1")
			lqCPULight, cleanupLQCPULight, err := testutils.NewLocalQueue(namespace.Name, "lq-cpu-light").
				WithClusterQueue(clusterQueue.Name).
				WithFairSharingWeight("1").
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue lq-cpu-light")
			DeferCleanup(cleanupLQCPULight)

			By("Creating job-heavy on lq-cpu-heavy consuming 1500m CPU and 500Mi memory")
			jobHeavy, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, newLongRunningJob("job-heavy", namespace.Name, lqCPUHeavy.Name, "1500m", "500Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create job on cpu-heavy queue")

			By("Verifying job-heavy workload is admitted")
			checkWorkloadCondition(ctx, namespace.Name, string(jobHeavy.UID), kueuev1beta2.WorkloadAdmitted, "job-heavy")

			By("Creating job-light on lq-cpu-light consuming 500m CPU")
			jobLight, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, newLongRunningJob("job-light", namespace.Name, lqCPULight.Name, "500m", "1Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create job on cpu-light queue")

			By("Verifying job-light workload is admitted")
			checkWorkloadCondition(ctx, namespace.Name, string(jobLight.UID), kueuev1beta2.WorkloadAdmitted, "job-light")

			By("Waiting for both job pods to be running")
			Eventually(func() bool {
				return testutils.IsJobPodRunning(ctx, kubeClient, namespace.Name, "job-heavy")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "job-heavy pod should be running")
			Eventually(func() bool {
				return testutils.IsJobPodRunning(ctx, kubeClient, namespace.Name, "job-light")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "job-light pod should be running")

			By("Waiting for both LocalQueues to accumulate usage (consumedResources.cpu > 0)")
			Eventually(func() bool {
				lq, err := clients.UpstreamKueueClient.KueueV1beta2().LocalQueues(namespace.Name).Get(ctx, lqCPUHeavy.Name, metav1.GetOptions{})
				if err != nil || lq.Status.FairSharing == nil || lq.Status.FairSharing.AdmissionFairSharingStatus == nil {
					return false
				}
				cpu := lq.Status.FairSharing.AdmissionFairSharingStatus.ConsumedResources[corev1.ResourceCPU]
				return cpu.Cmp(resource.MustParse("0")) > 0
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "lq-cpu-heavy should have consumedResources.cpu > 0")
			Eventually(func() bool {
				lq, err := clients.UpstreamKueueClient.KueueV1beta2().LocalQueues(namespace.Name).Get(ctx, lqCPULight.Name, metav1.GetOptions{})
				if err != nil || lq.Status.FairSharing == nil || lq.Status.FairSharing.AdmissionFairSharingStatus == nil {
					return false
				}
				cpu := lq.Status.FairSharing.AdmissionFairSharingStatus.ConsumedResources[corev1.ResourceCPU]
				return cpu.Cmp(resource.MustParse("0")) > 0
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "lq-cpu-light should have consumedResources.cpu > 0")

			By("Creating job-heavy-pending on lq-cpu-heavy (pending, no quota available)")
			jobHeavyPending, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, newLongRunningJob("job-heavy-pending", namespace.Name, lqCPUHeavy.Name, "1500m", "500Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create pending job on cpu-heavy queue")

			By("Creating job-light-pending on lq-cpu-light (pending, no quota available)")
			jobLightPending, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, newLongRunningJob("job-light-pending", namespace.Name, lqCPULight.Name, "1500m", "1Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create pending job on cpu-light queue")

			By("Waiting for both pending jobs to be Suspended")
			Eventually(func(g Gomega) {
				g.Expect(testutils.IsJobSuspended(ctx, kubeClient, namespace.Name, jobHeavyPending.Name)).To(BeTrue(),
					"job-heavy-pending should be suspended")
				g.Expect(testutils.IsJobSuspended(ctx, kubeClient, namespace.Name, jobLightPending.Name)).To(BeTrue(),
					"job-light-pending should be suspended")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Both pending jobs should become suspended")

			By("Verifying both pending jobs remain suspended (ClusterQueue is full)")
			Consistently(func(g Gomega) {
				g.Expect(testutils.IsJobSuspended(ctx, kubeClient, namespace.Name, jobHeavyPending.Name)).To(BeTrue(),
					"job-heavy-pending should remain suspended")
				g.Expect(testutils.IsJobSuspended(ctx, kubeClient, namespace.Name, jobLightPending.Name)).To(BeTrue(),
					"job-light-pending should remain suspended")
			}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(Succeed(), "Pending jobs should stay suspended while CQ is full")

			By("Deleting job-heavy to free 1500m CPU")
			err = kubeClient.BatchV1().Jobs(namespace.Name).Delete(ctx, jobHeavy.Name, metav1.DeleteOptions{
				PropagationPolicy: ptr.To(metav1.DeletePropagationBackground),
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to delete job-heavy")

			By("Verifying job-light-pending (lq-cpu-light, lower weighted usage) wins admission")
			Eventually(func() bool {
				return !testutils.IsJobSuspended(ctx, kubeClient, namespace.Name, jobLightPending.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "job-light-pending should be admitted (lower CPU usage score)")

			By("Verifying job-heavy-pending (lq-cpu-heavy, higher weighted usage) remains suspended")
			Consistently(func() bool {
				return testutils.IsJobSuspended(ctx, kubeClient, namespace.Name, jobHeavyPending.Name)
			}, testutils.ConsistentlyLongTimeout, testutils.ConsistentlyLongPoll).Should(BeTrue(), "job-heavy-pending should remain suspended (higher CPU usage score)")
		})
	})

})

func enableAdmissionFairSharing(ctx context.Context, halfLifeSeconds, samplingSeconds int32, resourceWeights ...ssv1.ResourceWeight) {
	By("Saving initial Kueue configuration")
	kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
	savedConfig := kueueInstance.Spec.Config

	DeferCleanup(func(ctx context.Context) {
		By("Restoring initial Kueue configuration")
		testutils.ApplyKueueConfig(ctx, savedConfig, clients)
	})

	By("Configuring Kueue with AdmissionFairSharing enabled")
	desiredConfig := kueueInstance.Spec.Config
	desiredConfig.AdmissionFairSharing = ssv1.AdmissionFairSharing{
		UsageHalfLifeTimeSeconds:     halfLifeSeconds,
		UsageSamplingIntervalSeconds: samplingSeconds,
		ResourceWeights:              resourceWeights,
	}
	testutils.ApplyKueueConfig(ctx, desiredConfig, clients)

	expectedHalfLife := (time.Duration(halfLifeSeconds) * time.Second).String()
	expectedSampling := (time.Duration(samplingSeconds) * time.Second).String()

	By("Verifying kueue-manager-config ConfigMap contains AdmissionFairSharing settings")
	Eventually(func(g Gomega) {
		configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
		g.Expect(err).ToNot(HaveOccurred(), "Failed to get kueue-manager-config ConfigMap")
		configData := configMap.Data["controller_manager_config.yaml"]
		g.Expect(configData).To(ContainSubstring(fmt.Sprintf("usageHalfLifeTime: %s", expectedHalfLife)), "usageHalfLifeTime not found in ConfigMap")
		g.Expect(configData).To(ContainSubstring(fmt.Sprintf("usageSamplingInterval: %s", expectedSampling)), "usageSamplingInterval not found in ConfigMap")
		for _, rw := range resourceWeights {
			g.Expect(configData).To(ContainSubstring(fmt.Sprintf("%s: %s", rw.Name, rw.Weight)),
				fmt.Sprintf("resourceWeight %s: %s not found in ConfigMap", rw.Name, rw.Weight))
		}
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "kueue-manager-config should have AdmissionFairSharing settings")
}

func newLongRunningJob(name, namespace, queueName, cpu, memory string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				testutils.QueueLabel: queueName,
			},
		},
		Spec: batchv1.JobSpec{
			Suspend: ptr.To(true),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "worker",
							Image:   testutils.GetContainerImageForWorkloads(),
							Command: []string{"sh", "-c", "sleep infinity"},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse(cpu),
									corev1.ResourceMemory: resource.MustParse(memory),
								},
							},
						},
					},
				},
			},
		},
	}
}
