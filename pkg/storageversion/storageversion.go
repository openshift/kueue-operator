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

// Package storageversion migrates custom resources that are still persisted in
// a version which a newer CustomResourceDefinition no longer lists.
//
// The Kubernetes API server rejects a CRD update that removes a version still
// listed in status.storedVersions, and objects persisted in a removed version
// can no longer be read. Before such a CRD is applied, every object therefore
// has to be rewritten in the current storage version, and the stale versions
// removed from status.storedVersions. This follows the algorithm of the
// Kubernetes storage version migrator: list every object, send it back
// unchanged so the API server re-encodes it in the storage version, then prune
// status.storedVersions.
package storageversion

import (
	"context"
	"fmt"
	"slices"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/pager"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
)

// FieldManager identifies the storage version rewrites in the API server.
const FieldManager = "kueue-operator-storage-version-migration"

// unchangedPatch is an empty JSON merge patch. The API server applies it to
// the current object and persists the result in the storage version.
var unchangedPatch = []byte("{}")

// progressInterval is the number of rewritten objects between progress logs.
const progressInterval = 1000

// Result describes a completed migration.
type Result struct {
	// StaleVersions are the versions removed from status.storedVersions.
	StaleVersions []string
	// StorageVersion is the version every object was rewritten in.
	StorageVersion string
	// Rewritten is the number of listed objects that were sent back to the API
	// server, including objects that were deleted in the meantime.
	Rewritten int
}

// StaleStoredVersions returns the versions in existing.Status.StoredVersions
// that required does not list, in status order.
func StaleStoredVersions(existing, required *apiextensionsv1.CustomResourceDefinition) []string {
	keep := versionNames(required)
	var stale []string
	for _, v := range existing.Status.StoredVersions {
		if !keep.Has(v) {
			stale = append(stale, v)
		}
	}
	return stale
}

// Migrate rewrites every object of the CRD named required.Name in the storage
// version of the CRD currently installed, then removes the versions that
// required does not list from status.storedVersions.
//
// It must run before required is applied, while the installed CRD and its
// conversion webhook, if any, can still read every persisted version. It
// fails without pruning status.storedVersions if any object cannot be read or
// rewritten, or if the installed CRD changes during the migration, and it
// refuses to run when the installed storage version is not listed by
// required, because objects could then not be moved to a version that
// survives the update. It does nothing when the CRD does not exist or no
// stored version is stale.
func Migrate(ctx context.Context, crdClient apiextv1.CustomResourceDefinitionsGetter, dynamicClient dynamic.Interface, required *apiextensionsv1.CustomResourceDefinition) (Result, error) {
	existing, err := crdClient.CustomResourceDefinitions().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("failed to get CustomResourceDefinition %s: %w", required.Name, err)
	}

	stale := StaleStoredVersions(existing, required)
	if len(stale) == 0 {
		return Result{}, nil
	}

	storageVersion, err := servedStorageVersion(existing)
	if err != nil {
		return Result{}, err
	}
	if !versionNames(required).Has(storageVersion) {
		return Result{}, fmt.Errorf("CustomResourceDefinition %s stores objects as %s, which the new definition does not list; objects persisted as %v cannot be migrated", required.Name, storageVersion, stale)
	}

	resource := dynamicClient.Resource(schema.GroupVersionResource{
		Group:    existing.Spec.Group,
		Version:  storageVersion,
		Resource: existing.Spec.Names.Plural,
	})

	// List the keys before rewriting, so that the paginated list completes
	// quickly and its continue token cannot expire while objects are being
	// rewritten at the client's rate limit.
	keys, err := listKeys(ctx, resource)
	if err != nil {
		return Result{}, fmt.Errorf("failed to list %s as %s: %w", required.Name, storageVersion, err)
	}
	klog.Infof("Migrating %d objects of CustomResourceDefinition %s from stored versions %v to %s", len(keys), required.Name, stale, storageVersion)

	namespaced := existing.Spec.Scope == apiextensionsv1.NamespaceScoped
	for i, key := range keys {
		var client dynamic.ResourceInterface = resource
		if namespaced {
			client = resource.Namespace(key.Namespace)
		}
		if err := rewrite(ctx, client, key.Name); err != nil {
			return Result{}, fmt.Errorf("failed to rewrite %s %s in %s: %w", required.Name, keyString(key), storageVersion, err)
		}
		if (i+1)%progressInterval == 0 {
			klog.Infof("Rewrote %d of %d objects of CustomResourceDefinition %s", i+1, len(keys), required.Name)
		}
	}

	if err := pruneStoredVersions(ctx, crdClient, required, existing.Generation, storageVersion); err != nil {
		return Result{}, fmt.Errorf("failed to remove %v from the stored versions of CustomResourceDefinition %s: %w", stale, required.Name, err)
	}

	return Result{StaleVersions: stale, StorageVersion: storageVersion, Rewritten: len(keys)}, nil
}

