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

package operator

import (
	"bytes"
	"context"
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

var _ = Describe("Kueue Operator", func() {
	// AfterEach(func() {
	// 	Expect(kubeClient.CoreV1().Namespaces().Delete(context.TODO(), namespace, metav1.DeleteOptions{})).To(Succeed())
	// })
	When("a subscription is created", func() {
		//create NS, operatorgroup, subscription
		//It("creates all the objects")
		It("operator pods should be ready", Label("day-zero"), func() {
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

	})

	When("default kueue CR is created", func() {
		// create kueue CR that has all compatible options enabled
		// It("kueue pods should be ready", Label("day-zero"), func() {
		// 	Expect(deployOperand()).To(Succeed(), "operand deployment should not fail")
		// })
		// deployOperand()

		It("should set ReadyReplicas in operator status and handle degraded condition", Label("day-zero"), func() {
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

		It("kueue operator deployment should contain priority class", Label("day-zero"), func() {
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

		It("kueue deployment should contain priority class and have no resource limits set", Label("day-zero"), func() {
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

		It("Verifying that no v1alpha Kueue CRDs are installed", Label("day-zero"), func() {
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

		It("verify webhook readiness", Label("day-zero"), func() {
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

		It("verify that deny-all network policy is present", Label("day-zero"), func() {
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

		It("kueues can be created and used", func() { 
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
			// defer jobCleanupFn()

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


	})

	When("kueue CR is deleted", func() {
		var (
			kueueName      = "cluster"
			kueueClientset *kueueclient.Clientset
			dynamicClient  dynamic.Interface
		)

		BeforeEach(func() {
			kueueClientset = clients.KueueClient
			dynamicClient = clients.DynamicClient
		})

		It("should delete Kueue instance and verify cleanup", Label("day-zero"), func() {
			ctx := context.TODO()

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
