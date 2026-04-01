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
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
)

const (
	draDeviceClassName  = "gpu.nvidia.com"
	draLogicalResource  = "nvidia-gpu"
	draTestNamespace    = "kueue-dra-test"
	draTestQueue        = "dra-test-queue"
	draClusterQueueName = "dra-test-clusterqueue"
)

// setDRAJobCPU reduces CPU request for DRA test jobs to fit on GPU nodes
// where NVIDIA operator daemonsets consume most of the CPU budget.
func setDRAJobCPU(job *batchv1.Job) {
	job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("100m")
}

var _ = Describe("DRA Structured Parameters", Label("operator", "dra"), Ordered, func() {
	var (
		draSupported          bool
		initialKueueInstance  *ssv1.Kueue
		cleanupClusterQueue   func()
		cleanupLocalQueue     func()
		cleanupResourceFlavor func()
	)

	JustAfterEach(func(ctx context.Context) {
		testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 500)
	})

	BeforeAll(func(ctx context.Context) {
		// Check if DRA APIs are available
		apiResourceLists, err := kubeClient.Discovery().ServerResourcesForGroupVersion("resource.k8s.io/v1")
		if err == nil {
			for _, apiResource := range apiResourceLists.APIResources {
				if apiResource.Kind == "DeviceClass" {
					draSupported = true
					break
				}
			}
		}
		if !draSupported {
			Skip("DRA APIs (resource.k8s.io/v1) not available on this cluster")
		}

		// Check if ResourceSlices exist for gpu.nvidia.com (driver is running)
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

		kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		initialKueueInstance = kueueInstance.DeepCopy()

		found := false
		for i, m := range kueueInstance.Spec.Config.Resources.DeviceClassMappings {
			if m.Name == draLogicalResource {
				kueueInstance.Spec.Config.Resources.DeviceClassMappings[i].DeviceClassNames = []ssv1.DeviceClassName{draDeviceClassName}
				found = true
				break
			}
		}
		if !found {
			kueueInstance.Spec.Config.Resources.DeviceClassMappings = append(
				kueueInstance.Spec.Config.Resources.DeviceClassMappings,
				ssv1.DeviceClassMapping{
					Name:             draLogicalResource,
					DeviceClassNames: []ssv1.DeviceClassName{draDeviceClassName},
				},
			)
		}
		applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)

		// Wait for DRA feature gate to be enabled in Kueue config
		Eventually(func() error {
			configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
			if err != nil {
				return err
			}
			configData := configMap.Data["controller_manager_config.yaml"]
			if !strings.Contains(configData, "DynamicResourceAllocation: true") {
				return fmt.Errorf("DynamicResourceAllocation not enabled yet")
			}
			if !strings.Contains(configData, draLogicalResource) {
				return fmt.Errorf("deviceClassMappings not configured yet")
			}
			return nil
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		_, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: draTestNamespace,
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
		cq.Name = draClusterQueueName
		cq.Spec.ResourceGroups = []kueuev1beta2.ResourceGroup{{
			CoveredResources: []corev1.ResourceName{"cpu", "memory", corev1.ResourceName(draLogicalResource)},
			Flavors: []kueuev1beta2.FlavorQuotas{{
				Name: "default",
				Resources: []kueuev1beta2.ResourceQuota{
					{Name: "cpu", NominalQuota: resource.MustParse("100")},
					{Name: "memory", NominalQuota: resource.MustParse("100Gi")},
					{Name: corev1.ResourceName(draLogicalResource), NominalQuota: resource.MustParse("1")},
				},
			}},
		}}
		cleanupClusterQueue, err = cq.Create(ctx, kueueClient)
		Expect(err).NotTo(HaveOccurred())

		lq := testutils.NewLocalQueue(draTestNamespace, draTestQueue)
		lq.Spec.ClusterQueue = kueuev1beta2.ClusterQueueReference(draClusterQueueName)
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

		kubeClient.CoreV1().Namespaces().Delete(ctx, draTestNamespace, metav1.DeleteOptions{})

		if initialKueueInstance != nil {
			applyKueueConfig(ctx, initialKueueInstance.Spec.Config, kubeClient)
		}
	})

	It("should admit job with ResourceClaimTemplate and account DRA quota correctly", func(ctx context.Context) {
		By("Creating ResourceClaimTemplate for gpu.nvidia.com")
		rct := &resourcev1.ResourceClaimTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gpu-template-basic",
				Namespace: draTestNamespace,
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
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(draTestNamespace).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer kubeClient.ResourceV1().ResourceClaimTemplates(draTestNamespace).Delete(ctx, rct.Name, metav1.DeleteOptions{})

		By("Creating Job referencing ResourceClaimTemplate")
		builder := testutils.NewTestResourceBuilder(draTestNamespace, draTestQueue)
		job := builder.NewJob()
		setDRAJobCPU(job)
		job.Name = "dra-basic-job"
		job.Labels[testutils.QueueLabel] = draTestQueue
		job.Spec.Template.Spec.ResourceClaims = []corev1.PodResourceClaim{
			{Name: "gpu", ResourceClaimTemplateName: ptr.To("gpu-template-basic")},
		}
		job.Spec.Template.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{
			{Name: "gpu"},
		}
		createdJob, err := kubeClient.BatchV1().Jobs(draTestNamespace).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

		By("Verifying workload is admitted with correct DRA quota")
		kueueClient := clients.UpstreamKueueClient
		Eventually(func(g Gomega) {
			wlList, err := kueueClient.KueueV1beta2().Workloads(draTestNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", string(createdJob.UID)),
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(wlList.Items).NotTo(BeEmpty(), "workload not found for job %s", createdJob.Name)

			wl := wlList.Items[0]
			g.Expect(wl.Status.Admission).NotTo(BeNil(), "workload should be admitted")
			g.Expect(wl.Status.Admission.PodSetAssignments).To(HaveLen(1))

			assignment := wl.Status.Admission.PodSetAssignments[0]
			g.Expect(assignment.ResourceUsage).To(HaveKey(corev1.ResourceName(draLogicalResource)))
			g.Expect(assignment.ResourceUsage[corev1.ResourceName(draLogicalResource)]).To(Equal(resource.MustParse("1")))
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By("Verifying job is unsuspended")
		Eventually(func() bool {
			j, err := kubeClient.BatchV1().Jobs(draTestNamespace).Get(ctx, createdJob.Name, metav1.GetOptions{})
			if err != nil {
				return false
			}
			return j.Spec.Suspend == nil || !*j.Spec.Suspend
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

		By("Verifying job pod is running")
		Eventually(func() bool {
			return testutils.IsJobPodRunning(ctx, kubeClient, draTestNamespace, createdJob.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())
	})

	It("should suspend job when DRA quota is exceeded", func(ctx context.Context) {
		By("Creating ResourceClaimTemplate requesting more GPUs than quota allows")
		rct := &resourcev1.ResourceClaimTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gpu-template-exceed",
				Namespace: draTestNamespace,
			},
			Spec: resourcev1.ResourceClaimTemplateSpec{
				Spec: resourcev1.ResourceClaimSpec{
					Devices: resourcev1.DeviceClaim{
						Requests: []resourcev1.DeviceRequest{
							{
								Name: "gpu-req",
								Exactly: &resourcev1.ExactDeviceRequest{
									DeviceClassName: draDeviceClassName,
									Count:           5, // quota is 1
								},
							},
						},
					},
				},
			},
		}
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(draTestNamespace).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer kubeClient.ResourceV1().ResourceClaimTemplates(draTestNamespace).Delete(ctx, rct.Name, metav1.DeleteOptions{})

		By("Creating Job that exceeds DRA quota")
		builder := testutils.NewTestResourceBuilder(draTestNamespace, draTestQueue)
		job := builder.NewJob()
		setDRAJobCPU(job)
		job.Name = "dra-exceed-job"
		job.Labels[testutils.QueueLabel] = draTestQueue
		job.Spec.Template.Spec.ResourceClaims = []corev1.PodResourceClaim{
			{Name: "gpu", ResourceClaimTemplateName: ptr.To("gpu-template-exceed")},
		}
		job.Spec.Template.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{
			{Name: "gpu"},
		}
		createdJob, err := kubeClient.BatchV1().Jobs(draTestNamespace).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

		By("Verifying job remains suspended")
		Consistently(func() bool {
			return testutils.IsJobSuspended(ctx, kubeClient, draTestNamespace, createdJob.Name)
		}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(BeTrue())
	})

	It("should admit waiting job after DRA quota is freed", func(ctx context.Context) {
		By("Waiting for DRA quota to be fully released from previous tests")
		kueueClient := clients.UpstreamKueueClient
		Eventually(func(g Gomega) {
			cq, err := kueueClient.KueueV1beta2().ClusterQueues().Get(ctx, draClusterQueueName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			for _, flavor := range cq.Status.FlavorsReservation {
				for _, res := range flavor.Resources {
					if res.Name == corev1.ResourceName(draLogicalResource) {
						g.Expect(res.Total.Cmp(resource.MustParse("0"))).To(Equal(0),
							"%s quota should be 0 before test starts, got %s", draLogicalResource, res.Total.String())
					}
				}
			}
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By("Creating shared ResourceClaimTemplate requesting 1 GPU")
		rct := &resourcev1.ResourceClaimTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gpu-template-shared",
				Namespace: draTestNamespace,
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
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(draTestNamespace).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer kubeClient.ResourceV1().ResourceClaimTemplates(draTestNamespace).Delete(ctx, rct.Name, metav1.DeleteOptions{})

		By("Creating first job referencing shared template (fills 1/1 quota)")
		builder := testutils.NewTestResourceBuilder(draTestNamespace, draTestQueue)
		job1 := builder.NewJob()
		setDRAJobCPU(job1)
		job1.Name = "dra-fill-job-1"
		job1.Labels[testutils.QueueLabel] = draTestQueue
		job1.Spec.Template.Spec.ResourceClaims = []corev1.PodResourceClaim{
			{Name: "gpu", ResourceClaimTemplateName: ptr.To("gpu-template-shared")},
		}
		job1.Spec.Template.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{
			{Name: "gpu"},
		}
		createdJob1, err := kubeClient.BatchV1().Jobs(draTestNamespace).Create(ctx, job1, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer testutils.CleanUpJob(ctx, kubeClient, createdJob1.Namespace, createdJob1.Name)

		By("Waiting for first job to be admitted")
		Eventually(func() bool {
			return !testutils.IsJobSuspended(ctx, kubeClient, draTestNamespace, createdJob1.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

		By("Verifying first job pod is running")
		Eventually(func() bool {
			return testutils.IsJobPodRunning(ctx, kubeClient, draTestNamespace, createdJob1.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

		By("Creating second job referencing same shared template (quota full, should be suspended)")
		job2 := builder.NewJob()
		setDRAJobCPU(job2)
		job2.Name = "dra-fill-job-2"
		job2.Labels[testutils.QueueLabel] = draTestQueue
		job2.Spec.Template.Spec.ResourceClaims = []corev1.PodResourceClaim{
			{Name: "gpu", ResourceClaimTemplateName: ptr.To("gpu-template-shared")},
		}
		job2.Spec.Template.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{
			{Name: "gpu"},
		}
		createdJob2, err := kubeClient.BatchV1().Jobs(draTestNamespace).Create(ctx, job2, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer testutils.CleanUpJob(ctx, kubeClient, createdJob2.Namespace, createdJob2.Name)

		By("Verifying second job is suspended (quota full)")
		Consistently(func() bool {
			return testutils.IsJobSuspended(ctx, kubeClient, draTestNamespace, createdJob2.Name)
		}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(BeTrue())

		By("Deleting first job to free quota")
		testutils.CleanUpJob(ctx, kubeClient, createdJob1.Namespace, createdJob1.Name)

		By("Verifying second job is admitted after quota freed")
		Eventually(func() bool {
			return !testutils.IsJobSuspended(ctx, kubeClient, draTestNamespace, createdJob2.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

		By("Verifying second job pod is running")
		Eventually(func() bool {
			return testutils.IsJobPodRunning(ctx, kubeClient, draTestNamespace, createdJob2.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())
	})

	It("should verify ClusterQueue shows correct DRA resource usage", func(ctx context.Context) {
		By("Waiting for DRA quota to be fully released from previous tests")
		kueueClient := clients.UpstreamKueueClient
		Eventually(func(g Gomega) {
			cq, err := kueueClient.KueueV1beta2().ClusterQueues().Get(ctx, draClusterQueueName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			for _, flavor := range cq.Status.FlavorsReservation {
				for _, res := range flavor.Resources {
					if res.Name == corev1.ResourceName(draLogicalResource) {
						g.Expect(res.Total.Cmp(resource.MustParse("0"))).To(Equal(0),
							"%s quota should be 0 before test starts, got %s", draLogicalResource, res.Total.String())
					}
				}
			}
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By("Creating ResourceClaimTemplate for 1 GPU")
		rct := &resourcev1.ResourceClaimTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gpu-template-usage",
				Namespace: draTestNamespace,
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
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(draTestNamespace).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer kubeClient.ResourceV1().ResourceClaimTemplates(draTestNamespace).Delete(ctx, rct.Name, metav1.DeleteOptions{})

		By("Creating Job")
		builder := testutils.NewTestResourceBuilder(draTestNamespace, draTestQueue)
		job := builder.NewJob()
		setDRAJobCPU(job)
		job.Name = "dra-usage-job"
		job.Labels[testutils.QueueLabel] = draTestQueue
		job.Spec.Template.Spec.ResourceClaims = []corev1.PodResourceClaim{
			{Name: "gpu", ResourceClaimTemplateName: ptr.To("gpu-template-usage")},
		}
		job.Spec.Template.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{
			{Name: "gpu"},
		}
		createdJob, err := kubeClient.BatchV1().Jobs(draTestNamespace).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)

		By("Verifying ClusterQueue shows DRA resource reservation")
		Eventually(func(g Gomega) {
			cq, err := kueueClient.KueueV1beta2().ClusterQueues().Get(ctx, draClusterQueueName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cq.Status.FlavorsReservation).NotTo(BeEmpty())

			found := false
			for _, flavor := range cq.Status.FlavorsReservation {
				for _, res := range flavor.Resources {
					if res.Name == corev1.ResourceName(draLogicalResource) {
						g.Expect(res.Total.Cmp(resource.MustParse("1"))).To(Equal(0),
							"ClusterQueue should show 1 %s reserved", draLogicalResource)
						found = true
					}
				}
			}
			g.Expect(found).To(BeTrue(), "DRA resource %s not found in ClusterQueue reservation", draLogicalResource)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		By("Verifying job pod is running")
		Eventually(func() bool {
			return testutils.IsJobPodRunning(ctx, kubeClient, draTestNamespace, createdJob.Name)
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())
	})
})
