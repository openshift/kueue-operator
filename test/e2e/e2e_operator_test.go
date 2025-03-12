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

	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
)

var _ = Describe("Kueue Operator", Ordered, func() {
	var namespace = "openshift-kueue-operator"
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

			_, err := kueueClientset.KueueV1alpha1().Kueues(namespace).Get(ctx, kueueName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")

			err = kueueClientset.KueueV1alpha1().Kueues(namespace).Delete(ctx, kueueName, metav1.DeleteOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to delete Kueue instance")

			Eventually(func() error {
				_, err := kueueClientset.KueueV1alpha1().Kueues(namespace).Get(ctx, kueueName, metav1.GetOptions{})
				if err == nil {
					return fmt.Errorf("Kueue instance %s still exists", kueueName)
				}

				clusterRoles, _ := kubeClientset.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{})
				klog.Infof("Verifying removal of Cluster Roles")
				for _, role := range clusterRoles.Items {
					if strings.Contains(role.Name, "kueue") && role.Name != "openshift-kueue-operator" {
						return fmt.Errorf("ClusterRole %s still exists", role.Name)
					}
				}

				clusterRoleBindings, _ := kubeClientset.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
				klog.Infof("Verifying removal of Cluster Role Bindings")
				for _, binding := range clusterRoleBindings.Items {
					if strings.Contains(binding.Name, "kueue") && binding.Name != "openshift-kueue-operator" {
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
			_, err = kueueClientset.KueueV1alpha1().Kueues(requiredSS.Namespace).Create(ctx, requiredSS, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to create Kueue instance")

			Eventually(func() error {
				_, err := kueueClientset.KueueV1alpha1().Kueues(namespace).Get(ctx, kueueName, metav1.GetOptions{})
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
				_, err = ssClient.KueueV1alpha1().Kueues(requiredSS.Namespace).Create(ctx, requiredSS, metav1.CreateOptions{})
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
