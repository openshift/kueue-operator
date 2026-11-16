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

// Package storageversion_test reproduces upgrades of the Kueue CRDs on a real
// API server and etcd: objects persisted as v1beta1 by operator 1.2 survive
// the switch to v1beta2 storage in operator 1.5, and the current CRDs, which
// drop v1beta1, can only be applied once those objects have been rewritten.
package storageversion_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	clientv3 "go.etcd.io/etcd/client/v3"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/webhook/conversion"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"

	"github.com/openshift/kueue-operator/bindata"
	"github.com/openshift/kueue-operator/pkg/storageversion"
)

const (
	kueueGroup    = "kueue.x-k8s.io"
	teamNamespace = "team-a"
	operatorUser  = "system:serviceaccount:openshift-kueue-operator:openshift-kueue-operator"
	pollTimeout   = 30 * time.Second
)

// plurals are the CRDs exercised: ClusterQueue and LocalQueue use webhook
// conversion, AdmissionCheck and ResourceFlavor use none.
var plurals = []string{"admissionchecks", "clusterqueues", "localqueues", "resourceflavors"}

type objectRef struct {
	plural string
	key    client.ObjectKey
}

func (r objectRef) String() string {
	return r.plural + " " + path.Join(r.key.Namespace, r.key.Name)
}

// conversionServer serves the v1beta1 <-> v1beta2 conversion of Kueue 0.19,
// the operand of operator 1.5, as the previous operand does during an upgrade.
type conversionServer struct {
	server   *httptest.Server
	caBundle []byte

	mu          sync.Mutex
	available   bool
	fromV1beta1 int
	refused     int
}

func newConversionServer(t *testing.T) *conversionServer {
	t.Helper()
	scheme := runtime.NewScheme()
	utilruntime.Must(kueuev1beta1.AddToScheme(scheme))
	utilruntime.Must(kueuev1beta2.AddToScheme(scheme))
	handler := conversion.NewWebhookHandler(scheme, conversion.NewRegistry())

	cs := &conversionServer{available: true}
	cs.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cs.mu.Lock()
		if !cs.available {
			cs.refused++
			cs.mu.Unlock()
			http.Error(w, "conversion webhook unavailable", http.StatusServiceUnavailable)
			return
		}
		cs.fromV1beta1 += countObjects(body, kueueGroup+"/v1beta1")
		cs.mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(body))
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(cs.server.Close)
	cs.caBundle = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cs.server.Certificate().Raw})
	return cs
}

func (cs *conversionServer) setAvailable(available bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.available = available
}

func (cs *conversionServer) counts() (fromV1beta1, refused int) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.fromV1beta1, cs.refused
}

func countObjects(body []byte, apiVersion string) int {
	var review apiextensionsv1.ConversionReview
	if err := json.Unmarshal(body, &review); err != nil || review.Request == nil {
		return 0
	}
	n := 0
	for _, obj := range review.Request.Objects {
		var typeMeta metav1.TypeMeta
		if json.Unmarshal(obj.Raw, &typeMeta) == nil && typeMeta.APIVersion == apiVersion {
			n++
		}
	}
	return n
}

type testEnv struct {
	env        *envtest.Environment
	crds       apiextv1.ApiextensionsV1Interface
	dynamic    dynamic.Interface
	kube       kubernetes.Interface
	client     client.Client
	etcd       *clientv3.Client
	conversion *conversionServer
	recorder   events.Recorder
}

func startEnv(t *testing.T) *testEnv {
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

	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(kueuev1beta1.AddToScheme(scheme))
	utilruntime.Must(kueuev1beta2.AddToScheme(scheme))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{env.ControlPlane.Etcd.URL.String()},
		DialTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create etcd client: %v", err)
	}
	t.Cleanup(func() { _ = etcdClient.Close() })

	return &testEnv{
		env:        env,
		crds:       apiextv1.NewForConfigOrDie(cfg),
		dynamic:    dynamic.NewForConfigOrDie(cfg),
		kube:       kubernetes.NewForConfigOrDie(cfg),
		client:     c,
		etcd:       etcdClient,
		conversion: newConversionServer(t),
		recorder:   events.NewInMemoryRecorder("storage-version-test", clock.RealClock{}),
	}
}

// poll retries condition until it succeeds or pollTimeout elapses.
func poll(ctx context.Context, condition wait.ConditionWithContextFunc) error {
	return wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, pollTimeout, true, condition)
}

func fixtureCRD(t *testing.T, release, plural string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", release, plural+".yaml"))
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	return resourceread.ReadCustomResourceDefinitionV1OrDie(data)
}

// currentCRD returns the CRD the operator applies.
func currentCRD(plural string) *apiextensionsv1.CustomResourceDefinition {
	asset := fmt.Sprintf("assets/kueue-operator/crds/crd-%s.%s-v1beta2.yaml", plural, kueueGroup)
	return resourceread.ReadCustomResourceDefinitionV1OrDie(bindata.MustAsset(asset))
}

// withTestConversion points webhook conversion at the test conversion server.
func (e *testEnv) withTestConversion(
	crd *apiextensionsv1.CustomResourceDefinition,
) *apiextensionsv1.CustomResourceDefinition {
	if crd.Spec.Conversion != nil && crd.Spec.Conversion.Strategy == apiextensionsv1.WebhookConverter {
		crd.Spec.Conversion.Webhook.ClientConfig = &apiextensionsv1.WebhookClientConfig{
			URL:      ptr.To(e.conversion.server.URL + "/convert"),
			CABundle: e.conversion.caBundle,
		}
	}
	return crd
}

