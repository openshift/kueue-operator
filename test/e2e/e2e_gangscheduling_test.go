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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ssv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
)

var _ = Describe("Gangscheduling", Label("gangscheduling"), Ordered, func() {
	var (
		initialKueueInstance *ssv1.Kueue
		labelKey             = testutils.OpenShiftManagedLabel
		labelValue           = trueLabelValue
	)

	When("Policy is ByWorkload and Admission is Sequential", func() {
		BeforeAll(func(ctx context.Context) {
			By("Saving initial Kueue configuration")
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
			initialKueueInstance = kueueInstance.DeepCopy()

			By("Configuring Kueue with gangScheduling: policy=ByWorkload, admission=Sequential")
			newConfig := ssv1.KueueConfiguration{
				Integrations: ssv1.Integrations{
					Frameworks: []ssv1.KueueIntegration{
						ssv1.KueueIntegrationBatchJob,
					},
				},
				GangScheduling: ssv1.GangScheduling{
					Policy: ssv1.GangSchedulingPolicyByWorkload,
					ByWorkload: &ssv1.ByWorkload{
						Admission: ssv1.GangSchedulingWorkloadAdmissionSequential,
					},
				},
			}
			applyKueueConfig(ctx, newConfig, kubeClient)

			By("Waiting for Kueue configuration to be applied")
			Eventually(func(g Gomega) {
				kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
				g.Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
				g.Expect(kueueInstance.Spec.Config.GangScheduling.Policy).To(Equal(ssv1.GangSchedulingPolicyByWorkload))
				g.Expect(kueueInstance.Spec.Config.GangScheduling.ByWorkload).ToNot(BeNil())
				g.Expect(kueueInstance.Spec.Config.GangScheduling.ByWorkload.Admission).To(Equal(ssv1.GangSchedulingWorkloadAdmissionSequential))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Kueue configuration should be applied")
		})

		AfterAll(func(ctx context.Context) {
			By("Restoring initial Kueue configuration")
			applyKueueConfig(ctx, initialKueueInstance.Spec.Config, kubeClient)
		})

		It("should apply all-or-nothing admission for workloads", func(ctx context.Context) {
			By("Creating Cluster Resources")
			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
			DeferCleanup(cleanupResourceFlavor)
			clusterQueue, cleanupClusterQueue, err := testutils.NewClusterQueue().
				WithGenerateName().WithCPU("500m").WithMemory("512Mi").
				WithFlavorName(resourceFlavor.Name).
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue")
			DeferCleanup(cleanupClusterQueue)

			By("Creating Namespaces and LocalQueues")
			namespace, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "gangscheduling-",
					Labels: map[string]string{
						labelKey: labelValue,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				deleteNamespace(ctx, namespace)
			})
			localQueue, cleanupLocalQueue, err := testutils.NewLocalQueue(namespace.Name, "local-queue").WithClusterQueue(clusterQueue.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue")
			DeferCleanup(cleanupLocalQueue)

			By("Admitting a Job that consumes partial quota")
			cleanupJob1, job1, err := createJobGang(ctx, "job-1", namespace.Name, localQueue.Name, "250m", "128Mi", 1)
			Expect(err).NotTo(HaveOccurred(), "Failed to create first job")
			DeferCleanup(cleanupJob1)

			By("Verifying first job workload is created and admitted")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespace.Name, string(job1.UID))

			By("Creating a gang job (parallelism=2) that exceeds remaining quota")
			cleanupJob2, job2, err := createJobGang(ctx, "job-gang", namespace.Name, localQueue.Name, "150m", "128Mi", 2)
			Expect(err).NotTo(HaveOccurred(), "Failed to create gang job")
			DeferCleanup(cleanupJob2)

			By("Verifying the gang job workload is created but NOT admitted (not enough quota for all pods)")
			verifyWorkloadCreatedNotAdmitted(clients.UpstreamKueueClient, namespace.Name, job2.UID)

			// Verify workload stays NOT admitted while first job is running
			Consistently(func() bool {
				workloads, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace.Name).List(ctx, metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred(), "Failed to list workloads")
				for _, wl := range workloads.Items {
					for _, ownerRef := range wl.OwnerReferences {
						if ownerRef.UID == job2.UID {
							return !apimeta.IsStatusConditionTrue(wl.Status.Conditions, kueuev1beta2.WorkloadAdmitted)
						}
					}
				}
				return true
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "Gang job should stay NOT admitted while first job consumes quota")
			// }, 5*time.Second, 1*time.Second).Should(BeTrue(), "Gang job should stay NOT admitted while first job consumes quota")

			By("Waiting for first job to complete and free up quota")
			Eventually(func() error {
				j, err := kubeClient.BatchV1().Jobs(namespace.Name).Get(ctx, job1.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				if j.Status.Succeeded >= 1 || j.Status.CompletionTime != nil {
					return nil
				}
				return fmt.Errorf("job %s not completed yet, succeeded: %d", job1.Name, j.Status.Succeeded)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "First job should complete")

			By("Verifying the second gang job is now admitted after quota is freed (all pods admitted together)")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespace.Name, string(job2.UID))

		})

		It("should admit workloads sequentially even when quota is available", func(ctx context.Context) {

			By("Creating Cluster Resources with enough quota for multiple jobs")
			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
			DeferCleanup(cleanupResourceFlavor)

			clusterQueue, cleanupClusterQueue, err := testutils.NewClusterQueue().
				WithGenerateName().WithCPU("500m").WithMemory("512Mi").
				WithFlavorName(resourceFlavor.Name).
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue")
			DeferCleanup(cleanupClusterQueue)

			By("Creating Namespace and LocalQueue")
			namespace, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "sequential-",
					Labels: map[string]string{
						labelKey: labelValue,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				deleteNamespace(ctx, namespace)
			})

			localQueue, cleanupLocalQueue, err := testutils.NewLocalQueue(namespace.Name, "local-queue").WithClusterQueue(clusterQueue.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue")
			DeferCleanup(cleanupLocalQueue)

			By("Creating the first gang job (parallelism=2) with delayed pod readiness")
			cleanupJob1, createdJob1, err := createJobGang(ctx, "job-sequential-1", namespace.Name, localQueue.Name, "100m", "100Mi", 2)
			Expect(err).NotTo(HaveOccurred(), "Failed to create first gang job")
			DeferCleanup(cleanupJob1)

			By("Verifying first gang job workload is admitted")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespace.Name, string(createdJob1.UID))

			By("Creating the second gang job (parallelism=2) while first gang job pods are not yet ready")
			cleanupJob2, createdJob2, err := createJobGang(ctx, "job-sequential-2", namespace.Name, localQueue.Name, "100m", "100Mi", 2)
			Expect(err).NotTo(HaveOccurred(), "Failed to create second gang job")
			DeferCleanup(cleanupJob2)

			By("Verifying second gang job is NOT admitted because sequential admission waits for first gang job pods to be ready")
			verifyWorkloadCreatedNotAdmitted(clients.UpstreamKueueClient, namespace.Name, createdJob2.UID)

			// Consistently verify workload stays NOT admitted while first gang job pods are not ready
			Consistently(func() bool {
				workloads, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace.Name).List(ctx, metav1.ListOptions{})
				if err != nil {
					return true
				}
				for _, wl := range workloads.Items {
					for _, ownerRef := range wl.OwnerReferences {
						if ownerRef.UID == createdJob2.UID {
							return !apimeta.IsStatusConditionTrue(wl.Status.Conditions, kueuev1beta2.WorkloadAdmitted)
						}
					}
				}
				return true
			}, 5*time.Second, 1*time.Second).Should(BeTrue(), "Second gang job should stay NOT admitted during sequential admission while first job pods are not ready")

			By("Waiting for first gang job pods to complete")
			Eventually(func() bool {
				job, err := kubeClient.BatchV1().Jobs(namespace.Name).Get(ctx, createdJob1.Name, metav1.GetOptions{})
				if err != nil {
					return false
				}
				return job.Status.Succeeded >= 2
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue(), "First gang job pods should complete")

			By("Verifying second gang job is now admitted after first gang job pods became ready")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespace.Name, string(createdJob2.UID))
		})
	})

	When("Policy is ByWorkload and Admission is Parallel", func() {
		BeforeAll(func(ctx context.Context) {
			By("Saving initial Kueue configuration")
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
			initialKueueInstance = kueueInstance.DeepCopy()

			By("Configuring Kueue with gangScheduling: policy=ByWorkload, admission=Parallel")
			newConfig := ssv1.KueueConfiguration{
				Integrations: ssv1.Integrations{
					Frameworks: []ssv1.KueueIntegration{
						ssv1.KueueIntegrationBatchJob,
					},
				},
				GangScheduling: ssv1.GangScheduling{
					Policy: ssv1.GangSchedulingPolicyByWorkload,
					ByWorkload: &ssv1.ByWorkload{
						Admission: ssv1.GangSchedulingWorkloadAdmissionParallel,
					},
				},
			}
			applyKueueConfig(ctx, newConfig, kubeClient)

			By("Waiting for Kueue configuration to be applied")
			Eventually(func(g Gomega) {
				kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
				g.Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
				g.Expect(kueueInstance.Spec.Config.GangScheduling.Policy).To(Equal(ssv1.GangSchedulingPolicyByWorkload))
				g.Expect(kueueInstance.Spec.Config.GangScheduling.ByWorkload).ToNot(BeNil())
				g.Expect(kueueInstance.Spec.Config.GangScheduling.ByWorkload.Admission).To(Equal(ssv1.GangSchedulingWorkloadAdmissionParallel))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "Kueue configuration should be applied")
		})

		AfterAll(func(ctx context.Context) {
			By("Restoring initial Kueue configuration")
			applyKueueConfig(ctx, initialKueueInstance.Spec.Config, kubeClient)
		})

		It("should admit workloads in parallel without waiting for pods to be ready", func(ctx context.Context) {
			By("Creating Cluster Resources with enough quota for multiple jobs")
			resourceFlavor, cleanupResourceFlavor, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
			DeferCleanup(cleanupResourceFlavor)

			clusterQueue, cleanupClusterQueue, err := testutils.NewClusterQueue().
				WithGenerateName().WithCPU("500m").WithMemory("512Mi").
				WithFlavorName(resourceFlavor.Name).
				CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue")
			DeferCleanup(cleanupClusterQueue)

			By("Creating Namespace and LocalQueue")
			namespace, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "parallel-",
					Labels: map[string]string{
						labelKey: labelValue,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				deleteNamespace(ctx, namespace)
			})

			localQueue, cleanupLocalQueue, err := testutils.NewLocalQueue(namespace.Name, "local-queue").WithClusterQueue(clusterQueue.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create local queue")
			DeferCleanup(cleanupLocalQueue)

			By("Creating the first gang job (parallelism=2) with delayed pod readiness")
			cleanupJob1, createdJob1, err := createJobGang(ctx, "job-parallel-1", namespace.Name, localQueue.Name, "100m", "100Mi", 2)
			Expect(err).NotTo(HaveOccurred(), "Failed to create first gang job")
			DeferCleanup(cleanupJob1)

			By("Verifying first gang job workload is admitted")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespace.Name, string(createdJob1.UID))

			By("Creating the second gang job (parallelism=2) while first gang job pods are not yet ready")
			cleanupJob2, createdJob2, err := createJobGang(ctx, "job-parallel-2", namespace.Name, localQueue.Name, "100m", "100Mi", 2)
			Expect(err).NotTo(HaveOccurred(), "Failed to create second gang job")
			DeferCleanup(cleanupJob2)

			By("Verifying second gang job is admitted immediately despite first gang job pods not being ready (parallel admission)")
			verifyWorkloadCreated(clients.UpstreamKueueClient, namespace.Name, string(createdJob2.UID))

			By("Verifying both gang jobs are admitted together without waiting for pod readiness")
			workloads, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace.Name).List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())

			admittedCount := 0
			for _, wl := range workloads.Items {
				if apimeta.IsStatusConditionTrue(wl.Status.Conditions, kueuev1beta2.WorkloadAdmitted) {
					admittedCount++
				}
			}
			Expect(admittedCount).To(Equal(2), "Both gang jobs should be admitted together with parallel admission")
		})
	})

})

