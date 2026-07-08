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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	jobsetapi "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
)

func newTASJob(builder *testutils.TestResourceBuilder, queueName, topologyAnnotationKey, topologyLevel string) *batchv1.Job {
	job := builder.NewJob()
	job.GenerateName = "tas-job-"
	job.Labels[testutils.QueueLabel] = queueName
	job.Spec.Template.ObjectMeta.Annotations = map[string]string{
		topologyAnnotationKey: topologyLevel,
	}
	job.Spec.Template.Spec.Containers[0].Resources.Requests = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("100m"),
		corev1.ResourceMemory: resource.MustParse("64Mi"),
	}
	return job
}

func newTASPod(builder *testutils.TestResourceBuilder, queueName, topologyAnnotationKey, topologyLevel string) *corev1.Pod {
	pod := builder.NewPod()
	pod.GenerateName = "tas-pod-"
	pod.Labels[testutils.QueueLabel] = queueName
	pod.Annotations = map[string]string{
		topologyAnnotationKey: topologyLevel,
	}
	pod.Spec.Containers[0].Resources.Requests = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("100m"),
		corev1.ResourceMemory: resource.MustParse("64Mi"),
	}
	return pod
}

func newTASJobSet(builder *testutils.TestResourceBuilder, queueName, topologyAnnotationKey, topologyLevel string, replicatedJobCount int) *jobsetapi.JobSet {
	jobSet := builder.NewJobSet()
	jobSet.GenerateName = "tas-jobset-"
	jobSet.Labels[testutils.QueueLabel] = queueName

	basePodSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers: []corev1.Container{
			{
				Name:    "test",
				Image:   testutils.GetContainerImageForWorkloads(),
				Command: []string{"sh", "-c", "echo Hello Kueue; sleep 3600"},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("64Mi"),
					},
				},
			},
		},
	}

	var replicatedJobs []jobsetapi.ReplicatedJob
	for i := 0; i < replicatedJobCount; i++ {
		rj := jobsetapi.ReplicatedJob{
			Name:     fmt.Sprintf("rj%d", i+1),
			Replicas: 1,
			Template: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Parallelism: ptr.To[int32](1),
					Completions: ptr.To[int32](1),
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								topologyAnnotationKey: topologyLevel,
							},
						},
						Spec: *basePodSpec.DeepCopy(),
					},
				},
			},
		}
		replicatedJobs = append(replicatedJobs, rj)
	}
	jobSet.Spec.ReplicatedJobs = replicatedJobs
	return jobSet
}

func newTASDeployment(builder *testutils.TestResourceBuilder, queueName, topologyAnnotationKey, topologyLevel string) *appsv1.Deployment {
	deploy := builder.NewDeployment()
	deploy.GenerateName = "tas-deploy-"
	deploy.Labels[testutils.QueueLabel] = queueName
	deploy.Spec.Template.ObjectMeta.Annotations = map[string]string{
		topologyAnnotationKey: topologyLevel,
	}
	deploy.Spec.Template.Spec.Containers[0].Resources.Requests = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("100m"),
		corev1.ResourceMemory: resource.MustParse("64Mi"),
	}
	return deploy
}

