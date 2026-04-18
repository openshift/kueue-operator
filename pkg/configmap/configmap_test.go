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
	configapi "sigs.k8s.io/kueue/apis/config/v1beta2"

	kueue "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
)

func TestBuildConfigMap(t *testing.T) {
	testCases := map[string]struct {
		configuration kueue.KueueConfiguration
		gvrToKind     map[string]string
		draSupported  bool
		tlsOpts       *configapi.TLSOptions
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
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta2
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
namespace: test
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
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta2
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
namespace: test
waitForPodsReady:
  blockAdmission: false
  timeout: 5m0s
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
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta2
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
  preemptionStrategies:
  - LessThanOrEqualToFinalShare
  - LessThanInitialShare
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
namespace: test
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
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta2
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
namespace: test
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
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta2
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
namespace: test
waitForPodsReady:
  blockAdmission: true
  timeout: 5m0s
webhook:
  port: 9443
`,
				},
			},
			wantErr: nil,
		},
		"spark application": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationSparkApplication},
				},
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta2
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
featureGates:
  SparkApplicationIntegration: true
health:
  healthProbeBindAddress: :8081
integrations:
  frameworks:
  - sparkoperator.k8s.io/sparkapplication
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
namespace: test
webhook:
  port: 9443
`,
				},
			},
			wantErr: nil,
		},
		"dra with device class mappings": {
			draSupported: true,
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationBatchJob},
				},
				Resources: kueue.Resources{
					DeviceClassMappings: []kueue.DeviceClassMapping{
						{
							Name:             "example.com/gpus",
							DeviceClassNames: []kueue.DeviceClassName{"gpu.example.com", "gpu-large.example.com"},
						},
					},
				},
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta2
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
featureGates:
  DynamicResourceAllocation: true
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
namespace: test
resources:
  deviceClassMappings:
  - deviceClassNames:
    - gpu.example.com
    - gpu-large.example.com
    name: example.com/gpus
webhook:
  port: 9443
`,
				},
			},
			wantErr: nil,
		},
		"dra with device class mappings on unsupported cluster": {
			draSupported: false,
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationBatchJob},
				},
				Resources: kueue.Resources{
					DeviceClassMappings: []kueue.DeviceClassMapping{
						{
							Name:             "example.com/gpus",
							DeviceClassNames: []kueue.DeviceClassName{"gpu.example.com", "gpu-large.example.com"},
						},
					},
				},
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta2
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
namespace: test
resources:
  deviceClassMappings:
  - deviceClassNames:
    - gpu.example.com
    - gpu-large.example.com
    name: example.com/gpus
webhook:
  port: 9443
`,
				},
			},
			wantErr: nil,
		},
		"multikueue with external frameworks": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationBatchJob},
				},
				MultiKueue: &kueue.MultiKueue{
					ExternalFrameworks: []kueue.ExternalFramework{
						{
							Group:    "example.com",
							Version:  "v1",
							Resource: "customjobs",
						},
						{
							Group:    "test.io",
							Version:  "v1alpha1",
							Resource: "myworkloads",
						}},
				},
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta2
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
featureGates:
  ShortWorkloadNames: true
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
multiKueue:
  externalFrameworks:
  - name: customjobs.v1.example.com
  - name: myworkloads.v1alpha1.test.io
  gcInterval: null
namespace: test
webhook:
  port: 9443
