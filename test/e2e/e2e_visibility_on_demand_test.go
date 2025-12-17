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
	"net/http"

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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	flowcontrolclientv1 "k8s.io/client-go/kubernetes/typed/flowcontrol/v1"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	visibilityv1beta1 "sigs.k8s.io/kueue/apis/visibility/v1beta1"
	upstreamkueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"
)

const (
	priorityName   = "kueue-visibility"
	trueLabelValue = "true"
)

var _ = Describe("VisibilityOnDemand", Label("visibility-on-demand"), Ordered, func() {

	When("kueue.openshift.io/allow-nominal-concurrency-shares-update annotation is set to true", func() {
		labelKey := testutils.OpenShiftManagedLabel
		labelValue := trueLabelValue
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
			err := updateNominalConcurrencyShares(ctx, priorityClient, 0)
			Expect(err).NotTo(HaveOccurred())

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
			err := updateNominalConcurrencyShares(ctx, priorityClient, 5)
			Expect(err).NotTo(HaveOccurred())

			By("Try to access the pending workload")
			_, err = visibilityClient.LocalQueues(ns.Name).GetPendingWorkloadsSummary(ctx, testQueue, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Check is able to access the pending workload")
			_, err = visibilityClient.LocalQueues(ns.Name).GetPendingWorkloadsSummary(ctx, testQueue, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
		})
		It("should not allow modification of the nominal concurrency shares to 1", func(ctx context.Context) {
			By("Modifying the PriorityLevelConfiguration with nominal concurrency shares set to 1")
			err := updateNominalConcurrencyShares(ctx, priorityClient, 1)
			Expect(err).NotTo(HaveOccurred())

			By("Wait to verify the value of nominal concurrency shares is changed back to the default")
			Eventually(func() int32 {
				priority, err := priorityClient.Get(ctx, priorityName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				return *priority.Spec.Limited.NominalConcurrencyShares
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Equal(int32(2)), "Nominal concurrency shares did not change back to the default")
		})
		It("should not allow modification of the nominal concurrency shares to 6", func(ctx context.Context) {
			By("Modifying the PriorityLevelConfiguration with nominal concurrency shares set to 6")
			err := updateNominalConcurrencyShares(ctx, priorityClient, 6)
			Expect(err).NotTo(HaveOccurred())

			By("Wait to verify the value of nominal concurrency shares is changed back to the default")
			Eventually(func() int32 {
				priority, err := priorityClient.Get(ctx, priorityName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				return *priority.Spec.Limited.NominalConcurrencyShares
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Equal(int32(2)), "Nominal concurrency shares did not change back to the default")
		})

		It("should not allow modification of the nominal concurrency shares if it's not admin", func(ctx context.Context) {

			By("Creating Kubernetes client with impersonation for non-admin service account")
			cfg := *clients.RestConfig
			cfg.Impersonate.UserName = fmt.Sprintf("system:serviceaccount:%s:%s", ns.Name, "deployer")
			nonAdminKubeClient, err := kubernetes.NewForConfig(&cfg)
			Expect(err).NotTo(HaveOccurred(), "Failed to create Kubernetes client with impersonation")
			nonAdminPriorityClient := nonAdminKubeClient.FlowcontrolV1().PriorityLevelConfigurations()

			By("Attempting to modify PriorityLevelConfiguration with nominal concurrency shares set to 4")
			err = updateNominalConcurrencyShares(ctx, nonAdminPriorityClient, 4)
			Expect(err).To(HaveOccurred(), "Expected an error when non-admin tries to update PriorityLevelConfiguration")
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "Expected a Forbidden error when non-admin tries to update PriorityLevelConfiguration")
		})
	})

	When("PendingWorkloads list should be checked for a ClusterQueue and LocalQueue", func() {
		var (
			labelKey   = testutils.OpenShiftManagedLabel
			labelValue = trueLabelValue
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
			highPriorityClass, cleanupHighPriorityClass, err := createPriorityClass(ctx, 100, "High priority class")
			Expect(err).NotTo(HaveOccurred(), "Failed to create high priority class")
			DeferCleanup(cleanupHighPriorityClass)
			midPriorityClass, cleanupMidPriorityClass, err := createPriorityClass(ctx, 75, "Medium priority class")
			Expect(err).NotTo(HaveOccurred(), "Failed to create medium priority class")
			DeferCleanup(cleanupMidPriorityClass)
			lowPriorityClass, cleanupLowPriorityClass, err := createPriorityClass(ctx, 50, "Low priority class")
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

			Byf("Checking the pending workloads for cluster queue %s", clusterQueueA.Name)
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

			Byf("Checking the pending workloads for cluster queue %s", clusterQueueB.Name)
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
			highPriorityClass, cleanupHighPriorityClass, err := createPriorityClass(ctx, 100, "High priority class")
			Expect(err).NotTo(HaveOccurred(), "Failed to create high priority class")
			DeferCleanup(cleanupHighPriorityClass)
			lowPriorityClass, cleanupLowPriorityClass, err := createPriorityClass(ctx, 50, "Low priority class")
			Expect(err).NotTo(HaveOccurred(), "Failed to create low priority class")
			DeferCleanup(cleanupLowPriorityClass)

			By("Creating testing data")
			cleanupJobBlockerA, jobBlockerA, err := createCustomJob(ctx, "job-blocker", namespaceA.Name, localQueueA.Name, highPriorityClass.Name, "2", "1Gi")
			Expect(err).NotTo(HaveOccurred(), "Failed to create blocker job")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespaceA.Name, string(jobBlockerA.UID))
			DeferCleanup(cleanupJobBlockerA)
			cleanupJobLowA, jobHighA, err := createCustomJob(ctx, "job-high-a", namespaceA.Name, localQueueA.Name, lowPriorityClass.Name, "1", "512Mi")
			Expect(err).NotTo(HaveOccurred(), "Failed to create high priority job")
			DeferCleanup(cleanupJobLowA)
			cleanupJobBlockerB, jobBlockerB, err := createCustomJob(ctx, "job-blocker-b", namespaceB.Name, localQueueB.Name, highPriorityClass.Name, "2", "1Gi")
			Expect(err).NotTo(HaveOccurred(), "Failed to create blocker job")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespaceB.Name, string(jobBlockerB.UID))
			DeferCleanup(cleanupJobBlockerB)
			cleanupJobLowB, jobLowB, err := createCustomJob(ctx, "job-low-b", namespaceB.Name, localQueueB.Name, lowPriorityClass.Name, "1", "512Mi")
			Expect(err).NotTo(HaveOccurred(), "Failed to create low priority job")
			DeferCleanup(cleanupJobLowB)

			// Wait for all workloads to be created before checking pending status
			By("Waiting for job-high-a workload to be created")
			Eventually(func() error {
				workloads, err := clients.UpstreamKueueClient.KueueV1beta1().Workloads(namespaceA.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", jobHighA.UID),
				})
				if err != nil {
					return err
				}
				if len(workloads.Items) == 0 {
					return fmt.Errorf("workload for job-high-a not found yet")
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "job-high-a workload was not created")

			By("Waiting for job-low-b workload to be created")
			Eventually(func() error {
				workloads, err := clients.UpstreamKueueClient.KueueV1beta1().Workloads(namespaceB.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", jobLowB.UID),
				})
				if err != nil {
					return err
				}
				if len(workloads.Items) == 0 {
					return fmt.Errorf("workload for job-low-b not found yet")
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "job-low-b workload was not created")

			Byf("Checking the pending workloads for local queue %s in namespace %s", localQueueA.Name, namespaceA.Name)
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
				localPendingWorkloadsA, err = testUserVisibilityClientA.LocalQueues(namespaceA.Name).GetPendingWorkloadsSummary(ctx, localQueueA.Name, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Failed to get pending workloads for LocalQueue %s", localQueueA.Name)

			By("Verifying pending workloads for LocalQueue A")
			Expect(localPendingWorkloadsA.Items).To(HaveLen(1), fmt.Sprintf("Expected 1 pending workload on LocalQueue %s", localQueueA.Name))
			Expect(localPendingWorkloadsA.Items[0].Priority).To(Equal(int32(50)), "Pending workload should have low priority (50)")

			Byf("Checking the pending workloads for local queue %s in namespace %s", localQueueB.Name, namespaceB.Name)
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
				localPendingWorkloadsB, err = testUserVisibilityClientB.LocalQueues(namespaceB.Name).GetPendingWorkloadsSummary(ctx, localQueueB.Name, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Failed to get pending workloads for LocalQueue %s", localQueueB.Name)

			By("Verifying pending workloads for LocalQueue B")
			Expect(localPendingWorkloadsB.Items).To(HaveLen(1), fmt.Sprintf("Expected 1 pending workload on LocalQueue %s", localQueueB.Name))
			Expect(localPendingWorkloadsB.Items[0].Priority).To(Equal(int32(50)), "Pending workload should have low priority (50)")

			By("Check if not allowed users does not have access to LocalQueueA")
			Eventually(func() bool {
				_, err = testUserVisibilityClientB.LocalQueues(namespaceA.Name).GetPendingWorkloadsSummary(ctx, localQueueA.Name, metav1.GetOptions{})
				return err != nil && apierrors.IsForbidden(err)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "Expected a Forbidden error when user from namespaceB tries to access LocalQueue in namespaceA")

			By("Check if not allowed users does not have access to LocalQueueB")
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

	When("PendingWorkloads Endpoints should be checked", func() {
		labelKey := testutils.OpenShiftManagedLabel
		labelValue := trueLabelValue

		It("Should use the correct PriorityLevelConfiguration and FlowSchema for ClusterQueue and LocalQueue", func(ctx context.Context) {
			// capturedHeaders stores HTTP response headers captured by headerCaptureRoundTripper during API calls.
			var capturedHeaders = make(http.Header)

			By("Creating Cluster Resources")
			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
			DeferCleanup(cleanupResourceFlavor)
			clusterQueue, cleanupClusterQueue, err := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue")
			DeferCleanup(cleanupClusterQueue)

			By("Creating Namespaces and LocalQueues")
			namespace, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "namespace-",
					Labels: map[string]string{
						labelKey: labelValue,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				deleteNamespace(ctx, namespace)
			})

			localQueue, cleanupLocalQueue, err := testutils.NewLocalQueue(namespace.Name, "local-queue").WithClusterQueue(clusterQueue.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue")
			DeferCleanup(cleanupLocalQueue)

			By("Creating RBAC kueue-batch-admin-role for Visibility API")
			cleanupClusterRoleBinding, err := createClusterRoleBinding(ctx, "default", namespace.Name, "kueue-batch-admin-role")
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster role binding")
			DeferCleanup(cleanupClusterRoleBinding)

			By("Creating custom visibility client for ClusterQueue user")
			testUserVisibilityClient, err := testutils.GetVisibilityClient(fmt.Sprintf("system:serviceaccount:%s:%s", namespace.Name, "default"))
			Expect(err).NotTo(HaveOccurred(), "Failed to create visibility client for system:serviceaccount:%s:%s", namespace.Name, "default")

			By("Getting the PriorityLevelConfiguration and FlowSchema")
			priorityLevelConfiguration, err := kubeClient.FlowcontrolV1().PriorityLevelConfigurations().Get(ctx, "kueue-visibility", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			flowSchema, err := kubeClient.FlowcontrolV1().FlowSchemas().Get(ctx, "visibility", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Setting up HTTP header capture")
			// Set up header capture: wrap the RESTClient's HTTP transport to intercept response headers
			restClient := testUserVisibilityClient.RESTClient().(*rest.RESTClient)
			originalClient := restClient.Client

			restClient.Client = &http.Client{
				Transport: &headerCaptureRoundTripper{
					original: originalClient.Transport,
					headers:  &capturedHeaders,
				},
			}
			DeferCleanup(func() {
				restClient.Client = originalClient
			})

			By("Getting the pending workloads for the ClusterQueue API response")
			Eventually(func() error {
				_, err = testUserVisibilityClient.ClusterQueues().GetPendingWorkloadsSummary(ctx, clusterQueue.Name, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Failed to get pending workloads for ClusterQueue %s", clusterQueue.Name)

			By("Extracting response headers from API call")
			priorityLevelUIDHeaderClusterQueue := capturedHeaders.Get("X-Kubernetes-Pf-Prioritylevel-Uid")
			flowSchemaUIDHeaderClusterQueue := capturedHeaders.Get("X-Kubernetes-Pf-Flowschema-Uid")

			By("Checking that response headers match expected PriorityLevelConfiguration and FlowSchema UIDs for ClusterQueue")
			Expect(priorityLevelUIDHeaderClusterQueue).To(Equal(string(priorityLevelConfiguration.UID)),
				"X-Kubernetes-Pf-Prioritylevel-Uid header does not match \"kueue-visibility\" PriorityLevelConfiguration UID for ClusterQueue")
			Expect(flowSchemaUIDHeaderClusterQueue).To(Equal(string(flowSchema.UID)),
				"X-Kubernetes-Pf-Flowschema-Uid header does not match \"visibility\" FlowSchema UID for ClusterQueue")

			By("Getting the pending workloads for the LocalQueue API response")
			Eventually(func() error {
				_, err = testUserVisibilityClient.LocalQueues(namespace.Name).GetPendingWorkloadsSummary(ctx, localQueue.Name, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Failed to get pending workloads for LocalQueue %s", localQueue.Name)

			By("Extracting response headers from API call")
			priorityLevelUIDHeaderLocalQueue := capturedHeaders.Get("X-Kubernetes-Pf-Prioritylevel-Uid")
			flowSchemaUIDHeaderLocalQueue := capturedHeaders.Get("X-Kubernetes-Pf-Flowschema-Uid")

			By("Checking that response headers match expected PriorityLevelConfiguration and FlowSchema UIDs for LocalQueue")
			Expect(priorityLevelUIDHeaderLocalQueue).To(Equal(string(priorityLevelConfiguration.UID)),
				"X-Kubernetes-Pf-Prioritylevel-Uid header does not match \"kueue-visibility\" PriorityLevelConfiguration UID for LocalQueue")
			Expect(flowSchemaUIDHeaderLocalQueue).To(Equal(string(flowSchema.UID)),
				"X-Kubernetes-Pf-Flowschema-Uid header does not match \"visibility\" FlowSchema UID for LocalQueue")

		})
	})

	When("PendingWorkloads list should be checked for LWS workloads", func() {
		var (
			labelKey   = testutils.OpenShiftManagedLabel
			labelValue = trueLabelValue
		)

		It("Should show pending LWS workloads ordered by priority and admit them sequentially based on resource availability", func(ctx context.Context) {
			var (
				clusterPendingWorkloads *visibilityv1beta1.PendingWorkloadsSummary
				localPendingWorkloadsA  *visibilityv1beta1.PendingWorkloadsSummary
				err                     error
			)

			lwsGVR := schema.GroupVersionResource{
				Group:    "leaderworkerset.x-k8s.io",
				Version:  "v1",
				Resource: "leaderworkersets",
			}

			By("Creating Cluster Resources")
			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
			DeferCleanup(cleanupResourceFlavor)
			clusterQueue, cleanupClusterQueue, err := testutils.NewClusterQueue().WithGenerateName().WithCPU("400m").WithMemory("512Mi").WithFlavorName(resourceFlavor.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue")
			DeferCleanup(cleanupClusterQueue)

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

			localQueueA, cleanupLocalQueueA, err := testutils.NewLocalQueue(namespaceA.Name, "local-queue-a").WithClusterQueue(clusterQueue.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
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

			localQueueB, cleanupLocalQueueB, err := testutils.NewLocalQueue(namespaceB.Name, "local-queue-b").WithClusterQueue(clusterQueue.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue")
			DeferCleanup(cleanupLocalQueueB)

			By("Creating Priority Classes")
			highPriorityClass, cleanupHighPriorityClass, err := createPriorityClass(ctx, 100, "High priority class")
			Expect(err).NotTo(HaveOccurred(), "Failed to create high priority class")
			DeferCleanup(cleanupHighPriorityClass)
			midPriorityClass, cleanupMidPriorityClass, err := createPriorityClass(ctx, 75, "Medium priority class")
			Expect(err).NotTo(HaveOccurred(), "Failed to create medium priority class")
			DeferCleanup(cleanupMidPriorityClass)
			lowPriorityClass, cleanupLowPriorityClass, err := createPriorityClass(ctx, 50, "Low priority class")
			Expect(err).NotTo(HaveOccurred(), "Failed to create low priority class")
			DeferCleanup(cleanupLowPriorityClass)

			By("Creating RBAC kueue-batch-admin-role for Visibility API")
			kueueTestSA, err := createServiceAccount(ctx, namespaceA.Name)
			Expect(err).NotTo(HaveOccurred(), "Failed to create service account")

			cleanupClusterRoleBinding, err := createClusterRoleBinding(ctx, kueueTestSA.Name, namespaceA.Name, "kueue-batch-admin-role")
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster role binding")
			DeferCleanup(cleanupClusterRoleBinding)

			By("Creating RBAC kueue-batch-user-role for Visibility API")
			kueueTestSAUserA, err := createServiceAccount(ctx, namespaceA.Name)
			Expect(err).NotTo(HaveOccurred(), "Failed to create service account")

			cleanupRoleBindingA, err := createRoleBinding(ctx, namespaceA.Name, kueueTestSAUserA.Name, "kueue-batch-user-role")
			Expect(err).NotTo(HaveOccurred(), "Failed to create role binding")
			DeferCleanup(cleanupRoleBindingA)

			kueueTestSAUserB, err := createServiceAccount(ctx, namespaceB.Name)
			Expect(err).NotTo(HaveOccurred(), "Failed to create service account")

			cleanupRoleBindingB, err := createRoleBinding(ctx, namespaceB.Name, kueueTestSAUserB.Name, "kueue-batch-user-role")
			Expect(err).NotTo(HaveOccurred(), "Failed to create role binding")
			DeferCleanup(cleanupRoleBindingB)

			By("Creating custom visibility client for ClusterQueue access")
			testUserVisibilityClient, err := testutils.GetVisibilityClient(fmt.Sprintf("system:serviceaccount:%s:%s", namespaceA.Name, kueueTestSA.Name))
			Expect(err).NotTo(HaveOccurred(), "Failed to create visibility client for system:serviceaccount:%s:%s", namespaceA.Name, kueueTestSA.Name)

			By("Creating custom visibility client for LocalQueue A access")
			userVisibilityClientA, err := testutils.GetVisibilityClient(fmt.Sprintf("system:serviceaccount:%s:%s", namespaceA.Name, kueueTestSAUserA.Name))
			Expect(err).NotTo(HaveOccurred(), "Failed to create visibility client for LocalQueue A access")

			By("Creating custom visibility client for LocalQueue B access")
			userVisibilityClientB, err := testutils.GetVisibilityClient(fmt.Sprintf("system:serviceaccount:%s:%s", namespaceB.Name, kueueTestSAUserB.Name))
			Expect(err).NotTo(HaveOccurred(), "Failed to create visibility client for LocalQueue B access")

			By("Creating blocker LWS in namespace A")
			builderA := testutils.NewTestResourceBuilder(namespaceA.Name, localQueueA.Name)
			blockerLWS := builderA.NewLeaderWorkerSet()
			blockerLWS.SetName("lws-blocker-a")
			testutils.AddQueueLabelToLWS(blockerLWS, localQueueA.Name)
			testutils.SetLWSPriority(blockerLWS, highPriorityClass.Name)
			testutils.SetLWSSize(blockerLWS, 3)
			createdBlockerLWS, err := clients.DynamicClient.Resource(lwsGVR).Namespace(namespaceA.Name).Create(ctx, blockerLWS, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create blocker LWS")
			DeferCleanup(func() {
				_ = clients.DynamicClient.Resource(lwsGVR).Namespace(namespaceA.Name).Delete(ctx, createdBlockerLWS.GetName(), metav1.DeleteOptions{})
			})
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespaceA.Name, string(createdBlockerLWS.GetUID()))

			By("Waiting for blocker LWS pods to be created")
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(namespaceA.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("leaderworkerset.sigs.k8s.io/name=%s", createdBlockerLWS.GetName()),
				})
				if err != nil {
					return err
				}
				if len(pods.Items) < 3 {
					return fmt.Errorf("blocker LWS pods not created yet, found %d pods (expected 3)", len(pods.Items))
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Blocker LWS pods were not created")

			By("Creating pending LWS workloads after blocker pods are ready")
			builderB := testutils.NewTestResourceBuilder(namespaceB.Name, localQueueB.Name)
			highLWS := builderB.NewLeaderWorkerSet()
			highLWS.SetName("lws-high-b")
			testutils.AddQueueLabelToLWS(highLWS, localQueueB.Name)
			testutils.SetLWSPriority(highLWS, highPriorityClass.Name)
			createdHighLWS, err := clients.DynamicClient.Resource(lwsGVR).Namespace(namespaceB.Name).Create(ctx, highLWS, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create high priority LWS")
			DeferCleanup(func() {
				_ = clients.DynamicClient.Resource(lwsGVR).Namespace(namespaceB.Name).Delete(ctx, createdHighLWS.GetName(), metav1.DeleteOptions{})
			})

			mediumLWS := builderA.NewLeaderWorkerSet()
			mediumLWS.SetName("lws-medium-a")
			testutils.AddQueueLabelToLWS(mediumLWS, localQueueA.Name)
			testutils.SetLWSPriority(mediumLWS, midPriorityClass.Name)
			createdMediumLWS, err := clients.DynamicClient.Resource(lwsGVR).Namespace(namespaceA.Name).Create(ctx, mediumLWS, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create medium priority LWS")
			DeferCleanup(func() {
				_ = clients.DynamicClient.Resource(lwsGVR).Namespace(namespaceA.Name).Delete(ctx, createdMediumLWS.GetName(), metav1.DeleteOptions{})
			})

			lowLWS := builderA.NewLeaderWorkerSet()
			lowLWS.SetName("lws-low-a")
			testutils.AddQueueLabelToLWS(lowLWS, localQueueA.Name)
			testutils.SetLWSPriority(lowLWS, lowPriorityClass.Name)
			createdLowLWS, err := clients.DynamicClient.Resource(lwsGVR).Namespace(namespaceA.Name).Create(ctx, lowLWS, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create low priority LWS")
			DeferCleanup(func() {
				_ = clients.DynamicClient.Resource(lwsGVR).Namespace(namespaceA.Name).Delete(ctx, createdLowLWS.GetName(), metav1.DeleteOptions{})
			})

			By("Verifying all pending workloads are created")
			verifyWorkloadCreatedNotAdmitted(clients.UpstreamKueueClient, namespaceB.Name, createdHighLWS.GetUID())
			verifyWorkloadCreatedNotAdmitted(clients.UpstreamKueueClient, namespaceA.Name, createdMediumLWS.GetUID())
			verifyWorkloadCreatedNotAdmitted(clients.UpstreamKueueClient, namespaceA.Name, createdLowLWS.GetUID())

			Byf("Checking the pending workloads for cluster queue %s", clusterQueue.Name)

			Eventually(func() error {
				clusterPendingWorkloads, err = testUserVisibilityClient.ClusterQueues().GetPendingWorkloadsSummary(ctx, clusterQueue.Name, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Failed to get pending workloads for ClusterQueue %s", clusterQueue.Name)

			By("Verifying number of pending workloads and their priority ordering for ClusterQueue")
			Expect(clusterPendingWorkloads.Items).To(HaveLen(3), fmt.Sprintf("Expected 3 pending workloads on ClusterQueue %s", clusterQueue.Name))
			Expect(clusterPendingWorkloads.Items[0].Priority).To(Equal(int32(100)), "First workload should have high priority (100)")
			Expect(clusterPendingWorkloads.Items[1].Priority).To(Equal(int32(75)), "Second workload should have medium priority (75)")
			Expect(clusterPendingWorkloads.Items[2].Priority).To(Equal(int32(50)), "Third workload should have low priority (50)")

			Byf("Checking the pending workloads for local queue %s in namespace %s", localQueueA.Name, namespaceA.Name)
			Eventually(func() error {
				localPendingWorkloadsA, err = userVisibilityClientA.LocalQueues(namespaceA.Name).GetPendingWorkloadsSummary(ctx, localQueueA.Name, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Failed to get pending workloads for LocalQueue %s", localQueueA.Name)

			By("Verifying pending workloads for LocalQueue A")
			Expect(localPendingWorkloadsA.Items).To(HaveLen(2), fmt.Sprintf("Expected 2 pending workloads on LocalQueue %s", localQueueA.Name))
			Expect(localPendingWorkloadsA.Items[0].Priority).To(Equal(int32(75)), "First workload should have medium priority (75)")
			Expect(localPendingWorkloadsA.Items[1].Priority).To(Equal(int32(50)), "Second workload should have low priority (50)")

			Byf("Checking the pending workloads for local queue %s in namespace %s", localQueueB.Name, namespaceB.Name)
			var localPendingWorkloadsB *visibilityv1beta1.PendingWorkloadsSummary
			Eventually(func() error {
				localPendingWorkloadsB, err = userVisibilityClientB.LocalQueues(namespaceB.Name).GetPendingWorkloadsSummary(ctx, localQueueB.Name, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Failed to get pending workloads for LocalQueue %s", localQueueB.Name)

			By("Verifying pending workloads for LocalQueue B")
			Expect(localPendingWorkloadsB.Items).To(HaveLen(1), fmt.Sprintf("Expected 1 pending workload on LocalQueue %s", localQueueB.Name))
			Expect(localPendingWorkloadsB.Items[0].Priority).To(Equal(int32(100)), "Pending workload should have high priority (100)")

			By("Verifying RBAC: LocalQueue user A cannot query ClusterQueue")
			_, err = userVisibilityClientA.ClusterQueues().GetPendingWorkloadsSummary(ctx, clusterQueue.Name, metav1.GetOptions{})
			Expect(err).To(HaveOccurred(), "LocalQueue user A should not be able to query ClusterQueue")
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "Error should be Forbidden (403)")

			By("Verifying RBAC: LocalQueue user A cannot query LocalQueue B")
			_, err = userVisibilityClientA.LocalQueues(namespaceB.Name).GetPendingWorkloadsSummary(ctx, localQueueB.Name, metav1.GetOptions{})
			Expect(err).To(HaveOccurred(), "LocalQueue user A should not be able to query LocalQueue B")
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "Error should be Forbidden (403)")

			By("Deleting blocker LWS to free resources")
			err = clients.DynamicClient.Resource(lwsGVR).Namespace(namespaceA.Name).Delete(ctx, createdBlockerLWS.GetName(), metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to delete blocker LWS")

			By("Waiting for blocker LWS deletion and resource release")
			Eventually(func() error {
				_, err := clients.DynamicClient.Resource(lwsGVR).Namespace(namespaceA.Name).Get(ctx, createdBlockerLWS.GetName(), metav1.GetOptions{})
				if apierrors.IsNotFound(err) {
					return nil
				}
				return fmt.Errorf("blocker LWS still exists")
			}, testutils.DeletionTime, testutils.DeletionPoll).Should(Succeed(), "Blocker LWS was not deleted")

			By("Waiting for blocker LWS pods to be deleted")
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(namespaceA.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("leaderworkerset.sigs.k8s.io/name=%s", createdBlockerLWS.GetName()),
				})
				if err != nil {
					return err
				}
				if len(pods.Items) > 0 {
					return fmt.Errorf("blocker LWS pods still exist, found %d pods", len(pods.Items))
				}
				return nil
			}, testutils.DeletionTime, testutils.DeletionPoll).Should(Succeed(), "Blocker LWS pods were not deleted")

			By("Verifying high priority workload is admitted")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespaceB.Name, string(createdHighLWS.GetUID()))

			By("Verifying high priority LWS pods are running after admission")
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(namespaceB.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("leaderworkerset.sigs.k8s.io/name=%s", createdHighLWS.GetName()),
				})
				if err != nil {
					return err
				}
				if len(pods.Items) < 2 {
					return fmt.Errorf("high priority LWS pods not found, found %d pods", len(pods.Items))
				}
				for _, pod := range pods.Items {
					if pod.Status.Phase != corev1.PodRunning {
						return fmt.Errorf("pod %s is not in Running state, current phase: %s", pod.Name, pod.Status.Phase)
					}
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "High priority LWS pods should be running after admission")

			By("Verifying medium priority workload is admitted")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespaceA.Name, string(createdMediumLWS.GetUID()))

			By("Verifying medium priority LWS pods are running after admission")
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(namespaceA.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("leaderworkerset.sigs.k8s.io/name=%s", createdMediumLWS.GetName()),
				})
				if err != nil {
					return err
				}
				if len(pods.Items) < 2 {
					return fmt.Errorf("medium priority LWS pods not found, found %d pods", len(pods.Items))
				}
				for _, pod := range pods.Items {
					if pod.Status.Phase != corev1.PodRunning {
						return fmt.Errorf("pod %s is not in Running state, current phase: %s", pod.Name, pod.Status.Phase)
					}
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Medium priority LWS pods should be running after admission")

			By("Verifying low priority LWS is still pending")
			Eventually(func() error {
				clusterPendingWorkloads, err = testUserVisibilityClient.ClusterQueues().GetPendingWorkloadsSummary(ctx, clusterQueue.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				if len(clusterPendingWorkloads.Items) != 1 {
					return fmt.Errorf("expected 1 pending workload, found %d", len(clusterPendingWorkloads.Items))
				}
				if clusterPendingWorkloads.Items[0].Priority != 50 {
					return fmt.Errorf("expected low priority workload, found priority %d", clusterPendingWorkloads.Items[0].Priority)
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Low priority LWS should still be pending")

			By("Deleting medium priority LWS to free resources for low priority")
			err = clients.DynamicClient.Resource(lwsGVR).Namespace(namespaceA.Name).Delete(ctx, createdMediumLWS.GetName(), metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to delete medium priority LWS")

			By("Waiting for medium priority LWS deletion")
			Eventually(func() error {
				_, err := clients.DynamicClient.Resource(lwsGVR).Namespace(namespaceA.Name).Get(ctx, createdMediumLWS.GetName(), metav1.GetOptions{})
				if apierrors.IsNotFound(err) {
					return nil
				}
				return fmt.Errorf("medium priority LWS still exists")
			}, testutils.DeletionTime, testutils.DeletionPoll).Should(Succeed(), "Medium priority LWS was not deleted")

			By("Waiting for medium priority LWS pods to be deleted")
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(namespaceA.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("leaderworkerset.sigs.k8s.io/name=%s", createdMediumLWS.GetName()),
				})
				if err != nil {
					return err
				}
				if len(pods.Items) > 0 {
					return fmt.Errorf("medium priority LWS pods still exist, found %d pods", len(pods.Items))
				}
				return nil
			}, testutils.DeletionTime, testutils.DeletionPoll).Should(Succeed(), "Medium priority LWS pods were not deleted")

			By("Verifying low priority workload is admitted")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespaceA.Name, string(createdLowLWS.GetUID()))

			By("Verifying low priority LWS pods are running after admission")
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(namespaceA.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("leaderworkerset.sigs.k8s.io/name=%s", createdLowLWS.GetName()),
				})
				if err != nil {
					return err
				}
				if len(pods.Items) < 2 {
					return fmt.Errorf("low priority LWS pods not found, found %d pods", len(pods.Items))
				}
				for _, pod := range pods.Items {
					if pod.Status.Phase != corev1.PodRunning {
						return fmt.Errorf("pod %s is not in Running state, current phase: %s", pod.Name, pod.Status.Phase)
					}
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Low priority LWS pods should be running after admission")

			By("Verifying pending workloads list is empty")
			Eventually(func() error {
				clusterPendingWorkloads, err = testUserVisibilityClient.ClusterQueues().GetPendingWorkloadsSummary(ctx, clusterQueue.Name, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Failed to get final pending workloads")
			Expect(clusterPendingWorkloads.Items).To(BeEmpty(), "Pending workloads list should be empty after all workloads were admitted")
		})
	})
})

// updateNominalConcurrencyShares updates the nominal concurrency shares of the priority level configuration
// and verifies the update is successful
func updateNominalConcurrencyShares(ctx context.Context, priorityClient flowcontrolclientv1.PriorityLevelConfigurationInterface, nominalConcurrencyShares int32) error {
	priority, err := priorityClient.Get(ctx, priorityName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	priority.Annotations = map[string]string{
		"kueue.openshift.io/allow-nominal-concurrency-shares-update": trueLabelValue,
	}
	priority.Spec.Limited.NominalConcurrencyShares = &nominalConcurrencyShares
	updatedPriority, err := priorityClient.Update(ctx, priority, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	Expect(updatedPriority.Spec.Limited.NominalConcurrencyShares).To(Equal(&nominalConcurrencyShares))
	return nil
}

// createPriorityClass creates a PriorityClass with the specified value and description
// and returns the created object along with a cleanup function
func createPriorityClass(ctx context.Context, value int32, description string) (*schedulingv1.PriorityClass, func(), error) {
	priorityClass := &schedulingv1.PriorityClass{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "priority-class-",
		},
		Value:         value,
		GlobalDefault: false,
		Description:   description,
	}
	createdPriorityClass, err := kubeClient.SchedulingV1().PriorityClasses().Create(ctx, priorityClass, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	cleanup := func() {
		ctx := context.TODO()
		Byf("Deleting PriorityClass %s", createdPriorityClass.Name)
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
		Byf("Deleting ClusterRoleBinding %s", createdClusterRoleBinding.Name)
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
		Byf("Deleting RoleBinding %s in namespace %s", createdRoleBinding.Name, namespace)
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
							Image:   "quay.io/prometheus/busybox:latest",
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
		Byf("Deleting Job %s in namespace %s", name, namespace)
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

// headerCaptureRoundTripper wraps an http.RoundTripper to capture response headers
type headerCaptureRoundTripper struct {
	original http.RoundTripper
	headers  *http.Header
}

// RoundTrip executes the request using the original transport and captures response headers
func (rt *headerCaptureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := rt.original.RoundTrip(req)
	if err == nil && resp != nil {
		// Copy response headers to the captured headers map
		for key, values := range resp.Header {
			(*rt.headers)[key] = values
		}
	}
	return resp, err
}

// Byf is a helper function that wraps By(fmt.Sprintf(...)) to reduce verbosity
func Byf(format string, args ...interface{}) {
	By(fmt.Sprintf(format, args...))
}

// verifyWorkloadCreatedNotAdmitted verifies that a workload is created for a LeaderWorkerSet
// by checking owner references, but does not verify admission status.
func verifyWorkloadCreatedNotAdmitted(kueueClient *upstreamkueueclient.Clientset, namespace string, uid types.UID) {
	ctx := context.TODO()
	By(fmt.Sprintf("verifying workload is created for LeaderWorkerSet in namespace %s", namespace))
	Eventually(func() error {
		workloads, err := kueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}

		for _, wl := range workloads.Items {
			for _, ownerRef := range wl.OwnerReferences {
				if ownerRef.UID == uid {
					return nil
				}
			}
		}
		return fmt.Errorf("no workload found for LeaderWorkerSet")
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Workload should be created for LeaderWorkerSet")
}
