package v1alpha1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Kueue is the CRD to represent the Kueue operator
// This CRD defines the configuration that the Kueue
// Compatibility level 4: No compatibility is provided, the API can change at any point for any reason. These capabilities should not be used by applications needing long term support.
// +openshift:compatibility-gen:level=4
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=kueue,scope=Cluster
// +k8s:openapi-gen=true
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'cluster'",message="Kueue is a singleton, .metadata.name must be 'cluster'"
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
	// config is the desired configuration
	// for the Kueue operator.
	// +required
	Config KueueConfiguration `json:"config"`
}

type KueueConfiguration struct {
	// integrations is a required field that configures the Kueue's workload integrations.
	// Kueue has both standard integrations, known as job frameworks, and external integrations
	// known as external frameworks.
	// Kueue will only manage workloads that correspond to the specified integrations.
	// +required
	Integrations Integrations `json:"integrations"`
	// workloadManagement controls how kueue manages workloads.
	// By default Kueue will manage workloads that have a queue-name label.
	// Workloads that are missing the queue-name will be ignored by Kueue.
	// This field is optional.
	// If this field is not specified, Kueue will only manage workloads
	// that have the queue-name label.
	// +optional
	WorkloadManagement *WorkloadManagement `json:"workloadManagement,omitempty"`
	// gangScheduling controls how Kueue admits workloads.
	// Gang Scheduling is the act of all or nothing scheduling,
	// where workloads do not become ready within a certain period, they may be evicted and later retried.
	// This field is optional.
	// If this field is not specified, gang scheduling will be disabled.
	// +optional
	GangScheduling *GangScheduling `json:"gangScheduling,omitempty"`
	// preemption is the process of evicting one or more admitted Workloads to accommodate another Workload.
	// Kueue has classical premption and preemption via fair sharing.
	// This field is optional.
	// If this field is not specified, preemption will be set to Classical.
	// +optional
	Preemption *Preemption `json:"preemption,omitempty"`
}

