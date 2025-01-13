package operator

import (
	"context"
	"fmt"
	"strconv"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	openshiftrouteclientset "github.com/openshift/client-go/route/clientset/versioned"
	"github.com/openshift/kueue-operator/bindata"
	kueuev1alpha1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1alpha1"
	"github.com/openshift/kueue-operator/pkg/builders/configmap"
	kueueconfigclient "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned/typed/kueueoperator/v1alpha1"
	operatorclientinformers "github.com/openshift/kueue-operator/pkg/generated/informers/externalversions/kueueoperator/v1alpha1"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"

	"github.com/openshift/kueue-operator/pkg/operator/operatorclient"

	"github.com/openshift/library-go/pkg/controller"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

const (
	PromNamespace   = "openshift-monitoring"
	KueueConfigMap  = "kueue-manager-config"
	PromRouteName   = "prometheus-k8s"
	PromTokenPrefix = "prometheus-k8s-token"
)

type TargetConfigReconciler struct {
	ctx                        context.Context
	operatorClient             kueueconfigclient.KueueV1alpha1Interface
	kueueClient                *operatorclient.KueueClient
	kubeClient                 kubernetes.Interface
	osrClient                  openshiftrouteclientset.Interface
	dynamicClient              dynamic.Interface
	eventRecorder              events.Recorder
	queue                      workqueue.RateLimitingInterface
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces
}

func NewTargetConfigReconciler(
	ctx context.Context,
	operatorConfigClient kueueconfigclient.KueueV1alpha1Interface,
	operatorClientInformer operatorclientinformers.KueueInformer,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	kueueClient *operatorclient.KueueClient,
	kubeClient kubernetes.Interface,
	osrClient openshiftrouteclientset.Interface,
	dynamicClient dynamic.Interface,
	eventRecorder events.Recorder,
) (*TargetConfigReconciler, error) {
	c := &TargetConfigReconciler{
		ctx:                        ctx,
		operatorClient:             operatorConfigClient,
		kueueClient:                kueueClient,
		kubeClient:                 kubeClient,
		osrClient:                  osrClient,
		dynamicClient:              dynamicClient,
		eventRecorder:              eventRecorder,
		queue:                      workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "TargetConfigReconciler"),
		kubeInformersForNamespaces: kubeInformersForNamespaces,
	}

	_, err := operatorClientInformer.Informer().AddEventHandler(c.eventHandler(queueItem{kind: "kueue"}))
	if err != nil {
		return nil, err
	}

	_, err = kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {},
		UpdateFunc: func(old, new interface{}) {
			cm, ok := old.(*v1.ConfigMap)
			if !ok {
				klog.Errorf("Unable to convert obj to ConfigMap")
				return
			}
			c.queue.Add(queueItem{kind: "configmap", name: cm.Name})
		},
		DeleteFunc: func(obj interface{}) {
			cm, ok := obj.(*v1.ConfigMap)
			if !ok {
				klog.Errorf("Unable to convert obj to ConfigMap")
				return
			}
			c.queue.Add(queueItem{kind: "configmap", name: cm.Name})
		},
	})
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (c TargetConfigReconciler) sync(item queueItem) error {
	kueue, err := c.operatorClient.Kueues(operatorclient.OperatorNamespace).Get(c.ctx, operatorclient.OperatorConfigName, metav1.GetOptions{})
	if err != nil {
		klog.ErrorS(err, "unable to get operator configuration", "namespace", operatorclient.OperatorNamespace, "kueue", operatorclient.OperatorConfigName)
		return err
	}

	specAnnotations := map[string]string{
		"secondaryschedulers.operator.openshift.io/cluster": strconv.FormatInt(kueue.Generation, 10),
	}

	// Skip any sync triggered by other than the Kueue CM changes
	if item.kind == "configmap" {
		if item.name != KueueConfigMap {
			return nil
		}
		klog.Infof("configmap %q changed, forcing redeployment", KueueConfigMap)
	}

	configMapResourceVersion, err := c.getConfigMapResourceVersion(kueue)
	if err != nil {
		return err
	}
	specAnnotations["configmaps/"+KueueConfigMap] = configMapResourceVersion

	if cm, _, err := c.manageConfigMap(kueue); err != nil {
		return err
	} else {
		resourceVersion := "0"
		if cm != nil { // SyncConfigMap can return nil
			resourceVersion = cm.ObjectMeta.ResourceVersion
		}
		specAnnotations["kueue/configmap"] = resourceVersion

	}

	if sa, _, err := c.manageServiceAccount(kueue); err != nil {
		return err
	} else {
		resourceVersion := "0"
		if sa != nil { // SyncConfigMap can return nil
			resourceVersion = sa.ObjectMeta.ResourceVersion
		}
		specAnnotations["serviceaccounts/kueue-operator"] = resourceVersion
	}

	if clusterRoleBindings, _, err := c.manageClusterRoleBindings(kueue); err != nil {
		return err
	} else {
		resourceVersion := "0"
		if clusterRoleBindings != nil { // SyncConfigMap can return nil
			resourceVersion = clusterRoleBindings.ObjectMeta.ResourceVersion
		}
		specAnnotations["clusterrolebindings/kueue-operator"] = resourceVersion
	}

	deployment, _, err := c.manageDeployment(kueue, specAnnotations)
	if err != nil {
		return err
	}

	_, _, err = v1helpers.UpdateStatus(c.ctx, c.kueueClient, func(status *operatorv1.OperatorStatus) error {
		resourcemerge.SetDeploymentGeneration(&status.Generations, deployment)
		return nil
	})
	if err != nil {
		return err
	}

	if _, _, err := c.manageMutatingWebhook(kueue); err != nil {
		return err
	}

	if _, _, err := c.manageValidatingWebhook(kueue); err != nil {
		return err
	}

	return nil
}

