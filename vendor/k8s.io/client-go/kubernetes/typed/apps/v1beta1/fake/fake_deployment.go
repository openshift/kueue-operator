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
	v1beta1 "k8s.io/api/apps/v1beta1"
	appsv1beta1 "k8s.io/client-go/applyconfigurations/apps/v1beta1"
	gentype "k8s.io/client-go/gentype"
	typedappsv1beta1 "k8s.io/client-go/kubernetes/typed/apps/v1beta1"
)

// fakeDeployments implements DeploymentInterface
type fakeDeployments struct {
	*gentype.FakeClientWithListAndApply[*v1beta1.Deployment, *v1beta1.DeploymentList, *appsv1beta1.DeploymentApplyConfiguration]
	Fake *FakeAppsV1beta1
}

func newFakeDeployments(fake *FakeAppsV1beta1, namespace string) typedappsv1beta1.DeploymentInterface {
	return &fakeDeployments{
		gentype.NewFakeClientWithListAndApply[*v1beta1.Deployment, *v1beta1.DeploymentList, *appsv1beta1.DeploymentApplyConfiguration](
			fake.Fake,
			namespace,
			v1beta1.SchemeGroupVersion.WithResource("deployments"),
			v1beta1.SchemeGroupVersion.WithKind("Deployment"),
			func() *v1beta1.Deployment { return &v1beta1.Deployment{} },
			func() *v1beta1.DeploymentList { return &v1beta1.DeploymentList{} },
			func(dst, src *v1beta1.DeploymentList) { dst.ListMeta = src.ListMeta },
			func(list *v1beta1.DeploymentList) []*v1beta1.Deployment { return gentype.ToPointerSlice(list.Items) },
			func(list *v1beta1.DeploymentList, items []*v1beta1.Deployment) {
				list.Items = gentype.FromPointerSlice(items)
			},
		),
		fake,
	}
}