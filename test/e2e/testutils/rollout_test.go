/*
Copyright 2026.

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

package testutils

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const testConfigAnnotation = "configmap/kueue-manager-config"

func TestConfigMapDataHash(t *testing.T) {
	t.Parallel()
	// The operator hashes the ConfigMap data with json.Marshal, which sorts
	// the keys and escapes HTML characters, and then takes the SHA-256 sum.
	data := map[string]string{
		"z.yaml":                         "b: 2",
		"controller_manager_config.yaml": "a: <x & y>",
	}
	encoded := `{"controller_manager_config.yaml":"a: \u003cx \u0026 y\u003e","z.yaml":"b: 2"}`
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(encoded)))

	got, err := ConfigMapDataHash(data)
	if err != nil {
		t.Fatalf("ConfigMapDataHash() returned an error: %v", err)
	}
	if got != want {
		t.Errorf("ConfigMapDataHash() = %q, want %q (the hash of %s)", got, want, encoded)
	}

	other, err := ConfigMapDataHash(map[string]string{"controller_manager_config.yaml": "a: 1"})
	if err != nil {
		t.Fatalf("ConfigMapDataHash() returned an error: %v", err)
	}
	if other == got {
		t.Errorf("ConfigMapDataHash() returned the same hash %q for different data", got)
	}
}

func TestExpectDeploymentRolledOut(t *testing.T) {
	t.Parallel()
	previousData := map[string]string{"controller_manager_config.yaml": "frameworks: [batch/job]"}
	requestedData := map[string]string{"controller_manager_config.yaml": "frameworks: [batch/job, disaggregatedset]"}
	previousHash := mustHash(t, previousData)
	requestedHash := mustHash(t, requestedData)
	unrelatedHash := mustHash(t, map[string]string{"controller_manager_config.yaml": "frameworks: [pod]"})

	testCases := map[string]struct {
		configAfter *corev1.ConfigMap
		deployment  *appsv1.Deployment
		wantFailure string
	}{
		"unchanged ConfigMap, Deployment still runs the previous template": {
			// The CR and the ConfigMap already hold the requested configuration.
			configAfter: &corev1.ConfigMap{Data: requestedData},
			deployment:  rolledOutDeployment(previousHash),
			wantFailure: "does not carry the requested manager configuration",
		},
		"changed ConfigMap, Deployment template has an unrelated hash": {
			configAfter: &corev1.ConfigMap{Data: requestedData},
			deployment:  rolledOutDeployment(unrelatedHash),
			wantFailure: "does not carry the requested manager configuration",
		},
		"requested hash, template not observed yet": {
			configAfter: &corev1.ConfigMap{Data: requestedData},
			deployment: withStatus(rolledOutDeployment(requestedHash), func(d *appsv1.Deployment) {
				d.Status.ObservedGeneration = d.Generation - 1
			}),
			wantFailure: "has not observed the latest template",
		},
		"requested hash, rollout in progress": {
			configAfter: &corev1.ConfigMap{Data: requestedData},
			deployment: withStatus(rolledOutDeployment(requestedHash), func(d *appsv1.Deployment) {
				d.Status.UpdatedReplicas = 1
				d.Status.Replicas = 3
			}),
			wantFailure: "not every replica runs the latest template",
		},
		"requested hash, replica not ready": {
			configAfter: &corev1.ConfigMap{Data: requestedData},
			deployment: withStatus(rolledOutDeployment(requestedHash), func(d *appsv1.Deployment) {
				d.Status.ReadyReplicas = 1
				d.Status.AvailableReplicas = 1
			}),
			wantFailure: "not every replica is ready",
		},
		"requested hash, complete rollout": {
			configAfter: &corev1.ConfigMap{Data: requestedData},
			deployment:  rolledOutDeployment(requestedHash),
		},
		"unchanged ConfigMap, complete rollout of the same template": {
			configAfter: &corev1.ConfigMap{Data: previousData},
			deployment:  rolledOutDeployment(previousHash),
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var failures []string
			g := NewGomega(func(message string, _ ...int) {
				failures = append(failures, message)
			})

			wantHash := mustHash(t, tc.configAfter.Data)
			ExpectDeploymentRolledOut(g, tc.deployment, testConfigAnnotation, wantHash)

			if tc.wantFailure == "" {
				if len(failures) != 0 {
					t.Fatalf("ExpectDeploymentRolledOut() failed, want success: %v", failures)
				}
				return
			}
			if len(failures) == 0 {
				t.Fatalf("ExpectDeploymentRolledOut() succeeded, want a failure containing %q", tc.wantFailure)
			}
			if !strings.Contains(failures[0], tc.wantFailure) {
				t.Errorf("ExpectDeploymentRolledOut() first failure = %q, want it to contain %q", failures[0], tc.wantFailure)
			}
		})
	}
}

func mustHash(t *testing.T, data map[string]string) string {
	t.Helper()
	hash, err := ConfigMapDataHash(data)
	if err != nil {
		t.Fatalf("ConfigMapDataHash() returned an error: %v", err)
	}
	return hash
}

// rolledOutDeployment returns a Deployment with two replicas whose Pod template
// carries configHash and whose status reports a complete rollout.
func rolledOutDeployment(configHash string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "kueue-controller-manager", Generation: 4},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](2),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{testConfigAnnotation: configHash},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 4,
			Replicas:           2,
			UpdatedReplicas:    2,
			ReadyReplicas:      2,
			AvailableReplicas:  2,
		},
	}
}

func withStatus(d *appsv1.Deployment, mutate func(*appsv1.Deployment)) *appsv1.Deployment {
	mutate(d)
	return d
}
