package namespace

import "os"

const (
	podNamespaceEnv   = "POD_NAMESPACE"
	operatorNamespace = "openshift-kueue-operator"
)

// getNamespace returns in-cluster namespace
func GetNamespace() string {
	if nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		return string(nsBytes)
	}
	if podNamespace := os.Getenv(podNamespaceEnv); len(podNamespace) > 0 {
		return podNamespace
	}
	return operatorNamespace
}
