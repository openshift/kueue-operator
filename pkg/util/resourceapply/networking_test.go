package resourceapply

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/openshift/library-go/pkg/operator/events"
)

func TestApplyNetworkPolicy(t *testing.T) {
	newObject := func() *networkingv1.NetworkPolicy {
		return &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "foo",
				Name:      "bar",
			},
			Spec: networkingv1.NetworkPolicySpec{
				PolicyTypes: []networkingv1.PolicyType{
					networkingv1.PolicyTypeIngress,
					networkingv1.PolicyTypeEgress,
				},
			},
		}
	}

	tests := []struct {
		name string
		// current represents what exists on the cluster
		// desired represents the desired object passed to ApplyNetworkPolicy
		// expected represents the object on the cluster after ApplyNetworkPolicy returns
		setup func() (current, desired, expected *networkingv1.NetworkPolicy)
		// used to mutate the object returned from ApplyNetworkPolicy
		mutator func(*networkingv1.NetworkPolicy)

		// expectation
		operations []string
		diff       bool
		err        error
	}{
		{
			name: "object does not exist on cluster, should be created",
			setup: func() (current, desired, expected *networkingv1.NetworkPolicy) {
				return nil, newObject(), newObject()
			},
			diff:       false,
			operations: []string{"get", "create"},
		},
		{
			name: "object exists on cluster, no change in spec desired, should not call update",
			setup: func() (current, desired, expected *networkingv1.NetworkPolicy) {
				return newObject(), newObject(), newObject()
			},
			diff:       false,
			operations: []string{"get"}, // update not expected
		},
		{
			name: "object exists on cluster, desired spec changes, should call update",
			setup: func() (current, desired, expected *networkingv1.NetworkPolicy) {
				current, desired = newObject(), newObject()
				desired.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
					{
						Ports: []networkingv1.NetworkPolicyPort{
							{
								Protocol: ptr.To(corev1.ProtocolTCP),
								Port:     &intstr.IntOrString{IntVal: 6443},
							},
						},
					},
				}

				expected = desired.DeepCopy()
				// resourcemerge.EnsureObjectMeta creates an empty instances, see
				// https://github.com/openshift/library-go/blob/ac3ba9eb16a23890629f719f3fe11428ad395796/pkg/operator/resource/resourcemerge/object_merger.go#L154-L156
				expected.Labels = map[string]string{}
				expected.Annotations = map[string]string{}
				expected.OwnerReferences = []metav1.OwnerReference{}

				return current, desired, expected
			},
			diff:       true,
			operations: []string{"get", "update"},
		},
		{
			name: "object exists on cluster, desired spec changes, existing labels/annotations/ownerreferences should be preserved",
			setup: func() (current, desired, expected *networkingv1.NetworkPolicy) {
				current, desired = newObject(), newObject()

				current.Labels = map[string]string{"key-1": "1"}
				current.Annotations = map[string]string{"key-2": "2"}
				current.OwnerReferences = []metav1.OwnerReference{
					{Name: "foo"},
				}

				desired.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
					{
						Ports: []networkingv1.NetworkPolicyPort{
							{
								Protocol: ptr.To(corev1.ProtocolTCP),
								Port:     &intstr.IntOrString{IntVal: 6443},
							},
						},
					},
				}
				desired.Labels = map[string]string{"key-3": "3"}
				desired.Annotations = map[string]string{"key-4": "4"}
				desired.OwnerReferences = []metav1.OwnerReference{
					{Name: "bar"},
				}

				expected = desired.DeepCopy()
				expected.Labels["key-1"] = "1"
				expected.Annotations["key-2"] = "2"
				expected.OwnerReferences = []metav1.OwnerReference{
					{Name: "foo"},
					{Name: "bar"},
				}

				return current, desired, expected
			},
			diff:       true,
			operations: []string{"get", "update"},
		},

		{
			name: "object exists on cluster, desired object should be immutable",
			setup: func() (current, desired, expected *networkingv1.NetworkPolicy) {
				current, desired = newObject(), newObject()
				desired.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
					{
						Ports: []networkingv1.NetworkPolicyPort{
							{
								Protocol: ptr.To(corev1.ProtocolTCP),
								Port:     &intstr.IntOrString{IntVal: 6443},
							},
						},
					},
				}

				expected = desired.DeepCopy()
				expected.Labels = map[string]string{}
				expected.Annotations = map[string]string{}
				expected.OwnerReferences = []metav1.OwnerReference{}

				return current, desired, expected
			},
			mutator: func(p *networkingv1.NetworkPolicy) {
				// mutate a pointer field that will be shared between the
				// two objects if DeepCopy is not performed
				p.Spec.Ingress[0].Ports[0].Port.IntVal = 8443
			},
			diff:       true,
			operations: []string{"get", "update"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current, desired, expected := test.setup()
			// make a copy so we can determine if the object in desired
			// passed to ApplyNetworkPolicy has been mutated
			desiredCopy := desired.DeepCopy()

			exists := []runtime.Object{}
			if current != nil {
				exists = append(exists, current)
			}
			client := fake.NewSimpleClientset(exists...)
			operationsObserved := make([]string, 0)
			client.PrependReactor("*", "*", func(action ktesting.Action) (bool, runtime.Object, error) {
				operationsObserved = append(operationsObserved, action.GetVerb())
				return false, nil, nil
			})

			recorder := events.NewInMemoryRecorder("test", clocktesting.NewFakePassiveClock(time.Now()))
			currentGot, diff, err := ApplyNetworkPolicy(context.Background(), client.NetworkingV1(), recorder, desired)

			if want, got := expected, currentGot; !cmp.Equal(want, got) {
				t.Errorf("expected object to be equal, diff: %s", cmp.Diff(want, got))
			}
			if want, got := test.diff, diff; want != got {
				t.Errorf("expected modified to be: %t, but got: %t", want, got)
			}
			if want, got := test.err, err; want != got {
				t.Errorf("expected err to be: %v, but got: %v", want, got)
			}
			if want, got := test.operations, operationsObserved; !cmp.Equal(want, got) {
				t.Errorf("expected operations to match, diff: %s", cmp.Diff(want, got))
			}

			// the original spec in the desired object passed to ApplyNetworkPolicy should
			// be immutable, mutating the object returned from ApplyNetworkPolicy
			// should never mutate the original object
			if test.mutator != nil {
				t.Logf("mutating the object returned from ApplyNetworkPolicy")
				test.mutator(currentGot)
				if want, got := desiredCopy, desired; !cmp.Equal(want, got) {
					t.Errorf("did not expect the object passed to ApplyNetworkPolicy to be mutated, diff: %s", cmp.Diff(want, got))
				}
			}
		})
	}
}

func TestReadNetworkPolicyV1OrDie(t *testing.T) {
	bytes := []byte(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kueue-deny-all
  namespace: openshift-kueue-operator
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: kueue
  policyTypes:
  - Ingress
  - Egress`)

	if obj := ReadNetworkPolicyV1OrDie(bytes); obj == nil {
		t.Errorf("expected a valid object of type: %T", &networkingv1.NetworkPolicy{})
	}
}
