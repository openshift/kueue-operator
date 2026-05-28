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

package testutils

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ssv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
	upstreamkueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"
)

const (
	WebhookStabilityPoll     = 3 * time.Second
	WebhookRequiredSuccesses = 3
	ConfigRestoreTimeout     = 30 * time.Second
	ConfigRestorePoll        = 2 * time.Second
)

// ApplyKueueConfig updates the Kueue CR config and waits for the operand to be
// redeployed and healthy.
func ApplyKueueConfig(ctx context.Context, config ssv1.KueueConfiguration, tc *TestClients) {
	By("Fetching Kueue Instance")
	kueueInstance, err := tc.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")

	configChanged := !reflect.DeepEqual(kueueInstance.Spec.Config, config)

	initialDeployment, err := tc.KubeClient.AppsV1().Deployments(OperatorNamespace).Get(ctx, "kueue-controller-manager", metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred(), "Failed to fetch kueue-controller-manager deployment")
	initialResourceVersion := initialDeployment.ResourceVersion

	By("Updating Kueue config (with conflict retry)")
	Eventually(func() error {
		instance, err := tc.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to fetch Kueue instance: %w", err)
		}
		configChanged = !reflect.DeepEqual(instance.Spec.Config, config)
		instance.Spec.Config = config
		_, err = tc.KueueClient.KueueV1().Kueues().Update(ctx, instance, metav1.UpdateOptions{})
		return err
	}, ConfigRestoreTimeout, ConfigRestorePoll).Should(Succeed(), "Failed to update Kueue config")

	if configChanged {
		By(fmt.Sprintf("Waiting for kueue-controller-manager resource version to change from %s", initialResourceVersion))
		Eventually(func() error {
			dep, err := tc.KubeClient.AppsV1().Deployments(OperatorNamespace).Get(ctx, "kueue-controller-manager", metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("unable to fetch manager deployment: %v", err)
			}
			if dep.ResourceVersion == initialResourceVersion {
				return fmt.Errorf("deployment resource version has not changed yet (still %s)", initialResourceVersion)
			}
			return nil
		}, OperatorReadyTime, OperatorPoll).Should(Succeed(), "kueue-controller-manager deployment resource version did not change")
	} else {
		By("Skipping deployment update wait - config unchanged")
	}

	WaitForManagerReady(ctx, tc.KubeClient)

	By("Waiting for webhook configurations to exist")
	Eventually(func() error {
		_, err := tc.KubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, "kueue-mutating-webhook-configuration", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("mutating webhook configuration not found: %w", err)
		}
		_, err = tc.KubeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(ctx, "kueue-validating-webhook-configuration", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("validating webhook configuration not found: %w", err)
		}
		return nil
	}, OperatorReadyTime, OperatorPoll).Should(Succeed(), "webhook configurations not found")

	WaitForWebhookReady(ctx, tc.KubeClient)

	By("Verifying kueue CRDs are servable")
	Eventually(func() error {
		_, err := tc.UpstreamKueueClient.KueueV1beta2().ClusterQueues().List(ctx, metav1.ListOptions{Limit: 1})
		return err
	}, OperatorReadyTime, OperatorPoll).Should(Succeed(), "kueue CRDs not servable after reconfiguration")
}

// WaitForManagerReady waits for the kueue-controller-manager deployment to have
// all replicas ready.
func WaitForManagerReady(ctx context.Context, kubeClient *kubernetes.Clientset) {
	Eventually(func() error {
		dep, err := kubeClient.AppsV1().Deployments(OperatorNamespace).Get(ctx, "kueue-controller-manager", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("unable to fetch manager deployment: %v", err)
		}
		By(fmt.Sprintf("Checking if deployment replicas: %v matches amount of ready replicas: %v", dep.Status.Replicas, dep.Status.ReadyReplicas))
		if dep.Status.ReadyReplicas == dep.Status.Replicas {
			return nil
		}
		return fmt.Errorf("deployment is not ready")
	}, OperatorReadyTime, OperatorPoll).Should(Succeed(), "kueue-controller-manager deployment failed to be ready")
}

