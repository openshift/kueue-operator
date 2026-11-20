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
	"encoding/json"
	"fmt"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/utils/ptr"
)

// ConfigMapDataHash returns the hash that the operator records for the data of
// a ConfigMap in the operand Deployment's Pod template annotations: the
// hex-encoded SHA-256 sum of the JSON encoding of the data.
func ConfigMapDataHash(data map[string]string) (string, error) {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("failed to encode the ConfigMap data: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(jsonBytes)), nil
}

// ExpectDeploymentRolledOut asserts that the Deployment's Pod template carries
// wantHash in the given annotation, and that every replica runs that template
// and is ready and available.
func ExpectDeploymentRolledOut(g Gomega, d *appsv1.Deployment, annotation, wantHash string) {
	g.Expect(d.Spec.Template.Annotations[annotation]).To(Equal(wantHash),
		"the Deployment template does not carry the requested manager configuration yet")
	replicas := ptr.Deref(d.Spec.Replicas, 1)
	g.Expect(d.Status.ObservedGeneration).To(BeNumerically(">=", d.Generation), "the Deployment controller has not observed the latest template")
	g.Expect(d.Status.UpdatedReplicas).To(Equal(replicas), "not every replica runs the latest template")
	g.Expect(d.Status.ReadyReplicas).To(Equal(replicas), "not every replica is ready")
	g.Expect(d.Status.AvailableReplicas).To(Equal(replicas), "not every replica is available")
	g.Expect(d.Status.Replicas).To(Equal(replicas), "replicas of the previous template are still running")
}