// install creates or replaces a CRD the way an earlier release did.
func (e *testEnv) install(t *testing.T, crd *apiextensionsv1.CustomResourceDefinition) {
	t.Helper()
	ctx := t.Context()
	e.withTestConversion(crd)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := e.crds.CustomResourceDefinitions().Get(ctx, crd.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = e.crds.CustomResourceDefinitions().Create(ctx, crd, metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		crd.ResourceVersion = existing.ResourceVersion
		_, err = e.crds.CustomResourceDefinitions().Update(ctx, crd, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		t.Fatalf("failed to install CRD %s: %v", crd.Name, err)
	}
	e.waitServed(t, crd)
}

// applyCurrent applies the current CRD with the operator's apply function and
// waits until the API server serves it in place of the previous definition:
// discovery lists the resource as v1beta2 and v1beta1 is no longer served.
// It does not read any object.
func (e *testEnv) applyCurrent(t *testing.T, plural string) error {
	t.Helper()
	required := e.withTestConversion(currentCRD(plural))
	if _, _, err := resourceapply.ApplyCustomResourceDefinitionV1(t.Context(), e.crds, e.recorder, required); err != nil {
		return err
	}
	removed := schema.GroupVersionResource{Group: kueueGroup, Version: "v1beta1", Resource: plural}
	var lastErr error
	err := poll(t.Context(), func(ctx context.Context) (bool, error) {
		resources, err := e.kube.Discovery().ServerResourcesForGroupVersion(kueueGroup + "/v1beta2")
		discovered := err == nil && slices.ContainsFunc(resources.APIResources, func(r metav1.APIResource) bool {
			return r.Name == plural
		})
		if !discovered {
			lastErr = fmt.Errorf("%s is not discovered as v1beta2: %w", plural, err)
			return false, nil
		}
		_, lastErr = e.dynamic.Resource(removed).List(ctx, metav1.ListOptions{Limit: 1})
		return apierrors.IsNotFound(lastErr), nil
	})
	if err != nil {
		t.Fatalf("%s is still served as v1beta1 after applying the current CRD: %v (last error: %v)", plural, err, lastErr)
	}
	return nil
}

func (e *testEnv) expectStoredVersions(t *testing.T, plural string, want ...string) {
	t.Helper()
	if got := e.storedVersions(t, plural); !cmp.Equal(got, want) {
		t.Fatalf("%s stored versions = %v, want %v", plural, got, want)
	}
}

func (e *testEnv) expectPersisted(t *testing.T, version string, refs ...objectRef) {
	t.Helper()
	for _, ref := range refs {
		if got := e.persistedAPIVersion(t, ref); got != kueueGroup+"/"+version {
			t.Fatalf("%s is persisted as %s, want %s", ref, got, version)
		}
	}
}

// waitServed waits until every served version of crd can be listed.
func (e *testEnv) waitServed(t *testing.T, crd *apiextensionsv1.CustomResourceDefinition) {
	t.Helper()
	var lastErr error
	err := poll(t.Context(), func(ctx context.Context) (bool, error) {
		for _, v := range crd.Spec.Versions {
			if !v.Served {
				continue
			}
			gvr := schema.GroupVersionResource{Group: crd.Spec.Group, Version: v.Name, Resource: crd.Spec.Names.Plural}
			if _, lastErr = e.dynamic.Resource(gvr).List(ctx, metav1.ListOptions{Limit: 1}); lastErr != nil {
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("CRD %s is not served: %v (last error: %v)", crd.Name, err, lastErr)
	}
}

func (e *testEnv) storedVersions(t *testing.T, plural string) []string {
	t.Helper()
	crd, err := e.crds.CustomResourceDefinitions().Get(t.Context(), plural+"."+kueueGroup, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get CRD: %v", err)
	}
	return crd.Status.StoredVersions
}

// persistedAPIVersion returns the apiVersion of the object as stored in etcd.
func (e *testEnv) persistedAPIVersion(t *testing.T, ref objectRef) string {
	t.Helper()
	key := path.Join("/registry", kueueGroup, ref.plural, ref.key.Namespace, ref.key.Name)
	resp, err := e.etcd.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("failed to read %s from etcd: %v", key, err)
	}
	if len(resp.Kvs) != 1 {
		t.Fatalf("expected one etcd entry for %s, got %d", key, len(resp.Kvs))
	}
	var typeMeta metav1.TypeMeta
	if err := json.Unmarshal(resp.Kvs[0].Value, &typeMeta); err != nil {
		t.Fatalf("failed to decode %s: %v", key, err)
	}
	return typeMeta.APIVersion
}

func (e *testEnv) resource(ref objectRef, version string) dynamic.ResourceInterface {
	r := e.dynamic.Resource(schema.GroupVersionResource{Group: kueueGroup, Version: version, Resource: ref.plural})
	if ref.key.Namespace != "" {
		return r.Namespace(ref.key.Namespace)
	}
	return r
}

// get reads the object from etcd through the API server: a get without a
// resourceVersion bypasses the watch cache.
func (e *testEnv) get(t *testing.T, ref objectRef, version string) (*unstructured.Unstructured, error) {
	t.Helper()
	return e.resource(ref, version).Get(t.Context(), ref.key.Name, metav1.GetOptions{})
}

func (e *testEnv) mustGet(t *testing.T, ref objectRef, version string) *unstructured.Unstructured {
	t.Helper()
	u, err := e.get(t, ref, version)
	if err != nil {
		t.Fatalf("failed to get %s as %s: %v", ref, version, err)
	}
	return u
}

// snapshot reads every object as v1beta2 without bookkeeping fields that
// change on any write.
func (e *testEnv) snapshot(t *testing.T, refs []objectRef) map[string]map[string]any {
	t.Helper()
	snap := make(map[string]map[string]any, len(refs))
	for _, ref := range refs {
		u := e.mustGet(t, ref, "v1beta2")
		unstructured.RemoveNestedField(u.Object, "metadata", "resourceVersion")
		unstructured.RemoveNestedField(u.Object, "metadata", "managedFields")
		snap[ref.String()] = u.Object
	}
	return snap
}

// operatorClients returns clients authenticated as the operator's service
// account and authorized only by deploy/02_clusterrole.yaml.
func (e *testEnv) operatorClients(t *testing.T) (apiextv1.ApiextensionsV1Interface, dynamic.Interface) {
	t.Helper()
	ctx := t.Context()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "02_clusterrole.yaml"))
	if err != nil {
		t.Fatalf("failed to read operator ClusterRole: %v", err)
	}
	role := resourceread.ReadClusterRoleV1OrDie(data)
	if _, err := e.kube.RbacV1().ClusterRoles().Create(ctx, role, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create operator ClusterRole: %v", err)
	}
	binding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: role.Name},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: role.Name},
		Subjects:   []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: rbacv1.UserKind, Name: operatorUser}},
	}
	if _, err := e.kube.RbacV1().ClusterRoleBindings().Create(ctx, binding, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to bind operator ClusterRole: %v", err)
	}
	// Wait until the authorizer has observed the binding.
	err = poll(ctx, func(ctx context.Context) (bool, error) {
		review, err := e.kube.AuthorizationV1().SubjectAccessReviews().Create(ctx, &authorizationv1.SubjectAccessReview{
			Spec: authorizationv1.SubjectAccessReviewSpec{
				User: operatorUser,
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Group:       "apiextensions.k8s.io",
					Resource:    "customresourcedefinitions",
					Subresource: "status",
					Verb:        "update",
				},
			},
		}, metav1.CreateOptions{})
		return err == nil && review.Status.Allowed, nil
	})
	if err != nil {
		t.Fatalf("operator ClusterRole binding was not observed: %v", err)
	}
	user, err := e.env.AddUser(envtest.User{Name: operatorUser, Groups: []string{"system:authenticated"}}, nil)
	if err != nil {
		t.Fatalf("failed to add operator user: %v", err)
	}
	cfg := user.Config()
	return apiextv1.NewForConfigOrDie(cfg), dynamic.NewForConfigOrDie(cfg)
}

