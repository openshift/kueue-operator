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
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ssv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1alpha1"
	kueueclient "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned"
	ssscheme "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned/scheme"
	"github.com/openshift/kueue-operator/test/e2e/bindata"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	batchv1 "k8s.io/api/batch/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	upstreamkueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
	//+kubebuilder:scaffold:imports
)

const (
	// sometimes kueue deployment takes a while
	operatorReadyTime time.Duration = 3 * time.Minute
	operatorPoll                    = 10 * time.Second
	operatorNamespace               = "openshift-kueue-operator"
)

var _ = Describe("Kueue Operator", Ordered, func() {
	var (
		namespace = operatorNamespace
	)
	// AfterEach(func() {
	// 	Expect(kubeClient.CoreV1().Namespaces().Delete(context.TODO(), namespace, metav1.DeleteOptions{})).To(Succeed())
	// })
	When("installs", func() {
		It("operator pods should be ready", func() {
			Expect(deployOperator()).To(Succeed(), "operator deployment should not fail")
			Eventually(func() error {
				ctx := context.TODO()
				podItems, err := kubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
				if err != nil {
					klog.Errorf("Unable to list pods: %v", err)
					return nil
				}
				for _, pod := range podItems.Items {
					if !strings.HasPrefix(pod.Name, namespace+"-") {
						continue
					}
					klog.Infof("Checking pod: %v, phase: %v, deletionTS: %v\n", pod.Name, pod.Status.Phase, pod.GetDeletionTimestamp())
					if pod.Status.Phase == corev1.PodRunning && pod.GetDeletionTimestamp() == nil {
						return nil
					}
				}
				return fmt.Errorf("pod is not ready")
			}, operatorReadyTime, operatorPoll).Should(Succeed(), "operator pod failed to be ready")
		})
		It("kueue pods should be ready", func() {
			Eventually(func() error {
				ctx := context.TODO()
				podItems, err := kubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
				if err != nil {
					klog.Errorf("Unable to list pods: %v", err)
					return nil
				}
				for _, pod := range podItems.Items {
					if !strings.HasPrefix(pod.Name, "kueue-controller-manager") {
						continue
					}
					klog.Infof("Checking pod: %v, phase: %v, deletionTS: %v\n", pod.Name, pod.Status.Phase, pod.GetDeletionTimestamp())
					if pod.Status.Phase == corev1.PodRunning && pod.GetDeletionTimestamp() == nil {
						return nil
					}
				}
				return fmt.Errorf("pod is not ready")
			}, operatorReadyTime, operatorPoll).Should(Succeed(), "kueue pod failed to be ready")

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
						if crd.Name != "kueues.operator.openshift.io" {
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
			}, operatorReadyTime, operatorPoll).Should(Succeed(), "Unexpected v1alpha CRD is installed")
		})

		It("verify webhook readiness", func() {
			Eventually(func() error {
				_, err := kubeClient.CoreV1().Endpoints(operatorNamespace).Get(
					context.TODO(),
					"kueue-webhook-service",
					metav1.GetOptions{},
				)
				return err
			}, operatorReadyTime, operatorPoll).Should(Succeed(), "webhook service is not ready")

			Eventually(func() error {
				endpoints, err := kubeClient.CoreV1().Endpoints(operatorNamespace).Get(
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
			}, operatorReadyTime, operatorPoll).Should(Succeed(), "webhook endpoints not ready")

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
			}, operatorReadyTime, operatorPoll).Should(Succeed(), "webhook configurations are not ready")
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

			Expect(testutils.CreateClusterQueue(kueueClient)).To(Succeed())

			// Create LocalQueue in managed namespace
			Expect(testutils.CreateLocalQueue(kueueClient, testNamespaceWithLabel)).To(Succeed())

			Expect(testutils.CreateResourceFlavor(kueueClient)).To(Succeed())

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
			_ = kubeClient.CoreV1().Namespaces().Delete(context.TODO(), testNamespaceWithLabel, metav1.DeleteOptions{})
			_ = kubeClient.CoreV1().Namespaces().Delete(context.TODO(), testNamespaceWithoutLabel, metav1.DeleteOptions{})
			_ = kueueClient.KueueV1beta1().ClusterQueues().Delete(context.TODO(), "test-clusterqueue", metav1.DeleteOptions{})
			_ = kueueClient.KueueV1beta1().LocalQueues(testNamespaceWithLabel).Delete(context.TODO(), testQueue, metav1.DeleteOptions{})
			_ = kueueClient.KueueV1beta1().ResourceFlavors().Delete(context.TODO(), "default", metav1.DeleteOptions{})
			_ = kubeClient.CoreV1().Pods(operatorNamespace).Delete(context.TODO(), "curl-metrics-test", metav1.DeleteOptions{})
		})

		It("should manage jobs only in labeled namespaces", func() {
			// Verify webhook configuration
			Eventually(func() error {
				validateWebhookConfig(kubeClient, labelKey, labelValue, "job")
				return nil
			}, operatorReadyTime, operatorPoll).Should(Succeed())

			// Test labeled namespace
			By("creating job in labeled namespace")
			jobWithLabel := builderWithLabel.NewJob()
			createdJob, err := kubeClient.BatchV1().Jobs(testNamespaceWithLabel).Create(context.TODO(), jobWithLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			verifyWorkloadCreated(kueueClient, testNamespaceWithLabel, string(createdJob.UID))

			By("creating job in unlabeled namespace")
			jobWithoutLabel := builderWithoutLabel.NewJob()
			createdUnlabeledJob, err := kubeClient.BatchV1().Jobs(testNamespaceWithoutLabel).Create(context.TODO(), jobWithoutLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Verify job starts normally (no Kueue interference)
			Eventually(func() *batchv1.JobStatus {
				job, _ := kubeClient.BatchV1().Jobs(testNamespaceWithoutLabel).Get(context.TODO(), createdUnlabeledJob.Name, metav1.GetOptions{})
				return &job.Status
			}, operatorReadyTime, operatorPoll).Should(HaveField("Active", BeNumerically(">=", 1)), "Job not started in unlabeled namespace")
		})
		It("should manage pods only in labeled namespaces", func() {
			// Verify webhook configuration
			Eventually(func() error {
				validateWebhookConfig(kubeClient, labelKey, labelValue, "pod")
				return nil
			}, operatorReadyTime, operatorPoll).Should(Succeed())

			// Test labeled namespace
			By("creating pod in labeled namespace")
			podWithLabel := builderWithLabel.NewPod()
			createdPod, err := kubeClient.CoreV1().Pods(testNamespaceWithLabel).Create(context.TODO(), podWithLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			verifyWorkloadCreated(kueueClient, testNamespaceWithLabel, string(createdPod.UID))

			By("creating pod in unlabeled namespace")
			podWithoutLabel := builderWithoutLabel.NewPod()
			createdUnlabeledPod, err := kubeClient.CoreV1().Pods(testNamespaceWithoutLabel).Create(context.TODO(), podWithoutLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Verify pod starts normally (no Kueue interference)
			Eventually(func() corev1.PodPhase {
				pod, _ := kubeClient.CoreV1().Pods(testNamespaceWithoutLabel).Get(context.TODO(), createdUnlabeledPod.Name, metav1.GetOptions{})
				return pod.Status.Phase
			}, operatorReadyTime, operatorPoll).Should(Equal(corev1.PodRunning), "Pod not running in unlabeled namespace")
		})
		It("should manage deployments only in labeled namespaces", func() {
			// Verify webhook configuration
			Eventually(func() error {
				validateWebhookConfig(kubeClient, labelKey, labelValue, "deployment")
				return nil
			}, operatorReadyTime, operatorPoll).Should(Succeed())

			By("creating deployment in labeled namespace")
			deployWithLabel := builderWithLabel.NewDeployment()
			createdDeploy, err := kubeClient.AppsV1().Deployments(testNamespaceWithLabel).Create(context.TODO(), deployWithLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Get the deployment's pod
			var deploymentPod *corev1.Pod
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(testNamespaceWithLabel).List(context.TODO(), metav1.ListOptions{
					LabelSelector: "app=test",
				})
				if err != nil || len(pods.Items) == 0 {
					return fmt.Errorf("no pods found for deployment")
				}
				deploymentPod = &pods.Items[0]
				return nil
			}, operatorReadyTime, operatorPoll).Should(Succeed())

			verifyWorkloadCreated(kueueClient, testNamespaceWithLabel, string(deploymentPod.UID))

			By("verifying deployment pods are available")
			Eventually(func() int32 {
				deploy, _ := kubeClient.AppsV1().Deployments(testNamespaceWithLabel).Get(context.TODO(), createdDeploy.Name, metav1.GetOptions{})
				return deploy.Status.AvailableReplicas
			}, operatorReadyTime, operatorPoll).Should(Equal(int32(1)), "Deployment not available")

			By("creating deployment in unlabeled namespace")
			deployWithoutLabel := builderWithoutLabel.NewDeployment()
			createdUnlabeledDeploy, err := kubeClient.AppsV1().Deployments(testNamespaceWithoutLabel).Create(context.TODO(), deployWithoutLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() int32 {
				deploy, _ := kubeClient.AppsV1().Deployments(testNamespaceWithoutLabel).Get(context.TODO(), createdUnlabeledDeploy.Name, metav1.GetOptions{})
				return deploy.Status.AvailableReplicas
			}, operatorReadyTime, operatorPoll).Should(Equal(int32(1)), "Deployment in unlabeled namespace not available")
		})
		It("should manage statefulsets only in labeled namespaces", func() {
			// Verify webhook configuration
			Eventually(func() error {
				validateWebhookConfig(kubeClient, labelKey, labelValue, "statefulset")
				return nil
			}, operatorReadyTime, operatorPoll).Should(Succeed())

			By("creating statefulset in labeled namespace")
			ssWithLabel := builderWithLabel.NewStatefulSet()
			createdSS, err := kubeClient.AppsV1().StatefulSets(testNamespaceWithLabel).Create(context.TODO(), ssWithLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Wait for StatefulSet to create pod
			var ssPod *corev1.Pod
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(testNamespaceWithLabel).List(context.TODO(), metav1.ListOptions{
					LabelSelector: "app=test",
				})
				if err != nil || len(pods.Items) == 0 {
					return fmt.Errorf("no pods found for statefulset")
				}
				ssPod = &pods.Items[0]
				return nil
			}, operatorReadyTime, operatorPoll).Should(Succeed())

			verifyWorkloadCreated(kueueClient, testNamespaceWithLabel, string(ssPod.UID))

			By("verifying statefulset pods are running")
			Eventually(func() int32 {
				ss, _ := kubeClient.AppsV1().StatefulSets(testNamespaceWithLabel).Get(context.TODO(), createdSS.Name, metav1.GetOptions{})
				return ss.Status.ReadyReplicas
			}, operatorReadyTime, operatorPoll).Should(Equal(int32(1)), "StatefulSet not ready")

			By("creating statefulset in unlabeled namespace")
			ssWithoutLabel := builderWithoutLabel.NewStatefulSet()
			createdUnlabeledSS, err := kubeClient.AppsV1().StatefulSets(testNamespaceWithoutLabel).Create(context.TODO(), ssWithoutLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() int32 {
				ss, _ := kubeClient.AppsV1().StatefulSets(testNamespaceWithoutLabel).Get(context.TODO(), createdUnlabeledSS.Name, metav1.GetOptions{})
				return ss.Status.ReadyReplicas
			}, operatorReadyTime, operatorPoll).Should(Equal(int32(1)), "StatefulSet in unlabeled namespace not ready")
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
			Expect(testutils.CreateWorkload(kueueClient, testNamespaceWithLabel, testQueue, "test-workload")).To(Succeed())
			Expect(testutils.CreateWorkload(kueueClient, testNamespaceWithLabel, testQueue, "test-workload-2")).To(Succeed())

			By("Creating curl test pod")
			curlPod := testutils.MakeCurlMetricsPod(operatorNamespace)
			_, err = kubeClient.CoreV1().Pods(operatorNamespace).Create(ctx, curlPod.Obj(), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create curl metrics test pod")

			Eventually(func() error {
				pod, err := kubeClient.CoreV1().Pods(operatorNamespace).Get(ctx, podName, metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get pod: %w", err)
				}
				if pod.Status.Phase != corev1.PodRunning {
					return fmt.Errorf("pod %q not ready, phase: %s", podName, pod.Status.Phase)
				}
				return nil
			}, operatorReadyTime, operatorPoll).Should(Succeed(), "curl-metrics-test pod did not become ready")

			Eventually(func() error {
				metricsOutput, _, err := Kexecute(ctx, clients.RestConfig, kubeClient, operatorNamespace, podName, containerName,
					[]string{
						"/bin/sh", "-c",
						fmt.Sprintf(
							"curl -s --cacert %s/ca.crt -H \"Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)\" https://%s.%s.svc.cluster.local:8443/metrics",
							certMountPath,
							metricsServiceName,
							operatorNamespace,
						),
					})
				if err != nil {
					return fmt.Errorf("exec into pod failed: %w", err)
				}

				if !strings.Contains(string(metricsOutput), "kueue_quota_reserved_workloads_total") {
					return fmt.Errorf("expected metric not found in output")
				}

				return nil
			}, operatorReadyTime, operatorPoll).Should(Succeed(), "expected HTTP 200 OK from metrics endpoint")
		})
	})

	When("cleaning up Kueue resources", func() {
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

			_, err := kueueClientset.KueueV1alpha1().Kueues().Get(ctx, kueueName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")

			err = kueueClientset.KueueV1alpha1().Kueues().Delete(ctx, kueueName, metav1.DeleteOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to delete Kueue instance")

			Eventually(func() error {
				_, err := kueueClientset.KueueV1alpha1().Kueues().Get(ctx, kueueName, metav1.GetOptions{})
				if err == nil {
					return fmt.Errorf("Kueue instance %s still exists", kueueName)
				}

				clusterRoles, _ := kubeClient.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{})
				klog.Infof("Verifying removal of Cluster Roles")
				for _, role := range clusterRoles.Items {
					if strings.Contains(role.Name, "kueue") && role.Name != operatorNamespace {
						return fmt.Errorf("ClusterRole %s still exists", role.Name)
					}
				}

				clusterRoleBindings, _ := kubeClient.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
				klog.Infof("Verifying removal of Cluster Role Bindings")
				for _, binding := range clusterRoleBindings.Items {
					if strings.Contains(binding.Name, "kueue") && binding.Name != operatorNamespace {
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

				return nil
			}, operatorReadyTime, operatorPoll).Should(Succeed(), "Resources were not cleaned up properly")
		})

		It("should redeploy Kueue instance and verify pod is ready", func() {
			ctx := context.TODO()

			klog.Infof("Redeploying Kueue instance by applying 08_kueue_default.yaml")
			requiredObj, err := runtime.Decode(ssscheme.Codecs.UniversalDecoder(ssv1.SchemeGroupVersion), bindata.MustAsset("assets/08_kueue_default.yaml"))
			Expect(err).ToNot(HaveOccurred(), "Failed to decode 08_kueue_default.yaml")

			requiredSS := requiredObj.(*ssv1.Kueue)
			_, err = kueueClientset.KueueV1alpha1().Kueues().Create(ctx, requiredSS, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to create Kueue instance")

			Eventually(func() error {
				_, err := kueueClientset.KueueV1alpha1().Kueues().Get(ctx, kueueName, metav1.GetOptions{})
				return err
			}, operatorReadyTime, operatorPoll).Should(Succeed(), "Kueue instance was not created successfully")

			Eventually(func() error {
				podItems, err := kubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
				if err != nil {
					klog.Errorf("Unable to list pods: %v", err)
					return nil
				}
				for _, pod := range podItems.Items {
					if !strings.HasPrefix(pod.Name, "kueue-") {
						continue
					}
					klog.Infof("Checking pod: %v, phase: %v, deletionTS: %v\n", pod.Name, pod.Status.Phase, pod.GetDeletionTimestamp())
					if pod.Status.Phase == corev1.PodRunning && pod.GetDeletionTimestamp() == nil {
						return nil
					}
				}
				return fmt.Errorf("pod is not ready")
			}, operatorReadyTime, operatorPoll).Should(Succeed(), "kueue pod failed to be ready")
		})
	})
})

func validateWebhookConfig(kubeClient *kubernetes.Clientset, labelKey, labelValue, framework string) {
	validateWebhook := func(webhooks []admissionregistrationv1.ValidatingWebhookConfiguration) {
		for i, wh := range webhooks {
			if strings.HasPrefix(wh.Name, "v"+framework) && strings.Contains(wh.Name, ".kb.io") {
				Expect(wh.Webhooks[i].NamespaceSelector).NotTo(BeNil())
				Expect(wh.Webhooks[i].NamespaceSelector.MatchExpressions).To(
					ContainElement(metav1.LabelSelectorRequirement{
						Key:      labelKey,
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{labelValue},
					}),
				)
			}
		}
	}

	mutatingWebhook := func(webhooks []admissionregistrationv1.MutatingWebhookConfiguration) {
		for i, wh := range webhooks {
			if strings.HasPrefix(wh.Name, "m"+framework) && strings.Contains(wh.Name, ".kb.io") {
				Expect(wh.Webhooks[i].NamespaceSelector).NotTo(BeNil())
				Expect(wh.Webhooks[i].NamespaceSelector.MatchExpressions).To(
					ContainElement(metav1.LabelSelectorRequirement{
						Key:      labelKey,
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{labelValue},
					}),
				)
			}
		}
	}

	vwh, err := kubeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{})
	Expect(err).NotTo(HaveOccurred())
	validateWebhook(vwh.Items)

	mwh, err := kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{})
	Expect(err).NotTo(HaveOccurred())
	mutatingWebhook(mwh.Items)
}

func verifyWorkloadCreated(kueueClient *upstreamkueueclient.Clientset, namespace, uid string) {
	// Verify that a Workload with the expected label is created and admitted
	var workload *kueuev1beta1.Workload
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
		workload = &workloads.Items[0]
		return nil
	}, operatorReadyTime, operatorPoll).Should(Succeed())

	Eventually(func() bool {
		updatedWorkload, err := kueueClient.KueueV1beta1().Workloads(namespace).Get(context.TODO(), workload.Name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return apimeta.IsStatusConditionTrue(updatedWorkload.Status.Conditions, kueuev1beta1.WorkloadAdmitted)
	}, operatorReadyTime, operatorPoll).Should(BeTrue(), "Workload not admitted")
}

