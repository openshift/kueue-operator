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
		initialKueueInstance *ssv1.Kueue
		labelKey             = testutils.OpenShiftManagedLabel
		labelValue           = trueLabelValue
	)

	JustAfterEach(func(ctx context.Context) {
		testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 500)
	})

	When("LocalQueues have different FairSharing weight values", func() {
		BeforeAll(func(ctx context.Context) {
			By("Saving initial Kueue configuration")
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
			initialKueueInstance = kueueInstance.DeepCopy()

			By("Configuring Kueue with AdmissionFairSharing enabled (usageHalfLifeTimeSeconds=60, usageSamplingIntervalSeconds=5)")
			desiredConfig := initialKueueInstance.Spec.Config
			desiredConfig.AdmissionFairSharing = ssv1.AdmissionFairSharing{
				UsageHalfLifeTimeSeconds:     60,
				UsageSamplingIntervalSeconds: 5,
			}
			applyKueueConfig(ctx, desiredConfig, kubeClient)

			By("Verifying kueue-manager-config ConfigMap contains AdmissionFairSharing settings")
			Eventually(func(g Gomega) {
				configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
				g.Expect(err).ToNot(HaveOccurred(), "Failed to get kueue-manager-config ConfigMap")
				configData := configMap.Data["controller_manager_config.yaml"]
				g.Expect(configData).To(ContainSubstring("usageHalfLifeTime: 1m0s"), "usageHalfLifeTime not found in ConfigMap")
				g.Expect(configData).To(ContainSubstring("usageSamplingInterval: 5s"), "usageSamplingInterval not found in ConfigMap")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "kueue-manager-config should have AdmissionFairSharing settings")

		})

		AfterAll(func(ctx context.Context) {
			By("Restoring initial Kueue configuration")
			applyKueueConfig(ctx, initialKueueInstance.Spec.Config, kubeClient)
		})

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
			propagationPolicy := metav1.DeletePropagationBackground
			err = kubeClient.BatchV1().Jobs(namespace.Name).Delete(ctx, job1.Name, metav1.DeleteOptions{
				PropagationPolicy: &propagationPolicy,
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

})

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
