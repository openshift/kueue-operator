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
// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1alpha1

import (
	kueueoperatorv1alpha1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1alpha1"
	v1 "k8s.io/client-go/applyconfigurations/meta/v1"
	v1beta1 "sigs.k8s.io/kueue/apis/config/v1beta1"
)

// KueueConfigurationApplyConfiguration represents a declarative configuration of the KueueConfiguration type for use
// with apply.
type KueueConfigurationApplyConfiguration struct {
	WaitForPodsReady             *v1beta1.WaitForPodsReady                               `json:"waitForPodsReady,omitempty"`
	Integrations                 *v1beta1.Integrations                                   `json:"integrations,omitempty"`
	FeatureGates                 map[string]bool                                         `json:"featureGates,omitempty"`
	Resources                    *v1beta1.Resources                                      `json:"resources,omitempty"`
	ManageJobsWithoutQueueName   *kueueoperatorv1alpha1.ManageJobsWithoutQueueNameOption `json:"manageJobsWithoutQueueName,omitempty"`
	ManagedJobsNamespaceSelector *v1.LabelSelectorApplyConfiguration                     `json:"managedJobsNamespaceSelector,omitempty"`
	FairSharing                  *v1beta1.FairSharing                                    `json:"fairSharing,omitempty"`
	DisableMetrics               *bool                                                   `json:"disableMetrics,omitempty"`
}

// KueueConfigurationApplyConfiguration constructs a declarative configuration of the KueueConfiguration type for use with
// apply.
func KueueConfiguration() *KueueConfigurationApplyConfiguration {
	return &KueueConfigurationApplyConfiguration{}
}

// WithWaitForPodsReady sets the WaitForPodsReady field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the WaitForPodsReady field is set to the value of the last call.
func (b *KueueConfigurationApplyConfiguration) WithWaitForPodsReady(value v1beta1.WaitForPodsReady) *KueueConfigurationApplyConfiguration {
	b.WaitForPodsReady = &value
	return b
}

// WithIntegrations sets the Integrations field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Integrations field is set to the value of the last call.
func (b *KueueConfigurationApplyConfiguration) WithIntegrations(value v1beta1.Integrations) *KueueConfigurationApplyConfiguration {
	b.Integrations = &value
	return b
}

// WithFeatureGates puts the entries into the FeatureGates field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, the entries provided by each call will be put on the FeatureGates field,
// overwriting an existing map entries in FeatureGates field with the same key.
func (b *KueueConfigurationApplyConfiguration) WithFeatureGates(entries map[string]bool) *KueueConfigurationApplyConfiguration {
	if b.FeatureGates == nil && len(entries) > 0 {
		b.FeatureGates = make(map[string]bool, len(entries))
	}
	for k, v := range entries {
		b.FeatureGates[k] = v
	}
	return b
}

// WithResources sets the Resources field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Resources field is set to the value of the last call.
func (b *KueueConfigurationApplyConfiguration) WithResources(value v1beta1.Resources) *KueueConfigurationApplyConfiguration {
	b.Resources = &value
	return b
}

// WithManageJobsWithoutQueueName sets the ManageJobsWithoutQueueName field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ManageJobsWithoutQueueName field is set to the value of the last call.
func (b *KueueConfigurationApplyConfiguration) WithManageJobsWithoutQueueName(value kueueoperatorv1alpha1.ManageJobsWithoutQueueNameOption) *KueueConfigurationApplyConfiguration {
	b.ManageJobsWithoutQueueName = &value
	return b
}

// WithManagedJobsNamespaceSelector sets the ManagedJobsNamespaceSelector field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ManagedJobsNamespaceSelector field is set to the value of the last call.
func (b *KueueConfigurationApplyConfiguration) WithManagedJobsNamespaceSelector(value *v1.LabelSelectorApplyConfiguration) *KueueConfigurationApplyConfiguration {
	b.ManagedJobsNamespaceSelector = value
	return b
}

// WithFairSharing sets the FairSharing field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the FairSharing field is set to the value of the last call.
func (b *KueueConfigurationApplyConfiguration) WithFairSharing(value v1beta1.FairSharing) *KueueConfigurationApplyConfiguration {
	b.FairSharing = &value
	return b
}

// WithDisableMetrics sets the DisableMetrics field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the DisableMetrics field is set to the value of the last call.
func (b *KueueConfigurationApplyConfiguration) WithDisableMetrics(value bool) *KueueConfigurationApplyConfiguration {
	b.DisableMetrics = &value
	return b
}
