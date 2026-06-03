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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
	upstreamkueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"
)

const (
	erTestNamespacePrefix = "kueue-dra-er-test-"
	erLocalQueueName      = "er-queue"
	extendedResourceName  = "nvidia.com/gpu"
	gpuDeviceType         = "gpu"
)

var _ = Describe("DRA Extended Resources", Label("operator", "dra", "dra-extended-resources"), Ordered, func() {
	var (
		initialKueueInstance *ssv1.Kueue
		maxGPUsPerNode       int
		originalDeviceClass  *resourcev1.DeviceClass
	)

	JustAfterEach(func(ctx context.Context) {
		testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 500)
	})

	BeforeAll(func(ctx context.Context) {
		// Check if DRA APIs are available
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

		// Check if ResourceSlices exist for gpu.nvidia.com (NVIDIA DRA driver running)
		slices, err := kubeClient.ResourceV1().ResourceSlices().List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
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

		gpusPerNode := map[string]int{}
		for _, s := range slices.Items {
			if s.Spec.Driver == draDeviceClassName {
				if s.Spec.NodeName != nil {
					for _, d := range s.Spec.Devices {
						// The NVIDIA DRA driver uses the bare "type" attribute key (not fully qualified)
						// with value "gpu" for full GPU devices. MIG slices have different type values.
						// See: device.attributes['gpu.nvidia.com'].type in DeviceClass CEL selectors.
						if typeAttr, ok := d.Attributes["type"]; ok {
							if typeAttr.StringValue != nil && *typeAttr.StringValue == gpuDeviceType {
								gpusPerNode[*s.Spec.NodeName]++
							}
						}
					}
				}
			}
		}
		for _, count := range gpusPerNode {
			if count > maxGPUsPerNode {
				maxGPUsPerNode = count
			}
		}
		if maxGPUsPerNode == 0 {
			Skip("No GPU devices found in ResourceSlices, skipping test")
		}

		// Check if DeviceClass has extendedResourceName set (K8s DRAExtendedResource gate enabled)
		dc, err := kubeClient.ResourceV1().DeviceClasses().Get(ctx, draDeviceClassName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		if dc.Spec.ExtendedResourceName == nil || *dc.Spec.ExtendedResourceName == "" {
			Skip("DeviceClass gpu.nvidia.com does not have extendedResourceName set - DRAExtendedResource feature gate not enabled")
		}
		originalDeviceClass = dc.DeepCopy()

		kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		initialKueueInstance = kueueInstance.DeepCopy()

		// Clear deviceClassMappings if set by a previous test suite
		kueueInstance.Spec.Config.Resources.DeviceClassMappings = nil
		applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)

		// Wait for DRA feature gates to be enabled and deviceClassMappings to be cleared
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
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
	})

	AfterAll(func(ctx context.Context) {
		if originalDeviceClass != nil {
			dc, err := kubeClient.ResourceV1().DeviceClasses().Get(ctx, draDeviceClassName, metav1.GetOptions{})
			if err == nil {
				dc.Spec.ExtendedResourceName = originalDeviceClass.Spec.ExtendedResourceName
				dc.Spec.Selectors = originalDeviceClass.Spec.Selectors
				_, _ = kubeClient.ResourceV1().DeviceClasses().Update(ctx, dc, metav1.UpdateOptions{})
			}
		}
		if initialKueueInstance != nil {
			restoreKueueConfig(ctx, initialKueueInstance.Spec.Config, kubeClient)
		}
	})

	It("should admit job with extended resource request and account DRA quota correctly", func(ctx context.Context) {
		By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
		kueueClient := clients.UpstreamKueueClient

		resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupResourceFlavor)

		cq, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name).
			WithDRAResource(extendedResourceName, fmt.Sprintf("%d", maxGPUsPerNode)).
			CreateWithObject(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupCQ)

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: erTestNamespacePrefix,
				Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
			},
		}
		cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupNs)

		lq := testutils.NewLocalQueue(ns.Name, erLocalQueueName).WithClusterQueue(cq.Name)
		_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupLQ)

		By("Creating Job with extended resource request nvidia.com/gpu")
		builder := testutils.NewTestResourceBuilder(ns.Name, erLocalQueueName)
		job := builder.NewJob()
		setDRAJobCPU(job)
		job.Name = "er-basic-job"
		job.Labels[testutils.QueueLabel] = erLocalQueueName
		job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
		job.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
		}
		createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

		By("Verifying workload is admitted with correct quota under extendedResourceName")
		Eventually(func(g Gomega) {
			wlList, err := kueueClient.KueueV1beta2().Workloads(ns.Name).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", string(createdJob.UID)),
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(wlList.Items).NotTo(BeEmpty(), "workload not found for job %s", createdJob.Name)

			wl := wlList.Items[0]
			g.Expect(wl.Status.Admission).NotTo(BeNil(), "workload should be admitted")
			g.Expect(wl.Status.Admission.PodSetAssignments).To(HaveLen(1))

			assignment := wl.Status.Admission.PodSetAssignments[0]
			g.Expect(assignment.ResourceUsage).To(HaveKey(corev1.ResourceName(extendedResourceName)))
			g.Expect(assignment.ResourceUsage[corev1.ResourceName(extendedResourceName)]).To(Equal(resource.MustParse("1")))
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By("Verifying job is unsuspended")
		Eventually(func(g Gomega) {
			suspended := testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
			g.Expect(suspended).To(BeFalse(), "job %s/%s should be unsuspended", ns.Name, createdJob.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By("Verifying job pod is running")
		Eventually(func(g Gomega) {
			running := testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob.Name)
			g.Expect(running).To(BeTrue(), "pod for job %s/%s should be running", ns.Name, createdJob.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By("Verifying extendedResourceClaimStatus is populated in pod status")
		Eventually(func(g Gomega) {
			pods, err := kubeClient.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("batch.kubernetes.io/job-name=%s", createdJob.Name),
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(pods.Items).NotTo(BeEmpty())

			pod := pods.Items[0]
			g.Expect(pod.Status.ExtendedResourceClaimStatus).NotTo(BeNil(), "extendedResourceClaimStatus should be populated")
			g.Expect(pod.Status.ExtendedResourceClaimStatus.ResourceClaimName).NotTo(BeEmpty(), "auto-generated ResourceClaim name should not be empty")
			g.Expect(pod.Status.ExtendedResourceClaimStatus.RequestMappings).NotTo(BeEmpty(), "requestMappings should not be empty")
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By("Verifying auto-generated ResourceClaim has the extended resource annotation")
		Eventually(func(g Gomega) {
			pods, err := kubeClient.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("batch.kubernetes.io/job-name=%s", createdJob.Name),
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(pods.Items).NotTo(BeEmpty())

			claimName := pods.Items[0].Status.ExtendedResourceClaimStatus.ResourceClaimName
			claim, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).Get(ctx, claimName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(claim.Annotations).To(HaveKeyWithValue("resource.kubernetes.io/extended-resource-claim", "true"))
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
	})

	It("should suspend job when extended resource quota is exceeded", func(ctx context.Context) {
		By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
		kueueClient := clients.UpstreamKueueClient

		resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupResourceFlavor)

		cq, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name).
			WithDRAResource(extendedResourceName, fmt.Sprintf("%d", maxGPUsPerNode)).
			CreateWithObject(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupCQ)

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: erTestNamespacePrefix,
				Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
			},
		}
		cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupNs)

		lq := testutils.NewLocalQueue(ns.Name, erLocalQueueName).WithClusterQueue(cq.Name)
		_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupLQ)

		By(fmt.Sprintf("Creating Job requesting %d GPUs via extended resources (quota is %d)", maxGPUsPerNode+1, maxGPUsPerNode))
		builder := testutils.NewTestResourceBuilder(ns.Name, erLocalQueueName)
		job := builder.NewJob()
		setDRAJobCPU(job)
		job.Name = "er-exceed-job"
		job.Labels[testutils.QueueLabel] = erLocalQueueName
		exceededCount := *resource.NewQuantity(int64(maxGPUsPerNode+1), resource.DecimalSI)
		job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = exceededCount
		job.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceName(extendedResourceName): exceededCount,
		}
		createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

		By("Verifying job is suspended")
		Eventually(func(g Gomega) {
			suspended := testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
			g.Expect(suspended).To(BeTrue(), "job %s/%s should be suspended", ns.Name, createdJob.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By("Verifying job remains suspended")
		Consistently(func(g Gomega) {
			suspended := testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
			g.Expect(suspended).To(BeTrue(), "job %s/%s should remain suspended", ns.Name, createdJob.Name)
		}, testutils.ConsistentlyLongTimeout, testutils.ConsistentlyLongPoll).Should(Succeed())
	})

	It("should admit waiting job after extended resource quota is freed", func(ctx context.Context) {
		By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
		kueueClient := clients.UpstreamKueueClient

		resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupResourceFlavor)

		cq, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name).
			WithDRAResource(extendedResourceName, fmt.Sprintf("%d", maxGPUsPerNode)).
			CreateWithObject(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupCQ)

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: erTestNamespacePrefix,
				Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
			},
		}
		cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupNs)

		lq := testutils.NewLocalQueue(ns.Name, erLocalQueueName).WithClusterQueue(cq.Name)
		_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupLQ)

		By(fmt.Sprintf("Creating first job requesting all %d GPUs via extended resources (fills quota)", maxGPUsPerNode))
		builder := testutils.NewTestResourceBuilder(ns.Name, erLocalQueueName)
		job1 := builder.NewJob()
		setDRAJobCPU(job1)
		job1.Name = "er-fill-job-1"
		job1.Labels[testutils.QueueLabel] = erLocalQueueName
		fillCount := *resource.NewQuantity(int64(maxGPUsPerNode), resource.DecimalSI)
		job1.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = fillCount
		job1.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceName(extendedResourceName): fillCount,
		}
		createdJob1, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job1, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer testutils.CleanUpJob(ctx, kubeClient, createdJob1.Namespace, createdJob1.Name)

		By("Waiting for first job to be admitted")
		Eventually(func(g Gomega) {
			suspended := testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob1.Name)
			g.Expect(suspended).To(BeFalse(), "job %s/%s should be unsuspended", ns.Name, createdJob1.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By("Verifying first job pod is running")
		Eventually(func(g Gomega) {
			running := testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob1.Name)
			g.Expect(running).To(BeTrue(), "pod for job %s/%s should be running", ns.Name, createdJob1.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By("Creating second job requesting 1 GPU via extended resources (quota full, should be suspended)")
		job2 := builder.NewJob()
		setDRAJobCPU(job2)
		job2.Name = "er-fill-job-2"
		job2.Labels[testutils.QueueLabel] = erLocalQueueName
		job2.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
		job2.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
		}
		createdJob2, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job2, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer testutils.CleanUpJob(ctx, kubeClient, createdJob2.Namespace, createdJob2.Name)

		By("Verifying second job is suspended (quota full)")
		Eventually(func(g Gomega) {
			suspended := testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob2.Name)
			g.Expect(suspended).To(BeTrue(), "job %s/%s should be suspended", ns.Name, createdJob2.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By("Verifying second job remains suspended")
		Consistently(func(g Gomega) {
			suspended := testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob2.Name)
			g.Expect(suspended).To(BeTrue(), "job %s/%s should remain suspended", ns.Name, createdJob2.Name)
		}, testutils.ConsistentlyLongTimeout, testutils.ConsistentlyLongPoll).Should(Succeed())

		By("Deleting first job to free quota")
		testutils.CleanUpJob(ctx, kubeClient, createdJob1.Namespace, createdJob1.Name)

		By("Verifying second job is admitted after quota freed")
		Eventually(func(g Gomega) {
			suspended := testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob2.Name)
			g.Expect(suspended).To(BeFalse(), "job %s/%s should be unsuspended after quota freed", ns.Name, createdJob2.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By("Verifying second job pod is running")
		Eventually(func(g Gomega) {
			running := testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob2.Name)
			g.Expect(running).To(BeTrue(), "pod for job %s/%s should be running", ns.Name, createdJob2.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
	})

	It("should verify ClusterQueue shows correct extended resource usage", func(ctx context.Context) {
		By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
		kueueClient := clients.UpstreamKueueClient

		resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupResourceFlavor)

		cq, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name).
			WithDRAResource(extendedResourceName, fmt.Sprintf("%d", maxGPUsPerNode)).
			CreateWithObject(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupCQ)

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: erTestNamespacePrefix,
				Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
			},
		}
		cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupNs)

		lq := testutils.NewLocalQueue(ns.Name, erLocalQueueName).WithClusterQueue(cq.Name)
		_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupLQ)

		By("Creating Job with 1 GPU via extended resource request")
		builder := testutils.NewTestResourceBuilder(ns.Name, erLocalQueueName)
		job := builder.NewJob()
		setDRAJobCPU(job)
		job.Name = "er-usage-job"
		job.Labels[testutils.QueueLabel] = erLocalQueueName
		job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
		job.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
		}
		createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

		By("Verifying ClusterQueue shows extended resource reservation")
		verifyClusterQueueReservation(ctx, kueueClient, cq.Name, extendedResourceName, "1")

		By("Verifying job is unsuspended")
		Eventually(func(g Gomega) {
			suspended := testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
			g.Expect(suspended).To(BeFalse(), "job %s/%s should be unsuspended", ns.Name, createdJob.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By("Verifying job pod is running")
		Eventually(func(g Gomega) {
			running := testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob.Name)
			g.Expect(running).To(BeTrue(), "pod for job %s/%s should be running", ns.Name, createdJob.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
	})

	When("different workload types use DRA extended resources", func() {
		It("should admit extended resource workloads for Pod type", func(ctx context.Context) {
			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cq, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name).
				WithDRAResource(extendedResourceName, fmt.Sprintf("%d", maxGPUsPerNode)).
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: erTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lq := testutils.NewLocalQueue(ns.Name, erLocalQueueName).WithClusterQueue(cq.Name)
			_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Creating a Kueue-managed Pod with extended resource request")
			builder := testutils.NewTestResourceBuilder(ns.Name, erLocalQueueName)
			pod := builder.NewPod()
			pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("100m")
			pod.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
			pod.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
			}
			createdPod, err := kubeClient.CoreV1().Pods(ns.Name).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpObject(ctx, genericClient, createdPod)

			verifyWorkloadCreated(kueueClient, ns.Name, string(createdPod.UID))

			By("Verifying Pod is running")
			Eventually(func(g Gomega) {
				p, err := kubeClient.CoreV1().Pods(ns.Name).Get(ctx, createdPod.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(p.Status.Phase).To(Equal(corev1.PodRunning), "Pod not running")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying auto-generated ResourceClaim has ER annotation, correct DeviceClass, and is allocated/reserved")
			Eventually(func(g Gomega) {
				claims, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(claims.Items).NotTo(BeEmpty(), "no ResourceClaims found")

				claim := claims.Items[0]
				g.Expect(claim.Status.Allocation).NotTo(BeNil(), "ResourceClaim not allocated")
				g.Expect(claim.Status.ReservedFor).NotTo(BeEmpty(), "ResourceClaim not reserved")
				g.Expect(claim.Annotations).To(HaveKeyWithValue("resource.kubernetes.io/extended-resource-claim", "true"))
				g.Expect(claim.Spec.Devices.Requests).NotTo(BeEmpty())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly).NotTo(BeNil())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly.DeviceClassName).To(Equal(draDeviceClassName))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying ClusterQueue shows extended resource reservation")
			verifyClusterQueueReservation(ctx, kueueClient, cq.Name, extendedResourceName, "1")
		})

		It("should admit extended resource workloads for Deployment type", func(ctx context.Context) {
			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cq, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name).
				WithDRAResource(extendedResourceName, fmt.Sprintf("%d", maxGPUsPerNode)).
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: erTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lq := testutils.NewLocalQueue(ns.Name, erLocalQueueName).WithClusterQueue(cq.Name)
			_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Creating a Kueue-managed Deployment with extended resource request")
			builder := testutils.NewTestResourceBuilder(ns.Name, erLocalQueueName)
			deploy := builder.NewDeployment()
			deploy.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("100m")
			deploy.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
			deploy.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
			}
			createdDeploy, err := kubeClient.AppsV1().Deployments(ns.Name).Create(ctx, deploy, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpObject(ctx, genericClient, createdDeploy)

			By("Waiting for Deployment pod to be created and workload to be admitted")
			waitForPodWorkloadAdmitted(ctx, kubeClient, kueueClient, ns.Name, "app=test-deployment")

			By("Verifying Deployment is available")
			Eventually(func(g Gomega) {
				d, err := kubeClient.AppsV1().Deployments(ns.Name).Get(ctx, createdDeploy.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(d.Status.AvailableReplicas).To(Equal(int32(1)), "Deployment not available")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying auto-generated ResourceClaim has ER annotation, correct DeviceClass, and is allocated/reserved")
			Eventually(func(g Gomega) {
				claims, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(claims.Items).NotTo(BeEmpty(), "no ResourceClaims found")

				claim := claims.Items[0]
				g.Expect(claim.Status.Allocation).NotTo(BeNil(), "ResourceClaim not allocated")
				g.Expect(claim.Status.ReservedFor).NotTo(BeEmpty(), "ResourceClaim not reserved")
				g.Expect(claim.Annotations).To(HaveKeyWithValue("resource.kubernetes.io/extended-resource-claim", "true"))
				g.Expect(claim.Spec.Devices.Requests).NotTo(BeEmpty())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly).NotTo(BeNil())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly.DeviceClassName).To(Equal(draDeviceClassName))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying ClusterQueue shows extended resource reservation")
			verifyClusterQueueReservation(ctx, kueueClient, cq.Name, extendedResourceName, "1")
		})

		It("should admit extended resource workloads for StatefulSet type", func(ctx context.Context) {
			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cq, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name).
				WithDRAResource(extendedResourceName, fmt.Sprintf("%d", maxGPUsPerNode)).
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: erTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lq := testutils.NewLocalQueue(ns.Name, erLocalQueueName).WithClusterQueue(cq.Name)
			_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Creating a Kueue-managed StatefulSet with extended resource request")
			builder := testutils.NewTestResourceBuilder(ns.Name, erLocalQueueName)
			ss := builder.NewStatefulSet()
			ss.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("100m")
			ss.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
			ss.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
			}
			createdSS, err := kubeClient.AppsV1().StatefulSets(ns.Name).Create(ctx, ss, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpObject(ctx, genericClient, createdSS)

			By("Waiting for StatefulSet pod to be created and workload to be admitted")
			waitForPodWorkloadAdmitted(ctx, kubeClient, kueueClient, ns.Name, "app=test-statefulset")

			By("Verifying StatefulSet is ready")
			Eventually(func(g Gomega) {
				s, err := kubeClient.AppsV1().StatefulSets(ns.Name).Get(ctx, createdSS.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(s.Status.ReadyReplicas).To(Equal(int32(1)), "StatefulSet not ready")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying auto-generated ResourceClaim has ER annotation, correct DeviceClass, and is allocated/reserved")
			Eventually(func(g Gomega) {
				claims, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(claims.Items).NotTo(BeEmpty(), "no ResourceClaims found")

				claim := claims.Items[0]
				g.Expect(claim.Status.Allocation).NotTo(BeNil(), "ResourceClaim not allocated")
				g.Expect(claim.Status.ReservedFor).NotTo(BeEmpty(), "ResourceClaim not reserved")
				g.Expect(claim.Annotations).To(HaveKeyWithValue("resource.kubernetes.io/extended-resource-claim", "true"))
				g.Expect(claim.Spec.Devices.Requests).NotTo(BeEmpty())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly).NotTo(BeNil())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly.DeviceClassName).To(Equal(draDeviceClassName))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying ClusterQueue shows extended resource reservation")
			verifyClusterQueueReservation(ctx, kueueClient, cq.Name, extendedResourceName, "1")
		})
	})

	When("preemption is enabled with extended resources", func() {
		It("should preempt low-priority ER workload when high-priority ER workload is submitted", func(ctx context.Context) {
			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: erTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			By("Creating PriorityClasses")
			lowPC, cleanupLowPC, err := createPriorityClass(ctx, 100, "Low priority for ER preemption")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLowPC)
			highPC, cleanupHighPC, err := createPriorityClass(ctx, 1000, "High priority for ER preemption")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupHighPC)

			By("Creating ClusterQueue with preemption enabled and ER quota of 1 GPU")
			cq, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name).
				WithPreemption(kueuev1beta2.PreemptionPolicyLowerPriority).
				WithDRAResource(extendedResourceName, "1").
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			By("Creating LocalQueue for preemption test")
			preemptLQ, cleanupLQ, err := testutils.NewLocalQueue(ns.Name, "").WithGenerateName().WithClusterQueue(cq.Name).CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Submitting low-priority ER job that fills the 1-GPU quota")
			builder := testutils.NewTestResourceBuilder(ns.Name, preemptLQ.Name)
			lowJob := builder.NewJob()
			setDRAJobCPU(lowJob)
			lowJob.Name = "er-low-prio"
			lowJob.Labels[testutils.QueueLabel] = preemptLQ.Name
			lowJob.Spec.Template.Spec.PriorityClassName = lowPC.Name
			lowJob.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "sleep 60"}
			lowJob.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
			lowJob.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
			}
			createdLowJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, lowJob, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdLowJob.Namespace, createdLowJob.Name)

			By("Verifying low-priority job is admitted")
			checkWorkloadCondition(ctx, ns.Name, string(createdLowJob.UID), kueuev1beta2.WorkloadAdmitted, "low-priority")

			By("Verifying low-priority job pod is running")
			Eventually(func(g Gomega) {
				running := testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdLowJob.Name)
				g.Expect(running).To(BeTrue(), "pod for job %s/%s should be running", ns.Name, createdLowJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying low-priority ER ResourceClaim has correct properties")
			Eventually(func(g Gomega) {
				pods, err := kubeClient.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("batch.kubernetes.io/job-name=%s", createdLowJob.Name),
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).NotTo(BeEmpty(), "no pods found for job %s", createdLowJob.Name)

				podUID := pods.Items[0].UID
				claims, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())

				var claim *resourcev1.ResourceClaim
				for i := range claims.Items {
					for _, ref := range claims.Items[i].OwnerReferences {
						if ref.UID == podUID {
							claim = &claims.Items[i]
							break
						}
					}
				}
				g.Expect(claim).NotTo(BeNil(), "no ResourceClaim found owned by pod %s", pods.Items[0].Name)
				g.Expect(claim.Status.Allocation).NotTo(BeNil(), "ResourceClaim not allocated")
				g.Expect(claim.Status.ReservedFor).NotTo(BeEmpty(), "ResourceClaim not reserved")
				g.Expect(claim.Annotations).To(HaveKeyWithValue("resource.kubernetes.io/extended-resource-claim", "true"))
				g.Expect(claim.Spec.Devices.Requests).NotTo(BeEmpty())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly).NotTo(BeNil())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly.DeviceClassName).To(Equal(draDeviceClassName))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Submitting high-priority ER job that triggers preemption")
			highJob := builder.NewJob()
			setDRAJobCPU(highJob)
			highJob.Name = "er-high-prio"
			highJob.Labels[testutils.QueueLabel] = preemptLQ.Name
			highJob.Spec.Template.Spec.PriorityClassName = highPC.Name
			highJob.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "sleep 10"}
			highJob.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
			highJob.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
			}
			createdHighJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, highJob, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdHighJob.Namespace, createdHighJob.Name)

			By("Verifying low-priority job is evicted")
			checkWorkloadCondition(ctx, ns.Name, string(createdLowJob.UID), kueuev1beta2.WorkloadEvicted, "low-priority")

			By("Verifying high-priority job is admitted")
			checkWorkloadCondition(ctx, ns.Name, string(createdHighJob.UID), kueuev1beta2.WorkloadAdmitted, "high-priority")

			By("Verifying high-priority ER ResourceClaim has correct properties")
			Eventually(func(g Gomega) {
				pods, err := kubeClient.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("batch.kubernetes.io/job-name=%s", createdHighJob.Name),
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).NotTo(BeEmpty(), "no pods found for job %s", createdHighJob.Name)

				podUID := pods.Items[0].UID
				claims, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())

				var claim *resourcev1.ResourceClaim
				for i := range claims.Items {
					for _, ref := range claims.Items[i].OwnerReferences {
						if ref.UID == podUID {
							claim = &claims.Items[i]
							break
						}
					}
				}
				g.Expect(claim).NotTo(BeNil(), "no ResourceClaim found owned by pod %s", pods.Items[0].Name)
				g.Expect(claim.Status.Allocation).NotTo(BeNil(), "ResourceClaim not allocated")
				g.Expect(claim.Status.ReservedFor).NotTo(BeEmpty(), "ResourceClaim not reserved")
				g.Expect(claim.Annotations).To(HaveKeyWithValue("resource.kubernetes.io/extended-resource-claim", "true"))
				g.Expect(claim.Spec.Devices.Requests).NotTo(BeEmpty())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly).NotTo(BeNil())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly.DeviceClassName).To(Equal(draDeviceClassName))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Waiting for high-priority job to complete")
			checkWorkloadCondition(ctx, ns.Name, string(createdHighJob.UID), kueuev1beta2.WorkloadFinished, "high-priority")

			By("Verifying low-priority job is re-admitted after high-priority completes")
			checkWorkloadCondition(ctx, ns.Name, string(createdLowJob.UID), kueuev1beta2.WorkloadAdmitted, "low-priority")

			By("Verifying re-admitted low-priority job pod is running")
			Eventually(func(g Gomega) {
				running := testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdLowJob.Name)
				g.Expect(running).To(BeTrue(), "pod for re-admitted job %s/%s should be running", ns.Name, createdLowJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying re-admitted low-priority ER ResourceClaim has correct properties")
			Eventually(func(g Gomega) {
				pods, err := kubeClient.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("batch.kubernetes.io/job-name=%s", createdLowJob.Name),
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).NotTo(BeEmpty(), "no pods found for job %s", createdLowJob.Name)

				podUIDs := make(map[types.UID]string, len(pods.Items))
				for _, p := range pods.Items {
					podUIDs[p.UID] = p.Name
				}

				claims, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())

				var claim *resourcev1.ResourceClaim
				for i := range claims.Items {
					for _, ref := range claims.Items[i].OwnerReferences {
						if _, ok := podUIDs[ref.UID]; ok {
							if claims.Items[i].Status.Allocation != nil {
								claim = &claims.Items[i]
								break
							}
						}
					}
				}
				g.Expect(claim).NotTo(BeNil(), "no allocated ResourceClaim found owned by any pod of job %s", createdLowJob.Name)
				g.Expect(claim.Status.ReservedFor).NotTo(BeEmpty(), "ResourceClaim not reserved")
				g.Expect(claim.Annotations).To(HaveKeyWithValue("resource.kubernetes.io/extended-resource-claim", "true"))
				g.Expect(claim.Spec.Devices.Requests).NotTo(BeEmpty())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly).NotTo(BeNil())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly.DeviceClassName).To(Equal(draDeviceClassName))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Waiting for low-priority job to complete")
			checkWorkloadCondition(ctx, ns.Name, string(createdLowJob.UID), kueuev1beta2.WorkloadFinished, "low-priority")

		})
	})

	When("gang scheduling is enabled with extended resources", func() {
		It("should enforce all-or-nothing admission for gang ER workloads", func(ctx context.Context) {
			if maxGPUsPerNode < 2 {
				Skip(fmt.Sprintf("gang scheduling test requires at least 2 GPUs, found %d", maxGPUsPerNode))
			}

			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: erTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			By("Saving current Kueue config and enabling gang scheduling")
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			savedConfig := kueueInstance.Spec.Config.DeepCopy()
			DeferCleanup(func(cleanupCtx context.Context) {
				restoreKueueConfig(cleanupCtx, *savedConfig, kubeClient)
			})

			gangConfig := *savedConfig
			gangConfig.GangScheduling = ssv1.GangScheduling{
				Policy: ssv1.GangSchedulingPolicyByWorkload,
				ByWorkload: &ssv1.ByWorkload{
					Admission: ssv1.GangSchedulingWorkloadAdmissionSequential,
				},
			}
			applyKueueConfig(ctx, gangConfig, kubeClient)

			By("Creating ClusterQueue with 2 GPU ER quota")
			gangCQ, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name).
				WithDRAResource(extendedResourceName, "2").
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			By("Creating LocalQueue for gang scheduling test")
			gangLQ, cleanupLQ, err := testutils.NewLocalQueue(ns.Name, "").WithGenerateName().WithClusterQueue(gangCQ.Name).CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Submitting single-pod ER job that fills 1 of 2 GPUs")
			builder := testutils.NewTestResourceBuilder(ns.Name, gangLQ.Name)
			singleJob := builder.NewJob()
			setDRAJobCPU(singleJob)
			singleJob.Name = "er-single-gpu"
			singleJob.Labels[testutils.QueueLabel] = gangLQ.Name
			singleJob.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "sleep 60"}
			singleJob.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
			singleJob.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
			}
			createdSingleJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, singleJob, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdSingleJob.Namespace, createdSingleJob.Name)

			By("Verifying single-pod job is admitted and running")
			checkWorkloadCondition(ctx, ns.Name, string(createdSingleJob.UID), kueuev1beta2.WorkloadAdmitted, "single-gpu")
			Eventually(func(g Gomega) {
				running := testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdSingleJob.Name)
				g.Expect(running).To(BeTrue(), "pod for job %s/%s should be running", ns.Name, createdSingleJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Submitting gang ER job (parallelism=2, needs 2 GPUs, only 1 free)")
			gangJob := builder.NewJob()
			setDRAJobCPU(gangJob)
			gangJob.Name = "er-gang-job"
			gangJob.Labels[testutils.QueueLabel] = gangLQ.Name
			gangJob.Spec.Parallelism = ptr.To(int32(2))
			gangJob.Spec.Completions = ptr.To(int32(2))
			gangJob.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "sleep 60"}
			gangJob.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
			gangJob.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
			}
			createdGangJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, gangJob, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdGangJob.Namespace, createdGangJob.Name)

			By("Verifying gang job is suspended (not enough ER quota for all pods)")
			Eventually(func(g Gomega) {
				suspended := testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdGangJob.Name)
				g.Expect(suspended).To(BeTrue(), "gang job %s/%s should be suspended", ns.Name, createdGangJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying gang job remains suspended")
			Consistently(func(g Gomega) {
				suspended := testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdGangJob.Name)
				g.Expect(suspended).To(BeTrue(), "gang job %s/%s should remain suspended", ns.Name, createdGangJob.Name)
			}, testutils.ConsistentlyLongTimeout, testutils.ConsistentlyLongPoll).Should(Succeed())

			By("Deleting single-pod job to free its GPU")
			testutils.CleanUpJob(ctx, kubeClient, createdSingleJob.Namespace, createdSingleJob.Name)

			By("Verifying gang job is admitted after quota is freed")
			checkWorkloadCondition(ctx, ns.Name, string(createdGangJob.UID), kueuev1beta2.WorkloadAdmitted, "gang")

			By("Verifying gang job is unsuspended")
			Eventually(func(g Gomega) {
				suspended := testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdGangJob.Name)
				g.Expect(suspended).To(BeFalse(), "gang job %s/%s should be unsuspended", ns.Name, createdGangJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying 2 ER ResourceClaims with correct properties")
			Eventually(func(g Gomega) {
				claims, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())

				var validCount int
				for _, claim := range claims.Items {
					if claim.Status.Allocation != nil && len(claim.Status.ReservedFor) > 0 {
						g.Expect(claim.Annotations).To(HaveKeyWithValue("resource.kubernetes.io/extended-resource-claim", "true"))
						g.Expect(claim.Spec.Devices.Requests).NotTo(BeEmpty())
						g.Expect(claim.Spec.Devices.Requests[0].Exactly).NotTo(BeNil())
						g.Expect(claim.Spec.Devices.Requests[0].Exactly.DeviceClassName).To(Equal(draDeviceClassName))
						validCount++
					}
				}
				g.Expect(validCount).To(Equal(2), "expected 2 allocated/reserved ER ResourceClaims, found %d", validCount)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying ClusterQueue shows 2 GPUs reserved")
			verifyClusterQueueReservation(ctx, kueueClient, gangCQ.Name, extendedResourceName, "2")

			By("Waiting for gang job to complete")
			checkWorkloadCondition(ctx, ns.Name, string(createdGangJob.UID), kueuev1beta2.WorkloadFinished, "gang")

			By("Verifying ClusterQueue quota is fully released")
			Eventually(func(g Gomega) {
				cqObj, err := kueueClient.KueueV1beta2().ClusterQueues().Get(ctx, gangCQ.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())

				for _, flavor := range cqObj.Status.FlavorsReservation {
					for _, res := range flavor.Resources {
						if res.Name == corev1.ResourceName(extendedResourceName) {
							g.Expect(res.Total.Cmp(resource.MustParse("0"))).To(Equal(0),
								"ClusterQueue should show 0 %s after completion, got %s", extendedResourceName, res.Total.String())
						}
					}
				}
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
		})
	})

	When("DeviceClass CEL selector validation with extended resources", Label("dra-extended-resources"), func() {
		It("should block scheduling when CEL selector matches no devices and resume after restoration", func(ctx context.Context) {
			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cq, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name).
				WithDRAResource(extendedResourceName, "1").
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: erTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lq := testutils.NewLocalQueue(ns.Name, erLocalQueueName).WithClusterQueue(cq.Name)
			_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Saving original DeviceClass CEL expression")
			dc, err := kubeClient.ResourceV1().DeviceClasses().Get(ctx, draDeviceClassName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(dc.Spec.Selectors).NotTo(BeEmpty(), "DeviceClass has no selectors")
			Expect(dc.Spec.Selectors[0].CEL).NotTo(BeNil(), "DeviceClass first selector has no CEL")
			originalExpression := dc.Spec.Selectors[0].CEL.Expression

			DeferCleanup(func(cleanupCtx context.Context) {
				By("Restoring original DeviceClass CEL expression in cleanup")
				dcCleanup, err := kubeClient.ResourceV1().DeviceClasses().Get(cleanupCtx, draDeviceClassName, metav1.GetOptions{})
				if err != nil {
					return
				}
				if len(dcCleanup.Spec.Selectors) > 0 && dcCleanup.Spec.Selectors[0].CEL != nil {
					dcCleanup.Spec.Selectors[0].CEL.Expression = originalExpression
					_, _ = kubeClient.ResourceV1().DeviceClasses().Update(cleanupCtx, dcCleanup, metav1.UpdateOptions{})
				}
			})

			By("Patching DeviceClass with invalid CEL expression (type == 'tpu' matches no devices)")
			invalidExpression := strings.Replace(originalExpression, "'gpu'", "'tpu'", 1)
			Expect(invalidExpression).NotTo(Equal(originalExpression), "CEL expression should have changed")
			dc.Spec.Selectors[0].CEL.Expression = invalidExpression
			_, err = kubeClient.ResourceV1().DeviceClasses().Update(ctx, dc, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying DeviceClass CEL expression was updated")
			Eventually(func(g Gomega) {
				updatedDC, err := kubeClient.ResourceV1().DeviceClasses().Get(ctx, draDeviceClassName, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(updatedDC.Spec.Selectors[0].CEL.Expression).To(ContainSubstring("'tpu'"))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Submitting ER job requesting 1 GPU")
			builder := testutils.NewTestResourceBuilder(ns.Name, erLocalQueueName)
			job := builder.NewJob()
			setDRAJobCPU(job)
			job.Name = "er-cel-invalid-test"
			job.Labels[testutils.QueueLabel] = erLocalQueueName
			job.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "sleep 30"}
			job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
			job.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
			}
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

			By("Verifying workload is admitted by Kueue (quota is available)")
			checkWorkloadCondition(ctx, ns.Name, string(createdJob.UID), kueuev1beta2.WorkloadAdmitted, "cel-invalid")

			By("Verifying job is unsuspended (Kueue admitted it)")
			Eventually(func(g Gomega) {
				suspended := testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
				g.Expect(suspended).To(BeFalse(), "job %s/%s should be unsuspended", ns.Name, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying pod stays Pending (scheduler can't find devices matching 'tpu' selector)")
			Consistently(func(g Gomega) {
				pods, err := kubeClient.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("batch.kubernetes.io/job-name=%s", createdJob.Name),
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).NotTo(BeEmpty(), "job pod should exist")
				g.Expect(pods.Items[0].Status.Phase).To(Equal(corev1.PodPending),
					"pod should stay Pending when CEL selector matches no devices")
			}, testutils.ConsistentlyLongTimeout, testutils.ConsistentlyLongPoll).Should(Succeed())

			By("Verifying ClusterQueue shows ER quota reserved (workload admitted)")
			verifyClusterQueueReservation(ctx, kueueClient, cq.Name, extendedResourceName, "1")

			By("Restoring original DeviceClass CEL expression")
			dc, err = kubeClient.ResourceV1().DeviceClasses().Get(ctx, draDeviceClassName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			dc.Spec.Selectors[0].CEL.Expression = originalExpression
			_, err = kubeClient.ResourceV1().DeviceClasses().Update(ctx, dc, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying DeviceClass CEL expression is restored")
			Eventually(func(g Gomega) {
				restoredDC, err := kubeClient.ResourceV1().DeviceClasses().Get(ctx, draDeviceClassName, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(restoredDC.Spec.Selectors[0].CEL.Expression).To(Equal(originalExpression))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying pod transitions to Running after CEL restoration")
			Eventually(func(g Gomega) {
				running := testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob.Name)
				g.Expect(running).To(BeTrue(), "pod for job %s/%s should be running after CEL restoration", ns.Name, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying ResourceClaim is allocated with ER properties after CEL restoration")
			Eventually(func(g Gomega) {
				claims, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(claims.Items).NotTo(BeEmpty(), "ResourceClaim should be created after CEL restoration")

				claim := claims.Items[0]
				g.Expect(claim.Status.Allocation).NotTo(BeNil(), "ResourceClaim should be allocated")
				g.Expect(claim.Status.ReservedFor).NotTo(BeEmpty(), "ResourceClaim should be reserved")
				g.Expect(claim.Annotations).To(HaveKeyWithValue("resource.kubernetes.io/extended-resource-claim", "true"))
				g.Expect(claim.Spec.Devices.Requests).NotTo(BeEmpty())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly).NotTo(BeNil())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly.DeviceClassName).To(Equal(draDeviceClassName))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Waiting for job to complete")
			checkWorkloadCondition(ctx, ns.Name, string(createdJob.UID), kueuev1beta2.WorkloadFinished, "cel-invalid")
		})

		It("should target a specific GPU by UUID via custom DeviceClass with extended resource", func(ctx context.Context) {
			By("Discovering GPU UUIDs from ResourceSlices")
			slices, err := kubeClient.ResourceV1().ResourceSlices().List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())

			type gpuInfo struct {
				deviceName string
				uuid       string
			}
			var gpuDevices []gpuInfo
			for _, s := range slices.Items {
				if s.Spec.Driver == draDeviceClassName {
					for _, d := range s.Spec.Devices {
						if typeAttr, ok := d.Attributes["type"]; ok {
							if typeAttr.StringValue != nil && *typeAttr.StringValue == gpuDeviceType {
								if uuidAttr, ok := d.Attributes["uuid"]; ok && uuidAttr.StringValue != nil {
									gpuDevices = append(gpuDevices, gpuInfo{
										deviceName: d.Name,
										uuid:       *uuidAttr.StringValue,
									})
								}
							}
						}
					}
				}
			}
			if len(gpuDevices) == 0 {
				Skip("No GPU devices with UUID attribute found in ResourceSlices")
			}

			targetGPU := gpuDevices[len(gpuDevices)-1]
			GinkgoWriter.Printf("Targeting GPU device=%s uuid=%s\n", targetGPU.deviceName, targetGPU.uuid)

			customDeviceClassName := "gpu-targeted.nvidia.com"
			customExtendedResource := "nvidia.com/gpu-targeted"

			By(fmt.Sprintf("Creating custom DeviceClass %s targeting GPU UUID %s", customDeviceClassName, targetGPU.uuid))
			customDC := &resourcev1.DeviceClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: customDeviceClassName,
				},
				Spec: resourcev1.DeviceClassSpec{
					ExtendedResourceName: ptr.To(customExtendedResource),
					Selectors: []resourcev1.DeviceSelector{
						{
							CEL: &resourcev1.CELDeviceSelector{
								Expression: fmt.Sprintf("device.driver == 'gpu.nvidia.com' && device.attributes['gpu.nvidia.com'].uuid == '%s'", targetGPU.uuid),
							},
						},
					},
				},
			}
			_, err = kubeClient.ResourceV1().DeviceClasses().Create(ctx, customDC, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(cleanupCtx context.Context) {
				_ = kubeClient.ResourceV1().DeviceClasses().Delete(cleanupCtx, customDeviceClassName, metav1.DeleteOptions{})
			})

			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue for targeted GPU")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			targetCQ, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name).
				WithCPU("1").WithMemory("1Gi").
				WithDRAResource(customExtendedResource, "1").
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: erTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			targetLQName := "gpu-targeted-queue"
			lq := testutils.NewLocalQueue(ns.Name, targetLQName).WithClusterQueue(targetCQ.Name)
			_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Creating a Pod requesting the targeted extended resource")
			builder := testutils.NewTestResourceBuilder(ns.Name, targetLQName)
			pod := builder.NewPod()
			pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("100m")
			pod.Spec.Containers[0].Resources.Requests[corev1.ResourceName(customExtendedResource)] = resource.MustParse("1")
			pod.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceName(customExtendedResource): resource.MustParse("1"),
			}
			createdPod, err := kubeClient.CoreV1().Pods(ns.Name).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpObject(ctx, genericClient, createdPod)

			By("Verifying workload is created and admitted")
			verifyWorkloadCreated(kueueClient, ns.Name, string(createdPod.UID))

			By("Verifying Pod is running")
			Eventually(func(g Gomega) {
				p, err := kubeClient.CoreV1().Pods(ns.Name).Get(ctx, createdPod.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(p.Status.Phase).To(Equal(corev1.PodRunning), "Pod not running")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying ResourceClaim has ER annotation, references custom DeviceClass, and targets the correct device")
			Eventually(func(g Gomega) {
				claims, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(claims.Items).NotTo(BeEmpty(), "no ResourceClaims found")

				claim := claims.Items[0]
				g.Expect(claim.Status.Allocation).NotTo(BeNil(), "ResourceClaim not allocated")
				g.Expect(claim.Status.ReservedFor).NotTo(BeEmpty(), "ResourceClaim not reserved")
				g.Expect(claim.Annotations).To(HaveKeyWithValue("resource.kubernetes.io/extended-resource-claim", "true"))
				g.Expect(claim.Spec.Devices.Requests).NotTo(BeEmpty())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly).NotTo(BeNil())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly.DeviceClassName).To(Equal(customDeviceClassName),
					"ResourceClaim should reference the custom DeviceClass")

				g.Expect(claim.Status.Allocation.Devices.Results).NotTo(BeEmpty(),
					"allocation should have device results")
				g.Expect(claim.Status.Allocation.Devices.Results[0].Device).To(Equal(targetGPU.deviceName),
					"allocated device should match the UUID-targeted GPU device name")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying ClusterQueue shows targeted resource reservation")
			verifyClusterQueueReservation(ctx, kueueClient, targetCQ.Name, customExtendedResource, "1")

		})
	})

	When("extendedResourceName is removed from DeviceClass", Label("dra-extended-resources"), func() {
		It("should prevent ER flow and recover after restoration", func(ctx context.Context) {
			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cq, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name).
				WithDRAResource(extendedResourceName, "1").
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: erTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lq := testutils.NewLocalQueue(ns.Name, erLocalQueueName).WithClusterQueue(cq.Name)
			_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Saving original extendedResourceName from DeviceClass")
			dc, err := kubeClient.ResourceV1().DeviceClasses().Get(ctx, draDeviceClassName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(dc.Spec.ExtendedResourceName).NotTo(BeNil(), "DeviceClass should have extendedResourceName set")
			originalERName := *dc.Spec.ExtendedResourceName

			DeferCleanup(func(cleanupCtx context.Context) {
				By("Restoring extendedResourceName on DeviceClass in cleanup")
				dcCleanup, err := kubeClient.ResourceV1().DeviceClasses().Get(cleanupCtx, draDeviceClassName, metav1.GetOptions{})
				if err != nil {
					return
				}
				dcCleanup.Spec.ExtendedResourceName = &originalERName
				_, _ = kubeClient.ResourceV1().DeviceClasses().Update(cleanupCtx, dcCleanup, metav1.UpdateOptions{})
			})

			By("Removing extendedResourceName from DeviceClass")
			dc.Spec.ExtendedResourceName = nil
			_, err = kubeClient.ResourceV1().DeviceClasses().Update(ctx, dc, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying extendedResourceName is removed")
			Eventually(func(g Gomega) {
				updatedDC, err := kubeClient.ResourceV1().DeviceClasses().Get(ctx, draDeviceClassName, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(updatedDC.Spec.ExtendedResourceName).To(BeNil(),
					"extendedResourceName should be nil after removal")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Submitting ER job requesting 1 GPU")
			builder := testutils.NewTestResourceBuilder(ns.Name, erLocalQueueName)
			job := builder.NewJob()
			setDRAJobCPU(job)
			job.Name = "er-no-mapping-test"
			job.Labels[testutils.QueueLabel] = erLocalQueueName
			job.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "sleep 30"}
			job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
			job.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
			}
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

			By("Verifying workload is admitted by Kueue (treats it as regular extended resource with quota)")
			checkWorkloadCondition(ctx, ns.Name, string(createdJob.UID), kueuev1beta2.WorkloadAdmitted, "no-er-mapping")

			By("Verifying job is unsuspended")
			Eventually(func(g Gomega) {
				suspended := testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
				g.Expect(suspended).To(BeFalse(), "job %s/%s should be unsuspended", ns.Name, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying no ResourceClaim is created (no ER mapping on DeviceClass)")
			Consistently(func(g Gomega) {
				claims, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(claims.Items).To(BeEmpty(),
					"no ResourceClaim should be auto-generated without extendedResourceName")
			}, testutils.ConsistentlyLongTimeout, testutils.ConsistentlyLongPoll).Should(Succeed())

			By("Verifying pod stays Pending (node doesn't advertise nvidia.com/gpu as traditional resource)")
			Consistently(func(g Gomega) {
				pods, err := kubeClient.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("batch.kubernetes.io/job-name=%s", createdJob.Name),
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).NotTo(BeEmpty(), "job pod should exist")
				g.Expect(pods.Items[0].Status.Phase).To(Equal(corev1.PodPending),
					"pod should stay Pending without ER mapping")
			}, testutils.ConsistentlyLongTimeout, testutils.ConsistentlyLongPoll).Should(Succeed())

			By("Verifying ClusterQueue shows ER quota reserved (workload admitted but pod unschedulable)")
			verifyClusterQueueReservation(ctx, kueueClient, cq.Name, extendedResourceName, "1")

			By("Restoring extendedResourceName on DeviceClass")
			dc, err = kubeClient.ResourceV1().DeviceClasses().Get(ctx, draDeviceClassName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			dc.Spec.ExtendedResourceName = &originalERName
			_, err = kubeClient.ResourceV1().DeviceClasses().Update(ctx, dc, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying extendedResourceName is restored")
			Eventually(func(g Gomega) {
				restoredDC, err := kubeClient.ResourceV1().DeviceClasses().Get(ctx, draDeviceClassName, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(restoredDC.Spec.ExtendedResourceName).NotTo(BeNil())
				g.Expect(*restoredDC.Spec.ExtendedResourceName).To(Equal(originalERName))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Deleting the stuck pending pod to force Job controller to create a fresh one")
			pods, err := kubeClient.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("batch.kubernetes.io/job-name=%s", createdJob.Name),
			})
			Expect(err).NotTo(HaveOccurred())
			for _, p := range pods.Items {
				_ = kubeClient.CoreV1().Pods(ns.Name).Delete(ctx, p.Name, metav1.DeleteOptions{})
			}

			By("Verifying new pod transitions to Running after ER mapping is restored")
			Eventually(func(g Gomega) {
				running := testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob.Name)
				g.Expect(running).To(BeTrue(), "pod for job %s/%s should be running after ER mapping restored", ns.Name, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying ResourceClaim is now created and allocated after restoration")
			Eventually(func(g Gomega) {
				claims, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(claims.Items).NotTo(BeEmpty(), "ResourceClaim should be created after restoration")

				claim := claims.Items[0]
				g.Expect(claim.Status.Allocation).NotTo(BeNil(), "ResourceClaim should be allocated")
				g.Expect(claim.Status.ReservedFor).NotTo(BeEmpty(), "ResourceClaim should be reserved")
				g.Expect(claim.Annotations).To(HaveKeyWithValue("resource.kubernetes.io/extended-resource-claim", "true"))
				g.Expect(claim.Spec.Devices.Requests).NotTo(BeEmpty())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly).NotTo(BeNil())
				g.Expect(claim.Spec.Devices.Requests[0].Exactly.DeviceClassName).To(Equal(draDeviceClassName))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Waiting for job to complete")
			checkWorkloadCondition(ctx, ns.Name, string(createdJob.UID), kueuev1beta2.WorkloadFinished, "no-er-mapping")
		})
	})

	When("Extended Resources are used with deviceClassMappings", Label("dra-extended-resources"), func() {
		BeforeAll(func(ctx context.Context) {
			if maxGPUsPerNode < 2 {
				Skip("Need at least 2 GPUs on a single node for mixed ER + RCT test")
			}
		})

		It("should account both ER and RCT under same quota with deviceClassMappings taking precedence", func(ctx context.Context) {
			By("Adding deviceClassMappings for RCT path")
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			kueueInstance.Spec.Config.Resources.DeviceClassMappings = []ssv1.DeviceClassMapping{
				{
					Name:             draLogicalResource,
					DeviceClassNames: []ssv1.DeviceClassName{draDeviceClassName},
				},
			}
			applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)

			Eventually(func() error {
				configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
				if err != nil {
					return err
				}
				configData := configMap.Data["controller_manager_config.yaml"]
				if !strings.Contains(configData, draLogicalResource) {
					return fmt.Errorf("deviceClassMappings not configured yet")
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cq, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name).
				WithDRAResource(draLogicalResource, fmt.Sprintf("%d", maxGPUsPerNode)).
				WithDRAResource(extendedResourceName, fmt.Sprintf("%d", maxGPUsPerNode)).
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: erTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lq := testutils.NewLocalQueue(ns.Name, "er-mixed-queue").WithClusterQueue(cq.Name)
			_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Creating ResourceClaimTemplate for 1 GPU")
			rct := &resourcev1.ResourceClaimTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gpu-template-mixed",
					Namespace: ns.Name,
				},
				Spec: resourcev1.ResourceClaimTemplateSpec{
					Spec: resourcev1.ResourceClaimSpec{
						Devices: resourcev1.DeviceClaim{
							Requests: []resourcev1.DeviceRequest{
								{
									Name: "gpu-req",
									Exactly: &resourcev1.ExactDeviceRequest{
										DeviceClassName: draDeviceClassName,
										Count:           1,
									},
								},
							},
						},
					},
				},
			}
			_, err = kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Delete(ctx, rct.Name, metav1.DeleteOptions{})

			By("Creating Job with both extended resource request and ResourceClaimTemplate")
			builder := testutils.NewTestResourceBuilder(ns.Name, "er-mixed-queue")
			job := builder.NewJob()
			setDRAJobCPU(job)
			job.Name = "er-mixed-job"
			job.Labels[testutils.QueueLabel] = "er-mixed-queue"
			job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
			job.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
			}
			job.Spec.Template.Spec.ResourceClaims = []corev1.PodResourceClaim{
				{Name: gpuDeviceType, ResourceClaimTemplateName: ptr.To("gpu-template-mixed")},
			}
			job.Spec.Template.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{
				{Name: gpuDeviceType},
			}
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

			By("Verifying workload is admitted and deviceClassMappings name takes precedence")
			Eventually(func(g Gomega) {
				wlList, err := kueueClient.KueueV1beta2().Workloads(ns.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", string(createdJob.UID)),
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(wlList.Items).NotTo(BeEmpty(), "workload not found for job %s", createdJob.Name)

				wl := wlList.Items[0]
				g.Expect(wl.Status.Admission).NotTo(BeNil(), "workload should be admitted")
				g.Expect(wl.Status.Admission.PodSetAssignments).To(HaveLen(1))

				assignment := wl.Status.Admission.PodSetAssignments[0]
				g.Expect(assignment.ResourceUsage).To(HaveKey(corev1.ResourceName(draLogicalResource)))
				g.Expect(assignment.ResourceUsage[corev1.ResourceName(draLogicalResource)]).To(Equal(resource.MustParse("2")),
					"both ER and RCT should be counted under %s", draLogicalResource)

				// Verify ClusterQueue shows mapping name used, extended resource name stays at 0
				cqObj, err := kueueClient.KueueV1beta2().ClusterQueues().Get(ctx, cq.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				for _, flavor := range cqObj.Status.FlavorsReservation {
					for _, res := range flavor.Resources {
						if res.Name == corev1.ResourceName(draLogicalResource) {
							g.Expect(res.Total.Cmp(resource.MustParse("2"))).To(Equal(0),
								"%s should show 2 reserved (ER + RCT converged), got %s", draLogicalResource, res.Total.String())
						}
						if res.Name == corev1.ResourceName(extendedResourceName) {
							g.Expect(res.Total.Cmp(resource.MustParse("0"))).To(Equal(0),
								"%s should show 0 (mapped to %s), got %s", extendedResourceName, draLogicalResource, res.Total.String())
						}
					}
				}
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying job is unsuspended")
			Eventually(func(g Gomega) {
				suspended := testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
				g.Expect(suspended).To(BeFalse(), "job %s/%s should be unsuspended", ns.Name, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying job pod is running")
			Eventually(func(g Gomega) {
				running := testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob.Name)
				g.Expect(running).To(BeTrue(), "pod for job %s/%s should be running", ns.Name, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying pod has both extendedResourceClaimStatus and resourceClaimStatuses")
			Eventually(func(g Gomega) {
				pods, err := kubeClient.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("batch.kubernetes.io/job-name=%s", createdJob.Name),
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).NotTo(BeEmpty())

				pod := pods.Items[0]
				g.Expect(pod.Status.ExtendedResourceClaimStatus).NotTo(BeNil(),
					"extendedResourceClaimStatus should be populated for ER path")
				g.Expect(pod.Status.ResourceClaimStatuses).NotTo(BeEmpty(),
					"resourceClaimStatuses should be populated for RCT path")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
		})
	})
})

func verifyClusterQueueReservation(ctx context.Context, kueueClient *upstreamkueueclient.Clientset, cqName, resourceName, expectedQuantity string) {
	Eventually(func(g Gomega) {
		cqObj, err := kueueClient.KueueV1beta2().ClusterQueues().Get(ctx, cqName, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cqObj.Status.FlavorsReservation).NotTo(BeEmpty())

		found := false
		for _, flavor := range cqObj.Status.FlavorsReservation {
			for _, res := range flavor.Resources {
				if res.Name == corev1.ResourceName(resourceName) {
					g.Expect(res.Total.Cmp(resource.MustParse(expectedQuantity))).To(Equal(0),
						"ClusterQueue should show %s %s reserved, got %s", expectedQuantity, resourceName, res.Total.String())
					found = true
				}
			}
		}
		g.Expect(found).To(BeTrue(), "resource %s not found in ClusterQueue reservation", resourceName)
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}
