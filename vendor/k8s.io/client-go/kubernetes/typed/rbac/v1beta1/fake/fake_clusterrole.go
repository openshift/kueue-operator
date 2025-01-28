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
	v1beta1 "k8s.io/api/rbac/v1beta1"
	rbacv1beta1 "k8s.io/client-go/applyconfigurations/rbac/v1beta1"
	gentype "k8s.io/client-go/gentype"
	typedrbacv1beta1 "k8s.io/client-go/kubernetes/typed/rbac/v1beta1"
)

// fakeClusterRoles implements ClusterRoleInterface
type fakeClusterRoles struct {
	*gentype.FakeClientWithListAndApply[*v1beta1.ClusterRole, *v1beta1.ClusterRoleList, *rbacv1beta1.ClusterRoleApplyConfiguration]
	Fake *FakeRbacV1beta1
}

func newFakeClusterRoles(fake *FakeRbacV1beta1) typedrbacv1beta1.ClusterRoleInterface {
	return &fakeClusterRoles{
		gentype.NewFakeClientWithListAndApply[*v1beta1.ClusterRole, *v1beta1.ClusterRoleList, *rbacv1beta1.ClusterRoleApplyConfiguration](
			fake.Fake,
			"",
			v1beta1.SchemeGroupVersion.WithResource("clusterroles"),
			v1beta1.SchemeGroupVersion.WithKind("ClusterRole"),
			func() *v1beta1.ClusterRole { return &v1beta1.ClusterRole{} },
			func() *v1beta1.ClusterRoleList { return &v1beta1.ClusterRoleList{} },
			func(dst, src *v1beta1.ClusterRoleList) { dst.ListMeta = src.ListMeta },
			func(list *v1beta1.ClusterRoleList) []*v1beta1.ClusterRole { return gentype.ToPointerSlice(list.Items) },
			func(list *v1beta1.ClusterRoleList, items []*v1beta1.ClusterRole) {
				list.Items = gentype.FromPointerSlice(items)
			},
		),
		fake,
	}
}