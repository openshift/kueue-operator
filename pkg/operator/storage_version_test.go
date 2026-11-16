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

package operator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/utils/clock"

	"github.com/openshift/kueue-operator/bindata"
)

// TestOperatorClusterRoleAllowsStorageVersionMigration checks that the
// operator may rewrite the objects of every Kueue CRD it applies and prune
// their stored versions, as manageCustomResources requires.
func TestOperatorClusterRoleAllowsStorageVersionMigration(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "02_clusterrole.yaml"))
	if err != nil {
		t.Fatalf("failed to read the operator ClusterRole: %v", err)
	}
	role := resourceread.ReadClusterRoleV1OrDie(data)

	if !allows(role, "apiextensions.k8s.io", "customresourcedefinitions", "get") ||
		!allows(role, "apiextensions.k8s.io", "customresourcedefinitions/status", "update") {
		t.Errorf("the operator ClusterRole does not allow getting CRDs and updating their status")
	}

	crds, err := kueueCustomResourceDefinitions()
	if err != nil {
		t.Fatalf("failed to read the CRD assets: %v", err)
	}
	if len(crds) == 0 {
		t.Fatalf("no CRD assets found")
	}
	for _, crd := range crds {
		for _, verb := range []string{"list", "patch"} {
			if !allows(role, crd.Spec.Group, crd.Spec.Names.Plural, verb) {
				t.Errorf("the operator ClusterRole does not allow %s on %s.%s", verb, crd.Spec.Names.Plural, crd.Spec.Group)
			}
		}
	}
}

func allows(role *rbacv1.ClusterRole, group, resource, verb string) bool {
	matches := func(values []string, value string) bool {
		return slices.Contains(values, value) || slices.Contains(values, rbacv1.ResourceAll)
	}
	for _, rule := range role.Rules {
		if matches(rule.APIGroups, group) && matches(rule.Resources, resource) && matches(rule.Verbs, verb) {
			return true
		}
	}
	return false
}

// fakeCRDClient serves a single live CRD; methods that are not overridden
// panic.
type fakeCRDClient struct {
	apiextv1.ApiextensionsV1Interface
	apiextv1.CustomResourceDefinitionInterface
	crd           *apiextensionsv1.CustomResourceDefinition
	gets          int
	statusUpdates [][]string
}

func (f *fakeCRDClient) CustomResourceDefinitions() apiextv1.CustomResourceDefinitionInterface {
	return f
}

func (f *fakeCRDClient) Get(_ context.Context, name string, _ metav1.GetOptions) (*apiextensionsv1.CustomResourceDefinition, error) {
	f.gets++
	if f.crd == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"}, name)
	}
	return f.crd.DeepCopy(), nil
}

func (f *fakeCRDClient) UpdateStatus(_ context.Context, crd *apiextensionsv1.CustomResourceDefinition, _ metav1.UpdateOptions) (*apiextensionsv1.CustomResourceDefinition, error) {
	f.statusUpdates = append(f.statusUpdates, crd.Status.StoredVersions)
	f.crd = crd.DeepCopy()
	return crd, nil
}

// embeddedCRD returns the embedded Kueue CRD with the given plural.
func embeddedCRD(t *testing.T, plural string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	crds, err := kueueCustomResourceDefinitions()
	if err != nil {
		t.Fatalf("failed to read the CRD assets: %v", err)
	}
	for _, crd := range crds {
		if crd.Spec.Names.Plural == plural {
			return crd
		}
	}
	t.Fatalf("no embedded CRD for %s", plural)
	return nil
}

// installedCRD returns crd as an earlier release left it: v1beta1 is still
// listed and in status.storedVersions, objects are stored as storageVersion.
func installedCRD(crd *apiextensionsv1.CustomResourceDefinition, storageVersion string) *apiextensionsv1.CustomResourceDefinition {
	installed := crd.DeepCopy()
	for i := range installed.Spec.Versions {
		installed.Spec.Versions[i].Storage = installed.Spec.Versions[i].Name == storageVersion
	}
	installed.Spec.Versions = append(installed.Spec.Versions, apiextensionsv1.CustomResourceDefinitionVersion{
		Name: "v1beta1", Served: true, Storage: storageVersion == "v1beta1",
	})
	installed.Status.StoredVersions = []string{"v1beta1", "v1beta2"}
	return installed
}

