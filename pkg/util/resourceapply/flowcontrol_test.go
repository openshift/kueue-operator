package resourceapply

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	flowcontrolv1 "k8s.io/api/flowcontrol/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/openshift/library-go/pkg/operator/events"
)

func TestApplyFlowSchema(t *testing.T) {
	newObject := func() *flowcontrolv1.FlowSchema {
		return &flowcontrolv1.FlowSchema{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-kueue-visibility",
			},
			Spec: flowcontrolv1.FlowSchemaSpec{
				PriorityLevelConfiguration: flowcontrolv1.PriorityLevelConfigurationReference{Name: "kueue-visibility"},
			},
		}
	}

	tests := []struct {
		name       string
		setup      func() (current, desired, expected *flowcontrolv1.FlowSchema)
		mutator    func(*flowcontrolv1.FlowSchema)
		operations []string
		diff       bool
		err        error
	}{
		{
			name: "object does not exist on cluster, should be created",
			setup: func() (current, desired, expected *flowcontrolv1.FlowSchema) {
				return nil, newObject(), newObject()
			},
			diff:       false,
			operations: []string{"get", "create"},
		},
		{
			name: "object exists on cluster, no change in spec desired, should not call update",
			setup: func() (current, desired, expected *flowcontrolv1.FlowSchema) {
				return newObject(), newObject(), newObject()
			},
			diff:       false,
			operations: []string{"get"},
		},
		{
			name: "object exists on cluster, desired spec changes, should call update",
			setup: func() (current, desired, expected *flowcontrolv1.FlowSchema) {
				current, desired = newObject(), newObject()
				// change spec by adding a rule
				desired.Spec.Rules = []flowcontrolv1.PolicyRulesWithSubjects{
					{
						Subjects: []flowcontrolv1.Subject{{
							Kind:  flowcontrolv1.SubjectKindGroup,
							Group: &flowcontrolv1.GroupSubject{Name: "system:authenticated"},
						}},
					},
				}

				expected = desired.DeepCopy()
				expected.Labels = map[string]string{}
				expected.Annotations = map[string]string{}
				expected.OwnerReferences = []metav1.OwnerReference{}
				return current, desired, expected
			},
			diff:       true,
			operations: []string{"get", "update"},
		},
		{
			name: "object exists on cluster, preserve labels/annotations/ownerrefs on update",
			setup: func() (current, desired, expected *flowcontrolv1.FlowSchema) {
				current, desired = newObject(), newObject()
				current.Labels = map[string]string{"a": "1"}
				current.Annotations = map[string]string{"b": "2"}
				current.OwnerReferences = []metav1.OwnerReference{{Name: "owner-a"}}

				desired.Spec.Rules = []flowcontrolv1.PolicyRulesWithSubjects{
					{
						Subjects: []flowcontrolv1.Subject{{
							Kind:  flowcontrolv1.SubjectKindGroup,
							Group: &flowcontrolv1.GroupSubject{Name: "system:authenticated"},
						}},
					},
				}
				desired.Labels = map[string]string{"c": "3"}
				desired.Annotations = map[string]string{"d": "4"}
				desired.OwnerReferences = []metav1.OwnerReference{{Name: "owner-b"}}

				expected = desired.DeepCopy()
				expected.Labels["a"] = "1"
				expected.Annotations["b"] = "2"
				expected.OwnerReferences = []metav1.OwnerReference{
					{Name: "owner-a"},
					{Name: "owner-b"},
				}
				return current, desired, expected
			},
			diff:       true,
			operations: []string{"get", "update"},
		},
		{
			name: "object exists on cluster, desired object should be immutable",
			setup: func() (current, desired, expected *flowcontrolv1.FlowSchema) {
				current, desired = newObject(), newObject()
				desired.Spec.Rules = []flowcontrolv1.PolicyRulesWithSubjects{
					{
						Subjects: []flowcontrolv1.Subject{{
							Kind:  flowcontrolv1.SubjectKindGroup,
							Group: &flowcontrolv1.GroupSubject{Name: "system:authenticated"},
						}},
					},
				}
				expected = desired.DeepCopy()
				expected.Labels = map[string]string{}
				expected.Annotations = map[string]string{}
				expected.OwnerReferences = []metav1.OwnerReference{}
				return current, desired, expected
			},
			mutator: func(fs *flowcontrolv1.FlowSchema) {
				fs.Spec.Rules[0].Subjects[0].Group.Name = "system:unauthenticated"
			},
			diff:       true,
			operations: []string{"get", "update"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			current, desired, expected := tc.setup()
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
			currentGot, diff, err := ApplyFlowSchema(context.Background(), client.FlowcontrolV1(), recorder, desired)

			if want, got := expected, currentGot; !cmp.Equal(want, got) {
				t.Errorf("expected object to be equal, diff: %s", cmp.Diff(want, got))
			}
			if want, got := tc.diff, diff; want != got {
				t.Errorf("expected modified to be: %t, but got: %t", want, got)
			}
			if want, got := tc.err, err; want != got {
				t.Errorf("expected err to be: %v, but got: %v", want, got)
			}
			if want, got := tc.operations, operationsObserved; !cmp.Equal(want, got) {
				t.Errorf("expected operations to match, diff: %s", cmp.Diff(want, got))
			}

			if tc.mutator != nil {
				t.Logf("mutating the object returned from ApplyFlowSchema")
				tc.mutator(currentGot)
				if want, got := desiredCopy, desired; !cmp.Equal(want, got) {
					t.Errorf("did not expect the object passed to ApplyFlowSchema to be mutated, diff: %s", cmp.Diff(want, got))
				}
			}
		})
	}
}