var (
	transitionTime = metav1.NewTime(time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC))
	cpuQuota       = resource.MustParse("9")
	cpuBorrowing   = resource.MustParse("3")
	memoryQuota    = resource.MustParse("36Gi")
	cpuUsage       = resource.MustParse("4")
	cpuQueueUsage  = resource.MustParse("2")
)

// createV1beta1Objects creates objects through the v1beta1 API of the
// operator 1.2 CRDs, so they are persisted as v1beta1.
func (e *testEnv) createV1beta1Objects(t *testing.T) []objectRef {
	t.Helper()
	ctx := t.Context()
	if err := e.client.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: teamNamespace}}); err != nil {
		t.Fatalf("failed to create namespace: %v", err)
	}

	rf := &kueuev1beta1.ResourceFlavor{
		ObjectMeta: metav1.ObjectMeta{Name: "on-demand", Labels: map[string]string{"example.com/tier": "gold"}},
		Spec: kueuev1beta1.ResourceFlavorSpec{
			NodeLabels: map[string]string{"instance-type": "on-demand"},
			NodeTaints: []corev1.Taint{{Key: "example.com/spot", Value: "false", Effect: corev1.TaintEffectNoSchedule}},
			Tolerations: []corev1.Toleration{{
				Key: "example.com/dedicated", Operator: corev1.TolerationOpEqual, Value: "ml", Effect: corev1.TaintEffectNoSchedule,
			}},
		},
	}
	ac := &kueuev1beta1.AdmissionCheck{
		ObjectMeta: metav1.ObjectMeta{Name: "provisioning"},
		Spec: kueuev1beta1.AdmissionCheckSpec{
			ControllerName: "kueue.x-k8s.io/provisioning-request",
			// The deprecated field is set to reproduce data that only v1beta1 can hold.
			RetryDelayMinutes: ptr.To[int64](15), //nolint:staticcheck // see above
			Parameters: &kueuev1beta1.AdmissionCheckParametersReference{
				APIGroup: kueueGroup, Kind: "ProvisioningRequestConfig", Name: "provisioning-config",
			},
		},
	}
	cq := &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "team-a-cq", Annotations: map[string]string{"example.com/owner": "team-a"}},
		Spec: kueuev1beta1.ClusterQueueSpec{
			Cohort:            "research",
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "a"}},
			QueueingStrategy:  kueuev1beta1.StrictFIFO,
			ResourceGroups: []kueuev1beta1.ResourceGroup{{
				CoveredResources: []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				Flavors: []kueuev1beta1.FlavorQuotas{{
					Name: "on-demand",
					Resources: []kueuev1beta1.ResourceQuota{
						{Name: corev1.ResourceCPU, NominalQuota: cpuQuota, BorrowingLimit: &cpuBorrowing},
						{Name: corev1.ResourceMemory, NominalQuota: memoryQuota},
					},
				}},
			}},
			AdmissionChecks: []kueuev1beta1.AdmissionCheckReference{"provisioning"},
			FlavorFungibility: &kueuev1beta1.FlavorFungibility{
				WhenCanBorrow:  kueuev1beta1.Borrow,
				WhenCanPreempt: kueuev1beta1.Preempt,
			},
			Preemption: &kueuev1beta1.ClusterQueuePreemption{
				ReclaimWithinCohort: kueuev1beta1.PreemptionPolicyAny,
				WithinClusterQueue:  kueuev1beta1.PreemptionPolicyLowerPriority,
			},
			StopPolicy: ptr.To(kueuev1beta1.HoldAndDrain),
		},
	}
	lq := &kueuev1beta1.LocalQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "user-queue", Namespace: teamNamespace},
		Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "team-a-cq"},
	}
	for _, obj := range []client.Object{rf, ac, cq, lq} {
		if err := e.client.Create(ctx, obj); err != nil {
			t.Fatalf("failed to create %T %s: %v", obj, obj.GetName(), err)
		}
	}

	activeCondition := metav1.Condition{
		Type: "Active", Status: metav1.ConditionTrue, Reason: "Ready", Message: "Can admit new workloads",
		LastTransitionTime: transitionTime,
	}
	cqUsage := []kueuev1beta1.FlavorUsage{{
		Name:      "on-demand",
		Resources: []kueuev1beta1.ResourceUsage{{Name: corev1.ResourceCPU, Total: cpuUsage}},
	}}
	lqUsage := []kueuev1beta1.LocalQueueFlavorUsage{{
		Name:      "on-demand",
		Resources: []kueuev1beta1.LocalQueueResourceUsage{{Name: corev1.ResourceCPU, Total: cpuQueueUsage}},
	}}
	cq.Status = kueuev1beta1.ClusterQueueStatus{
		Conditions:         []metav1.Condition{activeCondition},
		FlavorsReservation: cqUsage,
		FlavorsUsage:       cqUsage,
		PendingWorkloads:   3,
		ReservingWorkloads: 2,
		AdmittedWorkloads:  2,
	}
	lq.Status = kueuev1beta1.LocalQueueStatus{
		Conditions:         []metav1.Condition{activeCondition},
		PendingWorkloads:   1,
		ReservingWorkloads: 1,
		AdmittedWorkloads:  1,
		FlavorsReservation: lqUsage,
		FlavorUsage:        lqUsage,
	}
	for _, obj := range []client.Object{cq, lq} {
		if err := e.client.Status().Update(ctx, obj); err != nil {
			t.Fatalf("failed to update status of %T %s: %v", obj, obj.GetName(), err)
		}
	}

	return []objectRef{
		{plural: "resourceflavors", key: client.ObjectKeyFromObject(rf)},
		{plural: "admissionchecks", key: client.ObjectKeyFromObject(ac)},
		{plural: "clusterqueues", key: client.ObjectKeyFromObject(cq)},
		{plural: "localqueues", key: client.ObjectKeyFromObject(lq)},
	}
}

