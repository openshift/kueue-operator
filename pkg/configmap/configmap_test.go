/*
Copyright 2024.

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

package configmap

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"

	kueue "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
)

func TestBuildConfigMap(t *testing.T) {
	testCases := map[string]struct {
		configuration kueue.KueueConfiguration
		wantCfgMap    *corev1.ConfigMap
		wantErr       error
	}{
		"batch job example": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationBatchJob},
				},
				Resources: kueue.Resources{
					ExcludedResourcePrefixes: []string{"example.com/exclude"},
					Transformations: []kueue.ResourceTransformation{
						{
							Input:    "example.com/gpu-type1",
							Strategy: &[]kueue.ResourceTransformationStrategy{kueue.ResourceTransformationStrategyReplace}[0],
							Outputs: []kueue.ResourceOutput{
								{Resource: "example.com/gpu-memory", Quantity: "5Gi"},
								{Resource: "example.com/credits", Quantity: "10"},
							},
						},
						{
							Input:    "cpu",
							Strategy: &[]kueue.ResourceTransformationStrategy{kueue.ResourceTransformationStrategyRetain}[0],
							Outputs: []kueue.ResourceOutput{
								{Resource: "example.com/credits", Quantity: "1"},
							},
						},
					},
				},
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta1
clientConnection:
  burst: 100
  qps: 50
controller:
  groupKindConcurrency:
    ClusterQueue.kueue.x-k8s.io: 1
    Job.batch: 5
    LocalQueue.kueue.x-k8s.io: 1
    Pod: 5
    ResourceFlavor.kueue.x-k8s.io: 1
    Workload.kueue.x-k8s.io: 5
fairSharing:
  enable: false
featureGates:
  HierarchicalCohorts: false
  VisibilityOnDemand: false
health:
  healthProbeBindAddress: :8081
integrations:
  frameworks:
  - batch/job
internalCertManagement:
  enable: false
kind: Configuration
leaderElection:
  leaderElect: true
  leaseDuration: 2m17s
  renewDeadline: 1m47s
  resourceLock: ""
  resourceName: ""
  resourceNamespace: ""
  retryPeriod: 26s
manageJobsWithoutQueueName: false
managedJobsNamespaceSelector:
  matchLabels:
    kueue.openshift.io/managed: "true"
metrics:
  bindAddress: :8443
  enableClusterQueueResources: true
resources:
  excludeResourcePrefixes:
  - example.com/exclude
  transformations:
  - input: example.com/gpu-type1
    outputs:
      example.com/credits: "10"
      example.com/gpu-memory: 5Gi
    strategy: Replace
  - input: cpu
    outputs:
      example.com/credits: "1"
    strategy: Retain
waitForPodsReady: {}
webhook:
  port: 9443
`,
				},
			},
			wantErr: nil,
		},
		"rhoai example": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationRayJob, kueue.KueueIntegrationRayCluster, kueue.KueueIntegrationPyTorchJob},
				},
				GangScheduling: kueue.GangScheduling{
					Policy: kueue.GangSchedulingPolicyByWorkload,
					ByWorkload: &kueue.ByWorkload{
						Admission: kueue.GangSchedulingWorkloadAdmissionParallel,
					},
				},
				Preemption: kueue.Preemption{PreemptionPolicy: kueue.PreemptionStrategyClassical},
				Resources: kueue.Resources{
					ExcludedResourcePrefixes: []string{"nvidia.com/exclude"},
					Transformations: []kueue.ResourceTransformation{
						{
							Input:    "nvidia.com/gpu",
							Strategy: &[]kueue.ResourceTransformationStrategy{kueue.ResourceTransformationStrategyReplace}[0],
							Outputs: []kueue.ResourceOutput{
								{Resource: "nvidia.com/gpu-memory", Quantity: "16Gi"},
								{Resource: "example.com/credits", Quantity: "20"},
							},
						},
					},
				},
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta1
clientConnection:
  burst: 100
  qps: 50
controller:
  groupKindConcurrency:
    ClusterQueue.kueue.x-k8s.io: 1
    Job.batch: 5
    LocalQueue.kueue.x-k8s.io: 1
    Pod: 5
    ResourceFlavor.kueue.x-k8s.io: 1
    Workload.kueue.x-k8s.io: 5
fairSharing:
  enable: false
featureGates:
  HierarchicalCohorts: false
  VisibilityOnDemand: false
health:
  healthProbeBindAddress: :8081
integrations:
  frameworks:
  - ray.io/rayjob
  - ray.io/raycluster
  - kubeflow.org/pytorchjob
internalCertManagement:
  enable: false
kind: Configuration
leaderElection:
  leaderElect: true
  leaseDuration: 2m17s
  renewDeadline: 1m47s
  resourceLock: ""
  resourceName: ""
  resourceNamespace: ""
  retryPeriod: 26s
manageJobsWithoutQueueName: false
managedJobsNamespaceSelector:
  matchLabels:
    kueue.openshift.io/managed: "true"
metrics:
  bindAddress: :8443
  enableClusterQueueResources: true
resources:
  excludeResourcePrefixes:
  - nvidia.com/exclude
  transformations:
  - input: nvidia.com/gpu
    outputs:
      example.com/credits: "20"
      nvidia.com/gpu-memory: 16Gi
    strategy: Replace
waitForPodsReady:
  blockAdmission: false
  enable: true
webhook:
  port: 9443
`,
				},
			},
			wantErr: nil,
		},
		"ibm example": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationAppWrapper},
				},
				GangScheduling:     kueue.GangScheduling{Policy: kueue.GangSchedulingPolicyNone},
				WorkloadManagement: kueue.WorkloadManagement{LabelPolicy: kueue.LabelPolicyNone},
				Preemption:         kueue.Preemption{PreemptionPolicy: kueue.PreemptionStrategyFairsharing},
				Resources: kueue.Resources{
					Transformations: []kueue.ResourceTransformation{
						{
							Input:    "codeflare.dev/appwrapper-cpu",
							Strategy: &[]kueue.ResourceTransformationStrategy{kueue.ResourceTransformationStrategyRetain}[0],
							Outputs: []kueue.ResourceOutput{
								{Resource: "example.com/compute-units", Quantity: "2"},
							},
						},
					},
				},
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta1
clientConnection:
  burst: 100
  qps: 50
controller:
  groupKindConcurrency:
    ClusterQueue.kueue.x-k8s.io: 1
    Job.batch: 5
    LocalQueue.kueue.x-k8s.io: 1
    Pod: 5
    ResourceFlavor.kueue.x-k8s.io: 1
    Workload.kueue.x-k8s.io: 5
fairSharing:
  enable: true
  preemptionStrategies:
  - LessThanOrEqualToFinalShare
  - LessThanInitialShare
featureGates:
  HierarchicalCohorts: false
  VisibilityOnDemand: false
health:
  healthProbeBindAddress: :8081
integrations:
  frameworks:
  - workload.codeflare.dev/appwrapper
internalCertManagement:
  enable: false
kind: Configuration
leaderElection:
  leaderElect: true
  leaseDuration: 2m17s
  renewDeadline: 1m47s
  resourceLock: ""
  resourceName: ""
  resourceNamespace: ""
  retryPeriod: 26s
manageJobsWithoutQueueName: true
managedJobsNamespaceSelector:
  matchLabels:
    kueue.openshift.io/managed: "true"
metrics:
  bindAddress: :8443
  enableClusterQueueResources: true
resources:
  transformations:
  - input: codeflare.dev/appwrapper-cpu
    outputs:
      example.com/compute-units: "2"
    strategy: Retain
waitForPodsReady: {}
webhook:
  port: 9443
`,
				},
			},
			wantErr: nil,
		},

		"serving workloads": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationDeployment, kueue.KueueIntegrationPod, kueue.KueueIntegrationStatefulSet, kueue.KueueIntegrationAppWrapper, kueue.KueueIntegrationLeaderWorkerSet},
				},
				Resources: kueue.Resources{
					ExcludedResourcePrefixes: []string{"ephemeral-storage"},
					Transformations: []kueue.ResourceTransformation{
						{
							Input:    "memory",
							Strategy: &[]kueue.ResourceTransformationStrategy{kueue.ResourceTransformationStrategyRetain}[0],
							Outputs: []kueue.ResourceOutput{
								{Resource: "example.com/memory-credits", Quantity: "1"},
							},
						},
					},
				},
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta1
clientConnection:
  burst: 100
  qps: 50
controller:
  groupKindConcurrency:
    ClusterQueue.kueue.x-k8s.io: 1
    Job.batch: 5
    LocalQueue.kueue.x-k8s.io: 1
    Pod: 5
    ResourceFlavor.kueue.x-k8s.io: 1
    Workload.kueue.x-k8s.io: 5
fairSharing:
  enable: false
featureGates:
  HierarchicalCohorts: false
  VisibilityOnDemand: false
health:
  healthProbeBindAddress: :8081
integrations:
  frameworks:
  - deployment
  - pod
  - statefulset
  - workload.codeflare.dev/appwrapper
  - leaderworkerset.x-k8s.io/leaderworkerset
internalCertManagement:
  enable: false
kind: Configuration
leaderElection:
  leaderElect: true
  leaseDuration: 2m17s
  renewDeadline: 1m47s
  resourceLock: ""
  resourceName: ""
  resourceNamespace: ""
  retryPeriod: 26s
manageJobsWithoutQueueName: false
managedJobsNamespaceSelector:
  matchLabels:
    kueue.openshift.io/managed: "true"
metrics:
  bindAddress: :8443
  enableClusterQueueResources: true
resources:
  excludeResourcePrefixes:
  - ephemeral-storage
  transformations:
  - input: memory
    outputs:
      example.com/memory-credits: "1"
    strategy: Retain
waitForPodsReady: {}
webhook:
  port: 9443
`,
				},
			},
			wantErr: nil,
		},
		"sequential gang admission": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationDeployment, kueue.KueueIntegrationPod, kueue.KueueIntegrationStatefulSet, kueue.KueueIntegrationAppWrapper, kueue.KueueIntegrationLeaderWorkerSet},
				},
				GangScheduling: kueue.GangScheduling{
					Policy: kueue.GangSchedulingPolicyByWorkload,
					ByWorkload: &kueue.ByWorkload{
						Admission: kueue.GangSchedulingWorkloadAdmissionSequential,
					},
				},
				Resources: kueue.Resources{
					Transformations: []kueue.ResourceTransformation{
						{
							Input:    "cpu",
							Strategy: &[]kueue.ResourceTransformationStrategy{kueue.ResourceTransformationStrategyReplace}[0],
							Outputs: []kueue.ResourceOutput{
								{Resource: "example.com/cpu-credits", Quantity: "5"},
							},
						},
						{
							Input:    "memory",
							Strategy: &[]kueue.ResourceTransformationStrategy{kueue.ResourceTransformationStrategyReplace}[0],
							Outputs: []kueue.ResourceOutput{
								{Resource: "example.com/memory-credits", Quantity: "2"},
							},
						},
					},
				},
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta1
clientConnection:
  burst: 100
  qps: 50
controller:
  groupKindConcurrency:
    ClusterQueue.kueue.x-k8s.io: 1
    Job.batch: 5
    LocalQueue.kueue.x-k8s.io: 1
    Pod: 5
    ResourceFlavor.kueue.x-k8s.io: 1
    Workload.kueue.x-k8s.io: 5
fairSharing:
  enable: false
featureGates:
  HierarchicalCohorts: false
  VisibilityOnDemand: false
health:
  healthProbeBindAddress: :8081
integrations:
  frameworks:
  - deployment
  - pod
  - statefulset
  - workload.codeflare.dev/appwrapper
  - leaderworkerset.x-k8s.io/leaderworkerset
internalCertManagement:
  enable: false
kind: Configuration
leaderElection:
  leaderElect: true
  leaseDuration: 2m17s
  renewDeadline: 1m47s
  resourceLock: ""
  resourceName: ""
  resourceNamespace: ""
  retryPeriod: 26s
manageJobsWithoutQueueName: false
managedJobsNamespaceSelector:
  matchLabels:
    kueue.openshift.io/managed: "true"
metrics:
  bindAddress: :8443
  enableClusterQueueResources: true
resources:
  transformations:
  - input: cpu
    outputs:
      example.com/cpu-credits: "5"
    strategy: Replace
  - input: memory
    outputs:
      example.com/memory-credits: "2"
    strategy: Replace
waitForPodsReady:
  blockAdmission: true
  enable: true
webhook:
  port: 9443
`,
				},
			},
			wantErr: nil,
		},
	}

	for desc, tc := range testCases {
		t.Run(desc, func(t *testing.T) {
			got, err := BuildConfigMap("test", tc.configuration)
			if diff := cmp.Diff(got.Data["controller_manager_config.yaml"], tc.wantCfgMap.Data["controller_manager_config.yaml"]); len(diff) != 0 {
				t.Errorf("Unexpected buckets (-want,+got):\n%s", diff)
			}
			if err != nil && tc.wantErr == nil {
				t.Errorf("Unexpected error: want=%v, got=%v", tc.wantErr, err)
			}
		})
	}
}
