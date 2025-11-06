package testutils

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	operatorv1 "github.com/openshift/api/operator/v1"
	ssv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	kueueclient "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	upstreamkueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"
)

const (
	defaultImage = "quay.io/openshift/origin-cli:latest"
)

var removeFinalizersMergePatch = []byte(`{"metadata":{"finalizers":[]}}`)

func removeFinalizersWithPatch(patchFn func() error) {
	err := patchFn()
	if err != nil && !apierrors.IsNotFound(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

func GetContainerImageForWorkloads() string {
	if image := os.Getenv("CONTAINER_IMAGE"); image != "" {
		return image
	}
	return defaultImage
}

// PodWrapper wraps a Pod.
type PodWrapper struct {
	corev1.Pod
}

func CreateClusterQueue(client *upstreamkueueclient.Clientset) (func(), error) {
	cq := &kueuev1beta1.ClusterQueue{
		ObjectMeta: v1.ObjectMeta{Name: "test-clusterqueue"},
		Spec: kueuev1beta1.ClusterQueueSpec{
			NamespaceSelector: &v1.LabelSelector{MatchLabels: map[string]string{
				"kueue.openshift.io/managed": "true",
			}},
			ResourceGroups: []kueuev1beta1.ResourceGroup{{
				CoveredResources: []corev1.ResourceName{"cpu", "memory"},
				Flavors: []kueuev1beta1.FlavorQuotas{{
					Name: "default",
					Resources: []kueuev1beta1.ResourceQuota{
						{Name: "cpu", NominalQuota: resource.MustParse("100")},
						{Name: "memory", NominalQuota: resource.MustParse("100Gi")},
					},
				}},
			}},
		},
	}

	_, err := client.KueueV1beta1().ClusterQueues().Create(context.TODO(), cq, v1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	cleanup := func() {
		ctx := context.TODO()
		By(fmt.Sprintf("Destroying ClusterQueue %s", cq.Name))
		removeFinalizersWithPatch(func() error {
			_, err := client.KueueV1beta1().ClusterQueues().Patch(ctx, cq.Name, types.MergePatchType, removeFinalizersMergePatch, metav1.PatchOptions{})
			return err
		})
		err := client.KueueV1beta1().ClusterQueues().Delete(ctx, cq.Name, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			_, err := client.KueueV1beta1().ClusterQueues().Get(ctx, cq.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("clusterqueue %s still exists: %w", cq.Name, err)
		}, DeletionTime, DeletionPoll).Should(Succeed(), fmt.Sprintf("ClusterQueue %s was not cleaned up", cq.Name))
	}

	return cleanup, nil
}

func CreateLocalQueue(client *upstreamkueueclient.Clientset, namespace, name string) (func(), error) {
	lq := &kueuev1beta1.LocalQueue{
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: kueuev1beta1.LocalQueueSpec{ClusterQueue: "test-clusterqueue"},
	}

	_, err := client.KueueV1beta1().LocalQueues(namespace).Create(context.TODO(), lq, v1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	cleanup := func() {
		ctx := context.TODO()
		By(fmt.Sprintf("Destroying LocalQueue %s/%s", namespace, name))
		removeFinalizersWithPatch(func() error {
			_, err := client.KueueV1beta1().LocalQueues(namespace).Patch(ctx, name, types.MergePatchType, removeFinalizersMergePatch, metav1.PatchOptions{})
			return err
		})
		err := client.KueueV1beta1().LocalQueues(namespace).Delete(ctx, name, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			_, err := client.KueueV1beta1().LocalQueues(namespace).Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("localqueue %s/%s still exists: %w", namespace, name, err)
		}, DeletionTime, DeletionPoll).Should(Succeed(), fmt.Sprintf("LocalQueue %s/%s was not cleaned up", namespace, name))
	}

	return cleanup, nil
}

func CreateResourceFlavor(client *upstreamkueueclient.Clientset) (func(), error) {
	rf := &kueuev1beta1.ResourceFlavor{
		ObjectMeta: v1.ObjectMeta{Name: "default"},
		Spec:       kueuev1beta1.ResourceFlavorSpec{},
	}

	_, err := client.KueueV1beta1().ResourceFlavors().Create(context.TODO(), rf, v1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	cleanup := func() {
		ctx := context.TODO()
		By("Destroying ResourceFlavor default")
		removeFinalizersWithPatch(func() error {
			_, err := client.KueueV1beta1().ResourceFlavors().Patch(ctx, rf.Name, types.MergePatchType, removeFinalizersMergePatch, metav1.PatchOptions{})
			return err
		})
		err := client.KueueV1beta1().ResourceFlavors().Delete(ctx, rf.Name, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			_, err := client.KueueV1beta1().ResourceFlavors().Get(ctx, rf.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("resourceflavor %s still exists: %w", rf.Name, err)
		}, DeletionTime, DeletionPoll).Should(Succeed(), fmt.Sprintf("ResourceFlavor %s was not cleaned up", rf.Name))
	}

	return cleanup, nil
}

type KueueWrapper struct {
	*ssv1.Kueue
}

// NewKueueDefault returns a default Kueue instance for testing
func NewKueueDefault() *KueueWrapper {
	return &KueueWrapper{
		&ssv1.Kueue{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster",
				Namespace: OperatorNamespace,
				Labels: map[string]string{
					"app.kubernetes.io/name":       "kueue-operator",
					"app.kubernetes.io/managed-by": "kustomize",
				},
			},
			Spec: ssv1.KueueOperandSpec{
				OperatorSpec: operatorv1.OperatorSpec{
					ManagementState: operatorv1.Managed,
				},
				Config: ssv1.KueueConfiguration{
					Integrations: ssv1.Integrations{
						Frameworks: []ssv1.KueueIntegration{
							ssv1.KueueIntegrationBatchJob,
							ssv1.KueueIntegrationPod,
							ssv1.KueueIntegrationDeployment,
							ssv1.KueueIntegrationStatefulSet,
							ssv1.KueueIntegrationJobSet,
						},
					},
				},
			},
		},
	}
}

func (k *KueueWrapper) EnableDebug() *KueueWrapper {
	k.Kueue.Spec.LogLevel = operatorv1.Debug
	return k
}

func (k *KueueWrapper) GetKueue() *ssv1.Kueue {
	return k.Kueue
}

// DumpKueueControllerManagerLogs dumps the logs from kueue-controller-manager pods
// when a test fails. This should be called from JustAfterEach.
func DumpKueueControllerManagerLogs(ctx context.Context, kubeClient *kubernetes.Clientset, tailLines int64) {
	if !CurrentSpecReport().Failed() {
		return
	}

	By("Test failed - dumping kueue-controller-manager logs")
	pods, err := kubeClient.CoreV1().Pods(OperatorNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "control-plane=controller-manager",
	})
	if err != nil {
		GinkgoWriter.Printf("Failed to list controller pods: %v\n", err)
		return
	}

	if len(pods.Items) == 0 {
		GinkgoWriter.Printf("No kueue-controller-manager pods found\n")
		return
	}

	for _, pod := range pods.Items {
		GinkgoWriter.Printf("\n=== Logs from pod %s ===\n", pod.Name)
		for _, container := range pod.Spec.Containers {
			GinkgoWriter.Printf("\n--- Container: %s ---\n", container.Name)
			req := kubeClient.CoreV1().Pods(OperatorNamespace).GetLogs(pod.Name, &corev1.PodLogOptions{
				Container: container.Name,
				TailLines: &tailLines,
			})
			logs, err := req.Stream(ctx)
			if err != nil {
				GinkgoWriter.Printf("Failed to get logs for container %s: %v\n", container.Name, err)
				continue
			}
			defer logs.Close()

			logBytes, err := io.ReadAll(logs)
			if err != nil {
				GinkgoWriter.Printf("Failed to read logs: %v\n", err)
				continue
			}
			GinkgoWriter.Printf("%s\n", string(logBytes))
		}
	}
}

func MakeCurlMetricsPod(namespace string) *PodWrapper {
	pw := MakePod("curl-metrics-test", namespace)
	pw.Spec.ServiceAccountName = "kueue-controller-manager"
	pw.Spec.Containers[0].Name = "curl-metrics"
	pw.Spec.Containers[0].Image = GetContainerImageForWorkloads()
	pw.Spec.Containers[0].Command = []string{"sleep", "3600"}
	pw.Spec.Volumes = []corev1.Volume{
		{
			Name: "metrics-certs",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: "metrics-server-cert",
					Items:      []corev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}},
				},
			},
		},
	}
	pw.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
		{
			Name:      "metrics-certs",
			MountPath: "/etc/kueue/metrics/certs",
			ReadOnly:  true,
		},
	}
	return pw
}

