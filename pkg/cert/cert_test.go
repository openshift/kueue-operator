package cert

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

const (
	// Cert-manager condition types
	conditionTypeReady   = "Ready"
	conditionTypeIssuing = "Issuing"

	// Cert-manager condition reasons
	conditionReasonReady   = "Ready"
	conditionReasonIssuing = "Issuing"
)

func TestInjectCertAnnotation(t *testing.T) {
	testcases := map[string]struct {
		annotations         map[string]string
		expectedAnnotations map[string]string
	}{
		"nil annotations": {
			annotations:         nil,
			expectedAnnotations: map[string]string{"cert-manager.io/inject-ca-from": fmt.Sprintf("%s/webhook-cert", "test")},
		},
		"existingAnnotations": {
			annotations: map[string]string{"hello": "world"},
			expectedAnnotations: map[string]string{
				"hello":                          "world",
				"cert-manager.io/inject-ca-from": fmt.Sprintf("%s/webhook-cert", "test"),
			},
		},
	}
	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			got := InjectCertAnnotation(tc.annotations, "test")
			if diff := cmp.Diff(got, tc.expectedAnnotations); len(diff) != 0 {
				t.Errorf("Unexpected buckets (-want,+got):\n%s", diff)
			}
		})
	}
}

// Helper functions for creating test resources

//nolint:unparam
func createCertificate(namespace, name, secretName string, ready, issuing bool, readyReason, readyMessage string) *unstructured.Unstructured {
	cert := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cert-manager.io/v1",
			"kind":       "Certificate",
			"metadata": map[string]any{
				"namespace": namespace,
				"name":      name,
			},
			"spec": map[string]any{},
		},
	}

	if secretName != "" {
		_ = unstructured.SetNestedField(cert.Object, secretName, "spec", "secretName")
	}

	var conditions []any

	if ready || readyReason != "" {
		readyCondition := map[string]any{
			"type":               conditionTypeReady,
			"status":             string(corev1.ConditionFalse),
			"lastTransitionTime": time.Now().Format(time.RFC3339),
		}
		if ready {
			readyCondition["status"] = string(corev1.ConditionTrue)
			readyCondition["reason"] = conditionReasonReady
		} else if readyReason != "" {
			readyCondition["reason"] = readyReason
			readyCondition["message"] = readyMessage
		}
		conditions = append(conditions, readyCondition)
	}

	if issuing {
		issuingCondition := map[string]any{
			"type":               conditionTypeIssuing,
			"status":             string(corev1.ConditionTrue),
			"reason":             conditionReasonIssuing,
			"lastTransitionTime": time.Now().Format(time.RFC3339),
		}
		conditions = append(conditions, issuingCondition)
	}

	if len(conditions) > 0 {
		_ = unstructured.SetNestedSlice(cert.Object, conditions, "status", "conditions")
	}

	return cert
}

//nolint:unparam
func createSecret(namespace, name string, hasTLSCrt, hasTLSKey, hasCA bool, emptyData bool) *unstructured.Unstructured {
	secret := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]any{
				"namespace": namespace,
				"name":      name,
			},
			"type": "kubernetes.io/tls",
			"data": map[string]any{},
		},
	}

	data := map[string]any{}

	if hasTLSCrt {
		if emptyData {
			data["tls.crt"] = ""
		} else {
			data["tls.crt"] = base64.StdEncoding.EncodeToString([]byte("fake-cert-data"))
		}
	}

	if hasTLSKey {
		if emptyData {
			data["tls.key"] = ""
		} else {
			data["tls.key"] = base64.StdEncoding.EncodeToString([]byte("fake-key-data"))
		}
	}

	if hasCA {
		data["ca.crt"] = base64.StdEncoding.EncodeToString([]byte("fake-ca-data"))
	}

	if len(data) > 0 {
		_ = unstructured.SetNestedMap(secret.Object, data, "data")
	}

	return secret
}

