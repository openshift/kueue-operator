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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ssv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
)

const (
	erAlpha2NamespacePrefix = "kueue-dra-er-alpha2-"
	erAlpha2QueueName       = "er-alpha2-queue"
)

var _ = Describe("DRA Extended Resources Alpha-2 DeviceClass Lifecycle", Label("operator", "dra", "dra-extended-resources"), Ordered, func() {
	var (
		originalDeviceClass *resourcev1.DeviceClass
		initialKueueConfig  *ssv1.KueueConfiguration
	)

	JustAfterEach(func(ctx context.Context) {
		testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 500)
	})

	BeforeAll(func(ctx context.Context) {
		By("Checking DRA APIs are available")
		draSupported := false
		apiResourceLists, err := kubeClient.Discovery().ServerResourcesForGroupVersion("resource.k8s.io/v1")
		if err == nil {
			for _, apiResource := range apiResourceLists.APIResources {
				if apiResource.Kind == testutils.DeviceClassKind {
					draSupported = true
					break
				}
			}
		}
		if !draSupported {
			Skip("DRA APIs (resource.k8s.io/v1) not available on this cluster")
		}

		By("Checking ResourceSlices exist for NVIDIA DRA driver")
		slices, err := kubeClient.ResourceV1().ResourceSlices().List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to list ResourceSlices")
		hasDriverSlices := false
		for _, s := range slices.Items {
			if s.Spec.Driver == draDeviceClassName {
				hasDriverSlices = true
				break
			}
		}
		if !hasDriverSlices {
			Skip("No ResourceSlices found for driver gpu.nvidia.com - NVIDIA DRA driver not running")
		}

		By("Checking DeviceClass has extendedResourceName (DRAExtendedResource feature gate)")
		dc, err := kubeClient.ResourceV1().DeviceClasses().Get(ctx, draDeviceClassName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to get DeviceClass")
		if dc.Spec.ExtendedResourceName == nil || *dc.Spec.ExtendedResourceName == "" {
			Skip("DeviceClass gpu.nvidia.com does not have extendedResourceName set - DRAExtendedResource feature gate not enabled")
		}
		originalDeviceClass = dc.DeepCopy()

		By("Saving Kueue config and clearing deviceClassMappings")
		kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
		initialKueueConfig = kueueInstance.Spec.Config.DeepCopy()
		DeferCleanup(func(ctx context.Context) {
			By("Restoring Kueue config")
			if initialKueueConfig != nil {
				restoreKueueConfig(ctx, *initialKueueConfig, kubeClient)
			}
		})

		kueueInstance.Spec.Config.Resources.DeviceClassMappings = nil
		applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)

		By("Waiting for config to be reconciled")
		Eventually(func() error {
			configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
			if err != nil {
				return err
			}
			configData := configMap.Data["controller_manager_config.yaml"]
			if !strings.Contains(configData, "KueueDRAIntegrationExtendedResource: true") {
				return fmt.Errorf("KueueDRAIntegrationExtendedResource not enabled yet")
			}
			if strings.Contains(configData, draLogicalResource) {
				return fmt.Errorf("deviceClassMappings still contains %s, waiting for config reconciliation", draLogicalResource)
			}
			return nil
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(),
			"Config should be reconciled with deviceClassMappings cleared")
	})

	AfterAll(func(ctx context.Context) {
		if originalDeviceClass != nil {
			By("Restoring DeviceClass to original state")
			dc, err := kubeClient.ResourceV1().DeviceClasses().Get(ctx, draDeviceClassName, metav1.GetOptions{})
			if err != nil {
				recreated := originalDeviceClass.DeepCopy()
				recreated.ResourceVersion = ""
				_, _ = kubeClient.ResourceV1().DeviceClasses().Create(ctx, recreated, metav1.CreateOptions{})
			} else {
				dc.Spec.ExtendedResourceName = originalDeviceClass.Spec.ExtendedResourceName
				dc.Spec.Selectors = originalDeviceClass.Spec.Selectors
				_, _ = kubeClient.ResourceV1().DeviceClasses().Update(ctx, dc, metav1.UpdateOptions{})
			}
		}
	})

	When("no DeviceClass with extendedResourceName exists", func() {
		It("should admit workload via non-DRA extended resource path but leave pod pending", func(ctx context.Context) {
			var err error

			By("Deleting DeviceClass so no extendedResourceName mapping exists")
			err = kubeClient.ResourceV1().DeviceClasses().Delete(ctx, draDeviceClassName, metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to delete DeviceClass")

			By("Verifying DeviceClass is deleted")
			Eventually(func() error {
				_, err := kubeClient.ResourceV1().DeviceClasses().Get(ctx, draDeviceClassName, metav1.GetOptions{})
				if err != nil {
					return nil
				}
				return fmt.Errorf("DeviceClass %s still exists", draDeviceClassName)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(),
				"DeviceClass should be fully deleted")

			By("Creating ResourceFlavor")
			resourceFlavor, cleanupRF, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
			DeferCleanup(cleanupRF)

			By("Creating ClusterQueue with extended resource quota")
			cq, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name).
				WithDRAResource(extendedResourceName, "2").
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue")
			DeferCleanup(cleanupCQ)

			By("Creating namespace")
			ns, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: erAlpha2NamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")
			DeferCleanup(func(ctx context.Context) {
				By(fmt.Sprintf("Deleting namespace %s", ns.Name))
				err := kubeClient.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred(), "Failed to delete namespace")
				testutils.WaitForAllPodsInNamespaceDeleted(ctx, clients.GenericClient, ns)
			})

			By("Creating LocalQueue")
			_, cleanupLQ, err := testutils.NewLocalQueue(ns.Name, erAlpha2QueueName).WithClusterQueue(cq.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue")
			DeferCleanup(cleanupLQ)

			By("Creating Job requesting nvidia.com/gpu via extended resource")
			builder := testutils.NewTestResourceBuilder(ns.Name, erAlpha2QueueName)
			job := builder.NewJob()
			setDRAJobCPU(job)
			job.Name = "s1-no-deviceclass"
			job.Labels[testutils.QueueLabel] = erAlpha2QueueName
			job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
			job.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
			}
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create job")
			DeferCleanup(func(ctx context.Context) {
				By(fmt.Sprintf("Deleting job %s", createdJob.Name))
				err := kubeClient.BatchV1().Jobs(ns.Name).Delete(ctx, createdJob.Name, metav1.DeleteOptions{
					PropagationPolicy: ptr.To(metav1.DeletePropagationBackground),
				})
				Expect(err).NotTo(HaveOccurred(), "Failed to delete job")
			})

			By("Verifying workload is admitted via non-DRA extended resource path")
			checkWorkloadCondition(ctx, ns.Name, string(createdJob.UID), kueuev1beta2.WorkloadAdmitted, "s1-no-deviceclass")

			By("Verifying job is unsuspended")
			Eventually(func() bool {
				return !testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(),
				"Job should be unsuspended (admitted via non-DRA ER path)")

			By("Verifying pod is created but stays Pending")
			Consistently(func(g Gomega) {
				pods, err := kubeClient.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("batch.kubernetes.io/job-name=%s", createdJob.Name),
				})
				g.Expect(err).NotTo(HaveOccurred(), "Failed to list pods")
				g.Expect(pods.Items).NotTo(BeEmpty(), "Job pod should exist")
				g.Expect(pods.Items[0].Status.Phase).To(Equal(corev1.PodPending),
					"Pod should stay Pending when no DeviceClass provides DRA path for nvidia.com/gpu")
			}, testutils.ConsistentlyLongTimeout, testutils.ConsistentlyLongPoll).Should(Succeed())

			By("Verifying no ResourceClaim was created")
			Consistently(func(g Gomega) {
				claims, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "Failed to list ResourceClaims")
				g.Expect(claims.Items).To(BeEmpty(), "No ResourceClaim should be created without DeviceClass")
			}, testutils.ConsistentlyLongTimeout, testutils.ConsistentlyLongPoll).Should(Succeed())

			By("Verifying ClusterQueue shows extended resource quota consumed")
			verifyClusterQueueReservation(ctx, clients.UpstreamKueueClient, cq.Name, extendedResourceName, "1")
		})
	})

	When("DeviceClass is created after workload submission", func() {
		It("should transition pod to running via scheduler-level DRA activation", func(ctx context.Context) {
			var err error

			By("Deleting DeviceClass so no extendedResourceName mapping exists")
			_ = kubeClient.ResourceV1().DeviceClasses().Delete(ctx, draDeviceClassName, metav1.DeleteOptions{})
			Eventually(func() error {
				_, err := kubeClient.ResourceV1().DeviceClasses().Get(ctx, draDeviceClassName, metav1.GetOptions{})
				if err != nil {
					return nil
				}
				return fmt.Errorf("DeviceClass %s still exists", draDeviceClassName)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(),
				"DeviceClass should be fully deleted")

			By("Creating ResourceFlavor")
			resourceFlavor, cleanupRF, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
			DeferCleanup(cleanupRF)

			By("Creating ClusterQueue with extended resource quota")
			cq, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name).
				WithDRAResource(extendedResourceName, "2").
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue")
			DeferCleanup(cleanupCQ)

			By("Creating namespace")
			ns, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: erAlpha2NamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")
			DeferCleanup(func(ctx context.Context) {
				By(fmt.Sprintf("Deleting namespace %s", ns.Name))
				err := kubeClient.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred(), "Failed to delete namespace")
				testutils.WaitForAllPodsInNamespaceDeleted(ctx, clients.GenericClient, ns)
			})

			By("Creating LocalQueue")
			_, cleanupLQ, err := testutils.NewLocalQueue(ns.Name, erAlpha2QueueName).WithClusterQueue(cq.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue")
			DeferCleanup(cleanupLQ)

			By("Creating Job requesting nvidia.com/gpu via extended resource")
			builder := testutils.NewTestResourceBuilder(ns.Name, erAlpha2QueueName)
			job := builder.NewJob()
			setDRAJobCPU(job)
			job.Name = "s2-late-deviceclass"
			job.Labels[testutils.QueueLabel] = erAlpha2QueueName
			job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
			job.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
			}
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create job")
			DeferCleanup(func(ctx context.Context) {
				By(fmt.Sprintf("Deleting job %s", createdJob.Name))
				err := kubeClient.BatchV1().Jobs(ns.Name).Delete(ctx, createdJob.Name, metav1.DeleteOptions{
					PropagationPolicy: ptr.To(metav1.DeletePropagationBackground),
				})
				Expect(err).NotTo(HaveOccurred(), "Failed to delete job")
			})

			By("Verifying workload is admitted via non-DRA extended resource path")
			checkWorkloadCondition(ctx, ns.Name, string(createdJob.UID), kueuev1beta2.WorkloadAdmitted, "s2-late-deviceclass")

			By("Verifying pod is Pending before DeviceClass creation")
			Consistently(func(g Gomega) {
				pods, err := kubeClient.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("batch.kubernetes.io/job-name=%s", createdJob.Name),
				})
				g.Expect(err).NotTo(HaveOccurred(), "Failed to list pods")
				g.Expect(pods.Items).NotTo(BeEmpty(), "Job pod should exist")
				g.Expect(pods.Items[0].Status.Phase).To(Equal(corev1.PodPending),
					"Pod should be Pending before DeviceClass is created")
			}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(Succeed())

			By("Creating DeviceClass with extendedResourceName")
			newDC := &resourcev1.DeviceClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: draDeviceClassName,
				},
				Spec: resourcev1.DeviceClassSpec{
					ExtendedResourceName: originalDeviceClass.Spec.ExtendedResourceName,
					Selectors:            originalDeviceClass.Spec.Selectors,
				},
			}
			_, err = kubeClient.ResourceV1().DeviceClasses().Create(ctx, newDC, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create DeviceClass")

			By("Verifying DeviceClass was created with extendedResourceName")
			Eventually(func(g Gomega) {
				dc, err := kubeClient.ResourceV1().DeviceClasses().Get(ctx, draDeviceClassName, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get DeviceClass")
				g.Expect(dc.Spec.ExtendedResourceName).NotTo(BeNil(), "extendedResourceName should be set")
				g.Expect(*dc.Spec.ExtendedResourceName).To(Equal(extendedResourceName),
					"extendedResourceName should match")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying pod transitions to Running via scheduler-level DRA activation")
			Eventually(func() bool {
				return testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(),
				"Pod should transition to Running after DeviceClass creation")

			By("Verifying ResourceClaim was created by scheduler")
			Eventually(func(g Gomega) {
				claims, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "Failed to list ResourceClaims")
				g.Expect(claims.Items).NotTo(BeEmpty(), "ResourceClaim should be created after DeviceClass creation")

				claim := claims.Items[0]
				g.Expect(claim.Status.Allocation).NotTo(BeNil(), "ResourceClaim should be allocated")
				g.Expect(claim.Status.ReservedFor).NotTo(BeEmpty(), "ResourceClaim should be reserved")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying ClusterQueue still shows extended resource quota consumed")
			verifyClusterQueueReservation(ctx, clients.UpstreamKueueClient, cq.Name, extendedResourceName, "1")
		})
	})
})
