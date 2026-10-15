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
	"fmt"
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
	err     error
)

func TestCustomLabelsCEL(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CustomLabelsCEL envtest suite")
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
	_, err = kueues.Create(context.Background(), kueueObj, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	if testEnv == nil {
		return
	}
	Expect(testEnv.Stop()).To(Succeed())
})

// customLabels builds n custom metric labels with unique names, all sourced from
// the given source kind.
func customLabels(n int, kind kueueopv1.SourceKind) []kueueopv1.ControllerMetricsCustomLabel {
	labels := make([]kueueopv1.ControllerMetricsCustomLabel, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("label_%d", i)
		labels = append(labels, kueueopv1.ControllerMetricsCustomLabel{
			Name:       name,
			SourceKind: kind,
			SourceKey: kueueopv1.SourceKey{
				SourceType: kueueopv1.SourceLabel,
				Label: kueueopv1.SourceKeyLabel{
					Key: name,
				},
			},
		})
	}
	return labels
}

// setCustomLabels fetches the current cluster Kueue and updates it with the given
// custom metric labels, returning the API server error (if any).
func setCustomLabels(ctx context.Context, labels []kueueopv1.ControllerMetricsCustomLabel) error {
	current, getErr := clients.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
	Expect(getErr).NotTo(HaveOccurred())
	current.Spec.Config.ControllerManager = kueueopv1.ControllerManager{
		Metrics: kueueopv1.ControllerMetrics{
			CustomLabels: labels,
		},
	}
	_, updateErr := clients.KueueV1().Kueues().Update(ctx, current, metav1.UpdateOptions{})
	return updateErr
}

