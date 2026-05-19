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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ssv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
	upstreamkueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"
)

const (
	draDeviceClassName     = "gpu.nvidia.com"
	draLogicalResource     = "nvidia-gpu"
	draTestNamespacePrefix = "kueue-dra-test-"
	draLocalQueueName      = "dra-test-queue"
)

// setDRAJobCPU reduces CPU request for DRA test jobs to fit on GPU nodes
// where NVIDIA operator daemonsets consume most of the CPU budget.
func setDRAJobCPU(job *batchv1.Job) {
	job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("100m")
}

var _ = Describe("DRA Structured Parameters", Label("operator", "dra"), Ordered, func() {
	var (
		gpuCount             int
		initialKueueInstance *ssv1.Kueue
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

		// Wait for ResourceSlices from gpu.nvidia.com (NVIDIA DRA driver may still be deploying)
		var slices *resourcev1.ResourceSliceList
		hasDriverSlices := false
		for i := 0; i < 6 && !hasDriverSlices; i++ {
			if i > 0 {
				time.Sleep(5 * time.Second)
			}
			slices, err = kubeClient.ResourceV1().ResourceSlices().List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			for _, s := range slices.Items {
				if s.Spec.Driver == draDeviceClassName {
					hasDriverSlices = true
					break
				}
			}
		}
		if !hasDriverSlices {
			Skip("No ResourceSlices found for driver gpu.nvidia.com - NVIDIA DRA driver not running")
		}

		for _, s := range slices.Items {
			if s.Spec.Driver == draDeviceClassName {
				for _, d := range s.Spec.Devices {
					if typeAttr, ok := d.Attributes["type"]; ok {
						if typeAttr.StringValue != nil && *typeAttr.StringValue == gpuDeviceType {
							gpuCount++
						}
					}
				}
			}
		}
		if gpuCount == 0 {
			Skip("No GPU devices found in ResourceSlices, skipping test")
		}

		// Save the current Kueue config so it can be restored in AfterAll
		kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		initialKueueInstance = kueueInstance.DeepCopy()

		// Ensure deviceClassMappings maps the logical resource (nvidia-gpu) to the
		// actual DeviceClass (gpu.nvidia.com). Update the existing entry if present,
		// otherwise append a new one.
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

		// Wait for the operator to reconcile the config into the kueue-manager-config ConfigMap
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

	})

	AfterAll(func(ctx context.Context) {
		if initialKueueInstance != nil {
			// Clear deviceClassMappings rather than restoring originals to avoid
			// contaminating the extended-resources suite which manages its own mappings.
			initialKueueInstance.Spec.Config.Resources.DeviceClassMappings = nil
			restoreKueueConfig(ctx, initialKueueInstance.Spec.Config, kubeClient)
		}
	})

	When("basic DRA quota is configured", func() {
		It("should admit job with ResourceClaimTemplate and account DRA quota correctly", func(ctx context.Context) {
			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cq, cleanupCQ, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithFlavorName(resourceFlavor.Name).
				WithDRAResource(draLogicalResource, "1").
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: draTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lq := testutils.NewLocalQueue(ns.Name, draLocalQueueName).WithClusterQueue(cq.Name)
			_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Creating ResourceClaimTemplate for gpu.nvidia.com")
			rct := newDRAResourceClaimTemplate("gpu-template-basic", ns.Name, 1)
			_, err = kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating Job referencing ResourceClaimTemplate")
			builder := testutils.NewTestResourceBuilder(ns.Name, draLocalQueueName)
			job := newDRAJob(builder, "dra-basic-job", "gpu-template-basic", draLocalQueueName)
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob.Namespace, createdJob.Name)

			By("Verifying workload is admitted with correct DRA quota")
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
				g.Expect(assignment.ResourceUsage[corev1.ResourceName(draLogicalResource)]).To(Equal(resource.MustParse("1")))
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

		It("should suspend job when DRA quota is exceeded", func(ctx context.Context) {
			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cq, cleanupCQ, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithFlavorName(resourceFlavor.Name).
				WithDRAResource(draLogicalResource, "1").
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: draTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lq := testutils.NewLocalQueue(ns.Name, draLocalQueueName).WithClusterQueue(cq.Name)
			_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Creating ResourceClaimTemplate requesting more GPUs than quota allows")
			rct := newDRAResourceClaimTemplate("gpu-template-exceed", ns.Name, 5) // quota is 1
			_, err = kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating Job that exceeds DRA quota")
			builder := testutils.NewTestResourceBuilder(ns.Name, draLocalQueueName)
			job := newDRAJob(builder, "dra-exceed-job", "gpu-template-exceed", draLocalQueueName)
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob.Namespace, createdJob.Name)

			By("Verifying job is suspended due to exceeding DRA quota")
			Eventually(func() bool {
				return testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())
		})

		It("should admit waiting job after DRA quota is freed", func(ctx context.Context) {
			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cq, cleanupCQ, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithFlavorName(resourceFlavor.Name).
				WithDRAResource(draLogicalResource, "1").
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: draTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lq := testutils.NewLocalQueue(ns.Name, draLocalQueueName).WithClusterQueue(cq.Name)
			_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Creating shared ResourceClaimTemplate requesting 1 GPU")
			rct := newDRAResourceClaimTemplate("gpu-template-shared", ns.Name, 1)
			_, err = kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating first job referencing shared template (fills 1/1 quota)")
			builder := testutils.NewTestResourceBuilder(ns.Name, draLocalQueueName)
			job1 := newDRAJob(builder, "dra-fill-job-1", "gpu-template-shared", draLocalQueueName)
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

			By("Verifying ClusterQueue shows reservation before creating second job")
			Eventually(func(g Gomega) {
				cqObj, err := kueueClient.KueueV1beta2().ClusterQueues().Get(ctx, cq.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cqObj.Status.FlavorsReservation).NotTo(BeEmpty())
				found := false
				for _, flavor := range cqObj.Status.FlavorsReservation {
					for _, res := range flavor.Resources {
						if res.Name == corev1.ResourceName(draLogicalResource) && res.Total.Cmp(resource.MustParse("1")) == 0 {
							found = true
						}
					}
				}
				g.Expect(found).To(BeTrue(), "ClusterQueue should show 1 %s reserved before creating second job", draLogicalResource)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Creating second job referencing same shared template (quota full, should be suspended)")
			job2 := newDRAJob(builder, "dra-fill-job-2", "gpu-template-shared", draLocalQueueName)
			createdJob2, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job2, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob2.Namespace, createdJob2.Name)

			By("Waiting for second job to be suspended (quota full)")
			Eventually(func() bool {
				return testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob2.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

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

		It("should verify ClusterQueue shows correct DRA resource usage", func(ctx context.Context) {
			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cq, cleanupCQ, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithFlavorName(resourceFlavor.Name).
				WithDRAResource(draLogicalResource, "1").
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: draTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lq := testutils.NewLocalQueue(ns.Name, draLocalQueueName).WithClusterQueue(cq.Name)
			_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Creating ResourceClaimTemplate for 1 GPU")
			rct := newDRAResourceClaimTemplate("gpu-template-usage", ns.Name, 1)
			_, err = kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating Job")
			builder := testutils.NewTestResourceBuilder(ns.Name, draLocalQueueName)
			job := newDRAJob(builder, "dra-usage-job", "gpu-template-usage", draLocalQueueName)
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob.Namespace, createdJob.Name)

			By("Verifying ClusterQueue shows DRA resource reservation")
			Eventually(func(g Gomega) {
				cqObj, err := kueueClient.KueueV1beta2().ClusterQueues().Get(ctx, cq.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cqObj.Status.FlavorsReservation).NotTo(BeEmpty())

				found := false
				for _, flavor := range cqObj.Status.FlavorsReservation {
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
				return testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())
		})
	})

	When("two containers share a single GPU", func() {
		It("should admit job with two containers sharing a single GPU", func(ctx context.Context) {
			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cq, cleanupCQ, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithFlavorName(resourceFlavor.Name).
				WithDRAResource(draLogicalResource, "1").
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: draTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lq := testutils.NewLocalQueue(ns.Name, draLocalQueueName).WithClusterQueue(cq.Name)
			_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Creating ResourceClaimTemplate for a single GPU")
			rct := newDRAResourceClaimTemplate("gpu-template-shared-ctr", ns.Name, 1)
			_, err = kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating Job with two containers sharing the same GPU claim")
			builder := testutils.NewTestResourceBuilder(ns.Name, draLocalQueueName)
			job := newDRAJob(builder, "dra-shared-gpu-job", "gpu-template-shared-ctr", draLocalQueueName)
			job.Spec.Template.Spec.Containers = append(job.Spec.Template.Spec.Containers, corev1.Container{
				Name:    "ctr1",
				Image:   testutils.GetContainerImageForWorkloads(),
				Command: []string{"sh", "-c", "echo Container 1 sharing GPU; sleep 10"},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("100m"),
					},
					Claims: []corev1.ResourceClaim{{Name: "gpu"}},
				},
			})
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob.Namespace, createdJob.Name)

			By("Verifying workload is admitted with correct DRA quota (1 GPU shared between 2 containers)")
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
				g.Expect(assignment.ResourceUsage[corev1.ResourceName(draLogicalResource)]).To(Equal(resource.MustParse("1")))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying job is unsuspended")
			Eventually(func() bool {
				return !testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

			By("Verifying job pod is running")
			Eventually(func() bool {
				return testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

			By("Verifying exactly one ResourceClaim is allocated and reserved")
			Eventually(func(g Gomega) {
				claims, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(claims.Items).To(HaveLen(1), "expected exactly 1 ResourceClaim, got %d", len(claims.Items))
				g.Expect(claims.Items[0].Status.Allocation).NotTo(BeNil(), "ResourceClaim not allocated")
				g.Expect(claims.Items[0].Status.ReservedFor).NotTo(BeEmpty(), "ResourceClaim not reserved")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
		})
	})

	When("different workload types use DRA resources", func() {
		It("should admit DRA workloads for Pod type", func(ctx context.Context) {
			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cq, cleanupCQ, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithFlavorName(resourceFlavor.Name).
				WithDRAResource(draLogicalResource, "1").
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: draTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lq := testutils.NewLocalQueue(ns.Name, draLocalQueueName).WithClusterQueue(cq.Name)
			_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Creating ResourceClaimTemplate requesting 1 GPU")
			rct := newDRAResourceClaimTemplate("gpu-template-workload-types", ns.Name, 1)
			_, err = kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			builder := testutils.NewTestResourceBuilder(ns.Name, draLocalQueueName)

			By("Creating a Kueue-managed Pod with DRA ResourceClaim")
			pod := builder.NewPod()
			addDRAClaims(&pod.Spec, "gpu-template-workload-types")
			createdPod, err := kubeClient.CoreV1().Pods(ns.Name).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			verifyWorkloadCreated(kueueClient, ns.Name, string(createdPod.UID))

			By("Verifying Pod is running")
			Eventually(func(g Gomega) {
				p, err := kubeClient.CoreV1().Pods(ns.Name).Get(ctx, createdPod.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(p.Status.Phase).To(Equal(corev1.PodRunning), "Pod not running")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying ResourceClaim is allocated and reserved for Pod")
			verifyResourceClaimAllocated(ctx, kubeClient, ns.Name)
		})

		It("should admit DRA workloads for Deployment type", func(ctx context.Context) {
			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cq, cleanupCQ, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithFlavorName(resourceFlavor.Name).
				WithDRAResource(draLogicalResource, "1").
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: draTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lq := testutils.NewLocalQueue(ns.Name, draLocalQueueName).WithClusterQueue(cq.Name)
			_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Creating ResourceClaimTemplate requesting 1 GPU")
			rct := newDRAResourceClaimTemplate("gpu-template-workload-types", ns.Name, 1)
			_, err = kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			builder := testutils.NewTestResourceBuilder(ns.Name, draLocalQueueName)

			By("Creating a Kueue-managed Deployment with DRA ResourceClaim")
			deploy := builder.NewDeployment()
			addDRAClaims(&deploy.Spec.Template.Spec, "gpu-template-workload-types")
			createdDeploy, err := kubeClient.AppsV1().Deployments(ns.Name).Create(ctx, deploy, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for Deployment pod to be created and workload to be admitted")
			waitForPodWorkloadAdmitted(ctx, kubeClient, kueueClient, ns.Name, "app=test-deployment")

			By("Verifying Deployment is available")
			Eventually(func(g Gomega) {
				d, err := kubeClient.AppsV1().Deployments(ns.Name).Get(ctx, createdDeploy.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(d.Status.AvailableReplicas).To(Equal(int32(1)), "Deployment not available")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying ResourceClaim is allocated and reserved for Deployment")
			verifyResourceClaimAllocated(ctx, kubeClient, ns.Name)
		})

		It("should admit DRA workloads for StatefulSet type", func(ctx context.Context) {
			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cq, cleanupCQ, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithFlavorName(resourceFlavor.Name).
				WithDRAResource(draLogicalResource, "1").
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: draTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lq := testutils.NewLocalQueue(ns.Name, draLocalQueueName).WithClusterQueue(cq.Name)
			_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Creating ResourceClaimTemplate requesting 1 GPU")
			rct := newDRAResourceClaimTemplate("gpu-template-workload-types", ns.Name, 1)
			_, err = kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			builder := testutils.NewTestResourceBuilder(ns.Name, draLocalQueueName)

			By("Creating a Kueue-managed StatefulSet with DRA ResourceClaim")
			ss := builder.NewStatefulSet()
			addDRAClaims(&ss.Spec.Template.Spec, "gpu-template-workload-types")
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

			By("Verifying ResourceClaim is allocated and reserved for StatefulSet")
			verifyResourceClaimAllocated(ctx, kubeClient, ns.Name)
		})
	})

	When("deviceClassMappings are misconfigured", func() {
		It("should not admit DRA workloads when deviceClassMappings point to wrong DeviceClass", func(ctx context.Context) {
			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cq, cleanupCQ, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithFlavorName(resourceFlavor.Name).
				WithDRAResource(draLogicalResource, "1").
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: draTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lq := testutils.NewLocalQueue(ns.Name, draLocalQueueName).WithClusterQueue(cq.Name)
			_, cleanupLQ, err := lq.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Saving current Kueue config before modifying deviceClassMappings")
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			savedConfig := kueueInstance.Spec.Config.DeepCopy()
			DeferCleanup(func(cleanupCtx context.Context) {
				restoreKueueConfig(cleanupCtx, *savedConfig, kubeClient)
			})

			By("Setting wrong DeviceClass name in deviceClassMappings")
			kueueInstance.Spec.Config.Resources.DeviceClassMappings = []ssv1.DeviceClassMapping{
				{
					Name:             draLogicalResource,
					DeviceClassNames: []ssv1.DeviceClassName{"nonexistent.nvidia.com"},
				},
			}
			applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)

			By("Verifying ConfigMap has wrong DeviceClass name")
			Eventually(func(g Gomega) {
				configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				configData := configMap.Data["controller_manager_config.yaml"]
				g.Expect(configData).To(ContainSubstring("nonexistent.nvidia.com"), "wrong DeviceClass not configured yet")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Creating ResourceClaimTemplate for DRA job")
			rct := newDRAResourceClaimTemplate("gpu-template-wrong-class", ns.Name, 1)
			_, err = kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating DRA job with wrong DeviceClass mapping")
			wrongClassStart := metav1.Now()
			builder := testutils.NewTestResourceBuilder(ns.Name, draLocalQueueName)
			job := newDRAJob(builder, "dra-wrong-class", "gpu-template-wrong-class", draLocalQueueName)
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob.Namespace, createdJob.Name)

			By("Verifying job is suspended due to wrong DeviceClass mapping")
			Eventually(func() bool {
				return testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

			By("Verifying No ResourceClaim is created since the job was never unsuspended")
			verifyNoResourceClaims(ctx, kubeClient, ns.Name, wrongClassStart)
		})
	})

	When("preemption is enabled", func() {
		It("should preempt low-priority DRA workload when high-priority DRA workload is submitted", func(ctx context.Context) {
			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: draTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			By("Creating PriorityClasses")
			lowPC, cleanupLowPC, err := createPriorityClass(ctx, 100, "Low priority for DRA preemption")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLowPC)
			highPC, cleanupHighPC, err := createPriorityClass(ctx, 1000, "High priority for DRA preemption")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupHighPC)

			By("Creating ClusterQueue with preemption enabled and DRA quota")
			preemptCQ, cleanupCQ, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithFlavorName(resourceFlavor.Name).
				WithPreemption(kueuev1beta2.PreemptionPolicyLowerPriority).
				WithDRAResource(draLogicalResource, "1").
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			By("Creating LocalQueue for preemption test")
			preemptLQ, cleanupLQ, err := testutils.NewLocalQueue(ns.Name, "").WithGenerateName().WithClusterQueue(preemptCQ.Name).CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Creating ResourceClaimTemplate")
			rct := newDRAResourceClaimTemplate("gpu-template-preempt", ns.Name, 1)
			_, err = kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Submitting low-priority DRA job that fills the 1-GPU quota")
			builder := testutils.NewTestResourceBuilder(ns.Name, preemptLQ.Name)
			lowJob := newDRAJob(builder, "dra-low-prio", "gpu-template-preempt", preemptLQ.Name)
			lowJob.Spec.Template.Spec.PriorityClassName = lowPC.Name
			createdLowJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, lowJob, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdLowJob.Namespace, createdLowJob.Name)

			By("Verifying low-priority job is admitted")
			checkWorkloadCondition(ctx, ns.Name, string(createdLowJob.UID), kueuev1beta2.WorkloadAdmitted, "low-priority")

			By("Verifying low-priority job pod is running")
			Eventually(func() bool {
				return testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdLowJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

			By("Submitting high-priority DRA job that triggers preemption")
			highJob := newDRAJob(builder, "dra-high-prio", "gpu-template-preempt", preemptLQ.Name)
			highJob.Spec.Template.Spec.PriorityClassName = highPC.Name
			createdHighJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, highJob, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdHighJob.Namespace, createdHighJob.Name)

			By("Verifying low-priority job is evicted")
			checkWorkloadCondition(ctx, ns.Name, string(createdLowJob.UID), kueuev1beta2.WorkloadEvicted, "low-priority")

			By("Verifying high-priority job is admitted")
			checkWorkloadCondition(ctx, ns.Name, string(createdHighJob.UID), kueuev1beta2.WorkloadAdmitted, "high-priority")

			By("Waiting for high-priority job to complete")
			checkWorkloadCondition(ctx, ns.Name, string(createdHighJob.UID), kueuev1beta2.WorkloadFinished, "high-priority")

			By("Verifying low-priority job is re-admitted after high-priority completes")
			checkWorkloadCondition(ctx, ns.Name, string(createdLowJob.UID), kueuev1beta2.WorkloadAdmitted, "low-priority")
		})
	})

	When("gang scheduling is enabled", func() {
		It("should enforce all-or-nothing admission for gang DRA workloads", func(ctx context.Context) {
			if gpuCount < 2 {
				Skip(fmt.Sprintf("gang scheduling test requires at least 2 GPUs, found %d", gpuCount))
			}

			By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")
			kueueClient := clients.UpstreamKueueClient

			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: draTestNamespacePrefix,
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

			kueueInstance.Spec.Config.GangScheduling = ssv1.GangScheduling{
				Policy: ssv1.GangSchedulingPolicyByWorkload,
				ByWorkload: &ssv1.ByWorkload{
					Admission: ssv1.GangSchedulingWorkloadAdmissionSequential,
				},
			}
			applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)

			By("Creating ClusterQueue with 2 GPU DRA quota")
			gangCQ, cleanupCQ, err := testutils.NewClusterQueue().
				WithGenerateName().
				WithFlavorName(resourceFlavor.Name).
				WithDRAResource(draLogicalResource, "2").
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			By("Creating LocalQueue for gang scheduling test")
			gangLQ, cleanupLQ, err := testutils.NewLocalQueue(ns.Name, "").WithGenerateName().WithClusterQueue(gangCQ.Name).CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQ)

			By("Creating ResourceClaimTemplate")
			rct := newDRAResourceClaimTemplate("gpu-template-gang", ns.Name, 1)
			_, err = kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Submitting single-pod DRA job that fills 1 of 2 GPUs")
			builder := testutils.NewTestResourceBuilder(ns.Name, gangLQ.Name)
			singleJob := newDRAJob(builder, "dra-single-gpu", "gpu-template-gang", gangLQ.Name)
			createdSingleJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, singleJob, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdSingleJob.Namespace, createdSingleJob.Name)

			By("Verifying single-pod job is admitted and running")
			checkWorkloadCondition(ctx, ns.Name, string(createdSingleJob.UID), kueuev1beta2.WorkloadAdmitted, "single-gpu")
			Eventually(func() bool {
				return testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdSingleJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

			By("Submitting gang DRA job (parallelism=2, needs 2 GPUs, only 1 free)")
			gangJob := newDRAJob(builder, "dra-gang-job", "gpu-template-gang", gangLQ.Name)
			gangJob.Spec.Parallelism = ptr.To(int32(2))
			gangJob.Spec.Completions = ptr.To(int32(2))
			createdGangJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, gangJob, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdGangJob.Namespace, createdGangJob.Name)

			By("Verifying gang job is suspended (not enough DRA quota for all pods)")
			Eventually(func() bool {
				return testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdGangJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

			By("Deleting single-pod job to free its GPU")
			testutils.CleanUpJob(ctx, kubeClient, createdSingleJob.Namespace, createdSingleJob.Name)

			By("Verifying gang job is admitted after quota is freed")
			checkWorkloadCondition(ctx, ns.Name, string(createdGangJob.UID), kueuev1beta2.WorkloadAdmitted, "gang")

			By("Verifying gang job is unsuspended")
			Eventually(func() bool {
				return !testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdGangJob.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

			By("Waiting for gang job to complete")
			checkWorkloadCondition(ctx, ns.Name, string(createdGangJob.UID), kueuev1beta2.WorkloadFinished, "gang")
		})
	})

	When("admission fair sharing tracks DRA resources", func() {
		var savedAFSConfig *ssv1.KueueConfiguration

		BeforeAll(func(ctx context.Context) {
			By("Enabling AFS with custom settings for all AFS tests")
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			savedAFSConfig = kueueInstance.Spec.Config.DeepCopy()
			kueueInstance.Spec.Config.AdmissionFairSharing = ssv1.AdmissionFairSharing{
				UsageHalfLifeTimeSeconds:     120,
				UsageSamplingIntervalSeconds: 1,
			}
			applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)
		})

		AfterAll(func(ctx context.Context) {
			By("Restoring Kueue config after AFS tests")
			if savedAFSConfig != nil {
				restoreKueueConfig(ctx, *savedAFSConfig, kubeClient)
			}
		})

		It("should include DRA GPU in consumedResources for the GPU-using LocalQueue", func(ctx context.Context) {
			if gpuCount < 2 {
				Skip(fmt.Sprintf("AFS test requires at least 2 GPUs, found %d", gpuCount))
			}
			kueueClient := clients.UpstreamKueueClient

			By("Creating ResourceFlavor, ClusterQueue with AFS, Namespace and two LocalQueues")
			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cqWrapper := testutils.NewClusterQueue().
				WithGenerateName().
				WithFlavorName(resourceFlavor.Name).
				WithCPU("1").
				WithMemory("4Gi").
				WithDRAResource(draLogicalResource, "2")
			cqWrapper.Spec.AdmissionScope = &kueuev1beta2.AdmissionScope{
				AdmissionMode: kueuev1beta2.UsageBasedAdmissionFairSharing,
			}
			cq, cleanupCQ, err := cqWrapper.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: draTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lqGPU, cleanupLQGPU, err := testutils.NewLocalQueue(ns.Name, "lq-gpu").WithClusterQueue(cq.Name).CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQGPU)

			lqCPU, cleanupLQCPU, err := testutils.NewLocalQueue(ns.Name, "lq-cpu").WithClusterQueue(cq.Name).CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQCPU)

			By("Creating ResourceClaimTemplate for 1 GPU")
			rct := newDRAResourceClaimTemplate("gpu-template-afs", ns.Name, 1)
			_, err = kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Submitting GPU+CPU DRA jobs to lq-gpu")
			builder := testutils.NewTestResourceBuilder(ns.Name, lqGPU.Name)
			jobGPU1 := newDRAJob(builder, "job-gpu-afs-1", "gpu-template-afs", lqGPU.Name)
			createdJobGPU1, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, jobGPU1, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdJobGPU1.Namespace, createdJobGPU1.Name)

			jobGPU2 := newDRAJob(builder, "job-gpu-afs-2", "gpu-template-afs", lqGPU.Name)
			createdJobGPU2, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, jobGPU2, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdJobGPU2.Namespace, createdJobGPU2.Name)

			By("Submitting CPU-only job to lq-cpu")
			cpuBuilder := testutils.NewTestResourceBuilder(ns.Name, lqCPU.Name)
			jobCPU := cpuBuilder.NewJob()
			setDRAJobCPU(jobCPU)
			jobCPU.Name = "job-cpu-afs"
			jobCPU.Labels[testutils.QueueLabel] = lqCPU.Name
			jobCPU.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "echo Hello Kueue; sleep 10"}
			createdJobCPU, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, jobCPU, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdJobCPU.Namespace, createdJobCPU.Name)

			By("Waiting for all jobs to be running")
			for _, jobName := range []string{createdJobGPU1.Name, createdJobGPU2.Name, createdJobCPU.Name} {
				Eventually(func() bool {
					return testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, jobName)
				}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(),
					"job %s pod should be running", jobName)
			}

			By("Verifying ResourceClaims are allocated for GPU jobs")
			Eventually(func(g Gomega) {
				claims, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				var allocatedCount int
				for _, c := range claims.Items {
					if c.Status.Allocation != nil && len(c.Status.ReservedFor) > 0 {
						allocatedCount++
					}
				}
				g.Expect(allocatedCount).To(Equal(2), "expected 2 allocated ResourceClaims, got %d", allocatedCount)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying lq-gpu consumedResources includes non-zero nvidia-gpu")
			Eventually(func(g Gomega) {
				lq, err := kueueClient.KueueV1beta2().LocalQueues(ns.Name).Get(ctx, lqGPU.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(lq.Status.FairSharing).NotTo(BeNil(), "lq-gpu fairSharing status should be populated")
				g.Expect(lq.Status.FairSharing.AdmissionFairSharingStatus).NotTo(BeNil(),
					"lq-gpu admissionFairSharingStatus should be populated")
				consumed := lq.Status.FairSharing.AdmissionFairSharingStatus.ConsumedResources
				gpuUsage := consumed[corev1.ResourceName(draLogicalResource)]
				g.Expect(gpuUsage.IsZero()).To(BeFalse(),
					"lq-gpu consumedResources should include non-zero %s, got %s", draLogicalResource, gpuUsage.String())
			}, 60*time.Second, 2*time.Second).Should(Succeed())

			By("Verifying lq-cpu consumedResources has zero nvidia-gpu usage")
			Eventually(func(g Gomega) {
				lq, err := kueueClient.KueueV1beta2().LocalQueues(ns.Name).Get(ctx, lqCPU.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(lq.Status.FairSharing).NotTo(BeNil(), "lq-cpu fairSharing status should be populated")
				g.Expect(lq.Status.FairSharing.AdmissionFairSharingStatus).NotTo(BeNil(),
					"lq-cpu admissionFairSharingStatus should be populated")
				consumed := lq.Status.FairSharing.AdmissionFairSharingStatus.ConsumedResources
				gpuUsage := consumed[corev1.ResourceName(draLogicalResource)]
				g.Expect(gpuUsage.IsZero()).To(BeTrue(),
					"lq-cpu consumedResources should have zero %s usage, got %s", draLogicalResource, gpuUsage.String())
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should deprioritize GPU-heavy queue in admission ordering", func(ctx context.Context) {
			if gpuCount < 2 {
				Skip(fmt.Sprintf("AFS ordering test requires at least 2 GPUs, found %d", gpuCount))
			}
			kueueClient := clients.UpstreamKueueClient

			By("Creating ResourceFlavor, ClusterQueue with AFS, Namespace and two LocalQueues")
			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupResourceFlavor)

			cqWrapper := testutils.NewClusterQueue().
				WithGenerateName().
				WithFlavorName(resourceFlavor.Name).
				WithCPU("1").
				WithMemory("4Gi").
				WithDRAResource(draLogicalResource, "2")
			cqWrapper.Spec.AdmissionScope = &kueuev1beta2.AdmissionScope{
				AdmissionMode: kueuev1beta2.UsageBasedAdmissionFairSharing,
			}
			cq, cleanupCQ, err := cqWrapper.CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupCQ)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: draTestNamespacePrefix,
					Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
				},
			}
			cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNs)

			lqGPU, cleanupLQGPU, err := testutils.NewLocalQueue(ns.Name, "lq-gpu").WithClusterQueue(cq.Name).CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQGPU)

			lqCPU, cleanupLQCPU, err := testutils.NewLocalQueue(ns.Name, "lq-cpu").WithClusterQueue(cq.Name).CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupLQCPU)

			By("Creating ResourceClaimTemplate for 1 GPU")
			rct := newDRAResourceClaimTemplate("gpu-template-afs-order", ns.Name, 1)
			_, err = kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Submitting GPU+CPU DRA jobs to lq-gpu to build usage history")
			builder := testutils.NewTestResourceBuilder(ns.Name, lqGPU.Name)
			jobGPU1 := newDRAJob(builder, "job-gpu-order-1", "gpu-template-afs-order", lqGPU.Name)
			createdJobGPU1, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, jobGPU1, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdJobGPU1.Namespace, createdJobGPU1.Name)

			jobGPU2 := newDRAJob(builder, "job-gpu-order-2", "gpu-template-afs-order", lqGPU.Name)
			createdJobGPU2, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, jobGPU2, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdJobGPU2.Namespace, createdJobGPU2.Name)

			By("Submitting CPU-only job to lq-cpu to build usage history")
			cpuBuilder := testutils.NewTestResourceBuilder(ns.Name, lqCPU.Name)
			jobCPU := cpuBuilder.NewJob()
			setDRAJobCPU(jobCPU)
			jobCPU.Name = "job-cpu-order"
			jobCPU.Labels[testutils.QueueLabel] = lqCPU.Name
			jobCPU.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "echo Hello Kueue; sleep 10"}
			createdJobCPU, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, jobCPU, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdJobCPU.Namespace, createdJobCPU.Name)

			By("Waiting for all jobs to be running")
			for _, jobName := range []string{createdJobGPU1.Name, createdJobGPU2.Name, createdJobCPU.Name} {
				Eventually(func() bool {
					return testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, jobName)
				}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(),
					"job %s pod should be running", jobName)
			}

			By("Verifying ResourceClaims are allocated for GPU jobs")
			Eventually(func(g Gomega) {
				claims, err := kubeClient.ResourceV1().ResourceClaims(ns.Name).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				var allocatedCount int
				for _, c := range claims.Items {
					if c.Status.Allocation != nil && len(c.Status.ReservedFor) > 0 {
						allocatedCount++
					}
				}
				g.Expect(allocatedCount).To(Equal(2), "expected 2 allocated ResourceClaims, got %d", allocatedCount)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Waiting for all initial jobs to complete")
			for _, uid := range []string{string(createdJobGPU1.UID), string(createdJobGPU2.UID), string(createdJobCPU.UID)} {
				checkWorkloadCondition(ctx, ns.Name, uid, kueuev1beta2.WorkloadFinished, "initial")
			}

			By("Verifying lq-gpu usage still persists after completion (half-life decay)")
			Eventually(func(g Gomega) {
				lq, err := kueueClient.KueueV1beta2().LocalQueues(ns.Name).Get(ctx, lqGPU.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(lq.Status.FairSharing).NotTo(BeNil(), "lq-gpu fairSharing status should be populated")
				g.Expect(lq.Status.FairSharing.AdmissionFairSharingStatus).NotTo(BeNil(),
					"lq-gpu admissionFairSharingStatus should be populated")
				consumed := lq.Status.FairSharing.AdmissionFairSharingStatus.ConsumedResources
				gpuUsage := consumed[corev1.ResourceName(draLogicalResource)]
				g.Expect(gpuUsage.IsZero()).To(BeFalse(),
					"lq-gpu consumedResources should still include non-zero %s after completion, got %s",
					draLogicalResource, gpuUsage.String())
			}, 30*time.Second, 2*time.Second).Should(Succeed())

			By("Creating placeholder job on lq-gpu to fill CPU quota")
			placeholderBuilder := testutils.NewTestResourceBuilder(ns.Name, lqGPU.Name)
			jobPlaceholder := placeholderBuilder.NewJob()
			jobPlaceholder.Name = "job-placeholder"
			jobPlaceholder.Labels[testutils.QueueLabel] = lqGPU.Name
			jobPlaceholder.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "sleep infinity"}
			jobPlaceholder.Spec.Template.Spec.Containers[0].Resources.Requests = corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			}
			jobPlaceholder.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			}
			createdPlaceholder, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, jobPlaceholder, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdPlaceholder.Namespace, createdPlaceholder.Name)

			Eventually(func() bool {
				return !testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdPlaceholder.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(),
				"placeholder job should be admitted")

			By("Creating both contender jobs while CPU quota is full (both should stay suspended)")
			gpuCompeteBuilder := testutils.NewTestResourceBuilder(ns.Name, lqGPU.Name)
			jobGPUCompete := gpuCompeteBuilder.NewJob()
			jobGPUCompete.Name = "job-gpu-compete"
			jobGPUCompete.Labels[testutils.QueueLabel] = lqGPU.Name
			jobGPUCompete.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "sleep infinity"}
			jobGPUCompete.Spec.Template.Spec.Containers[0].Resources.Requests = corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			}
			jobGPUCompete.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			}
			createdJobGPUCompete, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, jobGPUCompete, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdJobGPUCompete.Namespace, createdJobGPUCompete.Name)

			jobCPUCompete := cpuBuilder.NewJob()
			jobCPUCompete.Name = "job-cpu-compete"
			jobCPUCompete.Labels[testutils.QueueLabel] = lqCPU.Name
			jobCPUCompete.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "sleep infinity"}
			jobCPUCompete.Spec.Template.Spec.Containers[0].Resources.Requests = corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			}
			jobCPUCompete.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			}
			createdJobCPUCompete, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, jobCPUCompete, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(testutils.CleanUpJob, kubeClient, createdJobCPUCompete.Namespace, createdJobCPUCompete.Name)

			By("Verifying both contender jobs are suspended")
			Consistently(func() bool {
				return testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJobCPUCompete.Name) &&
					testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJobGPUCompete.Name)
			}, 10*time.Second, 2*time.Second).Should(BeTrue(),
				"both contender jobs should remain suspended while placeholder holds CPU quota")

			By("Deleting placeholder job to free CPU quota for one contender")
			foreground := metav1.DeletePropagationForeground
			err = kubeClient.BatchV1().Jobs(ns.Name).Delete(ctx, createdPlaceholder.Name, metav1.DeleteOptions{
				PropagationPolicy: &foreground,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying lq-cpu job wins admission (lower AFS usage)")
			Eventually(func() bool {
				return !testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJobCPUCompete.Name)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(),
				"job-cpu-compete (lq-cpu, lower usage) should be admitted first")

			By("Verifying lq-gpu job remains suspended (higher AFS usage due to GPU history)")
			Consistently(func() bool {
				return testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJobGPUCompete.Name)
			}, 15*time.Second, 2*time.Second).Should(BeTrue(),
				"job-gpu-compete (lq-gpu, higher total usage including GPUs) should remain suspended")
		})
	})

})

