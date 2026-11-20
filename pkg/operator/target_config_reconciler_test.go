package operator

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/kueue-operator/bindata"
	kueuev1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	"github.com/openshift/kueue-operator/pkg/webhook"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	clientgotesting "k8s.io/client-go/testing"
)

func TestMutatingWebhookReinvocationPolicyAsset(t *testing.T) {
	configuration := resourceread.ReadMutatingWebhookConfigurationV1OrDie(
		bindata.MustAsset("assets/kueue-operator/mutatingwebhook.yaml"),
	)
	if len(configuration.Webhooks) == 0 {
		t.Fatal("expected at least one mutating webhook")
	}

	for _, webhook := range configuration.Webhooks {
		if webhook.ReinvocationPolicy == nil {
			t.Errorf("mutating webhook %q has no reinvocation policy", webhook.Name)
			continue
		}
		if *webhook.ReinvocationPolicy != admissionregistrationv1.IfNeededReinvocationPolicy {
			t.Errorf("mutating webhook %q has reinvocation policy %q, want %q", webhook.Name, *webhook.ReinvocationPolicy, admissionregistrationv1.IfNeededReinvocationPolicy)
		}
	}
}

func TestKueueCRDNames(t *testing.T) {
	names, err := kueueCRDNames()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("expected at least one CRD name, got none")
	}
	// Verify sorted order.
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf("CRD names not sorted: %q >= %q", names[i-1], names[i])
		}
	}
	// Verify all names belong to the kueue.x-k8s.io group.
	for _, name := range names {
		if len(name) < len(".kueue.x-k8s.io") {
			t.Errorf("unexpected CRD name %q", name)
			continue
		}
		suffix := name[len(name)-len(".kueue.x-k8s.io"):]
		if suffix != ".kueue.x-k8s.io" {
			t.Errorf("CRD name %q does not end with .kueue.x-k8s.io", name)
		}
	}
}

