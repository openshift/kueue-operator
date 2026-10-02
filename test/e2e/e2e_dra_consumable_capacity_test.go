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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	operatorv1 "github.com/openshift/api/operator/v1"
	ssv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kueueconfigapi "sigs.k8s.io/kueue/apis/config/v1beta2"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
	"sigs.k8s.io/yaml"
)

// Consumable Capacity (CC) e2e coverage. CC is a DRA feature (the Capacity
// source sibling of the Counter source exercised in
// e2e_dra_partitionable_devices_test.go). CC requires Kubernetes 1.36+
// (OCP 4.23+), where the DRAConsumableCapacity feature gate is beta/default-on.
//
// The operator classifies the cluster into three bands and behaves accordingly:
//
//	Band          Kube / OCP          DRA APIs   CC gate   Behavior
//	A             <1.34 / 4.18-4.20   absent     off       Degraded: DRA missing dependency
//	B             1.34-1.35 / 4.21-22 present    off       Degraded: CC missing dependency
//	supported     1.36+ / 4.23, 5.0+  present    on        Capacity source is honored
//
// Unsupported bands (A, B): the operator must fail closed - Degraded=True with
// Reason=MissingDependencies and the band-specific message, no Capacity source in
// the rendered operand config, controller stays Available.
// Supported band: the operator renders the Capacity source into
// kueue-manager-config and keeps the operand Available. Workload-level scenarios
// additionally need a DRA driver publishing consumable capacity.

const (
	ccResourceName        = "gpu.memory"
	ccDeviceClassName     = "gpu.example.com"
	ccDriverName          = "gpu.example.com"
	ccCapacityDimension   = "memory"
	ccDeviceSelectorCEL   = "device.driver == 'gpu.example.com'"
	ccTestNamespacePrefix = "kueue-dra-cc-test-"
	ccLocalQueueName      = "cc-test-queue"

	ccFeatureGate = "KueueDRAIntegrationConsumableCapacity"

	ccMissingDependenciesReason = "MissingDependencies"
	ccMissingDependenciesPrefix = "Please install the following on your cluster:"

	draMissingDependency = "DRA (Dynamic Resource Allocation) requires Kubernetes 1.34+ (OCP 4.21+)"
	ccMissingDependency  = "DRA Consumable Capacity requires Kubernetes 1.36+ (OCP 4.23+) and the DRAConsumableCapacity feature gate to be enabled"
)