// createV1beta2Objects creates objects through the v1beta2 API, so they are
// persisted as v1beta2.
func (e *testEnv) createV1beta2Objects(t *testing.T) []objectRef {
	t.Helper()
	ctx := t.Context()
	if err := e.client.Get(ctx, client.ObjectKey{Name: teamNamespace}, &corev1.Namespace{}); apierrors.IsNotFound(err) {
		if err := e.client.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: teamNamespace}}); err != nil {
			t.Fatalf("failed to create namespace: %v", err)
		}
	}
	rf := &kueuev1beta2.ResourceFlavor{
		ObjectMeta: metav1.ObjectMeta{Name: "spot"},
		Spec:       kueuev1beta2.ResourceFlavorSpec{NodeLabels: map[string]string{"instance-type": "spot"}},
	}
	cq := &kueuev1beta2.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "team-b-cq"},
		Spec: kueuev1beta2.ClusterQueueSpec{
			CohortName:        "research",
			NamespaceSelector: &metav1.LabelSelector{},
			ResourceGroups: []kueuev1beta2.ResourceGroup{{
				CoveredResources: []corev1.ResourceName{corev1.ResourceCPU},
				Flavors: []kueuev1beta2.FlavorQuotas{{
					Name:      "spot",
					Resources: []kueuev1beta2.ResourceQuota{{Name: corev1.ResourceCPU, NominalQuota: cpuQuota}},
				}},
			}},
		},
	}
	lq := &kueuev1beta2.LocalQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "late-queue", Namespace: teamNamespace},
		Spec:       kueuev1beta2.LocalQueueSpec{ClusterQueue: "team-b-cq"},
	}
	for _, obj := range []client.Object{rf, cq, lq} {
		if err := e.client.Create(ctx, obj); err != nil {
			t.Fatalf("failed to create %T %s: %v", obj, obj.GetName(), err)
		}
	}
	return []objectRef{
		{plural: "resourceflavors", key: client.ObjectKeyFromObject(rf)},
		{plural: "clusterqueues", key: client.ObjectKeyFromObject(cq)},
		{plural: "localqueues", key: client.ObjectKeyFromObject(lq)},
	}
}