func MakePod(name, ns string) *PodWrapper {
	return &PodWrapper{corev1.Pod{
		ObjectMeta: v1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Annotations: make(map[string]string, 1),
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:      "c",
					Image:     GetContainerImageForWorkloads(),
					Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{}, Limits: corev1.ResourceList{}},
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: ptr.To(false),
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
				},
			},
			SchedulingGates: make([]corev1.PodSchedulingGate, 0),
		},
	}}
}

// Obj returns the inner Pod.
func (p *PodWrapper) Obj() *corev1.Pod {
	return &p.Pod
}

func CreateWorkload(client *upstreamkueueclient.Clientset, namespace, queueName, workloadName string) (func(), error) {
	workload := &kueuev1beta1.Workload{
		ObjectMeta: v1.ObjectMeta{
			Name:      workloadName,
			Namespace: namespace,
		},
		Spec: kueuev1beta1.WorkloadSpec{
			QueueName: queueName,
			PodSets: []kueuev1beta1.PodSet{
				{
					Name:  "ps1",
					Count: 1,
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "container",
									Image: GetContainerImageForWorkloads(),
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("1Gi"),
										},
									},
								},
							},
							RestartPolicy: corev1.RestartPolicyNever,
						},
					},
				},
			},
		},
	}

	_, err := client.KueueV1beta1().Workloads(namespace).Create(context.TODO(), workload, v1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	cleanup := func() {
		ctx := context.TODO()
		By(fmt.Sprintf("Destroying Workload %s/%s", namespace, workloadName))
		removeFinalizersWithPatch(func() error {
			_, err := client.KueueV1beta1().Workloads(namespace).Patch(ctx, workloadName, types.MergePatchType, removeFinalizersMergePatch, metav1.PatchOptions{})
			return err
		})
		err := client.KueueV1beta1().Workloads(namespace).Delete(ctx, workloadName, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			_, err := client.KueueV1beta1().Workloads(namespace).Get(ctx, workloadName, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("workload %s/%s still exists: %w", namespace, workloadName, err)
		}, DeletionTime, DeletionPoll).Should(Succeed(), fmt.Sprintf("Workload %s/%s was not cleaned up", namespace, workloadName))
	}

	return cleanup, nil
}

