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

package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("QETesting", Ordered, func() {

	BeforeAll(func() {
		Expect(deployOperand()).To(Succeed(), "operand deployment should not fail")
	})
	AfterAll(func(ctx context.Context) {
		testutils.CleanUpKueuInstance(ctx, clients.KueueClient, "cluster")
	})

	When("QE testing", func() {
		var testNamespace *corev1.Namespace

		BeforeEach(func(ctx context.Context) {
			// Create a test namespace for this test
			testNamespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "qe-test-",
					Labels: map[string]string{
						testutils.OpenShiftManagedLabel: "true",
					},
				},
			}
			var err error
			testNamespace, err = kubeClient.CoreV1().Namespaces().Create(ctx, testNamespace, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func(ctx context.Context) {
			if testNamespace != nil {
				err := kubeClient.CoreV1().Namespaces().Delete(ctx, testNamespace.Name, metav1.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("should label and admit Pod and Job", func(ctx context.Context) {
			By(fmt.Sprintf("Testing in namespace: %s", testNamespace.Name))
			// TODO: Add actual test logic here
			// Example commented code shows what could be implemented:
			// job := builder.NewJobWithoutQueue()
			// createdJob, err := kubeClient.BatchV1().Jobs(testNamespace.Name).Create(ctx, job, metav1.CreateOptions{})
			// Expect(err).NotTo(HaveOccurred())
			// Expect(createdJob.Labels).To(HaveKeyWithValue(testutils.QueueLabel, testutils.DefaultLocalQueueName))
		})
	})

})