// installUpgradedCluster builds the state of a cluster installed with
// operator 1.2 and upgraded to operator 1.5: objects created by 1.2 are still
// persisted as v1beta1, objects created by 1.5 as v1beta2.
func (e *testEnv) installUpgradedCluster(t *testing.T) (v1beta1Refs, v1beta2Refs []objectRef) {
	t.Helper()
	for _, plural := range plurals {
		e.install(t, fixtureCRD(t, "release-1.2", plural))
	}
	v1beta1Refs = e.createV1beta1Objects(t)
	for _, plural := range plurals {
		e.install(t, fixtureCRD(t, "release-1.5", plural))
	}
	v1beta2Refs = e.createV1beta2Objects(t)

	for _, plural := range plurals {
		e.expectStoredVersions(t, plural, "v1beta1", "v1beta2")
	}
	e.expectPersisted(t, "v1beta1", v1beta1Refs...)
	e.expectPersisted(t, "v1beta2", v1beta2Refs...)
	return v1beta1Refs, v1beta2Refs
}

func countByPlural(refs []objectRef) map[string]int {
	counts := map[string]int{}
	for _, ref := range refs {
		counts[ref.plural]++
	}
	return counts
}

// TestMigrateUpgradeFromV1beta1Storage reproduces the upgrade from operator
// 1.5 on a cluster first installed with operator 1.2.
func TestMigrateUpgradeFromV1beta1Storage(t *testing.T) {
	e := startEnv(t)
	ctx := t.Context()
	v1beta1Refs, v1beta2Refs := e.installUpgradedCluster(t)
	allRefs := append(append([]objectRef{}, v1beta1Refs...), v1beta2Refs...)

	// The deprecated v1beta1-only field is still persisted.
	ac := e.mustGet(t, objectRef{plural: "admissionchecks", key: client.ObjectKey{Name: "provisioning"}}, "v1beta1")
	if v, _, _ := unstructured.NestedInt64(ac.Object, "spec", "retryDelayMinutes"); v != 15 {
		t.Fatalf("AdmissionCheck retryDelayMinutes as v1beta1 = %d, want 15", v)
	}

	// Without the migration, the current CRDs cannot be applied.
	for _, plural := range plurals {
		err := e.applyCurrent(t, plural)
		if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "status.storedVersions") {
			t.Fatalf("applying the current %s CRD before migrating: got %v, want an invalid storedVersions error", plural, err)
		}
		t.Logf("before migration, applying the current %s CRD fails: %v", plural, err)
	}

	convertedBefore, _ := e.conversion.counts()
	before := e.snapshot(t, allRefs)
	convertedAfter, _ := e.conversion.counts()
	// Only ClusterQueues and LocalQueues use webhook conversion.
	if converted := convertedAfter - convertedBefore; converted != 2 {
		t.Fatalf("reading the objects as v1beta2 converted %d v1beta1 objects through the webhook, want 2", converted)
	}
	t.Logf("reading the objects as v1beta2 converted 2 v1beta1 objects through the conversion webhook")

	// An identity without the operator ClusterRole is not allowed to migrate.
	unprivileged, err := e.env.AddUser(envtest.User{Name: "unprivileged", Groups: []string{"system:authenticated"}}, nil)
	if err != nil {
		t.Fatalf("failed to add user: %v", err)
	}
	unprivilegedCfg := unprivileged.Config()
	_, err = storageversion.Migrate(ctx,
		apiextv1.NewForConfigOrDie(unprivilegedCfg), dynamic.NewForConfigOrDie(unprivilegedCfg), currentCRD("clusterqueues"))
	if !apierrors.IsForbidden(err) {
		t.Fatalf("migrating without the operator ClusterRole: got %v, want forbidden", err)
	}
	crdClient, dynamicClient := e.operatorClients(t)

	// While the previous operand cannot convert, the migration fails and
	// leaves the stored versions in place.
	e.conversion.setAvailable(false)
	_, err = storageversion.Migrate(ctx, crdClient, dynamicClient, currentCRD("clusterqueues"))
	if err == nil {
		t.Fatalf("migrating ClusterQueues without the conversion webhook succeeded")
	}
	t.Logf("without the conversion webhook, the migration fails: %v", err)
	if got := e.storedVersions(t, "clusterqueues"); !cmp.Equal(got, []string{"v1beta1", "v1beta2"}) {
		t.Fatalf("clusterqueues stored versions after a failed migration = %v, want [v1beta1 v1beta2]", got)
	}
	failedRef := objectRef{plural: "clusterqueues", key: client.ObjectKey{Name: "team-a-cq"}}
	if got := e.persistedAPIVersion(t, failedRef); got != kueueGroup+"/v1beta1" {
		t.Fatalf("ClusterQueue is persisted as %s after a failed migration, want v1beta1", got)
	}
	_, refusedDuringFailure := e.conversion.counts()
	if refusedDuringFailure == 0 {
		t.Fatalf("the failed migration did not reach the conversion webhook")
	}
	e.conversion.setAvailable(true)

	// With the operator's permissions and the previous operand's conversion,
	// the migration rewrites every object and prunes the stored versions.
	want := countByPlural(allRefs)
	for _, plural := range plurals {
		result, err := storageversion.Migrate(ctx, crdClient, dynamicClient, currentCRD(plural))
		if err != nil {
			t.Fatalf("failed to migrate %s: %v", plural, err)
		}
		wantResult := storageversion.Result{
			StaleVersions: []string{"v1beta1"}, StorageVersion: "v1beta2", Rewritten: want[plural],
		}
		if diff := cmp.Diff(wantResult, result); diff != "" {
			t.Fatalf("unexpected result of migrating %s (-want,+got):\n%s", plural, diff)
		}
		e.expectStoredVersions(t, plural, "v1beta2")
		t.Logf("migrated %s: %+v", plural, result)
	}
	e.expectPersisted(t, "v1beta2", allRefs...)

	// The deprecated v1beta1-only AdmissionCheck field has no v1beta2
	// counterpart: rewriting the object in v1beta2 does not preserve it.
	ac = e.mustGet(t, objectRef{plural: "admissionchecks", key: client.ObjectKey{Name: "provisioning"}}, "v1beta1")
	if _, found, _ := unstructured.NestedFieldNoCopy(ac.Object, "spec", "retryDelayMinutes"); found {
		t.Fatalf("AdmissionCheck retryDelayMinutes survived the migration to v1beta2")
	}

	// A second migration finds nothing to do.
	for _, plural := range plurals {
		result, err := storageversion.Migrate(ctx, crdClient, dynamicClient, currentCRD(plural))
		if err != nil || len(result.StaleVersions) != 0 || result.Rewritten != 0 {
			t.Fatalf("migrating %s again returned %+v, %v; want no work", plural, result, err)
		}
	}

	// Without any conversion, every object reads back unchanged as v1beta2.
	e.conversion.setAvailable(false)
	if diff := cmp.Diff(before, e.snapshot(t, allRefs)); diff != "" {
		t.Fatalf("objects changed during the migration (-before +after):\n%s", diff)
	}

	// The current CRDs now apply, and the objects stay readable and keep
	// their data, still without any conversion.
	for _, plural := range plurals {
		if err := e.applyCurrent(t, plural); err != nil {
			t.Fatalf("failed to apply the current %s CRD after migrating: %v", plural, err)
		}
		e.expectStoredVersions(t, plural, "v1beta2")
	}
	for _, plural := range plurals {
		gvr := schema.GroupVersionResource{Group: kueueGroup, Version: "v1beta2", Resource: plural}
		list, err := e.dynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
		if err != nil {
			t.Fatalf("failed to list %s with the current CRD: %v", plural, err)
		}
		if len(list.Items) != want[plural] {
			t.Fatalf("listed %d %s with the current CRD, want %d", len(list.Items), plural, want[plural])
		}
	}
	verifyV1beta2Semantics(t, e)
	for _, ref := range allRefs {
		wantGeneration, _, _ := unstructured.NestedInt64(before[ref.String()], "metadata", "generation")
		if got := e.mustGet(t, ref, "v1beta2").GetGeneration(); got != wantGeneration {
			t.Fatalf("%s generation changed from %d to %d", ref, wantGeneration, got)
		}
	}
	// No read after the migration needed the conversion webhook.
	if _, refused := e.conversion.counts(); refused != refusedDuringFailure {
		t.Fatalf("%d conversion requests were made after the migration", refused-refusedDuringFailure)
	}
}

