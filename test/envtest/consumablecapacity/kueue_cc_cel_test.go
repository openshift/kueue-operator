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
package consumablecapacity

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

const kueueCRDPath = "../../../manifests/kueue.openshift.io_kueues.yaml"

var (
	testEnv *envtest.Environment
	clients *kueueclient.Clientset
	kueue   *kueueopv1.Kueue
)

func TestConsumableCapacityCEL(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ConsumableCapacityCEL envtest suite")
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	kueue, err = clients.KueueV1().Kueues().Create(ctx, kueueObj, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	if testEnv == nil {
		return
	}
	Expect(testEnv.Stop()).To(Succeed())
})

func validCapacitySources() []kueueopv1.DeviceClassSourceConfig {
	return []kueueopv1.DeviceClassSourceConfig{
		{
			Type: kueueopv1.DeviceClassSourceTypeCapacity,
			Capacity: kueueopv1.DeviceClassCapacitySource{
				Name:   "memory",
				Driver: "gpu.example.com",
				DeviceSelector: kueueopv1.DeviceSelector{
					Type: kueueopv1.DeviceSelectorTypeCEL,
					CEL:  kueueopv1.CELDeviceSelector{Expression: "device.driver == 'gpu.example.com'"},
				},
			},
		},
	}
}

func updateWithSources(ctx context.Context, sources []kueueopv1.DeviceClassSourceConfig) error {
	latest, err := clients.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	latest.Spec.Config.Resources = kueueopv1.Resources{
		DeviceClassMappings: []kueueopv1.DeviceClassMapping{
			{
				Name:             "gpu.memory",
				DeviceClassNames: []kueueopv1.DeviceClassName{"gpu.example.com"},
				Sources:          sources,
			},
		},
	}
	updated, err := clients.KueueV1().Kueues().Update(ctx, latest, metav1.UpdateOptions{})
	if err == nil {
		kueue = updated
	}
	return err
}

var _ = Describe("ConsumableCapacityCEL", func() {
	It("should allow valid sources with capacity", func(ctx context.Context) {
		Expect(updateWithSources(ctx, validCapacitySources())).To(Succeed())
	})

	It("should allow qualified capacity name", func(ctx context.Context) {
		sources := validCapacitySources()
		sources[0].Capacity.Name = "gpu.example.com/memory"
		Expect(updateWithSources(ctx, sources)).To(Succeed())
	})

	It("should allow deviceClassMappings without sources", func(ctx context.Context) {
		Expect(updateWithSources(ctx, nil)).To(Succeed())
	})

	It("should reject invalid source type", func(ctx context.Context) {
		err := updateWithSources(ctx, []kueueopv1.DeviceClassSourceConfig{{Type: "Invalid"}})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})

	It("should reject type=Capacity without capacity field", func(ctx context.Context) {
		err := updateWithSources(ctx, []kueueopv1.DeviceClassSourceConfig{{Type: kueueopv1.DeviceClassSourceTypeCapacity}})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})

	It("should reject type=Capacity with counter field", func(ctx context.Context) {
		sources := validCapacitySources()
		sources[0].Counter = kueueopv1.DeviceClassCounterSource{Name: "memory"}
		err := updateWithSources(ctx, sources)
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})

	It("should reject type=Counter with capacity field", func(ctx context.Context) {
		sources := validCapacitySources()
		sources[0].Type = kueueopv1.DeviceClassSourceTypeCounter
		sources[0].Counter = kueueopv1.DeviceClassCounterSource{Name: "memory"}
		err := updateWithSources(ctx, sources)
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})

	It("should reject empty capacity name", func(ctx context.Context) {
		sources := validCapacitySources()
		sources[0].Capacity.Name = ""
		err := updateWithSources(ctx, sources)
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})

	It("should reject invalid capacity name", func(ctx context.Context) {
		sources := validCapacitySources()
		sources[0].Capacity.Name = "gpu.example.com/gpu-memory"
		err := updateWithSources(ctx, sources)
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})

	It("should reject invalid capacity driver", func(ctx context.Context) {
		sources := validCapacitySources()
		sources[0].Capacity.Driver = "GPU.EXAMPLE.COM"
		err := updateWithSources(ctx, sources)
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})

	It("should reject invalid DeviceSelector type", func(ctx context.Context) {
		sources := validCapacitySources()
		sources[0].Capacity.DeviceSelector = kueueopv1.DeviceSelector{Type: "Invalid"}
		err := updateWithSources(ctx, sources)
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})

	It("should reject empty CEL expression", func(ctx context.Context) {
		sources := validCapacitySources()
		sources[0].Capacity.DeviceSelector.CEL.Expression = ""
		err := updateWithSources(ctx, sources)
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})

	It("should reject more than 1 source", func(ctx context.Context) {
		err := updateWithSources(ctx, append(validCapacitySources(), validCapacitySources()...))
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})
})
