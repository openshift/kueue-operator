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
	"fmt"
	"strings"

	kueue "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// kueueManagedLabel is the label used to opt-in resources for kueue.
	kueueManagedLabel        = "kueue.openshift.io/managed"
	annotationWorkload       = "kueue.x-k8s.io/workload"
	annotationClusterQueue   = "kueue.x-k8s.io/clusterqueue"
	annotationResourceFlavor = "kueue.x-k8s.io/resourceflavor"
	annotationCohort         = "kueue.x-k8s.io/cohort"
)

var (
	// webhooksList maps the webhook name to the framework/resource name.
	// for e.g. "BatchJob" -> (vjob.kb.io -> job)
	webhooksList = map[string]string{
		string(kueue.KueueIntegrationBatchJob):        "job",
		string(kueue.KueueIntegrationMPIJob):          "mpijob",
		string(kueue.KueueIntegrationRayJob):          "rayjob",
		string(kueue.KueueIntegrationRayCluster):      "raycluster",
		string(kueue.KueueIntegrationJobSet):          "jobset",
		string(kueue.KueueIntegrationPaddleJob):       "paddlejob",
		string(kueue.KueueIntegrationPyTorchJob):      "pytorchjob",
		string(kueue.KueueIntegrationTFJob):           "tfjob",
		string(kueue.KueueIntegrationXGBoostJob):      "xgboostjob",
		string(kueue.KueueIntegrationJaxJob):          "jaxjob",
		string(kueue.KueueIntegrationPod):             "pod",
		string(kueue.KueueIntegrationDeployment):      "deployment",
		string(kueue.KueueIntegrationStatefulSet):     "statefulset",
		string(kueue.KueueIntegrationLeaderWorkerSet): "leaderworkerset",
		annotationClusterQueue:                        "clusterqueue",
		annotationWorkload:                            "workload",
		annotationResourceFlavor:                      "resourceflavor",
		annotationCohort:                              "cohort",
		string(kueue.KueueIntegrationAppWrapper):      "appwrapper",
	}
)

func ModifyPodBasedValidatingWebhook(kueueCfg kueue.KueueConfiguration, currentWebhook *admissionregistrationv1.ValidatingWebhookConfiguration) *admissionregistrationv1.ValidatingWebhookConfiguration {
	newWebhook := currentWebhook.DeepCopy()
	newWebhook.Webhooks = []admissionregistrationv1.ValidatingWebhook{}

	enabledFrameworks := make(map[string]bool)
	podSelector := defaultLabelSelector()

	for _, fw := range kueueCfg.Integrations.Frameworks {
		enabledFrameworks[string(fw)] = true
	}

	for _, wh := range currentWebhook.Webhooks {
		framework := getFrameworkForValidatingWebhook(wh.Name)
		if enabledFrameworks[framework] {
			newWebhook.Webhooks = append(newWebhook.Webhooks, wh)
		}
	}

	mergeNamespaceSelectors(newWebhook, podSelector)
	return newWebhook
}

func ModifyPodBasedMutatingWebhook(kueueCfg kueue.KueueConfiguration, currentWebhook *admissionregistrationv1.MutatingWebhookConfiguration) *admissionregistrationv1.MutatingWebhookConfiguration {
	newWebhook := currentWebhook.DeepCopy()
	newWebhook.Webhooks = []admissionregistrationv1.MutatingWebhook{}

	enabledFrameworks := make(map[string]bool)
	podSelector := defaultLabelSelector()

	for _, fw := range kueueCfg.Integrations.Frameworks {
		enabledFrameworks[string(fw)] = true
	}

	for _, wh := range currentWebhook.Webhooks {
		framework := getFrameworkForMutatingWebhook(wh.Name)
		if enabledFrameworks[framework] {
			newWebhook.Webhooks = append(newWebhook.Webhooks, wh)
		}
	}

	mergeNamespaceSelectors(newWebhook, podSelector)
	return newWebhook
}

func isClusterScopedFramework(framework string) bool {
	switch framework {
	case annotationClusterQueue, annotationResourceFlavor, annotationCohort:
		return true
	}
	return false
}