// verifyV1beta2Semantics checks the objects created as v1beta1 against their
// expected v1beta2 form, read through the current CRDs.
func verifyV1beta2Semantics(t *testing.T, e *testEnv) {
	t.Helper()
	ctx := t.Context()
	check := func(object, field string, got, want any) {
		t.Helper()
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("%s %s changed (-want,+got):\n%s", object, field, diff)
		}
	}
	quantity := func(q resource.Quantity) string { return q.String() }

	var rf kueuev1beta2.ResourceFlavor
	if err := e.client.Get(ctx, client.ObjectKey{Name: "on-demand"}, &rf); err != nil {
		t.Fatalf("failed to get ResourceFlavor: %v", err)
	}
	check("ResourceFlavor", "labels", rf.Labels, map[string]string{"example.com/tier": "gold"})
	check("ResourceFlavor", "nodeLabels", rf.Spec.NodeLabels, map[string]string{"instance-type": "on-demand"})
	check("ResourceFlavor", "nodeTaints", rf.Spec.NodeTaints,
		[]corev1.Taint{{Key: "example.com/spot", Value: "false", Effect: corev1.TaintEffectNoSchedule}})
	check("ResourceFlavor", "tolerations", rf.Spec.Tolerations, []corev1.Toleration{{
		Key: "example.com/dedicated", Operator: corev1.TolerationOpEqual, Value: "ml", Effect: corev1.TaintEffectNoSchedule,
	}})

	var ac kueuev1beta2.AdmissionCheck
	if err := e.client.Get(ctx, client.ObjectKey{Name: "provisioning"}, &ac); err != nil {
		t.Fatalf("failed to get AdmissionCheck: %v", err)
	}
	check("AdmissionCheck", "controllerName", ac.Spec.ControllerName, "kueue.x-k8s.io/provisioning-request")
	check("AdmissionCheck", "parameters", ac.Spec.Parameters, &kueuev1beta2.AdmissionCheckParametersReference{
		APIGroup: kueueGroup, Kind: "ProvisioningRequestConfig", Name: "provisioning-config",
	})

	var cq kueuev1beta2.ClusterQueue
	if err := e.client.Get(ctx, client.ObjectKey{Name: "team-a-cq"}, &cq); err != nil {
		t.Fatalf("failed to get ClusterQueue: %v", err)
	}
	check("ClusterQueue", "annotations", cq.Annotations, map[string]string{"example.com/owner": "team-a"})
	check("ClusterQueue", "cohortName (v1beta1 cohort)", cq.Spec.CohortName, kueuev1beta2.CohortReference("research"))
	check("ClusterQueue", "namespaceSelector", cq.Spec.NamespaceSelector,
		&metav1.LabelSelector{MatchLabels: map[string]string{"team": "a"}})
	check("ClusterQueue", "queueingStrategy", cq.Spec.QueueingStrategy, kueuev1beta2.StrictFIFO)
	check("ClusterQueue", "admissionChecksStrategy (v1beta1 admissionChecks)", cq.Spec.AdmissionChecksStrategy,
		&kueuev1beta2.AdmissionChecksStrategy{
			AdmissionChecks: []kueuev1beta2.AdmissionCheckStrategyRule{{Name: "provisioning"}},
		})
	if cq.Spec.FlavorFungibility == nil {
		t.Errorf("ClusterQueue flavorFungibility is missing")
	} else {
		ff := cq.Spec.FlavorFungibility
		check("ClusterQueue", "whenCanBorrow (v1beta1 Borrow)", ff.WhenCanBorrow, kueuev1beta2.MayStopSearch)
		check("ClusterQueue", "whenCanPreempt (v1beta1 Preempt)", ff.WhenCanPreempt, kueuev1beta2.MayStopSearch)
	}
	if cq.Spec.Preemption == nil {
		t.Errorf("ClusterQueue preemption is missing")
	} else {
		preemption := cq.Spec.Preemption
		check("ClusterQueue", "reclaimWithinCohort", preemption.ReclaimWithinCohort, kueuev1beta2.PreemptionPolicyAny)
		check("ClusterQueue", "withinClusterQueue", preemption.WithinClusterQueue, kueuev1beta2.PreemptionPolicyLowerPriority)
	}
	check("ClusterQueue", "stopPolicy", cq.Spec.StopPolicy, ptr.To(kueuev1beta2.HoldAndDrain))
	var quotas []string
	for _, group := range cq.Spec.ResourceGroups {
		for _, flavor := range group.Flavors {
			for _, r := range flavor.Resources {
				borrowing := "-"
				if r.BorrowingLimit != nil {
					borrowing = quantity(*r.BorrowingLimit)
				}
				quotas = append(quotas, fmt.Sprintf("%s/%s nominal=%s borrowing=%s",
					flavor.Name, r.Name, quantity(r.NominalQuota), borrowing))
			}
		}
	}
	check("ClusterQueue", "resourceGroups", quotas,
		[]string{"on-demand/cpu nominal=9 borrowing=3", "on-demand/memory nominal=36Gi borrowing=-"})
	check("ClusterQueue", "status workload counts",
		[]int32{cq.Status.PendingWorkloads, cq.Status.ReservingWorkloads, cq.Status.AdmittedWorkloads}, []int32{3, 2, 2})
	var cqUsage []string
	for _, usage := range cq.Status.FlavorsUsage {
		for _, r := range usage.Resources {
			cqUsage = append(cqUsage, fmt.Sprintf("%s/%s=%s", usage.Name, r.Name, quantity(r.Total)))
		}
	}
	check("ClusterQueue", "status flavorsUsage", cqUsage, []string{"on-demand/cpu=4"})
	check("ClusterQueue", "status conditions", conditionSummaries(cq.Status.Conditions), []string{activeConditionSummary})

	var lq kueuev1beta2.LocalQueue
	if err := e.client.Get(ctx, client.ObjectKey{Namespace: teamNamespace, Name: "user-queue"}, &lq); err != nil {
		t.Fatalf("failed to get LocalQueue: %v", err)
	}
	check("LocalQueue", "clusterQueue", lq.Spec.ClusterQueue, kueuev1beta2.ClusterQueueReference("team-a-cq"))
	check("LocalQueue", "status workload counts",
		[]int32{lq.Status.PendingWorkloads, lq.Status.ReservingWorkloads, lq.Status.AdmittedWorkloads}, []int32{1, 1, 1})
	var lqUsage []string
	for _, usage := range lq.Status.FlavorsUsage {
		for _, r := range usage.Resources {
			lqUsage = append(lqUsage, fmt.Sprintf("%s/%s=%s", usage.Name, r.Name, quantity(r.Total)))
		}
	}
	check("LocalQueue", "status flavorsUsage (v1beta1 flavorUsage)", lqUsage, []string{"on-demand/cpu=2"})
	check("LocalQueue", "status conditions", conditionSummaries(lq.Status.Conditions), []string{activeConditionSummary})
}

