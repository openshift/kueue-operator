package operator

import (
	"context"
	"time"

	openshiftrouteclientset "github.com/openshift/client-go/route/clientset/versioned"
	operatorconfigclient "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned"
	operatorclientinformers "github.com/openshift/kueue-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/kueue-operator/pkg/namespace"
	"github.com/openshift/kueue-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/loglevel"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	workQueueKey          = "key"
	workQueueCMChangedKey = "CMkey"
)

type queueItem struct {
	kind string
	name string
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

	kubeInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(kubeClient,
		"",
		namespace.GetNamespace(),
	)

	operatorConfigClient, err := operatorconfigclient.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}
	operatorConfigInformers := operatorclientinformers.NewSharedInformerFactory(operatorConfigClient, 10*time.Minute)
	kueueClient := &operatorclient.KueueClient{
		Ctx:            ctx,
		SharedInformer: operatorConfigInformers.Kueue().V1alpha1().Kueues().Informer(),
		OperatorClient: operatorConfigClient.KueueV1alpha1(),
	}

	osrClient, err := openshiftrouteclientset.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	crdClient, err := apiextv1.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	targetConfigReconciler, err := NewTargetConfigReconciler(
		ctx,
		operatorConfigClient.KueueV1alpha1(),
		operatorConfigInformers.Kueue().V1alpha1().Kueues(),
		kubeInformersForNamespaces,
		kueueClient,
		kubeClient,
		osrClient,
		dynamicClient,
		crdClient,
		cc.EventRecorder,
	)
	if err != nil {
		return err
	}

	logLevelController := loglevel.NewClusterOperatorLoggingController(kueueClient, cc.EventRecorder)

	klog.Infof("Starting informers")
	operatorConfigInformers.Start(ctx.Done())
	kubeInformersForNamespaces.Start(ctx.Done())

	klog.Infof("Starting log level controller")
	go logLevelController.Run(ctx, 1)
	klog.Infof("Starting target config reconciler")
	go targetConfigReconciler.Run(1, ctx.Done())

	<-ctx.Done()
	return nil
}
