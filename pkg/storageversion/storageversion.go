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

// Package storageversion checks, before a newer CustomResourceDefinition is
// applied, that the installed definition does not record a stored version
// which the newer one drops.
//
// The Kubernetes API server rejects a CRD update that removes a version still
// listed in status.storedVersions, and objects persisted in a removed version
// can no longer be read. Moving every object to a remaining version and then
// removing the old version from status.storedVersions is a storage migration
// that has to be completed before such an upgrade. This package only reads
// the installed CRD to check that this was done; it never writes.
package storageversion

import (
	"context"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

// IncompatibleError reports an installed CRD whose status.storedVersions lists
// versions that the new definition does not include.
type IncompatibleError struct {
	// Name is the name of the CRD.
	Name string
	// StaleVersions are the stored versions that the new definition drops.
	StaleVersions []string
	// Versions are the versions of the new definition.
	Versions []string
}

func (e *IncompatibleError) Error() string {
	return fmt.Sprintf("CustomResourceDefinition %s lists %v in status.storedVersions, but the new definition only has versions %v, "+
		"so objects may still be stored as %v; complete the storage migration before upgrading: "+
		"rewrite every object of %s in one of %v, then remove %v from status.storedVersions",
		e.Name, e.StaleVersions, e.Versions, e.StaleVersions, e.Name, e.Versions, e.StaleVersions)
}

// staleStoredVersions returns the versions in existing.Status.StoredVersions
// that required does not list, in status order.
func staleStoredVersions(existing, required *apiextensionsv1.CustomResourceDefinition) []string {
	keep := versionNames(required)
	var stale []string
	for _, v := range existing.Status.StoredVersions {
		if !keep.Has(v) {
			stale = append(stale, v)
		}
	}
	return stale
}

// Check reads the installed CRD named required.Name from the API server and
// returns an *IncompatibleError when its status.storedVersions lists a version
// that required does not list. A CRD that is not installed passes. Check does
// not write to the API server.
func Check(ctx context.Context, crdClient apiextv1.CustomResourceDefinitionsGetter, required *apiextensionsv1.CustomResourceDefinition) error {
	existing, err := crdClient.CustomResourceDefinitions().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get CustomResourceDefinition %s: %w", required.Name, err)
	}

	if stale := staleStoredVersions(existing, required); len(stale) > 0 {
		return &IncompatibleError{
			Name:          required.Name,
			StaleVersions: stale,
			Versions:      sets.List(versionNames(required)),
		}
	}
	return nil
}

func versionNames(crd *apiextensionsv1.CustomResourceDefinition) sets.Set[string] {
	names := sets.New[string]()
	for _, v := range crd.Spec.Versions {
		names.Insert(v.Name)
	}
	return names
}
