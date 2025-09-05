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
	kueueoperatorv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	upstreamkueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("LocalQueueDefaulting", Ordered, func() {
	BeforeAll(func() {
		Expect(deployOperand()).To(Succeed(), "operand deployment should not fail")
	})
	AfterAll(func() {
		testutils.CleanUpKueuInstance(context.TODO(), clients.KueueClient, "cluster")
	})

	When("labelPolicy=None and LocalQueue default", func() {
		var (
			ns                   *corev1.Namespace
			initialKueueInstance *kueueoperatorv1.Kueue
			kueueClient          *upstreamkueueclient.Clientset
			builder              *testutils.TestResourceBuilder
		)

		BeforeAll(func() {
			ctx := context.TODO()
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
			initialKueueInstance = kueueInstance.DeepCopy()
			kueueInstance.Spec.Config.WorkloadManagement.LabelPolicy = kueueoperatorv1.LabelPolicyNone
			applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)
			kueueClient = clients.UpstreamKueueClient
			Expect(testutils.CreateClusterQueue(kueueClient)).To(Succeed(), "Failed to create cluster queue")
			Expect(testutils.CreateResourceFlavor(kueueClient)).To(Succeed(), "Failed to create resource flavor")
		})

		AfterAll(func() {
			By("deleting cluster queue")
			err := kueueClient.KueueV1beta1().ClusterQueues().Delete(context.TODO(), "test-clusterqueue", metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() error {
				_, err := kueueClient.KueueV1beta1().ClusterQueues().Get(context.TODO(), "test-clusterqueue", metav1.GetOptions{})
				if apierrors.IsNotFound(err) {
					return nil
				}
				return fmt.Errorf("ClusterQueue test-clusterqueue still exists")
			}, testutils.DeletionTime, testutils.DeletionPoll).Should(Succeed(), "ClusterQueue was not cleaned up properly")
			By("deleting resource flavor")
			err = kueueClient.KueueV1beta1().ResourceFlavors().Delete(context.TODO(), "default", metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() error {
				_, err := kueueClient.KueueV1beta1().ResourceFlavors().Get(context.TODO(), "default", metav1.GetOptions{})
				if apierrors.IsNotFound(err) {
					return nil
				}
				return fmt.Errorf("ResourceFlavor default still exists")
			}, testutils.DeletionTime, testutils.DeletionPoll).Should(Succeed(), "ResourceFlavor was not cleaned up properly")
			applyKueueConfig(context.TODO(), initialKueueInstance.Spec.Config, kubeClient)
		})
		BeforeEach(func() {
			var err error
			ns, err = kubeClient.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "e2e-kueue-",
					Labels: map[string]string{
						testutils.OpenShiftManagedLabel: "true",
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			By(fmt.Sprintf("Created namespace %s", ns.Name))
			builder = testutils.NewTestResourceBuilder(ns.Name, testutils.DefaultLocalQueueName)
			Expect(testutils.CreateLocalQueue(kueueClient, ns.Name, testutils.DefaultLocalQueueName)).To(Succeed())
		})
		AfterEach(func() {
			By(fmt.Sprintf("Deleting namespace %s", ns.Name))
			err := kubeClient.CoreV1().Namespaces().Delete(context.TODO(), ns.Name, metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())
			testutils.WaitForAllPodsInNamespaceDeleted(context.TODO(), clients.GenericClient, ns)
		})

		It("should label and admit Pod and Job", func() {
			ctx := context.TODO()
			By("creating job without queue name")
			job := builder.NewJobWithoutQueue()
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(context.TODO(), job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(createdJob.Labels).To(HaveKeyWithValue(testutils.QueueLabel, testutils.DefaultLocalQueueName))
			verifyWorkloadCreated(kueueClient, ns.Name, string(createdJob.UID))

			By("creating pod without queue name")
			podWithLabel := builder.NewPodWithoutQueue()
			createdPod, err := kubeClient.CoreV1().Pods(ns.Name).Create(ctx, podWithLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(createdPod.Labels).To(HaveKeyWithValue(testutils.QueueLabel, testutils.DefaultLocalQueueName))
			verifyWorkloadCreated(kueueClient, ns.Name, string(createdPod.UID))
		})

		It("should allow other local queues in same namespace without interfering", func() {
			ctx := context.TODO()
			secondQueueName := "test-queue-2"

			By("creating job without queue name")
			job := builder.NewJobWithoutQueue()
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(context.TODO(), job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(createdJob.Labels).To(HaveKeyWithValue(testutils.QueueLabel, testutils.DefaultLocalQueueName))

			verifyWorkloadCreated(kueueClient, ns.Name, string(createdJob.UID))

			By("creating pod without queue name")
			pod := builder.NewPodWithoutQueue()
			createdPod, err := kubeClient.CoreV1().Pods(ns.Name).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(createdPod.Labels).To(HaveKeyWithValue(testutils.QueueLabel, testutils.DefaultLocalQueueName))

			verifyWorkloadCreated(kueueClient, ns.Name, string(createdPod.UID))

			Expect(testutils.CreateLocalQueue(kueueClient, ns.Name, secondQueueName)).To(Succeed())

			By("creating job in other local queue")
			job = builder.NewJobWithoutQueue()
			job.Labels = map[string]string{testutils.QueueLabel: secondQueueName}
			createdJob, err = kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(createdJob.Labels).To(HaveKeyWithValue(testutils.QueueLabel, secondQueueName))

			verifyWorkloadCreated(kueueClient, ns.Name, string(createdPod.UID))

			By("creating pod in other local queue")
			pod = builder.NewPodWithoutQueue()
			pod.Labels = map[string]string{testutils.QueueLabel: secondQueueName}
			createdPod, err = kubeClient.CoreV1().Pods(ns.Name).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(createdPod.Labels).To(HaveKeyWithValue(testutils.QueueLabel, secondQueueName))

			verifyWorkloadCreated(kueueClient, ns.Name, string(createdPod.UID))
		})
	})

})
