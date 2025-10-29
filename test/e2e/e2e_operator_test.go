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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ssv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	kueueclient "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned"
	ssscheme "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned/scheme"
	"github.com/openshift/kueue-operator/test/e2e/bindata"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	upstreamkueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"

	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/klog/v2"
	//+kubebuilder:scaffold:imports
)

func findOperatorPods(namespace string, list *corev1.PodList) []*corev1.Pod {
	pods := make([]*corev1.Pod, 0)
	for i := range list.Items {
		pod := &list.Items[i]
		if strings.HasPrefix(pod.Name, namespace+"-") {
			pods = append(pods, pod)
		}
	}
	return pods
}

func findKueuePods(list *corev1.PodList) []*corev1.Pod {
	pods := make([]*corev1.Pod, 0)
	for i := range list.Items {
		pod := &list.Items[i]
		if strings.HasPrefix(pod.Name, "kueue-controller-manager") {
			pods = append(pods, pod)
		}
	}
	return pods
}

var _ = Describe("Kueue Operator", Label("operator"), Serial, Ordered, func() {
	When("installs", func() {
		It("operator pods should be ready", func() {
			Eventually(func() error {
				ctx := context.TODO()
				podItems, err := kubeClient.CoreV1().Pods(testutils.OperatorNamespace).List(ctx, metav1.ListOptions{})
				if err != nil {
					klog.Errorf("Unable to list pods: %v", err)
					return nil
				}
				for _, pod := range podItems.Items {
					if !strings.HasPrefix(pod.Name, testutils.OperatorNamespace+"-") {
						continue
					}
					klog.Infof("Checking pod: %v, phase: %v, deletionTS: %v\n", pod.Name, pod.Status.Phase, pod.GetDeletionTimestamp())
					if pod.Status.Phase == corev1.PodRunning && pod.GetDeletionTimestamp() == nil && pod.Status.ContainerStatuses[0].Ready {
						return nil
					}
				}
				return fmt.Errorf("pod is not ready")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "operator pod failed to be ready")
		})
		It("kueue pods should be ready", func() {
			Expect(deployOperand()).To(Succeed(), "operand deployment should not fail")
		})
	})

	When("installs", func() {
		It("should set ReadyReplicas in operator status and handle degraded condition", func() {
			ctx := context.TODO()
			Eventually(func() error {
				kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("Unable to fetch Kueue instance: %v", err)
				}

				// Check ReadyReplicas is set correctly
				if kueueInstance.Status.ReadyReplicas == 0 {
					// If ReadyReplicas is 0, check if the operator properly reports degraded condition.
					for _, condition := range kueueInstance.Status.Conditions {
						if condition.Type == "Degraded" && condition.Status == "True" {
							if strings.Contains(condition.Message, "No replicas ready") {
								return nil
							}
						}
					}
					return fmt.Errorf("operator status ReadyReplicas is 0 but Degraded condition is not True or message does not match expected pattern")
				}

				// If ReadyReplicas is non-zero, verify it matches deployment status.
				deployment, err := kubeClient.AppsV1().Deployments(testutils.OperatorNamespace).Get(ctx, "kueue-controller-manager", metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("Unable to fetch deployment: %v", err)
				}

				if kueueInstance.Status.ReadyReplicas != deployment.Status.ReadyReplicas {
					return fmt.Errorf("operator status ReadyReplicas (%d) does not match deployment ReadyReplicas (%d)",
						kueueInstance.Status.ReadyReplicas, deployment.Status.ReadyReplicas)
				}

				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "ReadyReplicas not properly set or degraded condition not handled")
		})

		It("kueue operator deployment should contain priority class", func() {
			ctx := context.TODO()
			Eventually(func() error {
				operatorDeployment, err := kubeClient.AppsV1().Deployments(testutils.OperatorNamespace).Get(ctx, "openshift-kueue-operator", metav1.GetOptions{})
				if err != nil {
					return err
				}
				if operatorDeployment.Spec.Template.Spec.PriorityClassName != "system-cluster-critical" {
					return fmt.Errorf("Operator deployment does not have priority class system-cluster-critical")
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Unexpected priority class")
		})

		It("kueue deployment should contain priority class and have no resource limits set", func() {
			ctx := context.TODO()

			Eventually(func() error {
				managerDeployment, err := kubeClient.AppsV1().Deployments(testutils.OperatorNamespace).Get(ctx, "kueue-controller-manager", metav1.GetOptions{})
				if err != nil {
					return err
				}
				if managerDeployment.Spec.Template.Spec.PriorityClassName != "system-cluster-critical" {
					return fmt.Errorf("kueue-controller-manager does not have priority class system-cluster-critical")
				}
				if len(managerDeployment.Spec.Template.Spec.Containers[0].Resources.Limits) > 0 {
					return fmt.Errorf("kueue-controller-manager shouldn't set resources limits")
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Unexpected kueue-controller-manager spec")
		})

		It("Verifying that no v1alpha Kueue CRDs are installed", func() {
			Eventually(func() error {
				ctx := context.TODO()
				crdList, err := clients.APIExtClient.CustomResourceDefinitions().List(ctx, metav1.ListOptions{})
				if err != nil {
					klog.Errorf("Failed to list CRDs: %v", err)
					return err
				}

				var kueueCRDs []apiextensionsv1.CustomResourceDefinition

				for _, crd := range crdList.Items {
					if strings.Contains(crd.Name, "kueue") {
						if crd.Name != "kueue.openshift.io" {
							kueueCRDs = append(kueueCRDs, crd)
						}
					}
				}

				if len(kueueCRDs) == 0 {
					return fmt.Errorf("no kueue CRDs found")
				}

				for _, crd := range kueueCRDs {
					for _, version := range crd.Spec.Versions {
						if strings.HasPrefix(version.Name, "v1alpha") {
							return fmt.Errorf("unexpected v1alpha CRD installed: %s", crd.Name)
						}
					}
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Unexpected v1alpha CRD is installed")
		})

		It("verify webhook readiness", func() {
			Eventually(func() error {
				_, err := kubeClient.CoreV1().Endpoints(testutils.OperatorNamespace).Get(
					context.TODO(),
					"kueue-webhook-service",
					metav1.GetOptions{},
				)
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "webhook service is not ready")

			Eventually(func() error {
				endpoints, err := kubeClient.CoreV1().Endpoints(testutils.OperatorNamespace).Get(
					context.TODO(),
					"kueue-webhook-service",
					metav1.GetOptions{},
				)
				if err != nil {
					return err
				}
				if len(endpoints.Subsets) == 0 || len(endpoints.Subsets[0].Addresses) == 0 {
					return fmt.Errorf("webhook service has no endpoints")
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "webhook endpoints not ready")

			Eventually(func() error {
				// Validate validating webhook configuration
				vwh, err := kubeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(
					context.TODO(),
					"kueue-validating-webhook-configuration",
					metav1.GetOptions{},
				)
				Expect(err).NotTo(HaveOccurred(), "Failed to get validating webhook configuration")
				Expect(vwh.Name).To(Equal("kueue-validating-webhook-configuration"))

				// Validate mutating webhook configuration
				mwh, err := kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(
					context.TODO(),
					"kueue-mutating-webhook-configuration",
					metav1.GetOptions{},
				)
				Expect(err).NotTo(HaveOccurred(), "Failed to get mutating webhook configuration")
				Expect(mwh.Name).To(Equal("kueue-mutating-webhook-configuration"))

				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "webhook configurations are not ready")
		})

		It("verify that deny-all network policy is present", func() {
			Eventually(func() error {
				const (
					denyLabelKey   = "app.openshift.io/name"
					denyLabelValue = "kueue"
				)

				deny, err := kubeClient.NetworkingV1().NetworkPolicies(testutils.OperatorNamespace).Get(
					context.Background(),
					"kueue-deny-all",
					metav1.GetOptions{},
				)
				if err != nil {
					return err // retry
				}

				// deny-all policy should prohibit all traffic
				// (ingress and egress) for pods that has the
				// label 'app.openshift.io/name: kueue'
				Expect(deny.Spec.PodSelector.MatchLabels).To(Equal(map[string]string{denyLabelKey: denyLabelValue}))
				Expect(deny.Spec.Ingress).To(BeEmpty())
				Expect(deny.Spec.Egress).To(BeEmpty())
				Expect(deny.Spec.PolicyTypes).To(Equal([]networkingv1.PolicyType{
					networkingv1.PolicyTypeIngress,
					networkingv1.PolicyTypeEgress,
				}))

				// make sure that both our operator pod and the
				// operand pod have the right label
				pods, err := kubeClient.CoreV1().Pods(testutils.OperatorNamespace).List(context.Background(), metav1.ListOptions{})
				if err != nil {
					return err // retry
				}

				operatorPods := findOperatorPods(testutils.OperatorNamespace, pods)
				Expect(len(operatorPods)).To(BeNumerically(">=", 1), "no operator pod seen")
				kueuePods := findKueuePods(pods)
				Expect(len(kueuePods)).To(BeNumerically(">=", 1), "no kueue pod seen")

				for _, pod := range append(operatorPods, kueuePods...) {
					Expect(pod.Labels).To(HaveKeyWithValue(denyLabelKey, denyLabelValue))
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "network policy has not been setup")
		})
	})

	When("enable webhook via opt-in namespaces", func() {
		var (
			testNamespaceWithLabel    = "kueue-managed-test"
			testNamespaceWithoutLabel = "kueue-unmanaged-test"
			labelKey                  = "kueue.openshift.io/managed"
			labelValue                = "true"
			kueueClient               *upstreamkueueclient.Clientset
			testQueue                 = "test-queue"
			builderWithLabel          *testutils.TestResourceBuilder
			builderWithoutLabel       *testutils.TestResourceBuilder
			cleanupClusterQueue       func()
			cleanupLocalQueue         func()
			cleanupResourceFlavor     func()
		)

		BeforeAll(func() {
			kueueClient = clients.UpstreamKueueClient
			builderWithLabel = testutils.NewTestResourceBuilder(testNamespaceWithLabel, testQueue)
			builderWithoutLabel = testutils.NewTestResourceBuilder(testNamespaceWithoutLabel, testQueue)
			var err error
			// Create namespaces
			_, err = kubeClient.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNamespaceWithLabel,
					Labels: map[string]string{
						labelKey: labelValue,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			_, err = kubeClient.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNamespaceWithoutLabel,
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			cleanupClusterQueue, err = testutils.CreateClusterQueue(kueueClient)
			Expect(err).NotTo(HaveOccurred())

			// Create LocalQueue in managed namespace
			cleanupLocalQueue, err = testutils.CreateLocalQueue(kueueClient, testNamespaceWithLabel, testQueue)
			Expect(err).NotTo(HaveOccurred())

			cleanupResourceFlavor, err = testutils.CreateResourceFlavor(kueueClient)
			Expect(err).NotTo(HaveOccurred())

			nodes, err := kubeClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())

			for _, node := range nodes.Items {
				nodeCopy := node.DeepCopy()
				if nodeCopy.Labels == nil {
					nodeCopy.Labels = make(map[string]string)
				}
				nodeCopy.Labels["kueue.x-k8s.io/default-flavor"] = "true"

				patchData := map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": nodeCopy.Labels,
					},
				}
				patchBytes, _ := json.Marshal(patchData)

				_, err = kubeClient.CoreV1().Nodes().Patch(
					context.TODO(),
					node.Name,
					types.StrategicMergePatchType,
					patchBytes,
					metav1.PatchOptions{},
				)
				Expect(err).NotTo(HaveOccurred(), "failed to label node %s", node.Name)
			}
		})

		AfterAll(func() {
			ctx := context.TODO()
			cleanupLocalQueue()
			deleteNamespace(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNamespaceWithLabel,
				},
			})
			deleteNamespace(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNamespaceWithoutLabel,
				},
			})

			cleanupClusterQueue()
			cleanupResourceFlavor()
		})

		It("should suspend jobs only in labeled namespaces when labelPolicy=None", func() {
			kueueClientset := clients.KueueClient
			ctx := context.TODO()

			// Verify webhook configuration
			Eventually(func() error {
				validateWebhookConfig(kubeClient, labelKey, labelValue, "job")
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			kueueInstance, err := kueueClientset.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
			initialKueueInstance := kueueInstance.DeepCopy()
			kueueInstance.Spec.Config.WorkloadManagement.LabelPolicy = "None"
			applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)

			// Test labeled namespace
			By("creating job in labeled namespace")
			job := builderWithLabel.NewJob()
			job.Labels = map[string]string{}
			createdJob, err := kubeClient.BatchV1().Jobs(testNamespaceWithLabel).Create(context.TODO(), job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

			fetchWorkload(kueueClient, testNamespaceWithLabel, string(createdJob.UID))

			// Verify job did not start (Kueue interference)
			Eventually(func() *batchv1.JobStatus {
				job, _ := kubeClient.BatchV1().Jobs(testNamespaceWithLabel).Get(context.TODO(), createdJob.Name, metav1.GetOptions{})
				return &job.Status
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(HaveField("Active", BeNumerically("==", 0)), "Job started in labeled namespace")

			By("creating job in unlabeled namespace")
			jobWithoutLabel := builderWithoutLabel.NewJob()
			createdUnlabeledJob, err := kubeClient.BatchV1().Jobs(testNamespaceWithoutLabel).Create(context.TODO(), jobWithoutLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdUnlabeledJob.Namespace, createdUnlabeledJob.Name)

			// Verify job starts normally (no Kueue interference)
			Eventually(func() *batchv1.JobStatus {
				job, _ := kubeClient.BatchV1().Jobs(testNamespaceWithoutLabel).Get(context.TODO(), createdUnlabeledJob.Name, metav1.GetOptions{})
				return &job.Status
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(HaveField("Active", BeNumerically(">=", 1)), "Job not started in unlabeled namespace")

			applyKueueConfig(ctx, initialKueueInstance.Spec.Config, kubeClient)
		})

		It("should manage jobs only in labeled namespaces", func() {
			// Verify webhook configuration
			Eventually(func() error {
				validateWebhookConfig(kubeClient, labelKey, labelValue, "job")
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			ctx := context.TODO()
			// Test labeled namespace
			By("creating job in labeled namespace")
			jobWithLabel := builderWithLabel.NewJob()
			createdJob, err := kubeClient.BatchV1().Jobs(testNamespaceWithLabel).Create(ctx, jobWithLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

			verifyWorkloadCreated(kueueClient, testNamespaceWithLabel, string(createdJob.UID))

			By("creating job in labeled namespace not managed by Kueue")
			jobWithoutQueue := builderWithLabel.NewJobWithoutQueue()
			createdUnmanagedJob, err := kubeClient.BatchV1().Jobs(testNamespaceWithLabel).Create(context.TODO(), jobWithoutQueue, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdUnmanagedJob.Namespace, createdUnmanagedJob.Name)

			Eventually(func() *batchv1.JobStatus {
				job, _ := kubeClient.BatchV1().Jobs(testNamespaceWithLabel).Get(context.TODO(), createdUnmanagedJob.Name, metav1.GetOptions{})
				return &job.Status
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(HaveField("Active", BeNumerically(">=", 1)), "Job not managed by Kueue in labeled namespace")

			By("creating job in unlabeled namespace")
			jobWithoutLabel := builderWithoutLabel.NewJob()
			createdUnlabeledJob, err := kubeClient.BatchV1().Jobs(testNamespaceWithoutLabel).Create(context.TODO(), jobWithoutLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdUnlabeledJob.Namespace, createdUnlabeledJob.Name)
			// Verify job starts normally (no Kueue interference)
			Eventually(func() *batchv1.JobStatus {
				job, _ := kubeClient.BatchV1().Jobs(testNamespaceWithoutLabel).Get(context.TODO(), createdUnlabeledJob.Name, metav1.GetOptions{})
				return &job.Status
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(HaveField("Active", BeNumerically(">=", 1)), "Job not started in unlabeled namespace")
		})
		It("should manage pods only in labeled namespaces", func() {
			// Verify webhook configuration
			Eventually(func() error {
				validateWebhookConfig(kubeClient, labelKey, labelValue, "pod")
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			// Test labeled namespace
			By("creating pod in labeled namespace")
			podWithLabel := builderWithLabel.NewPod()
			ctx := context.TODO()
			createdPod, err := kubeClient.CoreV1().Pods(testNamespaceWithLabel).Create(ctx, podWithLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpObject(ctx, genericClient, createdPod)

			verifyWorkloadCreated(kueueClient, testNamespaceWithLabel, string(createdPod.UID))

			By("creating pod in labeled namespace not managed by Kueue")
			podWithoutQueue := builderWithLabel.NewPodWithoutQueue()
			createdUnmanagedPod, err := kubeClient.CoreV1().Pods(testNamespaceWithLabel).Create(ctx, podWithoutQueue, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpObject(ctx, genericClient, createdUnmanagedPod)

			Eventually(func() corev1.PodPhase {
				pod, _ := kubeClient.CoreV1().Pods(testNamespaceWithLabel).Get(context.TODO(), createdUnmanagedPod.Name, metav1.GetOptions{})
				return pod.Status.Phase
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Equal(corev1.PodRunning), "Pod not managed by Kueue in labeled namespace")

			By("creating pod in unlabeled namespace")
			podWithoutLabel := builderWithoutLabel.NewPod()
			createdUnlabeledPod, err := kubeClient.CoreV1().Pods(testNamespaceWithoutLabel).Create(context.TODO(), podWithoutLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpObject(ctx, genericClient, createdUnlabeledPod)

			// Verify pod starts normally (no Kueue interference)
			Eventually(func() corev1.PodPhase {
				pod, _ := kubeClient.CoreV1().Pods(testNamespaceWithoutLabel).Get(context.TODO(), createdUnlabeledPod.Name, metav1.GetOptions{})
				return pod.Status.Phase
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Equal(corev1.PodRunning), "Pod not running in unlabeled namespace")
		})
		It("should manage deployments only in labeled namespaces", func() {
			// Verify webhook configuration
			Eventually(func() error {
				validateWebhookConfig(kubeClient, labelKey, labelValue, "deployment")
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
			ctx := context.TODO()
			By("creating deployment in labeled namespace")
			deployWithLabel := builderWithLabel.NewDeployment()
			createdDeploy, err := kubeClient.AppsV1().Deployments(testNamespaceWithLabel).Create(ctx, deployWithLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpObject(ctx, genericClient, createdDeploy)

			// Get the deployment's pod
			var deploymentPod *corev1.Pod
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(testNamespaceWithLabel).List(context.TODO(), metav1.ListOptions{
					LabelSelector: "app=test-deployment",
				})
				if err != nil || len(pods.Items) == 0 {
					return fmt.Errorf("no pods found for deployment")
				}
				deploymentPod = &pods.Items[0]
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			verifyWorkloadCreated(kueueClient, testNamespaceWithLabel, string(deploymentPod.UID))

			By("verifying deployment pods are available")
			Eventually(func() int32 {
				deploy, _ := kubeClient.AppsV1().Deployments(testNamespaceWithLabel).Get(context.TODO(), createdDeploy.Name, metav1.GetOptions{})
				return deploy.Status.AvailableReplicas
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Equal(int32(1)), "Deployment not available")

			By("creating deployment in labeled namespace not managed by Kueue")
			deployWithoutQueue := builderWithLabel.NewDeploymentWithoutQueue()
			createdUnmanagedDeploy, err := kubeClient.AppsV1().Deployments(testNamespaceWithLabel).Create(ctx, deployWithoutQueue, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpObject(ctx, genericClient, createdUnmanagedDeploy)

			Eventually(func() int32 {
				deploy, _ := kubeClient.AppsV1().Deployments(testNamespaceWithLabel).Get(context.TODO(), createdUnmanagedDeploy.Name, metav1.GetOptions{})
				return deploy.Status.AvailableReplicas
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Equal(int32(1)), "Deployment not managed by Kueue in labeled namespace")

			By("creating deployment in unlabeled namespace")
			deployWithoutLabel := builderWithoutLabel.NewDeployment()
			createdUnlabeledDeploy, err := kubeClient.AppsV1().Deployments(testNamespaceWithoutLabel).Create(context.TODO(), deployWithoutLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpObject(ctx, genericClient, createdUnlabeledDeploy)

			Eventually(func() int32 {
				deploy, _ := kubeClient.AppsV1().Deployments(testNamespaceWithoutLabel).Get(context.TODO(), createdUnlabeledDeploy.Name, metav1.GetOptions{})
				return deploy.Status.AvailableReplicas
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Equal(int32(1)), "Deployment in unlabeled namespace not available")
		})
		It("should manage statefulsets only in labeled namespaces", func() {
			// Verify webhook configuration
			Eventually(func() error {
				validateWebhookConfig(kubeClient, labelKey, labelValue, "statefulset")
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("creating statefulset in labeled namespace")
			ctx := context.TODO()
			ssWithLabel := builderWithLabel.NewStatefulSet()
			createdSS, err := kubeClient.AppsV1().StatefulSets(testNamespaceWithLabel).Create(ctx, ssWithLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpObject(ctx, genericClient, createdSS)

			// Wait for StatefulSet to create pod
			var ssPod *corev1.Pod
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(testNamespaceWithLabel).List(context.TODO(), metav1.ListOptions{
					LabelSelector: "app=test-statefulset",
				})
				if err != nil || len(pods.Items) == 0 {
					return fmt.Errorf("no pods found for statefulset")
				}
				ssPod = &pods.Items[0]
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			workload := verifyWorkloadCreated(kueueClient, testNamespaceWithLabel, string(ssPod.UID))
			defer testutils.CleanUpWorkload(ctx, kueueClient, testNamespaceWithLabel, workload)

			By("verifying statefulset pods are running")
			Eventually(func() int32 {
				ss, _ := kubeClient.AppsV1().StatefulSets(testNamespaceWithLabel).Get(context.TODO(), createdSS.Name, metav1.GetOptions{})
				return ss.Status.ReadyReplicas
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Equal(int32(1)), "StatefulSet not ready")

			By("creating statefulset in labeled namespace not managed by Kueue")
			ssWithoutQueue := builderWithLabel.NewStatefulSetWithoutQueue()
			createdUnmanagedSS, err := kubeClient.AppsV1().StatefulSets(testNamespaceWithLabel).Create(context.TODO(), ssWithoutQueue, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpObject(ctx, genericClient, createdUnmanagedSS)

			Eventually(func() int32 {
				ss, _ := kubeClient.AppsV1().StatefulSets(testNamespaceWithLabel).Get(context.TODO(), createdUnmanagedSS.Name, metav1.GetOptions{})
				return ss.Status.ReadyReplicas
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Equal(int32(1)), "StatefulSet not managed by Kueue in labeled namespace")

			By("creating statefulset in unlabeled namespace")
			ssWithoutLabel := builderWithoutLabel.NewStatefulSet()
			createdUnlabeledSS, err := kubeClient.AppsV1().StatefulSets(testNamespaceWithoutLabel).Create(context.TODO(), ssWithoutLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpObject(ctx, genericClient, createdUnlabeledSS)

			Eventually(func() int32 {
				ss, _ := kubeClient.AppsV1().StatefulSets(testNamespaceWithoutLabel).Get(context.TODO(), createdUnlabeledSS.Name, metav1.GetOptions{})
				return ss.Status.ReadyReplicas
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Equal(int32(1)), "StatefulSet in unlabeled namespace not ready")
		})
		It("should expose metrics endpoint with TLS", func() {
			ctx := context.TODO()
			var (
				err                error
				podName            = "curl-metrics-test"
				containerName      = "curl-metrics"
				certMountPath      = "/etc/kueue/metrics/certs"
				metricsServiceName = "kueue-controller-manager-metrics-service"
				kueueClient        = clients.UpstreamKueueClient
			)

			By("creating workloads")
			_, err = testutils.CreateWorkload(kueueClient, testNamespaceWithLabel, testQueue, "test-workload")
			Expect(err).NotTo(HaveOccurred())

			_, err = testutils.CreateWorkload(kueueClient, testNamespaceWithLabel, testQueue, "test-workload-2")
			Expect(err).NotTo(HaveOccurred())

			By("Creating curl test pod")
			curlPod := testutils.MakeCurlMetricsPod(testutils.OperatorNamespace)
			podCleanupFn, err := testutils.CreatePod(kubeClient, curlPod.Obj())
			Expect(err).NotTo(HaveOccurred(), "failed to create curl metrics test pod")
			defer podCleanupFn()

			Eventually(func() error {
				pod, err := kubeClient.CoreV1().Pods(testutils.OperatorNamespace).Get(ctx, podName, metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get pod: %w", err)
				}
				if pod.Status.Phase != corev1.PodRunning {
					return fmt.Errorf("pod %q not ready, phase: %s", podName, pod.Status.Phase)
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "curl-metrics-test pod did not become ready")

			Eventually(func() error {
				metricsOutput, _, err := Kexecute(ctx, clients.RestConfig, kubeClient, testutils.OperatorNamespace, podName, containerName,
					[]string{
						"/bin/sh", "-c",
						fmt.Sprintf(
							"curl -s --cacert %s/ca.crt -H \"Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)\" https://%s.%s.svc.cluster.local:8443/metrics",
							certMountPath,
							metricsServiceName,
							testutils.OperatorNamespace,
						),
					})
				if err != nil {
					return fmt.Errorf("exec into pod failed: %w", err)
				}

				if !strings.Contains(string(metricsOutput), "kueue_quota_reserved_workloads_total") {
					return fmt.Errorf("expected metric not found in output")
				}

				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "expected HTTP 200 OK from metrics endpoint")
		})
	})
	When("cleaning up Kueue resources", Label("disruptive"), func() {
		var (
			kueueName      = "cluster"
			kueueClientset *kueueclient.Clientset
			dynamicClient  dynamic.Interface
		)

		BeforeEach(func() {
			kueueClientset = clients.KueueClient
			dynamicClient = clients.DynamicClient
		})

		It("should delete Kueue instance and verify cleanup", func() {
			ctx := context.TODO()

			// First, create some Kueue Custom Resources to test that they are NOT deleted
			// This addresses OCPBUGS-62254: Kueue uninstall does not delete all Kueue CRs
			klog.Infof("Creating Kueue Custom Resources to test they are NOT deleted when Kueue CR is deleted")

			By("create a resourceFlavor")
			cleanupResourceFlavorFn, err := testutils.CreateResourceFlavor(clients.UpstreamKueueClient)
			Expect(err).ToNot(HaveOccurred(), "Failed to create ResourceFlavor")
			defer cleanupResourceFlavorFn()

			By("create clusterQueue")
			cleanupClusterQueueFn, err := testutils.CreateClusterQueue(clients.UpstreamKueueClient)
			Expect(err).ToNot(HaveOccurred(), "Failed to create ClusterQueue")
			defer cleanupClusterQueueFn()

			// Create a test namespace for LocalQueue
			testNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "kueue-test-namespace",
					Labels: map[string]string{
						"kueue.x-k8s.io/managed": "true",
					},
				},
			}
			_, err = testutils.CreateNamespace(kubeClient, testNamespace)
			Expect(err).ToNot(HaveOccurred(), "Failed to create test namespace")

			By("create a LocalQueue in the test namespace")
			cleanupLocalQueueFn, err := testutils.CreateLocalQueue(clients.UpstreamKueueClient, testNamespace.Name, "test-localqueue")
			Expect(err).ToNot(HaveOccurred(), "Failed to create LocalQueue")
			defer cleanupLocalQueueFn()

			job := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-job",
					Namespace: testNamespace.Name,
					Labels: map[string]string{
						"kueue.x-k8s.io/queue-name": "test-localqueue",
					},
				},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers: []corev1.Container{
								{
									Name:    "test-container",
									Image:   "busybox",
									Command: []string{"sh", "-c", "echo 'Hello World'"},
								},
							},
						},
					},
				},
			}
			jobCleanupFn, err := testutils.CreateJob(kubeClient, job)
			Expect(err).ToNot(HaveOccurred(), "Failed to create test job")
			defer jobCleanupFn()

			By("wait for workload to be created")
			Eventually(func() error {
				workloads, err := clients.UpstreamKueueClient.KueueV1beta1().Workloads(testNamespace.Name).List(ctx, metav1.ListOptions{})
				if err != nil {
					return fmt.Errorf("Failed to list workloads: %v", err)
				}
				if len(workloads.Items) == 0 {
					return fmt.Errorf("No workloads found for job")
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Workload was not created for job")

			By("verify the Kueue instance exists and delete it")
			_, err = kueueClientset.KueueV1().Kueues().Get(ctx, kueueName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")

			err = kueueClientset.KueueV1().Kueues().Delete(ctx, kueueName, metav1.DeleteOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to delete Kueue instance")

			Eventually(func() error {
				_, err := kueueClientset.KueueV1().Kueues().Get(ctx, kueueName, metav1.GetOptions{})
				if err == nil {
					return fmt.Errorf("Kueue instance %s still exists", kueueName)
				}

				By("verify that Kueue Custom Resources are NOT deleted when Kueue CR is deleted")

				clusterQueues, err := clients.UpstreamKueueClient.KueueV1beta1().ClusterQueues().List(ctx, metav1.ListOptions{})
				if err != nil {
					return fmt.Errorf("Failed to list ClusterQueues: %v", err)
				}
				if len(clusterQueues.Items) == 0 {
					return fmt.Errorf("Expected ClusterQueues to still exist after Kueue CR deletion, but none found")
				}
				klog.Infof("Found %d ClusterQueues still existing after Kueue CR deletion", len(clusterQueues.Items))

				resourceFlavors, err := clients.UpstreamKueueClient.KueueV1beta1().ResourceFlavors().List(ctx, metav1.ListOptions{})
				if err != nil {
					return fmt.Errorf("Failed to list ResourceFlavors: %v", err)
				}
				if len(resourceFlavors.Items) == 0 {
					return fmt.Errorf("Expected ResourceFlavors to still exist after Kueue CR deletion, but none found")
				}
				klog.Infof("Found %d ResourceFlavors still existing after Kueue CR deletion", len(resourceFlavors.Items))

				localQueues, err := clients.UpstreamKueueClient.KueueV1beta1().LocalQueues(testNamespace.Name).List(ctx, metav1.ListOptions{})
				if err != nil {
					return fmt.Errorf("Failed to list LocalQueues: %v", err)
				}
				if len(localQueues.Items) == 0 {
					return fmt.Errorf("Expected LocalQueues to still exist after Kueue CR deletion, but none found")
				}
				klog.Infof("Found %d LocalQueues still existing after Kueue CR deletion", len(localQueues.Items))

				workloads, err := clients.UpstreamKueueClient.KueueV1beta1().Workloads(testNamespace.Name).List(ctx, metav1.ListOptions{})
				if err != nil {
					return fmt.Errorf("Failed to list Workloads: %v", err)
				}
				if len(workloads.Items) == 0 {
					return fmt.Errorf("Expected Workloads to still exist after Kueue CR deletion, but none found")
				}
				klog.Infof("Found %d Workloads still existing after Kueue CR deletion", len(workloads.Items))

				clusterRoles, err := kubeClient.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{})
				if err != nil {
					return fmt.Errorf("Failure on fetching cluster roles: %v", err)
				}
				klog.Infof("Verifying removal of Cluster Roles")
				bundleClusterRoleNames := []string{
					"kueue-batch-user-role",
					"kueue-batch-admin-role",
				}
				for _, role := range clusterRoles.Items {
					if strings.Contains(role.Name, "kueue") && !strings.Contains(role.Name, "kueue-operator") && !strings.Contains(role.Name, "openshift.io") && !slices.Contains(bundleClusterRoleNames, role.Name) {
						return fmt.Errorf("ClusterRole %s still exists", role.Name)
					}
				}

				clusterRoleBindings, err := kubeClient.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
				if err != nil {
					return fmt.Errorf("Failure on fetching cluster role bindings: %v", err)
				}
				klog.Infof("Verifying removal of Cluster Role Bindings")
				for _, binding := range clusterRoleBindings.Items {
					if strings.Contains(binding.Name, "kueue") && !strings.Contains(binding.Name, "kueue-operator") {
						return fmt.Errorf("ClusterRoleBinding %s still exists", binding.Name)
					}
				}

				mutatingwebhooks, _ := kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
				klog.Infof("Verifying removal of Mutating Webhooks")
				for _, wh := range mutatingwebhooks.Items {
					if strings.Contains(wh.Name, "kueue") {
						return fmt.Errorf("MutatingWebhookConfiguration %s still exists", wh.Name)
					}
				}

				validatingwebhooks, _ := kubeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
				klog.Infof("Verifying removal of Validating Webhooks")
				for _, wh := range validatingwebhooks.Items {
					if strings.Contains(wh.Name, "kueue") {
						return fmt.Errorf("ValidatingWebhookConfiguration %s still exists", wh.Name)
					}
				}

				certManagerResources := []string{"certificates", "issuers"}
				for _, resource := range certManagerResources {
					gvr := schema.GroupVersionResource{
						Group:    "cert-manager.io",
						Version:  "v1",
						Resource: resource,
					}

					klog.Infof("Verifying removal of %s instances", resource)

					crList, err := dynamicClient.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{})
					if err != nil {
						return fmt.Errorf("Failed to list instances of %s: %v", resource, err)
					}

					if len(crList.Items) > 0 {
						return fmt.Errorf("%s instances still exist", resource)
					}
				}

				klog.Infof("Verifying removal of ConfigMap: kueue-manager-config")
				_, err = kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
				if err == nil {
					return fmt.Errorf("ConfigMap kueue-manager-config still exists")
				}

				secretNames := []string{"kueue-webhook-server-cert", "metrics-server-cert"}
				for _, secretName := range secretNames {
					klog.Infof("Verifying removal of Secret: %s", secretName)
					_, err := kubeClient.CoreV1().Secrets(testutils.OperatorNamespace).Get(ctx, secretName, metav1.GetOptions{})
					if err == nil {
						return fmt.Errorf("Secret %s still exists", secretName)
					}
				}

				klog.Infof("Verifying removal of ServiceAccount: kueue-controller-manager")
				_, err = kubeClient.CoreV1().ServiceAccounts(testutils.OperatorNamespace).Get(ctx, "kueue-controller-manager", metav1.GetOptions{})
				if err == nil {
					return fmt.Errorf("ServiceAccount kueue-controller-manager still exists")
				}

				klog.Infof("Verifying removal of all NetworkPolicies in namespace: %s", testutils.OperatorNamespace)
				networkPolicyList, err := kubeClient.NetworkingV1().NetworkPolicies(testutils.OperatorNamespace).List(ctx, metav1.ListOptions{})
				if err != nil {
					return fmt.Errorf("Failed to list NetworkPolicies: %v", err)
				}
				if len(networkPolicyList.Items) > 0 {
					var networkPolicyNames []string
					for _, netpl := range networkPolicyList.Items {
						networkPolicyNames = append(networkPolicyNames, netpl.Name)
					}
					return fmt.Errorf("NetworkPolicies still exist in namespace %s: %v", testutils.OperatorNamespace, networkPolicyNames)
				}

				klog.Infof("Verifying removal of all Services in namespace: %s", testutils.OperatorNamespace)
				serviceList, err := kubeClient.CoreV1().Services(testutils.OperatorNamespace).List(ctx, metav1.ListOptions{})
				if err != nil {
					return fmt.Errorf("Failed to list Services: %v", err)
				}
				if len(serviceList.Items) > 0 {
					var serviceNames []string
					for _, svc := range serviceList.Items {
						serviceNames = append(serviceNames, svc.Name)
					}
					return fmt.Errorf("Services still exist in namespace %s: %v", testutils.OperatorNamespace, serviceNames)
				}

				_, err = kubeClient.FlowcontrolV1().PriorityLevelConfigurations().Get(ctx, "kueue-visibility", metav1.GetOptions{})
				if err == nil || (err != nil && !apierrors.IsNotFound(err)) {
					return fmt.Errorf("PriorityLevelConfiguration still exists: %v", err)
				}

				_, err = kubeClient.FlowcontrolV1().FlowSchemas().Get(ctx, "visibility", metav1.GetOptions{})
				if err == nil || (err != nil && !apierrors.IsNotFound(err)) {
					return fmt.Errorf("FlowSchema still exists: %v", err)
				}

				_, err = clients.ApiregistrationClient.APIServices().Get(ctx, "visibility.kueue.x-k8s.io", metav1.GetOptions{})
				if err == nil || (err != nil && !apierrors.IsNotFound(err)) {
					return fmt.Errorf("APIService still exists: %v", err)
				}

				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Resources were not cleaned up properly")
		})

		It("should redeploy Kueue instance and verify pod is ready", func() {
			ctx := context.TODO()

			klog.Infof("Redeploying Kueue instance by applying 08_kueue_default.yaml")
			requiredObj, err := runtime.Decode(ssscheme.Codecs.UniversalDecoder(ssv1.SchemeGroupVersion), bindata.MustAsset("assets/08_kueue_default.yaml"))
			Expect(err).ToNot(HaveOccurred(), "Failed to decode 08_kueue_default.yaml")

			requiredSS := requiredObj.(*ssv1.Kueue)
			_, err = kueueClientset.KueueV1().Kueues().Create(ctx, requiredSS, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to create Kueue instance")
			defer testutils.CleanUpKueueInstance(ctx, kueueClientset, "cluster")

			Eventually(func() error {
				_, err := kueueClientset.KueueV1().Kueues().Get(ctx, kueueName, metav1.GetOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Kueue instance was not created successfully")

			Eventually(func() error {
				podItems, err := kubeClient.CoreV1().Pods(testutils.OperatorNamespace).List(ctx, metav1.ListOptions{})
				if err != nil {
					klog.Errorf("Unable to list pods: %v", err)
					return nil
				}
				for _, pod := range podItems.Items {
					if !strings.HasPrefix(pod.Name, "kueue-") {
						continue
					}
					klog.Infof("Checking pod: %v, phase: %v, deletionTS: %v\n", pod.Name, pod.Status.Phase, pod.GetDeletionTimestamp())
					if pod.Status.Phase == corev1.PodRunning && pod.GetDeletionTimestamp() == nil && pod.Status.ContainerStatuses[0].Ready {
						return nil
					}
				}
				return fmt.Errorf("pod is not ready")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "kueue pod failed to be ready")
		})
	})
})

func applyKueueConfig(ctx context.Context, config ssv1.KueueConfiguration, kClient *kubernetes.Clientset) {
	kueueClientset := clients.KueueClient
	By("Feching Kueue Instance")
	kueueInstance, err := kueueClientset.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
	kueueInstance.Spec.Config = config
	By("Updating Kueue config")
	_, err = kueueClientset.KueueV1().Kueues().Update(ctx, kueueInstance, metav1.UpdateOptions{})
	Expect(err).ToNot(HaveOccurred(), "Failed to update Kueue config")

	By("Deleting kueue-controller-manager deployment")
	err = kClient.AppsV1().Deployments(testutils.OperatorNamespace).Delete(ctx, "kueue-controller-manager", metav1.DeleteOptions{})
	Expect(err).NotTo(HaveOccurred())

	Eventually(func() error {
		managerDeployment, err := kClient.AppsV1().Deployments(testutils.OperatorNamespace).Get(ctx, "kueue-controller-manager", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("Unable to fetch manager deployment: %v", err)
		}
		By(fmt.Sprintf("Checking if deployment replicas: %v matches amount of ready replicas: %v", managerDeployment.Status.Replicas, managerDeployment.Status.ReadyReplicas))
		if managerDeployment.Status.ReadyReplicas == managerDeployment.Status.Replicas {
			return nil
		}
		return fmt.Errorf("deployment is not ready")
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "kueue-controller-manager deployment failed to be ready")
}

func validateWebhookConfig(kubeClient *kubernetes.Clientset, labelKey, labelValue, framework string) {
	validateWebhook := func(webhooks []admissionregistrationv1.ValidatingWebhookConfiguration) {
		found := false
		for _, wh := range webhooks {
			for _, webhook := range wh.Webhooks {
				if strings.Contains(webhook.Name, framework) && strings.Contains(webhook.Name, ".kb.io") {
					found = true
					Expect(webhook.NamespaceSelector).NotTo(BeNil(),
						"NamespaceSelector is nil for webhook %s", webhook.Name)
					Expect(webhook.NamespaceSelector.MatchExpressions).To(
						ContainElement(metav1.LabelSelectorRequirement{
							Key:      labelKey,
							Operator: metav1.LabelSelectorOpIn,
							Values:   []string{labelValue},
						}),
						"Webhook %s missing required namespace selector", webhook.Name)
				}
			}
		}
		Expect(found).To(BeTrue(), "No validating webhook found for framework %s", framework)
	}

	mutatingWebhook := func(webhooks []admissionregistrationv1.MutatingWebhookConfiguration) {
		found := false
		for _, wh := range webhooks {
			for _, webhook := range wh.Webhooks {
				if strings.Contains(webhook.Name, framework) && strings.Contains(webhook.Name, ".kb.io") {
					found = true
					Expect(webhook.NamespaceSelector).NotTo(BeNil(),
						"NamespaceSelector is nil for webhook %s", webhook.Name)
					Expect(webhook.NamespaceSelector.MatchExpressions).To(
						ContainElement(metav1.LabelSelectorRequirement{
							Key:      labelKey,
							Operator: metav1.LabelSelectorOpIn,
							Values:   []string{labelValue},
						}),
						"Webhook %s missing required namespace selector", webhook.Name)
				}
			}
		}
		Expect(found).To(BeTrue(), "No mutating webhook found for framework %s", framework)
	}

	vwh, err := kubeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{})
	Expect(err).NotTo(HaveOccurred())
	validateWebhook(vwh.Items)

	mwh, err := kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{})
	Expect(err).NotTo(HaveOccurred())
	mutatingWebhook(mwh.Items)
}

func fetchWorkload(kueueClient *upstreamkueueclient.Clientset, namespace, uid string) {
	Eventually(func() error {
		workloads, err := kueueClient.KueueV1beta1().Workloads(namespace).List(context.TODO(), metav1.ListOptions{
			LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", uid),
		})
		if err != nil {
			return err
		}
		if len(workloads.Items) == 0 {
			return fmt.Errorf("no workload found")
		}
		return nil
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}

func verifyWorkloadCreated(kueueClient *upstreamkueueclient.Clientset, namespace, uid string) string {
	By("verifying workload created")
	// Verify that a Workload with the expected label is created and admitted
	var workload *kueuev1beta1.Workload
	Eventually(func() error {
		workloads, err := kueueClient.KueueV1beta1().Workloads(namespace).List(context.TODO(), metav1.ListOptions{
			LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", uid),
		})
		if err != nil {
			return err
		}
		if len(workloads.Items) > 0 {
			workload = &workloads.Items[0]
			return nil
		}

		// If not found by job-uid label, look for workloads by ownerReference (for StatefulSets)
		allWorkloads, err := kueueClient.KueueV1beta1().Workloads(namespace).List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			return err
		}

		for _, wl := range allWorkloads.Items {
			for _, ownerRef := range wl.OwnerReferences {
				if ownerRef.UID == types.UID(uid) {
					workload = &wl

					return nil
				}
			}
		}

		return fmt.Errorf("no workload found")
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

	Eventually(func() bool {
		updatedWorkload, err := kueueClient.KueueV1beta1().Workloads(namespace).Get(context.TODO(), workload.Name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return apimeta.IsStatusConditionTrue(updatedWorkload.Status.Conditions, kueuev1beta1.WorkloadAdmitted)
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "Workload not admitted")
	return workload.Name
}

func deployOperand() error {
	ssClient := clients.KueueClient

	ctx, cancelFnc := context.WithCancel(context.TODO())
	defer cancelFnc()

	assets := []struct {
		path           string
		readerAndApply func(objBytes []byte) error
	}{
		{
			path: "assets/08_kueue_default.yaml",
			readerAndApply: func(objBytes []byte) error {
				requiredObj, err := runtime.Decode(ssscheme.Codecs.UniversalDecoder(ssv1.SchemeGroupVersion), objBytes)
				if err != nil {
					klog.Errorf("Unable to decode assets/08_kueue_default.yaml: %v", err)
					return err
				}
				requiredSS := requiredObj.(*ssv1.Kueue)
				_, err = ssClient.KueueV1().Kueues().Create(ctx, requiredSS, metav1.CreateOptions{})
				if err != nil {
					if !errors.IsNotFound(err) {
						return nil
					}
					return err
				}
				return nil
			},
		},
	}

	Eventually(func() error {
		for _, asset := range assets {
			klog.Infof("Creating %v", asset.path)
			if err := asset.readerAndApply(bindata.MustAsset(asset.path)); err != nil {
				return err
			}
		}
		return nil
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "assets should be deployed")

	Eventually(func() error {
		ctx := context.TODO()
		podItems, err := kubeClient.CoreV1().Pods(testutils.OperatorNamespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			klog.Errorf("Unable to list pods: %v", err)
			return nil
		}
		for _, pod := range podItems.Items {
			if !strings.HasPrefix(pod.Name, "kueue-controller-manager") {
				continue
			}
			klog.Infof("Checking pod: %v, phase: %v, deletionTS: %v\n", pod.Name, pod.Status.Phase, pod.GetDeletionTimestamp())
			if pod.Status.Phase == corev1.PodRunning && pod.GetDeletionTimestamp() == nil && pod.Status.ContainerStatuses[0].Ready {
				return nil
			}
		}
		return fmt.Errorf("pod is not ready")
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "kueue pod failed to be ready")

	return nil
}

func Kexecute(ctx context.Context, restConfig *rest.Config, kubeClient kubernetes.Interface, namespace, podName, containerName string, command []string) ([]byte, []byte, error) {
	var out, outErr bytes.Buffer

	req := kubeClient.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command:   command,
			Container: containerName,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
	if err != nil {
		return nil, nil, err
	}
	if err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &out, Stderr: &outErr}); err != nil {
		return nil, nil, err
	}

	return out.Bytes(), outErr.Bytes(), nil
}