func (c *TargetConfigReconciler) manageConfigMap(kueue *kueuev1alpha1.Kueue) (*v1.ConfigMap, bool, error) {
	var required *v1.ConfigMap
	var err error

	required, err = c.kubeClient.CoreV1().ConfigMaps(operatorclient.OperatorNamespace).Get(context.TODO(), KueueConfigMap, metav1.GetOptions{})

	if err != nil {
		klog.Errorf("Cannot load ConfigMap %s for the secondaryscheduler", operatorclient.OperatorNamespace)
		return nil, false, err
	}

	cfgMap, buildErr := configmap.BuildConfigMap(operatorclient.OperatorNamespace, kueue.Spec.Config)
	if buildErr != nil {
		klog.Errorf("Cannot build configmap %s for kueue", operatorclient.OperatorNamespace)
		return nil, false, buildErr
	}
	if required.Data["controller_manager_config.yaml"] == cfgMap.Data["controller_manager_config.yaml"] {
		return nil, true, nil
	}
	klog.InfoS("Configmap difference detected", "Namespace", operatorclient.OperatorNamespace, "ConfigMap", KueueConfigMap)
	return resourceapply.ApplyConfigMap(c.ctx, c.kubeClient.CoreV1(), c.eventRecorder, cfgMap)
}

func (c *TargetConfigReconciler) getConfigMapResourceVersion(secondaryScheduler *kueuev1alpha1.Kueue) (string, error) {
	required, err := c.kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister().ConfigMaps(operatorclient.OperatorNamespace).Get(KueueConfigMap)
	if err != nil {
		return "", fmt.Errorf("could not get configuration configmap: %v", err)
	}

	return required.ObjectMeta.ResourceVersion, nil
}

