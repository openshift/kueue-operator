// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

import (
	configv1 "github.com/openshift/api/config/v1"
)

// BareMetalPlatformLoadBalancerApplyConfiguration represents a declarative configuration of the BareMetalPlatformLoadBalancer type for use
// with apply.
type BareMetalPlatformLoadBalancerApplyConfiguration struct {
	Type *configv1.PlatformLoadBalancerType `json:"type,omitempty"`
}

// BareMetalPlatformLoadBalancerApplyConfiguration constructs a declarative configuration of the BareMetalPlatformLoadBalancer type for use with
// apply.
func BareMetalPlatformLoadBalancer() *BareMetalPlatformLoadBalancerApplyConfiguration {
	return &BareMetalPlatformLoadBalancerApplyConfiguration{}
}

// WithType sets the Type field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Type field is set to the value of the last call.
func (b *BareMetalPlatformLoadBalancerApplyConfiguration) WithType(value configv1.PlatformLoadBalancerType) *BareMetalPlatformLoadBalancerApplyConfiguration {
	b.Type = &value
	return b
}