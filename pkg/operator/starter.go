package operator

import (
	"context"
	"os"
	"reflect"
	"time"

	openshiftrouteclientset "github.com/openshift/client-go/route/clientset/versioned"
	operatorconfigclient "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned"
	operatorclientinformers "github.com/openshift/kueue-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/kueue-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/loglevel"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	apiextclientsetv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apiextinformer "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	"k8s.io/client-go/informers"
	"k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset"
	apiregistrationv1client "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"
	apiregistrationinformers "k8s.io/kube-aggregator/pkg/client/informers/externalversions"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type queueItem struct {
	kind string
	name string
}

// stripStatusTransform removes the status field from objects to prevent
// status-only changes from triggering reconciliation loops.
// This is critical for resources like APIService, FlowSchema, and
// PriorityLevelConfiguration whose status changes frequently.
func stripStatusTransform(obj interface{}) (interface{}, error) {
	// Use reflection to find and clear the Status field
	v := reflect.ValueOf(obj)

	// Handle pointer types
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	// Only process struct types
	if v.Kind() != reflect.Struct {
		return obj, nil
	}

	// Look for a field named "Status"
	statusField := v.FieldByName("Status")
	if statusField.IsValid() && statusField.CanSet() {
		// Set the Status field to its zero value
		statusField.Set(reflect.Zero(statusField.Type()))
	}

	return obj, nil
}

func RunOperator(ctx context.Context, cc *controllercmd.ControllerContext) error {
	kubeClient, err := kubernetes.NewForConfig(cc.ProtoKubeConfig)
	if err != nil {
		return err
	}

	dynamicClient, err := dynamic.NewForConfig(cc.ProtoKubeConfig)
	if err != nil {
		return err
	}

	kubeInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(kubeClient, "", cc.OperatorNamespace)

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	operatorConfigClient, err := operatorconfigclient.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}
	operatorConfigInformers := operatorclientinformers.NewSharedInformerFactory(operatorConfigClient, 10*time.Minute)
	kueueClient := &operatorclient.KueueClient{
		Ctx:            ctx,
		SharedInformer: operatorConfigInformers.Kueue().V1().Kueues().Informer(),
		OperatorClient: operatorConfigClient.KueueV1(),
	}

	osrClient, err := openshiftrouteclientset.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	crdClient, err := apiextv1.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	crdClientSet, err := apiextclientsetv1.NewForConfig(cc.ProtoKubeConfig)
	if err != nil {
		return err
	}

	apiRegistrationClient, err := apiregistrationv1client.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	// Apply status strip transform to prevent status-only changes from triggering reconciliation
	crdInformer := apiextinformer.NewSharedInformerFactoryWithOptions(
		crdClientSet,
		10*time.Minute,
		apiextinformer.WithTransform(stripStatusTransform),
	)
	apiregistrationInformer := apiregistrationinformers.NewSharedInformerFactoryWithOptions(
		clientset.NewForConfigOrDie(cc.KubeConfig),
		5*time.Minute,
		apiregistrationinformers.WithTransform(stripStatusTransform),
	)

	kubeInformer := informers.NewSharedInformerFactoryWithOptions(
		kubeClient,
		5*time.Minute,
		informers.WithTransform(stripStatusTransform),
	)

	targetConfigReconciler, err := NewTargetConfigReconciler(
		ctx,
		operatorConfigClient.KueueV1(),
		operatorConfigInformers.Kueue().V1().Kueues(),
		kubeInformersForNamespaces,
		kueueClient,
		kubeClient,
		osrClient,
		dynamicClient,
		discoveryClient,
		crdClient,
		apiRegistrationClient,
		crdInformer,
		apiregistrationInformer,
		kubeInformer,
		cc.EventRecorder,
		os.Getenv("RELATED_IMAGE_OPERAND_IMAGE"),
	)
	if err != nil {
		return err
	}

	logLevelController := loglevel.NewClusterOperatorLoggingController(kueueClient, cc.EventRecorder)

	klog.Infof("Starting informers")
	operatorConfigInformers.Start(ctx.Done())
	kubeInformersForNamespaces.Start(ctx.Done())
	crdInformer.Start(ctx.Done())
	apiregistrationInformer.Start(ctx.Done())
	kubeInformer.Start(ctx.Done())

	klog.Infof("Starting log level controller")
	go logLevelController.Run(ctx, 1)
	klog.Infof("Starting target config reconciler")
	go targetConfigReconciler.Run(ctx, 1)

	<-ctx.Done()
	return nil
}