var _ = Describe("DRA Consumable Capacity", Label("dra", "dra-consumable-capacity"), Ordered, func() {
	var (
		initialKueueInstance *ssv1.Kueue
		initialConfigMapData string
		draAbsent            bool
		ccSupported          bool
	)

	JustAfterEach(func(ctx context.Context) {
		testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 500)
	})

	BeforeAll(func(ctx context.Context) {
		draAbsent = !testutils.IsDRASupported(kubeClient)
		var err error
		ccSupported, err = testutils.KubeMinorAtLeast(kubeClient, 36)
		Expect(err).NotTo(HaveOccurred(), "failed to discover Kubernetes server version")
		ccSupported = !draAbsent && ccSupported

		instance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to fetch Kueue instance")
		initialKueueInstance = instance.DeepCopy()

		cm, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to fetch kueue-manager-config")
		initialConfigMapData = cm.Data["controller_manager_config.yaml"]
	})

	AfterAll(func(ctx context.Context) {
		if initialKueueInstance == nil {
			return
		}
		setKueueConfig(ctx, initialKueueInstance.Spec.Config)
		waitForRestoredController(ctx, initialConfigMapData)
	})

	// Unsupported clusters (OCP 4.18-4.22) must report the missing dependency and
	// fail closed. Skips entirely on supported clusters.
	When("the cluster does not support Consumable Capacity", Ordered, func() {
		BeforeAll(func(ctx context.Context) {
			if ccSupported {
				Skip("cluster supports Consumable Capacity (Kubernetes 1.36+); missing-dependency scenario not applicable")
			}
			setKueueConfig(ctx, ccCapacityConfig(initialKueueInstance.Spec.Config))
		})

		It("reports the DRA missing dependency when the DRA APIs are absent", func(ctx context.Context) {
			if !draAbsent {
				Skip("DRA APIs (resource.k8s.io/v1) are present on this cluster")
			}
			expectDegradedMissingDependency(ctx, draMissingDependency)
			expectCapacityNotRendered(ctx)
			expectControllerAvailable(ctx)
		})

		It("reports the Consumable Capacity missing dependency when the feature gate is off", func(ctx context.Context) {
			if draAbsent {
				Skip("DRA APIs (resource.k8s.io/v1) are absent on this cluster")
			}
			expectDegradedMissingDependency(ctx, ccMissingDependency)
			expectCapacityNotRendered(ctx)
			expectControllerAvailable(ctx)
		})
	})

	// Supported clusters (OCP 4.23+/5.0, Kubernetes 1.36+) render the Capacity
	// source and keep the operand healthy. Skips entirely on unsupported clusters.
	When("the cluster supports Consumable Capacity", Ordered, func() {
		BeforeAll(func(ctx context.Context) {
			if !ccSupported {
				Skip("cluster does not support Consumable Capacity (requires Kubernetes 1.36+)")
			}
			applyKueueConfig(ctx, ccCapacityConfig(initialKueueInstance.Spec.Config), kubeClient)
		})

		It("renders the Capacity source into the operand config and keeps the operand Available", func(ctx context.Context) {
			expectCapacityRendered(ctx)
			expectControllerAvailable(ctx)
			expectKueueAvailable(ctx)
		})

		It("enables the Consumable Capacity feature gate in the operand config", func(ctx context.Context) {
			expectConsumableCapacityGateEnabled(ctx)
		})

		When("a DRA driver supporting Consumable Capacity is available", func() {
			BeforeAll(func(ctx context.Context) {
				if !hasConsumableCapacityResourceSlices(ctx) {
					Skip("no ResourceSlices with consumable capacity found for driver " + ccDriverName)
				}
			})

			It("admits and charges a Job with an explicit capacity request", func(ctx context.Context) {
				kueueClient := clients.UpstreamKueueClient
				cq, ns := testutils.SetupTestEnv(ctx, kubeClient, kueueClient,
					ccTestNamespacePrefix, ccLocalQueueName,
					func(cq *testutils.ClusterQueueWrapper) {
						cq.WithDRAResource(ccResourceName, "320Gi")
					})

				By("Creating ResourceClaimTemplate with an explicit 20Gi capacity request")
				rct := newConsumableCapacityResourceClaimTemplate(
					"cc-explicit-template", ns.Name, 1, "20Gi", "")
				_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())

				By("Creating Job that references the capacity ResourceClaimTemplate")
				builder := testutils.NewTestResourceBuilder(ns.Name, ccLocalQueueName)
				job := builder.NewDRAJob("cc-explicit-job", ccLocalQueueName, rct.Name)
				createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob.Namespace, createdJob.Name)

				By("Verifying the Workload is admitted and charged 20Gi of gpu.memory")
				verifyConsumableCapacityWorkload(
					ctx, ns.Name, string(createdJob.UID), resource.MustParse("20Gi"))

				By("Verifying the ClusterQueue has a 20Gi gpu.memory reservation")
				expectClusterQueueResourceReservation(ctx, cq.Name, resource.MustParse("20Gi"))

				By("Verifying the Job is unsuspended and its Pod is running")
				Eventually(func() bool {
					return !testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
				}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())
				Eventually(func() bool {
					return testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob.Name)
				}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

				By("Deleting the Job and verifying the capacity reservation is released")
				testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)
				expectClusterQueueResourceReservation(ctx, cq.Name, resource.Quantity{})
			})

			It("multiplies the capacity charge by the requested device count", func(ctx context.Context) {
				kueueClient := clients.UpstreamKueueClient
				cq, ns := testutils.SetupTestEnv(ctx, kubeClient, kueueClient,
					ccTestNamespacePrefix, ccLocalQueueName,
					func(cq *testutils.ClusterQueueWrapper) {
						cq.WithDRAResource(ccResourceName, "320Gi")
					})

				By("Creating a ResourceClaimTemplate for 2 devices with a 20Gi capacity request")
				rct := newConsumableCapacityResourceClaimTemplate(
					"cc-count2-template", ns.Name, 2, "20Gi", "")
				_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())

				By("Creating a Job that requests 2 devices")
				builder := testutils.NewTestResourceBuilder(ns.Name, ccLocalQueueName)
				job := builder.NewDRAJob("cc-count2-job", ccLocalQueueName, rct.Name)
				createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob.Namespace, createdJob.Name)

				By("Verifying the Workload is admitted and charged 40Gi of gpu.memory")
				verifyConsumableCapacityWorkload(
					ctx, ns.Name, string(createdJob.UID), resource.MustParse("40Gi"))

				By("Verifying the ClusterQueue has a 40Gi gpu.memory reservation")
				expectClusterQueueResourceReservation(ctx, cq.Name, resource.MustParse("40Gi"))

				By("Verifying the Job is unsuspended and its Pod is running")
				Eventually(func() bool {
					return !testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
				}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())
				Eventually(func() bool {
					return testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, createdJob.Name)
				}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

				By("Deleting the Job and verifying the capacity reservation is released")
				testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)
				expectClusterQueueResourceReservation(ctx, cq.Name, resource.Quantity{})
			})

			It("marks a workload inadmissible when no device matches the capacity selector", func(ctx context.Context) {
				const noMatchSelectorCEL = "device.capacity[\"gpu.example.com\"].memory.compareTo(quantity(\"999Gi\")) == 0"
				kueueClient := clients.UpstreamKueueClient
				_, ns := testutils.SetupTestEnv(ctx, kubeClient, kueueClient,
					ccTestNamespacePrefix, ccLocalQueueName,
					func(cq *testutils.ClusterQueueWrapper) {
						cq.WithDRAResource(ccResourceName, "320Gi")
					})

				By("Creating a ResourceClaimTemplate with an unmatchable CEL selector")
				rct := newConsumableCapacityResourceClaimTemplate(
					"cc-nomatch-template", ns.Name, 1, "10Gi", noMatchSelectorCEL)
				_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())

				By("Creating a Job with the unmatchable CEL selector")
				builder := testutils.NewTestResourceBuilder(ns.Name, ccLocalQueueName)
				job := builder.NewDRAJob("cc-nomatch-job", ccLocalQueueName, rct.Name)
				createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob.Namespace, createdJob.Name)

				By("Verifying the Workload is inadmissible")
				verifyConsumableCapacityWorkloadInadmissible(ctx, ns.Name, string(createdJob.UID))
			})

			It("admits multiple workloads that fully consume capacity and leaves the next workload pending", func(ctx context.Context) {
				kueueClient := clients.UpstreamKueueClient
				cq, ns := testutils.SetupTestEnv(ctx, kubeClient, kueueClient,
					ccTestNamespacePrefix, ccLocalQueueName,
					func(cq *testutils.ClusterQueueWrapper) {
						cq.WithDRAResource(ccResourceName, "40Gi")
					})

				By("Creating a ResourceClaimTemplate for a 20Gi capacity request")
				rct := newConsumableCapacityResourceClaimTemplate(
					"cc-share-template", ns.Name, 1, "20Gi", "")
				_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())

				By("Creating two Jobs sharing the same gpu.memory capacity pool")
				builder := testutils.NewTestResourceBuilder(ns.Name, ccLocalQueueName)
				job1 := builder.NewDRAJob("cc-share-job-1", ccLocalQueueName, rct.Name)
				createdJob1, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job1, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob1.Namespace, createdJob1.Name)

				job2 := builder.NewDRAJob("cc-share-job-2", ccLocalQueueName, rct.Name)
				createdJob2, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job2, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob2.Namespace, createdJob2.Name)

				By("Verifying both Workloads are admitted with 20Gi charges")
				for _, jobUID := range []string{string(createdJob1.UID), string(createdJob2.UID)} {
					verifyConsumableCapacityWorkload(ctx, ns.Name, jobUID, resource.MustParse("20Gi"))
				}

				By("Verifying the ClusterQueue has a 40Gi gpu.memory reservation")
				expectClusterQueueResourceReservation(ctx, cq.Name, resource.MustParse("40Gi"))

				By("Creating a third Job that exceeds the shared capacity quota")
				job3 := builder.NewDRAJob("cc-share-job-3", ccLocalQueueName, rct.Name)
				createdJob3, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job3, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob3.Namespace, createdJob3.Name)

				By("Verifying the third Workload remains pending without a reservation")
				verifyConsumableCapacityWorkloadPending(ctx, ns.Name, cq.Name, string(createdJob3.UID))
				expectClusterQueueResourceReservation(ctx, cq.Name, resource.MustParse("40Gi"))
				Eventually(func() bool {
					return testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob3.Name)
				}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

				By("Verifying both admitted Jobs are unsuspended and their Pods are running")
				for _, jobName := range []string{createdJob1.Name, createdJob2.Name} {
					Eventually(func() bool {
						return !testutils.IsJobSuspended(ctx, kubeClient, ns.Name, jobName)
					}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())
					Eventually(func() bool {
						return testutils.IsJobPodRunning(ctx, kubeClient, ns.Name, jobName)
					}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())
				}

				By("Deleting both Jobs and verifying the capacity reservation is released")
				testutils.CleanUpJob(ctx, kubeClient, createdJob1.Namespace, createdJob1.Name)
				testutils.CleanUpJob(ctx, kubeClient, createdJob2.Namespace, createdJob2.Name)
				testutils.CleanUpJob(ctx, kubeClient, createdJob3.Namespace, createdJob3.Name)
				expectClusterQueueResourceReservation(ctx, cq.Name, resource.Quantity{})
			})

			It("keeps a workload pending when its capacity charge exceeds ClusterQueue quota", func(ctx context.Context) {
				kueueClient := clients.UpstreamKueueClient
				cq, ns := testutils.SetupTestEnv(ctx, kubeClient, kueueClient,
					ccTestNamespacePrefix, ccLocalQueueName,
					func(cq *testutils.ClusterQueueWrapper) {
						cq.WithDRAResource(ccResourceName, "40Gi")
					})

				By("Creating a ResourceClaimTemplate requesting 3 devices at 20Gi each")
				rct := newConsumableCapacityResourceClaimTemplate(
					"cc-exceed-template", ns.Name, 3, "20Gi", "")
				_, err := kubeClient.ResourceV1().ResourceClaimTemplates(ns.Name).Create(ctx, rct, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())

				By("Creating a Job whose 60Gi capacity charge exceeds the 40Gi quota")
				builder := testutils.NewTestResourceBuilder(ns.Name, ccLocalQueueName)
				job := builder.NewDRAJob("cc-exceed-job", ccLocalQueueName, rct.Name)
				createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(testutils.CleanUpJob, kubeClient, createdJob.Namespace, createdJob.Name)

				By("Verifying the Workload remains pending without a reservation")
				verifyConsumableCapacityWorkloadPending(ctx, ns.Name, cq.Name, string(createdJob.UID))
				expectClusterQueueResourceReservation(ctx, cq.Name, resource.Quantity{})
				Eventually(func() bool {
					return testutils.IsJobSuspended(ctx, kubeClient, ns.Name, createdJob.Name)
				}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

				testutils.CleanUpJob(ctx, kubeClient, createdJob.Namespace, createdJob.Name)
			})
		})

		When("Capacity is added to an existing Kueue configuration", Ordered, func() {
			BeforeAll(func(ctx context.Context) {
				if !ccSupported {
					Skip("cluster does not support Consumable Capacity (requires Kubernetes 1.36+)")
				}
				applyKueueConfig(ctx, initialKueueInstance.Spec.Config, kubeClient)
			})

			It("updates the Kueue configuration and rolls the controller", func(ctx context.Context) {
				By("Adding the Capacity source to the existing Kueue configuration")
				applyKueueConfig(ctx, ccCapacityConfig(initialKueueInstance.Spec.Config), kubeClient)

				By("Verifying the Capacity source and feature gate are rendered after the rollout")
				expectCapacityRendered(ctx)
				expectConsumableCapacityGateEnabled(ctx)
				expectControllerAvailable(ctx)
				expectKueueAvailable(ctx)
			})
		})
	})
})

