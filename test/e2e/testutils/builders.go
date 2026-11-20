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

package testutils

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	jobsetapi "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	lwsapi "sigs.k8s.io/lws/api/leaderworkerset/v1"
)

type TestResourceBuilder struct {
	namespace      string
	queueName      string
	containerImage string
}

func NewTestResourceBuilder(namespace, queueName string) *TestResourceBuilder {
	return &TestResourceBuilder{
		namespace:      namespace,
		queueName:      queueName,
		containerImage: GetContainerImageForWorkloads(),
	}
}

func (b *TestResourceBuilder) NewPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-pod-",
			Namespace:    b.namespace,
			Labels: map[string]string{
				"kueue.x-k8s.io/queue-name": b.queueName,
			},
		},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: ptr.To(int64(1)),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: ptr.To(true),
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "test-container",
					Image:   b.containerImage,
					Command: []string{"sh", "-c", "echo Hello Kueue; sleep 3600"},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("200m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
					},
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: ptr.To(false),
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
					},
				},
			},
		},
	}
}

func (b *TestResourceBuilder) NewJob() *batchv1.Job {
	labels := map[string]string{}
	if b.namespace == "kueue-managed-test" {
		labels["kueue.x-k8s.io/queue-name"] = b.queueName
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-job-",
			Namespace:    b.namespace,
			Labels:       labels,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: *b.NewPod().Spec.DeepCopy(),
			},
		},
	}
}

