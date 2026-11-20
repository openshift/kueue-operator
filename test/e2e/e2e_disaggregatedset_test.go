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
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ssv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/utils/ptr"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
	"sigs.k8s.io/yaml"
)

const (
	disaggregatedSetCRDName     = "disaggregatedsets.disaggregatedset.x-k8s.io"
	disaggregatedSetFramework   = "disaggregatedset.x-k8s.io/disaggregatedset"
	disaggregatedSetGate        = "DisaggregatedSetIntegration"
	admissionSchedulingGate     = "kueue.x-k8s.io/admission"
	podSuspendingParentAnnot    = "kueue.x-k8s.io/pod-suspending-parent"
	podSetRequiredTopologyAnnot = "kueue.x-k8s.io/podset-required-topology"
	exclusiveTopologyAnnot      = "leaderworkerset.sigs.k8s.io/exclusive-topology"
	// The CPU quota cut-points of the DisaggregatedSet fixture, which has two
	// slices of 700m CPU each.
	quotaOneRole   = "500m"
	quotaOneSlice  = "1000m"
	quotaTwoSlices = "1400m"
	sliceCPU       = "700m"
)

var _ = Describe("DisaggregatedSet", Label("disaggregatedset"), Ordered, func() {
	var initialKueueInstance *ssv1.Kueue

	BeforeAll(func(ctx context.Context) {
		By("Checking that the DisaggregatedSet API is served")
		crd, err := clients.APIExtClient.CustomResourceDefinitions().Get(ctx, disaggregatedSetCRDName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "the DisaggregatedSet CRD is required for this suite; install a LeaderWorkerSet that serves disaggregatedset.x-k8s.io/v1")
		Expect(slices.ContainsFunc(crd.Spec.Versions, func(v apiextensionsv1.CustomResourceDefinitionVersion) bool {
			return v.Name == "v1" && v.Served
		})).To(BeTrue(), "the DisaggregatedSet CRD does not serve v1")

		kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		initialKueueInstance = kueueInstance.DeepCopy()

		By("Enabling the DisaggregatedSet integration")
		config := kueueInstance.Spec.Config.DeepCopy()
		for _, framework := range []ssv1.KueueIntegration{ssv1.KueueIntegrationBatchJob, ssv1.KueueIntegrationPod, ssv1.KueueIntegrationLeaderWorkerSet, ssv1.KueueIntegrationDisaggregatedSet} {
			if !slices.Contains(config.Integrations.Frameworks, framework) {
				config.Integrations.Frameworks = append(config.Integrations.Frameworks, framework)
			}
		}
		applyKueueConfigAndWaitForRollout(ctx, *config)
	})

	AfterAll(func(ctx context.Context) {
		if initialKueueInstance == nil {
			return
		}
		By("Restoring the initial Kueue configuration")
		applyKueueConfigAndWaitForRollout(ctx, initialKueueInstance.Spec.Config)
	})

	When("the integration is enabled", func() {
		var (
			ns      *corev1.Namespace
			cqName  string
			lqName  string
			builder *testutils.TestResourceBuilder
		)

		BeforeEach(func(ctx context.Context) {
			ns, cqName, lqName = createDisaggregatedSetQueues(ctx, "ds-e2e-", quotaTwoSlices, nil)
			builder = testutils.NewTestResourceBuilder(ns.Name, lqName)
		})

		AfterEach(func(ctx context.Context) {
			if CurrentSpecReport().Failed() {
				dumpDisaggregatedSetDiagnostics(ctx, ns.Name, cqName)
			}
			deleteDisaggregatedSetsAndWait(ctx, ns.Name)
		})

		It("should render the framework, the feature gate and the webhooks and admit the slices", func(ctx context.Context) {
			By("Checking the kueue-manager-config ConfigMap")
			cm, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(cm.Data["controller_manager_config.yaml"]).To(ContainSubstring(disaggregatedSetFramework))
			Expect(cm.Data["controller_manager_config.yaml"]).To(ContainSubstring(disaggregatedSetGate + ": true"))

			By("Checking the webhook configurations")
			mutating, err := kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, "kueue-mutating-webhook-configuration", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			validating, err := kubeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(ctx, "kueue-validating-webhook-configuration", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			for _, name := range []string{"mdisaggregatedset.kb.io", "mpod.kb.io"} {
				idx := slices.IndexFunc(mutating.Webhooks, func(wh admissionregistrationv1.MutatingWebhook) bool { return wh.Name == name })
				Expect(idx).NotTo(Equal(-1), "mutating webhook %s not found", name)
				Expect(requiresManagedNamespace(mutating.Webhooks[idx].NamespaceSelector)).To(BeTrue(), "mutating webhook %s does not require managed namespaces", name)
			}
			for _, name := range []string{"vdisaggregatedset.kb.io", "vpod.kb.io"} {
				idx := slices.IndexFunc(validating.Webhooks, func(wh admissionregistrationv1.ValidatingWebhook) bool { return wh.Name == name })
				Expect(idx).NotTo(Equal(-1), "validating webhook %s not found", name)
				Expect(requiresManagedNamespace(validating.Webhooks[idx].NamespaceSelector)).To(BeTrue(), "validating webhook %s does not require managed namespaces", name)
			}

			By("Checking that the Kueue CR is not degraded")
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			for _, condition := range kueueInstance.Status.Conditions {
				if condition.Type == "Degraded" {
					Expect(string(condition.Status)).NotTo(Equal("True"), "Kueue CR is degraded: %s", condition.Message)
				}
			}

			By("Creating the DisaggregatedSet fixture")
			ds := createDisaggregatedSet(ctx, builder.NewDisaggregatedSet(testutils.DisaggregatedSetOptions{QueueName: lqName}))
			Eventually(func(g Gomega) {
				g.Expect(admittedSliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(Equal(2))
			}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())
		})

		It("should create one Workload per slice with every role of the slice", func(ctx context.Context) {
			ds := createDisaggregatedSet(ctx, builder.NewDisaggregatedSet(testutils.DisaggregatedSetOptions{QueueName: lqName}))
			Eventually(func(g Gomega) {
				workloads := sliceWorkloads(g, ctx, ns.Name, ds.GetUID())
				g.Expect(workloads).To(HaveLen(2))
				for _, wl := range workloads {
					counts := map[string]int32{}
					cpu := resource.Quantity{}
					for _, ps := range wl.Spec.PodSets {
						counts[string(ps.Name)] = ps.Count
						podCPU := ps.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
						for range ps.Count {
							cpu.Add(podCPU)
						}
					}
					g.Expect(counts).To(Equal(map[string]int32{"prefill": 2, "decode-leader": 1, "decode-worker": 1}))
					g.Expect(cpu.Cmp(resource.MustParse(sliceCPU))).To(Equal(0), "slice requests %s CPU", cpu.String())
				}
			}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())
		})

		It("should keep every role of a slice gated when the quota fits only one role", func(ctx context.Context) {
			setClusterQueueCPU(ctx, cqName, quotaOneRole)
			ds := createDisaggregatedSet(ctx, builder.NewDisaggregatedSet(testutils.DisaggregatedSetOptions{QueueName: lqName}))
			Eventually(func(g Gomega) {
				g.Expect(sliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(HaveLen(2))
				g.Expect(disaggregatedSetPods(g, ctx, ns.Name, ds.GetName())).To(HaveLen(8))
			}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())

			Consistently(func(g Gomega) {
				for _, wl := range sliceWorkloads(g, ctx, ns.Name, ds.GetUID()) {
					g.Expect(apimeta.IsStatusConditionTrue(wl.Status.Conditions, kueuev1beta2.WorkloadQuotaReserved)).To(BeFalse(), "Workload %s reserved quota", wl.Name)
				}
				for _, pod := range disaggregatedSetPods(g, ctx, ns.Name, ds.GetName()) {
					g.Expect(hasAdmissionGate(&pod)).To(BeTrue(), "Pod %s is not gated", pod.Name)
				}
				g.Expect(reservedCPU(g, ctx, cqName).IsZero()).To(BeTrue())
			}, testutils.ConsistentlyLongTimeout, testutils.OperatorPoll).Should(Succeed())

			By("Raising the quota to fit both slices")
			setClusterQueueCPU(ctx, cqName, quotaTwoSlices)
			Eventually(func(g Gomega) {
				g.Expect(admittedSliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(Equal(2))
				for _, pod := range disaggregatedSetPods(g, ctx, ns.Name, ds.GetName()) {
					g.Expect(hasAdmissionGate(&pod)).To(BeFalse(), "Pod %s is still gated", pod.Name)
				}
			}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())
		})

		It("should admit only the slices that fit the quota as a whole", func(ctx context.Context) {
			setClusterQueueCPU(ctx, cqName, quotaOneSlice)
			ds := createDisaggregatedSet(ctx, builder.NewDisaggregatedSet(testutils.DisaggregatedSetOptions{QueueName: lqName}))
			Eventually(func(g Gomega) {
				g.Expect(admittedSliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(Equal(1))
				g.Expect(reservedCPU(g, ctx, cqName).Cmp(resource.MustParse(sliceCPU))).To(Equal(0))
			}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())

			Consistently(func(g Gomega) {
				g.Expect(admittedSliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(Equal(1))
				g.Expect(gatedPods(g, ctx, ns.Name, ds.GetName())).To(Equal(4), "every Pod of the slice without quota must stay gated")
			}, testutils.ConsistentlyLongTimeout, testutils.OperatorPoll).Should(Succeed())

			By("Raising the quota to fit both slices")
			setClusterQueueCPU(ctx, cqName, quotaTwoSlices)
			Eventually(func(g Gomega) {
				g.Expect(admittedSliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(Equal(2))
			}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())
		})

		It("should scale the slices and release the quota of removed slices", func(ctx context.Context) {
			ds := createDisaggregatedSet(ctx, builder.NewDisaggregatedSet(testutils.DisaggregatedSetOptions{QueueName: lqName}))
			Eventually(func(g Gomega) {
				g.Expect(admittedSliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(Equal(2))
			}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())

			By("Scaling to three slices")
			patchDisaggregatedSet(ctx, ns.Name, ds.GetName(), `{"spec":{"slices":3}}`)
			Eventually(func(g Gomega) {
				g.Expect(sliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(HaveLen(3))
				g.Expect(admittedSliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(Equal(2))
				g.Expect(gatedPods(g, ctx, ns.Name, ds.GetName())).To(Equal(4))
			}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())

			By("Scaling to one slice")
			patchDisaggregatedSet(ctx, ns.Name, ds.GetName(), `{"spec":{"slices":1}}`)
			Eventually(func(g Gomega) {
				g.Expect(sliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(HaveLen(1))
				g.Expect(reservedCPU(g, ctx, cqName).Cmp(resource.MustParse(sliceCPU))).To(Equal(0))
			}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())
		})

		It("should delete the slice Workloads at zero replicas and admit new ones on scale-up", func(ctx context.Context) {
			ds := createDisaggregatedSet(ctx, builder.NewDisaggregatedSet(testutils.DisaggregatedSetOptions{QueueName: lqName}))
			Eventually(func(g Gomega) {
				g.Expect(admittedSliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(Equal(2))
			}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())

			By("Scaling every role to zero")
			setRoleReplicas(ctx, ns.Name, ds.GetName(), 0, 0)
			Eventually(func(g Gomega) {
				g.Expect(sliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(BeEmpty())
				g.Expect(reservedCPU(g, ctx, cqName).IsZero()).To(BeTrue())
			}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())

			By("Scaling up again after the slice Workloads are deleted")
			setRoleReplicas(ctx, ns.Name, ds.GetName(), 2, 1)
			Eventually(func(g Gomega) {
				g.Expect(admittedSliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(Equal(2))
			}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())
		})

		It("should roll out a new template within the admitted quota", func(ctx context.Context) {
			ds := createDisaggregatedSet(ctx, builder.NewDisaggregatedSet(testutils.DisaggregatedSetOptions{QueueName: lqName}))
			var initial []kueuev1beta2.Workload
			Eventually(func(g Gomega) {
				g.Expect(admittedSliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(Equal(2))
				initial = sliceWorkloads(g, ctx, ns.Name, ds.GetUID())
			}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())

			By("Changing the environment of every role, which keeps the Pod shape")
			current := getDisaggregatedSet(ctx, ns.Name, ds.GetName())
			roles, _, err := unstructured.NestedSlice(current.Object, "spec", "roles")
			Expect(err).NotTo(HaveOccurred())
			for i := range roles {
				for _, template := range []string{"leaderTemplate", "workerTemplate"} {
					containers, found, err := unstructured.NestedSlice(roles[i].(map[string]any), "spec", "leaderWorkerTemplate", template, "spec", "containers")
					Expect(err).NotTo(HaveOccurred())
					if !found {
						continue
					}
					containers[0].(map[string]any)["env"] = []any{map[string]any{"name": "ROLLOUT", "value": "v2"}}
					Expect(unstructured.SetNestedSlice(roles[i].(map[string]any), containers, "spec", "leaderWorkerTemplate", template, "spec", "containers")).To(Succeed())
				}
			}
			Expect(unstructured.SetNestedSlice(current.Object, roles, "spec", "roles")).To(Succeed())
			_, err = disaggregatedSets(ns.Name).Update(ctx, current, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Keep the highest reservation seen while the rollout runs, so a single
			// over-quota observation fails the spec instead of being retried away.
			maxReserved := resource.Quantity{}
			Eventually(func(g Gomega) {
				if reserved := reservedCPU(g, ctx, cqName); reserved.Cmp(maxReserved) > 0 {
					maxReserved = reserved.DeepCopy()
				}
				g.Expect(sliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(HaveLen(2))
				pods := disaggregatedSetPods(g, ctx, ns.Name, ds.GetName())
				g.Expect(pods).To(HaveLen(8))
				for _, pod := range pods {
					g.Expect(pod.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{Name: "ROLLOUT", Value: "v2"}), "Pod %s is not rolled out", pod.Name)
				}
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
			Expect(maxReserved.Cmp(resource.MustParse(quotaTwoSlices))).To(BeNumerically("<=", 0),
				"the rollout reserved %s CPU, more than the %s quota of two slices", maxReserved.String(), quotaTwoSlices)

			By("Checking that the rollout kept the slice Workloads")
			final := sliceWorkloads(Default, ctx, ns.Name, ds.GetUID())
			Expect(workloadNames(final)).To(ConsistOf(workloadNames(initial)))
			for _, wl := range final {
				idx := slices.IndexFunc(initial, func(w kueuev1beta2.Workload) bool { return w.Name == wl.Name })
				if initial[idx].UID != wl.UID {
					// Upstream documents a rare race that recreates a slice Workload
					// with a WorkloadRecreated event; it is a diagnostic, not a failure.
					Expect(hasEvent(ctx, ns.Name, "WorkloadRecreated")).To(BeTrue(), "Workload %s was recreated without a WorkloadRecreated event", wl.Name)
					AddReportEntry("WorkloadRecreated", wl.Name)
				}
			}
		})

		It("should reject unsupported changes to a managed DisaggregatedSet", func(ctx context.Context) {
			By("Rejecting maxSurge above zero")
			ds := builder.NewDisaggregatedSet(testutils.DisaggregatedSetOptions{QueueName: lqName})
			setRoleField(ds, 0, map[string]any{"type": "RollingUpdate", "rollingUpdateConfiguration": map[string]any{"maxSurge": int64(1), "maxUnavailable": int64(1)}}, "spec", "rolloutStrategy")
			expectRejected(ctx, ds)

			By("Rejecting subRoles")
			ds = builder.NewDisaggregatedSet(testutils.DisaggregatedSetOptions{QueueName: lqName})
			setRoleField(ds, 0, []any{map[string]any{"name": "sub", "replicas": int64(1)}}, "subRoles")
			expectRejected(ctx, ds)

			By("Rejecting the queue name in role metadata")
			ds = builder.NewDisaggregatedSet(testutils.DisaggregatedSetOptions{QueueName: lqName})
			setRoleField(ds, 0, map[string]any{"labels": map[string]any{testutils.QueueLabel: lqName}}, "metadata")
			expectRejected(ctx, ds)

			By("Rejecting changes of a running DisaggregatedSet")
			running := createDisaggregatedSet(ctx, builder.NewDisaggregatedSet(testutils.DisaggregatedSetOptions{QueueName: lqName}))
			Eventually(func(g Gomega) {
				g.Expect(admittedSliceWorkloads(g, ctx, ns.Name, running.GetUID())).To(Equal(2))
			}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())
			current := getDisaggregatedSet(ctx, ns.Name, running.GetName())
			setRoleField(current, 0, int64(3), "spec", "replicas")
			_, err := disaggregatedSets(ns.Name).Update(ctx, current, metav1.UpdateOptions{})
			Expect(err).To(HaveOccurred(), "changing the replicas of a running role must be rejected")

			current = getDisaggregatedSet(ctx, ns.Name, running.GetName())
			setRoleField(current, 0, "300m", "spec", "leaderWorkerTemplate", "workerTemplate", "spec", "containers", "0", "resources", "requests", "cpu")
			_, err = disaggregatedSets(ns.Name).Update(ctx, current, metav1.UpdateOptions{})
			Expect(err).To(HaveOccurred(), "changing the resource requests of a role must be rejected")
		})

		It("should not manage a DisaggregatedSet without a queue name", func(ctx context.Context) {
			ds := createDisaggregatedSet(ctx, builder.NewDisaggregatedSet(testutils.DisaggregatedSetOptions{}))
			Consistently(func(g Gomega) {
				g.Expect(sliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(BeEmpty())
			}, testutils.ConsistentlyLongTimeout, testutils.OperatorPoll).Should(Succeed())
		})

		It("should keep the existing integrations working", func(ctx context.Context) {
			job := builder.NewJob()
			job.Labels[testutils.QueueLabel] = lqName
			job.Spec.Template.Spec.Containers[0].Resources.Requests = corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			}
			createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			pod := builder.NewPod()
			pod.Spec.Containers[0].Resources.Requests = corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			}
			createdPod, err := kubeClient.CoreV1().Pods(ns.Name).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			lws := builder.NewLeaderWorkerSet(testutils.LeaderWorkerSetOptions{QueueName: lqName})
			Expect(genericClient.Create(ctx, lws)).To(Succeed())
			DeferCleanup(func(ctx context.Context) {
				testutils.CleanUpObject(ctx, genericClient, lws)
			})

			Eventually(func(g Gomega) {
				for _, owner := range []types.UID{createdJob.UID, createdPod.UID, lws.UID} {
					g.Expect(isOwnedWorkloadAdmitted(g, ctx, ns.Name, owner)).To(BeTrue(), "the Workload of %s is not admitted", owner)
				}
			}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())
		})
	})

	It("should not manage a DisaggregatedSet in a namespace that is not managed", func(ctx context.Context) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "ds-e2e-unmanaged-"}}
		cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cleanupNs)
		DeferCleanup(deleteDisaggregatedSetsAndWait, ns.Name)

		ds := createDisaggregatedSet(ctx, testutils.NewTestResourceBuilder(ns.Name, "ds-lq").NewDisaggregatedSet(testutils.DisaggregatedSetOptions{QueueName: "ds-lq"}))
		Consistently(func(g Gomega) {
			g.Expect(sliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(BeEmpty())
			current, err := disaggregatedSets(ns.Name).Get(ctx, ds.GetName(), metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(strings.Contains(fmt.Sprint(current.Object["spec"]), podSuspendingParentAnnot)).To(BeFalse(), "the DisaggregatedSet was mutated by Kueue")
			for _, pod := range disaggregatedSetPods(g, ctx, ns.Name, ds.GetName()) {
				g.Expect(hasAdmissionGate(&pod)).To(BeFalse(), "Pod %s is gated", pod.Name)
			}
		}, testutils.ConsistentlyLongTimeout, testutils.OperatorPoll).Should(Succeed())
	})

	It("should preempt a whole slice for a higher-priority workload", func(ctx context.Context) {
		low := createWorkloadPriorityClass(ctx, "ds-low-", 100)
		high := createWorkloadPriorityClass(ctx, "ds-high-", 1000)
		ns, _, lqName := createDisaggregatedSetQueues(ctx, "ds-e2e-preempt-", quotaTwoSlices, ptr.To(kueuev1beta2.PreemptionPolicyLowerPriority))
		DeferCleanup(deleteDisaggregatedSetsAndWait, ns.Name)
		builder := testutils.NewTestResourceBuilder(ns.Name, lqName)

		ds := createDisaggregatedSet(ctx, builder.NewDisaggregatedSet(testutils.DisaggregatedSetOptions{QueueName: lqName, WorkloadPriorityClass: low}))
		Eventually(func(g Gomega) {
			g.Expect(admittedSliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(Equal(2))
		}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())

		By("Recording the Pods of both admitted slices before the preemption")
		var originalPods []types.UID
		Eventually(func(g Gomega) {
			originalPods = ungatedPodUIDs(g, ctx, ns.Name, ds.GetName())
			g.Expect(originalPods).To(HaveLen(8), "every Pod of both admitted slices must be ungated before the preemption")
		}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())

		By("Creating a higher-priority Job that needs the quota of one slice")
		job := builder.NewJob()
		job.Labels[testutils.QueueLabel] = lqName
		job.Labels[testutils.WorkloadPriorityClassLabel] = high
		job.Spec.Template.Spec.Containers[0].Resources.Requests = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(sliceCPU),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		}
		createdJob, err := kubeClient.BatchV1().Jobs(ns.Name).Create(ctx, job, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		var evicted, kept string
		Eventually(func(g Gomega) {
			g.Expect(isOwnedWorkloadAdmitted(g, ctx, ns.Name, createdJob.UID)).To(BeTrue())
			evictedCount := 0
			for _, wl := range sliceWorkloads(g, ctx, ns.Name, ds.GetUID()) {
				condition := apimeta.FindStatusCondition(wl.Status.Conditions, kueuev1beta2.WorkloadEvicted)
				if condition != nil && condition.Status == metav1.ConditionTrue && condition.Reason == kueuev1beta2.WorkloadEvictedByPreemption {
					evictedCount++
					evicted = wl.Name
				} else {
					kept = wl.Name
				}
			}
			g.Expect(evictedCount).To(Equal(1), "exactly one slice must be preempted")
		}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())
		GinkgoLogr.Info("Preempted slice", "evicted", evicted, "kept", kept)

		By("Checking that the other slice keeps running")
		// Only the four Pods of the surviving slice stay ungated: the Pods of the
		// preempted slice are deleted or gated again.
		var keptPods []types.UID
		Eventually(func(g Gomega) {
			keptPods = ungatedPodUIDs(g, ctx, ns.Name, ds.GetName())
			g.Expect(keptPods).To(HaveLen(4), "only the Pods of the surviving slice must stay ungated")
			g.Expect(originalPods).To(ContainElements(keptPods), "the surviving slice must keep its original Pods, not recreated ones")
		}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())
		Consistently(func(g Gomega) {
			wl, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(ns.Name).Get(ctx, kept, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(apimeta.IsStatusConditionTrue(wl.Status.Conditions, kueuev1beta2.WorkloadAdmitted)).To(BeTrue())
			g.Expect(ungatedPodUIDs(g, ctx, ns.Name, ds.GetName())).To(Equal(keptPods), "the Pods of the surviving slice must not be recreated")
		}, testutils.ConsistentlyLongTimeout, testutils.OperatorPoll).Should(Succeed())

		By("Re-admitting the preempted slice once the Job is deleted")
		Expect(kubeClient.BatchV1().Jobs(ns.Name).Delete(ctx, createdJob.Name, metav1.DeleteOptions{PropagationPolicy: ptr.To(metav1.DeletePropagationBackground)})).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(admittedSliceWorkloads(g, ctx, ns.Name, ds.GetUID())).To(Equal(2))
		}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())
	})

	It("should place every PodSet of a slice with Topology Aware Scheduling", Label("tas"), func(ctx context.Context) {
		ns, _, lqName := createTASDisaggregatedSetQueues(ctx)
		DeferCleanup(deleteDisaggregatedSetsAndWait, ns.Name)
		ds := testutils.NewTestResourceBuilder(ns.Name, lqName).NewDisaggregatedSet(testutils.DisaggregatedSetOptions{QueueName: lqName, Slices: 1})
		setTemplateAnnotation(ds, podSetRequiredTopologyAnnot, corev1.LabelHostname)
		ds = createDisaggregatedSet(ctx, ds)

		Eventually(func(g Gomega) {
			workloads := sliceWorkloads(g, ctx, ns.Name, ds.GetUID())
			g.Expect(workloads).To(HaveLen(1))
			wl := workloads[0]
			g.Expect(apimeta.IsStatusConditionTrue(wl.Status.Conditions, kueuev1beta2.WorkloadAdmitted)).To(BeTrue())
			g.Expect(wl.Status.Admission).NotTo(BeNil())
			g.Expect(wl.Status.Admission.PodSetAssignments).To(HaveLen(3))
			for _, psa := range wl.Status.Admission.PodSetAssignments {
				g.Expect(psa.TopologyAssignment).NotTo(BeNil(), "PodSet %s has no topology assignment", psa.Name)
			}
		}, testutils.WorkloadTimeout, testutils.OperatorPoll).Should(Succeed())
	})

	It("should reject TAS annotations combined with LeaderWorkerSet placement", Label("tas"), func(ctx context.Context) {
		ns, _, lqName := createDisaggregatedSetQueues(ctx, "ds-e2e-tas-reject-", quotaTwoSlices, nil)
		builder := testutils.NewTestResourceBuilder(ns.Name, lqName)

		By("Rejecting a placement policy other than None")
		ds := builder.NewDisaggregatedSet(testutils.DisaggregatedSetOptions{QueueName: lqName})
		setTemplateAnnotation(ds, podSetRequiredTopologyAnnot, corev1.LabelHostname)
		Expect(unstructured.SetNestedMap(ds.Object, map[string]any{"type": "ExclusiveTopology", "topology": corev1.LabelHostname}, "spec", "placementPolicy")).To(Succeed())
		expectRejected(ctx, ds)

		By("Rejecting the exclusive-topology annotation")
		ds = builder.NewDisaggregatedSet(testutils.DisaggregatedSetOptions{QueueName: lqName})
		setTemplateAnnotation(ds, podSetRequiredTopologyAnnot, corev1.LabelHostname)
		setRoleField(ds, 0, map[string]any{"annotations": map[string]any{exclusiveTopologyAnnot: corev1.LabelHostname}}, "metadata")
		expectRejected(ctx, ds)
	})

	It("should remove the framework, the feature gate and the webhooks on disable", func(ctx context.Context) {
		By("Disabling the integration after every DisaggregatedSet is deleted")
		kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		config := kueueInstance.Spec.Config.DeepCopy()
		config.Integrations.Frameworks = slices.DeleteFunc(config.Integrations.Frameworks, func(f ssv1.KueueIntegration) bool {
			return f == ssv1.KueueIntegrationDisaggregatedSet
		})
		applyKueueConfigAndWaitForRollout(ctx, *config)

		cm, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(cm.Data["controller_manager_config.yaml"]).NotTo(ContainSubstring(disaggregatedSetFramework))
		Expect(cm.Data["controller_manager_config.yaml"]).NotTo(ContainSubstring(disaggregatedSetGate))
		Eventually(func(g Gomega) {
			mutating, err := kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, "kueue-mutating-webhook-configuration", metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(slices.ContainsFunc(mutating.Webhooks, func(wh admissionregistrationv1.MutatingWebhook) bool { return wh.Name == "mdisaggregatedset.kb.io" })).To(BeFalse())
			validating, err := kubeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(ctx, "kueue-validating-webhook-configuration", metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(slices.ContainsFunc(validating.Webhooks, func(wh admissionregistrationv1.ValidatingWebhook) bool { return wh.Name == "vdisaggregatedset.kb.io" })).To(BeFalse())
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			for _, condition := range kueueInstance.Status.Conditions {
				if condition.Type == "Degraded" {
					g.Expect(string(condition.Status)).NotTo(Equal("True"), "Kueue CR is degraded: %s", condition.Message)
				}
			}
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
	})
})

// kueueManagerConfigAnnotation is the Pod template annotation in which the
// operator records the hash of the kueue-manager-config data.
const kueueManagerConfigAnnotation = "configmap/kueue-manager-config"

// applyKueueConfigAndWaitForRollout applies the configuration with applyKueueConfig,
// then waits until the kueue-controller-manager Deployment has rolled out the Pod
// template that carries the rendered manager configuration. The template must
// carry the operator's hash of exactly that ConfigMap data, also when the
// configuration did not change. A Deployment whose old replicas are still ready
// is not enough: their webhooks would serve the tests with the previous
// configuration.
func applyKueueConfigAndWaitForRollout(ctx context.Context, config ssv1.KueueConfiguration) {
	deployments := kubeClient.AppsV1().Deployments(testutils.OperatorNamespace)
	configMaps := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace)

	applyKueueConfig(ctx, config, kubeClient)

	configAfter, err := configMaps.Get(ctx, "kueue-manager-config", metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to get the kueue-manager-config ConfigMap")
	wantConfigHash, err := testutils.ConfigMapDataHash(configAfter.Data)
	Expect(err).NotTo(HaveOccurred(), "failed to hash the kueue-manager-config ConfigMap data")

	By("Waiting for the kueue-controller-manager rollout of the requested configuration")
	Eventually(func(g Gomega) {
		d, err := deployments.Get(ctx, "kueue-controller-manager", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred(), "failed to get the kueue-controller-manager Deployment")
		testutils.ExpectDeploymentRolledOut(g, d, kueueManagerConfigAnnotation, wantConfigHash)
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}

func disaggregatedSets(namespace string) dynamic.ResourceInterface {
	return clients.DynamicClient.Resource(testutils.DisaggregatedSetGVR).Namespace(namespace)
}

func createDisaggregatedSet(ctx context.Context, ds *unstructured.Unstructured) *unstructured.Unstructured {
	created, err := disaggregatedSets(ds.GetNamespace()).Create(ctx, ds, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to create DisaggregatedSet")
	return created
}

func getDisaggregatedSet(ctx context.Context, namespace, name string) *unstructured.Unstructured {
	ds, err := disaggregatedSets(namespace).Get(ctx, name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	return ds
}

func patchDisaggregatedSet(ctx context.Context, namespace, name, patch string) {
	_, err := disaggregatedSets(namespace).Patch(ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	Expect(err).NotTo(HaveOccurred())
}

func expectRejected(ctx context.Context, ds *unstructured.Unstructured) {
	_, err := disaggregatedSets(ds.GetNamespace()).Create(ctx, ds, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
	Expect(err).To(HaveOccurred(), "the DisaggregatedSet must be rejected")
	Expect(err.Error()).To(ContainSubstring("vdisaggregatedset.kb.io"), "the DisaggregatedSet must be rejected by the Kueue webhook")
}

// setRoleField sets a field of the role at the given index. A numeric path
// element selects an item of a list.
func setRoleField(ds *unstructured.Unstructured, role int, value any, path ...string) {
	roles, _, err := unstructured.NestedSlice(ds.Object, "spec", "roles")
	Expect(err).NotTo(HaveOccurred())
	var current any = roles[role]
	for i, field := range path {
		last := i == len(path)-1
		switch node := current.(type) {
		case map[string]any:
			if last {
				node[field] = value
				break
			}
			if _, ok := node[field]; !ok {
				node[field] = map[string]any{}
			}
			current = node[field]
		case []any:
			var idx int
			_, err := fmt.Sscanf(field, "%d", &idx)
			Expect(err).NotTo(HaveOccurred())
			if last {
				node[idx] = value
				break
			}
			current = node[idx]
		}
	}
	Expect(unstructured.SetNestedSlice(ds.Object, roles, "spec", "roles")).To(Succeed())
}

func setTemplateAnnotation(ds *unstructured.Unstructured, key, value string) {
	roles, _, err := unstructured.NestedSlice(ds.Object, "spec", "roles")
	Expect(err).NotTo(HaveOccurred())
	for i := range roles {
		for _, template := range []string{"leaderTemplate", "workerTemplate"} {
			_, found, err := unstructured.NestedMap(roles[i].(map[string]any), "spec", "leaderWorkerTemplate", template)
			Expect(err).NotTo(HaveOccurred(), "role %d has a malformed %s", i, template)
			if !found {
				continue
			}
			Expect(unstructured.SetNestedField(roles[i].(map[string]any), value, "spec", "leaderWorkerTemplate", template, "metadata", "annotations", key)).To(Succeed())
		}
	}
	Expect(unstructured.SetNestedSlice(ds.Object, roles, "spec", "roles")).To(Succeed())
}

func setRoleReplicas(ctx context.Context, namespace, name string, prefill, decode int64) {
	ds := getDisaggregatedSet(ctx, namespace, name)
	setRoleField(ds, 0, prefill, "spec", "replicas")
	setRoleField(ds, 1, decode, "spec", "replicas")
	_, err := disaggregatedSets(namespace).Update(ctx, ds, metav1.UpdateOptions{})
	Expect(err).NotTo(HaveOccurred())
}

func deleteDisaggregatedSetsAndWait(ctx context.Context, namespace string) {
	By("Deleting the DisaggregatedSets and waiting for their Workloads and Pods")
	Expect(disaggregatedSets(namespace).DeleteCollection(ctx, metav1.DeleteOptions{PropagationPolicy: ptr.To(metav1.DeletePropagationForeground)}, metav1.ListOptions{})).To(Succeed())
	Eventually(func(g Gomega) {
		list, err := disaggregatedSets(namespace).List(ctx, metav1.ListOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(list.Items).To(BeEmpty())
		pods, err := kubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: testutils.DisaggregatedSetNameLabel})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(pods.Items).To(BeEmpty())
		workloads, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		for _, wl := range workloads.Items {
			g.Expect(isOwnedByDisaggregatedSet(&wl)).To(BeFalse(), "Workload %s is not deleted", wl.Name)
		}
	}, testutils.DeletionTime, testutils.DeletionPoll).Should(Succeed())
}

// createDisaggregatedSetQueues creates a managed namespace and a ClusterQueue
// and LocalQueue with the given CPU quota and a generous memory quota.
func createDisaggregatedSetQueues(ctx context.Context, prefix, cpu string, preemption *kueuev1beta2.PreemptionPolicy) (*corev1.Namespace, string, string) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		GenerateName: prefix,
		Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
	}}
	cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(cleanupNs)

	rf, cleanupRF, err := testutils.NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, clients.UpstreamKueueClient)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(cleanupRF)

	cqWrapper := testutils.NewClusterQueue().WithGenerateName().WithCPU(cpu).WithMemory("2Gi").WithFlavorName(rf.Name)
	if preemption != nil {
		cqWrapper = cqWrapper.WithPreemption(*preemption)
	}
	cq, cleanupCQ, err := cqWrapper.CreateWithObject(ctx, clients.UpstreamKueueClient)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(cleanupCQ)

	lq, cleanupLQ, err := testutils.NewLocalQueue(ns.Name, "ds-lq").WithClusterQueue(cq.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(cleanupLQ)
	return ns, cq.Name, lq.Name
}

// createTASDisaggregatedSetQueues creates queues whose flavor uses a
// kubernetes.io/hostname topology, so every node is a topology domain.
func createTASDisaggregatedSetQueues(ctx context.Context) (*corev1.Namespace, string, string) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		GenerateName: "ds-e2e-tas-",
		Labels:       map[string]string{testutils.OpenShiftManagedLabel: "true"},
	}}
	cleanupNs, err := testutils.CreateNamespace(kubeClient, ns)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(cleanupNs)

	topology, cleanupTopology, err := testutils.NewTopology().WithGenerateName().WithLevels([]string{corev1.LabelHostname}).CreateWithObject(ctx, clients.UpstreamKueueClient)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(cleanupTopology)

	rf, cleanupRF, err := testutils.NewResourceFlavor().WithGenerateName().WithNodeLabel("node-role.kubernetes.io/worker", "").WithTopologyName(topology.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(cleanupRF)

	cq, cleanupCQ, err := testutils.NewClusterQueue().WithGenerateName().WithCPU(quotaTwoSlices).WithMemory("2Gi").WithFlavorName(rf.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(cleanupCQ)

	lq, cleanupLQ, err := testutils.NewLocalQueue(ns.Name, "ds-tas-lq").WithClusterQueue(cq.Name).CreateWithObject(ctx, clients.UpstreamKueueClient)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(cleanupLQ)
	return ns, cq.Name, lq.Name
}

func createWorkloadPriorityClass(ctx context.Context, prefix string, value int32) string {
	wpc, err := clients.UpstreamKueueClient.KueueV1beta2().WorkloadPriorityClasses().Create(ctx, &kueuev1beta2.WorkloadPriorityClass{
		ObjectMeta: metav1.ObjectMeta{GenerateName: prefix},
		Value:      value,
	}, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func(ctx context.Context) {
		Expect(clients.UpstreamKueueClient.KueueV1beta2().WorkloadPriorityClasses().Delete(ctx, wpc.Name, metav1.DeleteOptions{})).To(Succeed())
	})
	return wpc.Name
}

func setClusterQueueCPU(ctx context.Context, cqName, cpu string) {
	Eventually(func(g Gomega) {
		cq, err := clients.UpstreamKueueClient.KueueV1beta2().ClusterQueues().Get(ctx, cqName, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		for i := range cq.Spec.ResourceGroups[0].Flavors[0].Resources {
			if cq.Spec.ResourceGroups[0].Flavors[0].Resources[i].Name == corev1.ResourceCPU {
				cq.Spec.ResourceGroups[0].Flavors[0].Resources[i].NominalQuota = resource.MustParse(cpu)
			}
		}
		_, err = clients.UpstreamKueueClient.KueueV1beta2().ClusterQueues().Update(ctx, cq, metav1.UpdateOptions{})
		g.Expect(err).NotTo(HaveOccurred())
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed())
}

func reservedCPU(g Gomega, ctx context.Context, cqName string) *resource.Quantity {
	cq, err := clients.UpstreamKueueClient.KueueV1beta2().ClusterQueues().Get(ctx, cqName, metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	total := resource.Quantity{}
	for _, flavor := range cq.Status.FlavorsReservation {
		for _, res := range flavor.Resources {
			if res.Name == corev1.ResourceCPU {
				total.Add(res.Total)
			}
		}
	}
	return &total
}

func isOwnedByDisaggregatedSet(wl *kueuev1beta2.Workload) bool {
	return slices.ContainsFunc(wl.OwnerReferences, func(ref metav1.OwnerReference) bool {
		return ref.Kind == "DisaggregatedSet"
	})
}

func sliceWorkloads(g Gomega, ctx context.Context, namespace string, dsUID types.UID) []kueuev1beta2.Workload {
	list, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	var workloads []kueuev1beta2.Workload
	for _, wl := range list.Items {
		if slices.ContainsFunc(wl.OwnerReferences, func(ref metav1.OwnerReference) bool { return ref.UID == dsUID }) {
			workloads = append(workloads, wl)
		}
	}
	return workloads
}

func admittedSliceWorkloads(g Gomega, ctx context.Context, namespace string, dsUID types.UID) int {
	admitted := 0
	for _, wl := range sliceWorkloads(g, ctx, namespace, dsUID) {
		if apimeta.IsStatusConditionTrue(wl.Status.Conditions, kueuev1beta2.WorkloadAdmitted) {
			admitted++
		}
	}
	return admitted
}

func isOwnedWorkloadAdmitted(g Gomega, ctx context.Context, namespace string, owner types.UID) bool {
	list, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	for _, wl := range list.Items {
		if slices.ContainsFunc(wl.OwnerReferences, func(ref metav1.OwnerReference) bool { return ref.UID == owner }) &&
			apimeta.IsStatusConditionTrue(wl.Status.Conditions, kueuev1beta2.WorkloadAdmitted) {
			return true
		}
	}
	return false
}

func workloadNames(workloads []kueuev1beta2.Workload) []string {
	names := make([]string, 0, len(workloads))
	for _, wl := range workloads {
		names = append(names, wl.Name)
	}
	return names
}

func disaggregatedSetPods(g Gomega, ctx context.Context, namespace, dsName string) []corev1.Pod {
	pods, err := kubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: testutils.DisaggregatedSetNameLabel + "=" + dsName})
	g.Expect(err).NotTo(HaveOccurred())
	live := make([]corev1.Pod, 0, len(pods.Items))
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp.IsZero() {
			live = append(live, pod)
		}
	}
	return live
}

func gatedPods(g Gomega, ctx context.Context, namespace, dsName string) int {
	gated := 0
	for _, pod := range disaggregatedSetPods(g, ctx, namespace, dsName) {
		if hasAdmissionGate(&pod) {
			gated++
		}
	}
	return gated
}

// ungatedPodUIDs returns the sorted UIDs of the live Pods of the DisaggregatedSet
// that no longer have the Kueue admission scheduling gate.
func ungatedPodUIDs(g Gomega, ctx context.Context, namespace, dsName string) []types.UID {
	var uids []types.UID
	for _, pod := range disaggregatedSetPods(g, ctx, namespace, dsName) {
		if !hasAdmissionGate(&pod) {
			uids = append(uids, pod.UID)
		}
	}
	slices.Sort(uids)
	return uids
}

func hasAdmissionGate(pod *corev1.Pod) bool {
	return slices.ContainsFunc(pod.Spec.SchedulingGates, func(gate corev1.PodSchedulingGate) bool {
		return gate.Name == admissionSchedulingGate
	})
}

func hasEvent(ctx context.Context, namespace, reason string) bool {
	events, err := kubeClient.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: "reason=" + reason})
	return err == nil && len(events.Items) > 0
}

func requiresManagedNamespace(selector *metav1.LabelSelector) bool {
	if selector == nil {
		return false
	}
	return slices.ContainsFunc(selector.MatchExpressions, func(req metav1.LabelSelectorRequirement) bool {
		return req.Key == testutils.OpenShiftManagedLabel && req.Operator == metav1.LabelSelectorOpIn && slices.Equal(req.Values, []string{"true"})
	})
}

func dumpDisaggregatedSetDiagnostics(ctx context.Context, namespace, cqName string) {
	dump := func(title string, obj any) {
		out, err := yaml.Marshal(obj)
		if err != nil {
			GinkgoLogr.Error(err, "failed to marshal diagnostics", "title", title)
			return
		}
		GinkgoWriter.Printf("=== %s ===\n%s\n", title, out)
	}
	if list, err := disaggregatedSets(namespace).List(ctx, metav1.ListOptions{}); err != nil {
		GinkgoLogr.Error(err, "failed to list DisaggregatedSets for diagnostics", "namespace", namespace)
	} else {
		dump("DisaggregatedSets", list.Items)
	}
	if list, err := clients.UpstreamKueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{}); err != nil {
		GinkgoLogr.Error(err, "failed to list Workloads for diagnostics", "namespace", namespace)
	} else {
		dump("Workloads", list.Items)
	}
	if pods, err := kubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{}); err != nil {
		GinkgoLogr.Error(err, "failed to list Pods for diagnostics", "namespace", namespace)
	} else {
		for _, pod := range pods.Items {
			GinkgoWriter.Printf("Pod %s owners=%v gates=%v phase=%s\n", pod.Name, pod.OwnerReferences, pod.Spec.SchedulingGates, pod.Status.Phase)
		}
	}
	if cq, err := clients.UpstreamKueueClient.KueueV1beta2().ClusterQueues().Get(ctx, cqName, metav1.GetOptions{}); err != nil {
		GinkgoLogr.Error(err, "failed to get the ClusterQueue for diagnostics", "clusterQueue", cqName)
	} else {
		dump("ClusterQueue status", cq.Status)
	}
	if events, err := kubeClient.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{}); err != nil {
		GinkgoLogr.Error(err, "failed to list Events for diagnostics", "namespace", namespace)
	} else {
		for _, event := range events.Items {
			GinkgoWriter.Printf("Event %s %s/%s: %s\n", event.Reason, event.InvolvedObject.Kind, event.InvolvedObject.Name, event.Message)
		}
	}
	if cm, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(ctx, "kueue-manager-config", metav1.GetOptions{}); err != nil {
		GinkgoLogr.Error(err, "failed to get the kueue-manager-config ConfigMap for diagnostics", "namespace", testutils.OperatorNamespace)
	} else {
		GinkgoWriter.Printf("=== kueue-manager-config ===\n%s\n", cm.Data["controller_manager_config.yaml"])
	}
	testutils.DumpKueueControllerManagerLogs(ctx, kubeClient, 200)
}
