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
// Code generated by client-gen. DO NOT EDIT.

package v1alpha1

import (
	context "context"

	kueueoperatorv1alpha1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1alpha1"
	applyconfigurationkueueoperatorv1alpha1 "github.com/openshift/kueue-operator/pkg/generated/applyconfiguration/kueueoperator/v1alpha1"
	scheme "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned/scheme"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	gentype "k8s.io/client-go/gentype"
)

// KueuesGetter has a method to return a KueueInterface.
// A group's client should implement this interface.
type KueuesGetter interface {
	Kueues(namespace string) KueueInterface
}

// KueueInterface has methods to work with Kueue resources.
type KueueInterface interface {
	Create(ctx context.Context, kueue *kueueoperatorv1alpha1.Kueue, opts v1.CreateOptions) (*kueueoperatorv1alpha1.Kueue, error)
	Update(ctx context.Context, kueue *kueueoperatorv1alpha1.Kueue, opts v1.UpdateOptions) (*kueueoperatorv1alpha1.Kueue, error)
	// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
	UpdateStatus(ctx context.Context, kueue *kueueoperatorv1alpha1.Kueue, opts v1.UpdateOptions) (*kueueoperatorv1alpha1.Kueue, error)
	Delete(ctx context.Context, name string, opts v1.DeleteOptions) error
	DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error
	Get(ctx context.Context, name string, opts v1.GetOptions) (*kueueoperatorv1alpha1.Kueue, error)
	List(ctx context.Context, opts v1.ListOptions) (*kueueoperatorv1alpha1.KueueList, error)
	Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error)
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *kueueoperatorv1alpha1.Kueue, err error)
	Apply(ctx context.Context, kueue *applyconfigurationkueueoperatorv1alpha1.KueueApplyConfiguration, opts v1.ApplyOptions) (result *kueueoperatorv1alpha1.Kueue, err error)
	// Add a +genclient:noStatus comment above the type to avoid generating ApplyStatus().
	ApplyStatus(ctx context.Context, kueue *applyconfigurationkueueoperatorv1alpha1.KueueApplyConfiguration, opts v1.ApplyOptions) (result *kueueoperatorv1alpha1.Kueue, err error)
	KueueExpansion
}

// kueues implements KueueInterface
type kueues struct {
	*gentype.ClientWithListAndApply[*kueueoperatorv1alpha1.Kueue, *kueueoperatorv1alpha1.KueueList, *applyconfigurationkueueoperatorv1alpha1.KueueApplyConfiguration]
}

// newKueues returns a Kueues
func newKueues(c *KueueV1alpha1Client, namespace string) *kueues {
	return &kueues{
		gentype.NewClientWithListAndApply[*kueueoperatorv1alpha1.Kueue, *kueueoperatorv1alpha1.KueueList, *applyconfigurationkueueoperatorv1alpha1.KueueApplyConfiguration](
			"kueues",
			c.RESTClient(),
			scheme.ParameterCodec,
			namespace,
			func() *kueueoperatorv1alpha1.Kueue { return &kueueoperatorv1alpha1.Kueue{} },
			func() *kueueoperatorv1alpha1.KueueList { return &kueueoperatorv1alpha1.KueueList{} },
		),
	}
}