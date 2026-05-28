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
	erTestNamespacePrefix = "kueue-dra-er-test-"
	erLocalQueueName      = "er-queue"
	extendedResourceName  = "nvidia.com/gpu"
	gpuDeviceType         = "gpu"
)

var _ = Describe("DRA Extended Resources", Label("operator", "dra", "dra-extended-resources"), Ordered, func() {
	var (
		initialKueueInstance *ssv1.Kueue
		maxGPUsPerNode       int
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
			if s.Spec.Driver == testutils.DRADeviceClassName {
				hasDriverSlices = true
				break
			}
		}
		if !hasDriverSlices {
			Skip("No ResourceSlices found for driver gpu.nvidia.com - NVIDIA DRA driver not running")
		}

		gpusPerNode := map[string]int{}
		for _, s := range slices.Items {
			if s.Spec.Driver == testutils.DRADeviceClassName {
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
		dc, err := kubeClient.ResourceV1().DeviceClasses().Get(ctx, testutils.DRADeviceClassName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		if dc.Spec.ExtendedResourceName == nil || *dc.Spec.ExtendedResourceName == "" {
			Skip("DeviceClass gpu.nvidia.com does not have extendedResourceName set - DRAExtendedResource feature gate not enabled")
		}

		kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		initialKueueInstance = kueueInstance.DeepCopy()

		// Clear deviceClassMappings if set by a previous test suite
		kueueInstance.Spec.Config.Resources.DeviceClassMappings = nil
		testutils.ApplyKueueConfig(ctx, kueueInstance.Spec.Config, clients)

		// Wait for DRA feature gates to be enabled and deviceClassMappings to be cleared
		Eventually(func() error {
			configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
			if err != nil {
				return err
			}
			configData := configMap.Data["controller_manager_config.yaml"]
			if !strings.Contains(configData, "DynamicResourceAllocation: true") {
				return fmt.Errorf("DynamicResourceAllocation not enabled yet")
			}
			if !strings.Contains(configData, "DRAExtendedResources: true") {
				return fmt.Errorf("DRAExtendedResources not enabled yet")
			}
			if strings.Contains(configData, testutils.DRALogicalResource) {
				return fmt.Errorf("deviceClassMappings still contains %s, waiting for config reconciliation", testutils.DRALogicalResource)
			}
			return nil
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
	})

	AfterAll(func(ctx context.Context) {
		if initialKueueInstance != nil {
			testutils.ApplyKueueConfig(ctx, initialKueueInstance.Spec.Config, clients)
		}
	})

	It("should admit job with extended resource request and account DRA quota correctly", func(ctx context.Context) {
		By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
		kueueClient := clients.UpstreamKueueClient

		resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupResourceFlavor)

		cqWrapper := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name)
		cqWrapper.Spec.ResourceGroups = []kueuev1beta2.ResourceGroup{{
			CoveredResources: []corev1.ResourceName{"cpu", "memory", corev1.ResourceName(extendedResourceName)},
			Flavors: []kueuev1beta2.FlavorQuotas{{
				Name: kueuev1beta2.ResourceFlavorReference(resourceFlavor.Name),
				Resources: []kueuev1beta2.ResourceQuota{
					{Name: "cpu", NominalQuota: resource.MustParse("100")},
					{Name: "memory", NominalQuota: resource.MustParse("100Gi")},
					{Name: corev1.ResourceName(extendedResourceName), NominalQuota: *resource.NewQuantity(int64(maxGPUsPerNode), resource.DecimalSI)},
				},
			}},
		}}
		cq, cleanupCQ, err := cqWrapper.CreateWithObject(ctx, kueueClient)
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
		testutils.SetDRAJobCPU(job)
		job.Name = "er-basic-job"
		job.Labels[testutils.QueueLabel] = erLocalQueueName
		job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
		job.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
		}
		createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob.Namespace, createdJob.Name)

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
		Eventually(func() bool {
			return !testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

		By("Verifying job pod is running")
		Eventually(func() bool {
			return testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

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

		cqWrapper := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name)
		cqWrapper.Spec.ResourceGroups = []kueuev1beta2.ResourceGroup{{
			CoveredResources: []corev1.ResourceName{"cpu", "memory", corev1.ResourceName(extendedResourceName)},
			Flavors: []kueuev1beta2.FlavorQuotas{{
				Name: kueuev1beta2.ResourceFlavorReference(resourceFlavor.Name),
				Resources: []kueuev1beta2.ResourceQuota{
					{Name: "cpu", NominalQuota: resource.MustParse("100")},
					{Name: "memory", NominalQuota: resource.MustParse("100Gi")},
					{Name: corev1.ResourceName(extendedResourceName), NominalQuota: *resource.NewQuantity(int64(maxGPUsPerNode), resource.DecimalSI)},
				},
			}},
		}}
		cq, cleanupCQ, err := cqWrapper.CreateWithObject(ctx, kueueClient)
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
		testutils.SetDRAJobCPU(job)
		job.Name = "er-exceed-job"
		job.Labels[testutils.QueueLabel] = erLocalQueueName
		exceededCount := *resource.NewQuantity(int64(maxGPUsPerNode+1), resource.DecimalSI)
		job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = exceededCount
		job.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceName(extendedResourceName): exceededCount,
		}
		createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob.Namespace, createdJob.Name)

		By("Verifying job remains suspended")
		Consistently(func() bool {
			return testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
		}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(BeTrue())
	})

	It("should admit waiting job after extended resource quota is freed", func(ctx context.Context) {
		By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
		kueueClient := clients.UpstreamKueueClient

		resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupResourceFlavor)

		cqWrapper := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name)
		cqWrapper.Spec.ResourceGroups = []kueuev1beta2.ResourceGroup{{
			CoveredResources: []corev1.ResourceName{"cpu", "memory", corev1.ResourceName(extendedResourceName)},
			Flavors: []kueuev1beta2.FlavorQuotas{{
				Name: kueuev1beta2.ResourceFlavorReference(resourceFlavor.Name),
				Resources: []kueuev1beta2.ResourceQuota{
					{Name: "cpu", NominalQuota: resource.MustParse("100")},
					{Name: "memory", NominalQuota: resource.MustParse("100Gi")},
					{Name: corev1.ResourceName(extendedResourceName), NominalQuota: *resource.NewQuantity(int64(maxGPUsPerNode), resource.DecimalSI)},
				},
			}},
		}}
		cq, cleanupCQ, err := cqWrapper.CreateWithObject(ctx, kueueClient)
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
		testutils.SetDRAJobCPU(job1)
		job1.Name = "er-fill-job-1"
		job1.Labels[testutils.QueueLabel] = erLocalQueueName
		fillCount := *resource.NewQuantity(int64(maxGPUsPerNode), resource.DecimalSI)
		job1.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = fillCount
		job1.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceName(extendedResourceName): fillCount,
		}
		createdJob1, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job1, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob1.Namespace, createdJob1.Name)

		By("Waiting for first job to be admitted")
		Eventually(func() bool {
			return !testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob1.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

		By("Verifying first job pod is running")
		Eventually(func() bool {
			return testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob1.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

		By("Creating second job requesting 1 GPU via extended resources (quota full, should be suspended)")
		job2 := builder.NewJob()
		testutils.SetDRAJobCPU(job2)
		job2.Name = "er-fill-job-2"
		job2.Labels[testutils.QueueLabel] = erLocalQueueName
		job2.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
		job2.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
		}
		createdJob2, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job2, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob2.Namespace, createdJob2.Name)

		By("Verifying second job is suspended (quota full)")
		Consistently(func() bool {
			return testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob2.Name)
		}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(BeTrue())

		By("Deleting first job to free quota")
		testutils.CleanUpJob(ctx, kubeClient, createdJob1.Namespace, createdJob1.Name)

		By("Verifying second job is admitted after quota freed")
		Eventually(func() bool {
			return !testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob2.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

		By("Verifying second job pod is running")
		Eventually(func() bool {
			return testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob2.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())
	})

	It("should verify ClusterQueue shows correct extended resource usage", func(ctx context.Context) {
		By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
		kueueClient := clients.UpstreamKueueClient

		resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupResourceFlavor)

		cqWrapper := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name)
		cqWrapper.Spec.ResourceGroups = []kueuev1beta2.ResourceGroup{{
			CoveredResources: []corev1.ResourceName{"cpu", "memory", corev1.ResourceName(extendedResourceName)},
			Flavors: []kueuev1beta2.FlavorQuotas{{
				Name: kueuev1beta2.ResourceFlavorReference(resourceFlavor.Name),
				Resources: []kueuev1beta2.ResourceQuota{
					{Name: "cpu", NominalQuota: resource.MustParse("100")},
					{Name: "memory", NominalQuota: resource.MustParse("100Gi")},
					{Name: corev1.ResourceName(extendedResourceName), NominalQuota: *resource.NewQuantity(int64(maxGPUsPerNode), resource.DecimalSI)},
				},
			}},
		}}
		cq, cleanupCQ, err := cqWrapper.CreateWithObject(ctx, kueueClient)
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
		testutils.SetDRAJobCPU(job)
		job.Name = "er-usage-job"
		job.Labels[testutils.QueueLabel] = erLocalQueueName
		job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
		job.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
		}
		createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob.Namespace, createdJob.Name)

		By("Verifying ClusterQueue shows extended resource reservation")
		Eventually(func(g Gomega) {
			cqObj, err := kueueClient.KueueV1beta2().ClusterQueues().Get(ctx, cq.Name, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cqObj.Status.FlavorsReservation).NotTo(BeEmpty())

			found := false
			for _, flavor := range cqObj.Status.FlavorsReservation {
				for _, res := range flavor.Resources {
					if res.Name == corev1.ResourceName(extendedResourceName) {
						g.Expect(res.Total.Cmp(resource.MustParse("1"))).To(Equal(0),
							"ClusterQueue should show 1 %s reserved", extendedResourceName)
						found = true
					}
				}
			}
			g.Expect(found).To(BeTrue(), "resource %s not found in ClusterQueue reservation", extendedResourceName)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By("Verifying job is unsuspended")
		Eventually(func() bool {
			return !testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

		By("Verifying job pod is running")
		Eventually(func() bool {
			return testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())
	})

	When("Extended Resources are used with deviceClassMappings", Label("dra-extended-resources"), func() {
		BeforeAll(func(ctx context.Context) {
			if maxGPUsPerNode < 2 {
				Skip("Need at least 2 GPUs on a single node for mixed ER + RCT test")
			}
		})

		It("should account both ER and RCT under same quota with deviceClassMappings taking precedence", func(ctx context.Context) {
			By("Adding deviceClassMappings to enable DynamicResourceAllocation for RCT path")
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			kueueInstance.Spec.Config.Resources.DeviceClassMappings = []ssv1.DeviceClassMapping{
				{
					Name:             testutils.DRALogicalResource,
					DeviceClassNames: []ssv1.DeviceClassName{testutils.DRADeviceClassName},
				},
			}
			testutils.ApplyKueueConfig(ctx, kueueInstance.Spec.Config, clients)

			Eventually(func() error {
				configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
				if err != nil {
					return err
				}
				configData := configMap.Data["controller_manager_config.yaml"]
				if !strings.Contains(configData, "DynamicResourceAllocation: true") {
					return fmt.Errorf("DynamicResourceAllocation not enabled yet")
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cqWrapper := testutils.NewClusterQueue().WithGenerateName().WithFlavorName(resourceFlavor.Name)
			cqWrapper.Spec.ResourceGroups = []kueuev1beta2.ResourceGroup{{
				CoveredResources: []corev1.ResourceName{"cpu", "memory", corev1.ResourceName(testutils.DRALogicalResource), corev1.ResourceName(extendedResourceName)},
				Flavors: []kueuev1beta2.FlavorQuotas{{
					Name: kueuev1beta2.ResourceFlavorReference(resourceFlavor.Name),
					Resources: []kueuev1beta2.ResourceQuota{
						{Name: "cpu", NominalQuota: resource.MustParse("100")},
						{Name: "memory", NominalQuota: resource.MustParse("100Gi")},
						{Name: corev1.ResourceName(testutils.DRALogicalResource), NominalQuota: *resource.NewQuantity(int64(maxGPUsPerNode), resource.DecimalSI)},
						{Name: corev1.ResourceName(extendedResourceName), NominalQuota: *resource.NewQuantity(int64(maxGPUsPerNode), resource.DecimalSI)},
					},
				}},
			}}
			cq, cleanupCQ, err := cqWrapper.CreateWithObject(ctx, kueueClient)
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
										DeviceClassName: testutils.DRADeviceClassName,
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
			testutils.SetDRAJobCPU(job)
			job.Name = "er-mixed-job"
			job.Labels[testutils.QueueLabel] = "er-mixed-queue"
			job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
			job.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
			}
			job.Spec.Template.Spec.ResourceClaims = []corev1.PodResourceClaim{
				{Name: "gpu", ResourceClaimTemplateName: ptr.To("gpu-template-mixed")},
			}
			job.Spec.Template.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{
				{Name: "gpu"},
			}
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob.Namespace, createdJob.Name)

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
				g.Expect(assignment.ResourceUsage).To(HaveKey(corev1.ResourceName(testutils.DRALogicalResource)))
				g.Expect(assignment.ResourceUsage[corev1.ResourceName(testutils.DRALogicalResource)]).To(Equal(resource.MustParse("2")),
					"both ER and RCT should be counted under %s", testutils.DRALogicalResource)

				// Verify ClusterQueue shows mapping name used, extended resource name stays at 0
				cqObj, err := kueueClient.KueueV1beta2().ClusterQueues().Get(ctx, cq.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				for _, flavor := range cqObj.Status.FlavorsReservation {
					for _, res := range flavor.Resources {
						if res.Name == corev1.ResourceName(testutils.DRALogicalResource) {
							g.Expect(res.Total.Cmp(resource.MustParse("2"))).To(Equal(0),
								"%s should show 2 reserved (ER + RCT converged), got %s", testutils.DRALogicalResource, res.Total.String())
						}
						if res.Name == corev1.ResourceName(extendedResourceName) {
							g.Expect(res.Total.Cmp(resource.MustParse("0"))).To(Equal(0),
								"%s should show 0 (mapped to %s), got %s", extendedResourceName, testutils.DRALogicalResource, res.Total.String())
						}
					}
				}
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying job is unsuspended")
			Eventually(func() bool {
				return !testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

			By("Verifying job pod is running")
			Eventually(func() bool {
				return testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

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
