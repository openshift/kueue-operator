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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	upstreamkueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"
)

var _ = Describe("Scheduling Gate", Label("scheduling-gate"), Ordered, func() {
	var (
		testNamespace         = "scheduling-gate-test"
		nonExistentQueue      = "non-existent-queue"
		labelKey              = "kueue.openshift.io/managed"
		kueueGate             = "kueue.x-k8s.io/admission"
		labelValue            = "true"
		kueueClient           *upstreamkueueclient.Clientset
		builder               *testutils.TestResourceBuilder
		cleanupClusterQueue   func()
		cleanupResourceFlavor func()
	)

	JustAfterEach(func(ctx context.Context) {
		testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 500)
	})

	BeforeAll(func(ctx context.Context) {
		kueueClient = clients.UpstreamKueueClient
		builder = testutils.NewTestResourceBuilder(testNamespace, nonExistentQueue)
		var err error

		By("Updating Kueue configuration to only include Deployment, StatefulSet, and LeaderWorkerSet")
		// Update the config to only include Deployment, StatefulSet, and LeaderWorkerSet
		newConfig := ssv1.KueueConfiguration{
			Integrations: ssv1.Integrations{
				Frameworks: []ssv1.KueueIntegration{
					ssv1.KueueIntegrationDeployment,
					ssv1.KueueIntegrationStatefulSet,
					ssv1.KueueIntegrationLeaderWorkerSet,
				},
			},
			WorkloadManagement: ssv1.WorkloadManagement{
				LabelPolicy: ssv1.LabelPolicyQueueName,
			},
		}
		applyKueueConfig(ctx, newConfig, kubeClient)

		// Create namespace with managed label
		By(fmt.Sprintf("Creating namespace %s with managed label", testNamespace))
		_, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespace,
				Labels: map[string]string{
					labelKey: labelValue,
				},
			},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Create ClusterQueue and ResourceFlavor (but NOT a LocalQueue in our test namespace)
		cleanupClusterQueue, err = testutils.CreateClusterQueue(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())

		cleanupResourceFlavor, err = testutils.CreateResourceFlavor(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())

		// Note: We intentionally do NOT create a LocalQueue in testNamespace
		// This means workloads will be stuck in SchedulingGated state
		klog.Infof("Setup complete - no LocalQueue created in namespace %s", testNamespace)
	})

	AfterAll(func(ctx context.Context) {
		By("Cleaning up test resources")

		// Delete namespace
		deleteNamespaceForSchedulingGateTest(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespace,
			},
		})

		if cleanupClusterQueue != nil {
			cleanupClusterQueue()
		}
		if cleanupResourceFlavor != nil {
			cleanupResourceFlavor()
		}

		// Restore the default Kueue configuration
		By("Restoring default Kueue configuration")
		defaultConfig := ssv1.KueueConfiguration{
			Integrations: ssv1.Integrations{
				Frameworks: []ssv1.KueueIntegration{
					ssv1.KueueIntegrationBatchJob,
					ssv1.KueueIntegrationPod,
					ssv1.KueueIntegrationDeployment,
					ssv1.KueueIntegrationStatefulSet,
					ssv1.KueueIntegrationJobSet,
					ssv1.KueueIntegrationLeaderWorkerSet,
				},
			},
		}
		applyKueueConfig(ctx, defaultConfig, kubeClient)
	})

	When("workloads are submitted to a non-existent LocalQueue", func() {
		It("should verify Kueue is configured with only Deployment, StatefulSet, and LeaderWorkerSet integrations", func(ctx context.Context) {
			By("Verifying kueue-manager-config ConfigMap contains only expected integrations")
			Eventually(func() error {
				configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
				if err != nil {
					return err
				}

				configData, ok := configMap.Data["controller_manager_config.yaml"]
				Expect(ok).To(BeTrue(), "controller_manager_config.yaml key not found in ConfigMap")

				// Check that our integrations are present
				Expect(configData).To(ContainSubstring("deployment"), "Deployment integration should be present")
				Expect(configData).To(ContainSubstring("statefulset"), "StatefulSet integration should be present")
				Expect(configData).To(ContainSubstring("leaderworkerset.x-k8s.io/leaderworkerset"), "LeaderWorkerSet integration should be present")

				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
		})

		It("should keep Deployment pods scheduling gated when submitted to non-existent queue", func(ctx context.Context) {
			By("Creating deployment with queue label pointing to non-existent queue")
			deploy := builder.NewDeployment()
			createdDeploy, err := kubeClient.AppsV1().Deployments(testNamespace).Create(ctx, deploy, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpObject(ctx, genericClient, createdDeploy)

			By("Waiting for deployment pods to be created")
			var deploymentPods []corev1.Pod
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(testNamespace).List(ctx, metav1.ListOptions{
					LabelSelector: "app=test-deployment",
				})
				if err != nil {
					return err
				}
				if len(pods.Items) == 0 {
					return fmt.Errorf("no pods found for deployment")
				}
				deploymentPods = pods.Items
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying deployment pods have scheduling gates")
			for _, pod := range deploymentPods {
				Eventually(func() error {
					currentPod, err := kubeClient.CoreV1().Pods(testNamespace).Get(ctx, pod.Name, metav1.GetOptions{})
					if err != nil {
						return err
					}

					if len(currentPod.Spec.SchedulingGates) == 0 {
						return fmt.Errorf("pod %s does not have scheduling gates", pod.Name)
					}

					// Verify the scheduling gate is from Kueue
					hasKueueGate := false
					for _, gate := range currentPod.Spec.SchedulingGates {
						if gate.Name == kueueGate {
							hasKueueGate = true
							break
						}
					}
					if !hasKueueGate {
						return fmt.Errorf("pod %s does not have kueue scheduling gate", pod.Name)
					}

					klog.Infof("Deployment pod %s has scheduling gate as expected", pod.Name)
					return nil
				}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Pod %s should have scheduling gate", pod.Name)
			}

			By("Verifying pods remain scheduling gated (not scheduled)")
			Consistently(func() bool {
				for _, pod := range deploymentPods {
					currentPod, err := kubeClient.CoreV1().Pods(testNamespace).Get(ctx, pod.Name, metav1.GetOptions{})
					if err != nil {
						return false
					}
					if len(currentPod.Spec.SchedulingGates) == 0 {
						return false
					}
				}
				return true
			}, "30s", "5s").Should(BeTrue(), "Pods should remain scheduling gated")
		})

		It("should keep StatefulSet pods scheduling gated when submitted to non-existent queue", func(ctx context.Context) {
			By("Creating statefulset with queue label pointing to non-existent queue")
			ss := builder.NewStatefulSet()
			createdSS, err := kubeClient.AppsV1().StatefulSets(testNamespace).Create(ctx, ss, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpObject(ctx, genericClient, createdSS)

			By("Waiting for statefulset pods to be created")
			var ssPods []corev1.Pod
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(testNamespace).List(ctx, metav1.ListOptions{
					LabelSelector: "app=test-statefulset",
				})
				if err != nil {
					return err
				}
				if len(pods.Items) == 0 {
					return fmt.Errorf("no pods found for statefulset")
				}
				ssPods = pods.Items
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying statefulset pods have scheduling gates")
			for _, pod := range ssPods {
				Eventually(func() error {
					currentPod, err := kubeClient.CoreV1().Pods(testNamespace).Get(ctx, pod.Name, metav1.GetOptions{})
					if err != nil {
						return err
					}

					if len(currentPod.Spec.SchedulingGates) == 0 {
						return fmt.Errorf("pod %s does not have scheduling gates", pod.Name)
					}

					// Verify the scheduling gate is from Kueue
					hasKueueGate := false
					for _, gate := range currentPod.Spec.SchedulingGates {
						if gate.Name == kueueGate {
							hasKueueGate = true
							break
						}
					}
					if !hasKueueGate {
						return fmt.Errorf("pod %s does not have kueue scheduling gate", pod.Name)
					}

					klog.Infof("StatefulSet pod %s has scheduling gate as expected", pod.Name)
					return nil
				}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Pod %s should have scheduling gate", pod.Name)
			}

			By("Verifying pods remain scheduling gated (not scheduled)")
			Consistently(func() bool {
				for _, pod := range ssPods {
					currentPod, err := kubeClient.CoreV1().Pods(testNamespace).Get(ctx, pod.Name, metav1.GetOptions{})
					if err != nil {
						return false
					}
					if len(currentPod.Spec.SchedulingGates) == 0 {
						return false
					}
				}
				return true
			}, "30s", "5s").Should(BeTrue(), "Pods should remain scheduling gated")
		})

		It("should keep LeaderWorkerSet pods scheduling gated when submitted to non-existent queue", func(ctx context.Context) {
			By("Creating LeaderWorkerSet with queue label pointing to non-existent queue")
			lws := builder.NewLeaderWorkerSet(testutils.LeaderWorkerSetOptions{
				QueueName:         nonExistentQueue,
				PriorityClassName: "",
				Size:              2,
			})
			Expect(genericClient.Create(ctx, lws)).To(Succeed(), "Failed to create LeaderWorkerSet")
			defer testutils.CleanUpObject(ctx, genericClient, lws)

			By("Waiting for LeaderWorkerSet pods to be created")
			var lwsPods []corev1.Pod
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(testNamespace).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("leaderworkerset.sigs.k8s.io/name=%s", lws.Name),
				})
				if err != nil {
					return err
				}
				if len(pods.Items) == 0 {
					return fmt.Errorf("no pods found for LeaderWorkerSet")
				}
				lwsPods = pods.Items
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying LeaderWorkerSet pods have scheduling gates")
			for _, pod := range lwsPods {
				Eventually(func() error {
					currentPod, err := kubeClient.CoreV1().Pods(testNamespace).Get(ctx, pod.Name, metav1.GetOptions{})
					if err != nil {
						return err
					}

					if len(currentPod.Spec.SchedulingGates) == 0 {
						return fmt.Errorf("pod %s does not have scheduling gates", pod.Name)
					}

					// Verify the scheduling gate is from Kueue
					hasKueueGate := false
					for _, gate := range currentPod.Spec.SchedulingGates {
						if gate.Name == kueueGate {
							hasKueueGate = true
							break
						}
					}
					if !hasKueueGate {
						return fmt.Errorf("pod %s does not have kueue scheduling gate", pod.Name)
					}

					klog.Infof("LeaderWorkerSet pod %s has scheduling gate as expected", pod.Name)
					return nil
				}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Pod %s should have scheduling gate", pod.Name)
			}

			By("Verifying pods remain scheduling gated (not scheduled)")
			Consistently(func() bool {
				for _, pod := range lwsPods {
					currentPod, err := kubeClient.CoreV1().Pods(testNamespace).Get(ctx, pod.Name, metav1.GetOptions{})
					if err != nil {
						return false
					}
					if len(currentPod.Spec.SchedulingGates) == 0 {
						return false
					}
				}
				return true
			}, "30s", "5s").Should(BeTrue(), "Pods should remain scheduling gated")
		})
	})
})

func deleteNamespaceForSchedulingGateTest(ctx context.Context, namespace *corev1.Namespace) {
	By(fmt.Sprintf("Deleting namespace %s", namespace.Name))
	err := kubeClient.CoreV1().Namespaces().Delete(ctx, namespace.Name, metav1.DeleteOptions{})
	Expect(err).NotTo(HaveOccurred())
	testutils.WaitForAllPodsInNamespaceDeleted(ctx, clients.GenericClient, namespace)
}
