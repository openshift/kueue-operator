package testutils

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	kueueclient "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned"

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

func CreateClusterQueue(client *upstreamkueueclient.Clientset) error {
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
	return err
}

func CreateLocalQueue(client *upstreamkueueclient.Clientset, namespace, name string) error {
	lq := &kueuev1beta1.LocalQueue{
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: kueuev1beta1.LocalQueueSpec{ClusterQueue: "test-clusterqueue"},
	}

	_, err := client.KueueV1beta1().LocalQueues(namespace).Create(context.TODO(), lq, v1.CreateOptions{})
	return err
}

func CreateResourceFlavor(client *upstreamkueueclient.Clientset) error {
	rf := &kueuev1beta1.ResourceFlavor{
		ObjectMeta: v1.ObjectMeta{Name: "default"},
		Spec:       kueuev1beta1.ResourceFlavorSpec{},
	}

	_, err := client.KueueV1beta1().ResourceFlavors().Create(context.TODO(), rf, v1.CreateOptions{})
	return err
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

func CreateWorkload(client *upstreamkueueclient.Clientset, namespace, queueName, workloadName string) error {
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
	return err
}

// CleanUpJob deletes the specified Job in the given namespace.
func CleanUpJob(ctx context.Context, kubeClient *kubernetes.Clientset, namespace, name string) {
	By(fmt.Sprintf("Destroying job %s", name))
	backgroundPolicy := metav1.DeletePropagationBackground
	err := kubeClient.BatchV1().Jobs(namespace).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &backgroundPolicy})
	Expect(err).NotTo(HaveOccurred())
}

// CleanUpWorkload deletes the specified Kueue Workload in the given namespace.
func CleanUpWorkload(ctx context.Context, kueueClient *upstreamkueueclient.Clientset, namespace, name string) {
	By(fmt.Sprintf("Destroying Workload %s", name))
	err := kueueClient.KueueV1beta1().Workloads(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	Expect(err).NotTo(HaveOccurred())
}

// CleanUpKueuInstance deletes the specified Kueue instance and waits for its removal.
func CleanUpKueuInstance(ctx context.Context, kueueClientset *kueueclient.Clientset, name string) {
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
}

// CleanUpObject deletes the specified kubernetes object and waits for its removal.
func CleanUpObject(ctx context.Context, kubeClient client.Client, obj client.Object) {
	By(fmt.Sprintf("Destroying Object %s", obj.GetName()))

	err := kubeClient.Delete(ctx, obj)
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