func TestMissingConsumableCapacityDependencies(t *testing.T) {
	capacityResources := kueuev1.Resources{DeviceClassMappings: []kueuev1.DeviceClassMapping{{Sources: []kueuev1.DeviceClassSourceConfig{{Type: kueuev1.DeviceClassSourceTypeCapacity}}}}}
	counterResources := kueuev1.Resources{DeviceClassMappings: []kueuev1.DeviceClassMapping{{Sources: []kueuev1.DeviceClassSourceConfig{{Type: kueuev1.DeviceClassSourceTypeCounter}}}}}

	tests := map[string]struct {
		resources kueuev1.Resources
		enabled   bool
		want      []string
	}{
		"capacity source requires consumable capacity gate": {
			resources: capacityResources,
			want:      []string{draConsumableCapacityMissingDependency},
		},
		"capacity source with consumable capacity gate has no missing dependency": {
			resources: capacityResources,
			enabled:   true,
		},
		"counter source does not require consumable capacity gate": {
			resources: counterResources,
		},
		"no sources have no missing dependency": {},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, missingConsumableCapacityDependencies(tc.resources, tc.enabled)); diff != "" {
				t.Fatalf("unexpected dependencies (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMissingDisaggregatedSetDependencies(t *testing.T) {
	disaggregatedSetResources := &metav1.APIResourceList{
		GroupVersion: "disaggregatedset.x-k8s.io/v1",
		APIResources: []metav1.APIResource{
			{Name: "disaggregatedsets", Kind: "DisaggregatedSet", Namespaced: true},
		},
	}
	tests := map[string]struct {
		frameworks []kueuev1.KueueIntegration
		resources  []*metav1.APIResourceList
		listErr    error
		want       []string
		wantErr    bool
	}{
		"disaggregatedset selected and API group not served": {
			frameworks: []kueuev1.KueueIntegration{kueuev1.KueueIntegrationDisaggregatedSet},
			want:       []string{disaggregatedSetMissingDependency},
		},
		"disaggregatedset selected and only another version served": {
			frameworks: []kueuev1.KueueIntegration{kueuev1.KueueIntegrationDisaggregatedSet},
			resources: []*metav1.APIResourceList{{
				GroupVersion: "disaggregatedset.x-k8s.io/v1alpha1",
				APIResources: []metav1.APIResource{{Name: "disaggregatedsets", Kind: "DisaggregatedSet", Namespaced: true}},
			}},
			want: []string{disaggregatedSetMissingDependency},
		},
		"disaggregatedset selected and v1 served without the DisaggregatedSet kind": {
			frameworks: []kueuev1.KueueIntegration{kueuev1.KueueIntegrationDisaggregatedSet},
			resources: []*metav1.APIResourceList{{
				GroupVersion: "disaggregatedset.x-k8s.io/v1",
				APIResources: []metav1.APIResource{{Name: "disaggregatedsetrolescalers", Kind: "DisaggregatedSetRoleScaler", Namespaced: true}},
			}},
			want: []string{disaggregatedSetMissingDependency},
		},
		"disaggregatedset selected and v1 served": {
			frameworks: []kueuev1.KueueIntegration{kueuev1.KueueIntegrationLeaderWorkerSet, kueuev1.KueueIntegrationDisaggregatedSet},
			resources:  []*metav1.APIResourceList{disaggregatedSetResources},
		},
		"disaggregatedset selected and discovery fails": {
			frameworks: []kueuev1.KueueIntegration{kueuev1.KueueIntegrationDisaggregatedSet},
			listErr:    errors.New("discovery unavailable"),
			want:       []string{disaggregatedSetMissingDependency},
			wantErr:    true,
		},
		"disaggregatedset not selected and API not served": {
			frameworks: []kueuev1.KueueIntegration{kueuev1.KueueIntegrationBatchJob, kueuev1.KueueIntegrationLeaderWorkerSet},
		},
		"no frameworks": {},
	}

	t.Run("canceled context reports the API as missing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		fake := &clientgotesting.Fake{Resources: []*metav1.APIResourceList{disaggregatedSetResources}}
		fake.PrependReactor("get", "resource", func(clientgotesting.Action) (bool, runtime.Object, error) {
			return true, nil, ctx.Err()
		})
		got, err := missingDisaggregatedSetDependencies(ctx, []kueuev1.KueueIntegration{kueuev1.KueueIntegrationDisaggregatedSet}, &fakediscovery.FakeDiscovery{Fake: fake})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if diff := cmp.Diff([]string{disaggregatedSetMissingDependency}, got); diff != "" {
			t.Fatalf("unexpected dependencies (-want +got):\n%s", diff)
		}
	})

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &clientgotesting.Fake{Resources: tc.resources}
			if tc.listErr != nil {
				fake.PrependReactor("get", "resource", func(clientgotesting.Action) (bool, runtime.Object, error) {
					return true, nil, tc.listErr
				})
			}
			got, err := missingDisaggregatedSetDependencies(t.Context(), tc.frameworks, &fakediscovery.FakeDiscovery{Fake: fake})
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected error: %v, want error: %t", err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("unexpected dependencies (-want +got):\n%s", diff)
			}
			if !slices.Contains(tc.frameworks, kueuev1.KueueIntegrationDisaggregatedSet) && len(fake.Actions()) != 0 {
				t.Fatalf("expected no discovery calls when DisaggregatedSet is not selected, got %d", len(fake.Actions()))
			}
		})
	}
}

