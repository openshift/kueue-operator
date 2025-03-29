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

	kueue "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1alpha1"
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
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta1
controller:
  groupKindConcurrency:
    ClusterQueue.kueue.x-k8s.io: 1
    Job.batch: 5
    LocalQueue.kueue.x-k8s.io: 1
    Pod: 5
    ResourceFlavor.kueue.x-k8s.io: 1
    Workload.kueue.x-k8s.io: 5
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
  leaseDuration: 0s
  renewDeadline: 0s
  resourceLock: ""
  resourceName: ""
  resourceNamespace: ""
  retryPeriod: 0s
manageJobsWithoutQueueName: false
metrics:
  bindAddress: :8443
  enableClusterQueueResources: true
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
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta1
controller:
  groupKindConcurrency:
    ClusterQueue.kueue.x-k8s.io: 1
    Job.batch: 5
    LocalQueue.kueue.x-k8s.io: 1
    Pod: 5
    ResourceFlavor.kueue.x-k8s.io: 1
    Workload.kueue.x-k8s.io: 5
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
  leaseDuration: 0s
  renewDeadline: 0s
  resourceLock: ""
  resourceName: ""
  resourceNamespace: ""
  retryPeriod: 0s
manageJobsWithoutQueueName: false
metrics:
  bindAddress: :8443
  enableClusterQueueResources: true
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
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta1
controller:
  groupKindConcurrency:
    ClusterQueue.kueue.x-k8s.io: 1
    Job.batch: 5
    LocalQueue.kueue.x-k8s.io: 1
    Pod: 5
    ResourceFlavor.kueue.x-k8s.io: 1
    Workload.kueue.x-k8s.io: 5
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
  leaseDuration: 0s
  renewDeadline: 0s
  resourceLock: ""
  resourceName: ""
  resourceNamespace: ""
  retryPeriod: 0s
manageJobsWithoutQueueName: false
metrics:
  bindAddress: :8443
  enableClusterQueueResources: true
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
