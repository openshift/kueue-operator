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
	"context"
	"fmt"
	"time"

	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	upstreamkueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"
)

const (
	DRADeviceClassName     = "gpu.nvidia.com"
	DRALogicalResource     = "nvidia-gpu"
	DRATestNamespacePrefix = "kueue-dra-test-"
	DRALocalQueueName      = "dra-test-queue"
	DRAGPUDeviceType       = "gpu"

	DRAResourceSliceTimeout = 60 * time.Second
	DRAResourceSlicePoll    = 2 * time.Second
	DRASettleTimeout        = 30 * time.Second
	DRAQuotaCheckTimeout    = 15 * time.Second
)

// SetDRAJobCPU reduces CPU request for DRA test jobs to fit on GPU nodes
// where NVIDIA operator daemonsets consume most of the CPU budget.
func SetDRAJobCPU(job *batchv1.Job) {
	job.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("100m")
}

// NewDRAResourceClaimTemplate creates a ResourceClaimTemplate that requests
// the given number of GPUs from the gpu.nvidia.com DeviceClass.
func NewDRAResourceClaimTemplate(name, namespace string, gpuCount int64) *resourcev1.ResourceClaimTemplate {
	return &resourcev1.ResourceClaimTemplate{
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
								DeviceClassName: DRADeviceClassName,
								Count:           gpuCount,
							},
						},
					},
				},
			},
		},
	}
}

// NewDRAJob creates a Job with DRA ResourceClaim configuration for the given template.
func NewDRAJob(builder *TestResourceBuilder, name, templateName, queueName string) *batchv1.Job {
	job := builder.NewJob()
	SetDRAJobCPU(job)
	job.Name = name
	job.Labels[QueueLabel] = queueName
	job.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "echo Hello Kueue; sleep 10"}
	job.Spec.Template.Spec.ResourceClaims = []corev1.PodResourceClaim{
		{Name: "gpu", ResourceClaimTemplateName: ptr.To(templateName)},
	}
	job.Spec.Template.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{{Name: "gpu"}}
	return job
}

// AddDRAClaims adds DRA ResourceClaim references and reduces CPU request on a PodSpec.
func AddDRAClaims(spec *corev1.PodSpec, templateName string) {
	spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("100m")
	spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{{Name: "gpu-claim"}}
	spec.ResourceClaims = []corev1.PodResourceClaim{
		{Name: "gpu-claim", ResourceClaimTemplateName: ptr.To(templateName)},
	}
}

// WaitForPodWorkloadAdmitted waits until a pod matching the label selector has
// a corresponding admitted Kueue workload, correlating via OwnerReferences.
func WaitForPodWorkloadAdmitted(ctx context.Context, kubeClient *kubernetes.Clientset, kueueClient *upstreamkueueclient.Clientset, namespace, labelSelector string) {
	Eventually(func() error {
		pods, err := kubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return err
		}
		if len(pods.Items) == 0 {
			return fmt.Errorf("no pods found with label %s", labelSelector)
		}
		ownerUIDs := make(map[types.UID]bool)
		for _, pod := range pods.Items {
			for _, ref := range pod.OwnerReferences {
				ownerUIDs[ref.UID] = true
			}
			ownerUIDs[pod.UID] = true
		}
		workloads, err := kueueClient.KueueV1beta2().Workloads(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		for _, wl := range workloads.Items {
			if wl.Status.Admission == nil {
				continue
			}
			for _, ref := range wl.OwnerReferences {
				if ownerUIDs[ref.UID] {
					return nil
				}
			}
		}
		return fmt.Errorf("no admitted workload correlated to pods with label %s", labelSelector)
	}, OperatorReadyTime, OperatorPoll).Should(Succeed())
}

// VerifyResourceClaimAllocated verifies that at least one ResourceClaim
// in the namespace is in the "allocated,reserved" state.
func VerifyResourceClaimAllocated(ctx context.Context, kubeClient *kubernetes.Clientset, namespace string) {
	Eventually(func(g Gomega) {
		claims, err := kubeClient.ResourceV1().ResourceClaims(namespace).List(ctx, metav1.ListOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(claims.Items).NotTo(BeEmpty(), "no ResourceClaims found in namespace %s", namespace)
		claim := claims.Items[0]
		g.Expect(claim.Status.Allocation).NotTo(BeNil(), "ResourceClaim not allocated in namespace %s", namespace)
		g.Expect(claim.Status.ReservedFor).NotTo(BeEmpty(), "ResourceClaim not reserved in namespace %s", namespace)
	}, OperatorReadyTime, OperatorPoll).Should(Succeed())
}

// VerifyNoResourceClaims verifies that no ResourceClaims created after the given
// timestamp exist in the namespace.
func VerifyNoResourceClaims(ctx context.Context, kubeClient *kubernetes.Clientset, namespace string, createdAfter metav1.Time) {
	Consistently(func(g Gomega) {
		claims, err := kubeClient.ResourceV1().ResourceClaims(namespace).List(ctx, metav1.ListOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		var filtered []resourcev1.ResourceClaim
		for _, c := range claims.Items {
			if c.CreationTimestamp.After(createdAfter.Time) {
				filtered = append(filtered, c)
			}
		}
		g.Expect(filtered).To(BeEmpty(), "expected no ResourceClaims after %v in namespace %s", createdAfter.Time, namespace)
	}, ConsistentlyTimeout, ConsistentlyPoll).Should(Succeed())
}

// CountGPUsFromResourceSlices counts GPU devices (type="gpu") from the
// gpu.nvidia.com driver across all ResourceSlices. Filters by the "type"
// device attribute to exclude non-GPU devices like MIG partitions.
func CountGPUsFromResourceSlices(ctx context.Context, kubeClient *kubernetes.Clientset) (int, error) {
	slices, err := kubeClient.ResourceV1().ResourceSlices().List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, err
	}
	count := 0
	for _, s := range slices.Items {
		if s.Spec.Driver == DRADeviceClassName {
			for _, d := range s.Spec.Devices {
				if typeAttr, ok := d.Attributes["type"]; ok {
					if typeAttr.StringValue != nil && *typeAttr.StringValue == DRAGPUDeviceType {
						count++
					}
				}
			}
		}
	}
	return count, nil
}