func getFrameworkForValidatingWebhook(name string) string {
	suffix := strings.TrimSuffix(strings.TrimPrefix(name, "v"), ".kb.io")

	for framework, s := range webhooksList {
		if s == suffix {
			return framework
		}
	}
	panic(fmt.Sprintf("unregistered framework: %s", name))
}

func getFrameworkForMutatingWebhook(name string) string {
	suffix := strings.TrimSuffix(strings.TrimPrefix(name, "m"), ".kb.io")

	for framework, s := range webhooksList {
		if s == suffix {
			return framework
		}
	}
	panic(fmt.Sprintf("unregistered framework: %s", name))
}

// mergeNamespaceSelectors applies merged namespace selectors to all webhooks in the configuration.
// Example:
//
//		Existing webhook selector:
//		namespaceSelector:
//		  matchExpressions:
//	   		- key: kubernetes.io/metadata.name
//			  operator: NotIn
//			  values:
//			  - kube-system
//			  - openshift-kueue-operator
//
//		Pod integration selector:
//		  matchExpressions:
//		    - key: kueue.openshift.io/managed
//		      operator: In
//		      values: [true]
//
//		Merged selector:
//		  matchLabels:
//		    environment: prod
//		  matchExpressions:
//		    - key: kueue.openshift.io/managed
//		      operator: In
//		      values: [true]
//		    - key: kubernetes.io/metadata.name
//		      operator: NotIn
//			  values:
//			  - kube-system
//			  - openshift-kueue-operator
func mergeNamespaceSelectors(webhook interface{}, podSelector *metav1.LabelSelector) {
	switch wh := webhook.(type) {
	case *admissionregistrationv1.ValidatingWebhookConfiguration:
		for i := range wh.Webhooks {
			framework := getFrameworkForValidatingWebhook(wh.Webhooks[i].Name)
			if !isClusterScopedFramework(framework) {
				wh.Webhooks[i].NamespaceSelector = mergeSelectors(
					podSelector,
					wh.Webhooks[i].NamespaceSelector,
				)
			}
		}
	case *admissionregistrationv1.MutatingWebhookConfiguration:
		for i := range wh.Webhooks {
			framework := getFrameworkForMutatingWebhook(wh.Webhooks[i].Name)
			if !isClusterScopedFramework(framework) {
				wh.Webhooks[i].NamespaceSelector = mergeSelectors(
					podSelector,
					wh.Webhooks[i].NamespaceSelector,
				)
			}
		}
	}
}

// mergeSelectors combines two label selectors while preserving the required
// "kueue.openshift.io/managed" opt-in label.
// CRD-provided selector takes precedence over existing selector values.
func mergeSelectors(crSelector, existingSelector *metav1.LabelSelector) *metav1.LabelSelector {
	if crSelector == nil {
		return existingSelector
	}
	if existingSelector == nil {
		return crSelector
	}

	merged := &metav1.LabelSelector{
		MatchLabels: make(map[string]string),
	}

	// Merge labels with CRD taking precedence
	for k, v := range existingSelector.MatchLabels {
		merged.MatchLabels[k] = v
	}
	for k, v := range crSelector.MatchLabels {
		merged.MatchLabels[k] = v
	}

	// Merge expressions with deduplication
	seenKeys := make(map[string]bool)
	mergedExpressions := make([]metav1.LabelSelectorRequirement, 0, len(crSelector.MatchExpressions)+len(existingSelector.MatchExpressions))

	for _, expr := range crSelector.MatchExpressions {
		if !seenKeys[expr.Key] {
			mergedExpressions = append(mergedExpressions, expr)
			seenKeys[expr.Key] = true
		}
	}

	for _, expr := range existingSelector.MatchExpressions {
		if !seenKeys[expr.Key] {
			mergedExpressions = append(mergedExpressions, expr)
			seenKeys[expr.Key] = true
		}
	}

	merged.MatchExpressions = mergedExpressions
	return merged
}

func defaultLabelSelector() *metav1.LabelSelector {
	return &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      kueueManagedLabel,
				Operator: metav1.LabelSelectorOpIn,
				Values:   []string{"true"},
			},
		},
	}
}