var _ = Describe("TopologyAwareScheduling", Label("tas"), func() {

	When("Creating a workload requesting TAS with hostname topology", func() {
		var (
			namespace  *corev1.Namespace
			localQueue *kueuev1beta2.LocalQueue
		)

		BeforeEach(func(ctx context.Context) {
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "tas-test-",
					Labels: map[string]string{
						testutils.OpenShiftManagedLabel: "true",
					},
				},
			}
			var err error
			cleanupNS, err := testutils.CreateNamespace(kubeClient, namespace)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNS)

			nodeCleanup := labelWorkerNodesForHostnameTopology(ctx)
			DeferCleanup(nodeCleanup)

			var cleanupTopo, cleanupRF, cleanupCQ, cleanupLQ func()
			var rf *kueuev1beta2.ResourceFlavor
			_, cleanupTopo, rf, cleanupRF, _, cleanupCQ, localQueue, cleanupLQ = createTASResources(ctx, namespace.Name)
			_ = rf
			DeferCleanup(cleanupTopo)
			DeferCleanup(cleanupRF)
			DeferCleanup(cleanupCQ)
			DeferCleanup(cleanupLQ)
		})

		JustAfterEach(func(ctx context.Context) {
			testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 500)
		})

		It("should admit a Job via TAS", func(ctx context.Context) {
			builder := testutils.NewTestResourceBuilder(namespace.Name, localQueue.Name)
			job := newTASJob(builder, localQueue.Name, kueuev1beta2.PodSetRequiredTopologyAnnotation, corev1.LabelHostname)

			By("Creating the Job")
			createdJob, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				testutils.CleanUpJob(ctx, kubeClient, namespace.Name, createdJob.Name)
			})

			By("Verifying workload is admitted with TopologyAssignment")
			verifyWorkloadAdmittedWithTopology(ctx, namespace.Name, string(createdJob.UID), 1)
		})

		It("should admit a JobSet via TAS", func(ctx context.Context) {
			builder := testutils.NewTestResourceBuilder(namespace.Name, localQueue.Name)
			jobSet := newTASJobSet(builder, localQueue.Name, kueuev1beta2.PodSetRequiredTopologyAnnotation, corev1.LabelHostname, 2)

			By("Creating the JobSet")
			Expect(genericClient.Create(ctx, jobSet)).To(Succeed())
			DeferCleanup(func(ctx context.Context) {
				testutils.CleanUpObject(ctx, genericClient, jobSet)
			})

			By("Waiting for the JobSet to be unsuspended")
			Eventually(func(g Gomega) {
				g.Expect(genericClient.Get(ctx, client.ObjectKeyFromObject(jobSet), jobSet)).To(Succeed())
				g.Expect(jobSet.Spec.Suspend).Should(Equal(ptr.To(false)))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying workload is admitted with TopologyAssignment on both PodSets")
			verifyWorkloadAdmittedWithTopology(ctx, namespace.Name, string(jobSet.UID), 2)
		})

		It("should admit a single Pod via TAS", func(ctx context.Context) {
			builder := testutils.NewTestResourceBuilder(namespace.Name, localQueue.Name)
			pod := newTASPod(builder, localQueue.Name, kueuev1beta2.PodSetRequiredTopologyAnnotation, corev1.LabelHostname)

			By("Creating the Pod")
			createdPod, err := kubeClient.CoreV1().Pods(namespace.Name).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				testutils.CleanUpObject(ctx, genericClient, createdPod)
			})

			By("Verifying scheduling gates are removed and nodeSelector is set")
			Eventually(func(g Gomega) {
				updatedPod, err := kubeClient.CoreV1().Pods(namespace.Name).Get(ctx, createdPod.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(updatedPod.Spec.SchedulingGates).To(BeEmpty())
				g.Expect(updatedPod.Spec.NodeSelector).To(HaveKeyWithValue("instance-type", "on-demand"))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
		})

		It("should admit a Pod group via TAS", func(ctx context.Context) {
			groupName := "tas-group"
			groupSize := 2

			builder := testutils.NewTestResourceBuilder(namespace.Name, localQueue.Name)
			var pods []*corev1.Pod
			for i := 0; i < groupSize; i++ {
				pod := newTASPod(builder, localQueue.Name, kueuev1beta2.PodSetRequiredTopologyAnnotation, corev1.LabelHostname)
				pod.GenerateName = ""
				pod.Name = fmt.Sprintf("%s-%d", groupName, i)
				pod.Labels["kueue.x-k8s.io/pod-group-name"] = groupName
				pod.Annotations["kueue.x-k8s.io/pod-group-total-count"] = fmt.Sprintf("%d", groupSize)
				pods = append(pods, pod)
			}

			By("Creating the Pod group")
			for _, p := range pods {
				createdPod, err := kubeClient.CoreV1().Pods(namespace.Name).Create(ctx, p, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func(ctx context.Context) {
					testutils.CleanUpObject(ctx, genericClient, createdPod)
				})
			}

			By("Verifying all pods are ungated and have nodeSelector set")
			Eventually(func(g Gomega) {
				for _, p := range pods {
					updatedPod, err := kubeClient.CoreV1().Pods(namespace.Name).Get(ctx, p.Name, metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(updatedPod.Spec.SchedulingGates).To(BeEmpty())
					g.Expect(updatedPod.Spec.NodeSelector).To(HaveKeyWithValue("instance-type", "on-demand"))
				}
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
		})

		It("should admit a Deployment via TAS", func(ctx context.Context) {
			builder := testutils.NewTestResourceBuilder(namespace.Name, localQueue.Name)
			deploy := newTASDeployment(builder, localQueue.Name, kueuev1beta2.PodSetRequiredTopologyAnnotation, corev1.LabelHostname)

			By("Creating the Deployment")
			createdDeploy, err := kubeClient.AppsV1().Deployments(namespace.Name).Create(ctx, deploy, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				testutils.CleanUpObject(ctx, genericClient, createdDeploy)
			})

			By("Waiting for Deployment pod to be created")
			var deploymentPod *corev1.Pod
			Eventually(func(g Gomega) {
				pods, err := kubeClient.CoreV1().Pods(namespace.Name).List(ctx, metav1.ListOptions{
					LabelSelector: "app=test-deployment",
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).NotTo(BeEmpty(), "no pods found for deployment")
				deploymentPod = &pods.Items[0]
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying scheduling gates are removed and nodeSelector is set on the Deployment pod")
			Eventually(func(g Gomega) {
				updatedPod, err := kubeClient.CoreV1().Pods(namespace.Name).Get(ctx, deploymentPod.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(updatedPod.Spec.SchedulingGates).To(BeEmpty())
				g.Expect(updatedPod.Spec.NodeSelector).To(HaveKeyWithValue("instance-type", "on-demand"))
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying Deployment is available")
			Eventually(func(g Gomega) {
				d, err := kubeClient.AppsV1().Deployments(namespace.Name).Get(ctx, createdDeploy.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(d.Status.AvailableReplicas).To(Equal(int32(1)), "Deployment not available")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
		})

		It("should admit a multi-replica Deployment via TAS", func(ctx context.Context) {
			builder := testutils.NewTestResourceBuilder(namespace.Name, localQueue.Name)
			deploy := newTASDeployment(builder, localQueue.Name, kueuev1beta2.PodSetRequiredTopologyAnnotation, corev1.LabelHostname)
			deploy.Spec.Replicas = ptr.To[int32](2)

			By("Creating the multi-replica Deployment")
			createdDeploy, err := kubeClient.AppsV1().Deployments(namespace.Name).Create(ctx, deploy, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				testutils.CleanUpObject(ctx, genericClient, createdDeploy)
			})

			By("Waiting for Deployment pods to be created")
			Eventually(func(g Gomega) {
				pods, err := kubeClient.CoreV1().Pods(namespace.Name).List(ctx, metav1.ListOptions{
					LabelSelector: "app=test-deployment",
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pods.Items).To(HaveLen(2), "expected 2 pods for deployment")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying scheduling gates are removed and nodeSelector is set on all Deployment pods")
			Eventually(func(g Gomega) {
				pods, err := kubeClient.CoreV1().Pods(namespace.Name).List(ctx, metav1.ListOptions{
					LabelSelector: "app=test-deployment",
				})
				g.Expect(err).NotTo(HaveOccurred())
				for _, pod := range pods.Items {
					g.Expect(pod.Spec.SchedulingGates).To(BeEmpty(),
						fmt.Sprintf("pod %s still has scheduling gates", pod.Name))
					g.Expect(pod.Spec.NodeSelector).To(HaveKeyWithValue("instance-type", "on-demand"),
						fmt.Sprintf("pod %s missing expected nodeSelector", pod.Name))
				}
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())

			By("Verifying Deployment is fully available")
			Eventually(func(g Gomega) {
				d, err := kubeClient.AppsV1().Deployments(namespace.Name).Get(ctx, createdDeploy.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(d.Status.AvailableReplicas).To(Equal(int32(2)), "Deployment not fully available")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
		})
	})

	When("Using a datacenter topology with block and rack levels", func() {
		var (
			namespace  *corev1.Namespace
			topology   *kueuev1beta2.Topology
			localQueue *kueuev1beta2.LocalQueue
		)

		BeforeEach(func(ctx context.Context) {
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "tas-test-",
					Labels: map[string]string{
						testutils.OpenShiftManagedLabel: "true",
					},
				},
			}
			var err error
			cleanupNS, err := testutils.CreateNamespace(kubeClient, namespace)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNS)

			nodeCleanup := labelWorkerNodesForDatacenterTopology(ctx)
			DeferCleanup(nodeCleanup)

			kueueClient := clients.UpstreamKueueClient

			var cleanupTopo func()
			topology, cleanupTopo, err = testutils.NewTopology().WithGenerateName().
				WithLevels([]string{
					"cloud.provider.com/topology-block",
					"cloud.provider.com/topology-rack",
					corev1.LabelHostname,
				}).
				CreateWithObject(ctx, kueueClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to create datacenter topology")
			DeferCleanup(cleanupTopo)

			rf, cleanupRF, rfErr := testutils.NewResourceFlavor().WithGenerateName().
				WithNodeLabel("cloud.provider.com/node-group", "tas-group").
				WithTopologyName(topology.Name).
				CreateWithObject(ctx, kueueClient)
			Expect(rfErr).NotTo(HaveOccurred(), "Failed to create resource flavor")
			DeferCleanup(cleanupRF)

			cq, cleanupCQ, cqErr := testutils.NewClusterQueue().WithGenerateName().
				WithCPU("2").WithMemory("2Gi").
				WithFlavorName(rf.Name).
				CreateWithObject(ctx, kueueClient)
			Expect(cqErr).NotTo(HaveOccurred(), "Failed to create cluster queue")
			DeferCleanup(cleanupCQ)

			lq, cleanupLQ, lqErr := testutils.NewLocalQueue(namespace.Name, "tas-dc-queue").
				WithClusterQueue(cq.Name).
				CreateWithObject(ctx, kueueClient)
			Expect(lqErr).NotTo(HaveOccurred(), "Failed to create local queue")
			DeferCleanup(cleanupLQ)
			localQueue = lq
		})

		JustAfterEach(func(ctx context.Context) {
			testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 500)
		})

		It("should admit a Job with block-level required topology", func(ctx context.Context) {
			builder := testutils.NewTestResourceBuilder(namespace.Name, localQueue.Name)
			job := newTASJob(builder, localQueue.Name, kueuev1beta2.PodSetRequiredTopologyAnnotation, "cloud.provider.com/topology-block")
			job.GenerateName = "tas-dc-job-"
			job.Spec.Parallelism = ptr.To[int32](2)
			job.Spec.Completions = ptr.To[int32](2)

			By("Creating the Job with block-level topology requirement")
			createdJob, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				testutils.CleanUpJob(ctx, kubeClient, namespace.Name, createdJob.Name)
			})

			By("Verifying workload is admitted with TopologyAssignment")
			// When kubernetes.io/hostname is the leaf level, Kueue optimizes
			// the TopologyAssignment to only include the hostname level.
			verifyWorkloadAdmittedWithTopologyLevels(ctx, namespace.Name, string(createdJob.UID), []string{
				corev1.LabelHostname,
			})
		})

		It("should admit a Job with rack-level preferred topology", func(ctx context.Context) {
			builder := testutils.NewTestResourceBuilder(namespace.Name, localQueue.Name)
			job := newTASJob(builder, localQueue.Name, kueuev1beta2.PodSetPreferredTopologyAnnotation, "cloud.provider.com/topology-rack")
			job.GenerateName = "tas-dc-rack-job-"
			job.Spec.Parallelism = ptr.To[int32](2)
			job.Spec.Completions = ptr.To[int32](2)

			By("Creating the Job with rack-level preferred topology")
			createdJob, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				testutils.CleanUpJob(ctx, kubeClient, namespace.Name, createdJob.Name)
			})

			By("Verifying workload is admitted with TopologyAssignment")
			verifyWorkloadAdmittedWithTopologyLevels(ctx, namespace.Name, string(createdJob.UID), []string{
				corev1.LabelHostname,
			})
		})
	})

	When("Using a two-level datacenter topology (block and rack, no hostname)", func() {
		var (
			namespace  *corev1.Namespace
			localQueue *kueuev1beta2.LocalQueue
		)

		BeforeEach(func(ctx context.Context) {
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "tas-test-",
					Labels: map[string]string{
						testutils.OpenShiftManagedLabel: "true",
					},
				},
			}
			var err error
			cleanupNS, err := testutils.CreateNamespace(kubeClient, namespace)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNS)

			nodeCleanup := labelWorkerNodesForDatacenterTopology(ctx)
			DeferCleanup(nodeCleanup)

			kueueClient := clients.UpstreamKueueClient

			// Two-level topology without hostname: domain assignments will
			// include block and rack values so we can verify co-location.
			topology, cleanupTopo, topoErr := testutils.NewTopology().WithGenerateName().
				WithLevels([]string{
					"cloud.provider.com/topology-block",
					"cloud.provider.com/topology-rack",
				}).
				CreateWithObject(ctx, kueueClient)
			Expect(topoErr).NotTo(HaveOccurred(), "Failed to create two-level topology")
			DeferCleanup(cleanupTopo)

			rf, cleanupRF, rfErr := testutils.NewResourceFlavor().WithGenerateName().
				WithNodeLabel("cloud.provider.com/node-group", "tas-group").
				WithTopologyName(topology.Name).
				CreateWithObject(ctx, kueueClient)
			Expect(rfErr).NotTo(HaveOccurred(), "Failed to create resource flavor")
			DeferCleanup(cleanupRF)

			cq, cleanupCQ, cqErr := testutils.NewClusterQueue().WithGenerateName().
				WithCPU("4").WithMemory("4Gi").
				WithFlavorName(rf.Name).
				CreateWithObject(ctx, kueueClient)
			Expect(cqErr).NotTo(HaveOccurred(), "Failed to create cluster queue")
			DeferCleanup(cleanupCQ)

			lq, cleanupLQ, lqErr := testutils.NewLocalQueue(namespace.Name, "tas-2l-queue").
				WithClusterQueue(cq.Name).
				CreateWithObject(ctx, kueueClient)
			Expect(lqErr).NotTo(HaveOccurred(), "Failed to create local queue")
			DeferCleanup(cleanupLQ)
			localQueue = lq
		})

		JustAfterEach(func(ctx context.Context) {
			testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 500)
		})

		It("should place block-required pods within the same block", func(ctx context.Context) {
			builder := testutils.NewTestResourceBuilder(namespace.Name, localQueue.Name)
			job := newTASJob(builder, localQueue.Name, kueuev1beta2.PodSetRequiredTopologyAnnotation, "cloud.provider.com/topology-block")
			job.GenerateName = "tas-2l-block-job-"
			job.Spec.Parallelism = ptr.To[int32](2)
			job.Spec.Completions = ptr.To[int32](2)

			By("Creating the Job with block-level required topology")
			createdJob, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				testutils.CleanUpJob(ctx, kubeClient, namespace.Name, createdJob.Name)
			})

			By("Verifying workload has block and rack levels in TopologyAssignment")
			verifyWorkloadAdmittedWithTopologyLevels(ctx, namespace.Name, string(createdJob.UID), []string{
				"cloud.provider.com/topology-block",
				"cloud.provider.com/topology-rack",
			})

			By("Verifying all domains share the same block")
			verifyDomainsShareBlockValue(ctx, namespace.Name, string(createdJob.UID))
		})

		It("should place rack-preferred pods within the same rack when possible", func(ctx context.Context) {
			builder := testutils.NewTestResourceBuilder(namespace.Name, localQueue.Name)
			job := newTASJob(builder, localQueue.Name, kueuev1beta2.PodSetPreferredTopologyAnnotation, "cloud.provider.com/topology-rack")
			job.GenerateName = "tas-2l-rack-job-"
			job.Spec.Parallelism = ptr.To[int32](2)
			job.Spec.Completions = ptr.To[int32](2)

			By("Creating the Job with rack-level preferred topology")
			createdJob, err := kubeClient.BatchV1().Jobs(namespace.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				testutils.CleanUpJob(ctx, kubeClient, namespace.Name, createdJob.Name)
			})

			By("Verifying workload has block and rack levels in TopologyAssignment")
			verifyWorkloadAdmittedWithTopologyLevels(ctx, namespace.Name, string(createdJob.UID), []string{
				"cloud.provider.com/topology-block",
				"cloud.provider.com/topology-rack",
			})
		})
	})
})

// labelWorkerNodesForHostnameTopology labels all worker nodes with
// instance-type=on-demand so the hostname ResourceFlavor can match them
// and TAS can discover topology domains. Returns a cleanup function.
func labelWorkerNodesForHostnameTopology(ctx context.Context) func(context.Context) {
	nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: "node-role.kubernetes.io/worker",
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(len(nodes.Items)).To(BeNumerically(">=", 1), "Need at least 1 worker node for hostname topology tests")

	labeledNodes := make([]string, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		patchData := `{"metadata":{"labels":{"instance-type":"on-demand"}}}`
		_, err := kubeClient.CoreV1().Nodes().Patch(ctx, node.Name,
			"application/strategic-merge-patch+json",
			[]byte(patchData), metav1.PatchOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to label node %s", node.Name)
		labeledNodes = append(labeledNodes, node.Name)
	}

	return func(ctx context.Context) {
		By("Removing instance-type label from worker nodes")
		for _, name := range labeledNodes {
			patchData := `{"metadata":{"labels":{"instance-type":null}}}`
			Eventually(func() error {
				_, err := kubeClient.CoreV1().Nodes().Patch(ctx, name,
					"application/strategic-merge-patch+json",
					[]byte(patchData), metav1.PatchOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "failed to remove labels from node %s", name)
		}
	}
}

// labelWorkerNodesForDatacenterTopology labels up to 4 worker nodes with
// datacenter topology labels (block, rack, node-group) and returns a cleanup
// function that removes those labels.
func labelWorkerNodesForDatacenterTopology(ctx context.Context) func(context.Context) {
	nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: "node-role.kubernetes.io/worker",
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(len(nodes.Items)).To(BeNumerically(">=", 2), "Need at least 2 worker nodes for datacenter topology tests")

	type nodeLabel struct {
		nodeName string
		labels   map[string]string
	}
	var labeled []nodeLabel

	assignments := []map[string]string{
		{"cloud.provider.com/topology-block": "b1", "cloud.provider.com/topology-rack": "r1", "cloud.provider.com/node-group": "tas-group"},
		{"cloud.provider.com/topology-block": "b1", "cloud.provider.com/topology-rack": "r2", "cloud.provider.com/node-group": "tas-group"},
		{"cloud.provider.com/topology-block": "b2", "cloud.provider.com/topology-rack": "r1", "cloud.provider.com/node-group": "tas-group"},
		{"cloud.provider.com/topology-block": "b2", "cloud.provider.com/topology-rack": "r2", "cloud.provider.com/node-group": "tas-group"},
	}

	maxNodes := len(nodes.Items)
	if maxNodes > len(assignments) {
		maxNodes = len(assignments)
	}

	for i := 0; i < maxNodes; i++ {
		node := nodes.Items[i]
		patchLabels := assignments[i]

		patchData := fmt.Sprintf(`{"metadata":{"labels":{%s}}}`,
			labelsToJSONFields(patchLabels))
		_, err := kubeClient.CoreV1().Nodes().Patch(ctx, node.Name,
			"application/strategic-merge-patch+json",
			[]byte(patchData), metav1.PatchOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to label node %s", node.Name)
		labeled = append(labeled, nodeLabel{nodeName: node.Name, labels: patchLabels})
	}

	return func(ctx context.Context) {
		By("Removing datacenter topology labels from worker nodes")
		for _, nl := range labeled {
			patchData := fmt.Sprintf(`{"metadata":{"labels":{%s}}}`,
				labelsToJSONNullFields(nl.labels))
			Eventually(func() error {
				_, err := kubeClient.CoreV1().Nodes().Patch(ctx, nl.nodeName,
					"application/strategic-merge-patch+json",
					[]byte(patchData), metav1.PatchOptions{})
				return err
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "failed to remove labels from node %s", nl.nodeName)
		}
	}
}

func labelsToJSONFields(labels map[string]string) string {
	result := ""
	for k, v := range labels {
		if result != "" {
			result += ","
		}
		result += fmt.Sprintf(`"%s":"%s"`, k, v)
	}
	return result
}

func labelsToJSONNullFields(labels map[string]string) string {
	result := ""
	for k := range labels {
		if result != "" {
			result += ","
		}
		result += fmt.Sprintf(`"%s":null`, k)
	}
	return result
}

// createTASResources creates the shared Kueue resources needed for TAS tests:
// Topology, ResourceFlavor (with topology), ClusterQueue, and LocalQueue.
func createTASResources(ctx context.Context, namespaceName string) (
	*kueuev1beta2.Topology, func(),
	*kueuev1beta2.ResourceFlavor, func(),
	*kueuev1beta2.ClusterQueue, func(),
	*kueuev1beta2.LocalQueue, func(),
) {
	kueueClient := clients.UpstreamKueueClient

	topology, cleanupTopo, err := testutils.NewTopology().WithGenerateName().
		CreateWithObject(ctx, kueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create topology")

	rf, cleanupRF, err := testutils.NewResourceFlavor().WithGenerateName().
		WithNodeLabel("instance-type", "on-demand").
		WithTopologyName(topology.Name).
		CreateWithObject(ctx, kueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")

	cq, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().
		WithCPU("1").WithMemory("1Gi").
		WithFlavorName(rf.Name).
		CreateWithObject(ctx, kueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create cluster queue")

	lq, cleanupLQ, err := testutils.NewLocalQueue(namespaceName, "tas-local-queue").
		WithClusterQueue(cq.Name).
		CreateWithObject(ctx, kueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create local queue")

	return topology, cleanupTopo, rf, cleanupRF, cq, cleanupCQ, lq, cleanupLQ
}

// verifyWorkloadAdmittedWithTopologyLevels checks that a workload for the given job UID
// is admitted with a single TopologyAssignment whose levels match the expected levels.
func verifyWorkloadAdmittedWithTopologyLevels(ctx context.Context, namespace, jobUID string, expectedLevels []string) {
	kueueClient := clients.UpstreamKueueClient

	Eventually(func(g Gomega) {
		workloads, err := kueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{})
		g.Expect(err).NotTo(HaveOccurred())

		var found *kueuev1beta2.Workload
		for i := range workloads.Items {
			wl := &workloads.Items[i]
			for _, ownerRef := range wl.OwnerReferences {
				if string(ownerRef.UID) == jobUID {
					found = wl
					break
				}
			}
			if found != nil {
				break
			}
		}
		g.Expect(found).NotTo(BeNil(), "workload not found for job UID %s", jobUID)
		g.Expect(found.Status.Admission).NotTo(BeNil(), "workload not admitted")
		g.Expect(found.Status.Admission.PodSetAssignments).To(HaveLen(1))

		for i, psa := range found.Status.Admission.PodSetAssignments {
			g.Expect(psa.TopologyAssignment).NotTo(BeNil(),
				fmt.Sprintf("PodSetAssignment[%d] has no TopologyAssignment", i))
			g.Expect(psa.TopologyAssignment.Levels).To(Equal(expectedLevels),
				fmt.Sprintf("PodSetAssignment[%d] TopologyAssignment has unexpected levels", i))
			g.Expect(psa.TopologyAssignment.Slices).NotTo(BeEmpty(),
				fmt.Sprintf("PodSetAssignment[%d] TopologyAssignment has no slices", i))
		}
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}

// verifyDomainsShareBlockValue checks that all domains in the first PodSetAssignment's
// TopologyAssignment share the same block value (index 0 in the levels).
// This is only meaningful for topologies where block is in the levels (e.g., 2-level [block, rack]).
func verifyDomainsShareBlockValue(ctx context.Context, namespace, jobUID string) {
	kueueClient := clients.UpstreamKueueClient

	Eventually(func(g Gomega) {
		workloads, err := kueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{})
		g.Expect(err).NotTo(HaveOccurred())

		var found *kueuev1beta2.Workload
		for i := range workloads.Items {
			wl := &workloads.Items[i]
			for _, ownerRef := range wl.OwnerReferences {
				if string(ownerRef.UID) == jobUID {
					found = wl
					break
				}
			}
			if found != nil {
				break
			}
		}
		g.Expect(found).NotTo(BeNil(), "workload not found for job UID %s", jobUID)
		g.Expect(found.Status.Admission).NotTo(BeNil(), "workload not admitted")
		g.Expect(found.Status.Admission.PodSetAssignments).NotTo(BeEmpty())

		ta := found.Status.Admission.PodSetAssignments[0].TopologyAssignment
		g.Expect(ta).NotTo(BeNil())
		g.Expect(ta.Slices).NotTo(BeEmpty())

		// Collect all block values (level index 0) across all slices.
		blockValues := map[string]bool{}
		for _, slice := range ta.Slices {
			g.Expect(slice.ValuesPerLevel).NotTo(BeEmpty(),
				"slice has no valuesPerLevel")
			blockLevel := slice.ValuesPerLevel[0]
			if blockLevel.Universal != nil {
				blockValues[*blockLevel.Universal] = true
			} else if blockLevel.Individual != nil {
				for _, root := range blockLevel.Individual.Roots {
					blockValues[root] = true
				}
			}
		}
		g.Expect(blockValues).To(HaveLen(1),
			fmt.Sprintf("expected all domains to share one block value, got: %v", blockValues))
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}

// verifyWorkloadAdmittedWithTopology checks that a workload for the given job UID
// is admitted and has TopologyAssignment set on the expected number of PodSetAssignments.
func verifyWorkloadAdmittedWithTopology(ctx context.Context, namespace, jobUID string, expectedPodSets int) {
	kueueClient := clients.UpstreamKueueClient

	Eventually(func(g Gomega) {
		workloads, err := kueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{})
		g.Expect(err).NotTo(HaveOccurred())

		var found *kueuev1beta2.Workload
		for i := range workloads.Items {
			wl := &workloads.Items[i]
			for _, ownerRef := range wl.OwnerReferences {
				if string(ownerRef.UID) == jobUID {
					found = wl
					break
				}
			}
			if found != nil {
				break
			}
		}
		g.Expect(found).NotTo(BeNil(), "workload not found for job UID %s", jobUID)
		g.Expect(found.Status.Admission).NotTo(BeNil(), "workload not admitted")
		g.Expect(found.Status.Admission.PodSetAssignments).To(HaveLen(expectedPodSets))

		for i, psa := range found.Status.Admission.PodSetAssignments {
			g.Expect(psa.TopologyAssignment).NotTo(BeNil(),
				fmt.Sprintf("PodSetAssignment[%d] has no TopologyAssignment", i))
			g.Expect(psa.TopologyAssignment.Levels).NotTo(BeEmpty(),
				fmt.Sprintf("PodSetAssignment[%d] TopologyAssignment has no levels", i))
			g.Expect(psa.TopologyAssignment.Slices).NotTo(BeEmpty(),
				fmt.Sprintf("PodSetAssignment[%d] TopologyAssignment has no slices", i))
		}
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}
