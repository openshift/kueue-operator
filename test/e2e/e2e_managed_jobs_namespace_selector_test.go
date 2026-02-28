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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	kueueoperatorv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1 "k8s.io/api/core/v1"
	upstreamkueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"
)

var _ = Describe("ManagedJobsNamespaceSelectorAlwaysRespected", Label("managed-jobs-ns-selector"), Ordered, func() {
	When("labelPolicy=None with managed namespace selector", func() {
		var (
			managedNs            *corev1.Namespace
			unmanagedNs          *corev1.Namespace
			managedBuilder       *testutils.TestResourceBuilder
			unmanagedBuilder     *testutils.TestResourceBuilder
			nsKueueClient        *upstreamkueueclient.Clientset
			initialKueueInstance *kueueoperatorv1.Kueue
			localQueueClean      func()
		)

		BeforeAll(func(ctx context.Context) {
			nsKueueClient = clients.UpstreamKueueClient

			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
			initialKueueInstance = kueueInstance.DeepCopy()
			kueueInstance.Spec.Config.WorkloadManagement.LabelPolicy = kueueoperatorv1.LabelPolicyNone
			applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)
			createClusterQueueAndResourceFlavor(ctx)

			managedNs, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "e2e-ns-selector-managed-",
					Labels: map[string]string{
						testutils.OpenShiftManagedLabel: "true",
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			unmanagedNs, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "e2e-ns-selector-unmanaged-",
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			managedBuilder = testutils.NewTestResourceBuilder(managedNs.Name, testutils.DefaultLocalQueueName)
			unmanagedBuilder = testutils.NewTestResourceBuilder(unmanagedNs.Name, testutils.DefaultLocalQueueName)

			localQueueClean, err = testutils.CreateLocalQueue(ctx, nsKueueClient, managedNs.Name, testutils.DefaultLocalQueueName)
			Expect(err).NotTo(HaveOccurred())
		})

		AfterAll(func(ctx context.Context) {
			if localQueueClean != nil {
				localQueueClean()
			}
			if managedNs != nil {
				err := kubeClient.CoreV1().Namespaces().Delete(ctx, managedNs.Name, metav1.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred())
				testutils.WaitForAllPodsInNamespaceDeleted(ctx, clients.GenericClient, managedNs)
			}
			if unmanagedNs != nil {
				err := kubeClient.CoreV1().Namespaces().Delete(ctx, unmanagedNs.Name, metav1.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred())
				testutils.WaitForAllPodsInNamespaceDeleted(ctx, clients.GenericClient, unmanagedNs)
			}
			deleteClusterQueueAndResourceFlavor(ctx, nsKueueClient)
			applyKueueConfig(ctx, initialKueueInstance.Spec.Config, kubeClient)
		})

		JustAfterEach(func(ctx context.Context) {
			testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 500)
		})

		It("should not reconcile a job without queue-name in unlabeled namespace", func(ctx context.Context) {
			By("creating a job without queue-name in unmanaged namespace")
			job := unmanagedBuilder.NewJobWithoutQueue()
			createdJob, err := kubeClient.BatchV1().Jobs(unmanagedNs.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

			By("verifying no workload is created for the job in unmanaged namespace")
			Consistently(func() error {
				workloads, err := nsKueueClient.KueueV1beta2().Workloads(unmanagedNs.Name).List(ctx, metav1.ListOptions{})
				if err != nil {
					return err
				}
				for _, wl := range workloads.Items {
					for _, ownerRef := range wl.OwnerReferences {
						if ownerRef.UID == createdJob.UID {
							return fmt.Errorf("unexpected workload found for job in unmanaged namespace")
						}
					}
				}
				return nil
			}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(Succeed(), "No workload should be created in unmanaged namespace")
		})

		It("should reconcile a job without queue-name in labeled namespace and create a workload", func(ctx context.Context) {
			By("creating a job without queue-name in managed namespace")
			job := managedBuilder.NewJobWithoutQueue()
			createdJob, err := kubeClient.BatchV1().Jobs(managedNs.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

			By("verifying workload is created for the job in managed namespace")
			verifyWorkloadCreated(nsKueueClient, managedNs.Name, string(createdJob.UID))
		})

		It("should not reconcile a job with queue-name in unlabeled namespace", func(ctx context.Context) {
			By("creating a job with queue-name label in unmanaged namespace")
			job := unmanagedBuilder.NewJobWithoutQueue()
			job.Labels = map[string]string{testutils.QueueLabel: testutils.DefaultLocalQueueName}
			createdJob, err := kubeClient.BatchV1().Jobs(unmanagedNs.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

			By("verifying job is not suspended and runs normally")
			Eventually(func(g Gomega) {
				gotJob, err := kubeClient.BatchV1().Jobs(unmanagedNs.Name).Get(ctx, createdJob.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				if gotJob.Spec.Suspend != nil {
					g.Expect(*gotJob.Spec.Suspend).To(BeFalse(), "Job should not be suspended in unmanaged namespace")
				}
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Job should run without Kueue interference in unlabeled namespace")

			By("verifying no workload is created for the job in unmanaged namespace")
			Consistently(func() error {
				workloads, err := nsKueueClient.KueueV1beta2().Workloads(unmanagedNs.Name).List(ctx, metav1.ListOptions{})
				if err != nil {
					return err
				}
				for _, wl := range workloads.Items {
					for _, ownerRef := range wl.OwnerReferences {
						if ownerRef.UID == createdJob.UID {
							return fmt.Errorf("unexpected workload found for job with queue-name in unmanaged namespace")
						}
					}
				}
				return nil
			}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(Succeed(), "No workload should be created in unmanaged namespace")
		})

	})

	When("default labelPolicy with managed namespace selector", func() {
		var (
			unmanagedNs      *corev1.Namespace
			unmanagedBuilder *testutils.TestResourceBuilder
			nsKueueClient    *upstreamkueueclient.Clientset
		)

		BeforeAll(func(ctx context.Context) {
			nsKueueClient = clients.UpstreamKueueClient
			createClusterQueueAndResourceFlavor(ctx)

			var err error
			unmanagedNs, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "e2e-ns-selector-unmanaged-",
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			unmanagedBuilder = testutils.NewTestResourceBuilder(unmanagedNs.Name, "test-queue")
		})

		AfterAll(func(ctx context.Context) {
			if unmanagedNs != nil {
				err := kubeClient.CoreV1().Namespaces().Delete(ctx, unmanagedNs.Name, metav1.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred())
				testutils.WaitForAllPodsInNamespaceDeleted(ctx, clients.GenericClient, unmanagedNs)
			}
			deleteClusterQueueAndResourceFlavor(ctx, nsKueueClient)
		})

		JustAfterEach(func(ctx context.Context) {
			testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 500)
		})

		It("should not reconcile a job with queue-name in unlabeled namespace", func(ctx context.Context) {
			By("creating a job with queue-name label in unmanaged namespace")
			job := unmanagedBuilder.NewJobWithoutQueue()
			job.Labels = map[string]string{testutils.QueueLabel: "test-queue"}
			createdJob, err := kubeClient.BatchV1().Jobs(unmanagedNs.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

			By("verifying no workload is created for the job in unmanaged namespace")
			Consistently(func() error {
				workloads, err := nsKueueClient.KueueV1beta2().Workloads(unmanagedNs.Name).List(ctx, metav1.ListOptions{})
				if err != nil {
					return err
				}
				for _, wl := range workloads.Items {
					for _, ownerRef := range wl.OwnerReferences {
						if ownerRef.UID == createdJob.UID {
							return fmt.Errorf("unexpected workload found for job in unmanaged namespace")
						}
					}
				}
				return nil
			}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(Succeed(), "No workload should be created in unmanaged namespace")
		})
	})
})
