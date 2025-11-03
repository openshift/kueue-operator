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

var (
	ns                     *corev1.Namespace
	kueueClient            *upstreamkueueclient.Clientset
	builder                *testutils.TestResourceBuilder
	clusterQueueCleanup    func()
	resourceFlavorCleanup  func()
	defaultLocalQueueClean func()
)

var _ = Describe("LocalQueueDefaulting", Label("local-queue-default"), Ordered, func() {
	BeforeAll(func() {
		Expect(deployOperand()).To(Succeed(), "operand deployment should not fail")
	})
	AfterAll(func(ctx context.Context) {
		testutils.CleanUpKueueInstance(ctx, clients.KueueClient, "cluster", clients.KubeClient)
	})

	When("labelPolicy=None and LocalQueue default in a managed namespace", func() {
		var initialKueueInstance *kueueoperatorv1.Kueue

		BeforeAll(func(ctx context.Context) {
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
			initialKueueInstance = kueueInstance.DeepCopy()
			kueueInstance.Spec.Config.WorkloadManagement.LabelPolicy = kueueoperatorv1.LabelPolicyNone
			applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)
			createClusterQueueAndResourceFlavor()
		})

		AfterAll(func(ctx context.Context) {
			deleteClusterQueueAndResourceFlavor(ctx, kueueClient)
			applyKueueConfig(ctx, initialKueueInstance.Spec.Config, kubeClient)
		})

		BeforeEach(func(ctx context.Context) {
			namespaceLabel := map[string]string{
				testutils.OpenShiftManagedLabel: "true",
			}
			createNamespaceAndLocalQueueDefault(ctx, namespaceLabel)
		})

		AfterEach(func(ctx context.Context) {
			deleteNamespace(ctx, ns)
		})

		JustAfterEach(func(ctx context.Context) {
			testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 100)
		})

		It("should label and admit Pod and Job", func(ctx context.Context) {
			By("creating job without queue name")
			job := builder.NewJobWithoutQueue()
			By(fmt.Sprintf("namespace with labels: %s\n", ns.Name))
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
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

		It("should allow other local queues in same namespace without interfering", func(ctx context.Context) {
			secondQueueName := "test-queue-2"

			By("creating job without queue name")
			job := builder.NewJobWithoutQueue()
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(createdJob.Labels).To(HaveKeyWithValue(testutils.QueueLabel, testutils.DefaultLocalQueueName))

			verifyWorkloadCreated(kueueClient, ns.Name, string(createdJob.UID))

			By("creating pod without queue name")
			pod := builder.NewPodWithoutQueue()
			createdPod, err := kubeClient.CoreV1().Pods(ns.Name).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(createdPod.Labels).To(HaveKeyWithValue(testutils.QueueLabel, testutils.DefaultLocalQueueName))

			verifyWorkloadCreated(kueueClient, ns.Name, string(createdPod.UID))

			cleanupSecondQueue, err := testutils.CreateLocalQueue(kueueClient, ns.Name, secondQueueName)
			Expect(err).NotTo(HaveOccurred())
			defer cleanupSecondQueue()

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

		It("should allow to label pod and job with default localqueue after they're created", func(ctx context.Context) {
			// Job creation when there is no LocalQueue Default
			By("Creating a new job without localQueue")
			err := kueueClient.KueueV1beta1().LocalQueues(ns.Name).Delete(ctx, testutils.DefaultLocalQueueName, metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())
			jobWithoutQueue := builder.NewJobWithoutQueue()
			createdJobWithoutQueue, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, jobWithoutQueue, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() bool {
				return testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJobWithoutQueue.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "Job not in 'Suspended' condition")
			Expect(createdJobWithoutQueue.Labels).NotTo(HaveKey(testutils.QueueLabel))

			// Pod creation when there is no LocalQueue Default
			By("Creating a new pod without localQueue")
			podWithoutQueue := builder.NewPodWithoutQueue()
			createdPodWithoutQueue, err := kubeClient.CoreV1().Pods(ns.Name).Create(ctx, podWithoutQueue, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() bool {
				return testutils.IsPodScheduled(ctx, kubeClient, ns.Name, createdPodWithoutQueue.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeFalse(), "Pod in 'Scheduling' condition")
			Expect(createdPodWithoutQueue.Labels).NotTo(HaveKey(testutils.QueueLabel))

			// Creation of LocalQueue Default with new Job
			By("Creating localQueue Default")
			defaultLocalQueueClean, err = testutils.CreateLocalQueue(kueueClient, ns.Name, testutils.DefaultLocalQueueName)
			Expect(err).NotTo(HaveOccurred())
			job := builder.NewJobWithoutQueue()
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() bool {
				job, err := kubeClient.BatchV1().Jobs(ns.Name).Get(ctx, createdJob.Name, metav1.GetOptions{})
				if err != nil {
					return false
				}
				_, ok := job.Labels[testutils.QueueLabel]
				return ok && job.Labels[testutils.QueueLabel] == testutils.DefaultLocalQueueName
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "Job did not get labeled with queue name")
			verifyWorkloadCreated(kueueClient, ns.Name, string(createdJob.UID))

			// Checking that pod and job did not get labeled
			By("Checking that Job and Pod were not automatically labeled")
			updatedJobWithoutQueue, err := kubeClient.BatchV1().Jobs(ns.Name).Get(ctx, createdJobWithoutQueue.Name, metav1.GetOptions{})
			Expect(updatedJobWithoutQueue.Labels).NotTo(HaveKey(testutils.QueueLabel))
			Expect(err).NotTo(HaveOccurred())
			updatedPodWithoutQueue, err := kubeClient.CoreV1().Pods(ns.Name).Get(ctx, createdPodWithoutQueue.Name, metav1.GetOptions{})
			Expect(updatedPodWithoutQueue.Labels).NotTo(HaveKey(testutils.QueueLabel))
			Expect(err).NotTo(HaveOccurred())

			// Adding LocalQueue Default label to Pod
			By(fmt.Sprintf("Adding localQueue Default label to Pod scheduled: %s", updatedPodWithoutQueue.Name))
			err = testutils.AddLabelAndPatch(ctx, kubeClient, ns.Name, updatedPodWithoutQueue.Name, "pod")
			Expect(err).NotTo(HaveOccurred())
			podPatched, err := kubeClient.CoreV1().Pods(ns.Name).Get(ctx, updatedPodWithoutQueue.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() bool {
				return testutils.IsPodScheduled(ctx, kubeClient, ns.Name, createdPodWithoutQueue.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "Pod not in 'Scheduling' condition")
			verifyWorkloadCreated(kueueClient, ns.Name, string(podPatched.UID))

			// Adding LocalQueue Default label to Job
			By(fmt.Sprintf("Adding localQueue Default label to Job suspended: %s", updatedJobWithoutQueue.Name))
			err = testutils.AddLabelAndPatch(ctx, kubeClient, ns.Name, updatedJobWithoutQueue.Name, "job")
			Expect(err).NotTo(HaveOccurred())
			jobPatched, err := kubeClient.BatchV1().Jobs(ns.Name).Get(ctx, updatedJobWithoutQueue.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() bool {
				return testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJobWithoutQueue.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeFalse(), "Job in 'Suspended' condition")
			verifyWorkloadCreated(kueueClient, ns.Name, string(jobPatched.UID))
		})
	})

	When("labelPolicy is not defined and default LocalQueue is in a managed namespace", func() {
		BeforeEach(func(ctx context.Context) {
			// Initial configuration - creating clusterQueue, resourceFlavor, namespace and localqueue
			createClusterQueueAndResourceFlavor()
			namespaceLabel := map[string]string{
				testutils.OpenShiftManagedLabel: "true",
			}
			createNamespaceAndLocalQueueDefault(ctx, namespaceLabel)
		})
		AfterEach(func(ctx context.Context) {
			// Clean Up - resources deprovision
			deleteNamespace(ctx, ns)
			deleteClusterQueueAndResourceFlavor(ctx, kueueClient)
		})
		It("should label and admit Job", func(ctx context.Context) {
			By("Creating job without queue name")
			// Job creation and validation
			jobWithoutQueue := builder.NewJobWithoutQueue()
			createdJobWithoutQueue, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, jobWithoutQueue, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() bool {
				return testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJobWithoutQueue.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeFalse(), "Job in 'Suspended' condition")
			Expect(createdJobWithoutQueue.Labels).To(HaveKeyWithValue(testutils.QueueLabel, testutils.DefaultLocalQueueName))
			verifyWorkloadCreated(kueueClient, ns.Name, string(createdJobWithoutQueue.UID))
		})
	})

	When("labelPolicy is not defined and default LocalQueue is in an unmanaged namespace", func() {
		BeforeEach(func(ctx context.Context) {
			// Initial configuration - creating clusterQueue, resourceFlavor, namespace and localqueue
			createClusterQueueAndResourceFlavor()
			createNamespaceAndLocalQueueDefault(ctx, nil)
		})
		AfterEach(func(ctx context.Context) {
			// Clean Up - resources deprovision
			deleteNamespace(ctx, ns)
			deleteClusterQueueAndResourceFlavor(ctx, kueueClient)
		})
		It("should label and admit Pod", func(ctx context.Context) {
			By("creating pod without queue name")
			// Pod creation and validation
			podWithoutQueue := builder.NewPodWithoutQueue()
			createdPodWithoutQueue, err := kubeClient.CoreV1().Pods(ns.Name).Create(ctx, podWithoutQueue, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(createdPodWithoutQueue.Labels).NotTo(HaveKey(testutils.QueueLabel))
			Eventually(func() bool {
				pod, err := kubeClient.CoreV1().Pods(ns.Name).Get(ctx, createdPodWithoutQueue.Name, metav1.GetOptions{})
				if err != nil {
					return false
				}
				return pod.Status.Phase == corev1.PodRunning
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "Pod did not reach expected state (Successfully Scheduled and currently Running)")
		})
	})
})

func deleteClusterQueueAndResourceFlavor(ctx context.Context, kueueClient *upstreamkueueclient.Clientset) {
	By("deleting cluster queue")
	if clusterQueueCleanup != nil {
		clusterQueueCleanup()
		clusterQueueCleanup = nil
	} else {
		err := kueueClient.KueueV1beta1().ClusterQueues().Delete(ctx, "test-clusterqueue", metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			_, err := kueueClient.KueueV1beta1().ClusterQueues().Get(ctx, "test-clusterqueue", metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("ClusterQueue test-clusterqueue still exists")
		}, testutils.DeletionTime, testutils.DeletionPoll).Should(Succeed(), "ClusterQueue was not cleaned up properly")
	}
	By("deleting resource flavor")
	if resourceFlavorCleanup != nil {
		resourceFlavorCleanup()
		resourceFlavorCleanup = nil
	} else {
		err := kueueClient.KueueV1beta1().ResourceFlavors().Delete(ctx, "default", metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			_, err := kueueClient.KueueV1beta1().ResourceFlavors().Get(ctx, "default", metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("ResourceFlavor default still exists")
		}, testutils.DeletionTime, testutils.DeletionPoll).Should(Succeed(), "ResourceFlavor was not cleaned up properly")
	}
}

func deleteNamespace(ctx context.Context, namespace *corev1.Namespace) {
	if defaultLocalQueueClean != nil {
		defaultLocalQueueClean()
		defaultLocalQueueClean = nil
	}
	By(fmt.Sprintf("Deleting namespace %s", namespace.Name))
	err := kubeClient.CoreV1().Namespaces().Delete(ctx, namespace.Name, metav1.DeleteOptions{})
	Expect(err).NotTo(HaveOccurred())
	testutils.WaitForAllPodsInNamespaceDeleted(ctx, clients.GenericClient, namespace)
}

func createClusterQueueAndResourceFlavor() {
	kueueClient = clients.UpstreamKueueClient
	if clusterQueueCleanup != nil {
		clusterQueueCleanup()
		clusterQueueCleanup = nil
	}
	if resourceFlavorCleanup != nil {
		resourceFlavorCleanup()
		resourceFlavorCleanup = nil
	}
	var err error
	clusterQueueCleanup, err = testutils.CreateClusterQueue(kueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue")
	resourceFlavorCleanup, err = testutils.CreateResourceFlavor(kueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
}

func createNamespaceAndLocalQueueDefault(ctx context.Context, labels map[string]string) {
	var err error
	if defaultLocalQueueClean != nil {
		defaultLocalQueueClean()
		defaultLocalQueueClean = nil
	}
	namespaceSpec := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "e2e-kueue-",
		},
	}
	if labels != nil {
		namespaceSpec.ObjectMeta.Labels = labels
	}
	ns, err = kubeClient.CoreV1().Namespaces().Create(ctx, namespaceSpec, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred())
	By(fmt.Sprintf("Created namespace %s", ns.Name))
	builder = testutils.NewTestResourceBuilder(ns.Name, testutils.DefaultLocalQueueName)
	defaultLocalQueueClean, err = testutils.CreateLocalQueue(kueueClient, ns.Name, testutils.DefaultLocalQueueName)
	Expect(err).NotTo(HaveOccurred())
}