var _ = Describe("CustomLabelsCEL", func() {
	It("should allow exactly 6 labels for a single source kind", func(ctx context.Context) {
		By("setting 6 LocalQueue-sourced labels")
		err = setCustomLabels(ctx, customLabels(6, kueueopv1.SourceKindLocalQueue))
		Expect(err).NotTo(HaveOccurred(),
			"want 6 LocalQueue labels to be accepted: %v", err)
	})

	It("should not allow more than 6 labels for the LocalQueue source kind", func(ctx context.Context) {
		By("setting 7 LocalQueue-sourced labels")
		err = setCustomLabels(ctx, customLabels(7, kueueopv1.SourceKindLocalQueue))
		Expect(err).To(HaveOccurred(),
			"want error for 7 LocalQueue labels: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"want invalid for 7 LocalQueue labels: %v", err)
		Expect(err.Error()).To(ContainSubstring("at most 6 labels are allowed for sourceKind LocalQueue"))
	})

	It("should not allow more than 6 labels for the ClusterQueue source kind", func(ctx context.Context) {
		By("setting 7 ClusterQueue-sourced labels")
		err = setCustomLabels(ctx, customLabels(7, kueueopv1.SourceKindClusterQueue))
		Expect(err).To(HaveOccurred(),
			"want error for 7 ClusterQueue labels: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"want invalid for 7 ClusterQueue labels: %v", err)
		Expect(err.Error()).To(ContainSubstring("at most 6 labels are allowed for sourceKind ClusterQueue"))
	})

	It("should not allow label and annotation to be set together", func(ctx context.Context) {
		By("setting a SourceKey with both label and annotation set")
		err = setCustomLabels(ctx, []kueueopv1.ControllerMetricsCustomLabel{
			{
				Name:       "label_0",
				SourceKind: kueueopv1.SourceKindClusterQueue,
				SourceKey: kueueopv1.SourceKey{
					SourceType: kueueopv1.SourceLabel,
					Label:      kueueopv1.SourceKeyLabel{Key: "foo.openshift.io/tenant"},
					Annotation: kueueopv1.SourceKeyAnnotation{Key: "foo.openshift.io/tenant2"},
				},
			},
		})
		Expect(err).To(HaveOccurred(),
			"want error for label with both label and annotation set: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"want invalid for label with both label and annotation set: %v", err)
		Expect(err.Error()).To(
			ContainSubstring("annotation is required when sourceType is Annotation, and forbidden otherwise"),
		)
	})

	It("should not allow label to not be specified when sourceType is label", func(ctx context.Context) {
		By("setting a SourceKey with only sourceType")
		err = setCustomLabels(ctx, []kueueopv1.ControllerMetricsCustomLabel{
			{
				Name:       "label_0",
				SourceKind: kueueopv1.SourceKindClusterQueue,
				SourceKey: kueueopv1.SourceKey{
					SourceType: kueueopv1.SourceLabel,
				},
			},
		})
		Expect(err).To(HaveOccurred(),
			"want error for label with both label and annotation set: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"want invalid for label with both label and annotation set: %v", err)
		Expect(err.Error()).To(ContainSubstring("label is required when sourceType is Label, and forbidden otherwis"))
	})

	It("should not allow labels with the same name", func(ctx context.Context) {
		By("setting a label with the same name")
		err = setCustomLabels(ctx, []kueueopv1.ControllerMetricsCustomLabel{
			{
				Name:       "label_0",
				SourceKind: kueueopv1.SourceKindLocalQueue,
				SourceKey: kueueopv1.SourceKey{
					SourceType: kueueopv1.SourceLabel,
					Label:      kueueopv1.SourceKeyLabel{Key: "foo.openshift.io/tenant"},
				},
			},
			{
				Name:       "label_0",
				SourceKind: kueueopv1.SourceKindCohort,
				SourceKey: kueueopv1.SourceKey{
					SourceType: kueueopv1.SourceAnnotation,
					Annotation: kueueopv1.SourceKeyAnnotation{Key: "foo.openshift.io/tenant"},
				},
			},
		})
		Expect(err).To(HaveOccurred(),
			"want error for label with the same name: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"want invalid for label with the same name: %v", err)
		Expect(err.Error()).To(ContainSubstring("Duplicate value: {\"name\":\"label_0\"}"))
	})
	It("should reject name with wrong format", func(ctx context.Context) {
		By("setting a label with the wrong name")
		err = setCustomLabels(ctx, []kueueopv1.ControllerMetricsCustomLabel{
			{
				Name:       "label-0",
				SourceKind: kueueopv1.SourceKindLocalQueue,
				SourceKey: kueueopv1.SourceKey{
					SourceType: kueueopv1.SourceLabel,
					Label:      kueueopv1.SourceKeyLabel{Key: "foo.openshift.io/tenant"},
				},
			},
		})
		Expect(err).To(HaveOccurred(),
			"want error for wrong name: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"want invalid for wrong name: %v", err)
		Expect(err.Error()).To(
			ContainSubstring("must start with an ASCII letter followed by zero or more ASCII letters, digits, or underscores."))
	})
	It("should reject source label key with wrong format", func(ctx context.Context) {
		By("setting a label with the wrong source label key")
		err = setCustomLabels(ctx, []kueueopv1.ControllerMetricsCustomLabel{
			{
				Name:       "label_0",
				SourceKind: kueueopv1.SourceKindLocalQueue,
				SourceKey: kueueopv1.SourceKey{
					SourceType: kueueopv1.SourceLabel,
					Label:      kueueopv1.SourceKeyLabel{Key: ".label-0.com"},
				},
			},
		})
		Expect(err).To(HaveOccurred(),
			"want error for label with the wrong source label key: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"want invalid for label with the wrong source label key: %v", err)
		Expect(err.Error()).To(ContainSubstring("must be a valid Kubernetes label key (qualified name)"))
	})
	It("should reject source annotation key with wrong format", func(ctx context.Context) {
		By("setting a label with the wrong source annotation key")
		err = setCustomLabels(ctx, []kueueopv1.ControllerMetricsCustomLabel{
			{
				Name:       "label_0",
				SourceKind: kueueopv1.SourceKindLocalQueue,
				SourceKey: kueueopv1.SourceKey{
					SourceType: kueueopv1.SourceAnnotation,
					Annotation: kueueopv1.SourceKeyAnnotation{Key: "@invalid"},
				},
			},
		})
		Expect(err).To(HaveOccurred(),
			"want error for label with the wrong source annotation key: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"want invalid for label with the wrong source annotation key: %v", err)
		Expect(err.Error()).To(ContainSubstring("must be a valid Kubernetes annotation key (qualified name)"))
	})
	It("do not allow empty custom labels", func(ctx context.Context) {
		By("setting a label with the wrong source annotation key")
		err = setCustomLabels(ctx, []kueueopv1.ControllerMetricsCustomLabel{})
		Expect(err).To(HaveOccurred(),
			"want error for no custom labels specified: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"want invalid for no custom labels specified: %v", err)
		Expect(err.Error()).To(
			ContainSubstring("spec.config.controllerManager.metrics in body should have at least 1 properties"),
		)
	})
})
