package operator

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
