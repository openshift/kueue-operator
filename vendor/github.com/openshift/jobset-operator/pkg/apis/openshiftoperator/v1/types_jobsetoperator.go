package v1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// JobSetOperator is the Schema for the jobsetoperator API
// +k8s:openapi-gen=true
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'cluster'",message="JobSetOperator is a singleton, .metadata.name must be 'cluster'"
type JobSetOperator struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec holds user settable values for configuration
	// +required
	Spec JobSetOperatorSpec `json:"spec"`
	// status holds observed values from the cluster. They may not be overridden.
	// +optional
	Status JobSetOperatorStatus `json:"status"`
}

// JobSetOperatorSpec defines the desired state of JobSetOperator
type JobSetOperatorSpec struct {
	operatorv1.OperatorSpec `json:",inline"`
}

// JobSetOperatorStatus defines the observed state of JobSetOperator
type JobSetOperatorStatus struct {
	operatorv1.OperatorStatus `json:",inline"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// JobSetOperatorList contains a list of JobSetOperator
type JobSetOperatorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []JobSetOperator `json:"items"`
}