func TestApplyPriorityLevelConfiguration(t *testing.T) {
	var nominalConcurrencyShares int32 = 2
	var lendablePercent int32 = 90
	newObject := func() *flowcontrolv1.PriorityLevelConfiguration {
		return &flowcontrolv1.PriorityLevelConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-plc",
				Annotations: map[string]string{
					"kueue.openshift.io/allow-nominal-concurrency-shares-update": "false",
				},
			},
			Spec: flowcontrolv1.PriorityLevelConfigurationSpec{
				Type: flowcontrolv1.PriorityLevelEnablementLimited,
				Limited: &flowcontrolv1.LimitedPriorityLevelConfiguration{
					LimitResponse: flowcontrolv1.LimitResponse{
						Type: flowcontrolv1.LimitResponseTypeQueue,
						Queuing: &flowcontrolv1.QueuingConfiguration{
							HandSize:         4,
							QueueLengthLimit: 50,
							Queues:           16,
						},
					},
					NominalConcurrencyShares: &nominalConcurrencyShares,
					LendablePercent:          &lendablePercent,
				},
			},
		}
	}

	tests := []struct {
		name       string
		setup      func() (current, desired, expected *flowcontrolv1.PriorityLevelConfiguration)
		mutator    func(*flowcontrolv1.PriorityLevelConfiguration)
		operations []string
		diff       bool
		err        error
	}{
		{
			name: "object does not exist on cluster, should be created",
			setup: func() (current, desired, expected *flowcontrolv1.PriorityLevelConfiguration) {
				return nil, newObject(), newObject()
			},
			diff:       false,
			operations: []string{"get", "create"},
		},
		{
			name: "object exists on cluster, no change in spec desired, should not call update",
			setup: func() (current, desired, expected *flowcontrolv1.PriorityLevelConfiguration) {
				return newObject(), newObject(), newObject()
			},
			diff:       false,
			operations: []string{"get"},
		},
		{
			name: "object exists on cluster, desired spec changes, should call update",
			setup: func() (current, desired, expected *flowcontrolv1.PriorityLevelConfiguration) {
				current, desired = newObject(), newObject()
				// change the Limited configuration
				var ncs int32 = 42
				desired.Spec.Limited.NominalConcurrencyShares = &ncs
				expected = desired.DeepCopy()
				expected.Labels = map[string]string{}
				expected.Annotations = map[string]string{
					"kueue.openshift.io/allow-nominal-concurrency-shares-update": "false",
				}
				expected.OwnerReferences = []metav1.OwnerReference{}
				return current, desired, expected
			},
			diff:       true,
			operations: []string{"get", "update"},
		},
		{
			name: "desired object is immutable by ApplyPriorityLevelConfiguration",
			setup: func() (current, desired, expected *flowcontrolv1.PriorityLevelConfiguration) {
				current, desired = newObject(), newObject()
				desired.Spec.Limited.LendablePercent = ptr.To(int32(70))
				expected = desired.DeepCopy()
				expected.Labels = map[string]string{}
				expected.Annotations = map[string]string{
					"kueue.openshift.io/allow-nominal-concurrency-shares-update": "false",
				}
				expected.OwnerReferences = []metav1.OwnerReference{}
				return current, desired, expected
			},
			mutator: func(p *flowcontrolv1.PriorityLevelConfiguration) {
				p.Spec.Limited.LendablePercent = ptr.To(int32(80))
			},
			diff:       true,
			operations: []string{"get", "update"},
		},
		{
			name: "object exists on cluster, no desired spec changes, should not call update",
			setup: func() (current, desired, expected *flowcontrolv1.PriorityLevelConfiguration) {
				current, desired = newObject(), newObject()
				current.Annotations = map[string]string{"kueue.openshift.io/allow-nominal-concurrency-shares-update": "true"}
				var ncs int32 = 2
				current.Spec.Limited.NominalConcurrencyShares = &ncs
				desired.Spec.Limited.NominalConcurrencyShares = &ncs
				expected = desired.DeepCopy()
				expected.Annotations = map[string]string{"kueue.openshift.io/allow-nominal-concurrency-shares-update": "true"}
				return current, desired, expected
			},
			diff:       false,
			operations: []string{"get"},
		},
		{
			name: "object exists on cluster, desired spec changes back to default as value is not allowed",
			setup: func() (current, desired, expected *flowcontrolv1.PriorityLevelConfiguration) {
				current, desired = newObject(), newObject()
				current.Annotations = map[string]string{"kueue.openshift.io/allow-nominal-concurrency-shares-update": "true"}
				var ncs1 int32 = 1
				current.Spec.Limited.NominalConcurrencyShares = &ncs1
				expected = desired.DeepCopy()
				expected.Labels = map[string]string{}
				expected.Annotations = map[string]string{"kueue.openshift.io/allow-nominal-concurrency-shares-update": "false"}
				expected.OwnerReferences = []metav1.OwnerReference{}
				return current, desired, expected
			},
			diff:       true,
			operations: []string{"get", "update"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			current, desired, expected := tc.setup()
			desiredCopy := desired.DeepCopy()

			exists := []runtime.Object{}
			if current != nil {
				exists = append(exists, current)
			}
			client := fake.NewSimpleClientset(exists...)
			ops := make([]string, 0)
			client.PrependReactor("*", "*", func(action ktesting.Action) (bool, runtime.Object, error) {
				ops = append(ops, action.GetVerb())
				return false, nil, nil
			})

			recorder := events.NewInMemoryRecorder("test", clocktesting.NewFakePassiveClock(time.Now()))
			currentGot, diff, err := ApplyPriorityLevelConfiguration(context.Background(), client.FlowcontrolV1(), recorder, desired)

			if want, got := expected, currentGot; !cmp.Equal(want, got) {
				t.Errorf("expected object to be equal, diff: %s", cmp.Diff(want, got))
			}
			if want, got := tc.diff, diff; want != got {
				t.Errorf("expected modified to be: %t, but got: %t", want, got)
			}
			if want, got := tc.err, err; want != got {
				t.Errorf("expected err to be: %v, but got: %v", want, got)
			}
			if want, got := tc.operations, ops; !cmp.Equal(want, got) {
				t.Errorf("expected operations to match, diff: %s", cmp.Diff(want, got))
			}

			if tc.mutator != nil {
				t.Logf("mutating the object returned from ApplyPriorityLevelConfiguration")
				tc.mutator(currentGot)
				if want, got := desiredCopy, desired; !cmp.Equal(want, got) {
					t.Errorf("did not expect the object passed to ApplyPriorityLevelConfiguration to be mutated, diff: %s", cmp.Diff(want, got))
				}
			}
		})
	}
}

func TestReadFlowSchemaV1OrDie(t *testing.T) {
	bytes := []byte(`apiVersion: flowcontrol.apiserver.k8s.io/v1
kind: FlowSchema
metadata:
  name: test-fs
spec:
  priorityLevelConfiguration:
    name: catch-all`)
	if obj := ReadFlowSchemaV1OrDie(bytes); obj == nil {
		t.Errorf("expected a valid object of type: %T", &flowcontrolv1.FlowSchema{})
	}
}

func TestReadPriorityLevelConfigurationV1OrDie(t *testing.T) {
	bytes := []byte(`apiVersion: flowcontrol.apiserver.k8s.io/v1
kind: PriorityLevelConfiguration
metadata:
  name: kueue-visibility
spec:
  limited:
    lendablePercent: 90
    limitResponse:
      queuing:
        handSize: 4
        queueLengthLimit: 50
        queues: 16
      type: Queue
    nominalConcurrencyShares: 10
  type: Limited`)
	if obj := ReadPriorityLevelConfigurationV1OrDie(bytes); obj == nil {
		t.Errorf("expected a valid object of type: %T", &flowcontrolv1.PriorityLevelConfiguration{})
	}
}