func TestWaitForCertificateReady(t *testing.T) {
	testcases := map[string]struct {
		certificate       *unstructured.Unstructured
		secret            *unstructured.Unstructured
		setupReactor      func(client *fake.FakeDynamicClient)
		timeout           time.Duration
		expectError       bool
		errorContains     string
		expectedNamespace string
		expectedCertName  string
	}{
		"certificate ready with valid secret": {
			certificate:       createCertificate("test-ns", "test-cert", "test-secret", true, false, "", ""),
			secret:            createSecret("test-ns", "test-secret", true, true, true, false),
			timeout:           5 * time.Second,
			expectError:       false,
			expectedNamespace: "test-ns",
			expectedCertName:  "test-cert",
		},
		"certificate ready without CA in secret": {
			certificate:       createCertificate("test-ns", "test-cert", "test-secret", true, false, "", ""),
			secret:            createSecret("test-ns", "test-secret", true, true, false, false),
			timeout:           5 * time.Second,
			expectError:       false,
			expectedNamespace: "test-ns",
			expectedCertName:  "test-cert",
		},
		"certificate not ready - ready condition false": {
			certificate:       createCertificate("test-ns", "test-cert", "test-secret", false, false, "Pending", "Certificate is pending"),
			secret:            createSecret("test-ns", "test-secret", true, true, false, false),
			timeout:           3 * time.Second,
			expectError:       true,
			errorContains:     "timeout waiting for certificate",
			expectedNamespace: "test-ns",
			expectedCertName:  "test-cert",
		},
		"certificate is being issued": {
			certificate:       createCertificate("test-ns", "test-cert", "test-secret", false, true, "Issuing", "Certificate is being issued"),
			secret:            createSecret("test-ns", "test-secret", true, true, false, false),
			timeout:           3 * time.Second,
			expectError:       true,
			errorContains:     "timeout waiting for certificate",
			expectedNamespace: "test-ns",
			expectedCertName:  "test-cert",
		},
		"certificate has no conditions": {
			certificate: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]any{
						"namespace": "test-ns",
						"name":      "test-cert",
					},
					"spec": map[string]any{
						"secretName": "test-secret",
					},
				},
			},
			secret:            createSecret("test-ns", "test-secret", true, true, false, false),
			timeout:           3 * time.Second,
			expectError:       true,
			errorContains:     "timeout waiting for certificate",
			expectedNamespace: "test-ns",
			expectedCertName:  "test-cert",
		},
		"certificate has no secretName": {
			certificate: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]any{
						"namespace": "test-ns",
						"name":      "test-cert",
					},
					"spec": map[string]any{},
					"status": map[string]any{
						"conditions": []any{
							map[string]any{
								"type":   conditionTypeReady,
								"status": string(corev1.ConditionTrue),
								"reason": conditionReasonReady,
							},
						},
					},
				},
			},
			timeout:           3 * time.Second,
			expectError:       true,
			errorContains:     "timeout waiting for certificate",
			expectedNamespace: "test-ns",
			expectedCertName:  "test-cert",
		},
		"secret not found": {
			certificate:       createCertificate("test-ns", "test-cert", "test-secret", true, false, "", ""),
			secret:            nil, // No secret
			timeout:           3 * time.Second,
			expectError:       true,
			errorContains:     "timeout waiting for certificate",
			expectedNamespace: "test-ns",
			expectedCertName:  "test-cert",
		},
		"secret missing tls.crt": {
			certificate:       createCertificate("test-ns", "test-cert", "test-secret", true, false, "", ""),
			secret:            createSecret("test-ns", "test-secret", false, true, false, false),
			timeout:           3 * time.Second,
			expectError:       true,
			errorContains:     "timeout waiting for certificate",
			expectedNamespace: "test-ns",
			expectedCertName:  "test-cert",
		},
		"secret missing tls.key": {
			certificate:       createCertificate("test-ns", "test-cert", "test-secret", true, false, "", ""),
			secret:            createSecret("test-ns", "test-secret", true, false, false, false),
			timeout:           3 * time.Second,
			expectError:       true,
			errorContains:     "timeout waiting for certificate",
			expectedNamespace: "test-ns",
			expectedCertName:  "test-cert",
		},
		"secret has empty certificate data": {
			certificate:       createCertificate("test-ns", "test-cert", "test-secret", true, false, "", ""),
			secret:            createSecret("test-ns", "test-secret", true, true, false, true),
			timeout:           3 * time.Second,
			expectError:       true,
			errorContains:     "timeout waiting for certificate",
			expectedNamespace: "test-ns",
			expectedCertName:  "test-cert",
		},
		"secret has no data field": {
			certificate: createCertificate("test-ns", "test-cert", "test-secret", true, false, "", ""),
			secret: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"namespace": "test-ns",
						"name":      "test-secret",
					},
				},
			},
			timeout:           3 * time.Second,
			expectError:       true,
			errorContains:     "timeout waiting for certificate",
			expectedNamespace: "test-ns",
			expectedCertName:  "test-cert",
		},
		"certificate not found": {
			certificate:       nil,
			secret:            createSecret("test-ns", "test-secret", true, true, false, false),
			timeout:           3 * time.Second,
			expectError:       true,
			errorContains:     "timeout waiting for certificate",
			expectedNamespace: "test-ns",
			expectedCertName:  "test-cert",
		},
		"context cancelled": {
			certificate: createCertificate("test-ns", "test-cert", "test-secret", false, false, "Pending", "Still pending"),
			secret:      createSecret("test-ns", "test-secret", true, true, false, false),
			setupReactor: func(client *fake.FakeDynamicClient) {
				// Reactor to cancel context after first Get
				callCount := int32(0)
				client.PrependReactor("get", "certificates", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					if atomic.AddInt32(&callCount, 1) == 1 {
						// Let first call through
						return false, nil, nil
					}
					// Subsequent calls will happen but context will be cancelled
					return false, nil, nil
				})
			},
			timeout:           100 * time.Millisecond,
			expectError:       true,
			errorContains:     "timeout waiting for certificate",
			expectedNamespace: "test-ns",
			expectedCertName:  "test-cert",
		},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			var objects []runtime.Object

			if tc.certificate != nil {
				objects = append(objects, tc.certificate)
			}
			if tc.secret != nil {
				objects = append(objects, tc.secret)
			}

			fakeClient := fake.NewSimpleDynamicClient(scheme, objects...)

			if tc.setupReactor != nil {
				tc.setupReactor(fakeClient)
			}

			ctx := context.Background()
			err := WaitForCertificateReady(ctx, fakeClient, tc.expectedNamespace, tc.expectedCertName, tc.timeout)

			if tc.expectError {
				if err == nil {
					t.Fatalf("Expected error containing %q, got nil", tc.errorContains)
				}
				if tc.errorContains != "" && !contains(err.Error(), tc.errorContains) {
					t.Errorf("Expected error containing %q, got %q", tc.errorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
			}
		})
	}
}

