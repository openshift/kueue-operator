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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

var localQueues = schema.GroupVersionResource{Group: "kueue.x-k8s.io", Version: "v1beta2", Resource: "localqueues"}

func crd(storedVersions []string, versions ...apiextensionsv1.CustomResourceDefinitionVersion) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "localqueues.kueue.x-k8s.io"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group:    "kueue.x-k8s.io",
			Names:    apiextensionsv1.CustomResourceDefinitionNames{Plural: "localqueues"},
			Scope:    apiextensionsv1.NamespaceScoped,
			Versions: versions,
		},
		Status: apiextensionsv1.CustomResourceDefinitionStatus{StoredVersions: storedVersions},
	}
}

func version(name string, served, storage bool) apiextensionsv1.CustomResourceDefinitionVersion {
	return apiextensionsv1.CustomResourceDefinitionVersion{Name: name, Served: served, Storage: storage}
}

// upgradedCRD is a CRD that switched its storage from v1beta1 to v1beta2.
func upgradedCRD() *apiextensionsv1.CustomResourceDefinition {
	c := crd([]string{"v1beta1", "v1beta2"}, version("v1beta1", true, false), version("v1beta2", true, true))
	c.Generation = 3
	return c
}

// requiredCRD is the CRD that drops v1beta1.
func requiredCRD() *apiextensionsv1.CustomResourceDefinition {
	return crd(nil, version("v1beta2", true, true))
}

// fakeCRDs serves a single CRD; methods that are not overridden panic.
type fakeCRDs struct {
	apiextv1.CustomResourceDefinitionInterface
	crd             *apiextensionsv1.CustomResourceDefinition
	statusConflicts int
	statusUpdates   [][]string
}

func (f *fakeCRDs) CustomResourceDefinitions() apiextv1.CustomResourceDefinitionInterface {
	return f
}

func (f *fakeCRDs) Get(_ context.Context, name string, _ metav1.GetOptions) (*apiextensionsv1.CustomResourceDefinition, error) {
	if f.crd == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"}, name)
	}
	return f.crd.DeepCopy(), nil
}

func (f *fakeCRDs) UpdateStatus(_ context.Context, crd *apiextensionsv1.CustomResourceDefinition, _ metav1.UpdateOptions) (*apiextensionsv1.CustomResourceDefinition, error) {
	f.statusUpdates = append(f.statusUpdates, crd.Status.StoredVersions)
	if f.statusConflicts > 0 {
		f.statusConflicts--
		return nil, apierrors.NewConflict(schema.GroupResource{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"}, crd.Name, errors.New("modified"))
	}
	f.crd = crd.DeepCopy()
	return crd, nil
}

func localQueue(namespace, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("kueue.x-k8s.io/v1beta2")
	u.SetKind("LocalQueue")
	u.SetNamespace(namespace)
	u.SetName(name)
	return u
}

func newDynamicClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{localQueues: "LocalQueueList"}, objects...)
}

// patches returns the namespaced names of the patched objects.
func patches(t *testing.T, client *dynamicfake.FakeDynamicClient) []types.NamespacedName {
	t.Helper()
	var keys []types.NamespacedName
	for _, action := range client.Actions() {
		patch, ok := action.(clienttesting.PatchAction)
		if !ok {
			continue
		}
		if patch.GetPatchType() != types.MergePatchType || string(patch.GetPatch()) != "{}" {
			t.Errorf("unexpected patch %s %q", patch.GetPatchType(), patch.GetPatch())
		}
		keys = append(keys, types.NamespacedName{Namespace: patch.GetNamespace(), Name: patch.GetName()})
	}
	return keys
}

