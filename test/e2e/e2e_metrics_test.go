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
	"bytes"
	"context"
	"fmt"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

const (
	metricsServiceName       = "kueue-controller-manager-metrics-service"
	serviceMonitorName       = "kueue-metrics"
	metricsPort              = 8443
	metricsSecretName        = "metrics-server-cert"
	metricsNetworkPolicyName = "kueue-allow-ingress-metrics"
	monitoringNamespace      = "openshift-monitoring"
)

var _ = Describe("Kueue Metrics", Label("metrics"), Ordered, func() {
	var curlPod *corev1.Pod

	BeforeAll(func() {
		By("Deploying Kueue operand if not already deployed")
		Expect(deployOperand()).To(Succeed(), "operand deployment should not fail")

		By("Ensuring monitoring namespace exists")
		_, err := kubeClient.CoreV1().Namespaces().Get(context.Background(), monitoringNamespace, metav1.GetOptions{})
		if err != nil {
			// Create monitoring namespace if it doesn't exist
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: monitoringNamespace,
					Labels: map[string]string{
						"openshift.io/cluster-monitoring": "true",
						"kubernetes.io/metadata.name":     monitoringNamespace,
					},
				},
			}
			_, err = kubeClient.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
		}

		By("Copying metrics certificate secret to monitoring namespace")
		secret, err := kubeClient.CoreV1().Secrets(testutils.OperatorNamespace).Get(context.Background(), metricsSecretName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Delete existing secret if present
		_ = kubeClient.CoreV1().Secrets(monitoringNamespace).Delete(context.Background(), metricsSecretName, metav1.DeleteOptions{})

		monitoringSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      metricsSecretName,
				Namespace: monitoringNamespace,
			},
			Type: secret.Type,
			Data: secret.Data,
		}
		_, err = kubeClient.CoreV1().Secrets(monitoringNamespace).Create(context.Background(), monitoringSecret, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Creating curl pod in monitoring namespace")
		runAsNonRoot := true
		allowPrivilegeEscalation := false
		curlPod = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "metrics-curl-pod",
				Namespace: monitoringNamespace,
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: "default",
				SecurityContext: &corev1.PodSecurityContext{
					RunAsNonRoot: &runAsNonRoot,
					SeccompProfile: &corev1.SeccompProfile{
						Type: corev1.SeccompProfileTypeRuntimeDefault,
					},
				},
				Containers: []corev1.Container{
					{
						Name:    "curl",
						Image:   "registry.access.redhat.com/ubi9/ubi-minimal:latest",
						Command: []string{"/bin/sh", "-c", "sleep 3600"},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: &allowPrivilegeEscalation,
							RunAsNonRoot:             &runAsNonRoot,
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "metrics-certs",
								MountPath: "/etc/metrics-certs",
								ReadOnly:  true,
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "metrics-certs",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName: metricsSecretName,
							},
						},
					},
				},
			},
		}
		curlPod, err = kubeClient.CoreV1().Pods(monitoringNamespace).Create(context.Background(), curlPod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Waiting for curl pod to be ready")
		Eventually(func() bool {
			pod, err := kubeClient.CoreV1().Pods(monitoringNamespace).Get(context.Background(), curlPod.Name, metav1.GetOptions{})
			if err != nil {
				return false
			}
			return pod.Status.Phase == corev1.PodRunning
		}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())
	})

	AfterAll(func() {
		if curlPod != nil {
			_ = kubeClient.CoreV1().Pods(monitoringNamespace).Delete(context.Background(), curlPod.Name, metav1.DeleteOptions{})
		}
		// Clean up the copied secret
		_ = kubeClient.CoreV1().Secrets(monitoringNamespace).Delete(context.Background(), metricsSecretName, metav1.DeleteOptions{})
	})

	Context("Metrics Infrastructure", func() {
		It("should have metrics service created", func() {
			service, err := kubeClient.CoreV1().Services(testutils.OperatorNamespace).Get(context.Background(), metricsServiceName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(service).NotTo(BeNil())
			Expect(service.Spec.Ports).To(HaveLen(1))
			Expect(service.Spec.Ports[0].Port).To(Equal(int32(metricsPort)))
			Expect(service.Spec.Ports[0].Name).To(Equal("https"))
		})

		It("should have metrics TLS secret created", func() {
			secret, err := kubeClient.CoreV1().Secrets(testutils.OperatorNamespace).Get(context.Background(), metricsSecretName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(secret).NotTo(BeNil())
			Expect(secret.Data).To(HaveKey("tls.crt"))
			Expect(secret.Data).To(HaveKey("tls.key"))
			Expect(secret.Data).To(HaveKey("ca.crt"))
		})

		It("should have ServiceMonitor created", func() {
			sm := &monitoringv1.ServiceMonitor{}
			err := genericClient.Get(context.Background(), types.NamespacedName{
				Name:      serviceMonitorName,
				Namespace: testutils.OperatorNamespace,
			}, sm)
			Expect(err).NotTo(HaveOccurred())
			Expect(sm).NotTo(BeNil())
			Expect(sm.Spec.Endpoints).To(HaveLen(1))
			Expect(sm.Spec.Endpoints[0].Port).To(Equal("https"))
			Expect(sm.Spec.Endpoints[0].Scheme).To(Equal("https"))
		})

		It("should have metrics network policy with correct namespace selectors", func() {
			netpol := &networkingv1.NetworkPolicy{}
			err := genericClient.Get(context.Background(), types.NamespacedName{
				Name:      metricsNetworkPolicyName,
				Namespace: testutils.OperatorNamespace,
			}, netpol)
			Expect(err).NotTo(HaveOccurred())
			Expect(netpol).NotTo(BeNil())

			// Verify ingress rules exist
			Expect(netpol.Spec.Ingress).NotTo(BeEmpty())

			// Verify namespace selectors are present for monitoring namespaces
			hasMonitoringSelector := false
			for _, ingress := range netpol.Spec.Ingress {
				for _, from := range ingress.From {
					if from.NamespaceSelector != nil {
						// Check for monitoring namespace labels
						if labels := from.NamespaceSelector.MatchLabels; labels != nil {
							if _, ok := labels["openshift.io/cluster-monitoring"]; ok {
								hasMonitoringSelector = true
							}
							if name, ok := labels["kubernetes.io/metadata.name"]; ok {
								if name == "openshift-monitoring" || name == "openshift-user-workload-monitoring" {
									hasMonitoringSelector = true
								}
							}
						}
					}
				}
			}
			Expect(hasMonitoringSelector).To(BeTrue(), "NetworkPolicy should allow ingress from monitoring namespaces")
		})
	})

	Context("Metrics Endpoint", func() {
		It("should return valid metrics over HTTPS", func() {
			By("Executing curl command in monitoring pod")
			curlCmd := fmt.Sprintf("curl -s -k -v --cacert /etc/metrics-certs/ca.crt -H \"Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)\" https://%s.%s.svc.cluster.local:%d/metrics",
				metricsServiceName, testutils.OperatorNamespace, metricsPort)
			cmd := []string{"/bin/sh", "-c", curlCmd}

			output, err := execInPod(monitoringNamespace, curlPod.Name, cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).NotTo(BeEmpty())

			By("Verifying metrics format")
			metricsOutput := string(output)
			// Log the output for debugging
			outputLen := len(metricsOutput)
			if outputLen > 500 {
				outputLen = 500
			}
			GinkgoWriter.Printf("Metrics output (first %d chars): %s\n", outputLen, metricsOutput[:outputLen])

			// Check for Prometheus metric format (key-value or HELP/TYPE lines)
			Expect(metricsOutput).To(ContainSubstring("# HELP"), fmt.Sprintf("Expected metrics output to contain '# HELP'"))
			Expect(metricsOutput).To(ContainSubstring("# TYPE"))
		})

		It("should expose kueue-specific metrics", func() {
			By("Executing curl command in monitoring pod")
			curlCmd := fmt.Sprintf("curl -s -k --cacert /etc/metrics-certs/ca.crt -H \"Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)\" https://%s.%s.svc.cluster.local:%d/metrics",
				metricsServiceName, testutils.OperatorNamespace, metricsPort)
			cmd := []string{"/bin/sh", "-c", curlCmd}

			output, err := execInPod(monitoringNamespace, curlPod.Name, cmd)
			Expect(err).NotTo(HaveOccurred())

			metricsOutput := string(output)

			By("Checking for kueue build info metric")
			Expect(metricsOutput).To(ContainSubstring("kueue_build_info"))

			By("Checking for controller-runtime metrics")
			// These are always present in controller-runtime applications
			Expect(metricsOutput).To(Or(
				ContainSubstring("controller_runtime_"),
				ContainSubstring("workqueue_"),
			))
		})
	})

	Context("Cross-namespace Access", func() {
		It("should allow metrics scraping from monitoring namespace", func() {
			By("Verifying monitoring pod can reach metrics from operator namespace")
			curlCmd := fmt.Sprintf("curl -s -k --max-time 10 --cacert /etc/metrics-certs/ca.crt -H \"Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)\" https://%s.%s.svc.cluster.local:%d/metrics",
				metricsServiceName, testutils.OperatorNamespace, metricsPort)
			cmd := []string{"/bin/sh", "-c", curlCmd}

			output, err := execInPod(monitoringNamespace, curlPod.Name, cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).NotTo(BeEmpty())
			Expect(string(output)).To(ContainSubstring("# HELP"))
		})

		It("should block metrics scraping from non-monitoring namespace", func() {
			var testNamespace *corev1.Namespace
			var testPod *corev1.Pod

			By("Creating a test namespace without monitoring labels")
			testNamespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "metrics-negative-test-",
				},
			}
			var err error
			testNamespace, err = kubeClient.CoreV1().Namespaces().Create(context.Background(), testNamespace, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			defer func() {
				By("Cleaning up test namespace")
				_ = kubeClient.CoreV1().Namespaces().Delete(context.Background(), testNamespace.Name, metav1.DeleteOptions{})
			}()

			By("Copying metrics certificate secret to test namespace")
			secret, err := kubeClient.CoreV1().Secrets(testutils.OperatorNamespace).Get(context.Background(), metricsSecretName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			testSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      metricsSecretName,
					Namespace: testNamespace.Name,
				},
				Type: secret.Type,
				Data: secret.Data,
			}
			_, err = kubeClient.CoreV1().Secrets(testNamespace.Name).Create(context.Background(), testSecret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating curl pod in test namespace")
			runAsNonRoot := true
			allowPrivilegeEscalation := false
			testPod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "metrics-test-curl-pod",
					Namespace: testNamespace.Name,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "default",
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &runAsNonRoot,
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "curl",
							Image:   "registry.access.redhat.com/ubi9/ubi-minimal:latest",
							Command: []string{"/bin/sh", "-c", "sleep 3600"},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &allowPrivilegeEscalation,
								RunAsNonRoot:             &runAsNonRoot,
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "metrics-certs",
									MountPath: "/etc/metrics-certs",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "metrics-certs",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: metricsSecretName,
								},
							},
						},
					},
				},
			}
			testPod, err = kubeClient.CoreV1().Pods(testNamespace.Name).Create(context.Background(), testPod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for test pod to be ready")
			Eventually(func() bool {
				pod, err := kubeClient.CoreV1().Pods(testNamespace.Name).Get(context.Background(), testPod.Name, metav1.GetOptions{})
				if err != nil {
					return false
				}
				return pod.Status.Phase == corev1.PodRunning
			}, testutils.OperatorReadyTime, testutils.OperatorPoll).Should(BeTrue())

			By("Verifying test pod CANNOT reach metrics (should timeout or be forbidden)")
			curlCmd := fmt.Sprintf("curl -s -k --max-time 5 --cacert /etc/metrics-certs/ca.crt -H \"Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)\" https://%s.%s.svc.cluster.local:%d/metrics",
				metricsServiceName, testutils.OperatorNamespace, metricsPort)
			cmd := []string{"/bin/sh", "-c", curlCmd}

			output, err := execInPod(testNamespace.Name, testPod.Name, cmd)
			// Expect either: (1) command fails due to network policy timeout, OR (2) receives forbidden/authentication error
			if err == nil {
				// Command succeeded, check if it's an authorization failure
				outputStr := string(output)
				Expect(outputStr).To(Or(
					ContainSubstring("Forbidden"),
					ContainSubstring("Authentication failed"),
					ContainSubstring("Unauthorized"),
					ContainSubstring("Authorization denied"),
				), "Expected metrics access to be blocked by network policy or RBAC, but got: %s", outputStr)
			}
		})
	})
})

// execInPod executes a command in a pod and returns the output
func execInPod(namespace, podName string, command []string) ([]byte, error) {
	req := kubeClient.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "curl",
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(clients.RestConfig, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("failed to create executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute command: %w, stderr: %s", err, stderr.String())
	}

	return io.ReadAll(&stdout)
}
