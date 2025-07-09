package testutils

import (
	"context"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
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
