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
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	configclientv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"

	kueueconfigapi "sigs.k8s.io/kueue/apis/config/v1beta2"
)

var _ = Describe("TLS Security Profile", Label("tls-profile"), Ordered, func() {
	var (
		configClient         *configclientv1.ConfigV1Client
		originalTLSProfile   *configv1.TLSSecurityProfile
		initialConfigMapData string
	)

	BeforeAll(func() {
		var err error
		configClient, err = configclientv1.NewForConfig(clients.RestConfig)
		Expect(err).NotTo(HaveOccurred(), "failed to create OpenShift config client")

		// Save the original TLS profile so we can restore it after tests
		apiServer, err := configClient.APIServers().Get(context.TODO(), "cluster", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to get APIServer CR")
		originalTLSProfile = apiServer.Spec.TLSSecurityProfile

		// Capture current ConfigMap state
		configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(
			context.TODO(), "kueue-manager-config", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to get kueue-manager-config ConfigMap")
		initialConfigMapData = configMap.Data["controller_manager_config.yaml"]
	})

	AfterAll(func() {
		ctx := context.TODO()
		By("Restoring original TLS security profile")
		err := updateAPIServerTLSProfile(ctx, configClient, originalTLSProfile)
		Expect(err).NotTo(HaveOccurred(), "failed to restore original TLS profile")

		By("Waiting for operand to reconcile with restored TLS profile")
		waitForConfigMapUpdate(ctx, initialConfigMapData)
	})

	When("the cluster TLS profile is set to Modern (TLS 1.3)", func() {
		It("should propagate TLS 1.3 settings to the operand ConfigMap", func(ctx context.Context) {
			By("Setting APIServer TLS profile to Modern")
			modernProfile := &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileModernType,
				Modern: &configv1.ModernTLSProfile{},
			}
			err := updateAPIServerTLSProfile(ctx, configClient, modernProfile)
			Expect(err).NotTo(HaveOccurred(), "failed to set Modern TLS profile")

			By("Waiting for the operand ConfigMap to reflect TLS 1.3 settings")
			Eventually(func() error {
				configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(
					ctx, "kueue-manager-config", metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get ConfigMap: %w", err)
				}

				configData, ok := configMap.Data["controller_manager_config.yaml"]
				if !ok {
					return fmt.Errorf("controller_manager_config.yaml key not found in ConfigMap")
				}

				tlsOpts, err := extractTLSOptions(configData)
				if err != nil {
					return fmt.Errorf("failed to extract TLS options: %w", err)
				}

				if tlsOpts == nil {
					return fmt.Errorf("TLS options not found in operand ConfigMap")
				}

				if tlsOpts.MinVersion != "VersionTLS13" {
					return fmt.Errorf("expected minVersion VersionTLS13, got %q", tlsOpts.MinVersion)
				}

				klog.Infof("Operand ConfigMap has correct TLS 1.3 settings: minVersion=%s, cipherSuites=%v",
					tlsOpts.MinVersion, tlsOpts.CipherSuites)
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(),
				"operand ConfigMap should contain TLS 1.3 settings")

			By("Verifying operand deployment rolled out with new TLS config")
			Eventually(func() error {
				deployment, err := kubeClient.AppsV1().Deployments(testutils.OperatorNamespace).Get(
					ctx, "kueue-controller-manager", metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get deployment: %w", err)
				}
				if deployment.Status.ReadyReplicas != deployment.Status.Replicas {
					return fmt.Errorf("deployment not fully ready: %d/%d replicas ready",
						deployment.Status.ReadyReplicas, deployment.Status.Replicas)
				}
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(),
				"operand deployment should be fully rolled out")
		})
	})

	When("the cluster TLS profile is set to Custom", func() {
		It("should propagate custom TLS settings to the operand ConfigMap", func(ctx context.Context) {
			By("Setting APIServer TLS profile to Custom with specific ciphers")
			customProfile := &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						Ciphers: []string{
							"ECDHE-ECDSA-AES128-GCM-SHA256",
							"ECDHE-RSA-AES128-GCM-SHA256",
						},
						MinTLSVersion: configv1.VersionTLS12,
					},
				},
			}
			err := updateAPIServerTLSProfile(ctx, configClient, customProfile)
			Expect(err).NotTo(HaveOccurred(), "failed to set Custom TLS profile")

			By("Waiting for the operand ConfigMap to reflect custom TLS settings")
			Eventually(func() error {
				configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(
					ctx, "kueue-manager-config", metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get ConfigMap: %w", err)
				}

				configData, ok := configMap.Data["controller_manager_config.yaml"]
				if !ok {
					return fmt.Errorf("controller_manager_config.yaml key not found in ConfigMap")
				}

				tlsOpts, err := extractTLSOptions(configData)
				if err != nil {
					return fmt.Errorf("failed to extract TLS options: %w", err)
				}

				if tlsOpts == nil {
					return fmt.Errorf("TLS options not found in operand ConfigMap")
				}

				if tlsOpts.MinVersion != "VersionTLS12" {
					return fmt.Errorf("expected minVersion VersionTLS12, got %q", tlsOpts.MinVersion)
				}

				// Custom profile specified 2 OpenSSL ciphers which map to 2 IANA ciphers
				if len(tlsOpts.CipherSuites) != 2 {
					return fmt.Errorf("expected 2 cipher suites for Custom profile, got %d: %v",
						len(tlsOpts.CipherSuites), tlsOpts.CipherSuites)
				}

				klog.Infof("Operand ConfigMap has correct Custom TLS settings: minVersion=%s, cipherSuites=%v",
					tlsOpts.MinVersion, tlsOpts.CipherSuites)
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(),
				"operand ConfigMap should contain Custom TLS settings")
		})
	})

	When("the cluster TLS profile is restored to Intermediate (default)", func() {
		It("should propagate Intermediate TLS settings to the operand ConfigMap", func(ctx context.Context) {
			By("Setting APIServer TLS profile to Intermediate")
			intermediateProfile := &configv1.TLSSecurityProfile{
				Type:         configv1.TLSProfileIntermediateType,
				Intermediate: &configv1.IntermediateTLSProfile{},
			}
			err := updateAPIServerTLSProfile(ctx, configClient, intermediateProfile)
			Expect(err).NotTo(HaveOccurred(), "failed to set Intermediate TLS profile")

			By("Waiting for the operand ConfigMap to reflect Intermediate TLS settings")
			Eventually(func() error {
				configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(
					ctx, "kueue-manager-config", metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get ConfigMap: %w", err)
				}

				configData, ok := configMap.Data["controller_manager_config.yaml"]
				if !ok {
					return fmt.Errorf("controller_manager_config.yaml key not found in ConfigMap")
				}

				tlsOpts, err := extractTLSOptions(configData)
				if err != nil {
					return fmt.Errorf("failed to extract TLS options: %w", err)
				}

				if tlsOpts == nil {
					return fmt.Errorf("TLS options not found in operand ConfigMap")
				}

				if tlsOpts.MinVersion != "VersionTLS12" {
					return fmt.Errorf("expected minVersion VersionTLS12, got %q", tlsOpts.MinVersion)
				}

				klog.Infof("Operand ConfigMap has correct Intermediate TLS settings: minVersion=%s, %d cipherSuites",
					tlsOpts.MinVersion, len(tlsOpts.CipherSuites))
				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(),
				"operand ConfigMap should contain Intermediate TLS settings")
		})
	})
})

// updateAPIServerTLSProfile updates the TLS security profile on the cluster APIServer CR.
func updateAPIServerTLSProfile(ctx context.Context, configClient *configclientv1.ConfigV1Client, profile *configv1.TLSSecurityProfile) error {
	apiServer, err := configClient.APIServers().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get APIServer CR: %w", err)
	}

	apiServer.Spec.TLSSecurityProfile = profile

	_, err = configClient.APIServers().Update(ctx, apiServer, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update APIServer CR: %w", err)
	}

	klog.Infof("Updated APIServer TLS profile to %v", profileTypeString(profile))
	return nil
}

// profileTypeString returns a descriptive string for the TLS profile type.
func profileTypeString(profile *configv1.TLSSecurityProfile) string {
	if profile == nil {
		return "nil (default Intermediate)"
	}
	return string(profile.Type)
}

// extractTLSOptions parses the kueue controller manager config YAML and extracts the TLS options.
func extractTLSOptions(configData string) (*kueueconfigapi.TLSOptions, error) {
	var config kueueconfigapi.Configuration
	if err := yaml.Unmarshal([]byte(configData), &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal kueue configuration: %w", err)
	}
	return config.TLS, nil
}

// waitForConfigMapUpdate waits until the ConfigMap data changes from the initial value.
func waitForConfigMapUpdate(ctx context.Context, previousData string) {
	Eventually(func() error {
		configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(
			ctx, "kueue-manager-config", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get ConfigMap: %w", err)
		}
		currentData := configMap.Data["controller_manager_config.yaml"]
		if currentData == previousData {
			return fmt.Errorf("ConfigMap has not been updated yet")
		}
		return nil
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(),
		"ConfigMap should be updated after TLS profile change")
}