func CreateNamespace(kubeClient *kubernetes.Clientset, namespace *corev1.Namespace) (func(), error) {
	ctx := context.TODO()
	By(fmt.Sprintf("Creating namespace %s", namespace.Name))
	_, err := kubeClient.CoreV1().Namespaces().Create(ctx, namespace, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	cleanup := func() {
		By(fmt.Sprintf("Destroying Namespace %s", namespace.Name))
		removeFinalizersWithPatch(func() error {
			_, err := kubeClient.CoreV1().Namespaces().Patch(ctx, namespace.Name, types.MergePatchType, removeFinalizersMergePatch, metav1.PatchOptions{})
			return err
		})
		err := kubeClient.CoreV1().Namespaces().Delete(ctx, namespace.Name, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			_, err := kubeClient.CoreV1().Namespaces().Get(ctx, namespace.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("namespace %s still exists: %w", namespace.Name, err)
		}, DeletionTime, DeletionPoll).Should(Succeed(), fmt.Sprintf("Namespace %s was not cleaned up", namespace.Name))
	}

	return cleanup, nil
}

func CreateJob(kubeClient *kubernetes.Clientset, job *batchv1.Job) (func(), error) {
	ctx := context.TODO()
	By(fmt.Sprintf("Creating Job %s/%s", job.Namespace, job.Name))
	_, err := kubeClient.BatchV1().Jobs(job.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	cleanup := func() {
		By(fmt.Sprintf("Destroying Job %s/%s", job.Namespace, job.Name))
		removeFinalizersWithPatch(func() error {
			_, err := kubeClient.BatchV1().Jobs(job.Namespace).Patch(ctx, job.Name, types.MergePatchType, removeFinalizersMergePatch, metav1.PatchOptions{})
			return err
		})
		err := kubeClient.BatchV1().Jobs(job.Namespace).Delete(ctx, job.Name, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			_, err := kubeClient.BatchV1().Jobs(job.Namespace).Get(ctx, job.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("job %s/%s still exists: %w", job.Namespace, job.Name, err)
		}, DeletionTime, DeletionPoll).Should(Succeed(), fmt.Sprintf("Job %s/%s was not cleaned up", job.Namespace, job.Name))
	}

	return cleanup, nil
}

func CreatePod(kubeClient *kubernetes.Clientset, pod *corev1.Pod) (func(), error) {
	ctx := context.TODO()
	By(fmt.Sprintf("Creating Pod %s/%s", pod.Namespace, pod.Name))
	_, err := kubeClient.CoreV1().Pods(pod.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	cleanup := func() {
		By(fmt.Sprintf("Destroying Pod %s/%s", pod.Namespace, pod.Name))
		removeFinalizersWithPatch(func() error {
			_, err := kubeClient.CoreV1().Pods(pod.Namespace).Patch(ctx, pod.Name, types.MergePatchType, removeFinalizersMergePatch, metav1.PatchOptions{})
			return err
		})
		err := kubeClient.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			_, err := kubeClient.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("pod %s/%s still exists: %w", pod.Namespace, pod.Name, err)
		}, DeletionTime, DeletionPoll).Should(Succeed(), fmt.Sprintf("Pod %s/%s was not cleaned up", pod.Namespace, pod.Name))
	}

	return cleanup, nil
}

// CleanUpJob deletes the specified Job in the given namespace.
func CleanUpJob(ctx context.Context, kubeClient *kubernetes.Clientset, namespace, name string) {
	By(fmt.Sprintf("Destroying job %s", name))
	backgroundPolicy := metav1.DeletePropagationBackground
	removeFinalizersWithPatch(func() error {
		_, err := kubeClient.BatchV1().Jobs(namespace).Patch(ctx, name, types.MergePatchType, removeFinalizersMergePatch, metav1.PatchOptions{})
		return err
	})
	err := kubeClient.BatchV1().Jobs(namespace).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &backgroundPolicy})
	Expect(err).NotTo(HaveOccurred())
}

