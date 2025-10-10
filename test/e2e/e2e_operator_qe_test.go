package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	kueueoperatorv1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	"github.com/openshift/kueue-operator/test/e2e/bindata"
	"github.com/openshift/kueue-operator/test/e2e/testutils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

var _ = Describe("KueueOperatorQE", Ordered, func() {
	var namespaceName string

	AfterAll(func(ctx context.Context) {
		deleteNamespace(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}})
		deleteClusterQueueAndResourceFlavor(ctx, kueueClient)
		testutils.CleanUpKueuInstance(ctx, clients.KueueClient, "cluster")
	})

	When("LocalQueueDefaulting - Should label and admit Pod and Job in a managed namespace", func() {
		It("Should label and admit Job and Pod", func(ctx context.Context) {
			kueueClient = clients.UpstreamKueueClient
			Expect(deployOperand()).To(Succeed(), "operand deployment should not fail")
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
			kueueInstance.Spec.Config.WorkloadManagement.LabelPolicy = kueueoperatorv1.LabelPolicyNone
			applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)
			namespace := createResource("assets/qe/01_namespace.yaml")
			namespaceName = namespace.GetName()
			verifyResourceExists(namespace, "Namespace", namespaceName)
			resourceFlavor := createResource("assets/qe/10_resource_flavor.yaml")
			verifyResourceExists(resourceFlavor, "ResourceFlavor", "default")
			clusterQueue := createResource("assets/qe/09_cluster_queue.yaml")
			verifyResourceExists(clusterQueue, "ClusterQueue", "test-clusterqueue")
			localQueue := createResource("assets/qe/11_local_queue.yaml", namespaceName)
			verifyResourceExistsInNamespace(localQueue, "LocalQueue", "default", namespaceName)
			job := createResource("assets/qe/12_job.yaml", namespaceName)
			verifyResourceExistsInNamespace(job, "Job", "kueuejob2", namespaceName)
			Expect(job.GetLabels()).To(HaveKeyWithValue(testutils.QueueLabel, testutils.DefaultLocalQueueName))
			verifyWorkloadCreated(kueueClient, namespaceName, string(job.GetUID()))
			pod := createResource("assets/qe/13_pod.yaml", namespaceName)
			verifyResourceExistsInNamespace(pod, "Pod", "pod1", namespaceName)
			Expect(pod.GetLabels()).To(HaveKeyWithValue(testutils.QueueLabel, testutils.DefaultLocalQueueName))
			verifyWorkloadCreated(kueueClient, namespaceName, string(pod.GetUID()))
		})
	})

})

func createResource(assetPath string, namespaceName ...string) *unstructured.Unstructured {
	yamlBytes := bindata.MustAsset(assetPath)
	resource := &unstructured.Unstructured{}
	err := yaml.Unmarshal(yamlBytes, resource)
	Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Failed to unmarshal YAML from %s: %v", assetPath, err))
	if resource.GetKind() == "LocalQueue" || resource.GetKind() == "Job" || resource.GetKind() == "Pod" {
		Expect(namespaceName).NotTo(BeEmpty(), fmt.Sprintf("%s resource must have a namespace provided", resource.GetKind()))
		resource.SetNamespace(namespaceName[0])
	}
	err = clients.GenericClient.Create(context.Background(), resource)
	Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Failed to create %s: %v", resource.GetKind(), err))
	By(fmt.Sprintf("Created %s: %s", resource.GetKind(), resource.GetName()))
	return resource
}

func verifyResourceExistsInNamespace(resource *unstructured.Unstructured, kind, name, namespace string) {
	Expect(resource.GetKind()).To(Equal(kind), "Resource kind mismatch")
	Expect(resource.GetName()).To(Equal(name), "Resource name mismatch")
	Expect(resource.GetNamespace()).To(Equal(namespace), "Resource namespace mismatch")
}

func verifyResourceExists(resource *unstructured.Unstructured, kind, name string) {
	Expect(resource.GetKind()).To(Equal(kind), "Resource kind mismatch")
	Expect(resource.GetName()).To(Equal(name), "Resource name mismatch")
}
