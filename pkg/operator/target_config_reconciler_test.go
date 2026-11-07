package operator

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/kueue-operator/bindata"
	kueuev1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func customNoUpgradeFeatureGate(enabled, disabled []string) *configv1.FeatureGate {
	toNames := func(in []string) []configv1.FeatureGateName {
		if in == nil {
			return nil
		}
		names := make([]configv1.FeatureGateName, 0, len(in))
		for _, name := range in {
			names = append(names, configv1.FeatureGateName(name))
		}
		return names
	}
	return &configv1.FeatureGate{
		Spec: configv1.FeatureGateSpec{
			FeatureGateSelection: configv1.FeatureGateSelection{
				FeatureSet: configv1.CustomNoUpgrade,
				CustomNoUpgrade: &configv1.CustomFeatureGates{
					Enabled:  toNames(enabled),
					Disabled: toNames(disabled),
				},
			},
		},
	}
}

func TestDRAFeatureGateState(t *testing.T) {
	readErr := errors.New("simulated FeatureGate read failure")

	tests := map[string]struct {
		consultFeatureGate          bool
		versionEnablesConsumable    bool
		versionEnablesPartitionable bool
		previousConsumable          bool
		previousPartitionable       bool
		featureGate                 *configv1.FeatureGate
		featureGateErr              error
		wantConsumable              bool
		wantPartitionable           bool
	}{
		// Issue 1: on K8s 1.36+ a FeatureGate read failure must not clear the
		// version-default-on gates. DRAPartitionableDevices must stay enabled,
		// matching DRAConsumableCapacity.
		"featuregate read error on 1.36+ keeps version defaults": {
			consultFeatureGate:          true,
			versionEnablesConsumable:    true,
			versionEnablesPartitionable: true,
			previousConsumable:          false,
			previousPartitionable:       false,
			featureGateErr:              readErr,
			wantConsumable:              true,
			wantPartitionable:           true,
		},
		"featuregate read error below 1.36 preserves previous state": {
			consultFeatureGate:          true,
			versionEnablesConsumable:    false,
			versionEnablesPartitionable: false,
			previousConsumable:          true,
			previousPartitionable:       true,
			featureGateErr:              readErr,
			wantConsumable:              true,
			wantPartitionable:           true,
		},
		// Issue 2: on K8s 1.36+ an admin can opt out through CustomNoUpgrade.Disabled.
		"custom no upgrade disabled on 1.36+ turns gates off": {
			consultFeatureGate:          true,
			versionEnablesConsumable:    true,
			versionEnablesPartitionable: true,
			featureGate:                 customNoUpgradeFeatureGate(nil, []string{draPartitionableDevicesFeatureGate, draConsumableCapacityFeatureGate}),
			wantConsumable:              false,
			wantPartitionable:           false,
		},
		"custom no upgrade disables only partitionable on 1.36+": {
			consultFeatureGate:          true,
			versionEnablesConsumable:    true,
			versionEnablesPartitionable: true,
			featureGate:                 customNoUpgradeFeatureGate(nil, []string{draPartitionableDevicesFeatureGate}),
			wantConsumable:              true,
			wantPartitionable:           false,
		},
		"custom no upgrade enabled below 1.36 turns gates on": {
			consultFeatureGate:          true,
			versionEnablesConsumable:    false,
			versionEnablesPartitionable: false,
			featureGate:                 customNoUpgradeFeatureGate([]string{draPartitionableDevicesFeatureGate, draConsumableCapacityFeatureGate}, nil),
			wantConsumable:              true,
			wantPartitionable:           true,
		},
		"custom no upgrade disabled overrides enabled for same gate": {
			consultFeatureGate:          true,
			versionEnablesConsumable:    false,
			versionEnablesPartitionable: false,
			featureGate:                 customNoUpgradeFeatureGate([]string{draPartitionableDevicesFeatureGate}, []string{draPartitionableDevicesFeatureGate}),
			wantConsumable:              false,
			wantPartitionable:           false,
		},
		"no featuregate consult on 1.36+ uses version defaults": {
			consultFeatureGate:          false,
			versionEnablesConsumable:    true,
			versionEnablesPartitionable: true,
			wantConsumable:              true,
			wantPartitionable:           true,
		},
		"default featureset on 1.36+ keeps version defaults": {
			consultFeatureGate:          true,
			versionEnablesConsumable:    true,
			versionEnablesPartitionable: true,
			featureGate:                 &configv1.FeatureGate{},
			wantConsumable:              true,
			wantPartitionable:           true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			gotConsumable, gotPartitionable := draFeatureGateState(
				tc.consultFeatureGate,
				tc.versionEnablesConsumable,
				tc.versionEnablesPartitionable,
				tc.previousConsumable,
				tc.previousPartitionable,
				tc.featureGate,
				tc.featureGateErr,
			)
			if gotConsumable != tc.wantConsumable {
				t.Errorf("consumableCapacityEnabled = %v, want %v", gotConsumable, tc.wantConsumable)
			}
			if gotPartitionable != tc.wantPartitionable {
				t.Errorf("partitionableDevicesEnabled = %v, want %v", gotPartitionable, tc.wantPartitionable)
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