func eventReasons(recorder events.InMemoryRecorder) map[string]string {
	reasons := map[string]string{}
	for _, event := range recorder.Events() {
		reasons[event.Reason] = event.Type
	}
	return reasons
}

func TestMigrateStoredVersions(t *testing.T) {
	required := embeddedCRD(t, "localqueues")
	gvr := schema.GroupVersionResource{Group: required.Spec.Group, Version: "v1beta2", Resource: "localqueues"}
	queue := &unstructured.Unstructured{}
	queue.SetAPIVersion(gvr.GroupVersion().String())
	queue.SetKind("LocalQueue")
	queue.SetNamespace("team-a")
	queue.SetName("user-queue")

	testCases := map[string]struct {
		installed      *apiextensionsv1.CustomResourceDefinition
		wantErr        bool
		wantPrunes     [][]string
		wantPatches    int
		wantEvents     map[string]string
		wantVerified   bool
		wantGetsByCall []int
	}{
		"migrates the live stale stored versions": {
			installed:      installedCRD(required, "v1beta2"),
			wantPrunes:     [][]string{{"v1beta2"}},
			wantPatches:    1,
			wantEvents:     map[string]string{"StorageVersionMigrated": corev1.EventTypeNormal},
			wantVerified:   true,
			wantGetsByCall: []int{2, 2},
		},
		"skips a CRD that is not installed": {
			wantVerified:   true,
			wantEvents:     map[string]string{},
			wantGetsByCall: []int{1, 1},
		},
		"fails and retries when the installed CRD stores a dropped version": {
			installed:      installedCRD(required, "v1beta1"),
			wantErr:        true,
			wantEvents:     map[string]string{"StorageVersionMigrationFailed": corev1.EventTypeWarning},
			wantGetsByCall: []int{1, 2},
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			crdClient := &fakeCRDClient{crd: tc.installed}
			dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
				map[schema.GroupVersionResource]string{gvr: "LocalQueueList"}, queue.DeepCopy())
			recorder := events.NewInMemoryRecorder("test", clock.RealClock{})
			c := &TargetConfigReconciler{crdClient: crdClient, dynamicClient: dynamicClient, eventRecorder: recorder}

			gets := make([]int, 0, 2)
			for range 2 {
				err := c.migrateStoredVersions(t.Context(), required)
				if (err != nil) != tc.wantErr {
					t.Fatalf("got error %v, want error %t", err, tc.wantErr)
				}
				gets = append(gets, crdClient.gets)
			}

			if diff := cmp.Diff(tc.wantGetsByCall, gets); diff != "" {
				t.Errorf("unexpected CRD reads after each call (-want,+got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantPrunes, crdClient.statusUpdates); diff != "" {
				t.Errorf("unexpected status updates (-want,+got):\n%s", diff)
			}
			patches := 0
			for _, action := range dynamicClient.Actions() {
				if _, ok := action.(clienttesting.PatchAction); ok {
					patches++
				}
			}
			if patches != tc.wantPatches {
				t.Errorf("got %d patches, want %d", patches, tc.wantPatches)
			}
			if diff := cmp.Diff(tc.wantEvents, eventReasons(recorder)); diff != "" {
				t.Errorf("unexpected events (-want,+got):\n%s", diff)
			}
			if _, verified := c.storedVersionsVerified.Load(required.Name); verified != tc.wantVerified {
				t.Errorf("got verified %t, want %t", verified, tc.wantVerified)
			}
		})
	}
}

// bundledNonAlphaCRDNames lists the embedded CRDs without an alpha version,
// independently of kueueCustomResourceDefinitions.
func bundledNonAlphaCRDNames(t *testing.T) []string {
	t.Helper()
	crdDir := "assets/kueue-operator/crds"
	files, err := bindata.AssetDir(crdDir)
	if err != nil {
		t.Fatalf("failed to read the CRD assets: %v", err)
	}
	var names []string
	for _, file := range files {
		crd := resourceread.ReadCustomResourceDefinitionV1OrDie(bindata.MustAsset(filepath.Join(crdDir, file)))
		if slices.ContainsFunc(crd.Spec.Versions, func(v apiextensionsv1.CustomResourceDefinitionVersion) bool {
			return strings.HasPrefix(v.Name, "v1alpha")
		}) {
			continue
		}
		names = append(names, crd.Name)
	}
	slices.Sort(names)
	return names
}

