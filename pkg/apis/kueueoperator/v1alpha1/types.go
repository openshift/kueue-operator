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
	metav1.TypeMeta `json:",inline"`
	// metadata for kueue
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec holds user settable values for configuration
	// +required
	Spec KueueOperandSpec `json:"spec"`
	// status holds observed values from the cluster. They may not be overridden.
	// +optional
	Status KueueStatus `json:"status,omitempty"`
}

type KueueOperandSpec struct {
	operatorv1.OperatorSpec `json:",inline"`
	// config that is persisted to a config map
	// +required
	Config KueueConfiguration `json:"config"`
}

type ManageJobsWithoutQueueNameOption string

const (
	// NoQueueName means that all jobs will be gated by Kueue
	NoQueueName ManageJobsWithoutQueueNameOption = "NoQueueName"
	// QueueName means that the jobs require a queue label.
	QueueName ManageJobsWithoutQueueNameOption = "QueueName"
)

type KueueConfiguration struct {
	// waitForPodsReady configures gang admission
	// +optional
	WaitForPodsReady *configapi.WaitForPodsReady `json:"waitForPodsReady,omitempty"`
	// integrations are the types of integrations Kueue will manager
	// +required
	Integrations configapi.Integrations `json:"integrations"`
	// featureGates are advanced features for Kueue
	// +optional
	FeatureGates map[string]bool `json:"featureGates,omitempty"`
	// resources provides additional configuration options for handling the resources.
	// Supports https://github.com/kubernetes-sigs/kueue/blob/release-0.10/keps/2937-resource-transformer/README.md
	// +optional
	Resources *configapi.Resources `json:"resources,omitempty"`
	// ManageJobsWithoutQueueName controls whether or not Kueue reconciles
	// jobs that don't set the annotation kueue.x-k8s.io/queue-name.
	// Allowed values are NoQueueName and QueueName
	// Default will be QueueName
	// +optional
	ManageJobsWithoutQueueName *ManageJobsWithoutQueueNameOption `json:"manageJobsWithoutQueueName,omitempty"`
	// ManagedJobsNamespaceSelector can be used to omit some namespaces from ManagedJobsWithoutQueueName
	// +optional
	ManagedJobsNamespaceSelector *metav1.LabelSelector `json:"managedJobsNamespaceSelector,omitempty"`
	// FairSharing controls the fair sharing semantics across the cluster.
	FairSharing *configapi.FairSharing `json:"fairSharing,omitempty"`
}

// KueueStatus defines the observed state of Kueue
type KueueStatus struct {
	operatorv1.OperatorStatus `json:",inline"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KueueList contains a list of Kueue
type KueueList struct {
	metav1.TypeMeta `json:",inline"`
	// metadata for the list
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// items is a slice of kueue
	// +required
	Items []Kueue `json:"items"`
}
