/*
Copyright 2024.

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

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/kueue/apis/config/v1beta1"

	cachev1 "github.com/kannon92/kueue-operator/api/v1"
)

var _ = Describe("KueueOperator Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"
		var ns *corev1.Namespace

		ctx := context.Background()

		BeforeEach(func() {
			ns = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "kueue-",
				},
			}
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		})

		// AfterEach(func() {
		// 	Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
		// })
		BeforeEach(func() {
			By("creating the custom resource for the Kind KueueOperator")
			resource := &cachev1.KueueOperator{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: ns.Name,
					Name:      "kueue",
				},
				Spec: cachev1.KueueOperatorSpec{
					Kueue: &cachev1.Kueue{
						Config: cachev1.KueueConfiguration{
							Integrations: v1beta1.Integrations{
								Frameworks: []string{"batchv1.job"},
							},
						},
						Image: "registry.k8s.io/kueue/kueue:v0.10.0",
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
		})

		// AfterEach(func() {
		// 	typeNamespacedName := types.NamespacedName{
		// 		Name:      resourceName,
		// 		Namespace: ns.Name,
		// 	}
		// 	// TODO(user): Cleanup logic after each test, like removing the resource instance.
		// 	resource := &cachev1.KueueOperator{}
		// 	err := k8sClient.Get(ctx, typeNamespacedName, resource)
		// 	Expect(err).NotTo(HaveOccurred())

		// 	By("Cleanup the specific resource instance KueueOperator")
		// 	Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		// })
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &KueueOperatorReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			typeNamespacedName := types.NamespacedName{
				Name:      resourceName,
				Namespace: ns.Name,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})
