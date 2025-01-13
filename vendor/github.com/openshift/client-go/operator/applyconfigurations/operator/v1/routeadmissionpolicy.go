// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
)

// RouteAdmissionPolicyApplyConfiguration represents a declarative configuration of the RouteAdmissionPolicy type for use
// with apply.
type RouteAdmissionPolicyApplyConfiguration struct {
	NamespaceOwnership *operatorv1.NamespaceOwnershipCheck `json:"namespaceOwnership,omitempty"`
	WildcardPolicy     *operatorv1.WildcardPolicy          `json:"wildcardPolicy,omitempty"`
}

// RouteAdmissionPolicyApplyConfiguration constructs a declarative configuration of the RouteAdmissionPolicy type for use with
// apply.
func RouteAdmissionPolicy() *RouteAdmissionPolicyApplyConfiguration {
	return &RouteAdmissionPolicyApplyConfiguration{}
}

// WithNamespaceOwnership sets the NamespaceOwnership field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the NamespaceOwnership field is set to the value of the last call.
func (b *RouteAdmissionPolicyApplyConfiguration) WithNamespaceOwnership(value operatorv1.NamespaceOwnershipCheck) *RouteAdmissionPolicyApplyConfiguration {
	b.NamespaceOwnership = &value
	return b
}

// WithWildcardPolicy sets the WildcardPolicy field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the WildcardPolicy field is set to the value of the last call.
func (b *RouteAdmissionPolicyApplyConfiguration) WithWildcardPolicy(value operatorv1.WildcardPolicy) *RouteAdmissionPolicyApplyConfiguration {
	b.WildcardPolicy = &value
	return b
}