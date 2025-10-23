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

package operator

import (
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/kueue-operator/test/e2e/testutils"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/klog/v2"
	//+kubebuilder:scaffold:imports
)

var _ = Describe("Kueue Operator", func() {
	When("installing", func() {
		BeforeEach(func() {
			//operator-sdk run bundle instead of installing it before the tests
		})

		AfterEach(func() {
			// 	Expect(kubeClient.CoreV1().Namespaces().Delete(context.TODO(), namespace, metav1.DeleteOptions{})).To(Succeed())
		})

		It("operator pods should be ready", Label("day-zero"), func() {
			Eventually(func() error {
				ctx := context.TODO()
				podItems, err := kubeClient.CoreV1().Pods(testutils.OperatorNamespace).List(ctx, metav1.ListOptions{})
				if err != nil {
					klog.Errorf("Unable to list pods: %v", err)
					return nil
				}
				for _, pod := range podItems.Items {
					if !strings.HasPrefix(pod.Name, testutils.OperatorNamespace+"-") {
						continue
					}
					klog.Infof("Checking pod: %v, phase: %v, deletionTS: %v\n", pod.Name, pod.Status.Phase, pod.GetDeletionTimestamp())
					if pod.Status.Phase == corev1.PodRunning && pod.GetDeletionTimestamp() == nil && pod.Status.ContainerStatuses[0].Ready {
						return nil
					}
				}
				return fmt.Errorf("pod is not ready")
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(), "operator pod failed to be ready")
		})

	})

	When("deleting", func() {

	})

	When("reinstalling", func() {

	})

	When("upgrading", func() {

	})
})