`,
				},
			},
			wantErr: nil,
		},
		"with Intermediate TLS profile (default when TLSSecurityProfile is not specified)": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationBatchJob},
				},
			},
			tlsOpts: &configapi.TLSOptions{
				MinVersion: "VersionTLS12",
				CipherSuites: []string{
					"TLS_AES_128_GCM_SHA256",
					"TLS_AES_256_GCM_SHA384",
					"TLS_CHACHA20_POLY1305_SHA256",
					"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
					"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
					"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
					"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
					"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256",
					"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256",
				},
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta2
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
namespace: test
tls:
  cipherSuites:
  - TLS_AES_128_GCM_SHA256
  - TLS_AES_256_GCM_SHA384
  - TLS_CHACHA20_POLY1305_SHA256
  - TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
  - TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
  - TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384
  - TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384
  - TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256
  - TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256
  minVersion: VersionTLS12
webhook:
  port: 9443
`,
				},
			},
			wantErr: nil,
		},
		"with Modern TLS profile (TLS 1.3, no cipher suites)": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationBatchJob},
				},
			},
			tlsOpts: &configapi.TLSOptions{
				MinVersion: "VersionTLS13",
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta2
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
namespace: test
tls:
  minVersion: VersionTLS13
webhook:
  port: 9443
`,
				},
			},
			wantErr: nil,
		},
		"multikueue with external frameworks and gvr mapping": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationBatchJob},
				},
				MultiKueue: &kueue.MultiKueue{
					ExternalFrameworks: []kueue.ExternalFramework{
						{
							Group:    "example.com",
							Version:  "v1",
							Resource: "customjobs",
						},
						{
							Group:    "test.io",
							Version:  "v1alpha1",
							Resource: "myworkloads",
						}},
				},
			},
			gvrToKind: map[string]string{
				"example.com/v1/customjobs": "CustomJob",
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta2
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
featureGates:
  ShortWorkloadNames: true
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
multiKueue:
  externalFrameworks:
  - name: CustomJob.v1.example.com
  - name: myworkloads.v1alpha1.test.io
  gcInterval: null
namespace: test
webhook:
  port: 9443
`,
				},
			},
			wantErr: nil,
		},
		"integration external frameworks enables short workload names": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationBatchJob},
					ExternalFrameworks: []kueue.ExternalFramework{
						{
							Group:    "tekton.dev",
							Version:  "v1",
							Resource: "pipelineruns",
						},
					},
				},
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `apiVersion: config.kueue.x-k8s.io/v1beta2
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
featureGates:
  ShortWorkloadNames: true
health:
  healthProbeBindAddress: :8081
integrations:
  externalFrameworks:
  - pipelineruns.v1.tekton.dev
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
namespace: test
webhook:
  port: 9443
`,
				},
			},
			wantErr: nil,
		},
		"Admission Fair Sharing with Default configuration": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationBatchJob},
				},
				AdmissionFairSharing: kueue.AdmissionFairSharing{
					Configuration: kueue.AdmissionFairSharingConfigurationDefault,
				},
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `admissionFairSharing:
  usageHalfLifeTime: 0s
  usageSamplingInterval: 0s
apiVersion: config.kueue.x-k8s.io/v1beta2
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
namespace: test
webhook:
  port: 9443
`,
				},
			},
			wantErr: nil,
		},
		"Admission Fair Sharing with only resourceWeights set": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationBatchJob},
				},
				AdmissionFairSharing: kueue.AdmissionFairSharing{
					Configuration: kueue.AdmissionFairSharingConfigurationCustom,
					Custom: kueue.AdmissionFairSharingCustom{
						ResourceWeights: []kueue.ResourceWeight{
							{Name: string(corev1.ResourceCPU), Weight: "+0.5"},
							{Name: string(corev1.ResourceMemory), Weight: "2.0"},
						},
					},
				},
			},
			wantCfgMap: &corev1.ConfigMap{
				Data: map[string]string{
					"controller_manager_config.yaml": `admissionFairSharing:
  resourceWeights:
    cpu: 0.5
    memory: 2
  usageHalfLifeTime: 0s
  usageSamplingInterval: 0s
apiVersion: config.kueue.x-k8s.io/v1beta2
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
namespace: test
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
			got, err := BuildConfigMap("test", tc.configuration, tc.gvrToKind, tc.draSupported, tc.tlsOpts)
			if err != nil && tc.wantErr == nil {
				t.Fatalf("Unexpected error: want=%v, got=%v", tc.wantErr, err)
			}
			if diff := cmp.Diff(got.Data["controller_manager_config.yaml"], tc.wantCfgMap.Data["controller_manager_config.yaml"]); len(diff) != 0 {
				t.Errorf("Unexpected buckets (-want,+got):\n%s", diff)
			}
		})
	}
}