// CleanUpWorkload deletes the specified Kueue Workload in the given namespace.
func CleanUpWorkload(ctx context.Context, kueueClient *upstreamkueueclient.Clientset, namespace, name string) {
	By(fmt.Sprintf("Destroying Workload %s", name))
	removeFinalizersWithPatch(func() error {
		_, err := kueueClient.KueueV1beta1().Workloads(namespace).Patch(ctx, name, types.MergePatchType, removeFinalizersMergePatch, metav1.PatchOptions{})
		return err
	})
	err := kueueClient.KueueV1beta1().Workloads(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	Expect(err).NotTo(HaveOccurred())
}

// CleanUpKueueInstance deletes the specified Kueue instance and waits for its removal.
// It also waits for webhook endpointslices to be completely gone if kubeClient is provided.
func CleanUpKueueInstance(ctx context.Context, kueueClientset *kueueclient.Clientset, name string, kubeClient *kubernetes.Clientset) {
	By(fmt.Sprintf("Destroying Kueue %s", name))
	err := kueueClientset.KueueV1().Kueues().Delete(ctx, name, metav1.DeleteOptions{})
	Expect(err).NotTo(HaveOccurred())
	Eventually(func() error {
		_, err := kueueClientset.KueueV1().Kueues().Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("Kueue %s still exists", name)
	}, DeletionTime, DeletionPoll).Should(Succeed(), "Resources were not cleaned up properly")

	// Wait for webhook endpointslices to be completely gone if kubeClient is provided
	if kubeClient != nil {
		By("Waiting for webhook endpointslices to be deleted")
		Eventually(func() error {
			endpointSlices, err := kubeClient.DiscoveryV1().EndpointSlices(OperatorNamespace).List(
				ctx,
				metav1.ListOptions{
					LabelSelector: "kubernetes.io/service-name=kueue-webhook-service",
				},
			)
			if err != nil && !apierrors.IsNotFound(err) {
				return err
			}
			if len(endpointSlices.Items) > 0 {
				return fmt.Errorf("webhook endpointslices still exist: %d found", len(endpointSlices.Items))
			}
			return nil
		}, DeletionTime, DeletionPoll).Should(Succeed(), "Webhook endpointslices were not cleaned up")

		By("Waiting for operand deployment to be deleted")
		Eventually(func() error {
			_, err := kubeClient.AppsV1().Deployments(OperatorNamespace).Get(
				ctx,
				"kueue-controller-manager",
				metav1.GetOptions{},
			)
			if apierrors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}
			return fmt.Errorf("operand deployment kueue-controller-manager still exists")
		}, DeletionTime, DeletionPoll).Should(Succeed(), "Operand deployment was not cleaned up")

		By("Waiting for operand pods to be deleted")
		Eventually(func() error {
			pods, err := kubeClient.CoreV1().Pods(OperatorNamespace).List(
				ctx,
				metav1.ListOptions{
					LabelSelector: "control-plane=controller-manager",
				},
			)
			if err != nil && !apierrors.IsNotFound(err) {
				return err
			}
			if len(pods.Items) > 0 {
				return fmt.Errorf("operand pods still exist: %d found", len(pods.Items))
			}
			return nil
		}, DeletionTime, DeletionPoll).Should(Succeed(), "Operand pods were not cleaned up")
	}
}

