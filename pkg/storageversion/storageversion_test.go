/*
Copyright 2026.

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

package storageversion

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const crdName = "localqueues.kueue.x-k8s.io"

func crdWithVersions(storedVersions []string, versions ...string) *apiextensionsv1.CustomResourceDefinition {
	crd := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: crdName}}
	for i, v := range versions {
		crd.Spec.Versions = append(crd.Spec.Versions, apiextensionsv1.CustomResourceDefinitionVersion{
			Name: v, Served: true, Storage: i == len(versions)-1,
		})
	}
	crd.Status.StoredVersions = storedVersions
	return crd
}

// readOnlyCRDClient serves the installed CRD, if any. It implements only Get:
// any write panics through the nil embedded interface.
type readOnlyCRDClient struct {
	apiextv1.CustomResourceDefinitionInterface
	installed *apiextensionsv1.CustomResourceDefinition
	getErr    error
	gets      int
}

func (f *readOnlyCRDClient) CustomResourceDefinitions() apiextv1.CustomResourceDefinitionInterface {
	return f
}

func (f *readOnlyCRDClient) Get(_ context.Context, name string, _ metav1.GetOptions) (*apiextensionsv1.CustomResourceDefinition, error) {
	f.gets++
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.installed == nil || f.installed.Name != name {
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"}, name)
	}
	return f.installed.DeepCopy(), nil
}

func TestStaleStoredVersions(t *testing.T) {
	required := crdWithVersions(nil, "v1beta2")
	testCases := map[string]struct {
		storedVersions []string
		want           []string
	}{
		"nothing stored":            {},
		"only a listed version":     {storedVersions: []string{"v1beta2"}},
		"a dropped version":         {storedVersions: []string{"v1beta1", "v1beta2"}, want: []string{"v1beta1"}},
		"only dropped versions":     {storedVersions: []string{"v1alpha1", "v1beta1"}, want: []string{"v1alpha1", "v1beta1"}},
		"dropped versions in order": {storedVersions: []string{"v1beta1", "v1beta2", "v1alpha1"}, want: []string{"v1beta1", "v1alpha1"}},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			got := staleStoredVersions(crdWithVersions(tc.storedVersions, "v1beta1", "v1beta2"), required)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("unexpected stale versions (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	required := crdWithVersions(nil, "v1beta2")
	testCases := map[string]struct {
		installed        *apiextensionsv1.CustomResourceDefinition
		getErr           error
		wantIncompatible *IncompatibleError
		wantErr          bool
	}{
		"a CRD that is not installed passes": {},
		"an installed CRD that stores only listed versions passes": {
			installed: crdWithVersions([]string{"v1beta2"}, "v1beta1", "v1beta2"),
		},
		"an installed CRD that matches the new definition passes": {
			installed: crdWithVersions([]string{"v1beta2"}, "v1beta2"),
		},
		"an installed CRD that still records a dropped stored version is rejected": {
			installed: crdWithVersions([]string{"v1beta1", "v1beta2"}, "v1beta1", "v1beta2"),
			wantIncompatible: &IncompatibleError{
				Name: crdName, StaleVersions: []string{"v1beta1"}, Versions: []string{"v1beta2"},
			},
			wantErr: true,
		},
		"an installed CRD that stores objects in a dropped version is rejected": {
			installed: crdWithVersions([]string{"v1beta1"}, "v1beta2", "v1beta1"),
			wantIncompatible: &IncompatibleError{
				Name: crdName, StaleVersions: []string{"v1beta1"}, Versions: []string{"v1beta2"},
			},
			wantErr: true,
		},
		"a failed read fails closed": {
			getErr:  apierrors.NewForbidden(schema.GroupResource{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"}, crdName, errors.New("denied")),
			wantErr: true,
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			client := &readOnlyCRDClient{installed: tc.installed, getErr: tc.getErr}

			err := Check(t.Context(), client, required)
			if (err != nil) != tc.wantErr {
				t.Fatalf("got error %v, want error %t", err, tc.wantErr)
			}
			if client.gets != 1 {
				t.Errorf("got %d reads of the installed CRD, want 1", client.gets)
			}

			var incompatible *IncompatibleError
			if errors.As(err, &incompatible) != (tc.wantIncompatible != nil) {
				t.Fatalf("got error %v, want IncompatibleError %v", err, tc.wantIncompatible)
			}
			if tc.wantIncompatible == nil {
				return
			}
			if diff := cmp.Diff(tc.wantIncompatible, incompatible); diff != "" {
				t.Errorf("unexpected IncompatibleError (-want,+got):\n%s", diff)
			}
			for _, want := range []string{crdName, "status.storedVersions", "complete the storage migration before upgrading", "remove [v1beta1]"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}