// TestManageCustomResourcesMigratesBeforeApplying checks that the stored
// objects of every embedded CRD are migrated before any CRD is applied, and
// that a failed migration applies nothing.
func TestManageCustomResourcesMigratesBeforeApplying(t *testing.T) {
	const namespace = "openshift-kueue-operator"
	all := bundledNonAlphaCRDNames(t)
	if !slices.Contains(all, "workloads.kueue.x-k8s.io") || !slices.Contains(all, "localqueues.kueue.x-k8s.io") {
		t.Fatalf("unexpected embedded CRDs: %v", all)
	}

	testCases := map[string]struct {
		failMigration  string
		failApply      string
		wantMigrations []string
		wantApplies    []string
		wantErr        bool
	}{
		"every CRD is migrated before the first is applied": {
			wantMigrations: all,
			wantApplies:    all,
		},
		"a failed migration stops the migrations and applies no CRD": {
			failMigration:  "localqueues.kueue.x-k8s.io",
			wantMigrations: all[:slices.Index(all, "localqueues.kueue.x-k8s.io")+1],
			wantErr:        true,
		},
		"a failed apply happens after every migration": {
			failApply:      all[0],
			wantMigrations: all,
			wantApplies:    all[:1],
			wantErr:        true,
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			var steps []string
			var migrations, applies []string
			migrate := func(_ context.Context, crd *apiextensionsv1.CustomResourceDefinition) error {
				steps = append(steps, "migrate")
				migrations = append(migrations, crd.Name)
				if crd.Name == tc.failMigration {
					return errors.New("migration failed")
				}
				return nil
			}
			apply := func(_ context.Context, crd *apiextensionsv1.CustomResourceDefinition) (*apiextensionsv1.CustomResourceDefinition, bool, error) {
				steps = append(steps, "apply")
				applies = append(applies, crd.Name)
				if crd.Name == tc.failApply {
					return nil, false, errors.New("apply failed")
				}
				if crd.Annotations["cert-manager.io/inject-ca-from"] == "" {
					t.Errorf("CRD %s is applied without the CA injection annotation", crd.Name)
				}
				if conv := crd.Spec.Conversion; conv != nil && conv.Strategy == apiextensionsv1.WebhookConverter {
					if conv.Webhook == nil || conv.Webhook.ClientConfig == nil || conv.Webhook.ClientConfig.Service == nil ||
						conv.Webhook.ClientConfig.Service.Namespace != namespace {
						t.Errorf("CRD %s does not convert through the operator namespace: %+v", crd.Name, conv.Webhook)
					}
				}
				return crd, true, nil
			}
			specAnnotations := map[string]string{}
			c := &TargetConfigReconciler{operatorNamespace: namespace}

			err := c.manageCustomResourcesWith(t.Context(), specAnnotations, migrate, apply)
			if (err != nil) != tc.wantErr {
				t.Fatalf("got error %v, want error %t", err, tc.wantErr)
			}

			// The embedded CRDs are processed in asset order, which sorts by name.
			if diff := cmp.Diff(tc.wantMigrations, migrations); diff != "" {
				t.Errorf("unexpected migrations (-want,+got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantApplies, applies); diff != "" {
				t.Errorf("unexpected applies (-want,+got):\n%s", diff)
			}
			if firstApply := slices.Index(steps, "apply"); firstApply >= 0 && slices.Contains(steps[firstApply:], "migrate") {
				t.Errorf("a migration ran after a CRD was applied: %v", steps)
			}
			if tc.failApply == "" && tc.failMigration == "" && len(specAnnotations) != len(all) {
				t.Errorf("got %d CRD spec annotations, want %d", len(specAnnotations), len(all))
			}
		})
	}
}
