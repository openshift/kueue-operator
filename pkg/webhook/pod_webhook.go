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

	kueue "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1alpha1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	frameworkWebhooks = map[string]string{
		"pod":                           "pod",
		"batch/job":                     "job",
		"kubeflow.org/mpijob":           "mpijob",
		"ray.io/rayjob":                 "rayjob",
		"ray.io/raycluster":             "raycluster",
		"jobset.x-k8s.io/jobset":        "jobset",
		"kubeflow.org/mxjob":            "mxjob",
		"kubeflow.org/paddlejob":        "paddlejob",
		"kubeflow.org/pytorchjob":       "pytorchjob",
		"kubeflow.org/tfjob":            "tfjob",
		"kubeflow.org/xgboostjob":       "xgboostjob",
		"deployment":                    "deployment",
		"statefulset":                   "statefulset",
		"kueue.x-k8s.io/clusterqueue":   "clusterqueue",
		"kueue.x-k8s.io/workload":       "workload",
		"kueue.x-k8s.io/resourceflavor": "resourceflavor",
		"kueue.x-k8s.io/cohort":         "cohort",
	}
)

func ModifyPodBasedValidatingWebhook(kueueCfg kueue.KueueConfiguration, currentWebhook *admissionregistrationv1.ValidatingWebhookConfiguration) *admissionregistrationv1.ValidatingWebhookConfiguration {
	newWebhook := currentWebhook.DeepCopy()
	newWebhook.Webhooks = []admissionregistrationv1.ValidatingWebhook{}

	enabledFrameworks := make(map[string]bool)
	podIntegrationEnabled := kueueCfg.Integrations.PodOptions != nil

	// Get pod selector safely
	var podSelector *metav1.LabelSelector
	if podIntegrationEnabled {
		podSelector = kueueCfg.Integrations.PodOptions.NamespaceSelector
	}

	for _, fw := range kueueCfg.Integrations.Frameworks {
		if isPodBased(fw) && !podIntegrationEnabled {
			continue
		}
		enabledFrameworks[fw] = true
	}

	for _, wh := range currentWebhook.Webhooks {
		framework := getFrameworkForWebhook(wh.Name, "v")
		if strings.HasPrefix(framework, "kueue.x-k8s.io/") {
			// Always include core Kueue objects.
			newWebhook.Webhooks = append(newWebhook.Webhooks, wh)
		} else if isPodBased(framework) {
			if podIntegrationEnabled && enabledFrameworks[framework] {
				newWebhook.Webhooks = append(newWebhook.Webhooks, wh)
			}
		} else if enabledFrameworks[framework] {
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
	podIntegrationEnabled := kueueCfg.Integrations.PodOptions != nil

	// Get pod selector safely
	var podSelector *metav1.LabelSelector
	if podIntegrationEnabled {
		podSelector = kueueCfg.Integrations.PodOptions.NamespaceSelector
	}

	for _, fw := range kueueCfg.Integrations.Frameworks {
		if isPodBased(fw) && !podIntegrationEnabled {
			continue
		}
		enabledFrameworks[fw] = true
	}

	for _, wh := range currentWebhook.Webhooks {
		framework := getFrameworkForWebhook(wh.Name, "m")
		if strings.HasPrefix(framework, "kueue.x-k8s.io/") {
			// Always include core Kueue objects.
			newWebhook.Webhooks = append(newWebhook.Webhooks, wh)
		} else if isPodBased(framework) {
			if podIntegrationEnabled && enabledFrameworks[framework] {
				newWebhook.Webhooks = append(newWebhook.Webhooks, wh)
			}
		} else if enabledFrameworks[framework] {
			newWebhook.Webhooks = append(newWebhook.Webhooks, wh)
		}
	}

	mergeNamespaceSelectors(newWebhook, podSelector)
	return newWebhook
}

func isPodBased(framework string) bool {
	switch framework {
	case "pod", "deployment", "statefulset":
		return true
	}
	return false
}

func getFrameworkForWebhook(name, prefix string) string {
	suffix := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".kb.io")

	for framework, s := range frameworkWebhooks {
		if s == suffix {
			return framework
		}
	}
	panic(fmt.Sprintf("unregistered framework webhook: %s", name))
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
//			  - kueue-system
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
//			  - kueue-system
func mergeNamespaceSelectors(webhook interface{}, podSelector *metav1.LabelSelector) {
	switch wh := webhook.(type) {
	case *admissionregistrationv1.ValidatingWebhookConfiguration:
		for i := range wh.Webhooks {
			wh.Webhooks[i].NamespaceSelector = mergeSelectors(
				podSelector,
				wh.Webhooks[i].NamespaceSelector,
			)
		}
	case *admissionregistrationv1.MutatingWebhookConfiguration:
		for i := range wh.Webhooks {
			wh.Webhooks[i].NamespaceSelector = mergeSelectors(
				podSelector,
				wh.Webhooks[i].NamespaceSelector,
			)
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
