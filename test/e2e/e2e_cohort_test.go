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
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
)

const (
	cohortCQCPU            = "250m"
	cohortCQMemory         = "256Mi"
	cohortCQBorrowingLimit = "250m"
)

// cohortTestEnv holds the shared ResourceFlavor, cohort hierarchy, ClusterQueues,
// namespaces, and LocalQueues created by setupCohortTestEnv.
type cohortTestEnv struct {
	RootCohort  *kueuev1beta2.Cohort
	OrgEng      *kueuev1beta2.Cohort
	OrgResearch *kueuev1beta2.Cohort
	CQFrontend  *kueuev1beta2.ClusterQueue
	CQML        *kueuev1beta2.ClusterQueue
	NSFrontend  *corev1.Namespace
	NSML        *corev1.Namespace
	LQFrontend  *kueuev1beta2.LocalQueue
	LQML        *kueuev1beta2.LocalQueue
}

var _ = Describe("Hierarchical Cohorts", Label("cohort"), Ordered, func() {
	normalizePolicy := func(p ssv1.PreemptionPolicy) ssv1.PreemptionPolicy {
		if p == "" {
			return ssv1.PreemptionStrategyClassical
		}
		return p
	}

	var initialConfig *ssv1.KueueConfiguration

	BeforeAll(func(ctx context.Context) {
		By("Saving initial Kueue configuration")
		kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to get Kueue instance")
		initialConfig = kueueInstance.Spec.Config.DeepCopy()
	})

	AfterAll(func(ctx context.Context) {
		if initialConfig == nil {
			return
		}
		currentInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		if normalizePolicy(currentInstance.Spec.Config.Preemption.PreemptionPolicy) != normalizePolicy(initialConfig.Preemption.PreemptionPolicy) {
			By("Restoring initial Kueue configuration")
			applyKueueConfig(ctx, *initialConfig, kubeClient)
		}
	})

	setPreemptionPolicy := func(ctx context.Context, policy ssv1.PreemptionPolicy) {
		By(fmt.Sprintf("Setting preemption to %q", policy))
		kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to get Kueue instance")

		currentNormalized := normalizePolicy(kueueInstance.Spec.Config.Preemption.PreemptionPolicy)
		if currentNormalized != normalizePolicy(policy) {
			kueueInstance.Spec.Config.Preemption.PreemptionPolicy = policy
			applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)
		} else {
			By(fmt.Sprintf("Preemption policy is already %q, skipping update", policy))
		}
	}

	When("testing preemption behavior", func() {
		// Topology used by the Classical preemption test:
		//
		//   root (0 extra quota)
		//   ├── org-engineering
		//   │   └── cq-frontend (250m, borrowingLimit: 250m)
		//   └── org-research
		//       └── cq-ml (250m)

		It("should preempt with Classical preemption", func(ctx context.Context) {
			setPreemptionPolicy(ctx, ssv1.PreemptionStrategyClassical)

			env := setupCohortTestEnv(ctx,
				func(cq *testutils.ClusterQueueWrapper) {
					cq.WithReclaimWithinCohort(kueuev1beta2.PreemptionPolicyAny)
				},
				func(cq *testutils.ClusterQueueWrapper) {
					cq.WithReclaimWithinCohort(kueuev1beta2.PreemptionPolicyAny)
				},
			)

			By("Creating 2 long-running jobs on cq-frontend (250m each) — borrows 250m from cq-ml's idle quota")
			job1, err := kubeClient.BatchV1().Jobs(env.NSFrontend.Name).Create(ctx,
				newLongRunningJob("frontend-job-1", env.NSFrontend.Name, env.LQFrontend.Name, "250m", "128Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create frontend-job-1")
			DeferCleanup(func(cleanupCtx context.Context) {
				testutils.CleanUpJob(cleanupCtx, kubeClient, env.NSFrontend.Name, job1.Name)
			})

			job2, err := kubeClient.BatchV1().Jobs(env.NSFrontend.Name).Create(ctx,
				newLongRunningJob("frontend-job-2", env.NSFrontend.Name, env.LQFrontend.Name, "250m", "128Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create frontend-job-2")
			DeferCleanup(func(cleanupCtx context.Context) {
				testutils.CleanUpJob(cleanupCtx, kubeClient, env.NSFrontend.Name, job2.Name)
			})

			By("Verifying both frontend jobs are admitted")
			checkWorkloadCondition(ctx, env.NSFrontend.Name, string(job1.UID), kueuev1beta2.WorkloadAdmitted, "frontend-job-1")
			checkWorkloadCondition(ctx, env.NSFrontend.Name, string(job2.UID), kueuev1beta2.WorkloadAdmitted, "frontend-job-2")

			By("Verifying cq-frontend borrowed 250m CPU")
			verifyBorrowedCPU(ctx, env.CQFrontend.Name, "250m")

			By("Verifying fairSharing status is NOT populated in Classical mode")
			cqStatus, err := clients.UpstreamKueueClient.KueueV1beta2().ClusterQueues().Get(ctx, env.CQFrontend.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(cqStatus.Status.FairSharing).To(BeNil(), "fairSharing status should be nil in Classical mode")

			By("Creating a long-running job on cq-ml (250m) — reclaims its own nominal from cq-frontend")
			mlJob, err := kubeClient.BatchV1().Jobs(env.NSML.Name).Create(ctx,
				newLongRunningJob("ml-job-1", env.NSML.Name, env.LQML.Name, "250m", "128Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create ml-job-1")
			DeferCleanup(func(cleanupCtx context.Context) {
				testutils.CleanUpJob(cleanupCtx, kubeClient, env.NSML.Name, mlJob.Name)
			})

			By("Verifying cq-ml job is admitted (Classical reclaims nominal)")
			checkWorkloadCondition(ctx, env.NSML.Name, string(mlJob.UID), kueuev1beta2.WorkloadAdmitted, "ml-job-1")

			By("Verifying one frontend job was preempted (evicted)")
			Eventually(func() error {
				for _, uid := range []string{string(job1.UID), string(job2.UID)} {
					workloads, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(env.NSFrontend.Name).List(ctx, metav1.ListOptions{
						LabelSelector: fmt.Sprintf("kueue.x-k8s.io/job-uid=%s", uid),
					})
					if err != nil || len(workloads.Items) == 0 {
						continue
					}
					cond := apimeta.FindStatusCondition(workloads.Items[0].Status.Conditions, kueuev1beta2.WorkloadEvicted)
					if cond != nil && cond.Status == metav1.ConditionTrue {
						return nil
					}
				}
				return fmt.Errorf("neither frontend job has been evicted yet")
			}, 3*time.Minute, 2*time.Second).Should(Succeed(), "one frontend job should be evicted via ReclaimWithinCohort")
		})

		// Topology used by the FairSharing preemption test:
		//
		//   root (0 extra quota)
		//   ├── org-engineering
		//   │   └── cq-frontend (250m, borrowingLimit: 250m, weight: 3)
		//   └── org-research
		//       └── cq-ml (250m, weight: 1)

		It("should not preempt with FairSharing when weights protect the borrower", func(ctx context.Context) {
			setPreemptionPolicy(ctx, ssv1.PreemptionStrategyFairsharing)

			env := setupCohortTestEnv(ctx,
				func(cq *testutils.ClusterQueueWrapper) {
					cq.WithFairSharingWeight(3)
				},
				func(cq *testutils.ClusterQueueWrapper) {
					cq.WithFairSharingWeight(1)
				},
			)

			By("Creating 2 long-running jobs on cq-frontend (250m each) — borrows 250m from cq-ml's idle quota")
			job1, err := kubeClient.BatchV1().Jobs(env.NSFrontend.Name).Create(ctx,
				newLongRunningJob("frontend-job-1", env.NSFrontend.Name, env.LQFrontend.Name, "250m", "128Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create frontend-job-1")
			DeferCleanup(func(cleanupCtx context.Context) {
				testutils.CleanUpJob(cleanupCtx, kubeClient, env.NSFrontend.Name, job1.Name)
			})

			job2, err := kubeClient.BatchV1().Jobs(env.NSFrontend.Name).Create(ctx,
				newLongRunningJob("frontend-job-2", env.NSFrontend.Name, env.LQFrontend.Name, "250m", "128Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create frontend-job-2")
			DeferCleanup(func(cleanupCtx context.Context) {
				testutils.CleanUpJob(cleanupCtx, kubeClient, env.NSFrontend.Name, job2.Name)
			})

			By("Verifying both frontend jobs are admitted")
			checkWorkloadCondition(ctx, env.NSFrontend.Name, string(job1.UID), kueuev1beta2.WorkloadAdmitted, "frontend-job-1")
			checkWorkloadCondition(ctx, env.NSFrontend.Name, string(job2.UID), kueuev1beta2.WorkloadAdmitted, "frontend-job-2")

			By("Verifying cq-frontend borrowed 250m CPU")
			verifyBorrowedCPU(ctx, env.CQFrontend.Name, "250m")

			By("Verifying cq-frontend weightedShare reflects borrowing (weight:3, using 500m)")
			cqFrontendStatus, err := clients.UpstreamKueueClient.KueueV1beta2().ClusterQueues().Get(ctx, env.CQFrontend.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(cqFrontendStatus.Status.FairSharing).NotTo(BeNil(), "fairSharing status should be populated")
			Expect(cqFrontendStatus.Status.FairSharing.WeightedShare).To(BeNumerically(">", 0),
				"cq-frontend weightedShare should be > 0 while borrowing")

			By("Verifying cq-ml weightedShare is 0 (no usage yet)")
			cqMLStatus, err := clients.UpstreamKueueClient.KueueV1beta2().ClusterQueues().Get(ctx, env.CQML.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(cqMLStatus.Status.FairSharing).NotTo(BeNil(), "fairSharing status should be populated on cq-ml")
			Expect(cqMLStatus.Status.FairSharing.WeightedShare).To(Equal(int64(0)),
				"cq-ml weightedShare should be 0 with no usage")

			By("Creating a long-running job on cq-ml (250m) — weights should protect cq-frontend from preemption")
			mlJob, err := kubeClient.BatchV1().Jobs(env.NSML.Name).Create(ctx,
				newLongRunningJob("ml-job-1", env.NSML.Name, env.LQML.Name, "250m", "128Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create ml-job-1")
			DeferCleanup(func(cleanupCtx context.Context) {
				testutils.CleanUpJob(cleanupCtx, kubeClient, env.NSML.Name, mlJob.Name)
			})

			By("Waiting for cq-ml to register the pending workload")
			Eventually(func() int32 {
				cq, err := clients.UpstreamKueueClient.KueueV1beta2().ClusterQueues().Get(ctx, env.CQML.Name, metav1.GetOptions{})
				if err != nil {
					return -1
				}
				return cq.Status.PendingWorkloads
			}, 3*time.Minute, 2*time.Second).Should(Equal(int32(1)),
				"cq-ml should have 1 pending workload")

			By("Verifying cq-ml job stays pending — weights protect cq-frontend from preemption")
			Consistently(func() int32 {
				cq, err := clients.UpstreamKueueClient.KueueV1beta2().ClusterQueues().Get(ctx, env.CQML.Name, metav1.GetOptions{})
				if err != nil {
					return -1
				}
				return cq.Status.PendingWorkloads
			}, testutils.ConsistentlyLongTimeout, testutils.ConsistentlyLongPoll).Should(Equal(int32(1)),
				"cq-ml should have 1 pending workload — FairSharing weights prevent preemption")

			By("Verifying cq-frontend still has both jobs admitted (no eviction)")
			cqFrontendFinal, err := clients.UpstreamKueueClient.KueueV1beta2().ClusterQueues().Get(ctx, env.CQFrontend.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(cqFrontendFinal.Status.AdmittedWorkloads).To(Equal(int32(2)),
				"cq-frontend should still have 2 admitted workloads")

			By("Verifying cq-frontend is still borrowing 250m CPU")
			verifyBorrowedCPU(ctx, env.CQFrontend.Name, "250m")
		})
	})

	When("testing metrics observability", func() {
		// Topology used by the metrics test:
		//
		//   root (0 extra quota)
		//   └── org-engineering
		//       └── cq-frontend (250m, borrowingLimit: 250m)
		//
		// FairSharing mode is required — subtree metrics are only emitted
		// when FairSharing is active.

		It("should expose cohort metrics via TLS endpoint", func(ctx context.Context) {
			var (
				podName            = "curl-metrics-test"
				containerName      = "curl-metrics"
				certMountPath      = "/etc/kueue/metrics/certs"
				metricsServiceName = "kueue-controller-manager-metrics-service"
			)

			setPreemptionPolicy(ctx, ssv1.PreemptionStrategyFairsharing)

			env := setupCohortTestEnv(ctx, nil, nil)

			By("Creating a job on cq-frontend to generate metric data")
			job1, err := kubeClient.BatchV1().Jobs(env.NSFrontend.Name).Create(ctx,
				newLongRunningJob("metrics-job-1", env.NSFrontend.Name, env.LQFrontend.Name, "250m", "128Mi"), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(cleanupCtx context.Context) {
				testutils.CleanUpJob(cleanupCtx, kubeClient, env.NSFrontend.Name, job1.Name)
			})

			By("Verifying job is admitted")
			checkWorkloadCondition(ctx, env.NSFrontend.Name, string(job1.UID), kueuev1beta2.WorkloadAdmitted, "metrics-job-1")

			By("Creating curl metrics pod in operator namespace")
			curlPod := testutils.MakeCurlMetricsPod(testutils.OperatorNamespace)
			podCleanupFn, err := testutils.CreatePod(kubeClient, curlPod.Obj())
			Expect(err).NotTo(HaveOccurred(), "failed to create curl metrics pod")
			DeferCleanup(func() { podCleanupFn() })

			By("Waiting for curl pod to be running")
			Eventually(func() error {
				pod, err := kubeClient.CoreV1().Pods(testutils.OperatorNamespace).Get(ctx, podName, metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get pod: %w", err)
				}
				if pod.Status.Phase != corev1.PodRunning {
					return fmt.Errorf("pod %q not ready, phase: %s", podName, pod.Status.Phase)
				}
				return nil
			}, 3*time.Minute, 2*time.Second).Should(Succeed(), "curl pod did not become ready")

			By("Scraping TLS metrics endpoint and verifying cohort metrics")
			Eventually(func() error {
				metricsOutput, _, err := Kexecute(ctx, clients.RestConfig, kubeClient,
					testutils.OperatorNamespace, podName, containerName,
					[]string{
						"/bin/sh", "-c",
						fmt.Sprintf(
							"curl -s --cacert %s/ca.crt -H \"Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)\" https://%s.%s.svc.cluster.local:8443/metrics",
							certMountPath, metricsServiceName, testutils.OperatorNamespace,
						),
					})
				if err != nil {
					return fmt.Errorf("exec into pod failed: %w", err)
				}

				metrics := string(metricsOutput)

				cohortMetrics := []struct {
					name    string
					pattern string
				}{
					{"cohort subtree active workloads", fmt.Sprintf(`kueue_cohort_subtree_admitted_active_workloads{cohort="%s"`, env.OrgEng.Name)},
					{"cohort subtree total workloads", fmt.Sprintf(`kueue_cohort_subtree_admitted_workloads_total{cohort="%s"`, env.OrgEng.Name)},
					{"cohort subtree quota", fmt.Sprintf(`kueue_cohort_subtree_quota{cohort="%s"`, env.OrgEng.Name)},
					{"cohort subtree reservations", fmt.Sprintf(`kueue_cohort_subtree_resource_reservations{cohort="%s"`, env.OrgEng.Name)},
					{"cohort info", fmt.Sprintf(`kueue_cohort_info{cohort="%s",parent_cohort="%s"`, env.OrgEng.Name, env.RootCohort.Name)},
					{"cohort weighted share", fmt.Sprintf(`kueue_cohort_weighted_share{cohort="%s"`, env.OrgEng.Name)},
				}
				for _, m := range cohortMetrics {
					if !strings.Contains(metrics, m.pattern) {
						return fmt.Errorf("%s not found: %s", m.name, m.pattern)
					}
				}

				cqInfo := fmt.Sprintf(`kueue_cluster_queue_info{cluster_queue="%s",parent_cohort="%s"`, env.CQFrontend.Name, env.OrgEng.Name)
				if !strings.Contains(metrics, cqInfo) {
					return fmt.Errorf("CQ info with parent_cohort not found: %s", cqInfo)
				}

				return nil
			}, 3*time.Minute, 2*time.Second).Should(Succeed(), "cohort metrics should be present at TLS endpoint")
		})
	})
})

// setupCohortTestEnv creates a ResourceFlavor, GenerateName cohort hierarchy
// (root → org-eng [→ org-research]), ClusterQueues, namespaces, and LocalQueues.
// All resources are registered with DeferCleanup. Pass mlMod as nil to skip the
// research / cq-ml side.
func setupCohortTestEnv(ctx context.Context,
	frontendMod func(*testutils.ClusterQueueWrapper),
	mlMod func(*testutils.ClusterQueueWrapper),
) *cohortTestEnv {
	env := &cohortTestEnv{}
	kueueClient := clients.UpstreamKueueClient

	By("Creating ResourceFlavor, cohort hierarchy, ClusterQueues, namespaces and LocalQueues")
	resourceFlavor, cleanupRF, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, kueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create resource flavor")
	DeferCleanup(cleanupRF)

	rootCohort, cleanupRoot, err := testutils.NewCohort("").WithGenerateName().CreateWithObject(ctx, kueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create root cohort")
	DeferCleanup(cleanupRoot)
	env.RootCohort = rootCohort

	orgEng, cleanupOrgEng, err := testutils.NewCohort("").WithGenerateName().
		WithParentName(rootCohort.Name).
		CreateWithObject(ctx, kueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create org-engineering cohort")
	DeferCleanup(cleanupOrgEng)
	env.OrgEng = orgEng

	frontendBuilder := testutils.NewClusterQueue().
		WithGenerateName().
		WithCPU(cohortCQCPU).
		WithMemory(cohortCQMemory).
		WithFlavorName(resourceFlavor.Name).
		WithCohort(orgEng.Name).
		WithBorrowingLimit(corev1.ResourceCPU, cohortCQBorrowingLimit)
	if frontendMod != nil {
		frontendMod(frontendBuilder)
	}
	cqFrontend, cleanupCQFrontend, err := frontendBuilder.CreateWithObject(ctx, kueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create cq-frontend")
	DeferCleanup(cleanupCQFrontend)
	env.CQFrontend = cqFrontend

	nsFrontend := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "cohort-frontend-",
			Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
		},
	}
	cleanupNsFrontend, err := testutils.CreateNamespace(kubeClient, nsFrontend)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(cleanupNsFrontend)
	env.NSFrontend = nsFrontend

	lqFrontend, cleanupLQFrontend, err := testutils.NewLocalQueue(nsFrontend.Name, "lq-frontend").
		WithClusterQueue(cqFrontend.Name).
		CreateWithObject(ctx, kueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create lq-frontend")
	DeferCleanup(cleanupLQFrontend)
	env.LQFrontend = lqFrontend

	if mlMod == nil {
		return env
	}

	orgResearch, cleanupOrgRes, err := testutils.NewCohort("").WithGenerateName().
		WithParentName(rootCohort.Name).
		CreateWithObject(ctx, kueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create org-research cohort")
	DeferCleanup(cleanupOrgRes)
	env.OrgResearch = orgResearch

	mlBuilder := testutils.NewClusterQueue().
		WithGenerateName().
		WithCPU(cohortCQCPU).
		WithMemory(cohortCQMemory).
		WithFlavorName(resourceFlavor.Name).
		WithCohort(orgResearch.Name)
	mlMod(mlBuilder)
	cqML, cleanupCQML, err := mlBuilder.CreateWithObject(ctx, kueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create cq-ml")
	DeferCleanup(cleanupCQML)
	env.CQML = cqML

	nsML := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "cohort-ml-",
			Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
		},
	}
	cleanupNsML, err := testutils.CreateNamespace(kubeClient, nsML)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(cleanupNsML)
	env.NSML = nsML

	lqML, cleanupLQML, err := testutils.NewLocalQueue(nsML.Name, "lq-ml").
		WithClusterQueue(cqML.Name).
		CreateWithObject(ctx, kueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create lq-ml")
	DeferCleanup(cleanupLQML)
	env.LQML = lqML

	return env
}

// verifyBorrowedCPU asserts that the named ClusterQueue has borrowed exactly
// the given amount of CPU (e.g. "250m").
func verifyBorrowedCPU(ctx context.Context, clusterQueueName, expectedCPU string) {
	expected := resource.MustParse(expectedCPU)
	Eventually(func() error {
		cq, err := clients.UpstreamKueueClient.KueueV1beta2().ClusterQueues().Get(ctx, clusterQueueName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		for _, flavorUsage := range cq.Status.FlavorsUsage {
			for _, resourceUsage := range flavorUsage.Resources {
				if resourceUsage.Name == corev1.ResourceCPU {
					if resourceUsage.Borrowed.Cmp(expected) == 0 {
						return nil
					}
					return fmt.Errorf("expected borrowed CPU to be %s, got %s", expectedCPU, resourceUsage.Borrowed.String())
				}
			}
		}
		return fmt.Errorf("CPU resource not found in clusterQueue %s status", clusterQueueName)
	}, 3*time.Minute, 2*time.Second).Should(Succeed(), "%s should have borrowed %s CPU", clusterQueueName, expectedCPU)
}