// TestPodBasedWebhookAssets runs the webhook filtering against the bundled
// webhook configurations. Every webhook in the assets must map to a registered
// framework, otherwise the filtering panics during reconciliation.
func TestPodBasedWebhookAssets(t *testing.T) {
	validating := resourceread.ReadValidatingWebhookConfigurationV1OrDie(bindata.MustAsset("assets/kueue-operator/validatingwebhook.yaml"))
	mutating := resourceread.ReadMutatingWebhookConfigurationV1OrDie(bindata.MustAsset("assets/kueue-operator/mutatingwebhook.yaml"))
	allFrameworks := []kueuev1.KueueIntegration{
		kueuev1.KueueIntegrationBatchJob,
		kueuev1.KueueIntegrationRayJob,
		kueuev1.KueueIntegrationRayCluster,
		kueuev1.KueueIntegrationRayService,
		kueuev1.KueueIntegrationJobSet,
		kueuev1.KueueIntegrationMPIJob,
		kueuev1.KueueIntegrationPaddleJob,
		kueuev1.KueueIntegrationPyTorchJob,
		kueuev1.KueueIntegrationTFJob,
		kueuev1.KueueIntegrationTrainJob,
		kueuev1.KueueIntegrationXGBoostJob,
		kueuev1.KueueIntegrationJaxJob,
		kueuev1.KueueIntegrationAppWrapper,
		kueuev1.KueueIntegrationPod,
		kueuev1.KueueIntegrationDeployment,
		kueuev1.KueueIntegrationStatefulSet,
		kueuev1.KueueIntegrationLeaderWorkerSet,
		kueuev1.KueueIntegrationSparkApplication,
		kueuev1.KueueIntegrationDisaggregatedSet,
	}
	withoutDisaggregatedSet := slices.DeleteFunc(slices.Clone(allFrameworks), func(f kueuev1.KueueIntegration) bool {
		return f == kueuev1.KueueIntegrationDisaggregatedSet
	})

	tests := map[string]struct {
		frameworks           []kueuev1.KueueIntegration
		wantDisaggregatedSet bool
		wantPod              bool
	}{
		"all frameworks": {
			frameworks:           allFrameworks,
			wantDisaggregatedSet: true,
			wantPod:              true,
		},
		"all frameworks except disaggregatedset": {
			frameworks: withoutDisaggregatedSet,
			wantPod:    true,
		},
		"disaggregatedset only": {
			frameworks:           []kueuev1.KueueIntegration{kueuev1.KueueIntegrationDisaggregatedSet},
			wantDisaggregatedSet: true,
			wantPod:              true,
		},
		"batch job only": {
			frameworks: []kueuev1.KueueIntegration{kueuev1.KueueIntegrationBatchJob},
		},
		"no frameworks": {},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("webhook filtering panicked on the bundled webhook assets: %v", r)
				}
			}()
			cfg := kueuev1.KueueConfiguration{Integrations: kueuev1.Integrations{Frameworks: tc.frameworks}}
			gotValidating := webhook.ModifyPodBasedValidatingWebhook(cfg, validating)
			gotMutating := webhook.ModifyPodBasedMutatingWebhook(cfg, mutating)

			validatingSelectors := map[string]*metav1.LabelSelector{}
			for _, wh := range gotValidating.Webhooks {
				validatingSelectors[wh.Name] = wh.NamespaceSelector
			}
			mutatingSelectors := map[string]*metav1.LabelSelector{}
			for _, wh := range gotMutating.Webhooks {
				mutatingSelectors[wh.Name] = wh.NamespaceSelector
			}
			for name, want := range map[string]bool{"vdisaggregatedset.kb.io": tc.wantDisaggregatedSet, "vpod.kb.io": tc.wantPod} {
				selector, got := validatingSelectors[name]
				if got != want {
					t.Errorf("validating webhook %q present: %t, want %t", name, got, want)
				}
				if got && !hasManagedNamespaceRequirement(selector) {
					t.Errorf("validating webhook %q namespace selector does not require the managed label: %+v", name, selector)
				}
			}
			for name, want := range map[string]bool{"mdisaggregatedset.kb.io": tc.wantDisaggregatedSet, "mpod.kb.io": tc.wantPod} {
				selector, got := mutatingSelectors[name]
				if got != want {
					t.Errorf("mutating webhook %q present: %t, want %t", name, got, want)
				}
				if got && !hasManagedNamespaceRequirement(selector) {
					t.Errorf("mutating webhook %q namespace selector does not require the managed label: %+v", name, selector)
				}
			}
		})
	}
}

func hasManagedNamespaceRequirement(selector *metav1.LabelSelector) bool {
	if selector == nil {
		return false
	}
	return slices.ContainsFunc(selector.MatchExpressions, func(req metav1.LabelSelectorRequirement) bool {
		return req.Key == "kueue.openshift.io/managed" &&
			req.Operator == metav1.LabelSelectorOpIn &&
			slices.Equal(req.Values, []string{"true"})
	})
}