// CleanUpObject deletes the specified kubernetes object and waits for its removal.
func CleanUpObject(ctx context.Context, kubeClient client.Client, obj client.Object) {
	By(fmt.Sprintf("Destroying Object %s", obj.GetName()))

	objToDelete := obj
	if copiedObj, ok := obj.DeepCopyObject().(client.Object); ok {
		err := kubeClient.Get(ctx, client.ObjectKeyFromObject(obj), copiedObj)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}
		} else {
			if len(copiedObj.GetFinalizers()) > 0 {
				copiedObj.SetFinalizers(nil)
				err = kubeClient.Update(ctx, copiedObj)
				if err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred())
				}
			}
			objToDelete = copiedObj
		}
	}

	err := kubeClient.Delete(ctx, objToDelete)
	Expect(err).NotTo(HaveOccurred())
	Eventually(func() error {
		err := kubeClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("Object %s still exists", obj.GetName())
	}, DeletionTime, DeletionPoll).Should(Succeed(), "Resources were not cleaned up properly")
}

func WaitForAllPodsInNamespaceDeleted(ctx context.Context, c client.Client, ns *corev1.Namespace) {
	pods := corev1.PodList{}
	Eventually(func(g Gomega) {
		g.Expect(c.List(ctx, &pods, client.InNamespace(ns.Name))).Should(Succeed())
		g.Expect(len(pods.Items)).Should(BeZero())
	}, OperatorReadyTime, OperatorPoll).Should(Succeed())
}

func IsPodScheduled(ctx context.Context, kubeClient *kubernetes.Clientset, namespace, podName string) bool {
	pod, err := kubeClient.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == "PodScheduled" && condition.Status == "True" {
			return true
		}
	}
	return false
}

func IsJobSuspended(ctx context.Context, kubeClient *kubernetes.Clientset, namespace, jobName string) bool {
	job, err := kubeClient.BatchV1().Jobs(namespace).Get(context.TODO(), jobName, metav1.GetOptions{})
	if err != nil {
		return false
	}
	for _, condition := range job.Status.Conditions {
		if condition.Type == "Suspended" && condition.Status == "True" {
			return true
		}
	}
	return false
}

func AddLabelAndPatch(ctx context.Context, kubeClient *kubernetes.Clientset, namespace, resourceName, resourceType string) error {
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]string{
				QueueLabel: DefaultLocalQueueName,
			},
		},
	}
	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("error to create JSON patch : %w", err)
	}
	switch resourceType {
	case "job":
		_, err = kubeClient.BatchV1().Jobs(namespace).Patch(ctx, resourceName, types.StrategicMergePatchType, patchData, metav1.PatchOptions{})
	case "pod":
		_, err = kubeClient.CoreV1().Pods(namespace).Patch(ctx, resourceName, types.StrategicMergePatchType, patchData, metav1.PatchOptions{})
	default:
		return fmt.Errorf("resource not supported: %s", resourceType)
	}
	if err != nil {
		return fmt.Errorf("error to apply %s patch on %s: %w", resourceType, resourceName, err)
	}
	return nil
}
