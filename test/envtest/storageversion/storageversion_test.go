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

// Package storageversion_test runs the stored-version check against a real API
// server: the current Kueue CRDs, which drop v1beta1, cannot replace an
// installed CRD that still records v1beta1 in status.storedVersions; the check
// reports such a CRD without writing to it, and passes once the storage
// migration has been completed.
package storageversion_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/openshift/kueue-operator/bindata"
	"github.com/openshift/kueue-operator/pkg/storageversion"
)

// requestRecorder records the HTTP method of every request sent through it.
type requestRecorder struct {
	next    http.RoundTripper
	mu      sync.Mutex
	methods []string
}

func (r *requestRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.methods = append(r.methods, req.Method)
	r.mu.Unlock()
	return r.next.RoundTrip(req)
}

// takeMethods returns the recorded methods and resets the record.
func (r *requestRecorder) takeMethods() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	methods := r.methods
	r.methods = nil
	return methods
}

// startEnv starts an API server and returns a CRD client for the test steps
// and a CRD client whose requests are recorded, for the check.
func startEnv(t *testing.T) (apiextv1.ApiextensionsV1Interface, apiextv1.ApiextensionsV1Interface, *requestRecorder) {
	t.Helper()
	env := &envtest.Environment{
		DownloadBinaryAssets:     true,
		ControlPlaneStartTimeout: 2 * time.Minute,
		ControlPlaneStopTimeout:  time.Minute,
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("failed to start envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Logf("failed to stop envtest: %v", err)
		}
	})
	crds, err := apiextv1.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("failed to create the CRD client: %v", err)
	}
	recorder := &requestRecorder{}
	checkCfg := rest.CopyConfig(cfg)
	checkCfg.Wrap(func(next http.RoundTripper) http.RoundTripper {
		recorder.next = next
		return recorder
	})
	checkCRDs, err := apiextv1.NewForConfig(checkCfg)
	if err != nil {
		t.Fatalf("failed to create the recorded CRD client: %v", err)
	}
	return crds, checkCRDs, recorder
}

// currentCRD returns the embedded Kueue CRD for plural, which only has v1beta2.
func currentCRD(plural string) *apiextensionsv1.CustomResourceDefinition {
	asset := fmt.Sprintf("assets/kueue-operator/crds/crd-%s.kueue.x-k8s.io-v1beta2.yaml", plural)
	return resourceread.ReadCustomResourceDefinitionV1OrDie(bindata.MustAsset(asset))
}

// legacyCRD returns the current CRD for plural with v1beta1 served next to
// v1beta2, as earlier releases installed it, storing objects as
// storageVersion.
func legacyCRD(plural, storageVersion string) *apiextensionsv1.CustomResourceDefinition {
	crd := currentCRD(plural)
	crd.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{Strategy: apiextensionsv1.NoneConverter}
	v1beta1 := *crd.Spec.Versions[0].DeepCopy()
	v1beta1.Name = "v1beta1"
	crd.Spec.Versions = append([]apiextensionsv1.CustomResourceDefinitionVersion{v1beta1}, crd.Spec.Versions...)
	for i := range crd.Spec.Versions {
		crd.Spec.Versions[i].Storage = crd.Spec.Versions[i].Name == storageVersion
	}
	return crd
}

// install creates crd, or updates the installed CRD of the same name to it.
func install(
	t *testing.T, crds apiextv1.ApiextensionsV1Interface, crd *apiextensionsv1.CustomResourceDefinition,
) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	ctx := t.Context()
	var installed *apiextensionsv1.CustomResourceDefinition
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := crds.CustomResourceDefinitions().Get(ctx, crd.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			installed, err = crds.CustomResourceDefinitions().Create(ctx, crd, metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		updated := existing.DeepCopy()
		updated.Spec = *crd.Spec.DeepCopy()
		installed, err = crds.CustomResourceDefinitions().Update(ctx, updated, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		t.Fatalf("failed to install CRD %s: %v", crd.Name, err)
	}
	return installed
}

func get(t *testing.T, crds apiextv1.ApiextensionsV1Interface, name string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	crd, err := crds.CustomResourceDefinitions().Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get CRD %s: %v", name, err)
	}
	return crd
}

// apply applies required the way the operator does. A conflict with a status
// update by the API server's CRD controllers is retried, as the operator's
// next sync would.
func apply(
	t *testing.T, crds apiextv1.ApiextensionsV1Interface, required *apiextensionsv1.CustomResourceDefinition,
) error {
	t.Helper()
	recorder := events.NewInMemoryRecorder("test", clock.RealClock{})
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		_, _, err := resourceapply.ApplyCustomResourceDefinitionV1(t.Context(), crds, recorder, required.DeepCopy())
		return err
	})
}