// TestDisaggregatedSetRBACAssets checks the DisaggregatedSet permissions synced
// from upstream Kueue: the Kueue manager can read DisaggregatedSets, and the
// editor and viewer roles are aggregated into the Kueue batch admin and user roles.
func TestDisaggregatedSetRBACAssets(t *testing.T) {
	managerRole := resourceread.ReadClusterRoleV1OrDie(bindata.MustAsset("assets/kueue-operator/clusterroles/clusterrole-manager-role.yaml"))
	if err := scopeManagerRoleResourceNames(managerRole); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, rule := range managerRole.Rules {
		if slices.Contains(rule.APIGroups, "disaggregatedset.x-k8s.io") && slices.Contains(rule.Resources, "disaggregatedsets") {
			found = true
			if diff := cmp.Diff([]string{"get", "list", "watch"}, rule.Verbs); diff != "" {
				t.Errorf("unexpected manager role verbs for disaggregatedsets (-want +got):\n%s", diff)
			}
			if len(rule.ResourceNames) != 0 {
				t.Errorf("manager role rule for disaggregatedsets is scoped to resource names %v", rule.ResourceNames)
			}
		}
	}
	if !found {
		t.Fatal("manager role has no rule for disaggregatedsets")
	}

	tests := map[string]struct {
		asset     string
		name      string
		wantVerbs []string
	}{
		"editor": {
			asset:     "assets/kueue-operator/clusterroles/clusterrole-disaggregatedset-editor-role.yaml",
			name:      "kueue-disaggregatedset-editor-role",
			wantVerbs: []string{"create", "delete", "get", "list", "patch", "update", "watch"},
		},
		"viewer": {
			asset:     "assets/kueue-operator/clusterroles/clusterrole-disaggregatedset-viewer-role.yaml",
			name:      "kueue-disaggregatedset-viewer-role",
			wantVerbs: []string{"get", "list", "watch"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			role := resourceread.ReadClusterRoleV1OrDie(bindata.MustAsset(tc.asset))
			if role.Name != tc.name {
				t.Errorf("unexpected role name %q, want %q", role.Name, tc.name)
			}
			if role.AggregationRule != nil {
				t.Errorf("role %q has an aggregation rule, so the operator would not apply it", role.Name)
			}
			for _, label := range []string{"rbac.kueue.x-k8s.io/batch-admin", "rbac.kueue.x-k8s.io/batch-user"} {
				if role.Labels[label] != "true" {
					t.Errorf("role %q is missing aggregation label %s=true", role.Name, label)
				}
			}
			var verbs []string
			for _, rule := range role.Rules {
				if slices.Contains(rule.Resources, "disaggregatedsets") {
					verbs = rule.Verbs
				}
			}
			if diff := cmp.Diff(tc.wantVerbs, verbs); diff != "" {
				t.Errorf("unexpected verbs for disaggregatedsets (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIsKubernetesMinorAtLeast(t *testing.T) {
	tests := map[string]struct {
		minor string
		want  bool
	}{
		"below requested minor": {minor: "35", want: false},
		"requested minor":       {minor: "36", want: true},
		"above requested minor": {minor: "37", want: true},
		"minor with plus":       {minor: "36+", want: true},
		"invalid minor":         {minor: "36-beta.0", want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := isKubernetesMinorAtLeast(&fakediscovery.FakeDiscovery{
				Fake: &clientgotesting.Fake{},
				FakedServerVersion: &version.Info{
					Major: "1",
					Minor: tc.minor,
				},
			}, 36)
			if got != tc.want {
				t.Fatalf("isKubernetesMinorAtLeast() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScopeManagerRoleResourceNames(t *testing.T) {
	// Derive the expected CRD resource names from bindata, matching the
	// production code path so the test stays in sync automatically.
	expectedCRDNames, err := kueueCRDNames()
	if err != nil {
		t.Fatalf("failed to derive expected CRD names: %v", err)
	}

	tests := map[string]struct {
		input    *rbacv1.ClusterRole
		expected *rbacv1.ClusterRole
	}{
		"adds resourceNames to webhook and CRD rules": {
			input: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: "kueue-manager-role"},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups: []string{""},
						Resources: []string{"pods"},
						Verbs:     []string{"get", "list", "watch"},
					},
					{
						APIGroups: []string{"admissionregistration.k8s.io"},
						Resources: []string{"mutatingwebhookconfigurations", "validatingwebhookconfigurations"},
						Verbs:     []string{"get", "list", "update", "watch"},
					},
					{
						APIGroups: []string{"apiextensions.k8s.io"},
						Resources: []string{"customresourcedefinitions"},
						Verbs:     []string{"get", "list", "update", "watch"},
					},
				},
			},
			expected: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: "kueue-manager-role"},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups: []string{""},
						Resources: []string{"pods"},
						Verbs:     []string{"get", "list", "watch"},
					},
					{
						APIGroups: []string{"admissionregistration.k8s.io"},
						Resources: []string{"mutatingwebhookconfigurations", "validatingwebhookconfigurations"},
						ResourceNames: []string{
							"kueue-mutating-webhook-configuration",
							"kueue-validating-webhook-configuration",
						},
						Verbs: []string{"get", "list", "update", "watch"},
					},
					{
						APIGroups:     []string{"apiextensions.k8s.io"},
						Resources:     []string{"customresourcedefinitions"},
						ResourceNames: expectedCRDNames,
						Verbs:         []string{"get", "list", "update", "watch"},
					},
				},
			},
		},
		"does not modify unrelated rules": {
			input: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: "kueue-manager-role"},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups: []string{"batch"},
						Resources: []string{"jobs"},
						Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
					},
					{
						APIGroups: []string{"apps"},
						Resources: []string{"deployments"},
						Verbs:     []string{"get", "list", "watch"},
					},
				},
			},
			expected: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: "kueue-manager-role"},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups: []string{"batch"},
						Resources: []string{"jobs"},
						Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
					},
					{
						APIGroups: []string{"apps"},
						Resources: []string{"deployments"},
						Verbs:     []string{"get", "list", "watch"},
					},
				},
			},
		},
		"handles role with only webhook rule": {
			input: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: "kueue-manager-role"},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups: []string{"admissionregistration.k8s.io"},
						Resources: []string{"mutatingwebhookconfigurations", "validatingwebhookconfigurations"},
						Verbs:     []string{"get", "list", "update", "watch"},
					},
				},
			},
			expected: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: "kueue-manager-role"},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups: []string{"admissionregistration.k8s.io"},
						Resources: []string{"mutatingwebhookconfigurations", "validatingwebhookconfigurations"},
						ResourceNames: []string{
							"kueue-mutating-webhook-configuration",
							"kueue-validating-webhook-configuration",
						},
						Verbs: []string{"get", "list", "update", "watch"},
					},
				},
			},
		},
		"keeps the named ClusterProfile CRD get rule": {
			input: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: "kueue-manager-role"},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups: []string{"apiextensions.k8s.io"},
						Resources: []string{"customresourcedefinitions"},
						Verbs:     []string{"list", "watch"},
					},
					{
						APIGroups:     []string{"apiextensions.k8s.io"},
						Resources:     []string{"customresourcedefinitions"},
						ResourceNames: []string{"clusterprofiles.multicluster.x-k8s.io"},
						Verbs:         []string{"get"},
					},
					{
						APIGroups:     []string{"apiextensions.k8s.io"},
						Resources:     []string{"customresourcedefinitions"},
						ResourceNames: []string{"workloads.kueue.x-k8s.io"},
						Verbs:         []string{"get", "update"},
					},
				},
			},
			expected: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: "kueue-manager-role"},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups:     []string{"apiextensions.k8s.io"},
						Resources:     []string{"customresourcedefinitions"},
						ResourceNames: expectedCRDNames,
						Verbs:         []string{"list", "watch"},
					},
					{
						APIGroups:     []string{"apiextensions.k8s.io"},
						Resources:     []string{"customresourcedefinitions"},
						ResourceNames: []string{"clusterprofiles.multicluster.x-k8s.io"},
						Verbs:         []string{"get"},
					},
					{
						APIGroups:     []string{"apiextensions.k8s.io"},
						Resources:     []string{"customresourcedefinitions"},
						ResourceNames: expectedCRDNames,
						Verbs:         []string{"get", "update"},
					},
				},
			},
		},
		"scopes a ClusterProfile CRD rule that allows writes": {
			input: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: "kueue-manager-role"},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups:     []string{"apiextensions.k8s.io"},
						Resources:     []string{"customresourcedefinitions"},
						ResourceNames: []string{"clusterprofiles.multicluster.x-k8s.io"},
						Verbs:         []string{"get", "update"},
					},
				},
			},
			expected: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: "kueue-manager-role"},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups:     []string{"apiextensions.k8s.io"},
						Resources:     []string{"customresourcedefinitions"},
						ResourceNames: expectedCRDNames,
						Verbs:         []string{"get", "update"},
					},
				},
			},
		},
		"scopes a named get rule for another CRD": {
			input: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: "kueue-manager-role"},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups:     []string{"apiextensions.k8s.io"},
						Resources:     []string{"customresourcedefinitions"},
						ResourceNames: []string{"widgets.example.com"},
						Verbs:         []string{"get"},
					},
				},
			},
			expected: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: "kueue-manager-role"},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups:     []string{"apiextensions.k8s.io"},
						Resources:     []string{"customresourcedefinitions"},
						ResourceNames: expectedCRDNames,
						Verbs:         []string{"get"},
					},
				},
			},
		},
		"no rules is a no-op": {
			input: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: "kueue-manager-role"},
			},
			expected: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: "kueue-manager-role"},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if err := scopeManagerRoleResourceNames(tc.input); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.expected, tc.input); diff != "" {
				t.Errorf("unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

// crdAccessAllowed reports whether role allows verb on the CRD named name.
func crdAccessAllowed(role *rbacv1.ClusterRole, verb, name string) bool {
	matches := func(values []string, value, all string) bool {
		return slices.Contains(values, value) || slices.Contains(values, all)
	}
	for _, rule := range role.Rules {
		if matches(rule.APIGroups, "apiextensions.k8s.io", rbacv1.APIGroupAll) &&
			matches(rule.Resources, "customresourcedefinitions", rbacv1.ResourceAll) &&
			matches(rule.Verbs, verb, rbacv1.VerbAll) &&
			(len(rule.ResourceNames) == 0 || slices.Contains(rule.ResourceNames, name)) {
			return true
		}
	}
	return false
}

// TestManagerRoleCRDAccess checks the CRD permissions of the bundled manager
// role after scoping: the Kueue manager may get the ClusterProfile CRD, which
// it reads at startup when the MultiKueueClusterProfile feature gate is
// enabled, but not change it, and may not read or change any other CRD that
// Kueue does not own.
func TestManagerRoleCRDAccess(t *testing.T) {
	role := resourceread.ReadClusterRoleV1OrDie(bindata.MustAsset("assets/kueue-operator/clusterroles/clusterrole-manager-role.yaml"))
	if err := scopeManagerRoleResourceNames(role); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	kueueCRDs, err := kueueCRDNames()
	if err != nil {
		t.Fatalf("failed to derive CRD names: %v", err)
	}

	for _, verb := range []string{"get", "create", "update", "patch", "delete"} {
		if got, want := crdAccessAllowed(role, verb, clusterProfileCRDName), verb == "get"; got != want {
			t.Errorf("%s on %s: allowed %t, want %t", verb, clusterProfileCRDName, got, want)
		}
		if crdAccessAllowed(role, verb, "widgets.example.com") {
			t.Errorf("%s on an unrelated CRD is allowed", verb)
		}
	}
	for _, name := range kueueCRDs {
		for _, verb := range []string{"get", "update"} {
			if !crdAccessAllowed(role, verb, name) {
				t.Errorf("%s on %s is not allowed", verb, name)
			}
		}
		for _, verb := range []string{"create", "delete"} {
			if crdAccessAllowed(role, verb, name) {
				t.Errorf("%s on %s is allowed", verb, name)
			}
		}
	}
	for _, rule := range role.Rules {
		if isCRDRule(&rule) && len(rule.ResourceNames) == 0 {
			t.Errorf("CRD rule without resource names: %+v", rule)
		}
	}
}
