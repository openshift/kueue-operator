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
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
)

var _ = Describe("Preemption", Label("preemption"), Ordered, func() {
	var (
		labelKey   = testutils.OpenShiftManagedLabel
		labelValue = trueLabelValue
	)

	When("Preemption is Fair Sharing", func() {
		var initialKueueInstance *ssv1.Kueue

		BeforeAll(func(ctx context.Context) {
			By("Saving initial Kueue configuration")
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get Kueue instance")
			initialKueueInstance = kueueInstance.DeepCopy()

			By("Updating Kueue configuration to use FairSharing preemption")
			kueueInstance.Spec.Config.Preemption.PreemptionPolicy = ssv1.PreemptionStrategyFairsharing
			applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)
		})

		AfterAll(func(ctx context.Context) {
			By("Restoring initial Kueue configuration")
			applyKueueConfig(ctx, initialKueueInstance.Spec.Config, kubeClient)
		})

		It("should preempt workloads", func(ctx context.Context) {
			By("Creating Resource Flavor")
			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
			DeferCleanup(cleanupResourceFlavor)

			By("Creating ClusterQueue, Namespace and LocalQueue for A")
			cohortName := "fair-sharing-cohort"
			clusterQueueA, cleanupClusterQueueA, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithCPU("250m").
				WithMemory("256Mi").
				WithFlavorName(resourceFlavor.Name).
				WithCohort(cohortName).
				WithPreemption(kueuev1beta2.PreemptionPolicyLowerPriority).
				WithReclaimWithinCohort(kueuev1beta2.PreemptionPolicyLowerPriority).
				WithBorrowingLimit(corev1.ResourceCPU, "250m").
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue A")
			DeferCleanup(cleanupClusterQueueA)

			namespaceA, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "preemption-a-",
					Labels: map[string]string{
						labelKey: labelValue,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				deleteNamespace(ctx, namespaceA)
			})
			localQueueA, cleanupLocalQueueA, err := testutils.NewLocalQueue(namespaceA.Name, "local-queue-a").WithClusterQueue(clusterQueueA.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue A")
			DeferCleanup(cleanupLocalQueueA)

			By("Creating ClusterQueue, Namespace and LocalQueue for B")
			clusterQueueB, cleanupClusterQueueB, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithCPU("250m").
				WithMemory("256Mi").
				WithFlavorName(resourceFlavor.Name).
				WithCohort(cohortName).
				WithPreemption(kueuev1beta2.PreemptionPolicyLowerPriority).
				WithReclaimWithinCohort(kueuev1beta2.PreemptionPolicyAny).
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue B")
			DeferCleanup(cleanupClusterQueueB)

			namespaceB, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "preemption-b-",
					Labels: map[string]string{
						labelKey: labelValue,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				deleteNamespace(ctx, namespaceB)
			})
			localQueueB, cleanupLocalQueueB, err := testutils.NewLocalQueue(namespaceB.Name, "local-queue-b").WithClusterQueue(clusterQueueB.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue B")
			DeferCleanup(cleanupLocalQueueB)

			By("Creating a job on A that borrows resources from the cohort")
			cleanupBorrowingJob, borrowingJob, err := createCustomJob(ctx, "borrowing-job", namespaceA.Name, localQueueA.Name, "", "500m", "128Mi")
			Expect(err).NotTo(HaveOccurred(), "Failed to create borrowing job")
			DeferCleanup(cleanupBorrowingJob)

			By("Verifying borrowing job workload is admitted")
			checkWorkloadCondition(ctx, namespaceA.Name, string(borrowingJob.UID), kueuev1beta2.WorkloadAdmitted, "borrowing")

			By("Verifying clusterQueueA borrowed 250m CPU")
			Eventually(func() error {
				cq, err := clients.UpstreamKueueClient.KueueV1beta2().ClusterQueues().Get(ctx, clusterQueueA.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				for _, flavorUsage := range cq.Status.FlavorsUsage {
					for _, resourceUsage := range flavorUsage.Resources {
						if resourceUsage.Name == corev1.ResourceCPU {
							expectedBorrowed := resource.MustParse("250m")
							if resourceUsage.Borrowed.Cmp(expectedBorrowed) == 0 {
								return nil
							}
							return fmt.Errorf("expected borrowed CPU to be 250m, got %s", resourceUsage.Borrowed.String())
						}
					}
				}
				return fmt.Errorf("CPU resource not found in clusterQueue status")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "clusterQueueA should have borrowed 250m CPU")

			By("Creating a job on B that will reclaim quota from A")
			cleanupReclaimJob, reclaimJob, err := createCustomJob(ctx, "reclaim-job", namespaceB.Name, localQueueB.Name, "", "250m", "128Mi")
			Expect(err).NotTo(HaveOccurred(), "Failed to create reclaim job")
			DeferCleanup(cleanupReclaimJob)

			By("Verifying reclaim job workload is admitted")
			checkWorkloadCondition(ctx, namespaceB.Name, string(reclaimJob.UID), kueuev1beta2.WorkloadAdmitted, "reclaim")

			By("Verifying borrowing job on A was preempted")
			checkWorkloadCondition(ctx, namespaceA.Name, string(borrowingJob.UID), kueuev1beta2.WorkloadEvicted, "borrowing")

			By("Waiting for reclaim job to finish")
			checkWorkloadCondition(ctx, namespaceB.Name, string(reclaimJob.UID), kueuev1beta2.WorkloadFinished, "reclaim")

			By("Verifying borrowing job on A is re-admitted after reclaim job finishes")
			checkWorkloadCondition(ctx, namespaceA.Name, string(borrowingJob.UID), kueuev1beta2.WorkloadAdmitted, "borrowing")

		})
	})
})

// checkWorkloadCondition waits for a workload to have the specified condition set to True
func checkWorkloadCondition(ctx context.Context, namespace, jobUID, conditionType, description string) {
	Eventually(func() error {
		workloads, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", jobUID),
		})
		if err != nil {
			return err
		}
		if len(workloads.Items) == 0 {
			return fmt.Errorf("no workload found for %s job", description)
		}
		workload := &workloads.Items[0]

		condition := apimeta.FindStatusCondition(workload.Status.Conditions, conditionType)
		if condition == nil {
			return fmt.Errorf("workload %s does not have %s condition yet", workload.Name, conditionType)
		}
		if condition.Status != metav1.ConditionTrue {
			return fmt.Errorf("workload %s %s condition is not True, got: %s", workload.Name, conditionType, condition.Status)
		}

		return nil
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), fmt.Sprintf("%s workload did not have %s condition", description, conditionType))
}
