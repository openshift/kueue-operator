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
	//"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	flowcontrolclientv1 "k8s.io/client-go/kubernetes/typed/flowcontrol/v1"
)

const (
	priorityName = "kueue-visibility"
)

var _ = Describe("VisibilityOnDemand", Label("visibility-on-demand"), Ordered, func() {
	BeforeAll(func() {
		Expect(deployOperand()).To(Succeed(), "operand deployment should not fail")
	})
	AfterAll(func(ctx context.Context) {
		testutils.CleanUpKueueInstance(ctx, clients.KueueClient, "cluster")
	})

	When("kueue.openshift.io/allow-nominal-concurrency-shares-update annotation is set to true", func() {
		labelKey := "kueue.openshift.io/managed"
		labelValue := "true"
		testQueue := "test-queue"

		var cleanupClusterQueue, cleanupLocalQueue, cleanupResourceFlavor func()
		var err error
		var ns *corev1.Namespace
		var priorityClient flowcontrolclientv1.PriorityLevelConfigurationInterface

		BeforeAll(func(ctx context.Context) {
			priorityClient = clients.KubeClient.FlowcontrolV1().PriorityLevelConfigurations()
			ns, err = kubeClient.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "e2e-kueue-visibility-on-demand-",
					Labels: map[string]string{
						labelKey: labelValue,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			cleanupClusterQueue, err = testutils.CreateClusterQueue(clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred())

			cleanupLocalQueue, err = testutils.CreateLocalQueue(clients.UpstreamKueueClient, ns.Name, testQueue)
			Expect(err).NotTo(HaveOccurred())

			cleanupResourceFlavor, err = testutils.CreateResourceFlavor(clients.UpstreamKueueClient)
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
