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
	"fmt"
	"strings"
	"testing"

	"github.com/openshift/kueue-operator/test/e2e/testutils"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	visibilityv1beta1 "sigs.k8s.io/kueue/client-go/clientset/versioned/typed/visibility/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"
)

var (
	kubeClient       *kubernetes.Clientset
	genericClient    client.Client
	clients          *testutils.TestClients
	visibilityClient visibilityv1beta1.VisibilityV1beta1Interface
	err              error
)

// Run e2e tests using the Ginkgo runner.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	config, _ := GinkgoConfiguration()
	config.ParallelProcess = 1
	if config.DryRun {
		GinkgoWriter.Printf("Starting kueue operator suite [DryRun]\n")
		report := PreviewSpecs("E2E Suite", config)
		for _, sr := range report.SpecReports {
			if sr.State == types.SpecStateSkipped {
				continue
			}
			if len(sr.Labels()) > 0 {
				GinkgoWriter.Printf("%s [labels: %s]\n", sr.FullText(), strings.Join(sr.Labels(), ", "))
			} else {
				GinkgoWriter.Printf("%s\n", sr.FullText())
			}
		}
	} else {
		GinkgoWriter.Printf("Starting kueue operator suite\n")
		RunSpecs(t, "e2e suite", config)
	}
}

var _ = BeforeSuite(func() {
	clients = testutils.NewTestClients()
	kubeClient = clients.KubeClient
	genericClient = clients.GenericClient

	visibilityClient, err = testutils.GetVisibilityClient(fmt.Sprintf("system:serviceaccount:%s:default", testutils.OperatorNamespace))
	Expect(err).NotTo(HaveOccurred())

	// Deploy the Kueue operand once for all tests
	By("Deploying Kueue operand for all test suites")
	Expect(deployOperand()).To(Succeed(), "operand deployment should not fail")
})