func (c *TargetConfigReconciler) manageServiceAccount(kueue *kueuev1alpha1.Kueue) (*v1.ServiceAccount, bool, error) {
	required := resourceread.ReadServiceAccountV1OrDie(bindata.MustAsset("assets/kueue-operator/serviceaccount.yaml"))
	required.Namespace = kueue.Namespace
	ownerReference := metav1.OwnerReference{
		APIVersion: "operator.openshift.io/v1alpha1",
		Kind:       "Kueue",
		Name:       kueue.Name,
		UID:        kueue.UID,
	}
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	controller.EnsureOwnerRef(required, ownerReference)

	return resourceapply.ApplyServiceAccount(c.ctx, c.kubeClient.CoreV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageMutatingWebhook(kueue *kueuev1alpha1.Kueue) (*admissionregistrationv1.MutatingWebhookConfiguration, bool, error) {
	required := resourceread.ReadMutatingWebhookConfigurationV1OrDie(bindata.MustAsset("assets/kueue-operator/mutatingwebhook.yaml"))
	required.Namespace = kueue.Namespace
	ownerReference := metav1.OwnerReference{
		APIVersion: "operator.openshift.io/v1alpha1",
		Kind:       "Kueue",
		Name:       kueue.Name,
		UID:        kueue.UID,
	}
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	controller.EnsureOwnerRef(required, ownerReference)

	return resourceapply.ApplyMutatingWebhookConfigurationImproved(c.ctx, c.kubeClient.AdmissionregistrationV1(), c.eventRecorder, required, resourceapply.NewResourceCache())
}

func (c *TargetConfigReconciler) manageValidatingWebhook(kueue *kueuev1alpha1.Kueue) (*admissionregistrationv1.ValidatingWebhookConfiguration, bool, error) {
	required := resourceread.ReadValidatingWebhookConfigurationV1OrDie(bindata.MustAsset("assets/kueue-operator/validatingwebhook.yaml"))
	required.Namespace = kueue.Namespace
	ownerReference := metav1.OwnerReference{
		APIVersion: "operator.openshift.io/v1alpha1",
		Kind:       "Kueue",
		Name:       kueue.Name,
		UID:        kueue.UID,
	}
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	controller.EnsureOwnerRef(required, ownerReference)

	return resourceapply.ApplyValidatingWebhookConfigurationImproved(c.ctx, c.kubeClient.AdmissionregistrationV1(), c.eventRecorder, required, resourceapply.NewResourceCache())
}

func (c *TargetConfigReconciler) manageClusterRoleBindings(kueue *kueuev1alpha1.Kueue) (*rbacv1.ClusterRoleBinding, bool, error) {
	required := resourceread.ReadClusterRoleBindingV1OrDie(bindata.MustAsset("assets/kueue-operator/clusterrolebinding.yaml"))
	ownerReference := metav1.OwnerReference{
		APIVersion: "operator.openshift.io/v1alpha1",
		Kind:       "Kueue",
		Name:       kueue.Name,
		UID:        kueue.UID,
	}
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	controller.EnsureOwnerRef(required, ownerReference)

	return resourceapply.ApplyClusterRoleBinding(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageDeployment(kueueoperator *kueuev1alpha1.Kueue, specAnnotations map[string]string) (*appsv1.Deployment, bool, error) {
	required := resourceread.ReadDeploymentV1OrDie(bindata.MustAsset("assets/kueue-operator/deployment.yaml"))
	required.Name = operatorclient.OperandName
	required.Namespace = kueueoperator.Namespace
	ownerReference := metav1.OwnerReference{
		APIVersion: "operator.openshift.io/v1alpha1",
		Kind:       "Kueue",
		Name:       kueueoperator.Name,
		UID:        kueueoperator.UID,
	}
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	controller.EnsureOwnerRef(required, ownerReference)

	images := map[string]string{
		"${IMAGE}": kueueoperator.Spec.Image,
	}
	for i := range required.Spec.Template.Spec.Containers {
		for pat, img := range images {
			if required.Spec.Template.Spec.Containers[i].Image == pat {
				required.Spec.Template.Spec.Containers[i].Image = img
				break
			}
		}
	}

	configmaps := map[string]string{
		"${CONFIGMAP}": KueueConfigMap,
	}

	for i := range required.Spec.Template.Spec.Volumes {
		for pat, configmap := range configmaps {
			if required.Spec.Template.Spec.Volumes[i].ConfigMap.Name == pat {
				required.Spec.Template.Spec.Volumes[i].ConfigMap.Name = configmap
				break
			}
		}
	}

	switch kueueoperator.Spec.LogLevel {
	case operatorv1.Normal:
		required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, fmt.Sprintf("-v=%d", 2))
	case operatorv1.Debug:
		required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, fmt.Sprintf("-v=%d", 4))
	case operatorv1.Trace:
		required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, fmt.Sprintf("-v=%d", 6))
	case operatorv1.TraceAll:
		required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, fmt.Sprintf("-v=%d", 8))
	default:
		required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, fmt.Sprintf("-v=%d", 2))
	}

	resourcemerge.MergeMap(resourcemerge.BoolPtr(false), &required.Spec.Template.Annotations, specAnnotations)

	return resourceapply.ApplyDeployment(
		c.ctx,
		c.kubeClient.AppsV1(),
		c.eventRecorder,
		required,
		resourcemerge.ExpectedDeploymentGeneration(required, kueueoperator.Status.Generations))
}

// Run starts the kube-scheduler and blocks until stopCh is closed.
func (c *TargetConfigReconciler) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Infof("Starting TargetConfigReconciler")
	defer klog.Infof("Shutting down TargetConfigReconciler")

	// doesn't matter what workers say, only start one.
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
}

func (c *TargetConfigReconciler) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *TargetConfigReconciler) processNextWorkItem() bool {
	dsKey, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(dsKey)
	item := dsKey.(queueItem)
	err := c.sync(item)
	if err == nil {
		c.queue.Forget(dsKey)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("%v failed with : %v", dsKey, err))
	c.queue.AddRateLimited(dsKey)

	return true
}

// eventHandler queues the operator to check spec and status
func (c *TargetConfigReconciler) eventHandler(item queueItem) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(item) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(item) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(item) },
	}
}
