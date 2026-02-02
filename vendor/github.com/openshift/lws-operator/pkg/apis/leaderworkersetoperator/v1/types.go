package v1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LeaderWorkerSetOperator is the Schema for the LeaderWorkerSetOperator API
// +k8s:openapi-gen=true
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
type LeaderWorkerSetOperator struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec holds user settable values for configuration
	// +required
	Spec LeaderWorkerSetOperatorSpec `json:"spec"`
	// status holds observed values from the cluster. They may not be overridden.
	// +optional
	Status LeaderWorkerSetOperatorStatus `json:"status"`
}

// LeaderWorkerSetOperatorSpec defines the desired state of LeaderWorkerSetOperator
type LeaderWorkerSetOperatorSpec struct {
	operatorv1.OperatorSpec `json:",inline"`
}

// LeaderWorkerSetOperatorStatus defines the observed state of LeaderWorkerSetOperator
type LeaderWorkerSetOperatorStatus struct {
	operatorv1.OperatorStatus `json:",inline"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LeaderWorkerSetOperatorList contains a list of LeaderWorkerSetOperator
type LeaderWorkerSetOperatorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LeaderWorkerSetOperator `json:"items"`
}
