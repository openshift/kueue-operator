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

	"github.com/google/go-cmp/cmp"

	kueue "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1alpha1"
)

func TestModifyPodBasedValidatingWebhook(t *testing.T) {

	testCases := map[string]struct {
		configuration kueue.KueueConfiguration
		oldWebhook    *admissionregistrationv1.ValidatingWebhookConfiguration
		newWebhook    *admissionregistrationv1.ValidatingWebhookConfiguration
	}{
		"all kinds of pod integration": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationPod, kueue.KueueIntegrationDeployment, kueue.KueueIntegrationStatefulSet}},
			},
			oldWebhook: &admissionregistrationv1.ValidatingWebhookConfiguration{
				Webhooks: []admissionregistrationv1.ValidatingWebhook{
					{
						Name: "vpod.kb.io",
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
					},
					{
						Name: "vdeployment.kb.io",
					},
					{
						Name: "vstatefulset.kb.io",
					},
				},
			},
		},
		"job integration; drop all pod integrations": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationBatchJob}},
			},
			oldWebhook: &admissionregistrationv1.ValidatingWebhookConfiguration{
				Webhooks: []admissionregistrationv1.ValidatingWebhook{
					{
						Name: "vpod.kb.io",
					},
					{
						Name: "vjob.kb.io",
					},
					{
						Name: "vstatefulset.kb.io",
					},
					{
						Name: "vdeployment.kb.io",
					},
				},
			},
			newWebhook: &admissionregistrationv1.ValidatingWebhookConfiguration{
				Webhooks: []admissionregistrationv1.ValidatingWebhook{
					{
						Name: "vjob.kb.io",
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
		"all kinds of pod integration": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationPod, kueue.KueueIntegrationDeployment, kueue.KueueIntegrationStatefulSet}},
			},
			oldWebhook: &admissionregistrationv1.MutatingWebhookConfiguration{
				Webhooks: []admissionregistrationv1.MutatingWebhook{
					{
						Name: "mpod.kb.io",
					},
					{
						Name: "mdeployment.kb.io",
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
					},
					{
						Name: "mdeployment.kb.io",
					},
					{
						Name: "mstatefulset.kb.io",
					},
				},
			},
		},
		"job integration; drop all pod integration webhook": {
			configuration: kueue.KueueConfiguration{
				Integrations: kueue.Integrations{Frameworks: []kueue.KueueIntegration{kueue.KueueIntegrationBatchJob}},
			},
			oldWebhook: &admissionregistrationv1.MutatingWebhookConfiguration{
				Webhooks: []admissionregistrationv1.MutatingWebhook{
					{
						Name: "mpod.kb.io",
					},
					{
						Name: "mdeployment.kb.io",
					},
					{
						Name: "mstatefulset.kb.io",
					},
					{
						Name: "mjob.kb.io",
					},
				},
			},
			newWebhook: &admissionregistrationv1.MutatingWebhookConfiguration{
				Webhooks: []admissionregistrationv1.MutatingWebhook{
					{
						Name: "mjob.kb.io",
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