func hasConsumableCapacityResourceSlices(ctx context.Context) bool {
	hasDriverSlices := false
	for i := 0; i < 6 && !hasDriverSlices; i++ {
		if i > 0 {
			time.Sleep(5 * time.Second)
		}
		slices, err := kubeClient.ResourceV1().ResourceSlices().List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		for _, slice := range slices.Items {
			if slice.Spec.Driver != ccDriverName {
				continue
			}
			for _, device := range slice.Spec.Devices {
				allowed := device.AllowMultipleAllocations != nil && *device.AllowMultipleAllocations
				if allowed {
					if _, ok := device.Capacity[resourcev1.QualifiedName(ccCapacityDimension)]; ok {
						hasDriverSlices = true
						break
					}
				}
			}
		}
	}
	return hasDriverSlices
}

func verifyConsumableCapacityWorkload(ctx context.Context, namespace, jobUID string, expected resource.Quantity) {
	Eventually(func(g Gomega) {
		workloads, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", jobUID),
		})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(workloads.Items).NotTo(BeEmpty())

		workload := workloads.Items[0]
		g.Expect(workload.Status.Admission).NotTo(BeNil(), "workload should be admitted")
		g.Expect(workload.Status.Admission.PodSetAssignments).To(HaveLen(1))
		assignment := workload.Status.Admission.PodSetAssignments[0]
		g.Expect(assignment.ResourceUsage).To(HaveKey(corev1.ResourceName(ccResourceName)))
		usage := assignment.ResourceUsage[corev1.ResourceName(ccResourceName)]
		g.Expect(usage.Cmp(expected)).To(Equal(0), "expected %s=%s, got %s", ccResourceName, expected.String(), usage.String())
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}