// WaitForWebhookReady waits for the webhook to be responding by creating test
// jobs and checking for consecutive successes.
func WaitForWebhookReady(ctx context.Context, kubeClient *kubernetes.Clientset) {
	By("Waiting for webhook to handle requests successfully")

	consecutiveSuccesses := 0

	Eventually(func() error {
		endpointSlices, err := kubeClient.DiscoveryV1().EndpointSlices(OperatorNamespace).List(
			ctx,
			metav1.ListOptions{
				LabelSelector: "kubernetes.io/service-name=kueue-webhook-service",
			},
		)
		if err != nil {
			consecutiveSuccesses = 0
			return fmt.Errorf("webhook service endpointslices not found: %w", err)
		}

		readyEndpointCount := 0
		for _, slice := range endpointSlices.Items {
			for _, endpoint := range slice.Endpoints {
				if endpoint.Conditions.Ready != nil && *endpoint.Conditions.Ready {
					readyEndpointCount++
				}
			}
		}
		if readyEndpointCount < 2 {
			consecutiveSuccesses = 0
			return fmt.Errorf("webhook service has %d ready endpoints, need 2", readyEndpointCount)
		}

		testJob := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "webhook-test-",
				Namespace:    OperatorNamespace,
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						RestartPolicy: corev1.RestartPolicyNever,
						Containers: []corev1.Container{
							{
								Name:    "test",
								Image:   "busybox",
								Command: []string{"true"},
							},
						},
					},
				},
			},
		}

		job, err := kubeClient.BatchV1().Jobs(OperatorNamespace).Create(ctx, testJob, metav1.CreateOptions{})
		if err == nil {
			klog.Infof("Webhook test successful, cleaning up test job: %s", job.Name)
			_ = kubeClient.BatchV1().Jobs(OperatorNamespace).Delete(ctx, job.Name, metav1.DeleteOptions{
				PropagationPolicy: func() *metav1.DeletionPropagation {
					p := metav1.DeletePropagationBackground
					return &p
				}(),
			})
			consecutiveSuccesses++
			if consecutiveSuccesses >= WebhookRequiredSuccesses {
				klog.Infof("Webhook stable after %d consecutive successes", consecutiveSuccesses)
				return nil
			}
			klog.Infof("Webhook success %d/%d", consecutiveSuccesses, WebhookRequiredSuccesses)
			return fmt.Errorf("waiting for webhook stability (%d/%d successes)", consecutiveSuccesses, WebhookRequiredSuccesses)
		}

		consecutiveSuccesses = 0

		if strings.Contains(err.Error(), "failed calling webhook") ||
			strings.Contains(err.Error(), "failed to call webhook") ||
			strings.Contains(err.Error(), "context deadline exceeded") ||
			strings.Contains(err.Error(), "connection refused") {
			klog.Infof("Webhook not ready yet: %v", err)
			return fmt.Errorf("webhook not responding: %w", err)
		}

		klog.Infof("Webhook is responding (returned error: %v)", err)
		consecutiveSuccesses++
		if consecutiveSuccesses >= WebhookRequiredSuccesses {
			klog.Infof("Webhook stable after %d consecutive successes", consecutiveSuccesses)
			return nil
		}
		klog.Infof("Webhook success %d/%d", consecutiveSuccesses, WebhookRequiredSuccesses)
		return fmt.Errorf("waiting for webhook stability (%d/%d successes)", consecutiveSuccesses, WebhookRequiredSuccesses)
	}, OperatorReadyTime, WebhookStabilityPoll).Should(Succeed(), "webhook failed to respond to requests")
}