// listKeys returns the namespaced names of all objects of resource.
func listKeys(ctx context.Context, resource dynamic.NamespaceableResourceInterface) ([]types.NamespacedName, error) {
	var keys []types.NamespacedName
	listPager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		return resource.List(ctx, opts)
	})
	err := listPager.EachListItem(ctx, metav1.ListOptions{}, func(obj runtime.Object) error {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			return fmt.Errorf("unexpected list item type %T", obj)
		}
		keys = append(keys, types.NamespacedName{Namespace: u.GetNamespace(), Name: u.GetName()})
		return nil
	})
	return keys, err
}

// rewrite applies an empty patch to the named object, which makes the API
// server persist its current state in the storage version. A missing object
// was deleted after it was listed and needs no rewrite.
func rewrite(ctx context.Context, client dynamic.ResourceInterface, name string) error {
	_, err := client.Patch(ctx, name, types.MergePatchType, unchangedPatch, metav1.PatchOptions{FieldManager: FieldManager})
	if err == nil || apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// pruneStoredVersions sets status.storedVersions to the versions that
// required lists, provided the CRD did not change since its objects were
// listed. A changed CRD may have switched storage versions or stopped serving
// the version the objects were rewritten through.
func pruneStoredVersions(ctx context.Context, crdClient apiextv1.CustomResourceDefinitionsGetter, required *apiextensionsv1.CustomResourceDefinition, generation int64, storageVersion string) error {
	keep := versionNames(required)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		crd, err := crdClient.CustomResourceDefinitions().Get(ctx, required.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if crd.Generation != generation {
			return fmt.Errorf("CustomResourceDefinition changed during the migration (generation %d, now %d)", generation, crd.Generation)
		}
		current, err := servedStorageVersion(crd)
		if err != nil {
			return err
		}
		if current != storageVersion {
			return fmt.Errorf("storage version changed from %s to %s during the migration", storageVersion, current)
		}

		stored := make([]string, 0, len(crd.Status.StoredVersions))
		for _, v := range crd.Status.StoredVersions {
			if keep.Has(v) {
				stored = append(stored, v)
			}
		}
		if !slices.Contains(stored, storageVersion) {
			stored = append(stored, storageVersion)
		}
		if slices.Equal(stored, crd.Status.StoredVersions) {
			return nil
		}

		crd.Status.StoredVersions = stored
		_, err = crdClient.CustomResourceDefinitions().UpdateStatus(ctx, crd, metav1.UpdateOptions{FieldManager: FieldManager})
		return err
	})
}

// servedStorageVersion returns the storage version of crd, which must also be
// served so that objects can be listed and updated through it.
func servedStorageVersion(crd *apiextensionsv1.CustomResourceDefinition) (string, error) {
	for _, v := range crd.Spec.Versions {
		if !v.Storage {
			continue
		}
		if !v.Served {
			return "", fmt.Errorf("storage version %s of CustomResourceDefinition %s is not served", v.Name, crd.Name)
		}
		return v.Name, nil
	}
	return "", fmt.Errorf("CustomResourceDefinition %s has no storage version", crd.Name)
}

func versionNames(crd *apiextensionsv1.CustomResourceDefinition) sets.Set[string] {
	names := sets.New[string]()
	for _, v := range crd.Spec.Versions {
		names.Insert(v.Name)
	}
	return names
}

func keyString(key types.NamespacedName) string {
	if key.Namespace == "" {
		return key.Name
	}
	return key.String()
}
