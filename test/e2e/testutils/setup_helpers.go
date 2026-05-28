package testutils

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
)

var testClients *TestClients

func InitClients(c *TestClients) {
	testClients = c
}

// TestEnvironment holds the Kueue test resources created by SetupTestEnvironment.
type TestEnvironment struct {
	ResourceFlavor *kueuev1beta2.ResourceFlavor
	ClusterQueue   *kueuev1beta2.ClusterQueue
	Namespace      *corev1.Namespace
	LocalQueue     *kueuev1beta2.LocalQueue
}

// MustCreateResourceFlavor creates a ResourceFlavor with GenerateName and
// registers cleanup via DeferCleanup.
func MustCreateResourceFlavor(ctx context.Context) *kueuev1beta2.ResourceFlavor {
	rf, cleanup, err := NewResourceFlavor().WithGenerateName().CreateWithObject(ctx, testClients.UpstreamKueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create ResourceFlavor")
	DeferCleanup(cleanup)
	return rf
}

// MustCreateClusterQueue creates a ClusterQueue with GenerateName and the given
// flavor name. If cq is non-nil, the caller's customizations are preserved and
// GenerateName + FlavorName are applied on top. If cq is nil, defaults are used.
// Registers cleanup via DeferCleanup.
func MustCreateClusterQueue(ctx context.Context, flavorName string, cq *ClusterQueueWrapper) *kueuev1beta2.ClusterQueue {
	if cq == nil {
		cq = NewClusterQueue()
	}
	cq.WithGenerateName().WithFlavorName(flavorName)
	created, cleanup, err := cq.CreateWithObject(ctx, testClients.UpstreamKueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterQueue")
	DeferCleanup(cleanup)
	return created
}

// MustCreateManagedNamespace creates a namespace with GenerateName and the
// kueue.openshift.io/managed=true label. Registers cleanup via DeferCleanup.
func MustCreateManagedNamespace(ctx context.Context, prefix string) *corev1.Namespace {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: prefix,
			Labels:       map[string]string{OpenShiftManagedLabel: "true"},
		},
	}
	cleanup, err := CreateNamespace(testClients.KubeClient, ns)
	Expect(err).NotTo(HaveOccurred(), "Failed to create Namespace")
	DeferCleanup(cleanup)
	return ns
}

// MustCreateLocalQueue creates a LocalQueue in the given namespace, pointing
// to the given ClusterQueue. If lq is non-nil, the caller's customizations are
// preserved and ClusterQueue is applied on top. If lq is nil, defaults are used.
// Registers cleanup via DeferCleanup.
func MustCreateLocalQueue(ctx context.Context, namespace, name, clusterQueueName string, lq *LocalQueueWrapper) *kueuev1beta2.LocalQueue {
	if lq == nil {
		lq = NewLocalQueue(namespace, name)
	} else {
		lq.Namespace = namespace
		lq.Name = name
	}
	lq.WithClusterQueue(clusterQueueName)
	created, cleanup, err := lq.CreateWithObject(ctx, testClients.UpstreamKueueClient)
	Expect(err).NotTo(HaveOccurred(), "Failed to create LocalQueue")
	DeferCleanup(cleanup)
	return created
}

// SetupTestEnvironment creates a standard set of Kueue test resources:
// ResourceFlavor, ClusterQueue, Namespace, and LocalQueue.
// If cq is non-nil, the caller's CQ customizations are applied.
// GenerateName and FlavorName are always set by the helper — do not pre-set them on the wrapper.
// All resources are registered with DeferCleanup for automatic teardown.
func SetupTestEnvironment(ctx context.Context, nsPrefix, lqName string, cq *ClusterQueueWrapper) *TestEnvironment {
	Expect(testClients).NotTo(BeNil(), "testClients is nil — call testutils.InitClients() in BeforeSuite before using setup helpers")
	By("Creating ResourceFlavor, ClusterQueue, Namespace and LocalQueue")

	rf := MustCreateResourceFlavor(ctx)
	createdCQ := MustCreateClusterQueue(ctx, rf.Name, cq)
	ns := MustCreateManagedNamespace(ctx, nsPrefix)
	lq := MustCreateLocalQueue(ctx, ns.Name, lqName, createdCQ.Name, nil)

	return &TestEnvironment{
		ResourceFlavor: rf,
		ClusterQueue:   createdCQ,
		Namespace:      ns,
		LocalQueue:     lq,
	}
}