// newDRAResourceClaimTemplate creates a ResourceClaimTemplate that requests the given number of GPUs
// from the gpu.nvidia.com DeviceClass.
func newDRAResourceClaimTemplate(name, namespace string, gpuCount int64) *resourcev1.ResourceClaimTemplate {
	return &resourcev1.ResourceClaimTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: resourcev1.ResourceClaimTemplateSpec{
			Spec: resourcev1.ResourceClaimSpec{
				Devices: resourcev1.DeviceClaim{
					Requests: []resourcev1.DeviceRequest{
						{
							Name: "gpu-req",
							Exactly: &resourcev1.ExactDeviceRequest{
								DeviceClassName: draDeviceClassName,
								Count:           gpuCount,
							},
						},
					},
				},
			},
		},
	}
}

// waitForPodWorkloadAdmitted waits until a pod matching the label selector has
// a corresponding admitted Kueue workload, correlating via OwnerReferences.
func waitForPodWorkloadAdmitted(ctx context.Context, kubeClient *kubernetes.Clientset, kueueClient *upstreamkueueclient.Clientset, namespace, labelSelector string) {
	Eventually(func() error {
		pods, err := kubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return err
		}
		if len(pods.Items) == 0 {
			return fmt.Errorf("no pods found with label %s", labelSelector)
		}
		ownerUIDs := make(map[types.UID]bool)
		for _, pod := range pods.Items {
			for _, ref := range pod.OwnerReferences {
				ownerUIDs[ref.UID] = true
			}
			ownerUIDs[pod.UID] = true
		}
		workloads, err := kueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		for _, wl := range workloads.Items {
			if wl.Status.Admission == nil {
				continue
			}
			for _, ref := range wl.OwnerReferences {
				if ownerUIDs[ref.UID] {
					return nil
				}
			}
		}
		return fmt.Errorf("no admitted workload correlated to pods with label %s", labelSelector)
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}

// verifyResourceClaimAllocated verifies that at least one ResourceClaim
// in the namespace is in the "allocated,reserved" state.
func verifyResourceClaimAllocated(ctx context.Context, kubeClient *kubernetes.Clientset, namespace string) {
	Eventually(func(g Gomega) {
		claims, err := kubeClient.ResourceV1().ResourceClaims(namespace).List(ctx, metav1.ListOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(claims.Items).NotTo(BeEmpty(), "no ResourceClaims found in namespace %s", namespace)
		claim := claims.Items[0]
		g.Expect(claim.Status.Allocation).NotTo(BeNil(), "ResourceClaim not allocated in namespace %s", namespace)
		g.Expect(claim.Status.ReservedFor).NotTo(BeEmpty(), "ResourceClaim not reserved in namespace %s", namespace)
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}

// verifyNoResourceClaims verifies that no ResourceClaims created after the given
// timestamp exist in the namespace.
func verifyNoResourceClaims(ctx context.Context, kubeClient *kubernetes.Clientset, namespace string, createdAfter metav1.Time) {
	Consistently(func(g Gomega) {
		claims, err := kubeClient.ResourceV1().ResourceClaims(namespace).List(ctx, metav1.ListOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		var filtered []resourcev1.ResourceClaim
		for _, c := range claims.Items {
			if c.CreationTimestamp.After(createdAfter.Time) {
				filtered = append(filtered, c)
			}
		}
		g.Expect(filtered).To(BeEmpty(), "expected no ResourceClaims after %v in namespace %s", createdAfter.Time, namespace)
	}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(Succeed())
}

// newDRAJob creates a Job with DRA ResourceClaim configuration for the given template.
func newDRAJob(builder *testutils.TestResourceBuilder, name, templateName, queueName string) *batchv1.Job {
	job := builder.NewJob()
	setDRAJobCPU(job)
	job.Name = name
	job.Labels[testutils.QueueLabel] = queueName
	job.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "echo Hello Kueue; sleep 10"}
	job.Spec.Template.Spec.ResourceClaims = []corev1.PodResourceClaim{
		{Name: "gpu", ResourceClaimTemplateName: ptr.To(templateName)},
	}
	job.Spec.Template.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{{Name: "gpu"}}
	return job
}

// restoreKueueConfig restores a Kueue CR config with retry on resourceVersion conflicts.
// This function re-fetches the CR on each attempt to get a fresh resourceVersion.
func restoreKueueConfig(ctx context.Context, config ssv1.KueueConfiguration, kClient *kubernetes.Clientset) {
	kueueClientset := clients.KueueClient

	initialDeployment, err := kClient.AppsV1().Deployments(testutils.OperatorNamespace).Get(ctx, "kueue-controller-manager", metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "Failed to fetch kueue-controller-manager deployment before restore")
	initialResourceVersion := initialDeployment.ResourceVersion

	By("Restoring Kueue config (with conflict retry)")
	Eventually(func() error {
		instance, err := kueueClientset.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to fetch Kueue instance: %w", err)
		}
		instance.Spec.Config = config
		_, err = kueueClientset.KueueV1().Kueues().Update(ctx, instance, metav1.UpdateOptions{})
		return err
	}, 30*time.Second, 2*time.Second).Should(Succeed(), "Failed to restore Kueue config")

	By("Waiting for kueue-controller-manager deployment to be updated after config restore")
	Eventually(func() error {
		dep, err := kClient.AppsV1().Deployments(testutils.OperatorNamespace).Get(ctx, "kueue-controller-manager", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("unable to fetch manager deployment: %w", err)
		}
		if dep.ResourceVersion == initialResourceVersion {
			return fmt.Errorf("deployment resource version has not changed yet (still %s)", initialResourceVersion)
		}
		return nil
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "kueue-controller-manager deployment resource version did not change after restore")

	By("Waiting for kueue-controller-manager to be ready after config restore")
	Eventually(func() error {
		dep, err := kClient.AppsV1().Deployments(testutils.OperatorNamespace).Get(ctx, "kueue-controller-manager", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("unable to fetch manager deployment: %w", err)
		}
		if dep.Status.ReadyReplicas != dep.Status.Replicas {
			return fmt.Errorf("deployment not ready: %d/%d replicas ready", dep.Status.ReadyReplicas, dep.Status.Replicas)
		}
		return nil
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "kueue-controller-manager not ready after config restore")
}

// adds DRA ResourceClaim references and reduces CPU request on a PodSpec.
func addDRAClaims(spec *corev1.PodSpec, templateName string) {
	spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("100m")
	spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{{Name: "gpu-claim"}}
	spec.ResourceClaims = []corev1.PodResourceClaim{
		{Name: "gpu-claim", ResourceClaimTemplateName: ptr.To(templateName)},
	}
}
