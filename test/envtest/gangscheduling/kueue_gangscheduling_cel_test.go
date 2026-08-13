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
package envtest

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	operatorv1 "github.com/openshift/api/operator/v1"
	kueueopv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	kueueclient "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const kueueCRDPath = "../../../manifests/kueue.openshift.io_kueues.yaml"

var (
	testEnv *envtest.Environment
	clients *kueueclient.Clientset
	kueue   *kueueopv1.Kueue
	err     error
)

func TestGangSchedulingCEL(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "GangSchedulingCEL envtest suite")
}

var _ = BeforeSuite(func() {
	testEnv = &envtest.Environment{
		CRDInstallOptions: envtest.CRDInstallOptions{
			Paths:              []string{kueueCRDPath},
			ErrorIfPathMissing: true,
		},
		ErrorIfCRDPathMissing:    true,
		DownloadBinaryAssets:     true,
		ControlPlaneStartTimeout: 2 * time.Minute,
		ControlPlaneStopTimeout:  1 * time.Minute,
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	clients, err = kueueclient.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())
	Expect(clients).NotTo(BeNil())

	kueues := clients.KueueV1().Kueues()
	kueueObj := &kueueopv1.Kueue{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: kueueopv1.KueueOperandSpec{
			OperatorSpec: operatorv1.OperatorSpec{ManagementState: operatorv1.Managed},
			Config: kueueopv1.KueueConfiguration{
				Integrations: kueueopv1.Integrations{
					Frameworks: []kueueopv1.KueueIntegration{kueueopv1.KueueIntegrationBatchJob},
				},
			},
		},
	}
	kueue, err = kueues.Create(context.Background(), kueueObj, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	if testEnv == nil {
		return
	}
	Expect(testEnv.Stop()).To(Succeed())
})

var _ = Describe("GangSchedulingCEL", func() {
	It("should not allow updating RequeuingStrategy without at least one field set", func(ctx context.Context) {
		By("setting RequeuingStrategy without at least one field set")
		// NOTE: RequeuingStrategy uses the `omitzero` json tag, so setting it to
		// kueueopv1.RequeuingStrategy{} via the typed client would be silently
		// dropped during marshalling and never reach the API server as an empty
		// object. Use a raw merge patch instead, so `requeuingStrategy: {}` is
		// explicitly present in the payload and the MinProperties=1 rule fires.
		patch := []byte(`{"spec":{"config":{"gangScheduling":{"policy":"ByWorkload",` +
			`"byWorkload":{"admission":"Parallel","requeuingStrategy":{}}}}}}`)
		_, err = clients.KueueV1().Kueues().Patch(ctx, kueue.Name, types.MergePatchType, patch, metav1.PatchOptions{})
		Expect(err).To(HaveOccurred(),
			"want error for RequeuingStrategy without at least one field set: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"want invalid for RequeuingStrategy without at least one field set: %v", err)
		Expect(err.Error()).To(ContainSubstring("should have at least 1 properties"))
	})
	It("should not set backoffBaseSeconds greater than backoffMaxSeconds", func(ctx context.Context) {
		By("setting backoffBaseSeconds greater than backoffMaxSeconds")
		invalid := kueue.DeepCopy()
		invalid.Spec.Config.GangScheduling = kueueopv1.GangScheduling{
			Policy: kueueopv1.GangSchedulingPolicyByWorkload,
			ByWorkload: &kueueopv1.ByWorkload{
				RequeuingStrategy: kueueopv1.RequeuingStrategy{
					BackoffBaseSeconds: 60,
					BackoffMaxSeconds:  30,
				},
			},
		}
		_, err = clients.KueueV1().Kueues().Update(ctx, invalid, metav1.UpdateOptions{})
		Expect(err).To(HaveOccurred(),
			"want error for backoffBaseSeconds greater than backoffMaxSeconds: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"want invalid for backoffBaseSeconds greater than backoffMaxSeconds: %v", err)
		Expect(err.Error()).To(ContainSubstring("backoffBaseSeconds must be less than or equal to backoffMaxSeconds"))
	})
})
