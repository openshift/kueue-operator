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
	context "context"

	authenticationv1 "k8s.io/api/authentication/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/client-go/applyconfigurations/core/v1"
	gentype "k8s.io/client-go/gentype"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	testing "k8s.io/client-go/testing"
)

// fakeServiceAccounts implements ServiceAccountInterface
type fakeServiceAccounts struct {
	*gentype.FakeClientWithListAndApply[*v1.ServiceAccount, *v1.ServiceAccountList, *corev1.ServiceAccountApplyConfiguration]
	Fake *FakeCoreV1
}

func newFakeServiceAccounts(fake *FakeCoreV1, namespace string) typedcorev1.ServiceAccountInterface {
	return &fakeServiceAccounts{
		gentype.NewFakeClientWithListAndApply[*v1.ServiceAccount, *v1.ServiceAccountList, *corev1.ServiceAccountApplyConfiguration](
			fake.Fake,
			namespace,
			v1.SchemeGroupVersion.WithResource("serviceaccounts"),
			v1.SchemeGroupVersion.WithKind("ServiceAccount"),
			func() *v1.ServiceAccount { return &v1.ServiceAccount{} },
			func() *v1.ServiceAccountList { return &v1.ServiceAccountList{} },
			func(dst, src *v1.ServiceAccountList) { dst.ListMeta = src.ListMeta },
			func(list *v1.ServiceAccountList) []*v1.ServiceAccount { return gentype.ToPointerSlice(list.Items) },
			func(list *v1.ServiceAccountList, items []*v1.ServiceAccount) {
				list.Items = gentype.FromPointerSlice(items)
			},
		),
		fake,
	}
}

// CreateToken takes the representation of a tokenRequest and creates it.  Returns the server's representation of the tokenRequest, and an error, if there is any.
func (c *fakeServiceAccounts) CreateToken(ctx context.Context, serviceAccountName string, tokenRequest *authenticationv1.TokenRequest, opts metav1.CreateOptions) (result *authenticationv1.TokenRequest, err error) {
	emptyResult := &authenticationv1.TokenRequest{}
	obj, err := c.Fake.
		Invokes(testing.NewCreateSubresourceActionWithOptions(c.Resource(), serviceAccountName, "token", c.Namespace(), tokenRequest, opts), emptyResult)

	if obj == nil {
		return emptyResult, err
	}
	return obj.(*authenticationv1.TokenRequest), err
}