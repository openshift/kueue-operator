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
	"testing"

	"github.com/onsi/ginkgo/v2/reporters"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	kubeClient    *kubernetes.Clientset
	genericClient client.Client
	clients       *testutils.TestClients
)

// Run e2e tests using the Ginkgo runner.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	GinkgoWriter.Printf("Starting kueue operator suite\n")
	config, _ := GinkgoConfiguration()
	if config.DryRun {
		report := PreviewSpecs("E2E Suite", Label("suite-label"))
		_ = reporters.GenerateJUnitReport(report, "")
	} else {
		RunSpecs(t, "e2e suite")
	}
}

var _ = BeforeSuite(func() {
	clients = testutils.NewTestClients()
	kubeClient = clients.KubeClient
	genericClient = clients.GenericClient
})
