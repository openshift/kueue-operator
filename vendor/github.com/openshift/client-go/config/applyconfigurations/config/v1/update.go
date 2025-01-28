// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

import (
	configv1 "github.com/openshift/api/config/v1"
)

// UpdateApplyConfiguration represents a declarative configuration of the Update type for use
// with apply.
type UpdateApplyConfiguration struct {
	Architecture *configv1.ClusterVersionArchitecture `json:"architecture,omitempty"`
	Version      *string                              `json:"version,omitempty"`
	Image        *string                              `json:"image,omitempty"`
	Force        *bool                                `json:"force,omitempty"`
}

// UpdateApplyConfiguration constructs a declarative configuration of the Update type for use with
// apply.
func Update() *UpdateApplyConfiguration {
	return &UpdateApplyConfiguration{}
}

// WithArchitecture sets the Architecture field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Architecture field is set to the value of the last call.
func (b *UpdateApplyConfiguration) WithArchitecture(value configv1.ClusterVersionArchitecture) *UpdateApplyConfiguration {
	b.Architecture = &value
	return b
}

// WithVersion sets the Version field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Version field is set to the value of the last call.
func (b *UpdateApplyConfiguration) WithVersion(value string) *UpdateApplyConfiguration {
	b.Version = &value
	return b
}

// WithImage sets the Image field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Image field is set to the value of the last call.
func (b *UpdateApplyConfiguration) WithImage(value string) *UpdateApplyConfiguration {
	b.Image = &value
	return b
}

// WithForce sets the Force field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Force field is set to the value of the last call.
func (b *UpdateApplyConfiguration) WithForce(value bool) *UpdateApplyConfiguration {
	b.Force = &value
	return b
}