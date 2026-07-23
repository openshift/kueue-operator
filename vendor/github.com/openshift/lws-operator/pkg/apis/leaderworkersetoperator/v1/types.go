package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorv1 "github.com/openshift/api/operator/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LeaderWorkerSetOperator is the Schema for the LeaderWorkerSetOperator API
// +k8s:openapi-gen=true
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'cluster'",message="LeaderWorkerSetOperator is a singleton, .metadata.name must be 'cluster'"
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

	// nodePlacement provides explicit control over the scheduling of lws-controller-manager pods.
	//
	// If unset, the operator does not inject nodeSelector or tolerations beyond the upstream operand manifest.
	//
	// When set, each specified field within nodePlacement replaces the corresponding field on the
	// operand deployment pod template. Omitted fields within nodePlacement leave the upstream
	// operand manifest values unchanged.
	//
	// +optional
	NodePlacement *NodePlacement `json:"nodePlacement,omitempty"`
}

// NodePlacement describes node scheduling configuration for lws-controller-manager pods.
type NodePlacement struct {
	// nodeSelector is the node selector applied to lws-controller-manager pods.
	//
	// If set, the specified selector replaces the nodeSelector on the operand deployment pod template.
	//
	// If unset, the operand deployment keeps any nodeSelector from the upstream manifest.
	//
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// tolerations is a list of tolerations applied to lws-controller-manager pods.
	//
	// If set, the specified tolerations replace tolerations on the operand deployment pod template.
	//
	// If unset, the operand deployment keeps any tolerations from the upstream manifest.
	//
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
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
