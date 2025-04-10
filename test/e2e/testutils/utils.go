package testutils

import (
	"context"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	upstreamkueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"
)

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

func CreateLocalQueue(client *upstreamkueueclient.Clientset, namespace string) error {
	lq := &kueuev1beta1.LocalQueue{
		ObjectMeta: v1.ObjectMeta{
			Name:      "test-queue",
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
		Spec: kueuev1beta1.ResourceFlavorSpec{
			NodeLabels: map[string]string{
				"kueue.x-k8s.io/default-flavor": "true",
			},
		},
	}

	_, err := client.KueueV1beta1().ResourceFlavors().Create(context.TODO(), rf, v1.CreateOptions{})
	return err
}

func CreateSimpleWorkloadPod(kubeClient kubernetes.Interface, pod *corev1.Pod) error {
	ctx := context.TODO()
	_, err := kubeClient.CoreV1().Pods(pod.Namespace).Create(ctx, pod, v1.CreateOptions{})
	return err
}

func CreateJob(kubeClient kubernetes.Interface, job *batchv1.Job) error {
	ctx := context.TODO()
	_, err := kubeClient.BatchV1().Jobs(job.Namespace).Create(ctx, job, v1.CreateOptions{})
	return err
}

func MakeCurlMetricsPod(namespace string) *PodWrapper {
	pw := MakeOCPPod("curl-metrics-test", namespace)
	pw.Spec.ServiceAccountName = "kueue-controller-manager"
	pw.Spec.Containers[0].Name = "curl-metrics"
	pw.Spec.Containers[0].Image = "quay.io/openshift/origin-cli:latest"
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

func MakeOCPPod(name, ns string) *PodWrapper {
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
					Image:     "pause",
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