// DeployOperand creates the Kueue CR, waits for the operand to be running,
// CRDs to be established, and webhooks to be ready.
func DeployOperand(tc *TestClients) error {
	ctx, cancelFnc := context.WithCancel(context.TODO())
	defer cancelFnc()

	Eventually(func() error {
		klog.Infof("Creating Kueue instance")
		requiredSS := NewKueueDefault()
		_, err := tc.KueueClient.KueueV1().Kueues().Create(ctx, requiredSS.GetKueue(), metav1.CreateOptions{})
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				return nil
			}
			return err
		}
		return nil
	}, OperatorReadyTime, OperatorPoll).Should(Succeed(), "Kueue instance should be deployed")

	Eventually(func() error {
		podItems, err := tc.KubeClient.CoreV1().Pods(OperatorNamespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("unable to list pods: %v", err)
		}
		for _, pod := range podItems.Items {
			if !strings.HasPrefix(pod.Name, "kueue-controller-manager") {
				continue
			}
			klog.Infof("Checking pod: %v, phase: %v, deletionTS: %v\n", pod.Name, pod.Status.Phase, pod.GetDeletionTimestamp())
			if pod.Status.Phase == corev1.PodRunning && pod.GetDeletionTimestamp() == nil &&
				len(pod.Status.ContainerStatuses) > 0 && pod.Status.ContainerStatuses[0].Ready {
				return nil
			}
		}
		return fmt.Errorf("pod is not ready")
	}, OperatorReadyTime, OperatorPoll).Should(Succeed(), "kueue pod failed to be ready")

	By("Waiting for all Kueue CRDs to be registered")
	requiredCRDs := []string{
		"clusterqueues.kueue.x-k8s.io",
		"cohorts.kueue.x-k8s.io",
		"localqueues.kueue.x-k8s.io",
		"multikueueconfigs.kueue.x-k8s.io",
		"provisioningrequestconfigs.kueue.x-k8s.io",
		"resourceflavors.kueue.x-k8s.io",
		"topologies.kueue.x-k8s.io",
		"workloadpriorityclasses.kueue.x-k8s.io",
		"workloads.kueue.x-k8s.io",
	}

	Eventually(func() error {
		crdList, err := tc.APIExtClient.CustomResourceDefinitions().List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list CRDs: %w", err)
		}

		foundCRDs := make(map[string]bool)
		for _, crd := range crdList.Items {
			foundCRDs[crd.Name] = true
		}

		var missingCRDs []string
		for _, requiredCRD := range requiredCRDs {
			if !foundCRDs[requiredCRD] {
				missingCRDs = append(missingCRDs, requiredCRD)
			}
		}

		if len(missingCRDs) > 0 {
			return fmt.Errorf("missing Kueue CRDs: %v", missingCRDs)
		}

		for _, requiredCRD := range requiredCRDs {
			crd, err := tc.APIExtClient.CustomResourceDefinitions().Get(ctx, requiredCRD, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get CRD %s: %w", requiredCRD, err)
			}

			established := false
			for _, condition := range crd.Status.Conditions {
				if condition.Type == apiextensionsv1.Established && condition.Status == apiextensionsv1.ConditionTrue {
					established = true
					break
				}
			}

			if !established {
				return fmt.Errorf("CRD %s is not established yet", requiredCRD)
			}
		}

		klog.Infof("All %d Kueue CRDs are registered and established", len(requiredCRDs))
		return nil
	}, OperatorReadyTime, OperatorPoll).Should(Succeed(), "Kueue CRDs failed to be registered")

	WaitForWebhookReady(ctx, tc.KubeClient)

	return nil
}

// RestoreKueueConfig restores a Kueue CR config with retry on resourceVersion
// conflicts and waits for the operand to be ready.
func RestoreKueueConfig(ctx context.Context, config ssv1.KueueConfiguration, tc *TestClients) {
	initialDeployment, err := tc.KubeClient.AppsV1().Deployments(OperatorNamespace).Get(ctx, "kueue-controller-manager", metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "Failed to fetch kueue-controller-manager deployment before restore")
	initialResourceVersion := initialDeployment.ResourceVersion

	By("Restoring Kueue config (with conflict retry)")
	Eventually(func() error {
		instance, err := tc.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to fetch Kueue instance: %w", err)
		}
		instance.Spec.Config = config
		_, err = tc.KueueClient.KueueV1().Kueues().Update(ctx, instance, metav1.UpdateOptions{})
		return err
	}, ConfigRestoreTimeout, ConfigRestorePoll).Should(Succeed(), "Failed to restore Kueue config")

	By("Waiting for kueue-controller-manager deployment to be updated after config restore")
	Eventually(func() error {
		dep, err := tc.KubeClient.AppsV1().Deployments(OperatorNamespace).Get(ctx, "kueue-controller-manager", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("unable to fetch manager deployment: %w", err)
		}
		if dep.ResourceVersion == initialResourceVersion {
			return fmt.Errorf("deployment resource version has not changed yet (still %s)", initialResourceVersion)
		}
		return nil
	}, OperatorReadyTime, OperatorPoll).Should(Succeed(), "kueue-controller-manager deployment resource version did not change after restore")

	WaitForManagerReady(ctx, tc.KubeClient)
}

