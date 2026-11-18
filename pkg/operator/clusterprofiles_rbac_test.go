package operator

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"
	"k8s.io/utils/clock"

	"github.com/openshift/kueue-operator/bindata"
)

// TestManageClusterProfilesRBAC checks that the operator grants the Kueue
// manager's service account read access to ClusterProfiles in the operator
// namespace only, as the manager needs when the MultiKueueClusterProfile
// feature gate is enabled and the ClusterProfile CRD is installed.
func TestManageClusterProfilesRBAC(t *testing.T) {
	const namespace = "openshift-kueue-operator"
	client := fake.NewSimpleClientset()
	c := &TargetConfigReconciler{
		kubeClient:        client,
		kubeInformer:      informers.NewSharedInformerFactory(client, 0),
		resourceCache:     resourceapply.NewResourceCache(),
		eventRecorder:     events.NewInMemoryRecorder("test", clock.RealClock{}),
		operatorNamespace: namespace,
	}
	owner := metav1.OwnerReference{APIVersion: "kueue.openshift.io/v1", Kind: "Kueue", Name: "cluster", UID: "uid"}
	specAnnotations := map[string]string{}

	if err := c.manageClusterProfilesRBAC(t.Context(), specAnnotations, owner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	role, err := client.RbacV1().Roles(namespace).Get(t.Context(), "kueue-manager-clusterprofiles-role", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Role not created in %s: %v", namespace, err)
	}
	wantRules := []rbacv1.PolicyRule{{
		APIGroups: []string{"multicluster.x-k8s.io"},
		Resources: []string{"clusterprofiles"},
		Verbs:     []string{"get", "list", "watch"},
	}}
	if diff := cmp.Diff(wantRules, role.Rules); diff != "" {
		t.Errorf("unexpected Role rules (-want,+got):\n%s", diff)
	}
	if diff := cmp.Diff([]metav1.OwnerReference{owner}, role.OwnerReferences); diff != "" {
		t.Errorf("unexpected Role owner references (-want,+got):\n%s", diff)
	}

	binding, err := client.RbacV1().RoleBindings(namespace).Get(t.Context(), "kueue-manager-clusterprofiles-rolebinding", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("RoleBinding not created in %s: %v", namespace, err)
	}
	wantRoleRef := rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: role.Name}
	if diff := cmp.Diff(wantRoleRef, binding.RoleRef); diff != "" {
		t.Errorf("unexpected RoleBinding roleRef (-want,+got):\n%s", diff)
	}
	// The subject is the service account the Kueue manager Deployment runs as.
	serviceAccount := resourceread.ReadServiceAccountV1OrDie(bindata.MustAsset("assets/kueue-operator/serviceaccount.yaml"))
	deployment := resourceread.ReadDeploymentV1OrDie(bindata.MustAsset("assets/kueue-operator/deployment.yaml"))
	if deployment.Spec.Template.Spec.ServiceAccountName != serviceAccount.Name {
		t.Fatalf("the manager Deployment runs as %q, not %q", deployment.Spec.Template.Spec.ServiceAccountName, serviceAccount.Name)
	}
	wantSubjects := []rbacv1.Subject{{Kind: "ServiceAccount", Name: serviceAccount.Name, Namespace: namespace}}
	if diff := cmp.Diff(wantSubjects, binding.Subjects); diff != "" {
		t.Errorf("unexpected RoleBinding subjects (-want,+got):\n%s", diff)
	}

	for _, key := range []string{"role/" + role.Name, "rolebinding/" + binding.Name} {
		if specAnnotations[key] == "" {
			t.Errorf("missing spec annotation %q", key)
		}
	}

	// Only the namespaced Role and RoleBinding are written.
	for _, action := range client.Actions() {
		if action.GetVerb() == "get" || action.GetVerb() == "list" || action.GetVerb() == "watch" {
			continue
		}
		if action.GetNamespace() != namespace || (action.GetResource().Resource != "roles" && action.GetResource().Resource != "rolebindings") {
			t.Errorf("unexpected write: %s %s in %q", action.GetVerb(), action.GetResource().Resource, action.GetNamespace())
		}
		if create, ok := action.(clientgotesting.CreateAction); ok {
			if obj, ok := create.GetObject().(metav1.Object); ok && obj.GetNamespace() != namespace {
				t.Errorf("object %s created outside %s", obj.GetName(), namespace)
			}
		}
	}
}
