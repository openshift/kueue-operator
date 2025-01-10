package v1alpha1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	configapi "sigs.k8s.io/kueue/apis/config/v1beta1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Kueue is the Schema for the kueue API
// +k8s:openapi-gen=true
// +genclient
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
type Kueue struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec holds user settable values for configuration
	// +required
	Spec KueueOperandSpec `json:"spec"`
	// status holds observed values from the cluster. They may not be overridden.
	// +optional
	Status KueueStatus `json:"status"`
}

type KueueOperandSpec struct {
	operatorv1.OperatorSpec `json:",inline"`
	// The config that is persisted to a config map
	Config KueueConfiguration `json:"config"`
	// Image
	Image string `json:"image"`
}

type KueueConfiguration struct {
	// WaitForPodsReady configures gang admission
	// +optional
	WaitForPodsReady *configapi.WaitForPodsReady `json:"waitForPodsReady,omitempty"`
	// Integrations are the types of integrations Kueue will manager
	// Required
	Integrations configapi.Integrations `json:"integrations"`
	// Feature gates are advanced features for Kueue
	// +optional
	FeatureGates map[string]bool `json:"featureGates,omitempty"`
	// Resources provides additional configuration options for handling the resources.
	// Supports https://github.com/kubernetes-sigs/kueue/blob/release-0.10/keps/2937-resource-transformer/README.md
	// +optional
	Resources *configapi.Resources `json:"resources,omitempty"`
}

// KueueStatus defines the observed state of Kueue
type KueueStatus struct {
	operatorv1.OperatorStatus `json:",inline"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KueueList contains a list of Kueue
type KueueList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Kueue `json:"items"`
}