// check runs storageversion.Check through the recorded client and fails the
// test if it sent anything but reads.
func check(
	t *testing.T, crds apiextv1.ApiextensionsV1Interface, recorder *requestRecorder,
	required *apiextensionsv1.CustomResourceDefinition,
) error {
	t.Helper()
	recorder.takeMethods()
	err := storageversion.Check(t.Context(), crds, required)
	methods := recorder.takeMethods()
	if len(methods) == 0 {
		t.Errorf("the check sent no request")
	}
	for _, method := range methods {
		if method != http.MethodGet {
			t.Errorf("the check sent a %s request; it must only read", method)
		}
	}
	return err
}

func TestCheck(t *testing.T) {
	crds, checkCRDs, recorder := startEnv(t)

	t.Run("a CRD that is not installed passes and the current CRD can be created", func(t *testing.T) {
		required := currentCRD("workloads")
		if err := check(t, checkCRDs, recorder, required); err != nil {
			t.Fatalf("unexpected error for a missing CRD: %v", err)
		}
		if err := apply(t, crds, required); err != nil {
			t.Fatalf("failed to create the current CRD: %v", err)
		}
		if err := check(t, checkCRDs, recorder, required); err != nil {
			t.Fatalf("unexpected error for the current CRD: %v", err)
		}
	})

	t.Run("an installed CRD that only stored v1beta2 passes and can be updated", func(t *testing.T) {
		required := currentCRD("clusterqueues")
		installed := install(t, crds, legacyCRD("clusterqueues", "v1beta2"))
		if diff := cmp.Diff([]string{"v1beta2"}, installed.Status.StoredVersions); diff != "" {
			t.Fatalf("unexpected stored versions (-want,+got):\n%s", diff)
		}

		if err := check(t, checkCRDs, recorder, required); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := apply(t, crds, required); err != nil {
			t.Fatalf("failed to apply the current CRD: %v", err)
		}
	})

	t.Run("a CRD that still records v1beta1 is rejected without a write until it is migrated", func(t *testing.T) {
		required := currentCRD("localqueues")
		// Earlier releases stored objects as v1beta1, later ones as v1beta2:
		// the API server keeps both in status.storedVersions.
		install(t, crds, legacyCRD("localqueues", "v1beta1"))
		installed := install(t, crds, legacyCRD("localqueues", "v1beta2"))
		if diff := cmp.Diff([]string{"v1beta1", "v1beta2"}, installed.Status.StoredVersions); diff != "" {
			t.Fatalf("unexpected stored versions (-want,+got):\n%s", diff)
		}

		for range 2 {
			err := check(t, checkCRDs, recorder, required)
			var incompatible *storageversion.IncompatibleError
			if !errors.As(err, &incompatible) {
				t.Fatalf("got error %v, want an IncompatibleError", err)
			}
			want := &storageversion.IncompatibleError{
				Name: required.Name, StaleVersions: []string{"v1beta1"}, Versions: []string{"v1beta2"},
			}
			if diff := cmp.Diff(want, incompatible); diff != "" {
				t.Errorf("unexpected IncompatibleError (-want,+got):\n%s", diff)
			}
			if diff := cmp.Diff(installed.Status.StoredVersions, get(t, crds, required.Name).Status.StoredVersions); diff != "" {
				t.Errorf("the stored versions changed (-want,+got):\n%s", diff)
			}
		}

		// This is the failure the check prevents: the API server refuses to
		// drop a version that is still in status.storedVersions.
		if err := apply(t, crds, required); !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "storedVersions") {
			t.Fatalf("got error %v when applying the current CRD, want the stored versions to be rejected", err)
		}

		// The administrator completes the storage migration: once every
		// object is stored as v1beta2, v1beta1 is removed from
		// status.storedVersions.
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			crd := get(t, crds, required.Name)
			crd.Status.StoredVersions = []string{"v1beta2"}
			_, err := crds.CustomResourceDefinitions().UpdateStatus(t.Context(), crd, metav1.UpdateOptions{})
			return err
		})
		if err != nil {
			t.Fatalf("failed to complete the storage migration: %v", err)
		}

		if err := check(t, checkCRDs, recorder, required); err != nil {
			t.Fatalf("unexpected error after the storage migration: %v", err)
		}
		if err := apply(t, crds, required); err != nil {
			t.Fatalf("failed to apply the current CRD after the storage migration: %v", err)
		}
		if diff := cmp.Diff([]string{"v1beta2"}, get(t, crds, required.Name).Status.StoredVersions); diff != "" {
			t.Errorf("unexpected stored versions after the update (-want,+got):\n%s", diff)
		}
	})
}
