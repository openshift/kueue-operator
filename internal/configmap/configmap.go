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
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	configapi "sigs.k8s.io/kueue/apis/config/v1beta1"

	cachev1 "github.com/kannon92/kueue-operator/api/v1"
)

func BuildConfigMap(namespace string, kueueCfg cachev1.KueueConfiguration) (*corev1.ConfigMap, error) {
	config := defaultKueueConfigurationTemplate(kueueCfg)
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

func defaultKueueConfigurationTemplate(kueueCfg cachev1.KueueConfiguration) *configapi.Configuration {
	return &configapi.Configuration{
		TypeMeta: v1.TypeMeta{
			Kind:       "Configuration",
			APIVersion: "config.kueue.x-k8s.io/v1beta1",
		},
		ControllerManager: configapi.ControllerManager{
			Health: configapi.ControllerHealth{
				HealthProbeBindAddress: ":8081",
			},
			Metrics: configapi.ControllerMetrics{
				BindAddress:                 ":8080",
				EnableClusterQueueResources: true,
			},
			Webhook: configapi.ControllerWebhook{
				Port: ptr.To[int](9443),
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
		},
		WaitForPodsReady:           kueueCfg.WaitForPodsReady,
		ManageJobsWithoutQueueName: false,
		Integrations:               &kueueCfg.Integrations,
	}
}
