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
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ssv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1alpha1"
	kueueclient "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned"
	ssscheme "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned/scheme"
	"github.com/openshift/kueue-operator/test/e2e/bindata"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	batchv1 "k8s.io/api/batch/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	upstreamkueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
	//+kubebuilder:scaffold:imports
)

const (
	operatorReadyTime time.Duration = 2 * time.Minute
	operatorPoll                    = 10 * time.Second
	operatorNamespace               = "openshift-kueue-operator"
)

var _ = Describe("Kueue Operator", Ordered, func() {
	var namespace = operatorNamespace
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

		It("Verifying that no v1alpha Kueue CRDs are installed", func() {
			Eventually(func() error {
				ctx := context.TODO()
				crdList, err := getApiExtensionKubeClient().CustomResourceDefinitions().List(ctx, metav1.ListOptions{})
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
			kubeClientset := getKubeClientOrDie()
			Eventually(func() error {
				_, err := kubeClientset.CoreV1().Endpoints(operatorNamespace).Get(
					context.TODO(),
					"kueue-webhook-service",
					metav1.GetOptions{},
				)
				return err
			}, operatorReadyTime, operatorPoll).Should(Succeed(), "webhook service is not ready")

			Eventually(func() error {
				endpoints, err := kubeClientset.CoreV1().Endpoints(operatorNamespace).Get(
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
				vwh, err := kubeClientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(
					context.TODO(),
					"kueue-validating-webhook-configuration",
					metav1.GetOptions{},
				)
				Expect(err).NotTo(HaveOccurred(), "Failed to get validating webhook configuration")
				Expect(vwh.Name).To(Equal("kueue-validating-webhook-configuration"))

				// Validate mutating webhook configuration
				mwh, err := kubeClientset.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(
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
			kubeClientset             *kubernetes.Clientset
			kueueClient               *upstreamkueueclient.Clientset
			testQueue                 = "test-queue"
		)

		BeforeAll(func() {
			kueueClient = getUpstreamKueueClient()
			kubeClientset = getKubeClientOrDie()
			var err error
			// Create namespaces
			_, err = kubeClientset.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNamespaceWithLabel,
					Labels: map[string]string{
						labelKey: labelValue,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			_, err = kubeClientset.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNamespaceWithoutLabel,
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			cq := &kueuev1beta1.ClusterQueue{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusterqueue",
				},
				Spec: kueuev1beta1.ClusterQueueSpec{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							labelKey: labelValue,
						},
					},
					ResourceGroups: []kueuev1beta1.ResourceGroup{
						{
							CoveredResources: []corev1.ResourceName{"cpu", "memory"},
							Flavors: []kueuev1beta1.FlavorQuotas{
								{
									Name: "default",
									Resources: []kueuev1beta1.ResourceQuota{
										{
											Name:         "cpu",
											NominalQuota: resource.MustParse("100"),
										},
										{
											Name:         "memory",
											NominalQuota: resource.MustParse("100Gi"),
										},
									},
								},
							},
						},
					},
				},
			}

			_, err = kueueClient.KueueV1beta1().ClusterQueues().Create(context.TODO(), cq, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Create LocalQueue in managed namespace
			lq := &kueuev1beta1.LocalQueue{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testQueue,
					Namespace: testNamespaceWithLabel,
				},
				Spec: kueuev1beta1.LocalQueueSpec{
					ClusterQueue: "test-clusterqueue",
				},
			}
			_, err = kueueClient.KueueV1beta1().LocalQueues(testNamespaceWithLabel).Create(context.TODO(), lq, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			rf := &kueuev1beta1.ResourceFlavor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
				},
				Spec: kueuev1beta1.ResourceFlavorSpec{
					NodeLabels: map[string]string{
						"kueue.x-k8s.io/default-flavor": "true",
					},
				},
			}
			_, err = kueueClient.KueueV1beta1().ResourceFlavors().Create(context.TODO(), rf, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterAll(func() {
			_ = kubeClientset.CoreV1().Namespaces().Delete(context.TODO(), testNamespaceWithLabel, metav1.DeleteOptions{})
			_ = kubeClientset.CoreV1().Namespaces().Delete(context.TODO(), testNamespaceWithoutLabel, metav1.DeleteOptions{})
			_ = kueueClient.KueueV1beta1().ClusterQueues().Delete(context.TODO(), "test-clusterqueue", metav1.DeleteOptions{})
			_ = kueueClient.KueueV1beta1().LocalQueues(testNamespaceWithLabel).Delete(context.TODO(), testQueue, metav1.DeleteOptions{})
			_ = kueueClient.KueueV1beta1().ResourceFlavors().Delete(context.TODO(), "default", metav1.DeleteOptions{})
		})

		It("should manage jobs only in labeled namespaces", func() {
			// Verify webhook configuration
			Eventually(func() error {
				validateWebhookConfig(kubeClientset, labelKey, labelValue)
				return nil
			}, operatorReadyTime, operatorPoll).Should(Succeed())

			// Test labeled namespace
			By("creating job in labeled namespace")
			jobWithLabel := createTestJob(testNamespaceWithLabel, testQueue)
			createdJob, err := kubeClientset.BatchV1().Jobs(testNamespaceWithLabel).Create(context.TODO(), jobWithLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Verify Kueue creates a Workload
			var workload *kueuev1beta1.Workload
			Eventually(func() error {
				workloads, err := kueueClient.KueueV1beta1().Workloads(testNamespaceWithLabel).List(context.TODO(), metav1.ListOptions{
					LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", string(createdJob.UID)),
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

			// Verify workload gets admitted
			Eventually(func() bool {
				updatedWorkload, err := kueueClient.KueueV1beta1().Workloads(testNamespaceWithLabel).Get(context.TODO(), workload.Name, metav1.GetOptions{})
				if err != nil {
					return false
				}
				return apimeta.IsStatusConditionTrue(updatedWorkload.Status.Conditions, kueuev1beta1.WorkloadAdmitted)
			}, operatorReadyTime, operatorPoll).Should(BeTrue(), "Workload not admitted")

			By("creating job in unlabeled namespace")
			jobWithoutLabel := createTestJob(testNamespaceWithoutLabel, testQueue)
			createdUnlabeledJob, err := kubeClientset.BatchV1().Jobs(testNamespaceWithoutLabel).Create(context.TODO(), jobWithoutLabel, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Verify job starts normally (no Kueue interference)
			Eventually(func() *batchv1.JobStatus {
				job, _ := kubeClientset.BatchV1().Jobs(testNamespaceWithoutLabel).Get(context.TODO(), createdUnlabeledJob.Name, metav1.GetOptions{})
				return &job.Status
			}, operatorReadyTime, operatorPoll).Should(HaveField("Active", BeNumerically(">=", 1)), "Job not started in unlabeled namespace")
		})
	})

	When("cleaning up Kueue resources", func() {
		var (
			kueueName      = "cluster"
			kueueClientset *kueueclient.Clientset
			kubeClientset  *kubernetes.Clientset
			dynamicClient  dynamic.Interface
		)

		BeforeEach(func() {
			kueueClientset = getKueueClient()
			kubeClientset = getKubeClientOrDie()
			dynamicClient = getDynamicClient()
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

				clusterRoles, _ := kubeClientset.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{})
				klog.Infof("Verifying removal of Cluster Roles")
				for _, role := range clusterRoles.Items {
					if strings.Contains(role.Name, "kueue") && role.Name != operatorNamespace {
						return fmt.Errorf("ClusterRole %s still exists", role.Name)
					}
				}

				clusterRoleBindings, _ := kubeClientset.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
				klog.Infof("Verifying removal of Cluster Role Bindings")
				for _, binding := range clusterRoleBindings.Items {
					if strings.Contains(binding.Name, "kueue") && binding.Name != operatorNamespace {
						return fmt.Errorf("ClusterRoleBinding %s still exists", binding.Name)
					}
				}

				mutatingwebhooks, _ := kubeClientset.AdmissionregistrationV1().MutatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
				klog.Infof("Verifying removal of Mutating Webhooks")
				for _, wh := range mutatingwebhooks.Items {
					if strings.Contains(wh.Name, "kueue") {
						return fmt.Errorf("MutatingWebhookConfiguration %s still exists", wh.Name)
					}
				}

				validatingwebhooks, _ := kubeClientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
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

func createTestJob(namespace, queueName string) *batchv1.Job {
	labels := map[string]string{}
	if namespace == "kueue-managed-test" {
		labels["kueue.x-k8s.io/queue-name"] = queueName
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-job-",
			Namespace:    namespace,
			Labels:       labels,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "test-container",
							Image:   "busybox",
							Command: []string{"sleep", "3600"},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									"cpu":    resource.MustParse("100m"),
									"memory": resource.MustParse("100Mi"),
								},
							},
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}
}

func validateWebhookConfig(kubeClient *kubernetes.Clientset, labelKey, labelValue string) {
	validateWebhook := func(webhooks []admissionregistrationv1.ValidatingWebhookConfiguration) {
		for i, wh := range webhooks {
			if strings.HasPrefix(wh.Name, "v") && strings.Contains(wh.Name, ".kb.io") {
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
			if strings.HasPrefix(wh.Name, "m") && strings.Contains(wh.Name, ".kb.io") {
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

func getDynamicClient() dynamic.Interface {
	kubeconfig := os.Getenv("KUBECONFIG")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		klog.Errorf("Unable to build config: %v", err)
		os.Exit(1)
	}

	client, err := dynamic.NewForConfig(config)
	if err != nil {
		klog.Errorf("Unable to build dynamic client: %v", err)
		os.Exit(1)
	}

	return client
}

func getKubeClientOrDie() *kubernetes.Clientset {
	kubeconfig := os.Getenv("KUBECONFIG")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		klog.Errorf("Unable to build config: %v", err)
		os.Exit(1)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Errorf("Unable to build client: %v", err)
		os.Exit(1)
	}
	return client
}

func getApiExtensionKubeClient() *apiextv1.ApiextensionsV1Client {
	kubeconfig := os.Getenv("KUBECONFIG")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		klog.Errorf("Unable to build config: %v", err)
		os.Exit(1)
	}
	client, err := apiextv1.NewForConfig(config)
	if err != nil {
		klog.Errorf("Unable to build client: %v", err)
		os.Exit(1)
	}
	return client
}

func getKueueClient() *kueueclient.Clientset {
	kubeconfig := os.Getenv("KUBECONFIG")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		klog.Errorf("Unable to build config: %v", err)
		os.Exit(1)
	}
	client, err := kueueclient.NewForConfig(config)
	if err != nil {
		klog.Errorf("Unable to build client: %v", err)
		os.Exit(1)
	}
	return client
}

func getUpstreamKueueClient() *upstreamkueueclient.Clientset {
	kubeconfig := os.Getenv("KUBECONFIG")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		klog.Errorf("Unable to build config: %v", err)
		os.Exit(1)
	}
	client, err := upstreamkueueclient.NewForConfig(config)
	if err != nil {
		klog.Errorf("Unable to build client: %v", err)
		os.Exit(1)
	}
	return client
}

func deployOperator() error {
	kubeClient := getKubeClientOrDie()
	apiExtClient := getApiExtensionKubeClient()
	ssClient := getKueueClient()

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
			path: "assets/03_clusterrolebinding.yaml",
			readerAndApply: func(objBytes []byte) error {
				_, _, err := resourceapply.ApplyClusterRoleBinding(ctx, kubeClient.RbacV1(), eventRecorder, resourceread.ReadClusterRoleBindingV1OrDie(objBytes))
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
