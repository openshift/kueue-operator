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
package partitionabledevices

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
	err     error
)

func TestPartitionableDevicesCEL(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PartitionableDevicesCEL envtest suite")
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
	kueue, err = clients.KueueV1().Kueues().Create(context.Background(), kueueObj, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	if testEnv == nil {
		return
	}
	Expect(testEnv.Stop()).To(Succeed())
})

func validSources() []kueueopv1.DeviceClassSourceConfig {
	return []kueueopv1.DeviceClassSourceConfig{
		{
			Type: kueueopv1.DeviceClassSourceTypeCounter,
			Counter: kueueopv1.DeviceClassCounterSource{
				Name:   "memory",
				Driver: "gpu.nvidia.com",
				DeviceSelector: kueueopv1.DeviceSelector{
					Type: kueueopv1.DeviceSelectorTypeCEL,
					CEL:  kueueopv1.CELDeviceSelector{Expression: "device.driver == 'gpu.nvidia.com'"},
				},
			},
		},
	}
}

var _ = Describe("PartitionableDevicesCEL", func() {
	It("should allow valid sources with counter", func(ctx context.Context) {
		kueue.Spec.Config.Resources = kueueopv1.Resources{
			DeviceClassMappings: []kueueopv1.DeviceClassMapping{
				{
					Name:             "gpu.memory",
					DeviceClassNames: []kueueopv1.DeviceClassName{"gpu.nvidia.com"},
					Sources:          validSources(),
				},
			},
		}
		kueue, err = clients.KueueV1().Kueues().Update(ctx, kueue, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())
	})

	It("should allow deviceClassMappings without sources", func(ctx context.Context) {
		kueue.Spec.Config.Resources = kueueopv1.Resources{
			DeviceClassMappings: []kueueopv1.DeviceClassMapping{
				{
					Name:             "gpu.memory",
					DeviceClassNames: []kueueopv1.DeviceClassName{"gpu.nvidia.com"},
				},
			},
		}
		kueue, err = clients.KueueV1().Kueues().Update(ctx, kueue, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())
	})

	It("should reject counter name with uppercase", func(ctx context.Context) {
		kueue.Spec.Config.Resources = kueueopv1.Resources{
			DeviceClassMappings: []kueueopv1.DeviceClassMapping{
				{
					Name:             "gpu.memory",
					DeviceClassNames: []kueueopv1.DeviceClassName{"gpu.nvidia.com"},
					Sources: []kueueopv1.DeviceClassSourceConfig{
						{
							Type: kueueopv1.DeviceClassSourceTypeCounter,
							Counter: kueueopv1.DeviceClassCounterSource{
								Name:   "MEMORY",
								Driver: "gpu.nvidia.com",
								DeviceSelector: kueueopv1.DeviceSelector{
									Type: kueueopv1.DeviceSelectorTypeCEL,
									CEL:  kueueopv1.CELDeviceSelector{Expression: "device.driver == 'gpu.nvidia.com'"},
								},
							},
						},
					},
				},
			},
		}
		_, err = clients.KueueV1().Kueues().Update(ctx, kueue, metav1.UpdateOptions{})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})

	It("should reject counter name with dots", func(ctx context.Context) {
		kueue.Spec.Config.Resources = kueueopv1.Resources{
			DeviceClassMappings: []kueueopv1.DeviceClassMapping{
				{
					Name:             "gpu.memory",
					DeviceClassNames: []kueueopv1.DeviceClassName{"gpu.nvidia.com"},
					Sources: []kueueopv1.DeviceClassSourceConfig{
						{
							Type: kueueopv1.DeviceClassSourceTypeCounter,
							Counter: kueueopv1.DeviceClassCounterSource{
								Name:   "gpu.memory",
								Driver: "gpu.nvidia.com",
								DeviceSelector: kueueopv1.DeviceSelector{
									Type: kueueopv1.DeviceSelectorTypeCEL,
									CEL:  kueueopv1.CELDeviceSelector{Expression: "device.driver == 'gpu.nvidia.com'"},
								},
							},
						},
					},
				},
			},
		}
		_, err = clients.KueueV1().Kueues().Update(ctx, kueue, metav1.UpdateOptions{})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})

	It("should reject driver with uppercase", func(ctx context.Context) {
		kueue.Spec.Config.Resources = kueueopv1.Resources{
			DeviceClassMappings: []kueueopv1.DeviceClassMapping{
				{
					Name:             "gpu.memory",
					DeviceClassNames: []kueueopv1.DeviceClassName{"gpu.nvidia.com"},
					Sources: []kueueopv1.DeviceClassSourceConfig{
						{
							Type: kueueopv1.DeviceClassSourceTypeCounter,
							Counter: kueueopv1.DeviceClassCounterSource{
								Name:   "memory",
								Driver: "GPU.NVIDIA.COM",
								DeviceSelector: kueueopv1.DeviceSelector{
									Type: kueueopv1.DeviceSelectorTypeCEL,
									CEL:  kueueopv1.CELDeviceSelector{Expression: "device.driver == 'gpu.nvidia.com'"},
								},
							},
						},
					},
				},
			},
		}
		_, err = clients.KueueV1().Kueues().Update(ctx, kueue, metav1.UpdateOptions{})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})

	It("should reject invalid source type", func(ctx context.Context) {
		kueue.Spec.Config.Resources = kueueopv1.Resources{
			DeviceClassMappings: []kueueopv1.DeviceClassMapping{
				{
					Name:             "gpu.memory",
					DeviceClassNames: []kueueopv1.DeviceClassName{"gpu.nvidia.com"},
					Sources: []kueueopv1.DeviceClassSourceConfig{
						{Type: "Invalid"},
					},
				},
			},
		}
		_, err = clients.KueueV1().Kueues().Update(ctx, kueue, metav1.UpdateOptions{})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})

	It("should reject type=Counter without counter field", func(ctx context.Context) {
		kueue.Spec.Config.Resources = kueueopv1.Resources{
			DeviceClassMappings: []kueueopv1.DeviceClassMapping{
				{
					Name:             "gpu.memory",
					DeviceClassNames: []kueueopv1.DeviceClassName{"gpu.nvidia.com"},
					Sources: []kueueopv1.DeviceClassSourceConfig{
						{Type: kueueopv1.DeviceClassSourceTypeCounter},
					},
				},
			},
		}
		_, err = clients.KueueV1().Kueues().Update(ctx, kueue, metav1.UpdateOptions{})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})

	It("should reject invalid DeviceSelector type", func(ctx context.Context) {
		kueue.Spec.Config.Resources = kueueopv1.Resources{
			DeviceClassMappings: []kueueopv1.DeviceClassMapping{
				{
					Name:             "gpu.memory",
					DeviceClassNames: []kueueopv1.DeviceClassName{"gpu.nvidia.com"},
					Sources: []kueueopv1.DeviceClassSourceConfig{
						{
							Type: kueueopv1.DeviceClassSourceTypeCounter,
							Counter: kueueopv1.DeviceClassCounterSource{
								Name:           "memory",
								Driver:         "gpu.nvidia.com",
								DeviceSelector: kueueopv1.DeviceSelector{Type: "Invalid"},
							},
						},
					},
				},
			},
		}
		_, err = clients.KueueV1().Kueues().Update(ctx, kueue, metav1.UpdateOptions{})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})

	It("should reject empty CEL expression", func(ctx context.Context) {
		kueue.Spec.Config.Resources = kueueopv1.Resources{
			DeviceClassMappings: []kueueopv1.DeviceClassMapping{
				{
					Name:             "gpu.memory",
					DeviceClassNames: []kueueopv1.DeviceClassName{"gpu.nvidia.com"},
					Sources: []kueueopv1.DeviceClassSourceConfig{
						{
							Type: kueueopv1.DeviceClassSourceTypeCounter,
							Counter: kueueopv1.DeviceClassCounterSource{
								Name:   "memory",
								Driver: "gpu.nvidia.com",
								DeviceSelector: kueueopv1.DeviceSelector{
									Type: kueueopv1.DeviceSelectorTypeCEL,
									CEL:  kueueopv1.CELDeviceSelector{Expression: ""},
								},
							},
						},
					},
				},
			},
		}
		_, err = clients.KueueV1().Kueues().Update(ctx, kueue, metav1.UpdateOptions{})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})

	It("should reject more than 1 source", func(ctx context.Context) {
		kueue.Spec.Config.Resources = kueueopv1.Resources{
			DeviceClassMappings: []kueueopv1.DeviceClassMapping{
				{
					Name:             "gpu.memory",
					DeviceClassNames: []kueueopv1.DeviceClassName{"gpu.nvidia.com"},
					Sources: append(validSources(), kueueopv1.DeviceClassSourceConfig{
						Type: kueueopv1.DeviceClassSourceTypeCounter,
						Counter: kueueopv1.DeviceClassCounterSource{
							Name:   "compute",
							Driver: "gpu.nvidia.com",
							DeviceSelector: kueueopv1.DeviceSelector{
								Type: kueueopv1.DeviceSelectorTypeCEL,
								CEL:  kueueopv1.CELDeviceSelector{Expression: "device.driver == 'gpu.nvidia.com'"},
							},
						},
					}),
				},
			},
		}
		_, err = clients.KueueV1().Kueues().Update(ctx, kueue, metav1.UpdateOptions{})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
	})
})
