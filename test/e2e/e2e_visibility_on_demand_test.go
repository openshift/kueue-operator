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
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	flowcontrolclientv1 "k8s.io/client-go/kubernetes/typed/flowcontrol/v1"
	"k8s.io/utils/ptr"
	visibilityv1beta1 "sigs.k8s.io/kueue/apis/visibility/v1beta1"
	upstreamkueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"
)

const (
	priorityName = "kueue-visibility"
)

var _ = Describe("VisibilityOnDemand", Label("visibility-on-demand"), Ordered, func() {
	BeforeAll(func() {
		Expect(deployOperand()).To(Succeed(), "operand deployment should not fail")
	})
	AfterAll(func(ctx context.Context) {
		testutils.CleanUpKueueInstance(ctx, clients.KueueClient, "cluster", clients.KubeClient)
	})

	When("kueue.openshift.io/allow-nominal-concurrency-shares-update annotation is set to true", func() {
		labelKey := testutils.OpenShiftManagedLabel
		labelValue := "true"
		testQueue := "test-queue"

		var cleanupClusterQueue, cleanupLocalQueue, cleanupResourceFlavor func()
		var err error
		var ns *corev1.Namespace
		var priorityClient flowcontrolclientv1.PriorityLevelConfigurationInterface

		BeforeAll(func(ctx context.Context) {
			priorityClient = clients.KubeClient.FlowcontrolV1().PriorityLevelConfigurations()
			ns, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "e2e-kueue-visibility-on-demand-",
					Labels: map[string]string{
						labelKey: labelValue,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			cleanupClusterQueue, err = testutils.CreateClusterQueue(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred())

			cleanupLocalQueue, err = testutils.CreateLocalQueue(ctx, clients.UpstreamKueueClient, ns.Name, testQueue)
			Expect(err).NotTo(HaveOccurred())

			cleanupResourceFlavor, err = testutils.CreateResourceFlavor(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred())

			roleBinding := &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "read-pending-workloads", Namespace: ns.Name},
				RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "kueue-batch-user-role"},
				Subjects: []rbacv1.Subject{
					{Name: "default", APIGroup: "", Namespace: testutils.OperatorNamespace, Kind: rbacv1.ServiceAccountKind},
				},
			}
			_, err = kubeClient.RbacV1().RoleBindings(ns.Name).Create(ctx, roleBinding, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
		})
		AfterAll(func(ctx context.Context) {
			cleanupLocalQueue()
			deleteNamespace(ctx, ns)
			cleanupClusterQueue()
			cleanupResourceFlavor()
		})

		JustAfterEach(func(ctx context.Context) {
			testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 100)
		})

		It("should allow modification of the nominal concurrency shares to 0", func(ctx context.Context) {
			By("Modifying the PriorityLevelConfiguration with nominal concurrency shares set to 0")
			updateNominalConcurrencyShares(ctx, priorityClient, 0)

			By("Ensure the value of nominal concurrency shares is changed to 0")
			priority, err := priorityClient.Get(ctx, priorityName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(*priority.Spec.Limited.NominalConcurrencyShares).To(Equal(int32(0)))

			By("Try to access the pending workload")
			_, err = visibilityClient.LocalQueues(ns.Name).GetPendingWorkloadsSummary(ctx, testQueue, metav1.GetOptions{})
			Expect(apierrors.IsTooManyRequests(err)).To(BeTrue())
		})
		It("should allow modification of the nominal concurrency shares to 5", func(ctx context.Context) {
			By("Modifying the PriorityLevelConfiguration with nominal concurrency shares set to 5")
			updateNominalConcurrencyShares(ctx, priorityClient, 5)

			By("Try to access the pending workload")
			_, err = visibilityClient.LocalQueues(ns.Name).GetPendingWorkloadsSummary(ctx, testQueue, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Check is able to access the pending workload")
			_, err = visibilityClient.LocalQueues(ns.Name).GetPendingWorkloadsSummary(ctx, testQueue, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
		})
		It("should not allow modification of the nominal concurrency shares to 1", func(ctx context.Context) {
			By("Modifying the PriorityLevelConfiguration with nominal concurrency shares set to 1")
			updateNominalConcurrencyShares(ctx, priorityClient, 1)
			By("Wait to verify the value of nominal concurrency shares is changed back to the default")
			Eventually(func() int32 {
				priority, err := priorityClient.Get(ctx, priorityName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				return *priority.Spec.Limited.NominalConcurrencyShares
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Equal(int32(2)), "Nominal concurrency shares did not change back to the default")
		})
		It("should not allow modification of the nominal concurrency shares to 6", func(ctx context.Context) {
			By("Modifying the PriorityLevelConfiguration with nominal concurrency shares set to 6")
			updateNominalConcurrencyShares(ctx, priorityClient, 6)
			By("Wait to verify the value of nominal concurrency shares is changed back to the default")
			Eventually(func() int32 {
				priority, err := priorityClient.Get(ctx, priorityName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				return *priority.Spec.Limited.NominalConcurrencyShares
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Equal(int32(2)), "Nominal concurrency shares did not change back to the default")
		})
	})

	When("PendingWorkloads list should be checked for a ClusterQueue and LocalQueue", func() {
		var (
			labelKey   = testutils.OpenShiftManagedLabel
			labelValue = "true"
		)

		It("Should allow admin to access ClusterQueues, deny user access, and order pending workloads by priority", func(ctx context.Context) {
			var (
				clusterPendingWorkloadsA *visibilityv1beta1.PendingWorkloadsSummary
				clusterPendingWorkloadsB *visibilityv1beta1.PendingWorkloadsSummary
				err                      error
			)

			By("Creating Cluster Resources")
			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
			DeferCleanup(cleanupResourceFlavor)
			clusterQueueA, cleanupClusterQueueA, err := testutils.NewClusterQueue().WithGenerateName().WithCPU("2").WithMemory("1Gi").WithFlavorName(resourceFlavor.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue")
			DeferCleanup(cleanupClusterQueueA)
			clusterQueueB, cleanupClusterQueueB, err := testutils.NewClusterQueue().WithGenerateName().WithCPU("2").WithMemory("1Gi").WithFlavorName(resourceFlavor.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue")
			DeferCleanup(cleanupClusterQueueB)

			By("Creating Namespaces and LocalQueues")
			namespaceA, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "namespace-a-",
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
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue")
			DeferCleanup(cleanupLocalQueueA)

			namespaceB, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "namespace-b-",
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
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue")
			DeferCleanup(cleanupLocalQueueB)

			By("Creating Priority Classes")
			highPriorityClass, cleanupHighPriorityClass, err := createPriorityClass(ctx, 100, false, "High priority class")
			Expect(err).NotTo(HaveOccurred(), "Failed to create high priority class")
			DeferCleanup(cleanupHighPriorityClass)
			midPriorityClass, cleanupMidPriorityClass, err := createPriorityClass(ctx, 75, false, "Medium priority class")
			Expect(err).NotTo(HaveOccurred(), "Failed to create medium priority class")
			DeferCleanup(cleanupMidPriorityClass)
			lowPriorityClass, cleanupLowPriorityClass, err := createPriorityClass(ctx, 50, false, "Low priority class")
			Expect(err).NotTo(HaveOccurred(), "Failed to create low priority class")
			DeferCleanup(cleanupLowPriorityClass)

			By("Creating RBAC kueue-batch-admin-role for Visibility API")
			kueueTestSA, err := createServiceAccount(ctx, namespaceA.Name)
			Expect(err).NotTo(HaveOccurred(), "Failed to create service account")

			cleanupClusterRoleBinding, err := createClusterRoleBinding(ctx, kueueTestSA.Name, namespaceA.Name, "kueue-batch-admin-role")
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster role binding")
			DeferCleanup(cleanupClusterRoleBinding)

			By("Creating RBAC kueue-batch-user-role for Visibility API")
			kueueTestSAUser, err := createServiceAccount(ctx, namespaceA.Name)
			Expect(err).NotTo(HaveOccurred(), "Failed to create service account")

			cleanupClusterRoleBindingUser, err := createClusterRoleBinding(ctx, kueueTestSAUser.Name, namespaceA.Name, "kueue-batch-user-role")
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster role binding")
			DeferCleanup(cleanupClusterRoleBindingUser)

			By("Creating custom visibility client for ClusterQueue user")
			testUserVisibilityClient, err := testutils.GetVisibilityClient(fmt.Sprintf("system:serviceaccount:%s:%s", namespaceA.Name, kueueTestSA.Name))
			Expect(err).NotTo(HaveOccurred(), "Failed to create visibility client for system:serviceaccount:%s:%s", namespaceA.Name, kueueTestSA.Name)

			By("Creating testing data")
			cleanupJobBlockerA, jobBlockerA, err := createCustomJob(ctx, "job-blocker", namespaceA.Name, localQueueA.Name, highPriorityClass.Name, "2", "1Gi")
			Expect(err).NotTo(HaveOccurred(), "Failed to create blocker job")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespaceA.Name, string(jobBlockerA.UID))
			DeferCleanup(cleanupJobBlockerA)
			cleanupJobHighA, jobHighA, err := createCustomJob(ctx, "job-high-a", namespaceA.Name, localQueueA.Name, highPriorityClass.Name, "1", "512Mi")
			Expect(err).NotTo(HaveOccurred(), "Failed to create high priority job")
			DeferCleanup(cleanupJobHighA)
			cleanupJobMediumA, jobMediumA, err := createCustomJob(ctx, "job-medium-a", namespaceA.Name, localQueueA.Name, midPriorityClass.Name, "1", "512Mi")
			Expect(err).NotTo(HaveOccurred(), "Failed to create medium priority job")
			DeferCleanup(cleanupJobMediumA)
			cleanupJobLowA, jobLowA, err := createCustomJob(ctx, "job-low-a", namespaceA.Name, localQueueA.Name, lowPriorityClass.Name, "1", "512Mi")
			Expect(err).NotTo(HaveOccurred(), "Failed to create low priority job")
			DeferCleanup(cleanupJobLowA)
			cleanupJobBlockerB, jobBlockerB, err := createCustomJob(ctx, "job-blocker-b", namespaceB.Name, localQueueB.Name, highPriorityClass.Name, "2", "1Gi")
			Expect(err).NotTo(HaveOccurred(), "Failed to create blocker job")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespaceB.Name, string(jobBlockerB.UID))
			DeferCleanup(cleanupJobBlockerB)
			cleanupJobHighB, jobHighB, err := createCustomJob(ctx, "job-high-b", namespaceB.Name, localQueueB.Name, highPriorityClass.Name, "1", "512Mi")
			Expect(err).NotTo(HaveOccurred(), "Failed to create blocker job")
			DeferCleanup(cleanupJobHighB)

			By(fmt.Sprintf("Checking the pending workloads for cluster queue %s", clusterQueueA.Name))
			// Wait for job-blocker pod to be created
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(namespaceA.Name).List(ctx, metav1.ListOptions{})
				if err != nil {
					return err
				}
				for _, pod := range pods.Items {
					for _, owner := range pod.OwnerReferences {
						if owner.UID == jobBlockerA.UID {
							return nil
						}
					}
				}
				return fmt.Errorf("pod for job-blocker not found yet")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Blocker job pod was not created")
			Eventually(func() error {
				clusterPendingWorkloadsA, err = testUserVisibilityClient.ClusterQueues().GetPendingWorkloadsSummary(ctx, clusterQueueA.Name, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Failed to get pending workloads for ClusterQueue %s", clusterQueueA.Name)

			By("Verifying number of pending workloads and their priority ordering")
			Expect(clusterPendingWorkloadsA.Items).To(HaveLen(3), fmt.Sprintf("Expected 3 pending workloads on ClusterQueue %s", clusterQueueA.Name))
			Expect(clusterPendingWorkloadsA.Items[0].Priority).To(Equal(int32(100)), "First workload should have high priority (100)")
			Expect(clusterPendingWorkloadsA.Items[1].Priority).To(Equal(int32(75)), "Second workload should have medium priority (75)")
			Expect(clusterPendingWorkloadsA.Items[2].Priority).To(Equal(int32(50)), "Third workload should have low priority (50)")

			By(fmt.Sprintf("Checking the pending workloads for cluster queue %s", clusterQueueB.Name))
			// Wait for job-blocker-b pod to be created
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(namespaceB.Name).List(ctx, metav1.ListOptions{})
				if err != nil {
					return err
				}
				for _, pod := range pods.Items {
					for _, owner := range pod.OwnerReferences {
						if owner.UID == jobBlockerB.UID {
							return nil
						}
					}
				}
				return fmt.Errorf("pod for job-blocker-b not found yet")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Blocker job pod was not created")
			Eventually(func() error {
				clusterPendingWorkloadsB, err = testUserVisibilityClient.ClusterQueues().GetPendingWorkloadsSummary(ctx, clusterQueueB.Name, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Failed to get pending workloads for ClusterQueue %s", clusterQueueB.Name)

			By("Verifying number of pending workloads")
			Expect(clusterPendingWorkloadsB.Items).To(HaveLen(1), fmt.Sprintf("Expected 1 pending workload on ClusterQueue %s", clusterQueueB.Name))
			Expect(clusterPendingWorkloadsB.Items[0].Priority).To(Equal(int32(100)), "First workload should have high priority (100)")

			By("All workloads should have been created")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespaceA.Name, string(jobHighA.UID))
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespaceA.Name, string(jobMediumA.UID))
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespaceA.Name, string(jobLowA.UID))
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespaceB.Name, string(jobHighB.UID))

			By("Verifying pending workloads lists are empty")
			Eventually(func() error {
				clusterPendingWorkloadsA, err = testUserVisibilityClient.ClusterQueues().GetPendingWorkloadsSummary(ctx, clusterQueueA.Name, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Failed to get final pending workloads")
			Expect(clusterPendingWorkloadsA.Items).To(BeEmpty(), "Pending workloads list should be empty after workloads were executed")

			Eventually(func() error {
				clusterPendingWorkloadsB, err = testUserVisibilityClient.ClusterQueues().GetPendingWorkloadsSummary(ctx, clusterQueueB.Name, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Failed to get final pending workloads")
			Expect(clusterPendingWorkloadsB.Items).To(BeEmpty(), "Pending workloads list should be empty after workloads were executed")

			By("Verifying a unauthorized user cannot access the pending workloads")
			notAuthorizedVisibilityClient, err := testutils.GetVisibilityClient(
				fmt.Sprintf("system:serviceaccount:%s:%s", namespaceA.Name, "not-authorized-user"),
			)
			Expect(err).NotTo(HaveOccurred(), "Failed to create visibility client for not authorized user")
			_, err = notAuthorizedVisibilityClient.ClusterQueues().GetPendingWorkloadsSummary(ctx, clusterQueueA.Name, metav1.GetOptions{})
			Expect(err).To(HaveOccurred(), "Expected an error when not authorized user tries to access the pending workloads")
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "Expected a Forbidden error when not authorized user tries to access the pending workloads")

			By("Verifying user with kueue-batch-user-role cannot access ClusterQueue pending workloads")
			userVisibilityClient, err := testutils.GetVisibilityClient(
				fmt.Sprintf("system:serviceaccount:%s:%s", namespaceA.Name, kueueTestSAUser.Name),
			)
			Expect(err).NotTo(HaveOccurred(), "Failed to create visibility client for user with kueue-batch-user-role")
			_, err = userVisibilityClient.ClusterQueues().GetPendingWorkloadsSummary(ctx, clusterQueueA.Name, metav1.GetOptions{})
			Expect(err).To(HaveOccurred(), "Expected an error when user with kueue-batch-user-role tries to access ClusterQueue pending workloads")
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "Expected a Forbidden error when user with kueue-batch-user-role tries to access ClusterQueue pending workloads")

			By("Verifying all workloads have been completed")
			verifyWorkloadCompleted(clients.UpstreamKueueClient, namespaceA.Name, string(jobHighA.UID))
			verifyWorkloadCompleted(clients.UpstreamKueueClient, namespaceA.Name, string(jobMediumA.UID))
			verifyWorkloadCompleted(clients.UpstreamKueueClient, namespaceA.Name, string(jobLowA.UID))
			verifyWorkloadCompleted(clients.UpstreamKueueClient, namespaceB.Name, string(jobHighB.UID))
		})

		It("Should allow access to LocalQueues in bound namespaces and deny access to unbound namespaces", func(ctx context.Context) {
			var (
				localPendingWorkloadsA *visibilityv1beta1.PendingWorkloadsSummary
				localPendingWorkloadsB *visibilityv1beta1.PendingWorkloadsSummary
				err                    error
			)

			By("Creating Cluster Resources")
			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
			DeferCleanup(cleanupResourceFlavor)
			clusterQueue, cleanupClusterQueueA, err := testutils.NewClusterQueue().WithGenerateName().WithCPU("2").WithMemory("1Gi").WithFlavorName(resourceFlavor.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue")
			DeferCleanup(cleanupClusterQueueA)

			By("Creating namespace-a and LocalQueue-a")
			namespaceA, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "namespace-a-",
					Labels: map[string]string{
						labelKey: labelValue,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				deleteNamespace(ctx, namespaceA)
			})

			localQueueA, cleanupLocalQueueA, err := testutils.NewLocalQueue(namespaceA.Name, "local-queue-a").WithClusterQueue(clusterQueue.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue")
			DeferCleanup(cleanupLocalQueueA)

			By("Creating namespace-b and LocalQueue-b")
			namespaceB, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "namespace-b-",
					Labels: map[string]string{
						labelKey: labelValue,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				deleteNamespace(ctx, namespaceB)
			})

			localQueueB, cleanupLocalQueueB, err := testutils.NewLocalQueue(namespaceB.Name, "local-queue-b").WithClusterQueue(clusterQueue.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue")
			DeferCleanup(cleanupLocalQueueB)

			By("Creating RBAC kueue-batch-admin-role for Visibility API")
			kueueTestSAA, err := createServiceAccount(ctx, namespaceA.Name)
			Expect(err).NotTo(HaveOccurred(), "Failed to create service account")

			cleanupRoleBindingA, err := createRoleBinding(ctx, namespaceA.Name, kueueTestSAA.Name, "kueue-batch-user-role")
			Expect(err).NotTo(HaveOccurred(), "Failed to create role binding")
			DeferCleanup(cleanupRoleBindingA)

			kueueTestSAB, err := createServiceAccount(ctx, namespaceB.Name)
			Expect(err).NotTo(HaveOccurred(), "Failed to create service account")

			cleanupRoleBindingB, err := createRoleBinding(ctx, namespaceB.Name, kueueTestSAB.Name, "kueue-batch-user-role")
			Expect(err).NotTo(HaveOccurred(), "Failed to create role binding")
			DeferCleanup(cleanupRoleBindingB)

			By("Creating custom visibility client for ClusterQueue user")
			testUserVisibilityClientA, err := testutils.GetVisibilityClient(fmt.Sprintf("system:serviceaccount:%s:%s", namespaceA.Name, kueueTestSAA.Name))
			Expect(err).NotTo(HaveOccurred(), "Failed to create visibility client for system:serviceaccount:%s:%s", namespaceA.Name, kueueTestSAA.Name)
			testUserVisibilityClientB, err := testutils.GetVisibilityClient(fmt.Sprintf("system:serviceaccount:%s:%s", namespaceB.Name, kueueTestSAB.Name))
			Expect(err).NotTo(HaveOccurred(), "Failed to create visibility client for system:serviceaccount:%s:%s", namespaceB.Name, kueueTestSAB.Name)

			By("Creating Priority Classes")
			highPriorityClass, cleanupHighPriorityClass, err := createPriorityClass(ctx, 100, false, "High priority class")
			Expect(err).NotTo(HaveOccurred(), "Failed to create high priority class")
			DeferCleanup(cleanupHighPriorityClass)
			lowPriorityClass, cleanupLowPriorityClass, err := createPriorityClass(ctx, 50, false, "Low priority class")
			Expect(err).NotTo(HaveOccurred(), "Failed to create low priority class")
			DeferCleanup(cleanupLowPriorityClass)

			By("Creating testing data")
			cleanupJobBlockerA, jobBlockerA, err := createCustomJob(ctx, "job-blocker", namespaceA.Name, localQueueA.Name, highPriorityClass.Name, "2", "1Gi")
			Expect(err).NotTo(HaveOccurred(), "Failed to create blocker job")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespaceA.Name, string(jobBlockerA.UID))
			DeferCleanup(cleanupJobBlockerA)
			cleanupJobHighA, jobHighA, err := createCustomJob(ctx, "job-high-a", namespaceA.Name, localQueueA.Name, lowPriorityClass.Name, "1", "512Mi")
			Expect(err).NotTo(HaveOccurred(), "Failed to create high priority job")
			DeferCleanup(cleanupJobHighA)
			cleanupJobBlockerB, jobBlockerB, err := createCustomJob(ctx, "job-blocker-b", namespaceB.Name, localQueueB.Name, highPriorityClass.Name, "2", "1Gi")
			Expect(err).NotTo(HaveOccurred(), "Failed to create blocker job")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespaceB.Name, string(jobBlockerB.UID))
			DeferCleanup(cleanupJobBlockerB)
			cleanupJobLowB, jobLowB, err := createCustomJob(ctx, "job-low-b", namespaceB.Name, localQueueB.Name, lowPriorityClass.Name, "1", "512Mi")
			Expect(err).NotTo(HaveOccurred(), "Failed to create low priority job")
			DeferCleanup(cleanupJobLowB)

			By("Check if allowed users have access to LocalQueueA in bound namespaces")
			Eventually(func() error {
				localPendingWorkloadsA, err = testUserVisibilityClientA.LocalQueues(namespaceA.Name).GetPendingWorkloadsSummary(ctx, localQueueA.Name, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Failed to get pending workloads for LocalQueue %s", localQueueA.Name)

			Eventually(func() bool {
				_, err = testUserVisibilityClientB.LocalQueues(namespaceA.Name).GetPendingWorkloadsSummary(ctx, localQueueA.Name, metav1.GetOptions{})
				return err != nil && apierrors.IsForbidden(err)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "Expected a Forbidden error when user from namespaceB tries to access LocalQueue in namespaceA")

			By("Check if allowed users have access to LocalQueueB in bound namespaces")
			Eventually(func() error {
				localPendingWorkloadsB, err = testUserVisibilityClientB.LocalQueues(namespaceB.Name).GetPendingWorkloadsSummary(ctx, localQueueB.Name, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Failed to get pending workloads for LocalQueue %s", localQueueB.Name)

			Eventually(func() bool {
				_, err = testUserVisibilityClientA.LocalQueues(namespaceB.Name).GetPendingWorkloadsSummary(ctx, localQueueB.Name, metav1.GetOptions{})
				return err != nil && apierrors.IsForbidden(err)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "Expected a Forbidden error when user from namespaceA tries to access LocalQueue in namespaceB")

			By("All workloads should have been created")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespaceA.Name, string(jobHighA.UID))
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespaceB.Name, string(jobLowB.UID))

			By("Verifying pending workloads lists are empty")
			Eventually(func() error {
				localPendingWorkloadsA, err = testUserVisibilityClientA.LocalQueues(namespaceA.Name).GetPendingWorkloadsSummary(ctx, localQueueA.Name, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Failed to get final pending workloads")
			Expect(localPendingWorkloadsA.Items).To(BeEmpty(), "Pending workloads list should be empty after workloads were executed")

			Eventually(func() error {
				localPendingWorkloadsB, err = testUserVisibilityClientB.LocalQueues(namespaceB.Name).GetPendingWorkloadsSummary(ctx, localQueueB.Name, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Failed to get final pending workloads")
			Expect(localPendingWorkloadsB.Items).To(BeEmpty(), "Pending workloads list should be empty after workloads were executed")
		})
	})
})

// updateNominalConcurrencyShares updates the nominal concurrency shares of the priority level configuration
// and verifies the update is successful
func updateNominalConcurrencyShares(ctx context.Context, priorityClient flowcontrolclientv1.PriorityLevelConfigurationInterface, nominalConcurrencyShares int32) {
	priority, err := priorityClient.Get(ctx, priorityName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	priority.Annotations = map[string]string{
		"kueue.openshift.io/allow-nominal-concurrency-shares-update": "true",
	}
	priority.Spec.Limited.NominalConcurrencyShares = &nominalConcurrencyShares
	updatedPriority, err := priorityClient.Update(ctx, priority, metav1.UpdateOptions{})
	Expect(err).NotTo(HaveOccurred())
	Expect(updatedPriority.Spec.Limited.NominalConcurrencyShares).To(Equal(&nominalConcurrencyShares))
}

// createPriorityClass creates a PriorityClass with the specified value and description
// and returns the created object along with a cleanup function
func createPriorityClass(ctx context.Context, value int32, globalDefault bool, description string) (*schedulingv1.PriorityClass, func(), error) {
	priorityClass := &schedulingv1.PriorityClass{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "priority-class-",
		},
		Value:         value,
		GlobalDefault: globalDefault,
		Description:   description,
	}
	createdPriorityClass, err := kubeClient.SchedulingV1().PriorityClasses().Create(ctx, priorityClass, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	cleanup := func() {
		ctx := context.TODO()
		By(fmt.Sprintf("Deleting PriorityClass %s", createdPriorityClass.Name))
		err := kubeClient.SchedulingV1().PriorityClasses().Delete(ctx, createdPriorityClass.Name, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			_, err := kubeClient.SchedulingV1().PriorityClasses().Get(ctx, createdPriorityClass.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("priorityclass %s still exists: %w", createdPriorityClass.Name, err)
		}, testutils.DeletionTime, testutils.DeletionPoll).Should(Succeed(), fmt.Sprintf("PriorityClass %s was not cleaned up", createdPriorityClass.Name))
	}

	return createdPriorityClass, cleanup, nil
}

// createServiceAccount creates a ServiceAccount in the specified namespace and returns the created object
func createServiceAccount(ctx context.Context, namespace string) (*corev1.ServiceAccount, error) {
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "service-account-",
			Namespace:    namespace,
		},
	}
	createdServiceAccount, err := kubeClient.CoreV1().ServiceAccounts(namespace).Create(ctx, serviceAccount, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	return createdServiceAccount, nil
}

// createClusterRoleBinding creates a ClusterRoleBinding for the specified ServiceAccount and ClusterRole
// and returns a cleanup function
func createClusterRoleBinding(ctx context.Context, serviceAccountName, serviceAccountNamespace, clusterRoleName string) (func(), error) {
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "kueue-cluster-role-binding-",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      serviceAccountName,
				Namespace: serviceAccountNamespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     clusterRoleName,
		},
	}
	createdClusterRoleBinding, err := kubeClient.RbacV1().ClusterRoleBindings().Create(ctx, clusterRoleBinding, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	cleanup := func() {
		ctx := context.TODO()
		By(fmt.Sprintf("Deleting ClusterRoleBinding %s", createdClusterRoleBinding.Name))
		err := kubeClient.RbacV1().ClusterRoleBindings().Delete(ctx, createdClusterRoleBinding.Name, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			_, err := kubeClient.RbacV1().ClusterRoleBindings().Get(ctx, createdClusterRoleBinding.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("clusterrolebinding %s still exists: %w", createdClusterRoleBinding.Name, err)
		}, testutils.DeletionTime, testutils.DeletionPoll).Should(Succeed(), fmt.Sprintf("ClusterRoleBinding %s was not cleaned up", createdClusterRoleBinding.Name))
	}

	return cleanup, nil
}

// createRoleBinding creates a RoleBinding in the specified namespace for the given ServiceAccount and ClusterRole
// and returns a cleanup function
func createRoleBinding(ctx context.Context, namespace, serviceAccountName, clusterRoleName string) (func(), error) {
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "kueue-role-binding-",
			Namespace:    namespace,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      serviceAccountName,
				Namespace: namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     clusterRoleName,
		},
	}
	createdRoleBinding, err := kubeClient.RbacV1().RoleBindings(namespace).Create(ctx, roleBinding, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	cleanup := func() {
		ctx := context.TODO()
		By(fmt.Sprintf("Deleting RoleBinding %s in namespace %s", createdRoleBinding.Name, namespace))
		err := kubeClient.RbacV1().RoleBindings(namespace).Delete(ctx, createdRoleBinding.Name, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			_, err := kubeClient.RbacV1().RoleBindings(namespace).Get(ctx, createdRoleBinding.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("rolebinding %s still exists: %w", createdRoleBinding.Name, err)
		}, testutils.DeletionTime, testutils.DeletionPoll).Should(Succeed(), fmt.Sprintf("RoleBinding %s was not cleaned up", createdRoleBinding.Name))
	}

	return cleanup, nil
}

// createCustomJob creates a Job with the specified queue, priority class, and resource requirements
// and returns a cleanup function along with the created Job object
func createCustomJob(ctx context.Context, name, namespace, queueName, priorityClassName, cpu, memory string) (func(), *batchv1.Job, error) {
	cpuQuota := "1"
	memoryQuota := "1Gi"
	if cpu != "" {
		cpuQuota = cpu
	}
	if memory != "" {
		memoryQuota = memory
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				testutils.QueueLabel: queueName,
			},
		},
		Spec: batchv1.JobSpec{
			Parallelism: ptr.To[int32](1),
			Completions: ptr.To[int32](1),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:     corev1.RestartPolicyNever,
					PriorityClassName: priorityClassName,
					Containers: []corev1.Container{
						{
							Name:    "test-container",
							Image:   "busybox",
							Command: []string{"sh", "-c", "echo 'Hello Kueue'; sleep 40"},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse(cpuQuota),
									corev1.ResourceMemory: resource.MustParse(memoryQuota),
								},
							},
						},
					},
				},
			},
		},
	}
	createdJob, err := kubeClient.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	cleanup := func() {
		ctx := context.TODO()
		By(fmt.Sprintf("Deleting Job %s in namespace %s", name, namespace))
		propagationPolicy := metav1.DeletePropagationBackground
		err := kubeClient.BatchV1().Jobs(namespace).Delete(ctx, name, metav1.DeleteOptions{
			PropagationPolicy: &propagationPolicy,
		})
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			_, err := kubeClient.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("job %s still exists: %w", name, err)
		}, testutils.DeletionTime, testutils.DeletionPoll).Should(Succeed(), fmt.Sprintf("Job %s was not cleaned up", name))
	}

	return cleanup, createdJob, nil
}

// verifyWorkloadCompleted waits for a workload with the specified job UID to complete
// and asserts that the workload's Finished condition is set to True
func verifyWorkloadCompleted(kueueClient *upstreamkueueclient.Clientset, namespace, uid string) {
	Eventually(func() bool {
		// Find workload by job UID
		workloads, err := kueueClient.KueueV1beta1().Workloads(namespace).List(context.TODO(), metav1.ListOptions{
			LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", uid),
		})
		if err != nil || len(workloads.Items) == 0 {
			return false
		}

		workload := &workloads.Items[0]
		// Check if workload has Finished condition set to True
		for _, condition := range workload.Status.Conditions {
			if condition.Type == "Finished" && condition.Status == metav1.ConditionTrue {
				return true
			}
		}
		return false
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), fmt.Sprintf("Workload for job UID %s in namespace %s did not complete within timeout", uid, namespace))
}