func TestWaitForCertificateReady_EventuallyReady(t *testing.T) {
	// Test that certificate becomes ready after initial not-ready state
	scheme := runtime.NewScheme()

	// Start with not-ready certificate
	notReadyCert := createCertificate("test-ns", "test-cert", "test-secret", false, true, "Issuing", "Being issued")
	readyCert := createCertificate("test-ns", "test-cert", "test-secret", true, false, "", "")
	secret := createSecret("test-ns", "test-secret", true, true, true, false)

	fakeClient := fake.NewSimpleDynamicClient(scheme, notReadyCert, secret)

	// Setup reactor to change certificate state after a few calls
	callCount := int32(0)
	fakeClient.PrependReactor("get", "certificates", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		count := atomic.AddInt32(&callCount, 1)
		if count >= 3 {
			// After 3 calls, return ready certificate
			return true, readyCert, nil
		}
		// First 2 calls return not-ready certificate
		return true, notReadyCert, nil
	})

	ctx := context.Background()
	err := WaitForCertificateReady(ctx, fakeClient, "test-ns", "test-cert", 10*time.Second)
	if err != nil {
		t.Errorf("Expected certificate to eventually become ready, got error: %v", err)
	}
}

func TestWaitForCertificateReady_SecretEventuallyCreated(t *testing.T) {
	// Test that secret is created after certificate is ready
	scheme := runtime.NewScheme()

	readyCert := createCertificate("test-ns", "test-cert", "test-secret", true, false, "", "")
	secret := createSecret("test-ns", "test-secret", true, true, true, false)

	fakeClient := fake.NewSimpleDynamicClient(scheme, readyCert)

	// Setup reactor to create secret after a few calls
	callCount := int32(0)
	fakeClient.PrependReactor("get", "secrets", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		count := atomic.AddInt32(&callCount, 1)
		if count >= 3 {
			// After 3 calls, return the secret
			return true, secret, nil
		}
		// First 2 calls return not found error
		return true, nil, errors.NewNotFound(schema.GroupResource{Group: "", Resource: "secrets"}, "test-secret")
	})

	ctx := context.Background()
	err := WaitForCertificateReady(ctx, fakeClient, "test-ns", "test-cert", 10*time.Second)
	if err != nil {
		t.Errorf("Expected secret to eventually be created, got error: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && containsHelper(s, substr)))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
