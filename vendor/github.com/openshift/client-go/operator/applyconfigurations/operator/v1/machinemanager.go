// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
)

// MachineManagerApplyConfiguration represents a declarative configuration of the MachineManager type for use
// with apply.
type MachineManagerApplyConfiguration struct {
	Resource  *operatorv1.MachineManagerMachineSetsResourceType `json:"resource,omitempty"`
	APIGroup  *operatorv1.MachineManagerMachineSetsAPIGroupType `json:"apiGroup,omitempty"`
	Selection *MachineManagerSelectorApplyConfiguration         `json:"selection,omitempty"`
}

// MachineManagerApplyConfiguration constructs a declarative configuration of the MachineManager type for use with
// apply.
func MachineManager() *MachineManagerApplyConfiguration {
	return &MachineManagerApplyConfiguration{}
}

// WithResource sets the Resource field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Resource field is set to the value of the last call.
func (b *MachineManagerApplyConfiguration) WithResource(value operatorv1.MachineManagerMachineSetsResourceType) *MachineManagerApplyConfiguration {
	b.Resource = &value
	return b
}

// WithAPIGroup sets the APIGroup field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the APIGroup field is set to the value of the last call.
func (b *MachineManagerApplyConfiguration) WithAPIGroup(value operatorv1.MachineManagerMachineSetsAPIGroupType) *MachineManagerApplyConfiguration {
	b.APIGroup = &value
	return b
}

// WithSelection sets the Selection field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Selection field is set to the value of the last call.
func (b *MachineManagerApplyConfiguration) WithSelection(value *MachineManagerSelectorApplyConfiguration) *MachineManagerApplyConfiguration {
	b.Selection = value
	return b
}