// KueueStatus defines the observed state of Kueue
type KueueStatus struct {
	operatorv1.OperatorStatus `json:",inline"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KueueList contains a list of Kueue
//
// Compatibility level 4: No compatibility is provided, the API can change at any point for any reason. These capabilities should not be used by applications needing long term support.
// +openshift:compatibility-gen:level=4
type KueueList struct {
	metav1.TypeMeta `json:",inline"`
	// metadata for the list
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// items is a slice of Kueue
	// this is a cluster scoped resource and there can only be 1 Kueue
	// +kubebuilder:validation:MaxItems=1
	// +required
	Items []Kueue `json:"items"`
}

// +kubebuilder:validation:Enum=BatchJob;RayJob;RayCluster;JobSet;MPIJob;PaddleJob;PyTorchJob;TFJob;XGBoostJob;AppWrapper;Pod;Deployment;StatefulSet;LeaderWorkerSet
type KueueIntegration string

const (
	KueueIntegrationBatchJob        KueueIntegration = "BatchJob"
	KueueIntegrationRayJob          KueueIntegration = "RayJob"
	KueueIntegrationRayCluster      KueueIntegration = "RayCluster"
	KueueIntegrationJobSet          KueueIntegration = "JobSet"
	KueueIntegrationMPIJob          KueueIntegration = "MPIJob"
	KueueIntegrationPaddleJob       KueueIntegration = "PaddleJob"
	KueueIntegrationPyTorchJob      KueueIntegration = "PyTorchJob"
	KueueIntegrationTFJob           KueueIntegration = "TFJob"
	KueueIntegrationXGBoostJob      KueueIntegration = "XGBoostJob"
	KueueIntegrationAppWrapper      KueueIntegration = "AppWrapper"
	KueueIntegrationPod             KueueIntegration = "Pod"
	KueueIntegrationDeployment      KueueIntegration = "Deployment"
	KueueIntegrationStatefulSet     KueueIntegration = "StatefulSet"
	KueueIntegrationLeaderWorkerSet KueueIntegration = "LeaderWorkerSet"
)

// This is the GVR for an external framework.
// Controller runtime requires this in this format
// for api discoverability.
type ExternalFramework struct {
	// group is the API group of the externalFramework.
	// Must be a valid DNS 1123 subdomain consisting of of lower-case alphanumeric characters,
	// hyphens and periods, of at most 253 characters in length.
	// Each period separated segment within the subdomain must start and end with an alphanumeric character.
	// We are using matches and not cel functions to allow for use on 4.17.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self.matches(r'^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$') && self.size() <= 253"
	// +required
	Group string `json:"group"`
	// resource is the Resource type of the external framework.
	// Resource types are lowercase and plural (e.g. pods, deployments).
	// Must be a valid DNS 1123 label consisting of a lower-case alphanumeric string
	// and hyphens of at most 63 characters in length.
	// The value must start and end with an alphanumeric character.
	// We are using matches and not cel functions to allow for use on 4.17.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self.matches(r'^[a-z0-9]([-a-z0-9]*[a-z0-9])?$') && self.size() <= 63"
	// +required
	Resource string `json:"resource"`
	// version is the version of the api (e.g. v1alpha1, v1beta1, v1).
	// Must be a valid DNS 1035 label consisting of a lower-case alphanumeric string
	// and hyphens of at most 63 characters in length.
	// The value must start with an alphabetic character and end with an alphanumeric character.
	// We are using matches and not cel functions to allow for use on 4.17.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self.matches(r'^[a-z0-9]([-a-z0-9]*[a-z0-9])?$') && self.size() <= 63"
	// +required
	Version string `json:"version"`
}

// This is the integrations for Kueue.
// Kueue uses these apis to determine
// which jobs will be managed by Kueue.
type Integrations struct {
	// frameworks are a unique list of names to be enabled.
	// This is required and must have at least one element.
	// Each framework represents a type of job that Kueue will manage.
	// Frameworks are a list of frameworks that Kueue has support for.
	// The allowed values are BatchJob, RayJob, RayCluster, JobSet, MPIJob, PaddleJob, PytorchJob, TFJob, XGBoostJob, AppWrapper, Pod, Deployment, StatefulSet and LeaderWorkerSet.
	// +kubebuilder:validation:MaxItems=14
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:XValidation:rule="self.all(x, self.exists_one(y, x == y))",message="each item in frameworks must be unique"
	// +listType=atomic
	// +required
	Frameworks []KueueIntegration `json:"frameworks"`
	// externalFrameworks are a list of GroupVersionResources
	// that are managed for Kueue by external controllers.
	// These are optional and should only be used if you have an external controller
	// that integrates with Kueue.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=32
	// +optional
	ExternalFrameworks []ExternalFramework `json:"externalFrameworks,omitempty"`
	// labelKeysToCopy are a list of label keys that are copied once a workload is created.
	// These keys are persisted to the internal Kueue workload object.
	// If not specified, only the Kueue labels will be copied.
	// +kubebuilder:validation:MaxItems=64
	// +listType=set
	// +listMapType=key
	// +optional
	LabelKeysToCopy []LabelKeys `json:"labelKeysToCopy,omitempty"`
}

type LabelKeys struct {
	// key is the label key.
	// A label key must be a valid qualified name consisting of a lower-case alphanumeric string,
	// and hyphens of at most 63 characters in length.
	// We are using matches and not cel functions to allow for use on 4.17.
	// The name must start and end with an alphanumeric character.
	// The name may be optionally prefixed with a subdomain consisting of lower-case alphanumeric characters,
	// hyphens and periods, of at most 253 characters in length.
	// Each period separated segment within the subdomain must start and end with an alphanumeric character.
	// The optional prefix and the name are separate by a forward slash (/).
	// +kubebuilder:validation:MaxLength=317
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self.matches(r'^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?([a-z0-9]([-a-z0-9]*[a-z0-9])?)$') && self.size() <= 317"
	// +required
	Key string `json:"key"`
}

// +kubebuilder:validation:Enum=ByWorkload;Disabled
type GangSchedulingPolicy string

const (
	GangSchedulingPolicyByWorkload GangSchedulingPolicy = "ByWorkload"
	GangSchedulingPolicyDisabled   GangSchedulingPolicy = "Disabled"
)

// +kubebuilder:validation:Enum=Parallel;Sequential
type GangSchedulingWorkloadAdmission string

const (
	GangSchedulingWorkloadAdmissionSequential GangSchedulingWorkloadAdmission = "Sequential"
	GangSchedulingWorkloadAdmissionParallel   GangSchedulingWorkloadAdmission = "Parallel"
)

// Kueue provides the ability to admit workloads all in one (gang admission)
// and evicts workloads if they are not ready within a specific time.
// +kubebuilder:validation:XValidation:rule="has(self.policy) && self.policy == 'ByWorkload' ?  has(self.byWorkload) : !has(self.byWorkload)",message="byWorkload is required when policy is byWorkload, and forbidden otherwise"
// +union
type GangScheduling struct {
	// policy allows you to enable and configure gang scheduling.
	// This is an optional field.
	// The allowed values are ByWorkload and Disabled.
	// The default value will be Disabled.
	// When set to ByWorkload, this means each workload is processed and considered
	// for admission as a single unit.
	// Where workloads do not become ready over time, the entire workload may then be evicted and retried at a later time.
	// +required
	// +unionDiscriminator
	Policy GangSchedulingPolicy `json:"policy"`
	// byWorkload controls how admission is done.
	// byWorkload is only required if policy is equal to ByWorkload.
	// +optional
	ByWorkload *ByWorkload `json:"byWorkload,omitempty"`
}

// ByWorkload controls how admission is done
type ByWorkload struct {
	// admission controls how kueue will process workloads.
	// When admission is set to Sequential, only pods from the currently processing workload will be admitted.
	// Once all pods from the current workload are admitted, and ready, Kueue will process the next workload.
	// Sequential processing may slow down admission when the cluster has sufficient capacity for multiple workloads,
	// but provides a higher guarantee of workloads scheduling all pods together successfully.
	// When set to Parallel, pods from any workload will be admitted at any time.
	// This may lead to a deadlock where workloads are in contention for cluster capacity and
	// pods from another workload having successfully scheduled prevent pods from the current workload scheduling.
	// +optional
	Admission *GangSchedulingWorkloadAdmission `json:"admission,omitempty"`
}

// +kubebuilder:validation:Enum=QueueName;None
type LabelPolicy string

const (
	LabelPolicyQueueName LabelPolicy = "QueueName"
	LabelPolicyNone      LabelPolicy = "None"
)

type WorkloadManagement struct {
	// labelPolicy controls whether or not Kueue reconciles
	// jobs that don't set the label kueue.x-k8s.io/queue-name.
	// The allowed values are QueueName and None.
	// None means that workloads will be suspended on
	// creation and a label will be added via a mutating webhook.
	// This will be applied for all integrations that Kueue manages.
	// QueueName means that workloads that are managed
	// by Kueue must have a label kueue.x-k8s.io/queue-name.
	// If this label is not present on the workload, then Kueue will
	// ignore this workload.
	// +required
	LabelPolicy LabelPolicy `json:"labelPolicy"`
}

// +kubebuilder:validation:Enum=Classical;FairSharing
type PreemptionPolicy string

const (
	PreemptionStrategyClassical   PreemptionPolicy = "Classical"
	PreemptionStrategyFairsharing PreemptionPolicy = "FairSharing"
)

type Preemption struct {
	// preemptionPolicy are the types of preemption kueue allows.
	// Kueue has two types of preemption: Classical and FairSharing.
	// Classical means that an incoming workload, which does
	// not fit within the unusued quota, is eligible to issue preemptions
	// when the requests of the workload are below the
	// resource flavor's nominal quota or borrowWithinCohort is enabled
	// on the Cluster Queue.
	// FairSharing means that ClusterQueues with pending Workloads can preempt other Workloads
	// in their cohort until the preempting ClusterQueue
	// obtains an equal or weighted share of the borrowable resources.
	// The borrowable resources are the unused nominal quota
	// of all the ClusterQueues in the cohort.
	// FairSharing is a more heavy weight algorithm.
	// The default is Classical.
	// +optional
	PreemptionPolicy *PreemptionPolicy `json:"preemptionPolicy,omitempty"`
}
