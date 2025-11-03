/*
Copyright 2025.

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

package webhook

import (
	"testing"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/utils/ptr"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kueue "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
)

func TestModifyPodBasedValidatingWebhook(t *testing.T) {
	testCases := map[string]struct {
		configuration kueue.KueueConfiguration
		oldWebhook    *admissionregistrationv1.ValidatingWebhookConfiguration
		newWebhook    *admissionregistrationv1.ValidatingWebhookConfiguration
	}{
		"pod integration enabled with selector merge": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{
						kueue.KueueIntegrationPod, kueue.KueueIntegrationDeployment, kueue.KueueIntegrationStatefulSet,
					},
				},
			},
			oldWebhook: &admissionregistrationv1.ValidatingWebhookConfiguration{
				Webhooks: []admissionregistrationv1.ValidatingWebhook{
					{
						Name: "vpod.kb.io",
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"env": "prod"},
						},
					},
					{
						Name: "vdeployment.kb.io",
					},
					{
						Name: "vstatefulset.kb.io",
					},
				},
			},
			newWebhook: &admissionregistrationv1.ValidatingWebhookConfiguration{
				Webhooks: []admissionregistrationv1.ValidatingWebhook{
					{
						Name: "vpod.kb.io",
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"env": "prod"},
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "kueue.openshift.io/managed",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"true"},
								},
							},
						},
						TimeoutSeconds: ptr.To[int32](23),
					},
					{
						Name: "vdeployment.kb.io",
						NamespaceSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "kueue.openshift.io/managed",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"true"},
								},
							},
						},
						TimeoutSeconds: ptr.To[int32](23),
					},
					{
						Name: "vstatefulset.kb.io",
						NamespaceSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "kueue.openshift.io/managed",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"true"},
								},
							},
						},
						TimeoutSeconds: ptr.To[int32](23),
					},
				},
			},
		},
		"non-pod framework with selector": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{
						kueue.KueueIntegrationBatchJob,
					},
				},
			},
			oldWebhook: &admissionregistrationv1.ValidatingWebhookConfiguration{
				Webhooks: []admissionregistrationv1.ValidatingWebhook{
					{
						Name: "vjob.kb.io",
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"team": "ai"},
						},
					},
				},
			},
			newWebhook: &admissionregistrationv1.ValidatingWebhookConfiguration{
				Webhooks: []admissionregistrationv1.ValidatingWebhook{
					{
						Name: "vjob.kb.io",
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"team": "ai"},
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "kueue.openshift.io/managed",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"true"},
								},
							},
						},
						TimeoutSeconds: ptr.To[int32](23),
					},
				},
			},
		},
	}
	for desc, tc := range testCases {
		t.Run(desc, func(t *testing.T) {
			got := ModifyPodBasedValidatingWebhook(tc.configuration, tc.oldWebhook)
			if diff := cmp.Diff(got, tc.newWebhook); len(diff) != 0 {
				t.Errorf("Unexpected buckets (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestModifyPodBasedMutatingWebhook(t *testing.T) {
	testCases := map[string]struct {
		configuration kueue.KueueConfiguration
		oldWebhook    *admissionregistrationv1.MutatingWebhookConfiguration
		newWebhook    *admissionregistrationv1.MutatingWebhookConfiguration
	}{
		"pod integration enabled with selector merge": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{
						kueue.KueueIntegrationPod, kueue.KueueIntegrationStatefulSet,
					},
				},
			},
			oldWebhook: &admissionregistrationv1.MutatingWebhookConfiguration{
				Webhooks: []admissionregistrationv1.MutatingWebhook{
					{
						Name: "mpod.kb.io",
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"env": "staging"},
						},
					},
					{
						Name: "mstatefulset.kb.io",
					},
				},
			},
			newWebhook: &admissionregistrationv1.MutatingWebhookConfiguration{
				Webhooks: []admissionregistrationv1.MutatingWebhook{
					{
						Name: "mpod.kb.io",
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"env": "staging"},
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "kueue.openshift.io/managed",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"true"},
								},
							},
						},
						TimeoutSeconds: ptr.To[int32](23),
					},
					{
						Name: "mstatefulset.kb.io",
						NamespaceSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "kueue.openshift.io/managed",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"true"},
								},
							},
						},
						TimeoutSeconds: ptr.To[int32](23),
					},
				},
			},
		},
		"mixed framework types": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{
					Frameworks: []kueue.KueueIntegration{
						kueue.KueueIntegrationPod, kueue.KueueIntegrationRayJob,
					},
				},
			},
			oldWebhook: &admissionregistrationv1.MutatingWebhookConfiguration{
				Webhooks: []admissionregistrationv1.MutatingWebhook{
					{Name: "mpod.kb.io"},
					{Name: "mrayjob.kb.io"},
				},
			},
			newWebhook: &admissionregistrationv1.MutatingWebhookConfiguration{
				Webhooks: []admissionregistrationv1.MutatingWebhook{
					{
						Name: "mpod.kb.io",
						NamespaceSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "kueue.openshift.io/managed",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"true"},
								},
							},
						},
						TimeoutSeconds: ptr.To[int32](23),
					},
					{
						Name: "mrayjob.kb.io",
						NamespaceSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "kueue.openshift.io/managed",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"true"},
								},
							},
						},
						TimeoutSeconds: ptr.To[int32](23),
					},
				},
			},
		},
	}
	for desc, tc := range testCases {
		t.Run(desc, func(t *testing.T) {
			got := ModifyPodBasedMutatingWebhook(tc.configuration, tc.oldWebhook)
			if diff := cmp.Diff(got, tc.newWebhook); len(diff) != 0 {
				t.Errorf("Unexpected buckets (-want,+got):\n%s", diff)
			}
		})
	}
}
