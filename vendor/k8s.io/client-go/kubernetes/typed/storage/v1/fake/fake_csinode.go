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

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	v1 "k8s.io/api/storage/v1"
	storagev1 "k8s.io/client-go/applyconfigurations/storage/v1"
	gentype "k8s.io/client-go/gentype"
	typedstoragev1 "k8s.io/client-go/kubernetes/typed/storage/v1"
)

// fakeCSINodes implements CSINodeInterface
type fakeCSINodes struct {
	*gentype.FakeClientWithListAndApply[*v1.CSINode, *v1.CSINodeList, *storagev1.CSINodeApplyConfiguration]
	Fake *FakeStorageV1
}

func newFakeCSINodes(fake *FakeStorageV1) typedstoragev1.CSINodeInterface {
	return &fakeCSINodes{
		gentype.NewFakeClientWithListAndApply[*v1.CSINode, *v1.CSINodeList, *storagev1.CSINodeApplyConfiguration](
			fake.Fake,
			"",
			v1.SchemeGroupVersion.WithResource("csinodes"),
			v1.SchemeGroupVersion.WithKind("CSINode"),
			func() *v1.CSINode { return &v1.CSINode{} },
			func() *v1.CSINodeList { return &v1.CSINodeList{} },
			func(dst, src *v1.CSINodeList) { dst.ListMeta = src.ListMeta },
			func(list *v1.CSINodeList) []*v1.CSINode { return gentype.ToPointerSlice(list.Items) },
			func(list *v1.CSINodeList, items []*v1.CSINode) { list.Items = gentype.FromPointerSlice(items) },
		),
		fake,
	}
}