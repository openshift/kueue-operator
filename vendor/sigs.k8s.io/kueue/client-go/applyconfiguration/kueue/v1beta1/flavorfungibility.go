/*
Copyright The Kubernetes Authors.

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

package v1beta1

import (
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

// FlavorFungibilityApplyConfiguration represents a declarative configuration of the FlavorFungibility type for use
// with apply.
type FlavorFungibilityApplyConfiguration struct {
	WhenCanBorrow  *kueuev1beta1.FlavorFungibilityPolicy `json:"whenCanBorrow,omitempty"`
	WhenCanPreempt *kueuev1beta1.FlavorFungibilityPolicy `json:"whenCanPreempt,omitempty"`
}

// FlavorFungibilityApplyConfiguration constructs a declarative configuration of the FlavorFungibility type for use with
// apply.
func FlavorFungibility() *FlavorFungibilityApplyConfiguration {
	return &FlavorFungibilityApplyConfiguration{}
}

// WithWhenCanBorrow sets the WhenCanBorrow field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the WhenCanBorrow field is set to the value of the last call.
func (b *FlavorFungibilityApplyConfiguration) WithWhenCanBorrow(value kueuev1beta1.FlavorFungibilityPolicy) *FlavorFungibilityApplyConfiguration {
	b.WhenCanBorrow = &value
	return b
}

// WithWhenCanPreempt sets the WhenCanPreempt field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the WhenCanPreempt field is set to the value of the last call.
func (b *FlavorFungibilityApplyConfiguration) WithWhenCanPreempt(value kueuev1beta1.FlavorFungibilityPolicy) *FlavorFungibilityApplyConfiguration {
	b.WhenCanPreempt = &value
	return b
}
