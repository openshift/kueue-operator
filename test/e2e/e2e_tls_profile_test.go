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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configclientv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/openshift/kueue-operator/pkg/tlsprofile"
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
		isHyperShift         bool
	)

	BeforeAll(func() {
		var err error
		configClient, err = configclientv1.NewForConfig(clients.RestConfig)
		Expect(err).NotTo(HaveOccurred(), "failed to create OpenShift config client")

		isHyperShift, err = testutils.IsHyperShiftCluster(configClient)
		Expect(err).NotTo(HaveOccurred(), "failed to detect HyperShift cluster")

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
		if isHyperShift {
			return
		}
		ctx := context.TODO()
		By("Restoring original TLS security profile")
		err := updateAPIServerTLSProfile(ctx, configClient, originalTLSProfile)
		Expect(err).NotTo(HaveOccurred(), "failed to restore original TLS profile")

		By("Waiting for operand to reconcile with restored TLS profile")
		waitForConfigMapToMatch(ctx, initialConfigMapData)
	})

	When("the default Intermediate TLS profile is configured", func() {
		It("should have Intermediate TLS settings in the initial operand ConfigMap", func(ctx context.Context) {
			By("Verifying the initial ConfigMap captured in BeforeAll contains Intermediate TLS settings")
			expectedTLSOpts, _, err := tlsprofile.TLSOptionsFromProfile(nil)
			Expect(err).NotTo(HaveOccurred(), "failed to resolve default TLS profile")

			tlsOpts, err := extractTLSOptions(initialConfigMapData)
			Expect(err).NotTo(HaveOccurred(), "failed to extract TLS options from initial ConfigMap")
			Expect(tlsOpts).NotTo(BeNil(), "TLS options not found in initial operand ConfigMap")
			Expect(tlsOpts.MinVersion).To(Equal(expectedTLSOpts.MinVersion),
				"initial ConfigMap should have Intermediate minVersion")
			Expect(tlsOpts.CipherSuites).To(HaveLen(len(expectedTLSOpts.CipherSuites)),
				fmt.Sprintf("initial ConfigMap should have %d Intermediate cipher suites, got %v",
					len(expectedTLSOpts.CipherSuites), tlsOpts.CipherSuites))

			klog.Infof("Initial ConfigMap has correct Intermediate TLS settings: minVersion=%s, %d cipherSuites",
				tlsOpts.MinVersion, len(tlsOpts.CipherSuites))
		})
	})

	When("the cluster TLS profile is set to Modern (TLS 1.3)", func() {
		It("should propagate TLS 1.3 settings to the operand ConfigMap", func(ctx context.Context) {
			if isHyperShift {
				Skip("APIServer TLS profile mutation is not supported on HyperShift clusters")
			}
			By("Checking if Modern TLS profile is supported on this cluster")
			modernProfile := &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileModernType,
				Modern: &configv1.ModernTLSProfile{},
			}
			apiServer, err := configClient.APIServers().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to get APIServer CR")

			dryRunAPIServer := apiServer.DeepCopy()
			dryRunAPIServer.Spec.TLSSecurityProfile = modernProfile
			_, err = configClient.APIServers().Update(ctx, dryRunAPIServer, metav1.UpdateOptions{
				DryRun: []string{metav1.DryRunAll},
			})
			if err != nil {
				Skip(fmt.Sprintf("Modern TLS profile is not supported on this cluster: %v", err))
			}

			By("Setting APIServer TLS profile to Modern")
			err = updateAPIServerTLSProfile(ctx, configClient, modernProfile)
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

				// TLS 1.3 cipher suites are not configurable in Go's crypto/tls,
				// so CipherSuites must be empty for the Modern profile.
				if len(tlsOpts.CipherSuites) != 0 {
					return fmt.Errorf("expected empty CipherSuites for TLS 1.3 Modern profile, got %v", tlsOpts.CipherSuites)
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

	When("the cluster TLS profile is set to Old (TLS 1.0)", func() {
		It("should set Degraded condition instead of crashing the controller", func(ctx context.Context) {
			if isHyperShift {
				Skip("APIServer TLS profile mutation is not supported on HyperShift clusters")
			}
			By("Setting APIServer TLS profile to Old")
			oldProfile := &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
				Old:  &configv1.OldTLSProfile{},
			}
			err := updateAPIServerTLSProfile(ctx, configClient, oldProfile)
			Expect(err).NotTo(HaveOccurred(), "failed to set Old TLS profile")

			By("Waiting for the operator to report Degraded condition with UnsupportedTLSProfile reason")
			// Use a longer timeout because changing the APIServer TLS profile triggers
			// a kube-apiserver rollout, during which the operator retries until the
			// API server is available again to fetch the updated TLS profile.
			Eventually(func() error {
				kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get Kueue instance: %w", err)
				}
				for _, condition := range kueueInstance.Status.Conditions {
					if condition.Type == operatorv1.OperatorStatusTypeDegraded && condition.Status == operatorv1.ConditionTrue &&
						condition.Reason == "UnsupportedTLSProfile" {
						klog.Infof("Degraded condition correctly set: reason=%s, message=%s", condition.Reason, condition.Message)
						return nil
					}
				}
				conditionSummary := formatConditions(kueueInstance.Status.Conditions)
				return fmt.Errorf("expected Degraded condition with UnsupportedTLSProfile reason, current conditions: %s", conditionSummary)
			}, 10*time.Minute, testutils.OperatorPoll).Should(Succeed(),
				"operator should report Degraded condition for Old TLS profile")

			By("Verifying the operator also reports Available=False")
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to get Kueue instance")
			foundAvailable := false
			for _, condition := range kueueInstance.Status.Conditions {
				if condition.Type == operatorv1.OperatorStatusTypeAvailable {
					foundAvailable = true
					Expect(condition.Status).To(Equal(operatorv1.ConditionFalse),
						"Available condition should be False when TLS profile is unsupported")
					Expect(condition.Reason).To(Equal("UnsupportedTLSProfile"))
				}
			}
			Expect(foundAvailable).To(BeTrue(), "Available condition must be present")

			By("Restoring TLS profile to Intermediate to recover from Degraded state")
			err = updateAPIServerTLSProfile(ctx, configClient, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to restore Intermediate TLS profile")

			By("Waiting for the operator to recover and become Available")
			Eventually(func() error {
				kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get Kueue instance: %w", err)
				}
				for _, condition := range kueueInstance.Status.Conditions {
					if condition.Type == operatorv1.OperatorStatusTypeAvailable && condition.Status == operatorv1.ConditionTrue {
						return nil
					}
				}
				conditionSummary := formatConditions(kueueInstance.Status.Conditions)
				return fmt.Errorf("operator has not recovered to Available state yet, current conditions: %s", conditionSummary)
			}, 10*time.Minute, testutils.OperatorPoll).Should(Succeed(),
				"operator should recover after TLS profile is changed back to Intermediate")
		})
	})

	When("the cluster TLS profile is set to Custom", func() {
		It("should propagate custom TLS settings to the operand ConfigMap", func(ctx context.Context) {
			if isHyperShift {
				Skip("APIServer TLS profile mutation is not supported on HyperShift clusters")
			}
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

	When("the cluster TLS profile is set to Custom with invalid cipher suites", func() {
		It("should emit an InvalidTLSCipherSuites warning event for unmapped ciphers", func(ctx context.Context) {
			if isHyperShift {
				Skip("APIServer TLS profile mutation is not supported on HyperShift clusters")
			}

			By("Setting APIServer TLS profile to Custom with a mix of valid and invalid ciphers")
			customProfile := &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						Ciphers: []string{
							"ECDHE-RSA-AES128-GCM-SHA256",
							"TLS_ALICE_POLY1305_SHA256",
							"INVALID-CIPHER",
						},
						MinTLSVersion: configv1.VersionTLS12,
					},
				},
			}
			err := updateAPIServerTLSProfile(ctx, configClient, customProfile)
			Expect(err).NotTo(HaveOccurred(), "failed to set Custom TLS profile with invalid ciphers")

			By("Waiting for the operand ConfigMap to contain only the valid cipher")
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

				// Only the valid cipher should be present
				if len(tlsOpts.CipherSuites) != 1 {
					return fmt.Errorf("expected 1 cipher suite (only valid ones), got %d: %v",
						len(tlsOpts.CipherSuites), tlsOpts.CipherSuites)
				}

				return nil
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(),
				"operand ConfigMap should contain only valid cipher suites")

			By("Verifying that an InvalidTLSCipherSuites warning event was emitted")
			Eventually(func() error {
				events, err := kubeClient.CoreV1().Events(testutils.OperatorNamespace).List(ctx, metav1.ListOptions{
					FieldSelector: "reason=InvalidTLSCipherSuites",
				})
				if err != nil {
					return fmt.Errorf("failed to list events: %w", err)
				}

				for _, event := range events.Items {
					if event.Type == "Warning" && event.Reason == "InvalidTLSCipherSuites" {
						klog.Infof("Found expected warning event: reason=%s, message=%s", event.Reason, event.Message)
						return nil
					}
				}
				return fmt.Errorf("no InvalidTLSCipherSuites warning event found in namespace %s", testutils.OperatorNamespace)
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(),
				"operator should emit InvalidTLSCipherSuites warning event for unmapped ciphers")
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

// waitForConfigMapToMatch waits until the ConfigMap data matches the expected value.
func waitForConfigMapToMatch(ctx context.Context, expectedData string) {
	Eventually(func() error {
		configMap, err := kubeClient.CoreV1().ConfigMaps(testutils.OperatorNamespace).Get(
			ctx, "kueue-manager-config", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get ConfigMap: %w", err)
		}
		currentData := configMap.Data["controller_manager_config.yaml"]
		if currentData != expectedData {
			return fmt.Errorf("ConfigMap has not been restored yet")
		}
		return nil
	}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(Succeed(),
		"ConfigMap should be restored after TLS profile change")
}

// formatConditions returns a human-readable summary of operator conditions for debug logging.
func formatConditions(conditions []operatorv1.OperatorCondition) string {
	if len(conditions) == 0 {
		return "<none>"
	}
	result := ""
	for i, c := range conditions {
		if i > 0 {
			result += "; "
		}
		result += fmt.Sprintf("%s=%s (reason=%s)", c.Type, c.Status, c.Reason)
	}
	return result
}
