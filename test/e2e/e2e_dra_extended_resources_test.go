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
	erTestNamespace         = "kueue-dra-er-test"
	erTestQueue             = "dra-er-test-queue"
	erClusterQueueName      = "dra-er-test-clusterqueue"
	erMixedTestQueue        = "dra-er-mixed-queue"
	erMixedClusterQueueName = "dra-er-mixed-clusterqueue"
	extendedResourceName    = "nvidia.com/gpu"
)

var _ = Describe("DRA Extended Resources", Label("operator", "dra", "dra-extended-resources"), Ordered, func() {
	var (
		initialKueueInstance  *ssv1.Kueue
		cleanupClusterQueue   func()
		cleanupLocalQueue     func()
		cleanupResourceFlavor func()
		maxGPUsPerNode        int
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

		// Check if ResourceSlices exist for gpu.nvidia.com (driver is running)
		// and count GPUs per node (only devices with type=="gpu", excluding MIG slices)
		slices, err := kubeClient.ResourceV1().ResourceSlices().List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		hasDriverSlices := false
		gpusPerNode := map[string]int{}
		for _, s := range slices.Items {
			if s.Spec.Driver == draDeviceClassName {
				hasDriverSlices = true
				if s.Spec.NodeName != nil {
					for _, d := range s.Spec.Devices {
						// The NVIDIA DRA driver uses the bare "type" attribute key (not fully qualified)
						// with value "gpu" for full GPU devices. MIG slices have different type values.
						// See: device.attributes['gpu.nvidia.com'].type in DeviceClass CEL selectors.
						if typeAttr, ok := d.Attributes["type"]; ok {
							if typeAttr.StringValue != nil && *typeAttr.StringValue == "gpu" {
								gpusPerNode[*s.Spec.NodeName]++
							}
						}
					}
				}
			}
		}
		if !hasDriverSlices {
			Skip("No ResourceSlices found for driver gpu.nvidia.com - NVIDIA DRA driver not running")
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

		kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		initialKueueInstance = kueueInstance.DeepCopy()

		applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)

		Eventually(func() error {
			configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
			if err != nil {
				return err
			}
			configData := configMap.Data["controller_manager_config.yaml"]
			if !strings.Contains(configData, "DRAExtendedResources: true") {
				return fmt.Errorf("DRAExtendedResources not enabled yet")
			}
			return nil
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		_, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: erTestNamespace,
				Labels: map[string]string{
					testutils.OpenShiftManagedLabel: "true",
				},
			},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		kueueClient := clients.UpstreamKueueClient
		cleanupResourceFlavor, err = testutils.NewResourceFlavor().Create(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())

		cq := testutils.NewClusterQueue()
		cq.Name = erClusterQueueName
		cq.Spec.ResourceGroups = []kueuev1beta2.ResourceGroup{{
			CoveredResources: []corev1.ResourceName{"cpu", "memory", corev1.ResourceName(extendedResourceName)},
			Flavors: []kueuev1beta2.FlavorQuotas{{
				Name: "default",
				Resources: []kueuev1beta2.ResourceQuota{
					{Name: "cpu", NominalQuota: resource.MustParse("100")},
					{Name: "memory", NominalQuota: resource.MustParse("100Gi")},
					{Name: corev1.ResourceName(extendedResourceName), NominalQuota: *resource.NewQuantity(int64(maxGPUsPerNode), resource.DecimalSI)},
				},
			}},
		}}
		cleanupClusterQueue, err = cq.Create(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())

		lq := testutils.NewLocalQueue(erTestNamespace, erTestQueue)
		lq.Spec.ClusterQueue = kueuev1beta2.ClusterQueueReference(erClusterQueueName)
		cleanupLocalQueue, err = lq.Create(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func(ctx context.Context) {
		if cleanupLocalQueue != nil {
			cleanupLocalQueue()
		}
		if cleanupClusterQueue != nil {
			cleanupClusterQueue()
		}
		if cleanupResourceFlavor != nil {
			cleanupResourceFlavor()
		}

		kubeClient.CoreV1().Namespaces().Delete(ctx, erTestNamespace, metav1.DeleteOptions{})

		if initialKueueInstance != nil {
			applyKueueConfig(ctx, initialKueueInstance.Spec.Config, kubeClient)
		}
	})

	It("should admit job with extended resource request and account DRA quota correctly", func(ctx context.Context) {
		By("Creating Job with extended resource request nvidia.com/gpu")
		builder := testutils.NewTestResourceBuilder(erTestNamespace, erTestQueue)
		job := builder.NewJob()
		setDRAJobCPU(job)
		job.Name = "er-basic-job"
		job.Labels[testutils.QueueLabel] = erTestQueue
		job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
		job.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
		}
		createdJob, err := kubeClient.BatchV1().Jobs(erTestNamespace).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

		By("Verifying workload is admitted with correct quota under extendedResourceName")
		kueueClient := clients.UpstreamKueueClient
		Eventually(func(g Gomega) {
			wlList, err := kueueClient.KueueV1beta2().Workloads(erTestNamespace).List(ctx, metav1.ListOptions{
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
			return !testutils.IsJobSuspended(ctx, kubeClient, erTestNamespace, createdJob.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

		By("Verifying job pod is running")
		Eventually(func() bool {
			return testutils.IsJobPodRunning(ctx, kubeClient, erTestNamespace, createdJob.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

		By("Verifying extendedResourceClaimStatus is populated in pod status")
		Eventually(func(g Gomega) {
			pods, err := kubeClient.CoreV1().Pods(erTestNamespace).List(ctx, metav1.ListOptions{
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
			pods, err := kubeClient.CoreV1().Pods(erTestNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("batch.kubernetes.io/job-name=%s", createdJob.Name),
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(pods.Items).NotTo(BeEmpty())

			claimName := pods.Items[0].Status.ExtendedResourceClaimStatus.ResourceClaimName
			claim, err := kubeClient.ResourceV1().ResourceClaims(erTestNamespace).Get(ctx, claimName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(claim.Annotations).To(HaveKeyWithValue("resource.kubernetes.io/extended-resource-claim", "true"))
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
	})

	It("should suspend job when extended resource quota is exceeded", func(ctx context.Context) {
		By(fmt.Sprintf("Creating Job requesting %d GPUs via extended resources (quota is %d)", maxGPUsPerNode+1, maxGPUsPerNode))
		builder := testutils.NewTestResourceBuilder(erTestNamespace, erTestQueue)
		job := builder.NewJob()
		setDRAJobCPU(job)
		job.Name = "er-exceed-job"
		job.Labels[testutils.QueueLabel] = erTestQueue
		exceededCount := *resource.NewQuantity(int64(maxGPUsPerNode+1), resource.DecimalSI)
		job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = exceededCount
		job.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceName(extendedResourceName): exceededCount,
		}
		createdJob, err := kubeClient.BatchV1().Jobs(erTestNamespace).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

		By("Verifying job remains suspended")
		Consistently(func() bool {
			return testutils.IsJobSuspended(ctx, kubeClient, erTestNamespace, createdJob.Name)
		}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(BeTrue())
	})

	It("should admit waiting job after extended resource quota is freed", func(ctx context.Context) {
		By("Waiting for quota to be fully released from previous tests")
		kueueClient := clients.UpstreamKueueClient
		Eventually(func(g Gomega) {
			cq, err := kueueClient.KueueV1beta2().ClusterQueues().Get(ctx, erClusterQueueName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			for _, flavor := range cq.Status.FlavorsReservation {
				for _, res := range flavor.Resources {
					if res.Name == corev1.ResourceName(extendedResourceName) {
						g.Expect(res.Total.Cmp(resource.MustParse("0"))).To(Equal(0),
							"%s quota should be 0 before test starts, got %s", extendedResourceName, res.Total.String())
					}
				}
			}
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By(fmt.Sprintf("Creating first job requesting all %d GPUs via extended resources (fills quota)", maxGPUsPerNode))
		builder := testutils.NewTestResourceBuilder(erTestNamespace, erTestQueue)
		job1 := builder.NewJob()
		setDRAJobCPU(job1)
		job1.Name = "er-fill-job-1"
		job1.Labels[testutils.QueueLabel] = erTestQueue
		fillCount := *resource.NewQuantity(int64(maxGPUsPerNode), resource.DecimalSI)
		job1.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = fillCount
		job1.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceName(extendedResourceName): fillCount,
		}
		createdJob1, err := kubeClient.BatchV1().Jobs(erTestNamespace).Create(ctx, job1, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer testutils.CleanUpJob(ctx, kubeClient, createdJob1.Namespace, createdJob1.Name)

		By("Waiting for first job to be admitted")
		Eventually(func() bool {
			return !testutils.IsJobSuspended(ctx, kubeClient, erTestNamespace, createdJob1.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

		By("Verifying first job pod is running")
		Eventually(func() bool {
			return testutils.IsJobPodRunning(ctx, kubeClient, erTestNamespace, createdJob1.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

		By("Creating second job requesting 1 GPU via extended resources (quota full, should be suspended)")
		job2 := builder.NewJob()
		setDRAJobCPU(job2)
		job2.Name = "er-fill-job-2"
		job2.Labels[testutils.QueueLabel] = erTestQueue
		job2.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
		job2.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
		}
		createdJob2, err := kubeClient.BatchV1().Jobs(erTestNamespace).Create(ctx, job2, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer testutils.CleanUpJob(ctx, kubeClient, createdJob2.Namespace, createdJob2.Name)

		By("Verifying second job is suspended (quota full)")
		Consistently(func() bool {
			return testutils.IsJobSuspended(ctx, kubeClient, erTestNamespace, createdJob2.Name)
		}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(BeTrue())

		By("Deleting first job to free quota")
		testutils.CleanUpJob(ctx, kubeClient, createdJob1.Namespace, createdJob1.Name)

		By("Verifying second job is admitted after quota freed")
		Eventually(func() bool {
			return !testutils.IsJobSuspended(ctx, kubeClient, erTestNamespace, createdJob2.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

		By("Verifying second job pod is running")
		Eventually(func() bool {
			return testutils.IsJobPodRunning(ctx, kubeClient, erTestNamespace, createdJob2.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())
	})

	It("should verify ClusterQueue shows correct extended resource usage", func(ctx context.Context) {
		By("Waiting for quota to be fully released from previous tests")
		kueueClient := clients.UpstreamKueueClient
		Eventually(func(g Gomega) {
			cq, err := kueueClient.KueueV1beta2().ClusterQueues().Get(ctx, erClusterQueueName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			for _, flavor := range cq.Status.FlavorsReservation {
				for _, res := range flavor.Resources {
					if res.Name == corev1.ResourceName(extendedResourceName) {
						g.Expect(res.Total.Cmp(resource.MustParse("0"))).To(Equal(0),
							"%s quota should be 0 before test starts, got %s", extendedResourceName, res.Total.String())
					}
				}
			}
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By("Creating Job with 1 GPU via extended resource request")
		builder := testutils.NewTestResourceBuilder(erTestNamespace, erTestQueue)
		job := builder.NewJob()
		setDRAJobCPU(job)
		job.Name = "er-usage-job"
		job.Labels[testutils.QueueLabel] = erTestQueue
		job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResourceName)] = resource.MustParse("1")
		job.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceName(extendedResourceName): resource.MustParse("1"),
		}
		createdJob, err := kubeClient.BatchV1().Jobs(erTestNamespace).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

		By("Verifying ClusterQueue shows extended resource reservation")
		Eventually(func(g Gomega) {
			cq, err := kueueClient.KueueV1beta2().ClusterQueues().Get(ctx, erClusterQueueName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cq.Status.FlavorsReservation).NotTo(BeEmpty())

			found := false
			for _, flavor := range cq.Status.FlavorsReservation {
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

		By("Verifying job pod is running")
		Eventually(func() bool {
			return testutils.IsJobPodRunning(ctx, kubeClient, erTestNamespace, createdJob.Name)
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
				if !strings.Contains(configData, "DynamicResourceAllocation: true") {
					return fmt.Errorf("DynamicResourceAllocation not enabled yet")
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Creating ClusterQueue and LocalQueue for mixed test")
			kueueClient := clients.UpstreamKueueClient

			mixedCQ := testutils.NewClusterQueue()
			mixedCQ.Name = erMixedClusterQueueName
			mixedCQ.Spec.ResourceGroups = []kueuev1beta2.ResourceGroup{{
				CoveredResources: []corev1.ResourceName{"cpu", "memory", corev1.ResourceName(draLogicalResource), corev1.ResourceName(extendedResourceName)},
				Flavors: []kueuev1beta2.FlavorQuotas{{
					Name: "default",
					Resources: []kueuev1beta2.ResourceQuota{
						{Name: "cpu", NominalQuota: resource.MustParse("100")},
						{Name: "memory", NominalQuota: resource.MustParse("100Gi")},
						{Name: corev1.ResourceName(draLogicalResource), NominalQuota: *resource.NewQuantity(int64(maxGPUsPerNode), resource.DecimalSI)},
						{Name: corev1.ResourceName(extendedResourceName), NominalQuota: *resource.NewQuantity(int64(maxGPUsPerNode), resource.DecimalSI)},
					},
				}},
			}}
			cleanupMixedCQ, err := mixedCQ.Create(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			defer cleanupMixedCQ()

			mixedLQ := testutils.NewLocalQueue(erTestNamespace, erMixedTestQueue)
			mixedLQ.Spec.ClusterQueue = kueuev1beta2.ClusterQueueReference(erMixedClusterQueueName)
			cleanupMixedLQ, err := mixedLQ.Create(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			defer cleanupMixedLQ()

			By("Creating ResourceClaimTemplate for 1 GPU")
			rct := &resourcev1.ResourceClaimTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gpu-template-mixed",
					Namespace: erTestNamespace,
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
			_, err = kubeClient.ResourceV1().ResourceClaimTemplates(erTestNamespace).Create(ctx, rct, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer kubeClient.ResourceV1().ResourceClaimTemplates(erTestNamespace).Delete(ctx, rct.Name, metav1.DeleteOptions{})

			By("Creating Job with both extended resource request and ResourceClaimTemplate")
			builder := testutils.NewTestResourceBuilder(erTestNamespace, erMixedTestQueue)
			job := builder.NewJob()
			setDRAJobCPU(job)
			job.Name = "er-mixed-job"
			job.Labels[testutils.QueueLabel] = erMixedTestQueue
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
			createdJob, err := kubeClient.BatchV1().Jobs(erTestNamespace).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

			By("Verifying workload is admitted and deviceClassMappings name takes precedence")
			Eventually(func(g Gomega) {
				wlList, err := kueueClient.KueueV1beta2().Workloads(erTestNamespace).List(ctx, metav1.ListOptions{
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
				cqObj, err := kueueClient.KueueV1beta2().ClusterQueues().Get(ctx, erMixedClusterQueueName, metav1.GetOptions{})
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
			Eventually(func() bool {
				return !testutils.IsJobSuspended(ctx, kubeClient, erTestNamespace, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

			By("Verifying job pod is running")
			Eventually(func() bool {
				return testutils.IsJobPodRunning(ctx, kubeClient, erTestNamespace, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

			By("Verifying pod has both extendedResourceClaimStatus and resourceClaimStatuses")
			Eventually(func(g Gomega) {
				pods, err := kubeClient.CoreV1().Pods(erTestNamespace).List(ctx, metav1.ListOptions{
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
