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
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const kueueCRDPath = "../../manifests/kueue.openshift.io_kueues.yaml"

var (
	testEnv *envtest.Environment
	clients *kueueclient.Clientset
	kueue   *kueueopv1.Kueue
	err     error
)

func TestAdmissionFairSharingCEL(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AdmissionFairSharingCEL envtest suite")
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

var _ = Describe("AdmissionFairSharingCEL", func() {
	It("should not allow updating to Custom configuration without custom field", func(ctx context.Context) {
		By("setting only admissionFairSharing.configuration to Custom (no custom object)")
		kueue.Spec.Config.AdmissionFairSharing = kueueopv1.AdmissionFairSharing{
			Configuration: kueueopv1.AdmissionFairSharingConfigurationCustom,
		}
		_, err = clients.KueueV1().Kueues().Update(ctx, kueue, metav1.UpdateOptions{})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "want invalid for Custom without custom field: %v", err)
	})
	It("should not allow resourceWeights name with name wrongly formatted", func(ctx context.Context) {
		By("setting resourceWeights name with name wrongly formatted")
		kueue.Spec.Config.AdmissionFairSharing = kueueopv1.AdmissionFairSharing{
			Configuration: kueueopv1.AdmissionFairSharingConfigurationCustom,
			Custom: kueueopv1.AdmissionFairSharingCustom{
				ResourceWeights: []kueueopv1.ResourceWeight{
					{Name: "/gpu", Weight: "1.0"},
				},
			},
		}
		_, err = clients.KueueV1().Kueues().Update(ctx, kueue, metav1.UpdateOptions{})
		Expect(err).To(HaveOccurred(), "want error for resourceWeights name: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "want invalid for resourceWeights name: %v", err)
	})
	It("should not allow resourceWeights weight not being a number", func(ctx context.Context) {
		By("setting resourceWeights weight not a number")
		kueue.Spec.Config.AdmissionFairSharing = kueueopv1.AdmissionFairSharing{
			Configuration: kueueopv1.AdmissionFairSharingConfigurationCustom,
			Custom: kueueopv1.AdmissionFairSharingCustom{
				ResourceWeights: []kueueopv1.ResourceWeight{
					{Name: "nvidia.com/gpu", Weight: "foo"},
				},
			},
		}
		_, err = clients.KueueV1().Kueues().Update(ctx, kueue, metav1.UpdateOptions{})
		Expect(err).To(HaveOccurred(), "want error for resourceWeights weight not a number: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "want invalid for resourceWeights weight not a number: %v", err)
	})
	It("should not allow resourceWeights with negative weight", func(ctx context.Context) {
		By("setting resourceWeights with negative weight")
		kueue.Spec.Config.AdmissionFairSharing = kueueopv1.AdmissionFairSharing{
			Configuration: kueueopv1.AdmissionFairSharingConfigurationCustom,
			Custom: kueueopv1.AdmissionFairSharingCustom{
				ResourceWeights: []kueueopv1.ResourceWeight{
					{Name: "nvidia.com/gpu", Weight: "-1.0"},
				},
			},
		}
		_, err = clients.KueueV1().Kueues().Update(ctx, kueue, metav1.UpdateOptions{})
		Expect(err).To(HaveOccurred(), "want error for resourceWeights with negative weight: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "want invalid for resourceWeights with negative weight: %v", err)
	})

	It("should allow resourceWeights name with fully qualified name", func(ctx context.Context) {
		By("setting resourceWeights name with fully qualified name")
		kueue.Spec.Config.AdmissionFairSharing = kueueopv1.AdmissionFairSharing{
			Configuration: kueueopv1.AdmissionFairSharingConfigurationCustom,
			Custom: kueueopv1.AdmissionFairSharingCustom{
				UsageHalfLifeTimeSeconds: 10,
				ResourceWeights: []kueueopv1.ResourceWeight{
					{Name: "nvidia.com/gpu", Weight: "1.0"},
				},
			},
		}
		kueue, err = clients.KueueV1().Kueues().Update(ctx, kueue, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())
	})
})