// activeConditionSummary summarizes the condition set on the queues created as v1beta1.
const activeConditionSummary = "Active=True Ready 2026-01-02T03:04:05Z"

func conditionSummaries(conditions []metav1.Condition) []string {
	summaries := make([]string, 0, len(conditions))
	for _, c := range conditions {
		transition := c.LastTransitionTime.UTC().Format(time.RFC3339)
		summaries = append(summaries, fmt.Sprintf("%s=%s %s %s", c.Type, c.Status, c.Reason, transition))
	}
	return summaries
}

// TestMigrateFreshInstall covers an installation without earlier CRDs.
func TestMigrateFreshInstall(t *testing.T) {
	e := startEnv(t)
	ctx := t.Context()
	for _, plural := range plurals {
		result, err := storageversion.Migrate(ctx, e.crds, e.dynamic, currentCRD(plural))
		if err != nil || len(result.StaleVersions) != 0 {
			t.Fatalf("migrating a missing %s CRD returned %+v, %v; want no work", plural, result, err)
		}
		if err := e.applyCurrent(t, plural); err != nil {
			t.Fatalf("failed to apply the current %s CRD: %v", plural, err)
		}
	}
	refs := e.createV1beta2Objects(t)
	before := e.snapshot(t, refs)
	for _, plural := range plurals {
		e.expectStoredVersions(t, plural, "v1beta2")
		result, err := storageversion.Migrate(ctx, e.crds, e.dynamic, currentCRD(plural))
		if err != nil || len(result.StaleVersions) != 0 || result.Rewritten != 0 {
			t.Fatalf("migrating %s returned %+v, %v; want no work", plural, result, err)
		}
	}
	if diff := cmp.Diff(before, e.snapshot(t, refs)); diff != "" {
		t.Fatalf("objects changed (-before +after):\n%s", diff)
	}
}