// createJobGang creates a job with an init container that delays pod readiness by 10 seconds.
// Parallelism specifies how many pods should run in parallel (for gang scheduling tests).
// CPU and memory specify the resource requests per pod.
func createJobGang(ctx context.Context, name, namespace, queueName, cpu, memory string, parallelism int32) (func(context.Context), *batchv1.Job, error) {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				testutils.QueueLabel: queueName,
			},
		},
		Spec: batchv1.JobSpec{
			Parallelism: ptr.To(parallelism),
			Completions: ptr.To(parallelism),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					InitContainers: []corev1.Container{
						{
							Name:    "delay-ready",
							Image:   "quay.io/prometheus/busybox:latest",
							Command: []string{"sh", "-c", "echo 'Delaying readiness...'; sleep 30"},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "main",
							Image:   "quay.io/prometheus/busybox:latest",
							Command: []string{"sh", "-c", "echo 'Running main container'"},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse(cpu),
									corev1.ResourceMemory: resource.MustParse(memory),
								},
							},
						},
					},
				},
			},
		},
	}

	createdJob, err := kubeClient.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	cleanup := func(ctx context.Context) {
		_ = kubeClient.BatchV1().Jobs(namespace).Delete(ctx, createdJob.Name, metav1.DeleteOptions{})
	}

	return cleanup, createdJob, nil
}
