// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

import (
	metav1 "k8s.io/client-go/applyconfigurations/meta/v1"
)

// MachineConfigurationStatusApplyConfiguration represents a declarative configuration of the MachineConfigurationStatus type for use
// with apply.
type MachineConfigurationStatusApplyConfiguration struct {
	ObservedGeneration         *int64                                        `json:"observedGeneration,omitempty"`
	Conditions                 []metav1.ConditionApplyConfiguration          `json:"conditions,omitempty"`
	NodeDisruptionPolicyStatus *NodeDisruptionPolicyStatusApplyConfiguration `json:"nodeDisruptionPolicyStatus,omitempty"`
	ManagedBootImagesStatus    *ManagedBootImagesApplyConfiguration          `json:"managedBootImagesStatus,omitempty"`
}

// MachineConfigurationStatusApplyConfiguration constructs a declarative configuration of the MachineConfigurationStatus type for use with
// apply.
func MachineConfigurationStatus() *MachineConfigurationStatusApplyConfiguration {
	return &MachineConfigurationStatusApplyConfiguration{}
}

// WithObservedGeneration sets the ObservedGeneration field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ObservedGeneration field is set to the value of the last call.
func (b *MachineConfigurationStatusApplyConfiguration) WithObservedGeneration(value int64) *MachineConfigurationStatusApplyConfiguration {
	b.ObservedGeneration = &value
	return b
}

// WithConditions adds the given value to the Conditions field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Conditions field.
func (b *MachineConfigurationStatusApplyConfiguration) WithConditions(values ...*metav1.ConditionApplyConfiguration) *MachineConfigurationStatusApplyConfiguration {
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithConditions")
		}
		b.Conditions = append(b.Conditions, *values[i])
	}
	return b
}

// WithNodeDisruptionPolicyStatus sets the NodeDisruptionPolicyStatus field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the NodeDisruptionPolicyStatus field is set to the value of the last call.
func (b *MachineConfigurationStatusApplyConfiguration) WithNodeDisruptionPolicyStatus(value *NodeDisruptionPolicyStatusApplyConfiguration) *MachineConfigurationStatusApplyConfiguration {
	b.NodeDisruptionPolicyStatus = value
	return b
}

// WithManagedBootImagesStatus sets the ManagedBootImagesStatus field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ManagedBootImagesStatus field is set to the value of the last call.
func (b *MachineConfigurationStatusApplyConfiguration) WithManagedBootImagesStatus(value *ManagedBootImagesApplyConfiguration) *MachineConfigurationStatusApplyConfiguration {
	b.ManagedBootImagesStatus = value
	return b
}