// FetchWorkload waits for a Kueue workload matching the given job UID to exist.
func FetchWorkload(ctx context.Context, kueueClient *upstreamkueueclient.Clientset, namespace, uid string) {
	Eventually(func() error {
		workloads, err := kueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", uid),
		})
		if err != nil {
			return err
		}
		if len(workloads.Items) == 0 {
			return fmt.Errorf("no workload found")
		}
		return nil
	}, OperatorReadyTime, OperatorPoll).Should(Succeed())
}

// VerifyWorkloadCreated waits for a Kueue workload matching the given UID to
// be created and admitted, returning the workload name.
func VerifyWorkloadCreated(ctx context.Context, kueueClient *upstreamkueueclient.Clientset, namespace, uid string) string {
	By(fmt.Sprintf("verifying workload created in namespace %s and uid %s", namespace, uid))
	var workload *kueuev1beta2.Workload
	Eventually(func() error {
		workloads, err := kueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", uid),
		})
		if err != nil {
			return err
		}
		if len(workloads.Items) > 0 {
			workload = &workloads.Items[0]
			return nil
		}

		allWorkloads, err := kueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{})
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

		return fmt.Errorf("no workload found in namespace %s with uid %s", namespace, uid)
	}, OperatorReadyTime, OperatorPoll).Should(Succeed())

	Eventually(func() error {
		updatedWorkload, err := kueueClient.KueueV1beta2().Workloads(namespace).Get(ctx, workload.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if apimeta.IsStatusConditionTrue(updatedWorkload.Status.Conditions, kueuev1beta2.WorkloadAdmitted) ||
			apimeta.IsStatusConditionTrue(updatedWorkload.Status.Conditions, kueuev1beta2.WorkloadFinished) {
			return nil
		}
		return fmt.Errorf("workload %s/%s not admitted or finished", namespace, workload.Name)
	}, OperatorReadyTime, OperatorPoll).Should(Succeed())
	return workload.Name
}

// CreateManagedNamespaceWithQueue creates a managed namespace (with the
// kueue.openshift.io/managed label) and a LocalQueue pointing to the given
// ClusterQueue. Returns the namespace object — cleanup is registered via
// DeferCleanup automatically.
func CreateManagedNamespaceWithQueue(ctx context.Context, kubeClient *kubernetes.Clientset, kueueClient *upstreamkueueclient.Clientset, nsPrefix, clusterQueueName, localQueueName string) *corev1.Namespace {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: nsPrefix,
			Labels:       map[string]string{OpenShiftManagedLabel: "true"},
		},
	}
	cleanupNs, err := CreateNamespace(kubeClient, ns)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(cleanupNs)

	lq := NewLocalQueue(ns.Name, localQueueName).WithClusterQueue(clusterQueueName)
	_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(cleanupLQ)

	return ns
}

// DeleteNamespace deletes a namespace and waits for it to be gone.
func DeleteNamespace(ctx context.Context, kubeClient *kubernetes.Clientset, namespace *corev1.Namespace) {
	if namespace == nil {
		return
	}
	By(fmt.Sprintf("Deleting namespace %s", namespace.Name))
	err := kubeClient.CoreV1().Namespaces().Delete(ctx, namespace.Name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return
	}
	Expect(err).NotTo(HaveOccurred())
	Eventually(func() error {
		_, err := kubeClient.CoreV1().Namespaces().Get(ctx, namespace.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("namespace %s still exists", namespace.Name)
	}, DeletionTime, DeletionPoll).Should(Succeed())
}