func deployOperator() error {
	kubeClient := clients.KubeClient
	apiExtClient := clients.APIExtClient
	ssClient := clients.KueueClient

	eventRecorder := events.NewKubeRecorder(kubeClient.CoreV1().Events(namespace), "test-e2e", &corev1.ObjectReference{}, clock.RealClock{})

	ctx, cancelFnc := context.WithCancel(context.TODO())
	defer cancelFnc()

	assets := []struct {
		path           string
		readerAndApply func(objBytes []byte) error
	}{
		{
			path: "assets/00_kueue-operator.crd.yaml",
			readerAndApply: func(objBytes []byte) error {
				_, _, err := resourceapply.ApplyCustomResourceDefinitionV1(ctx, apiExtClient, eventRecorder, resourceread.ReadCustomResourceDefinitionV1OrDie(objBytes))
				return err
			},
		},
		{
			path: "assets/01_namespace.yaml",
			readerAndApply: func(objBytes []byte) error {
				_, _, err := resourceapply.ApplyNamespace(ctx, kubeClient.CoreV1(), eventRecorder, resourceread.ReadNamespaceV1OrDie(objBytes))
				return err
			},
		},
		{
			path: "assets/02_clusterrole.yaml",
			readerAndApply: func(objBytes []byte) error {
				_, _, err := resourceapply.ApplyClusterRole(ctx, kubeClient.RbacV1(), eventRecorder, resourceread.ReadClusterRoleV1OrDie(objBytes))
				return err
			},
		},
		{
			path: "assets/02_role.yaml",
			readerAndApply: func(objBytes []byte) error {
				_, _, err := resourceapply.ApplyRole(ctx, kubeClient.RbacV1(), eventRecorder, resourceread.ReadRoleV1OrDie(objBytes))
				return err
			},
		},
		{
			path: "assets/03_clusterrolebinding.yaml",
			readerAndApply: func(objBytes []byte) error {
				_, _, err := resourceapply.ApplyClusterRoleBinding(ctx, kubeClient.RbacV1(), eventRecorder, resourceread.ReadClusterRoleBindingV1OrDie(objBytes))
				return err
			},
		},
		{
			path: "assets/03_rolebinding.yaml",
			readerAndApply: func(objBytes []byte) error {
				_, _, err := resourceapply.ApplyRoleBinding(ctx, kubeClient.RbacV1(), eventRecorder, resourceread.ReadRoleBindingV1OrDie(objBytes))
				return err
			},
		},
		{
			path: "assets/04_serviceaccount.yaml",
			readerAndApply: func(objBytes []byte) error {
				_, _, err := resourceapply.ApplyServiceAccount(ctx, kubeClient.CoreV1(), eventRecorder, resourceread.ReadServiceAccountV1OrDie(objBytes))
				return err
			},
		},
		{
			path: "assets/05_clusterrole_kueue-batch.yaml",
			readerAndApply: func(objBytes []byte) error {
				_, _, err := resourceapply.ApplyClusterRole(ctx, kubeClient.RbacV1(), eventRecorder, resourceread.ReadClusterRoleV1OrDie(objBytes))
				return err
			},
		},
		{
			path: "assets/06_clusterrole_kueue-admin.yaml",
			readerAndApply: func(objBytes []byte) error {
				_, _, err := resourceapply.ApplyClusterRole(ctx, kubeClient.RbacV1(), eventRecorder, resourceread.ReadClusterRoleV1OrDie(objBytes))
				return err
			},
		},

		{
			path: "assets/07_deployment.yaml",
			readerAndApply: func(objBytes []byte) error {
				required := resourceread.ReadDeploymentV1OrDie(objBytes)
				operatorImage := os.Getenv("OPERATOR_IMAGE")
				kueueImage := os.Getenv("KUEUE_IMAGE")

				required.Spec.Template.Spec.Containers[0].Image = operatorImage
				required.Spec.Template.Spec.Containers[0].Env[2].Value = kueueImage
				_, _, err := resourceapply.ApplyDeployment(
					ctx,
					kubeClient.AppsV1(),
					eventRecorder,
					required,
					1000, // any random high number
				)
				return err
			},
		},
		{
			path: "assets/08_kueue_default.yaml",
			readerAndApply: func(objBytes []byte) error {
				requiredObj, err := runtime.Decode(ssscheme.Codecs.UniversalDecoder(ssv1.SchemeGroupVersion), objBytes)
				if err != nil {
					klog.Errorf("Unable to decode assets/08_kueue_default.yaml: %v", err)
					return err
				}
				requiredSS := requiredObj.(*ssv1.Kueue)
				_, err = ssClient.KueueV1alpha1().Kueues().Create(ctx, requiredSS, metav1.CreateOptions{})
				return err
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
	}, operatorReadyTime, operatorPoll).Should(Succeed(), "assets should be deployed")

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