func verifyConsumableCapacityWorkloadInadmissible(ctx context.Context, namespace, jobUID string) {
	Eventually(func(g Gomega) {
		workloads, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", jobUID),
		})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(workloads.Items).NotTo(BeEmpty())

		workload := workloads.Items[0]
		g.Expect(workload.Status.Admission).To(BeNil())
		g.Expect(workload.Status.Conditions).To(ContainElement(And(
			HaveField("Type", kueuev1beta2.WorkloadQuotaReserved),
			HaveField("Status", metav1.ConditionFalse),
			HaveField("Message", ContainSubstring("insufficient matching devices for CEL selector")),
		)))
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}

func verifyConsumableCapacityWorkloadPending(ctx context.Context, namespace, clusterQueueName, jobUID string) {
	Eventually(func(g Gomega) {
		workloads, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", jobUID),
		})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(workloads.Items).NotTo(BeEmpty())
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

	Consistently(func(g Gomega) {
		workloads, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", jobUID),
		})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(workloads.Items).NotTo(BeEmpty())
		workload := workloads.Items[0]
		g.Expect(workload.Status.Admission).To(BeNil())
		g.Expect(workload.Status.Conditions).To(ContainElement(And(
			HaveField("Type", kueuev1beta2.WorkloadQuotaReserved),
			HaveField("Status", metav1.ConditionFalse),
			HaveField("Message", And(
				ContainSubstring("insufficient"),
				ContainSubstring("quota"),
				ContainSubstring(ccResourceName),
			)),
		)))

	}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(Succeed())

	Eventually(func(g Gomega) {
		clusterQueue, err := clients.UpstreamKueueClient.KueueV1beta2().ClusterQueues().Get(ctx, clusterQueueName, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(clusterQueue.Status.PendingWorkloads).To(BeNumerically(">=", 1))
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}

func newConsumableCapacityResourceClaimTemplate(name, namespace string, count int64, memory, celExpression string) *resourcev1.ResourceClaimTemplate {
	rct := testutils.NewResourceClaimTemplate(name, namespace, ccDeviceClassName, count, celExpression)
	rct.Spec.Spec.Devices.Requests[0].Exactly.Capacity = &resourcev1.CapacityRequirements{
		Requests: map[resourcev1.QualifiedName]resource.Quantity{
			resourcev1.QualifiedName(ccCapacityDimension): resource.MustParse(memory),
		},
	}
	return rct
}

func expectClusterQueueResourceReservation(ctx context.Context, clusterQueueName string, expected resource.Quantity) {
	Eventually(func(g Gomega) {
		clusterQueue, err := clients.UpstreamKueueClient.KueueV1beta2().ClusterQueues().Get(ctx, clusterQueueName, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())

		for _, flavorUsage := range clusterQueue.Status.FlavorsReservation {
			for _, reservation := range flavorUsage.Resources {
				if reservation.Name == corev1.ResourceName(ccResourceName) {
					g.Expect(reservation.Total.Cmp(expected)).To(Equal(0),
						"ClusterQueue %s should reserve %s of %s", clusterQueueName, expected.String(), ccResourceName)
					return
				}
			}
		}

		if expected.IsZero() {
			return
		}
		g.Expect(false).To(BeTrue(), "resource reservation %s was not found", ccResourceName)
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}

// ccCapacityConfig returns a copy of base with a single Capacity-source
// DeviceClassMapping added.
func ccCapacityConfig(base ssv1.KueueConfiguration) ssv1.KueueConfiguration {
	config := *base.DeepCopy()
	config.Resources = ssv1.Resources{
		DeviceClassMappings: []ssv1.DeviceClassMapping{
			{
				Name:             ccResourceName,
				DeviceClassNames: []ssv1.DeviceClassName{ccDeviceClassName},
				Sources: []ssv1.DeviceClassSourceConfig{
					{
						Type: ssv1.DeviceClassSourceTypeCapacity,
						Capacity: ssv1.DeviceClassCapacitySource{
							Name:   ccCapacityDimension,
							Driver: ccDriverName,
							DeviceSelector: ssv1.DeviceSelector{
								Type: ssv1.DeviceSelectorTypeCEL,
								CEL:  ssv1.CELDeviceSelector{Expression: ccDeviceSelectorCEL},
							},
						},
					},
				},
			},
		},
	}
	return config
}

// setKueueConfig updates the cluster Kueue CR's spec.config with conflict retry.
// Unlike applyKueueConfig it does not wait for a ConfigMap change or deployment
// roll: on unsupported clusters the Capacity source is not rendered, so those
// waits would time out. The It-level assertions do the waiting via Eventually.
func setKueueConfig(ctx context.Context, config ssv1.KueueConfiguration) {
	kueueClientset := clients.KueueClient
	Eventually(func() error {
		instance, err := kueueClientset.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		if err != nil {
			return err
		}
		instance.Spec.Config = config
		_, err = kueueClientset.KueueV1().Kueues().Update(ctx, instance, metav1.UpdateOptions{})
		return err
	}, 30*time.Second, 2*time.Second).Should(Succeed(), "failed to update Kueue config")
}

// expectDegradedMissingDependency waits for the Kueue CR to report
// Degraded=True, Reason=MissingDependencies, with a message that contains the
// operator's wrapper prefix and the band-specific substring.
func expectDegradedMissingDependency(ctx context.Context, substring string) {
	By("Waiting for Degraded MissingDependencies condition")
	var matched operatorv1.OperatorCondition
	Eventually(func(g Gomega) {
		kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		var degraded *operatorv1.OperatorCondition
		for i := range kueueInstance.Status.Conditions {
			c := &kueueInstance.Status.Conditions[i]
			if c.Type == operatorv1.OperatorStatusTypeDegraded && c.Status == operatorv1.ConditionTrue {
				degraded = c
				break
			}
		}
		g.Expect(degraded).NotTo(BeNil(), "expected a Degraded=True condition on the Kueue CR; got: %s", formatConditions(kueueInstance.Status.Conditions))
		g.Expect(degraded.Reason).To(Equal(ccMissingDependenciesReason))
		g.Expect(degraded.Message).To(ContainSubstring(ccMissingDependenciesPrefix))
		g.Expect(degraded.Message).To(ContainSubstring(substring))
		matched = *degraded
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "expected Degraded MissingDependencies condition with substring %q", substring)
	By("Verified missing dependency: " + matched.Message)
}

// expectCapacityNotRendered asserts the operand config never renders the
// unsupported Capacity source (its DeviceClassMapping carries no sources) and
// never enables the Consumable Capacity feature gate.
func expectCapacityNotRendered(ctx context.Context) {
	By("Verifying the Capacity source and feature gate are not rendered into the operand config")
	Consistently(func(g Gomega) {
		cm, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		hasSources, err := configHasDeviceClassSources(cm.Data["controller_manager_config.yaml"])
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(hasSources).To(BeFalse(), "Capacity source should not be rendered into the operand config on an unsupported cluster")

		var config kueueconfigapi.Configuration
		g.Expect(yaml.Unmarshal([]byte(cm.Data["controller_manager_config.yaml"]), &config)).To(Succeed())
		g.Expect(config.FeatureGates).NotTo(HaveKey(ccFeatureGate), "Consumable Capacity feature gate should not be enabled on an unsupported cluster")
	}, testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(Succeed())
}

// controllerDeploymentReady returns a Gomega assertion that the operand
// deployment has finished any in-flight rollout and has all replicas ready.
func controllerDeploymentReady(ctx context.Context) func(Gomega) {
	return func(g Gomega) {
		dep, err := kubeClient.AppsV1().Deployments(testutils.OperatorNamespace).Get(ctx, "kueue-controller-manager", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(dep.Status.ObservedGeneration).To(Equal(dep.Generation), "controller deployment has an in-progress rollout")
		g.Expect(dep.Status.Replicas).To(BeNumerically(">", 0), "controller deployment has no replicas")
		g.Expect(dep.Status.UpdatedReplicas).To(Equal(dep.Status.Replicas), "controller deployment is mid-rollout")
		g.Expect(dep.Status.ReadyReplicas).To(Equal(dep.Status.Replicas), "controller deployment is not fully ready")
	}
}

// waitControllerSettled waits for any config-triggered operand rollout to
// complete and all replicas to become ready. Suitable for cleanup (AfterAll),
// where a restore rolls the deployment and we only need it to settle.
func waitControllerSettled(ctx context.Context) {
	Eventually(controllerDeploymentReady(ctx), 2*time.Minute, 3*time.Second).Should(Succeed(), "kueue-controller-manager did not become Available")
}

// waitForRestoredController waits for the operand config to roll back to the
// captured original content, then for the controller to settle. A content match
// (not a change) is a no-op on unsupported clusters, where nothing rendered.
func waitForRestoredController(ctx context.Context, expected string) {
	By("Waiting for the operand config to be restored")
	Eventually(func(g Gomega) {
		cm, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cm.Data["controller_manager_config.yaml"]).To(Equal(expected))
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
	waitControllerSettled(ctx)
}

// expectControllerAvailable asserts the operand settles after the config change and
// then STAYS Available. waitControllerSettled proves the roll completed without a
// startup crashloop; the short Consistently guards the "ready-then-crashes" case.
func expectControllerAvailable(ctx context.Context) {
	By("Verifying kueue-controller-manager stays Available")
	waitControllerSettled(ctx)
	Consistently(controllerDeploymentReady(ctx), testutils.ConsistentlyTimeout, testutils.ConsistentlyPoll).Should(Succeed(), "kueue-controller-manager should stay Available on an unsupported cluster")
}

// expectCapacityRendered asserts the operand config renders the configured
// Capacity source with its dimension name, driver, and CEL selector intact.
func expectCapacityRendered(ctx context.Context) {
	By("Verifying the Capacity source is rendered into the operand config")
	Eventually(func(g Gomega) {
		cm, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		var config kueueconfigapi.Configuration
		g.Expect(yaml.Unmarshal([]byte(cm.Data["controller_manager_config.yaml"]), &config)).To(Succeed())
		g.Expect(config.Resources).NotTo(BeNil(), "rendered config has no resources section")
		g.Expect(config.Resources.DeviceClassMappings).To(HaveLen(1), "expected exactly one rendered device class mapping")

		var capacity *kueueconfigapi.DeviceClassCapacitySource
		for _, mapping := range config.Resources.DeviceClassMappings {
			if string(mapping.Name) != ccResourceName {
				continue
			}
			g.Expect(mapping.DeviceClassNames).To(ContainElement(BeEquivalentTo(ccDeviceClassName)))
			for i := range mapping.Sources {
				if mapping.Sources[i].Capacity != nil {
					capacity = mapping.Sources[i].Capacity
				}
			}
		}
		g.Expect(capacity).NotTo(BeNil(), "expected a Capacity source for mapping %q", ccResourceName)
		g.Expect(string(capacity.Name)).To(Equal(ccCapacityDimension))
		g.Expect(capacity.Driver).To(Equal(ccDriverName))
		g.Expect(capacity.DeviceSelector.CEL).NotTo(BeNil(), "Capacity source has no CEL device selector")
		g.Expect(capacity.DeviceSelector.CEL.Expression).To(Equal(ccDeviceSelectorCEL))
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}

// expectConsumableCapacityGateEnabled asserts the operator automatically enabled
// the Consumable Capacity feature gate in the rendered operand config. The gate is
// derived from the Capacity source, never set by the user.
func expectConsumableCapacityGateEnabled(ctx context.Context) {
	By("Verifying the Consumable Capacity feature gate is enabled in the operand config")
	Eventually(func(g Gomega) {
		cm, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		var config kueueconfigapi.Configuration
		g.Expect(yaml.Unmarshal([]byte(cm.Data["controller_manager_config.yaml"]), &config)).To(Succeed())
		g.Expect(config.FeatureGates).To(HaveKeyWithValue(ccFeatureGate, true))
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}

// expectKueueAvailable asserts the Kueue CR reports Available=True and is not
// Degraded because of the Capacity config, i.e. the operator accepted it without
// failing closed. Unrelated Degraded causes (e.g. other missing deps) are ignored.
func expectKueueAvailable(ctx context.Context) {
	By("Verifying the Kueue CR is Available and not Degraded")
	Eventually(func(g Gomega) {
		kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		var available, degraded *operatorv1.OperatorCondition
		for i := range kueueInstance.Status.Conditions {
			c := &kueueInstance.Status.Conditions[i]
			switch c.Type {
			case operatorv1.OperatorStatusTypeAvailable:
				available = c
			case operatorv1.OperatorStatusTypeDegraded:
				degraded = c
			}
		}
		g.Expect(available).NotTo(BeNil(), "expected an Available condition; got: %s", formatConditions(kueueInstance.Status.Conditions))
		g.Expect(available.Status).To(Equal(operatorv1.ConditionTrue))
		// The CR may be Degraded for unrelated reasons (e.g. other missing
		// dependencies); only fail if it is Degraded because of the Capacity config.
		if degraded != nil && degraded.Status == operatorv1.ConditionTrue {
			g.Expect(degraded.Message).NotTo(ContainSubstring(ccMissingDependency))
			g.Expect(degraded.Message).NotTo(ContainSubstring(draMissingDependency))
		}
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}
