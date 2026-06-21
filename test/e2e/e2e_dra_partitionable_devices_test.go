/*
Copyright 2026.

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
	configv1 "github.com/openshift/api/config/v1"
	configclientv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	ssv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
)

const (
	pdDriverName          = "gpu.nvidia.com"
	pdMigDeviceClass      = "mig.nvidia.com"
	pdGpuDeviceClass      = "gpu.nvidia.com"
	pdLogicalResource     = "gpu.memory"
	pdTestNamespacePrefix = "kueue-pd-test-"
	pdLocalQueueName      = "pd-test-queue"
)

func verifyWorkloadGPUMemory(ctx context.Context, namespace, jobUID string, expectedBytes int64) {
	kueueClient := clients.UpstreamKueueClient
	Eventually(func(g Gomega) {
		wlList, err := kueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", jobUID),
		})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(wlList.Items).NotTo(BeEmpty())

		wl := wlList.Items[0]
		g.Expect(wl.Status.Admission).NotTo(BeNil())
		g.Expect(wl.Status.Admission.PodSetAssignments).To(HaveLen(1))

		assignment := wl.Status.Admission.PodSetAssignments[0]
		g.Expect(assignment.ResourceUsage).To(HaveKey(corev1.ResourceName(pdLogicalResource)))
		memUsage := assignment.ResourceUsage[corev1.ResourceName(pdLogicalResource)]
		g.Expect(memUsage.Value()).To(Equal(expectedBytes), "expected gpu.memory=%d, got=%d", expectedBytes, memUsage.Value())
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}

func verifyWorkloadInadmissible(ctx context.Context, namespace, jobUID string) {
	kueueClient := clients.UpstreamKueueClient
	Eventually(func(g Gomega) {
		wlList, err := kueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", jobUID),
		})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(wlList.Items).NotTo(BeEmpty())

		wl := wlList.Items[0]
		for _, c := range wl.Status.Conditions {
			if c.Type == string(kueuev1beta2.WorkloadQuotaReserved) {
				g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(c.Reason).To(Equal("Inadmissible"))
				return
			}
		}
		g.Expect(false).To(BeTrue(), "QuotaReserved condition not found")
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}

func verifyCQGPUMemory(ctx context.Context, cqName string, expectedBytes int64) {
	kueueClient := clients.UpstreamKueueClient
	Eventually(func(g Gomega) {
		cq, err := kueueClient.KueueV1beta2().ClusterQueues().Get(ctx, cqName, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		for _, flavorUsage := range cq.Status.FlavorsReservation {
			for _, res := range flavorUsage.Resources {
				if res.Name == corev1.ResourceName(pdLogicalResource) {
					g.Expect(res.Total.Value()).To(Equal(expectedBytes))
					return
				}
			}
		}
		g.Expect(false).To(BeTrue(), "gpu.memory not found in CQ status")
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}

var _ = Describe("DRA Partitionable Devices", Label("operator", "dra-pd"), Ordered, func() {
	var (
		initialKueueInstance *ssv1.Kueue
		migMemoryBytes       int64  // 1g.10gb memory in bytes
		migProfile           string // "1g.10gb"
		secondMIGMemoryBytes int64  // 3g.20gb or 3g.40gb memory in bytes
		secondMIGProfile     string // "3g.20gb" or "3g.40gb"
		migDeviceCount       int
		secondMIGDeviceCount int
		wholeGPUMemoryBytes  int64
		hasWholeGPUDevice    bool
	)

	JustAfterEach(func(ctx context.Context) {
		testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 500)
	})

	BeforeAll(func(ctx context.Context) {
		if !testutils.IsDRASupported(kubeClient) {
			Skip("DRA APIs (resource.k8s.io/v1) not available on this cluster")
		}

		configClient, err := configclientv1.NewForConfig(clients.RestConfig)
		Expect(err).NotTo(HaveOccurred())
		fg, err := configClient.FeatureGates().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		pdGateEnabled := false
		if fg.Spec.FeatureSet == configv1.CustomNoUpgrade && fg.Spec.CustomNoUpgrade != nil {
			for _, gate := range fg.Spec.CustomNoUpgrade.Enabled {
				if string(gate) == "DRAPartitionableDevices" {
					pdGateEnabled = true
					break
				}
			}
		}
		if !pdGateEnabled {
			Skip("DRAPartitionableDevices K8s feature gate not enabled via CustomNoUpgrade")
		}

		slices, err := kubeClient.ResourceV1().ResourceSlices().List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		for _, s := range slices.Items {
			if s.Spec.Driver != pdDriverName {
				continue
			}
			for _, sc := range s.Spec.SharedCounters {
				if m, ok := sc.Counters["memory"]; ok {
					wholeGPUMemoryBytes = m.Value.Value()
				}
			}
			for _, d := range s.Spec.Devices {
				if typeAttr, ok := d.Attributes["type"]; ok && typeAttr.StringValue != nil && *typeAttr.StringValue == "gpu" {
					hasWholeGPUDevice = true
				}
				profileAttr, hasProfile := d.Attributes["profile"]
				mem := int64(0)
				if len(d.ConsumesCounters) > 0 {
					if m, ok := d.ConsumesCounters[0].Counters["memory"]; ok {
						mem = m.Value.Value()
					}
				}
				if hasProfile && profileAttr.StringValue != nil && mem > 0 {
					switch *profileAttr.StringValue {
					case "1g.10gb":
						migMemoryBytes = mem
						migProfile = "1g.10gb"
						migDeviceCount++
					case "3g.20gb":
						secondMIGMemoryBytes = mem
						secondMIGProfile = "3g.20gb"
						secondMIGDeviceCount++
					case "3g.40gb":
						if secondMIGProfile == "" {
							secondMIGMemoryBytes = mem
							secondMIGProfile = "3g.40gb"
						}
						if secondMIGProfile == "3g.40gb" {
							secondMIGDeviceCount++
						}
					}
				}
			}
		}
		if migMemoryBytes == 0 {
			Skip("No MIG 1g.10gb devices found in ResourceSlices")
		}

		kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		initialKueueInstance = kueueInstance.DeepCopy()

		kueueInstance.Spec.Config.Resources.DeviceClassMappings = []ssv1.DeviceClassMapping{
			{
				Name:             pdLogicalResource,
				DeviceClassNames: []ssv1.DeviceClassName{pdGpuDeviceClass, pdMigDeviceClass},
				Sources: []ssv1.DeviceClassSourceConfig{
					{
						Type: ssv1.DeviceClassSourceTypeCounter,
						Counter: ssv1.DeviceClassCounterSource{
							Name:   "memory",
							Driver: pdDriverName,
							DeviceSelector: ssv1.DeviceSelector{
								Type: ssv1.DeviceSelectorTypeCEL,
								CEL:  ssv1.CELDeviceSelector{Expression: "device.driver == 'gpu.nvidia.com'"},
							},
						},
					},
				},
			},
		}
		applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)

		Eventually(func() error {
			configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
			if err != nil {
				return err
			}
			configData := configMap.Data["controller_manager_config.yaml"]
			if !strings.Contains(configData, "KueueDRAIntegrationPartitionableDevices") {
				return fmt.Errorf("PD feature gate not configured yet")
			}
			return nil
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
	})

	AfterAll(func(ctx context.Context) {
		if initialKueueInstance != nil {
			restoreKueueConfig(ctx, initialKueueInstance.Spec.Config, kubeClient)
		}
	})

	It("should charge counter-based gpu.memory for single MIG partition", func(ctx context.Context) {
		cq, ns := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
			pdTestNamespacePrefix, pdLocalQueueName,
			func(cq *testutils.ClusterQueueWrapper) {
				cq.WithDRAResource(pdLogicalResource, "80Gi")
			})

		By("Creating ResourceClaimTemplate and Job")
		rct := testutils.NewResourceClaimTemplate("mig-single", ns.Name, pdMigDeviceClass, 1,
			fmt.Sprintf("device.attributes['gpu.nvidia.com'].profile == '%s'", migProfile))
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		builder := testutils.NewTestResourceBuilder(ns.Name, pdLocalQueueName)
		job := builder.NewDRAJob("pd-single-mig", pdLocalQueueName, "mig-single")
		createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, ns.Name, createdJob.Name)

		By("Verifying workload is charged counter-based gpu.memory")
		verifyWorkloadGPUMemory(ctx, ns.Name, string(createdJob.UID), migMemoryBytes)
		verifyCQGPUMemory(ctx, cq.Name, migMemoryBytes)
	})

	It("should multiply counter charge by request count", func(ctx context.Context) {
		_, ns := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
			pdTestNamespacePrefix, pdLocalQueueName,
			func(cq *testutils.ClusterQueueWrapper) {
				cq.WithDRAResource(pdLogicalResource, "80Gi")
			})

		By("Creating ResourceClaimTemplate requesting 2 devices and Job")
		rct := testutils.NewResourceClaimTemplate("mig-count2", ns.Name, pdMigDeviceClass, 2,
			fmt.Sprintf("device.attributes['gpu.nvidia.com'].profile == '%s'", migProfile))
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		builder := testutils.NewTestResourceBuilder(ns.Name, pdLocalQueueName)
		job := builder.NewDRAJob("pd-count2", pdLocalQueueName, "mig-count2")
		createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, ns.Name, createdJob.Name)

		By("Verifying workload is charged 2x counter memory")
		verifyWorkloadGPUMemory(ctx, ns.Name, string(createdJob.UID), migMemoryBytes*2)
	})

	It("should mark workload inadmissible when CEL matches no devices", func(ctx context.Context) {
		_, ns := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
			pdTestNamespacePrefix, pdLocalQueueName,
			func(cq *testutils.ClusterQueueWrapper) {
				cq.WithDRAResource(pdLogicalResource, "80Gi")
			})

		By("Creating ResourceClaimTemplate with nonexistent profile and Job")
		rct := testutils.NewResourceClaimTemplate("mig-nonexistent", ns.Name, pdMigDeviceClass, 1, "device.attributes['gpu.nvidia.com'].profile == '1g.7gb'")
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		builder := testutils.NewTestResourceBuilder(ns.Name, pdLocalQueueName)
		job := builder.NewDRAJob("pd-nonexistent", pdLocalQueueName, "mig-nonexistent")
		createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, ns.Name, createdJob.Name)

		By("Verifying workload is inadmissible")
		verifyWorkloadInadmissible(ctx, ns.Name, string(createdJob.UID))
	})

	It("should admit multiple workloads sharing counter quota", func(ctx context.Context) {
		cq, ns := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
			pdTestNamespacePrefix, pdLocalQueueName,
			func(cq *testutils.ClusterQueueWrapper) {
				cq.WithDRAResource(pdLogicalResource, "80Gi")
			})

		By("Creating ResourceClaimTemplate and two Jobs sharing quota")
		rct := testutils.NewResourceClaimTemplate("mig-share", ns.Name, pdMigDeviceClass, 1,
			fmt.Sprintf("device.attributes['gpu.nvidia.com'].profile == '%s'", migProfile))
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		builder := testutils.NewTestResourceBuilder(ns.Name, pdLocalQueueName)

		jobA := builder.NewDRAJob("pd-share-a", pdLocalQueueName, "mig-share")
		createdJobA, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, jobA, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, ns.Name, createdJobA.Name)

		jobB := builder.NewDRAJob("pd-share-b", pdLocalQueueName, "mig-share")
		createdJobB, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, jobB, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, ns.Name, createdJobB.Name)

		By("Verifying both workloads are admitted with counter charging")
		verifyWorkloadGPUMemory(ctx, ns.Name, string(createdJobA.UID), migMemoryBytes)
		verifyWorkloadGPUMemory(ctx, ns.Name, string(createdJobB.UID), migMemoryBytes)
		verifyCQGPUMemory(ctx, cq.Name, migMemoryBytes*2)
	})

	It("should preempt low-priority PD workload for high-priority", func(ctx context.Context) {
		quota := resource.NewQuantity(migMemoryBytes*2, resource.BinarySI)
		_, ns := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
			pdTestNamespacePrefix, pdLocalQueueName,
			func(cq *testutils.ClusterQueueWrapper) {
				cq.WithDRAResource(pdLogicalResource, quota.String()).
					WithPreemption(kueuev1beta2.PreemptionPolicyLowerPriority)
			})

		lowPC, cleanupLowPC, err := createPriorityClass(ctx, 100, "PD low priority")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupLowPC)
		highPC, cleanupHighPC, err := createPriorityClass(ctx, 1000, "PD high priority")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupHighPC)

		By("Creating ResourceClaimTemplate and submitting two low-priority jobs")
		rct := testutils.NewResourceClaimTemplate("mig-preempt", ns.Name, pdMigDeviceClass, 1,
			fmt.Sprintf("device.attributes['gpu.nvidia.com'].profile == '%s'", migProfile))
		_, err = kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		builder := testutils.NewTestResourceBuilder(ns.Name, pdLocalQueueName)

		lowJobA := builder.NewDRAJob("pd-low-a", pdLocalQueueName, "mig-preempt")
		lowJobA.Spec.Template.Spec.PriorityClassName = lowPC.Name
		createdLowA, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, lowJobA, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, ns.Name, createdLowA.Name)

		verifyWorkloadGPUMemory(ctx, ns.Name, string(createdLowA.UID), migMemoryBytes)

		lowJobB := builder.NewDRAJob("pd-low-b", pdLocalQueueName, "mig-preempt")
		lowJobB.Spec.Template.Spec.PriorityClassName = lowPC.Name
		createdLowB, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, lowJobB, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, ns.Name, createdLowB.Name)

		verifyWorkloadGPUMemory(ctx, ns.Name, string(createdLowB.UID), migMemoryBytes)

		By("Submitting high-priority job to trigger preemption")
		highJob := builder.NewDRAJob("pd-high", pdLocalQueueName, "mig-preempt")
		highJob.Spec.Template.Spec.PriorityClassName = highPC.Name
		createdHigh, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, highJob, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, ns.Name, createdHigh.Name)

		By("Verifying high-priority workload is admitted")
		verifyWorkloadGPUMemory(ctx, ns.Name, string(createdHigh.UID), migMemoryBytes)

		By("Verifying one low-priority workload was preempted")
		Eventually(func(g Gomega) {
			wlList, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(ns.Name).List(ctx, metav1.ListOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			preempted := false
			for _, wl := range wlList.Items {
				for _, c := range wl.Status.Conditions {
					if c.Type == "Evicted" && strings.Contains(c.Message, "Preempted") {
						preempted = true
					}
				}
			}
			g.Expect(preempted).To(BeTrue(), "expected one workload to be preempted")
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
	})

	It("should fall back to device-count when sources are removed", func(ctx context.Context) {
		_, ns := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
			pdTestNamespacePrefix, pdLocalQueueName,
			func(cq *testutils.ClusterQueueWrapper) {
				cq.WithDRAResource(pdLogicalResource, "4")
			})

		By("Creating ResourceClaimTemplate")
		rct := testutils.NewResourceClaimTemplate("mig-config-test", ns.Name, pdMigDeviceClass, 1,
			fmt.Sprintf("device.attributes['gpu.nvidia.com'].profile == '%s'", migProfile))
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Removing sources from Kueue CR")
		kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		kueueInstance.Spec.Config.Resources.DeviceClassMappings = []ssv1.DeviceClassMapping{
			{
				Name:             pdLogicalResource,
				DeviceClassNames: []ssv1.DeviceClassName{pdGpuDeviceClass, pdMigDeviceClass},
			},
		}
		applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)

		Eventually(func() error {
			configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
			if err != nil {
				return err
			}
			configData := configMap.Data["controller_manager_config.yaml"]
			if strings.Contains(configData, "KueueDRAIntegrationPartitionableDevices") {
				return fmt.Errorf("PD feature gate still present")
			}
			return nil
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

		builder := testutils.NewTestResourceBuilder(ns.Name, pdLocalQueueName)
		job := builder.NewDRAJob("pd-no-sources", pdLocalQueueName, "mig-config-test")
		createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, ns.Name, createdJob.Name)

		By("Verifying workload uses device-count charging (gpu.memory: 1)")
		verifyWorkloadGPUMemory(ctx, ns.Name, string(createdJob.UID), 1)

		By("Restoring sources")
		kueueInstance, err = clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		kueueInstance.Spec.Config.Resources.DeviceClassMappings = []ssv1.DeviceClassMapping{
			{
				Name:             pdLogicalResource,
				DeviceClassNames: []ssv1.DeviceClassName{pdGpuDeviceClass, pdMigDeviceClass},
				Sources: []ssv1.DeviceClassSourceConfig{
					{
						Type: ssv1.DeviceClassSourceTypeCounter,
						Counter: ssv1.DeviceClassCounterSource{
							Name:   "memory",
							Driver: pdDriverName,
							DeviceSelector: ssv1.DeviceSelector{
								Type: ssv1.DeviceSelectorTypeCEL,
								CEL:  ssv1.CELDeviceSelector{Expression: "device.driver == 'gpu.nvidia.com'"},
							},
						},
					},
				},
			},
		}
		applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)

		Eventually(func() error {
			configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
			if err != nil {
				return err
			}
			configData := configMap.Data["controller_manager_config.yaml"]
			if !strings.Contains(configData, "KueueDRAIntegrationPartitionableDevices") {
				return fmt.Errorf("PD feature gate not restored yet")
			}
			return nil
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
	})

	It("should charge largest counter value when CEL matches multiple device types", func(ctx context.Context) {
		if secondMIGMemoryBytes == 0 {
			Skip("No second MIG profile found")
		}
		_, ns := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
			pdTestNamespacePrefix, pdLocalQueueName,
			func(cq *testutils.ClusterQueueWrapper) {
				cq.WithDRAResource(pdLogicalResource, "80Gi")
			})

		By("Creating ResourceClaimTemplate matching two MIG profiles and Job")
		rct := testutils.NewResourceClaimTemplate("mig-broad", ns.Name, pdMigDeviceClass, 1,
			fmt.Sprintf("device.attributes['gpu.nvidia.com'].profile == '%s' || device.attributes['gpu.nvidia.com'].profile == '%s'", migProfile, secondMIGProfile))
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		builder := testutils.NewTestResourceBuilder(ns.Name, pdLocalQueueName)
		job := builder.NewDRAJob("pd-broad-cel", pdLocalQueueName, "mig-broad")
		createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, ns.Name, createdJob.Name)

		By("Verifying workload is charged the largest counter value")
		verifyWorkloadGPUMemory(ctx, ns.Name, string(createdJob.UID), secondMIGMemoryBytes)
	})

	It("should charge whole GPU memory for gpu.nvidia.com without CEL", func(ctx context.Context) {
		if !hasWholeGPUDevice {
			Skip("No whole GPU device available - GPU is fully MIG-partitioned")
		}
		_, ns := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
			pdTestNamespacePrefix, pdLocalQueueName,
			func(cq *testutils.ClusterQueueWrapper) {
				cq.WithDRAResource(pdLogicalResource, "80Gi")
			})

		By("Creating ResourceClaimTemplate for whole GPU and Job")
		rct := testutils.NewResourceClaimTemplate("whole-gpu", ns.Name, pdGpuDeviceClass, 1, "")
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		builder := testutils.NewTestResourceBuilder(ns.Name, pdLocalQueueName)
		job := builder.NewDRAJob("pd-whole-gpu", pdLocalQueueName, "whole-gpu")
		createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, ns.Name, createdJob.Name)

		By("Verifying workload is charged whole GPU memory")
		verifyWorkloadGPUMemory(ctx, ns.Name, string(createdJob.UID), wholeGPUMemoryBytes)
	})

	It("should mark workload inadmissible when requesting more devices than available", func(ctx context.Context) {
		_, ns := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
			pdTestNamespacePrefix, pdLocalQueueName,
			func(cq *testutils.ClusterQueueWrapper) {
				cq.WithDRAResource(pdLogicalResource, "80Gi")
			})

		By("Creating ResourceClaimTemplate requesting more devices than available")
		exceedCount := int64(migDeviceCount + 1)
		rct := testutils.NewResourceClaimTemplate("mig-exceed", ns.Name, pdMigDeviceClass, exceedCount,
			fmt.Sprintf("device.attributes['gpu.nvidia.com'].profile == '%s'", migProfile))
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		builder := testutils.NewTestResourceBuilder(ns.Name, pdLocalQueueName)
		job := builder.NewDRAJob("pd-exceed", pdLocalQueueName, "mig-exceed")
		createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, ns.Name, createdJob.Name)

		By("Verifying workload is inadmissible")
		verifyWorkloadInadmissible(ctx, ns.Name, string(createdJob.UID))
	})

	It("should charge unified gpu.memory for whole GPU and MIG partition", func(ctx context.Context) {
		if !hasWholeGPUDevice {
			Skip("No whole GPU device available - GPU is fully MIG-partitioned")
		}
		_, ns := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
			pdTestNamespacePrefix, pdLocalQueueName,
			func(cq *testutils.ClusterQueueWrapper) {
				cq.WithDRAResource(pdLogicalResource, "160Gi")
			})

		By("Creating ResourceClaimTemplates for whole GPU and MIG partition")
		rctGPU := testutils.NewResourceClaimTemplate("unified-gpu", ns.Name, pdGpuDeviceClass, 1, "")
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rctGPU, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		rctMIG := testutils.NewResourceClaimTemplate("unified-mig", ns.Name, pdMigDeviceClass, 1,
			fmt.Sprintf("device.attributes['gpu.nvidia.com'].profile == '%s'", migProfile))
		_, err = kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rctMIG, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Creating Job with both whole GPU and MIG claims")
		builder := testutils.NewTestResourceBuilder(ns.Name, pdLocalQueueName)
		job := builder.NewDRAJob("pd-unified", pdLocalQueueName, "unified-gpu")
		job.Spec.Template.Spec.ResourceClaims = append(job.Spec.Template.Spec.ResourceClaims,
			corev1.PodResourceClaim{Name: "mig", ResourceClaimTemplateName: ptr.To("unified-mig")})
		job.Spec.Template.Spec.Containers[0].Resources.Claims = append(job.Spec.Template.Spec.Containers[0].Resources.Claims,
			corev1.ResourceClaim{Name: "mig"})
		createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, ns.Name, createdJob.Name)

		By("Verifying workload is charged combined GPU + MIG memory")

		verifyWorkloadGPUMemory(ctx, ns.Name, string(createdJob.UID), wholeGPUMemoryBytes+migMemoryBytes)
	})

	It("should admit Pod workload with counter-based charging", func(ctx context.Context) {
		_, ns := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
			pdTestNamespacePrefix, pdLocalQueueName,
			func(cq *testutils.ClusterQueueWrapper) {
				cq.WithDRAResource(pdLogicalResource, "80Gi")
			})

		By("Creating ResourceClaimTemplate and Pod with DRA claims")
		rct := testutils.NewResourceClaimTemplate("mig-pod", ns.Name, pdMigDeviceClass, 1,
			fmt.Sprintf("device.attributes['gpu.nvidia.com'].profile == '%s'", migProfile))
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		builder := testutils.NewTestResourceBuilder(ns.Name, pdLocalQueueName)
		pod := builder.NewPod()
		pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("100m")
		pod.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = resource.MustParse("100Mi")
		addDRAClaims(&pod.Spec, "mig-pod")
		createdPod, err := kubeClient.CoreV1().Pods(ns.Name).Create(ctx, pod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = kubeClient.CoreV1().Pods(ns.Name).Delete(context.TODO(), createdPod.Name, metav1.DeleteOptions{})
		})

		By("Verifying workload is admitted and pod is running")
		waitForPodWorkloadAdmitted(ctx, kubeClient, clients.UpstreamKueueClient, ns.Name, fmt.Sprintf("kueue.x-k8s.io/queue-name=%s", pdLocalQueueName))

		Eventually(func(g Gomega) {
			p, err := kubeClient.CoreV1().Pods(ns.Name).Get(ctx, createdPod.Name, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(p.Status.Phase).To(Equal(corev1.PodRunning))
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
	})

	It("should admit Deployment workload with counter-based charging", func(ctx context.Context) {
		_, ns := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
			pdTestNamespacePrefix, pdLocalQueueName,
			func(cq *testutils.ClusterQueueWrapper) {
				cq.WithDRAResource(pdLogicalResource, "80Gi")
			})

		By("Creating ResourceClaimTemplate and Deployment with DRA claims")
		rct := testutils.NewResourceClaimTemplate("mig-deploy", ns.Name, pdMigDeviceClass, 1,
			fmt.Sprintf("device.attributes['gpu.nvidia.com'].profile == '%s'", migProfile))
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		builder := testutils.NewTestResourceBuilder(ns.Name, pdLocalQueueName)
		deploy := builder.NewDeployment()
		addDRAClaims(&deploy.Spec.Template.Spec, "mig-deploy")
		createdDeploy, err := kubeClient.AppsV1().Deployments(ns.Name).Create(ctx, deploy, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = kubeClient.AppsV1().Deployments(ns.Name).Delete(context.TODO(), createdDeploy.Name, metav1.DeleteOptions{})
		})

		By("Verifying workload is admitted")
		waitForPodWorkloadAdmitted(ctx, kubeClient, clients.UpstreamKueueClient, ns.Name, "app=test-deployment")
	})

	It("should admit StatefulSet workload with counter-based charging", func(ctx context.Context) {
		_, ns := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
			pdTestNamespacePrefix, pdLocalQueueName,
			func(cq *testutils.ClusterQueueWrapper) {
				cq.WithDRAResource(pdLogicalResource, "80Gi")
			})

		By("Creating ResourceClaimTemplate and StatefulSet with DRA claims")
		rct := testutils.NewResourceClaimTemplate("mig-sts", ns.Name, pdMigDeviceClass, 1,
			fmt.Sprintf("device.attributes['gpu.nvidia.com'].profile == '%s'", migProfile))
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		builder := testutils.NewTestResourceBuilder(ns.Name, pdLocalQueueName)
		sts := builder.NewStatefulSet()
		addDRAClaims(&sts.Spec.Template.Spec, "mig-sts")
		createdSts, err := kubeClient.AppsV1().StatefulSets(ns.Name).Create(ctx, sts, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = kubeClient.AppsV1().StatefulSets(ns.Name).Delete(context.TODO(), createdSts.Name, metav1.DeleteOptions{})
		})

		By("Verifying workload is admitted")
		waitForPodWorkloadAdmitted(ctx, kubeClient, clients.UpstreamKueueClient, ns.Name, "app=test-statefulset")
	})

	It("alpha limitation: mixing counter and device-count charges in the same quota resource produces inconsistent accounting", func(ctx context.Context) {
		cq, ns := testutils.SetupTestEnv(ctx, kubeClient, clients.UpstreamKueueClient,
			pdTestNamespacePrefix, pdLocalQueueName,
			func(cq *testutils.ClusterQueueWrapper) {
				cq.WithDRAResource(pdLogicalResource, "80Gi")
			})

		rct := testutils.NewResourceClaimTemplate("mig-runtime", ns.Name, pdMigDeviceClass, 1,
			fmt.Sprintf("device.attributes['gpu.nvidia.com'].profile == '%s'", migProfile))
		_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		builder := testutils.NewTestResourceBuilder(ns.Name, pdLocalQueueName)

		By("Submitting first job with sources configured")
		job1 := builder.NewDRAJob("pd-runtime-1", pdLocalQueueName, "mig-runtime")
		job1.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "sleep 600"}
		createdJob1, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job1, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, ns.Name, createdJob1.Name)

		verifyWorkloadGPUMemory(ctx, ns.Name, string(createdJob1.UID), migMemoryBytes)

		By("Removing sources from Kueue CR")
		kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		kueueInstance.Spec.Config.Resources.DeviceClassMappings = []ssv1.DeviceClassMapping{
			{
				Name:             pdLogicalResource,
				DeviceClassNames: []ssv1.DeviceClassName{pdGpuDeviceClass, pdMigDeviceClass},
			},
		}
		applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)

		By("Submitting second job after sources removed")
		job2 := builder.NewDRAJob("pd-runtime-2", pdLocalQueueName, "mig-runtime")
		createdJob2, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job2, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(testutils.CleanUpJob, kubeClient, ns.Name, createdJob2.Name)

		By("Verifying second job uses device-count charging")
		verifyWorkloadGPUMemory(ctx, ns.Name, string(createdJob2.UID), 1)

		By("Verifying CQ has mixed counter and device-count charges")
		verifyCQGPUMemory(ctx, cq.Name, migMemoryBytes+1)
	})
})
