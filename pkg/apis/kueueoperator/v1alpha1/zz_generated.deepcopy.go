//go:build !ignore_autogenerated
// +build !ignore_autogenerated

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
// Code generated by deepcopy-gen. DO NOT EDIT.

package v1alpha1

import (
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	v1beta1 "sigs.k8s.io/kueue/apis/config/v1beta1"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Kueue) DeepCopyInto(out *Kueue) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Kueue.
func (in *Kueue) DeepCopy() *Kueue {
	if in == nil {
		return nil
	}
	out := new(Kueue)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *Kueue) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *KueueConfiguration) DeepCopyInto(out *KueueConfiguration) {
	*out = *in
	if in.WaitForPodsReady != nil {
		in, out := &in.WaitForPodsReady, &out.WaitForPodsReady
		*out = new(v1beta1.WaitForPodsReady)
		(*in).DeepCopyInto(*out)
	}
	in.Integrations.DeepCopyInto(&out.Integrations)
	if in.FeatureGates != nil {
		in, out := &in.FeatureGates, &out.FeatureGates
		*out = make(map[string]bool, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.Resources != nil {
		in, out := &in.Resources, &out.Resources
		*out = new(v1beta1.Resources)
		(*in).DeepCopyInto(*out)
	}
	if in.ManageJobsWithoutQueueName != nil {
		in, out := &in.ManageJobsWithoutQueueName, &out.ManageJobsWithoutQueueName
		*out = new(ManageJobsWithoutQueueNameOption)
		**out = **in
	}
	if in.ManagedJobsNamespaceSelector != nil {
		in, out := &in.ManagedJobsNamespaceSelector, &out.ManagedJobsNamespaceSelector
		*out = new(v1.LabelSelector)
		(*in).DeepCopyInto(*out)
	}
	if in.FairSharing != nil {
		in, out := &in.FairSharing, &out.FairSharing
		*out = new(v1beta1.FairSharing)
		(*in).DeepCopyInto(*out)
	}
	if in.DisableMetrics != nil {
		in, out := &in.DisableMetrics, &out.DisableMetrics
		*out = new(bool)
		**out = **in
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new KueueConfiguration.
func (in *KueueConfiguration) DeepCopy() *KueueConfiguration {
	if in == nil {
		return nil
	}
	out := new(KueueConfiguration)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *KueueList) DeepCopyInto(out *KueueList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]Kueue, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new KueueList.
func (in *KueueList) DeepCopy() *KueueList {
	if in == nil {
		return nil
	}
	out := new(KueueList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *KueueList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *KueueOperandSpec) DeepCopyInto(out *KueueOperandSpec) {
	*out = *in
	in.OperatorSpec.DeepCopyInto(&out.OperatorSpec)
	in.Config.DeepCopyInto(&out.Config)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new KueueOperandSpec.
func (in *KueueOperandSpec) DeepCopy() *KueueOperandSpec {
	if in == nil {
		return nil
	}
	out := new(KueueOperandSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *KueueStatus) DeepCopyInto(out *KueueStatus) {
	*out = *in
	in.OperatorStatus.DeepCopyInto(&out.OperatorStatus)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new KueueStatus.
func (in *KueueStatus) DeepCopy() *KueueStatus {
	if in == nil {
		return nil
	}
	out := new(KueueStatus)
	in.DeepCopyInto(out)
	return out
}