func (b *TestResourceBuilder) NewStatefulSet() *appsv1.StatefulSet {
	var replicas int32 = 1
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-ss-",
			Namespace:    b.namespace,
			Labels: map[string]string{
				"kueue.x-k8s.io/queue-name": b.queueName,
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To(replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test-statefulset"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test-statefulset"},
				},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: ptr.To(int64(1)),
					Containers: []corev1.Container{
						{
							Name:    "test-container",
							Image:   b.containerImage,
							Command: []string{"sh", "-c", "sleep 3600"},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("200m"),
									corev1.ResourceMemory: resource.MustParse("512Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptr.To(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (b *TestResourceBuilder) NewDeployment() *appsv1.Deployment {
	var replicas int32 = 1
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-deploy-",
			Namespace:    b.namespace,
			Labels: map[string]string{
				"kueue.x-k8s.io/queue-name": b.queueName,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test-deployment"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test-deployment"},
				},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: ptr.To(int64(1)),
					Containers: []corev1.Container{
						{
							Name:    "test-container",
							Image:   b.containerImage,
							Command: []string{"sh", "-c", "sleep 3600"},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("200m"),
									corev1.ResourceMemory: resource.MustParse("512Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptr.To(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (b *TestResourceBuilder) NewPodWithoutQueue() *corev1.Pod {
	pod := b.NewPod()
	delete(pod.Labels, "kueue.x-k8s.io/queue-name")
	return pod
}

func (b *TestResourceBuilder) NewJobWithoutQueue() *batchv1.Job {
	job := b.NewJob()
	if job.Labels != nil {
		delete(job.Labels, "kueue.x-k8s.io/queue-name")
	}
	return job
}

func (b *TestResourceBuilder) NewJobSetWithoutQueue() *jobsetapi.JobSet {
	jobSet := b.NewJobSet()
	if jobSet.Labels != nil {
		delete(jobSet.Labels, "kueue.x-k8s.io/queue-name")
	}
	return jobSet
}
func (b *TestResourceBuilder) NewJobSet() *jobsetapi.JobSet {
	jobSet := &jobsetapi.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-jobset-",
			Namespace:    b.namespace,
			Labels: map[string]string{
				"kueue.x-k8s.io/queue-name": b.queueName,
			},
		},
		Spec: jobsetapi.JobSetSpec{
			Suspend: ptr.To(false),
			ReplicatedJobs: []jobsetapi.ReplicatedJob{
				{
					Name:     "test-jobset",
					Replicas: 1,
					Template: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									Containers: []corev1.Container{
										{
											Name:  "test-jobset",
											Image: "busybox",
											Args:  []string{"sleep", "10s"},
											Resources: corev1.ResourceRequirements{
												Requests: corev1.ResourceList{
													corev1.ResourceCPU: resource.MustParse("50m")},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	return jobSet
}
func (b *TestResourceBuilder) NewDeploymentWithoutQueue() *appsv1.Deployment {
	deploy := b.NewDeployment()
	delete(deploy.Labels, "kueue.x-k8s.io/queue-name")
	return deploy
}

func (b *TestResourceBuilder) NewStatefulSetWithoutQueue() *appsv1.StatefulSet {
	ss := b.NewStatefulSet()
	delete(ss.Labels, "kueue.x-k8s.io/queue-name")
	return ss
}

type LeaderWorkerSetOptions struct {
	// QueueName: if non-empty, adds the kueue.x-k8s.io/queue-name label
	QueueName string
	// PriorityClassName: if non-empty, sets the priorityClassName in the leader template spec
	PriorityClassName string
	// Size: defaults to 2 if <= 0
	Size int
}

// NewLeaderWorkerSet creates a LeaderWorkerSet with optional queue name, priority class, and size.
func (b *TestResourceBuilder) NewLeaderWorkerSet(opts LeaderWorkerSetOptions) *lwsapi.LeaderWorkerSet {
	size := opts.Size
	if size <= 0 {
		size = 2
	}

	lws := &lwsapi.LeaderWorkerSet{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-lws-",
			Namespace:    b.namespace,
		},
		Spec: lwsapi.LeaderWorkerSetSpec{
			Replicas:      ptr.To(int32(1)),
			StartupPolicy: lwsapi.LeaderCreatedStartupPolicy,
			LeaderWorkerTemplate: lwsapi.LeaderWorkerTemplate{
				Size: ptr.To(int32(size)),
				LeaderTemplate: &corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						TerminationGracePeriodSeconds: ptr.To(int64(1)),
						Containers: []corev1.Container{
							{
								Name:  "leader",
								Image: b.containerImage,
								Command: []string{
									"sh", "-c", "sleep 3600",
								},
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("100m"),
										corev1.ResourceMemory: resource.MustParse("128Mi"),
									},
								},
								SecurityContext: &corev1.SecurityContext{
									AllowPrivilegeEscalation: ptr.To(false),
									Capabilities: &corev1.Capabilities{
										Drop: []corev1.Capability{"ALL"},
									},
								},
							},
						},
					},
				},
				WorkerTemplate: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						TerminationGracePeriodSeconds: ptr.To(int64(1)),
						Containers: []corev1.Container{
							{
								Name:  "worker",
								Image: b.containerImage,
								Command: []string{
									"sh", "-c", "sleep 3600",
								},
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("100m"),
										corev1.ResourceMemory: resource.MustParse("128Mi"),
									},
								},
								SecurityContext: &corev1.SecurityContext{
									AllowPrivilegeEscalation: ptr.To(false),
									Capabilities: &corev1.Capabilities{
										Drop: []corev1.Capability{"ALL"},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Add queue label if provided
	if opts.QueueName != "" {
		if lws.Labels == nil {
			lws.Labels = make(map[string]string)
		}
		lws.Labels["kueue.x-k8s.io/queue-name"] = opts.QueueName
	}

	// Add priority class if provided
	if opts.PriorityClassName != "" {
		if lws.Spec.LeaderWorkerTemplate.LeaderTemplate != nil {
			lws.Spec.LeaderWorkerTemplate.LeaderTemplate.Spec.PriorityClassName = opts.PriorityClassName
		}
	}

	return lws
}

// DisaggregatedSetGVR is the resource of the DisaggregatedSet API served by LeaderWorkerSet.
var DisaggregatedSetGVR = schema.GroupVersionResource{
	Group:    "disaggregatedset.x-k8s.io",
	Version:  "v1",
	Resource: "disaggregatedsets",
}

const (
	// DisaggregatedSetNameLabel is the label that the DisaggregatedSet controller sets on its Pods.
	DisaggregatedSetNameLabel = "disaggregatedset.x-k8s.io/name"
	// WorkloadPriorityClassLabel selects the WorkloadPriorityClass of a workload.
	WorkloadPriorityClassLabel = "kueue.x-k8s.io/priority-class"
)

type DisaggregatedSetOptions struct {
	// QueueName: if non-empty, adds the kueue.x-k8s.io/queue-name label
	QueueName string
	// WorkloadPriorityClass: if non-empty, adds the kueue.x-k8s.io/priority-class label
	WorkloadPriorityClass string
	// Slices: defaults to 2 if <= 0
	Slices int
}

// NewDisaggregatedSet creates a DisaggregatedSet with two roles as an unstructured
// object, so the e2e tests do not need the typed DisaggregatedSet API:
//   - prefill: 2 replicas of a single worker requesting 200m CPU and 64Mi memory
//   - decode: 1 replica of a leader requesting 100m CPU and a worker requesting
//     200m CPU, both with 64Mi memory
//
// Kueue admits each slice as one Workload with the PodSets prefill (2 Pods),
// decode-leader (1 Pod) and decode-worker (1 Pod), which request 700m CPU and
// 256Mi memory in total.
func (b *TestResourceBuilder) NewDisaggregatedSet(opts DisaggregatedSetOptions) *unstructured.Unstructured {
	slices := opts.Slices
	if slices <= 0 {
		slices = 2
	}
	labels := map[string]any{}
	if opts.QueueName != "" {
		labels[QueueLabel] = opts.QueueName
	}
	if opts.WorkloadPriorityClass != "" {
		labels[WorkloadPriorityClassLabel] = opts.WorkloadPriorityClass
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": DisaggregatedSetGVR.GroupVersion().String(),
		"kind":       "DisaggregatedSet",
		"metadata": map[string]any{
			"generateName": "test-ds-",
			"namespace":    b.namespace,
			"labels":       labels,
		},
		"spec": map[string]any{
			"slices": int64(slices),
			"roles": []any{
				map[string]any{
					"name": "prefill",
					"spec": map[string]any{
						"replicas": int64(2),
						"leaderWorkerTemplate": map[string]any{
							"size":           int64(1),
							"workerTemplate": b.disaggregatedSetPodTemplate("prefill", "200m"),
						},
					},
				},
				map[string]any{
					"name": "decode",
					"spec": map[string]any{
						"replicas": int64(1),
						"leaderWorkerTemplate": map[string]any{
							"size":           int64(2),
							"leaderTemplate": b.disaggregatedSetPodTemplate("decode-leader", "100m"),
							"workerTemplate": b.disaggregatedSetPodTemplate("decode-worker", "200m"),
						},
					},
				},
			},
		},
	}}
}

// disaggregatedSetPodTemplate returns a Pod template with the builder defaults
// and the given CPU request as an unstructured map.
func (b *TestResourceBuilder) disaggregatedSetPodTemplate(name, cpu string) map[string]any {
	spec := b.NewPod().Spec
	spec.RestartPolicy = ""
	spec.Containers[0].Name = name
	spec.Containers[0].Command = []string{"sh", "-c", "sleep 3600"}
	spec.Containers[0].Resources.Requests = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse("64Mi"),
	}
	template, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&corev1.PodTemplateSpec{Spec: spec})
	if err != nil {
		panic(fmt.Sprintf("failed to convert the %s Pod template: %v", name, err))
	}
	return template
}

// NewResourceClaimTemplate creates a ResourceClaimTemplate for DRA e2e tests.
// If celExpression is empty, no CEL selector is added.
func NewResourceClaimTemplate(name, namespace, deviceClass string, count int64, celExpression string) *resourcev1.ResourceClaimTemplate {
	rct := &resourcev1.ResourceClaimTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: resourcev1.ResourceClaimTemplateSpec{
			Spec: resourcev1.ResourceClaimSpec{
				Devices: resourcev1.DeviceClaim{
					Requests: []resourcev1.DeviceRequest{
						{
							Name: "gpu-req",
							Exactly: &resourcev1.ExactDeviceRequest{
								DeviceClassName: deviceClass,
								Count:           count,
							},
						},
					},
				},
			},
		},
	}
	if celExpression != "" {
		rct.Spec.Spec.Devices.Requests[0].Exactly.Selectors = []resourcev1.DeviceSelector{
			{CEL: &resourcev1.CELDeviceSelector{Expression: celExpression}},
		}
	}
	return rct
}

// NewDRAExtendedResourceJob creates a Job with an extended resource request for DRA e2e tests.
func (b *TestResourceBuilder) NewDRAExtendedResourceJob(name, queueName, extendedResource string, count int) *batchv1.Job {
	job := b.NewJob()
	job.Name = name
	job.Labels[QueueLabel] = queueName
	job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("100m")
	job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = resource.MustParse("100Mi")
	job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceName(extendedResource)] = resource.MustParse(fmt.Sprintf("%d", count))
	job.Spec.Template.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
		corev1.ResourceName(extendedResource): resource.MustParse(fmt.Sprintf("%d", count)),
	}
	job.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "echo DRA ER test; sleep 120"}
	return job
}

// NewDRAJob creates a Job with a DRA ResourceClaim for e2e tests.
func (b *TestResourceBuilder) NewDRAJob(name, queueName, templateName string) *batchv1.Job {
	job := b.NewJob()
	job.Name = name
	job.Labels[QueueLabel] = queueName
	job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("100m")
	job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = resource.MustParse("100Mi")
	job.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "echo DRA test; sleep 120"}
	job.Spec.Template.Spec.ResourceClaims = []corev1.PodResourceClaim{
		{Name: "gpu", ResourceClaimTemplateName: ptr.To(templateName)},
	}
	job.Spec.Template.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{{Name: "gpu"}}
	return job
}