func TestStaleStoredVersions(t *testing.T) {
	testCases := map[string]struct {
		stored   []string
		required []apiextensionsv1.CustomResourceDefinitionVersion
		want     []string
	}{
		"dropped version": {
			stored:   []string{"v1beta1", "v1beta2"},
			required: []apiextensionsv1.CustomResourceDefinitionVersion{version("v1beta2", true, true)},
			want:     []string{"v1beta1"},
		},
		"all versions kept": {
			stored:   []string{"v1beta1", "v1beta2"},
			required: []apiextensionsv1.CustomResourceDefinitionVersion{version("v1beta1", false, false), version("v1beta2", true, true)},
		},
		"only current version stored": {
			stored:   []string{"v1beta2"},
			required: []apiextensionsv1.CustomResourceDefinitionVersion{version("v1beta2", true, true)},
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			got := StaleStoredVersions(crd(tc.stored), crd(nil, tc.required...))
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("unexpected stale versions (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestMigrateDoesNothing(t *testing.T) {
	testCases := map[string]*apiextensionsv1.CustomResourceDefinition{
		"CRD not installed": nil,
		"no stale version":  crd([]string{"v1beta2"}, version("v1beta1", true, false), version("v1beta2", true, true)),
	}
	for name, existing := range testCases {
		t.Run(name, func(t *testing.T) {
			crds := &fakeCRDs{crd: existing}
			// A nil dynamic client panics if the migration lists or patches objects.
			result, err := Migrate(t.Context(), crds, nil, requiredCRD())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(Result{}, result); diff != "" {
				t.Errorf("unexpected result (-want,+got):\n%s", diff)
			}
			if len(crds.statusUpdates) != 0 {
				t.Errorf("unexpected status updates: %v", crds.statusUpdates)
			}
		})
	}
}

func TestMigrateRefuses(t *testing.T) {
	testCases := map[string]struct {
		existing *apiextensionsv1.CustomResourceDefinition
		wantErr  string
	}{
		"storage version dropped by the new CRD": {
			existing: crd([]string{"v1beta1"}, version("v1beta1", true, true)),
			wantErr:  "stores objects as v1beta1, which the new definition does not list",
		},
		"storage version not served": {
			existing: crd([]string{"v1beta1", "v1beta2"}, version("v1beta1", true, false), version("v1beta2", false, true)),
			wantErr:  "storage version v1beta2 of CustomResourceDefinition localqueues.kueue.x-k8s.io is not served",
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			crds := &fakeCRDs{crd: tc.existing}
			_, err := Migrate(t.Context(), crds, nil, requiredCRD())
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got error %v, want %q", err, tc.wantErr)
			}
			if len(crds.statusUpdates) != 0 {
				t.Errorf("unexpected status updates: %v", crds.statusUpdates)
			}
		})
	}
}

func TestMigrateRewritesObjectsAndPrunes(t *testing.T) {
	dynamicClient := newDynamicClient(
		localQueue("team-a", "queue"),
		localQueue("team-b", "deleted"),
		localQueue("team-b", "queue"),
	)
	dynamicClient.PrependReactor("patch", "localqueues", func(action clienttesting.Action) (bool, runtime.Object, error) {
		if action.(clienttesting.PatchAction).GetName() == "deleted" {
			return true, nil, apierrors.NewNotFound(localQueues.GroupResource(), "deleted")
		}
		return false, nil, nil
	})
	crds := &fakeCRDs{crd: upgradedCRD()}

	result, err := Migrate(t.Context(), crds, dynamicClient, requiredCRD())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Result{StaleVersions: []string{"v1beta1"}, StorageVersion: "v1beta2", Rewritten: 3}
	if diff := cmp.Diff(want, result); diff != "" {
		t.Errorf("unexpected result (-want,+got):\n%s", diff)
	}
	if diff := cmp.Diff([][]string{{"v1beta2"}}, crds.statusUpdates); diff != "" {
		t.Errorf("unexpected status updates (-want,+got):\n%s", diff)
	}
	wantPatches := []types.NamespacedName{{Namespace: "team-a", Name: "queue"}, {Namespace: "team-b", Name: "deleted"}, {Namespace: "team-b", Name: "queue"}}
	if diff := cmp.Diff(wantPatches, patches(t, dynamicClient)); diff != "" {
		t.Errorf("unexpected patches (-want,+got):\n%s", diff)
	}
}

func TestMigratePruneRetriesOnConflict(t *testing.T) {
	crds := &fakeCRDs{crd: upgradedCRD(), statusConflicts: 1}

	if _, err := Migrate(t.Context(), crds, newDynamicClient(localQueue("team-a", "queue")), requiredCRD()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff([][]string{{"v1beta2"}, {"v1beta2"}}, crds.statusUpdates); diff != "" {
		t.Errorf("unexpected status updates (-want,+got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"v1beta2"}, crds.crd.Status.StoredVersions); diff != "" {
		t.Errorf("unexpected stored versions (-want,+got):\n%s", diff)
	}
}

func TestMigrateDoesNotPrune(t *testing.T) {
	testCases := map[string]struct {
		reactor func(*fakeCRDs) (verb string, fn clienttesting.ReactionFunc)
		wantErr string
	}{
		"list fails": {
			reactor: func(*fakeCRDs) (string, clienttesting.ReactionFunc) {
				return "list", func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, apierrors.NewServiceUnavailable("conversion webhook unavailable")
				}
			},
			wantErr: "conversion webhook unavailable",
		},
		"rewrite rejected": {
			reactor: func(*fakeCRDs) (string, clienttesting.ReactionFunc) {
				return "patch", func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, apierrors.NewForbidden(localQueues.GroupResource(), "queue", errors.New("denied"))
				}
			},
			wantErr: "failed to rewrite localqueues.kueue.x-k8s.io team-a/queue in v1beta2",
		},
		"CRD changed during the migration": {
			reactor: func(crds *fakeCRDs) (string, clienttesting.ReactionFunc) {
				return "patch", func(clienttesting.Action) (bool, runtime.Object, error) {
					crds.crd.Generation++
					return false, nil, nil
				}
			},
			wantErr: "CustomResourceDefinition changed during the migration (generation 3, now 4)",
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			crds := &fakeCRDs{crd: upgradedCRD()}
			dynamicClient := newDynamicClient(localQueue("team-a", "queue"))
			verb, reaction := tc.reactor(crds)
			dynamicClient.PrependReactor(verb, "localqueues", reaction)

			_, err := Migrate(t.Context(), crds, dynamicClient, requiredCRD())
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got error %v, want %q", err, tc.wantErr)
			}
			if len(crds.statusUpdates) != 0 {
				t.Errorf("stored versions were pruned: %v", crds.statusUpdates)
			}
		})
	}
}
