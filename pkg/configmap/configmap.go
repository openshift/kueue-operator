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
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/component-base/config/v1alpha1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	configapi "sigs.k8s.io/kueue/apis/config/v1beta1"

	kueue "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
)

func BuildConfigMap(namespace string, kueueCfg kueue.KueueConfiguration) (*corev1.ConfigMap, error) {
	config := defaultKueueConfigurationTemplate(namespace, kueueCfg)
	cfg, err := yaml.Marshal(config)
	if err != nil {
		return nil, err
	}
	cfgMap := &corev1.ConfigMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      "kueue-manager-config",
			Namespace: namespace,
		},
		Data: map[string]string{"controller_manager_config.yaml": string(cfg)},
	}
	return cfgMap, nil
}

func mapOperatorIntegrationsToKueue(integrations *kueue.Integrations) *configapi.Integrations {

	return &configapi.Integrations{
		Frameworks:         buildFrameworkList(integrations.Frameworks),
		ExternalFrameworks: buildExternalFrameworkList(integrations.ExternalFrameworks),
		LabelKeysToCopy:    buildLabelKeysCopy(integrations.LabelKeysToCopy),
	}
}

func buildFrameworkList(kueuelist []kueue.KueueIntegration) []string {
	// Upstream kueue uses lowercase names for these.
	// This does not fit our api review so we are converted before building it into
	// the configmap.
	conversionMap := map[string]string{}
	conversionMap[string(kueue.KueueIntegrationBatchJob)] = "batch/job"
	conversionMap[string(kueue.KueueIntegrationMPIJob)] = "kubeflow.org/mpijob"
	conversionMap[string(kueue.KueueIntegrationRayJob)] = "ray.io/rayjob"
	conversionMap[string(kueue.KueueIntegrationRayCluster)] = "ray.io/raycluster"
	conversionMap[string(kueue.KueueIntegrationJobSet)] = "jobset.x-k8s.io/jobset"
	conversionMap[string(kueue.KueueIntegrationPaddleJob)] = "kubeflow.org/paddlejob"
	conversionMap[string(kueue.KueueIntegrationPyTorchJob)] = "kubeflow.org/pytorchjob"
	conversionMap[string(kueue.KueueIntegrationTFJob)] = "kubeflow.org/tfjob"
	conversionMap[string(kueue.KueueIntegrationXGBoostJob)] = "kubeflow.org/xgboostjob"
	conversionMap[string(kueue.KueueIntegrationAppWrapper)] = "workload.codeflare.dev/appwrapper"
	conversionMap[string(kueue.KueueIntegrationPod)] = "pod"
	conversionMap[string(kueue.KueueIntegrationDeployment)] = "deployment"
	conversionMap[string(kueue.KueueIntegrationLeaderWorkerSet)] = "leaderworkerset.x-k8s.io/leaderworkerset"
	conversionMap[string(kueue.KueueIntegrationStatefulSet)] = "statefulset"
	conversionMap[string(kueue.KueueIntegrationTrainJob)] = "trainer.kubeflow.org/trainjob"

	ret := []string{}
	for _, val := range kueuelist {
		ret = append(ret, conversionMap[string(val)])
	}
	return ret
}

func buildExternalFrameworkList(kueuelist []kueue.ExternalFramework) []string {
	ret := []string{}
	for _, val := range kueuelist {
		ret = append(ret, fmt.Sprintf("%s.%s.%s", val.Resource, val.Version, val.Group))
	}
	return ret
}

func buildLabelKeysCopy(labelKeys []kueue.LabelKeys) []string {
	ret := []string{}
	for _, val := range labelKeys {
		ret = append(ret, val.Key)
	}
	return ret
}

func mapOperatorMultiKueueToKueue(multiKueue *kueue.MultiKueue) *configapi.MultiKueue {
	if multiKueue == nil {
		return nil
	}
	return &configapi.MultiKueue{
		ExternalFrameworks: buildMultiKueueExternalFrameworkList(multiKueue.ExternalFrameworks),
	}
}

func buildMultiKueueExternalFrameworkList(kueuelist []kueue.ExternalFramework) []configapi.MultiKueueExternalFramework {
	ret := []configapi.MultiKueueExternalFramework{}
	for _, val := range kueuelist {
		ret = append(ret, configapi.MultiKueueExternalFramework{
			Name: fmt.Sprintf("%s.%s.%s", val.Resource, val.Version, val.Group),
		})
	}
	return ret
}

