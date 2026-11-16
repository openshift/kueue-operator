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
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/clock"

	"github.com/openshift/kueue-operator/bindata"
)

// fakeCRDClient serves the installed CRDs by name. It implements only Get:
// any write panics through the nil embedded interfaces.
type fakeCRDClient struct {
	apiextv1.ApiextensionsV1Interface
	apiextv1.CustomResourceDefinitionInterface
	installed map[string]*apiextensionsv1.CustomResourceDefinition
	gets      int
}

func (f *fakeCRDClient) CustomResourceDefinitions() apiextv1.CustomResourceDefinitionInterface {
	return f
}

func (f *fakeCRDClient) Get(_ context.Context, name string, _ metav1.GetOptions) (*apiextensionsv1.CustomResourceDefinition, error) {
	f.gets++
	crd, ok := f.installed[name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"}, name)
	}
	return crd.DeepCopy(), nil
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

// installedCRD returns crd as an earlier release installed it, still listing
// v1beta1, with the given stored versions.
func installedCRD(crd *apiextensionsv1.CustomResourceDefinition, storedVersions ...string) *apiextensionsv1.CustomResourceDefinition {
	installed := crd.DeepCopy()
	installed.Spec.Versions = append(installed.Spec.Versions, apiextensionsv1.CustomResourceDefinitionVersion{Name: "v1beta1", Served: true})
	installed.Status.StoredVersions = storedVersions
	return installed
}

func eventReasons(recorder events.InMemoryRecorder) map[string]string {
	reasons := map[string]string{}
	for _, event := range recorder.Events() {
		reasons[event.Reason] = event.Type
	}
	return reasons
}

func TestCheckStoredVersions(t *testing.T) {
	required := embeddedCRD(t, "localqueues")

	testCases := map[string]struct {
		installed *apiextensionsv1.CustomResourceDefinition
		// completeMigration removes v1beta1 from the stored versions after
		// the first check, as the administrator does once every object is
		// stored as v1beta2.
		completeMigration bool
		wantErrByCall     []bool
		wantGetsByCall    []int
		wantEvents        map[string]string
		wantVerified      bool
	}{
		"a CRD that is not installed passes and is not read again": {
			wantErrByCall:  []bool{false, false},
			wantGetsByCall: []int{1, 1},
			wantEvents:     map[string]string{},
			wantVerified:   true,
		},
		"an installed CRD that stores only v1beta2 passes and is not read again": {
			installed:      installedCRD(required, "v1beta2"),
			wantErrByCall:  []bool{false, false},
			wantGetsByCall: []int{1, 1},
			wantEvents:     map[string]string{},
			wantVerified:   true,
		},
		"an installed CRD that still records v1beta1 fails on every sync without writing": {
			installed:      installedCRD(required, "v1beta1", "v1beta2"),
			wantErrByCall:  []bool{true, true},
			wantGetsByCall: []int{1, 2},
			wantEvents:     map[string]string{"StoredVersionsCheckFailed": corev1.EventTypeWarning},
		},
		"a sync after the storage migration was completed passes": {
			installed:         installedCRD(required, "v1beta1", "v1beta2"),
			completeMigration: true,
			wantErrByCall:     []bool{true, false},
			wantGetsByCall:    []int{1, 2},
			wantEvents:        map[string]string{"StoredVersionsCheckFailed": corev1.EventTypeWarning},
			wantVerified:      true,
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			crdClient := &fakeCRDClient{installed: map[string]*apiextensionsv1.CustomResourceDefinition{}}
			if tc.installed != nil {
				crdClient.installed[required.Name] = tc.installed
			}
			recorder := events.NewInMemoryRecorder("test", clock.RealClock{})
			c := &TargetConfigReconciler{crdClient: crdClient, eventRecorder: recorder}

			errs := make([]bool, 0, 2)
			gets := make([]int, 0, 2)
			for range 2 {
				err := c.checkStoredVersions(t.Context(), required)
				if err != nil && !strings.Contains(err.Error(), "complete the storage migration before upgrading") {
					t.Errorf("error %q does not state the storage migration prerequisite", err)
				}
				errs = append(errs, err != nil)
				gets = append(gets, crdClient.gets)
				if tc.completeMigration {
					crdClient.installed[required.Name].Status.StoredVersions = []string{"v1beta2"}
				}
			}

			if diff := cmp.Diff(tc.wantErrByCall, errs); diff != "" {
				t.Errorf("unexpected errors by call (-want,+got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantGetsByCall, gets); diff != "" {
				t.Errorf("unexpected CRD reads after each call (-want,+got):\n%s", diff)
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

// TestManageCustomResourcesChecksBeforeApplying checks that every embedded CRD
// is checked before any CRD is applied, and that a failed check applies
// nothing.
func TestManageCustomResourcesChecksBeforeApplying(t *testing.T) {
	const namespace = "openshift-kueue-operator"
	all := bundledNonAlphaCRDNames(t)
	if !slices.Contains(all, "workloads.kueue.x-k8s.io") || !slices.Contains(all, "localqueues.kueue.x-k8s.io") {
		t.Fatalf("unexpected embedded CRDs: %v", all)
	}

	testCases := map[string]struct {
		failChecks  []string
		failApply   string
		wantApplies []string
		wantErr     []string
	}{
		"every CRD is checked before the first is applied": {
			wantApplies: all,
		},
		"one failed check applies no CRD": {
			failChecks: []string{"localqueues.kueue.x-k8s.io"},
			wantErr:    []string{"localqueues.kueue.x-k8s.io"},
		},
		"every failed check is reported and no CRD is applied": {
			failChecks: []string{"localqueues.kueue.x-k8s.io", "workloads.kueue.x-k8s.io"},
			wantErr:    []string{"localqueues.kueue.x-k8s.io", "workloads.kueue.x-k8s.io"},
		},
		"a failed apply happens after every check": {
			failApply:   all[0],
			wantApplies: all[:1],
			wantErr:     []string{"apply failed"},
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			var steps []string
			var checks, applies []string
			check := func(_ context.Context, crd *apiextensionsv1.CustomResourceDefinition) error {
				steps = append(steps, "check")
				checks = append(checks, crd.Name)
				if slices.Contains(tc.failChecks, crd.Name) {
					return errors.New("check failed for " + crd.Name)
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

			err := c.manageCustomResourcesWith(t.Context(), specAnnotations, check, apply)
			if (err != nil) != (len(tc.wantErr) > 0) {
				t.Fatalf("got error %v, want error mentioning %v", err, tc.wantErr)
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}

			// The embedded CRDs are processed in asset order, which sorts by name.
			if diff := cmp.Diff(all, checks); diff != "" {
				t.Errorf("unexpected checks (-want,+got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantApplies, applies); diff != "" {
				t.Errorf("unexpected applies (-want,+got):\n%s", diff)
			}
			if firstApply := slices.Index(steps, "apply"); firstApply >= 0 && slices.Contains(steps[firstApply:], "check") {
				t.Errorf("a check ran after a CRD was applied: %v", steps)
			}
			if len(tc.wantErr) == 0 && len(specAnnotations) != len(all) {
				t.Errorf("got %d CRD spec annotations, want %d", len(specAnnotations), len(all))
			}
		})
	}
}

// TestManageCustomResourcesRejectsStaleStoredVersions drives the real check
// through the start of sync and the CRD step: one installed CRD that still
// records v1beta1 stops every CRD apply, while missing and compatible CRDs
// pass, and the next sync after the storage migration applies every CRD.
func TestManageCustomResourcesRejectsStaleStoredVersions(t *testing.T) {
	all := bundledNonAlphaCRDNames(t)
	localQueues := embeddedCRD(t, "localqueues")
	clusterQueues := embeddedCRD(t, "clusterqueues")
	crdClient := &fakeCRDClient{installed: map[string]*apiextensionsv1.CustomResourceDefinition{
		clusterQueues.Name: installedCRD(clusterQueues, "v1beta2"),
		localQueues.Name:   installedCRD(localQueues, "v1beta1", "v1beta2"),
	}}
	recorder := events.NewInMemoryRecorder("test", clock.RealClock{})
	c := &TargetConfigReconciler{operatorNamespace: "openshift-kueue-operator", crdClient: crdClient, eventRecorder: recorder}

	var applies []string
	apply := func(_ context.Context, crd *apiextensionsv1.CustomResourceDefinition) (*apiextensionsv1.CustomResourceDefinition, bool, error) {
		applies = append(applies, crd.Name)
		return crd, true, nil
	}
	wantStale := func(err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), localQueues.Name) || !strings.Contains(err.Error(), "remove [v1beta1] from status.storedVersions") {
			t.Fatalf("got error %v, want the stale stored version of %s", err, localQueues.Name)
		}
	}

	// sync checks every embedded CRD before changing any operand resource.
	wantStale(c.checkKueueCustomResourceDefinitions(t.Context()))
	if crdClient.gets != len(all) {
		t.Errorf("got %d CRD reads, want one per embedded CRD", crdClient.gets)
	}
	// The CRD step checks again before applying: only the failed CRD is read.
	wantStale(c.manageCustomResourcesWith(t.Context(), map[string]string{}, c.checkStoredVersions, apply))
	if crdClient.gets != len(all)+1 {
		t.Errorf("got %d CRD reads, want only the failed CRD to be read again", crdClient.gets)
	}
	if len(applies) != 0 {
		t.Errorf("applied %v despite a failed check", applies)
	}

	// Once the storage migration is completed, the next sync applies every CRD.
	crdClient.installed[localQueues.Name].Status.StoredVersions = []string{"v1beta2"}
	if err := c.checkKueueCustomResourceDefinitions(t.Context()); err != nil {
		t.Fatalf("unexpected error after the storage migration: %v", err)
	}
	if err := c.manageCustomResourcesWith(t.Context(), map[string]string{}, c.checkStoredVersions, apply); err != nil {
		t.Fatalf("unexpected error after the storage migration: %v", err)
	}
	if diff := cmp.Diff(all, applies); diff != "" {
		t.Errorf("unexpected applies after the storage migration (-want,+got):\n%s", diff)
	}
}
