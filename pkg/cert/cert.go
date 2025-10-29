package cert

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
)

func InjectCertAnnotation(annotation map[string]string, namespace string) map[string]string {
	newAnnotation := annotation
	if annotation == nil {
		newAnnotation = map[string]string{}
	}
	newAnnotation["cert-manager.io/inject-ca-from"] = fmt.Sprintf("%s/webhook-cert", namespace)
	return newAnnotation
}

// WaitForCertificateReady waits for a cert-manager Certificate to be ready
// by checking that it's issued and the Secret exists with valid certificate data
func WaitForCertificateReady(ctx context.Context, dynamicClient dynamic.Interface, namespace, certificateName string, timeout time.Duration) error {
	klog.V(4).Infof("Waiting for certificate %s/%s to be issued and ready (timeout: %v)", namespace, certificateName, timeout)

	certGVR := schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "certificates",
	}

	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for certificate %s/%s to be ready after %v", namespace, certificateName, timeout)
		}

		// Get the Certificate resource
		cert, err := dynamicClient.Resource(certGVR).Namespace(namespace).Get(ctx, certificateName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				klog.V(4).Infof("Certificate %s/%s not found yet, retrying...", namespace, certificateName)
				time.Sleep(2 * time.Second)
				continue
			}
			return fmt.Errorf("failed to get certificate %s/%s: %w", namespace, certificateName, err)
		}

		// Check the status conditions
		conditions, found, err := unstructured.NestedSlice(cert.Object, "status", "conditions")
		if err != nil || !found {
			klog.V(4).Infof("Certificate %s/%s has no status conditions yet, retrying...", namespace, certificateName)
			time.Sleep(2 * time.Second)
			continue
		}

		// Check multiple conditions to ensure certificate is fully ready
		ready := false
		issuing := false
		var notReadyReason, notReadyMessage string

		for _, conditionRaw := range conditions {
			condition, ok := conditionRaw.(map[string]interface{})
			if !ok {
				continue
			}

			condType, _, _ := unstructured.NestedString(condition, "type")
			condStatus, _, _ := unstructured.NestedString(condition, "status")
			condReason, _, _ := unstructured.NestedString(condition, "reason")
			condMessage, _, _ := unstructured.NestedString(condition, "message")

			switch condType {
			case "Ready":
				if condStatus == "True" {
					ready = true
				} else {
					notReadyReason = condReason
					notReadyMessage = condMessage
				}
			case "Issuing":
				if condStatus == "True" {
					issuing = true
				}
			}
		}

		// Certificate is being issued - log and continue waiting
		if issuing {
			klog.V(4).Infof("Certificate %s/%s is currently being issued, waiting...", namespace, certificateName)
			time.Sleep(2 * time.Second)
			continue
		}

		// Certificate is not ready - log reason and continue
		if !ready {
			if notReadyReason != "" {
				klog.V(4).Infof("Certificate %s/%s not ready: %s - %s", namespace, certificateName, notReadyReason, notReadyMessage)
			} else {
				klog.V(4).Infof("Certificate %s/%s not ready yet, retrying...", namespace, certificateName)
			}
			time.Sleep(2 * time.Second)
			continue
		}

		// Certificate reports Ready=True, now verify the Secret exists and has data
		secretName, found, err := unstructured.NestedString(cert.Object, "spec", "secretName")
		if err != nil || !found || secretName == "" {
			klog.V(4).Infof("Certificate %s/%s has no secretName in spec, retrying...", namespace, certificateName)
			time.Sleep(2 * time.Second)
			continue
		}

		// Verify the Secret exists and has certificate data
		secretGVR := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "secrets",
		}

		secret, err := dynamicClient.Resource(secretGVR).Namespace(namespace).Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				klog.V(4).Infof("Certificate %s/%s secret %s not found yet, retrying...", namespace, certificateName, secretName)
				time.Sleep(2 * time.Second)
				continue
			}
			return fmt.Errorf("failed to get certificate secret %s/%s: %w", namespace, secretName, err)
		}

		// Check that the secret has the required certificate data
		data, found, err := unstructured.NestedMap(secret.Object, "data")
		if err != nil || !found {
			klog.V(4).Infof("Certificate %s/%s secret %s has no data field, retrying...", namespace, certificateName, secretName)
			time.Sleep(2 * time.Second)
			continue
		}

		// Verify tls.crt and tls.key exist
		tlsCrt, hasCrt := data["tls.crt"]
		tlsKey, hasKey := data["tls.key"]
		_, hasCa := data["ca.crt"]

		if !hasCrt || !hasKey {
			klog.V(4).Infof("Certificate %s/%s secret %s missing tls.crt or tls.key, retrying...", namespace, certificateName, secretName)
			time.Sleep(2 * time.Second)
			continue
		}

		// Verify the data fields are not empty
		tlsCrtStr, ok1 := tlsCrt.(string)
		tlsKeyStr, ok2 := tlsKey.(string)
		if !ok1 || !ok2 || tlsCrtStr == "" || tlsKeyStr == "" {
			klog.V(4).Infof("Certificate %s/%s secret %s has empty certificate data, retrying...", namespace, certificateName, secretName)
			time.Sleep(2 * time.Second)
			continue
		}

		// All checks passed
		klog.V(4).Infof("Certificate %s/%s is ready and issued (secret: %s, has CA: %v)", namespace, certificateName, secretName, hasCa)
		return nil
	}
}