// TestMigrateAlreadyV1beta2 covers a cluster first installed with operator
// 1.5, whose objects were never persisted as v1beta1.
func TestMigrateAlreadyV1beta2(t *testing.T) {
	e := startEnv(t)
	ctx := t.Context()
	for _, plural := range plurals {
		e.install(t, fixtureCRD(t, "release-1.5", plural))
	}
	refs := e.createV1beta2Objects(t)
	resourceVersions := map[string]string{}
	for _, ref := range refs {
		resourceVersions[ref.String()] = e.mustGet(t, ref, "v1beta2").GetResourceVersion()
	}
	before := e.snapshot(t, refs)

	e.conversion.setAvailable(false)
	for _, plural := range plurals {
		e.expectStoredVersions(t, plural, "v1beta2")
		result, err := storageversion.Migrate(ctx, e.crds, e.dynamic, currentCRD(plural))
		if err != nil || len(result.StaleVersions) != 0 || result.Rewritten != 0 {
			t.Fatalf("migrating %s returned %+v, %v; want no work", plural, result, err)
		}
	}
	if diff := cmp.Diff(before, e.snapshot(t, refs)); diff != "" {
		t.Fatalf("objects changed (-before +after):\n%s", diff)
	}
	for _, plural := range plurals {
		if err := e.applyCurrent(t, plural); err != nil {
			t.Fatalf("failed to apply the current %s CRD: %v", plural, err)
		}
	}
	for _, ref := range refs {
		if got := e.mustGet(t, ref, "v1beta2").GetResourceVersion(); got != resourceVersions[ref.String()] {
			t.Fatalf("%s was rewritten: resourceVersion %s -> %s", ref, resourceVersions[ref.String()], got)
		}
	}
}

// TestMigrateRefusesUnretainedStorageVersion covers CRDs that still store
// objects in a version the current CRDs drop, as operator 1.2 left them.
func TestMigrateRefusesUnretainedStorageVersion(t *testing.T) {
	e := startEnv(t)
	ctx := t.Context()
	for _, plural := range plurals {
		e.install(t, fixtureCRD(t, "release-1.2", plural))
	}
	refs := e.createV1beta1Objects(t)
	for _, plural := range plurals {
		_, err := storageversion.Migrate(ctx, e.crds, e.dynamic, currentCRD(plural))
		if err == nil || !strings.Contains(err.Error(), "stores objects as v1beta1") {
			t.Fatalf("migrating %s from v1beta1 storage: got %v, want a refusal", plural, err)
		}
		e.expectStoredVersions(t, plural, "v1beta1")
	}
	for _, ref := range refs {
		if got := e.persistedAPIVersion(t, ref); got != kueueGroup+"/v1beta1" {
			t.Fatalf("%s is persisted as %s, want v1beta1", ref, got)
		}
		e.mustGet(t, ref, "v1beta1")
	}
}

// TestPruningStoredVersionsAloneLosesObjects shows why removing v1beta1 from
// status.storedVersions without rewriting the objects is not enough: the
// current CRDs then apply, but objects persisted as v1beta1 become unreadable.
func TestPruningStoredVersionsAloneLosesObjects(t *testing.T) {
	e := startEnv(t)
	ctx := t.Context()
	v1beta1Refs, v1beta2Refs := e.installUpgradedCluster(t)

	for _, plural := range plurals {
		crd, err := e.crds.CustomResourceDefinitions().Get(ctx, plural+"."+kueueGroup, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("failed to get CRD: %v", err)
		}
		crd.Status.StoredVersions = []string{"v1beta2"}
		if _, err := e.crds.CustomResourceDefinitions().UpdateStatus(ctx, crd, metav1.UpdateOptions{}); err != nil {
			t.Fatalf("failed to prune stored versions of %s: %v", plural, err)
		}
		if err := e.applyCurrent(t, plural); err != nil {
			t.Fatalf("failed to apply the current %s CRD after pruning: %v", plural, err)
		}
	}
	// The conversion webhook stays available: the objects are lost only
	// because the CRD no longer lists the version they are persisted in.
	for _, ref := range v1beta2Refs {
		e.mustGet(t, ref, "v1beta2")
	}
	for _, ref := range v1beta1Refs {
		var lastErr error
		err := poll(ctx, func(context.Context) (bool, error) {
			_, lastErr = e.get(t, ref, "v1beta2")
			return lastErr != nil && strings.Contains(lastErr.Error(), "invalid group/version"), nil
		})
		if err != nil {
			t.Fatalf("%s persisted as v1beta1 did not become unreadable after pruning without migrating (last error: %v)",
				ref, lastErr)
		}
		t.Logf("after pruning without migrating, reading %s fails: %v", ref, lastErr)
		if got := e.persistedAPIVersion(t, ref); got != kueueGroup+"/v1beta1" {
			t.Fatalf("%s is persisted as %s, want v1beta1", ref, got)
		}
	}
}
