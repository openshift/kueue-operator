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

const kueueCRDPath = "../../../manifests/kueue.openshift.io_kueues.yaml"

var (
	testEnv *envtest.Environment
	clients *kueueclient.Clientset
	err     error
)

// initialFrameworks are the frameworks of the cluster Kueue object before each spec.
var initialFrameworks = []kueueopv1.KueueIntegration{kueueopv1.KueueIntegrationBatchJob}

// allFrameworks lists every value of the frameworks enum.
var allFrameworks = []kueueopv1.KueueIntegration{
	kueueopv1.KueueIntegrationBatchJob,
	kueueopv1.KueueIntegrationRayJob,
	kueueopv1.KueueIntegrationRayCluster,
	kueueopv1.KueueIntegrationRayService,
	kueueopv1.KueueIntegrationJobSet,
	kueueopv1.KueueIntegrationMPIJob,
	kueueopv1.KueueIntegrationPaddleJob,
	kueueopv1.KueueIntegrationPyTorchJob,
	kueueopv1.KueueIntegrationTFJob,
	kueueopv1.KueueIntegrationTrainJob,
	kueueopv1.KueueIntegrationXGBoostJob,
	kueueopv1.KueueIntegrationJaxJob,
	kueueopv1.KueueIntegrationAppWrapper,
	kueueopv1.KueueIntegrationPod,
	kueueopv1.KueueIntegrationDeployment,
	kueueopv1.KueueIntegrationStatefulSet,
	kueueopv1.KueueIntegrationLeaderWorkerSet,
	kueueopv1.KueueIntegrationSparkApplication,
	kueueopv1.KueueIntegrationDisaggregatedSet,
}

func TestIntegrationsCEL(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "IntegrationsCEL envtest suite")
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
	Expect(err).NotTo(HaveOccurred(), "failed to start the envtest environment")
	Expect(cfg).NotTo(BeNil(), "envtest returned no REST config")

	clients, err = kueueclient.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred(), "failed to create the Kueue clients")
	Expect(clients).NotTo(BeNil(), "no Kueue clients were created")

	kueues := clients.KueueV1().Kueues()
	kueueObj := &kueueopv1.Kueue{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: kueueopv1.KueueOperandSpec{
			OperatorSpec: operatorv1.OperatorSpec{ManagementState: operatorv1.Managed},
			Config: kueueopv1.KueueConfiguration{
				Integrations: kueueopv1.Integrations{
					Frameworks: initialFrameworks,
				},
			},
		},
	}
	_, err = kueues.Create(context.Background(), kueueObj, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to create the cluster Kueue object")
})

var _ = AfterSuite(func() {
	if testEnv == nil {
		return
	}
	Expect(testEnv.Stop()).To(Succeed(), "failed to stop the envtest environment")
})

// setFrameworks fetches the current cluster Kueue and updates it with the given
// frameworks, returning the API server error (if any).
func setFrameworks(ctx context.Context, frameworks []kueueopv1.KueueIntegration) error {
	current, getErr := clients.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
	Expect(getErr).NotTo(HaveOccurred(), "failed to get the cluster Kueue object")
	current.Spec.Config.Integrations.Frameworks = frameworks
	_, updateErr := clients.KueueV1().Kueues().Update(ctx, current, metav1.UpdateOptions{})
	return updateErr
}

var _ = Describe("IntegrationsCEL", func() {
	AfterEach(func(ctx context.Context) {
		Expect(setFrameworks(ctx, initialFrameworks)).To(Succeed(), "failed to restore the initial frameworks")
	})

	It("should allow the DisaggregatedSet framework", func(ctx context.Context) {
		By("setting frameworks to DisaggregatedSet")
		err = setFrameworks(ctx, []kueueopv1.KueueIntegration{kueueopv1.KueueIntegrationDisaggregatedSet})
		Expect(err).NotTo(HaveOccurred(),
			"want DisaggregatedSet to be accepted: %v", err)
	})

	It("should allow every framework at once", func(ctx context.Context) {
		By("setting all 19 frameworks")
		err = setFrameworks(ctx, allFrameworks)
		Expect(err).NotTo(HaveOccurred(),
			"want all frameworks to be accepted: %v", err)
	})

	It("should not allow a duplicated DisaggregatedSet framework", func(ctx context.Context) {
		By("setting DisaggregatedSet twice")
		err = setFrameworks(ctx, []kueueopv1.KueueIntegration{
			kueueopv1.KueueIntegrationDisaggregatedSet,
			kueueopv1.KueueIntegrationDisaggregatedSet,
		})
		Expect(err).To(HaveOccurred(),
			"want error for duplicated DisaggregatedSet: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"want invalid for duplicated DisaggregatedSet: %v", err)
		Expect(err.Error()).To(Or(
			ContainSubstring("each item in frameworks must be unique"),
			ContainSubstring("Duplicate value"),
		), "want the duplicate-framework error text")
	})

	It("should not allow a framework with the wrong case", func(ctx context.Context) {
		By("setting frameworks to Disaggregatedset")
		err = setFrameworks(ctx, []kueueopv1.KueueIntegration{"Disaggregatedset"})
		Expect(err).To(HaveOccurred(),
			"want error for Disaggregatedset: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"want invalid for Disaggregatedset: %v", err)
		Expect(err.Error()).To(ContainSubstring("Unsupported value"), "want the enum error text for the wrong-case framework")
	})

	It("should not allow an unknown framework", func(ctx context.Context) {
		By("setting frameworks to an unknown value")
		err = setFrameworks(ctx, []kueueopv1.KueueIntegration{"disaggregatedset.x-k8s.io/disaggregatedset"})
		Expect(err).To(HaveOccurred(),
			"want error for an unknown framework: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"want invalid for an unknown framework: %v", err)
		Expect(err.Error()).To(ContainSubstring("Unsupported value"), "want the enum error text for the unknown framework")
	})

	It("should not allow more frameworks than the list maximum", func(ctx context.Context) {
		By("setting 20 frameworks")
		tooMany := append(append([]kueueopv1.KueueIntegration{}, allFrameworks...), kueueopv1.KueueIntegrationBatchJob)
		err = setFrameworks(ctx, tooMany)
		Expect(err).To(HaveOccurred(),
			"want error for 20 frameworks: %v", err)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"want invalid for 20 frameworks: %v", err)
		Expect(err.Error()).To(ContainSubstring("must have at most 19 items"), "want the list-maximum error text")
	})
})