func buildManagedJobsWithoutQueueName(workloadManagement kueue.WorkloadManagement) bool {
	return workloadManagement.LabelPolicy == kueue.LabelPolicyNone
}

func buildWaitForPodsReady(gangSchedulingPolicy kueue.GangScheduling) *configapi.WaitForPodsReady {
	switch gangSchedulingPolicy.Policy {
	case kueue.GangSchedulingPolicyNone:
		return &configapi.WaitForPodsReady{Enable: false}
	case kueue.GangSchedulingPolicyByWorkload:
		return &configapi.WaitForPodsReady{Enable: true, BlockAdmission: blockAdmission(gangSchedulingPolicy.ByWorkload)}
	default:
		return &configapi.WaitForPodsReady{Enable: false}
	}
}

func blockAdmission(admission *kueue.ByWorkload) *bool {
	if admission == nil {
		return ptr.To(false)
	}
	if admission.Admission == kueue.GangSchedulingWorkloadAdmissionSequential {
		return ptr.To(true)
	} else {
		return ptr.To(false)
	}
}

func buildFairSharing(preemption kueue.Preemption) *configapi.FairSharing {
	switch preemption.PreemptionPolicy {
	case kueue.PreemptionStrategyClassical:
		return &configapi.FairSharing{Enable: false}
	case kueue.PreemptionStrategyFairsharing:
		return &configapi.FairSharing{Enable: true, PreemptionStrategies: []configapi.PreemptionStrategy{configapi.LessThanOrEqualToFinalShare, configapi.LessThanInitialShare}}
	default:
		return &configapi.FairSharing{Enable: false}
	}
}

func defaultKueueConfigurationTemplate(namespace string, kueueCfg kueue.KueueConfiguration) *configapi.Configuration {
	return &configapi.Configuration{
		TypeMeta: v1.TypeMeta{
			Kind:       "Configuration",
			APIVersion: "config.kueue.x-k8s.io/v1beta1",
		},
		Namespace: ptr.To(namespace),
		ControllerManager: configapi.ControllerManager{
			Health: configapi.ControllerHealth{
				HealthProbeBindAddress: ":8081",
			},
			Metrics: configapi.ControllerMetrics{
				BindAddress:                 ":8443",
				EnableClusterQueueResources: true,
			},
			Webhook: configapi.ControllerWebhook{
				Port: ptr.To(9443),
			},
			Controller: &configapi.ControllerConfigurationSpec{
				GroupKindConcurrency: map[string]int{
					"Job.batch":                     5,
					"Pod":                           5,
					"Workload.kueue.x-k8s.io":       5,
					"LocalQueue.kueue.x-k8s.io":     1,
					"ClusterQueue.kueue.x-k8s.io":   1,
					"ResourceFlavor.kueue.x-k8s.io": 1,
				},
			},
			// Durations recommended by OCP, taken from https://github.com/openshift/enhancements/blob/0f916a52af1a6fbdab0c5b80ae0e66c7a27efb6a/CONVENTIONS.md#handling-kube-apiserver-disruption
			LeaderElection: &v1alpha1.LeaderElectionConfiguration{
				LeaderElect:   ptr.To(true),
				LeaseDuration: v1.Duration{Duration: 137 * time.Second},
				RenewDeadline: v1.Duration{Duration: 107 * time.Second},
				RetryPeriod:   v1.Duration{Duration: 26 * time.Second},
			},
		},
		ClientConnection: &configapi.ClientConnection{
			QPS:   float32Ptr(50),
			Burst: int32Ptr(100),
		},
		Integrations: mapOperatorIntegrationsToKueue(&kueueCfg.Integrations),
		InternalCertManagement: &configapi.InternalCertManagement{
			Enable: ptr.To(false),
		},
		ManagedJobsNamespaceSelector: &v1.LabelSelector{
			MatchLabels: map[string]string{"kueue.openshift.io/managed": "true"},
		},
		ManageJobsWithoutQueueName: buildManagedJobsWithoutQueueName(kueueCfg.WorkloadManagement),
		WaitForPodsReady:           buildWaitForPodsReady(kueueCfg.GangScheduling),
		FairSharing:                buildFairSharing(kueueCfg.Preemption),
		MultiKueue:                 mapOperatorMultiKueueToKueue(kueueCfg.MultiKueue),
	}
}

func float32Ptr(f float32) *float32 {
	return &f
}

func int32Ptr(i int32) *int32 {
	return &i
}
