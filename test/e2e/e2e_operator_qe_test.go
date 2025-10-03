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
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/yaml"
)

var (
	namespaceName string
	resource      *unstructured.Unstructured // Global object to store the latest created resource
)

var _ = Describe("QETest", Ordered, func() {

	AfterAll(func(ctx context.Context) {
		deleteNamespace(ctx, ns)
		deleteClusterQueueAndResourceFlavor(ctx, kueueClient)
		testutils.CleanUpKueuInstance(ctx, clients.KueueClient, "cluster")
	})

	When("Kueue Operator - QE test", func() {
		It("LocalQueueDefaulting should label and admit Job and Pod", func(ctx context.Context) {
			kueueClient = clients.UpstreamKueueClient
			Expect(deployOperand()).To(Succeed(), "operand deployment should not fail")
			kueueInstance, err := clients.KueueClient.KueueV1().Kueues().Get(ctx, "cluster", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "Failed to fetch Kueue instance")
			kueueInstance.Spec.Config.WorkloadManagement.LabelPolicy = kueueoperatorv1.LabelPolicyNone
			applyKueueConfig(ctx, kueueInstance.Spec.Config, kubeClient)
			createResource("assets/01_namespace.yaml")
			verifyResourceExists("Namespace", namespaceName, "")
			createResource("assets/10_resource_flavor.yaml")
			verifyResourceExists("ResourceFlavor", "default", "")
			createResource("assets/09_cluster_queue.yaml")
			verifyResourceExists("ClusterQueue", "test-clusterqueue", "")
			createResource("assets/11_local_queue.yaml")
			verifyResourceExists("LocalQueue", "default", namespaceName)
			createResource("assets/12_job.yaml")
			verifyResourceExists("Job", "kueuejob2", namespaceName)
			Expect(resource.GetLabels()).To(HaveKeyWithValue(testutils.QueueLabel, testutils.DefaultLocalQueueName))
			By(fmt.Sprintf("resource.GetLabels(): %s", resource.GetLabels()))
			verifyWorkloadCreated(kueueClient, ns.Name, string(resource.GetUID()))
			createResource("assets/13_pod.yaml")
			verifyResourceExists("Pod", "pod1", namespaceName)
			Expect(resource.GetLabels()).To(HaveKeyWithValue(testutils.QueueLabel, testutils.DefaultLocalQueueName))
			By(fmt.Sprintf("resource.GetLabels(): %s", resource.GetLabels()))
			verifyWorkloadCreated(kueueClient, ns.Name, string(resource.GetUID()))
		})
	})

})

func createResource(assetPath string) {
	By(fmt.Sprintf("Applying yaml file: %v", assetPath))
	yamlBytes := bindata.MustAsset(assetPath)
	resource = &unstructured.Unstructured{}

	err := yaml.Unmarshal(yamlBytes, resource)
	Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Failed to unmarshal YAML from %s: %v", assetPath, err))

	if resource.GetKind() == "Namespace" {
		randomSuffix := rand.String(5)
		namespaceName = fmt.Sprintf("e2e-kueue-%s", randomSuffix)
		resource.SetName(namespaceName)
		ns = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespaceName,
			},
		}
	}

	if resource.GetKind() == "LocalQueue" || resource.GetKind() == "Job" || resource.GetKind() == "Pod" {
		resource.SetNamespace(namespaceName)
	}

	err = clients.GenericClient.Create(context.Background(), resource)
	Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Failed to create %s: %v", resource.GetKind(), err))
}

func verifyResourceExists(kind, name, namespace string) {
	Expect(resource.GetKind()).To(Equal(kind), "Resource kind mismatch")
	Expect(resource.GetName()).To(Equal(name), "Resource name mismatch")
	if namespace != "" {
		Expect(resource.GetNamespace()).To(Equal(namespace), "Resource namespace mismatch")
		By(fmt.Sprintf("Verified %s exists: %s/%s", kind, namespace, name))
	} else {
		By(fmt.Sprintf(" Verified %s exists: %s", kind, name))
	}
}
