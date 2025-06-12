package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	openshiftrouteclientset "github.com/openshift/client-go/route/clientset/versioned"
	"github.com/openshift/kueue-operator/bindata"
	kueuev1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	"github.com/openshift/kueue-operator/pkg/cert"
	"github.com/openshift/kueue-operator/pkg/configmap"
	kueueconfigclient "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned/typed/kueueoperator/v1"
	operatorclientinformers "github.com/openshift/kueue-operator/pkg/generated/informers/externalversions/kueueoperator/v1"
	"github.com/openshift/kueue-operator/pkg/namespace"
	"github.com/openshift/kueue-operator/pkg/operator/operatorclient"
	utilresourceapply "github.com/openshift/kueue-operator/pkg/util/resourceapply"
	"github.com/openshift/kueue-operator/pkg/webhook"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/openshift/library-go/pkg/controller"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	rbacv1 "k8s.io/api/rbac/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilerror "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

const (
	KueueConfigMap = "kueue-manager-config"
	KueueFinalizer = "kueue.openshift.io/finalizer"
)

type TargetConfigReconciler struct {
	ctx                        context.Context
	operatorClient             kueueconfigclient.KueueV1Interface
	kueueClient                *operatorclient.KueueClient
	kubeClient                 kubernetes.Interface
	osrClient                  openshiftrouteclientset.Interface
	dynamicClient              dynamic.Interface
	discoveryClient            discovery.DiscoveryInterface
	eventRecorder              events.Recorder
	queue                      workqueue.TypedRateLimitingInterface[queueItem]
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces
	crdClient                  apiextv1.ApiextensionsV1Interface
	operatorNamespace          string
	resourceCache              resourceapply.ResourceCache
	kueueImage                 string
}

func NewTargetConfigReconciler(
	ctx context.Context,
	operatorConfigClient kueueconfigclient.KueueV1Interface,
	operatorClientInformer operatorclientinformers.KueueInformer,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	kueueClient *operatorclient.KueueClient,
	kubeClient kubernetes.Interface,
	osrClient openshiftrouteclientset.Interface,
	dynamicClient dynamic.Interface,
	discoveryClient discovery.DiscoveryInterface,
	crdClient apiextv1.ApiextensionsV1Interface,
	eventRecorder events.Recorder,
	kueueImage string,
) (*TargetConfigReconciler, error) {
	c := &TargetConfigReconciler{
		ctx:                        ctx,
		operatorClient:             operatorConfigClient,
		kueueClient:                kueueClient,
		kubeClient:                 kubeClient,
		osrClient:                  osrClient,
		dynamicClient:              dynamicClient,
		discoveryClient:            discoveryClient,
		eventRecorder:              eventRecorder,
		queue:                      workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.DefaultTypedControllerRateLimiter[queueItem](), workqueue.TypedRateLimitingQueueConfig[queueItem]{Name: "TargetConfigReconciler"}),
		kubeInformersForNamespaces: kubeInformersForNamespaces,
		crdClient:                  crdClient,
		operatorNamespace:          namespace.GetNamespace(),
		resourceCache:              resourceapply.NewResourceCache(),
		kueueImage:                 kueueImage,
	}

	_, err := operatorClientInformer.Informer().AddEventHandler(c.eventHandler(queueItem{kind: "kueue"}))
	if err != nil {
		return nil, err
	}

	_, err = kubeInformersForNamespaces.InformersFor(c.operatorNamespace).Core().V1().ConfigMaps().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
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

func (c TargetConfigReconciler) sync() error {

	found, err := isResourceRegistered(c.discoveryClient, schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    "Issuer",
	})
	if err != nil {
		klog.Errorf("unable to check cert-manager is installed: %v", err)
		return err
	}
	if !found {
		klog.Errorf("please make sure that cert-manager is installed")
		return fmt.Errorf("please make sure that cert-manager is installed on your cluster")
	}

	kueue, err := c.operatorClient.Kueues().Get(c.ctx, operatorclient.OperatorConfigName, metav1.GetOptions{})
	if err != nil {
		klog.ErrorS(err, "unable to get operator configuration", "kueue", operatorclient.OperatorConfigName)
		return err
	}

	ownerReference := metav1.OwnerReference{
		APIVersion: "kueue.openshift.io/v1",
		Kind:       "Kueue",
		Name:       kueue.Name,
		UID:        kueue.UID,
	}

	if err := c.addFinalizerToKueueInstance(kueue); err != nil {
		klog.Errorf("Failed to add finalizer to Kueue instance: %v", err)
		return err
	}

	specAnnotations := map[string]string{
		"kueueoperator.kueue.openshift.io/cluster": strconv.FormatInt(kueue.Generation, 10),
	}

	if kueue.DeletionTimestamp != nil {
		klog.Infof("Kueue instance %s is being deleted. Initiating cleanup...", kueue.Name)

		cleanupResources := []func(context.Context) error{
			c.cleanUpWebhooks,
			c.cleanUpCertificatesAndIssuers,
			c.cleanUpClusterRoles,
			c.cleanUpClusterRoleBindings,
		}

		for _, step := range cleanupResources {
			if err := step(c.ctx); err != nil {
				return err
			}
		}

		klog.Info("Finished cleanup. Proceeding with finalizer removal.")

		if err := c.removeFinalizerFromKueueInstance(kueue); err != nil {
			klog.Errorf("Failed to remove finalizer from Kueue instance %s: %v", kueue.Name, err)
		} else {
			klog.Infof("Finalizer successfully removed from Kueue instance %s", kueue.Name)
		}

		return nil
	}

	_, _, err = c.manageIssuerCR(c.ctx, kueue)
	if err != nil {
		klog.Errorf("unable to manage issuer err: %v", err)
		return err
	}

	_, _, err = c.manageCertificateWebhookCR(c.ctx, kueue)
	if err != nil {
		klog.Errorf("unable to manage webhook certificate err: %v", err)
		return err
	}

	_, _, err = c.manageMetricsCertificateCR(c.ctx, kueue)
	if err != nil {
		klog.Errorf("unable to manage metrics certificate err: %v", err)
		return err
	}

	resourceVersion := "0"
	cm, _, err := c.manageConfigMap(kueue)
	if err != nil {
		return err
	}
	if cm != nil {
		resourceVersion = cm.ResourceVersion
	}
	specAnnotations["kueue/configmap"] = resourceVersion

	sa, _, err := c.manageServiceAccount(ownerReference)
	if err != nil {
		klog.Error("unable to manage service account")
		return err
	}
	if sa != nil {
		resourceVersion = sa.ResourceVersion
	}
	specAnnotations["serviceaccounts/kueue-operator"] = resourceVersion

	leaderRole, _, err := c.manageRole("assets/kueue-operator/role-leader-election.yaml", ownerReference)
	if err != nil {
		klog.Error("unable to create role leader-election")
		return err
	}
	if leaderRole != nil {
		resourceVersion = leaderRole.ResourceVersion
	}
	specAnnotations["role/role-leader-election"] = resourceVersion

	roleBindingLeader, _, err := c.manageRoleBindings("assets/kueue-operator/rolebinding-leader-election.yaml", ownerReference, true)
	if err != nil {
		klog.Error("unable to bind role leader-election")
		return err
	}
	if roleBindingLeader != nil {
		resourceVersion = roleBindingLeader.ResourceVersion
	}
	specAnnotations["rolebinding/leader-election"] = resourceVersion

	// TODO: We need to detect if openshift-monitoring exists
	// If it does then we should create these objects.
	// Otherwise we skip emitting metrics for microshift or other
	// services that do not have openshift-monitoring
	if _, _, err := c.manageRole("assets/kueue-operator/role-prometheus.yaml", ownerReference); err != nil {
		klog.Error("unable to create role prometheus")
		return err
	}

	if _, _, err := c.manageRoleBindings("assets/kueue-operator/rolebinding-prometheus.yaml", ownerReference, false); err != nil {
		klog.Error("unable to bind role prometheus")
		return err
	}

	controllerService, _, err := c.manageService("assets/kueue-operator/controller-manager-metrics-service.yaml", ownerReference)
	if err != nil {
		klog.Error("unable to manage metrics service")
		return err
	}
	if controllerService != nil {
		resourceVersion = controllerService.ResourceVersion
	}
	specAnnotations["service/controller-manager-metrics-service"] = resourceVersion

	visbilityService, _, err := c.manageService("assets/kueue-operator/visibility-server.yaml", ownerReference)
	if err != nil {
		klog.Error("unable to manage visbility service")
		return err
	}
	if visbilityService != nil {
		resourceVersion = visbilityService.ResourceVersion
	}
	specAnnotations["service/visibility-service"] = resourceVersion

	webhookService, _, err := c.manageService("assets/kueue-operator/webhook-service.yaml", ownerReference)
	if err != nil {
		klog.Error("unable to manage webhook service")
		return err
	}
	if webhookService != nil {
		resourceVersion = webhookService.ResourceVersion
	}
	specAnnotations["service/webhook-service"] = resourceVersion

	// From here, we will create our cluster wide resources.
	if err := c.manageCustomResources(ownerReference); err != nil {
		klog.Error("unable to manage custom resource")
		return err
	}

	if err := c.manageNetworkPolicies(ownerReference); err != nil {
		klog.Error("unable to manage network policies")
		return err
	}

	if err := c.manageClusterRoles(ownerReference); err != nil {
		klog.Error("unable to manage cluster roles")
		return err
	}

	if _, _, err := c.manageOpenshiftClusterRolesForKueue(ownerReference); err != nil {
		klog.Error("unable to manage openshift cluster roles")
		return err
	}

	if _, _, err := c.manageOpenshiftClusterRolesBindingForKueue(ownerReference); err != nil {
		klog.Error("unable to manage openshift cluster roles binding")
		return err
	}

	if _, _, err := c.manageClusterRoleBindings("assets/kueue-operator/clusterrolebinding-proxy.yaml", ownerReference); err != nil {
		klog.Error("unable to manage kube proxy cluster roles")
		return err
	}

	if _, _, err := c.manageClusterRoleBindings("assets/kueue-operator/clusterrolebinding-manager.yaml", ownerReference); err != nil {
		klog.Error("unable to manage cluster role kueue-manager")
		return err
	}

	if _, _, err := c.manageClusterRoleBindings("assets/kueue-operator/clusterrolebinding-metrics.yaml", ownerReference); err != nil {
		klog.Error("unable to manage cluster role kueue-manager")
		return err
	}

	if _, _, err := c.manageMutatingWebhook(kueue, ownerReference); err != nil {
		klog.Error("unable to manage mutating webhook")
		return err
	}

	if _, _, err := c.manageValidatingWebhook(kueue, ownerReference); err != nil {
		klog.Error("unable to manage validating webhook")
		return err
	}

	// TODO: metrics should autodetected if openshift-monitoring exists
	// For microshift we cannot assume monitoring apis exist.
	if _, _, err := c.manageServiceMonitor(c.ctx, kueue); err != nil {
		return err
	}

	deployment, _, err := c.manageDeployment(kueue, specAnnotations, ownerReference)
	if err != nil {
		klog.Error("unable to manage deployment")
		return err
	}

	_, _, err = v1helpers.UpdateStatus(c.ctx, c.kueueClient, func(status *operatorv1.OperatorStatus) error {
		resourcemerge.SetDeploymentGeneration(&status.Generations, deployment)
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (c *TargetConfigReconciler) updateFinalizer(kueue *kueuev1.Kueue, add bool) error {
	finalizerOp := "added"
	mutator := controllerutil.AddFinalizer
	if !add {
		finalizerOp = "removed"
		mutator = controllerutil.RemoveFinalizer
	}

	if add == controllerutil.ContainsFinalizer(kueue, KueueFinalizer) {
		return nil
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		original, err := c.operatorClient.Kueues().Get(c.ctx, kueue.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		if add == controllerutil.ContainsFinalizer(original, KueueFinalizer) {
			return nil
		}

		modified := original.DeepCopy()
		mutator(modified, KueueFinalizer)

		originalData, err := json.Marshal(original)
		if err != nil {
			return fmt.Errorf("failed to marshal original object: %w", err)
		}

		modifiedData, err := json.Marshal(modified)
		if err != nil {
			return fmt.Errorf("failed to marshal modified object: %w", err)
		}

		patch, err := strategicpatch.CreateTwoWayMergePatch(
			originalData,
			modifiedData,
			kueuev1.Kueue{},
		)
		if err != nil {
			return fmt.Errorf("failed to create patch: %w", err)
		}

		patched, err := c.operatorClient.Kueues().Patch(
			c.ctx,
			original.Name,
			types.MergePatchType,
			patch,
			metav1.PatchOptions{},
		)
		if err != nil {
			return err
		}

		*kueue = *patched
		klog.Infof("Finalizer %s %s on Kueue %s", KueueFinalizer, finalizerOp, kueue.Name)
		return nil
	})
}

func (c *TargetConfigReconciler) addFinalizerToKueueInstance(kueue *kueuev1.Kueue) error {
	return c.updateFinalizer(kueue, true)
}

func (c *TargetConfigReconciler) removeFinalizerFromKueueInstance(kueue *kueuev1.Kueue) error {
	return c.updateFinalizer(kueue, false)
}

func (c *TargetConfigReconciler) cleanUpWebhooks(ctx context.Context) error {
	var errorList []error
	webhookTypes := []struct {
		listFunc   func(context.Context, metav1.ListOptions) ([]string, error)
		deleteFunc func(context.Context, string, metav1.DeleteOptions) error
		name       string
	}{
		{
			listFunc: func(ctx context.Context, opts metav1.ListOptions) ([]string, error) {
				webhookList, err := c.kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().List(ctx, opts)
				if err != nil {
					return nil, err
				}
				var names []string
				for _, wh := range webhookList.Items {
					if strings.Contains(wh.Name, "kueue") {
						names = append(names, wh.Name)
					}
				}
				return names, nil
			},
			deleteFunc: c.kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Delete,
			name:       "MutatingWebhookConfiguration",
		},
		{
			listFunc: func(ctx context.Context, opts metav1.ListOptions) ([]string, error) {
				webhookList, err := c.kubeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(ctx, opts)
				if err != nil {
					return nil, err
				}
				var names []string
				for _, wh := range webhookList.Items {
					if strings.Contains(wh.Name, "kueue") {
						names = append(names, wh.Name)
					}
				}
				return names, nil
			},
			deleteFunc: c.kubeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete,
			name:       "ValidatingWebhookConfiguration",
		},
	}

	for _, webhook := range webhookTypes {
		names, err := webhook.listFunc(ctx, metav1.ListOptions{})
		if err != nil {
			klog.Errorf("Failed to list %s: %v", webhook.name, err)
			errorList = append(errorList, err)
			continue
		}

		for _, name := range names {
			err := retry.OnError(retry.DefaultBackoff, errors.IsTooManyRequests, func() error {
				return webhook.deleteFunc(ctx, name, metav1.DeleteOptions{})
			})
			if err != nil {
				klog.Errorf("Failed to delete %s %s: %v", webhook.name, name, err)
				errorList = append(errorList, err)
			}
			klog.Infof("%s %s deleted successfully.", webhook.name, name)
		}
	}

	if len(errorList) > 0 {
		return utilerror.NewAggregate(errorList)
	}
	return nil
}

func (c *TargetConfigReconciler) cleanUpCertificatesAndIssuers(ctx context.Context) error {
	var errorList []error
	certManagerCRDs := []string{
		"certificates",
		"issuers",
	}

	for _, resource := range certManagerCRDs {
		gvr := schema.GroupVersionResource{
			Group:    "cert-manager.io",
			Version:  "v1",
			Resource: resource,
		}

		crList, err := c.dynamicClient.Resource(gvr).Namespace(c.operatorNamespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			klog.Errorf("Failed to list instances of %s: %v", resource, err)
			errorList = append(errorList, err)
			continue
		}

		for _, cr := range crList.Items {
			klog.Infof("Deleting %s: %s/%s", resource, cr.GetNamespace(), cr.GetName())

			err := retry.OnError(retry.DefaultBackoff, errors.IsTooManyRequests, func() error {
				return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
					return c.dynamicClient.Resource(gvr).Namespace(cr.GetNamespace()).Delete(ctx, cr.GetName(), metav1.DeleteOptions{})
				})
			})

			if err != nil {
				klog.Errorf("Failed to delete %s %s/%s: %v", resource, cr.GetNamespace(), cr.GetName(), err)
				errorList = append(errorList, err)
			} else {
				klog.Infof("Successfully deleted %s: %s/%s", resource, cr.GetNamespace(), cr.GetName())
			}
		}
	}
	if len(errorList) > 0 {
		return utilerror.NewAggregate(errorList)
	}

	return nil
}

func (c *TargetConfigReconciler) cleanUpClusterRoles(ctx context.Context) error {
	var errorList []error
	clusterRoleList, err := c.kubeClient.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.Errorf("Failed to list ClusterRoles: %v", err)
		return err
	}

	for _, role := range clusterRoleList.Items {
		if !strings.Contains(role.Name, "kueue") || strings.Contains(role.Name, "kueue-operator") {
			continue
		}

		klog.Infof("Deleting ClusterRole: %s", role.Name)

		err := retry.OnError(retry.DefaultBackoff, errors.IsTooManyRequests, func() error {
			return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				return c.kubeClient.RbacV1().ClusterRoles().Delete(ctx, role.Name, metav1.DeleteOptions{})
			})
		})
		if err != nil {
			klog.Errorf("Failed to delete ClusterRole %s: %v", role.Name, err)
			errorList = append(errorList, err)
		} else {
			klog.Infof("Successfully deleted ClusterRole: %s", role.Name)
		}
	}

	if len(errorList) > 0 {
		return utilerror.NewAggregate(errorList)
	}
	return nil
}

func (c *TargetConfigReconciler) cleanUpClusterRoleBindings(ctx context.Context) error {
	var errorList []error
	clusterRoleBindingList, err := c.kubeClient.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.Errorf("Failed to list ClusterRoleBindings: %v", err)
		return err
	}
	for _, binding := range clusterRoleBindingList.Items {

		if !strings.Contains(binding.Name, "kueue") || strings.Contains(binding.Name, "kueue-operator") {
			continue
		}

		klog.Infof("Deleting ClusterRoleBinding: %s", binding.Name)

		err := retry.OnError(retry.DefaultBackoff, errors.IsTooManyRequests, func() error {
			return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				return c.kubeClient.RbacV1().ClusterRoleBindings().Delete(ctx, binding.Name, metav1.DeleteOptions{})
			})
		})
		if err != nil {
			klog.Errorf("Failed to delete ClusterRoleBinding %s: %v", binding.Name, err)
			errorList = append(errorList, err)
		} else {
			klog.Infof("Successfully deleted ClusterRoleBinding: %s", binding.Name)
		}
	}

	if len(errorList) > 0 {
		return utilerror.NewAggregate(errorList)
	}
	return nil
}

func (c *TargetConfigReconciler) manageConfigMap(kueue *kueuev1.Kueue) (*v1.ConfigMap, bool, error) {
	required, err := c.kubeClient.CoreV1().ConfigMaps(c.operatorNamespace).Get(context.TODO(), KueueConfigMap, metav1.GetOptions{})

	if errors.IsNotFound(err) {
		return c.buildAndApplyConfigMap(nil, kueue.Spec.Config)
	} else if err != nil {
		klog.Errorf("Cannot load ConfigMap %s/kueue-manager-config for the kueue operator", c.operatorNamespace)
		return nil, false, err
	}
	return c.buildAndApplyConfigMap(required, kueue.Spec.Config)
}

func (c *TargetConfigReconciler) buildAndApplyConfigMap(oldCfgMap *v1.ConfigMap, kueueCfg kueuev1.KueueConfiguration) (*v1.ConfigMap, bool, error) {
	cfgMap, buildErr := configmap.BuildConfigMap(c.operatorNamespace, kueueCfg)
	if buildErr != nil {
		klog.Errorf("Cannot build configmap %s for kueue", c.operatorNamespace)
		return nil, false, buildErr
	}
	if oldCfgMap != nil && oldCfgMap.Data["controller_manager_config.yaml"] == cfgMap.Data["controller_manager_config.yaml"] {
		return nil, true, nil
	}
	klog.InfoS("Configmap difference detected", "Namespace", c.operatorNamespace, "ConfigMap", KueueConfigMap)
	return resourceapply.ApplyConfigMap(c.ctx, c.kubeClient.CoreV1(), c.eventRecorder, cfgMap)
}

func (c *TargetConfigReconciler) manageServiceAccount(ownerReference metav1.OwnerReference) (*v1.ServiceAccount, bool, error) {
	required := resourceread.ReadServiceAccountV1OrDie(bindata.MustAsset("assets/kueue-operator/serviceaccount.yaml"))
	required.Namespace = c.operatorNamespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	controller.EnsureOwnerRef(required, ownerReference)

	return resourceapply.ApplyServiceAccount(c.ctx, c.kubeClient.CoreV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageMutatingWebhook(kueue *kueuev1.Kueue, ownerReference metav1.OwnerReference) (*admissionregistrationv1.MutatingWebhookConfiguration, bool, error) {
	required := resourceread.ReadMutatingWebhookConfigurationV1OrDie(bindata.MustAsset("assets/kueue-operator/mutatingwebhook.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	newWebhook := webhook.ModifyPodBasedMutatingWebhook(kueue.Spec.Config, required)
	for i := range newWebhook.Webhooks {
		newWebhook.Webhooks[i].ClientConfig.Service.Namespace = c.operatorNamespace
	}
	newWebhook.ObjectMeta.Annotations = cert.InjectCertAnnotation(newWebhook.ObjectMeta.Annotations, c.operatorNamespace)
	return resourceapply.ApplyMutatingWebhookConfigurationImproved(c.ctx, c.kubeClient.AdmissionregistrationV1(), c.eventRecorder, newWebhook, c.resourceCache)
}

func (c *TargetConfigReconciler) manageValidatingWebhook(kueue *kueuev1.Kueue, ownerReference metav1.OwnerReference) (*admissionregistrationv1.ValidatingWebhookConfiguration, bool, error) {
	required := resourceread.ReadValidatingWebhookConfigurationV1OrDie(bindata.MustAsset("assets/kueue-operator/validatingwebhook.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	controller.EnsureOwnerRef(required, ownerReference)

	newWebhook := webhook.ModifyPodBasedValidatingWebhook(kueue.Spec.Config, required)
	for i := range newWebhook.Webhooks {
		newWebhook.Webhooks[i].ClientConfig.Service.Namespace = c.operatorNamespace
	}
	newWebhook.ObjectMeta.Annotations = cert.InjectCertAnnotation(newWebhook.ObjectMeta.Annotations, c.operatorNamespace)
	return resourceapply.ApplyValidatingWebhookConfigurationImproved(c.ctx, c.kubeClient.AdmissionregistrationV1(), c.eventRecorder, newWebhook, c.resourceCache)
}

func (c *TargetConfigReconciler) manageRoleBindings(assetPath string, ownerReference metav1.OwnerReference, setServiceAccountToOperatorNamespace bool) (*rbacv1.RoleBinding, bool, error) {
	required := resourceread.ReadRoleBindingV1OrDie(bindata.MustAsset(assetPath))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	required.Namespace = c.operatorNamespace
	if setServiceAccountToOperatorNamespace {
		for i := range required.Subjects {
			if required.Subjects[i].Kind != "ServiceAccount" {
				continue
			}
			required.Subjects[i].Namespace = c.operatorNamespace
		}
	}
	return resourceapply.ApplyRoleBinding(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageClusterRoleBindings(assetDir string, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRoleBinding, bool, error) {
	required := resourceread.ReadClusterRoleBindingV1OrDie(bindata.MustAsset(assetDir))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	required.Namespace = c.operatorNamespace
	for i := range required.Subjects {
		required.Subjects[i].Namespace = c.operatorNamespace
	}
	return resourceapply.ApplyClusterRoleBinding(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageRole(assetPath string, ownerReference metav1.OwnerReference) (*rbacv1.Role, bool, error) {
	required := resourceread.ReadRoleV1OrDie(bindata.MustAsset(assetPath))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	required.Namespace = c.operatorNamespace
	return resourceapply.ApplyRole(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageService(assetPath string, ownerReference metav1.OwnerReference) (*v1.Service, bool, error) {
	required := resourceread.ReadServiceV1OrDie(bindata.MustAsset(assetPath))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	required.Namespace = c.operatorNamespace
	return resourceapply.ApplyService(c.ctx, c.kubeClient.CoreV1(), c.eventRecorder, required)
}

func (c *TargetConfigReconciler) manageClusterRoles(ownerReference metav1.OwnerReference) error {
	clusterRoleDir := "assets/kueue-operator/clusterroles"

	files, err := bindata.AssetDir(clusterRoleDir)
	if err != nil {
		return fmt.Errorf("failed to read clusterroles directory: %w", err)
	}

	for _, file := range files {
		assetPath := filepath.Join(clusterRoleDir, file)
		required := resourceread.ReadClusterRoleV1OrDie(bindata.MustAsset(assetPath))
		if required.AggregationRule != nil {
			continue
		}
		required.OwnerReferences = []metav1.OwnerReference{
			ownerReference,
		}

		_, _, err := resourceapply.ApplyClusterRole(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, required)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *TargetConfigReconciler) manageNetworkPolicies(ownerReference metav1.OwnerReference) error {
	networkPolicyDir := "assets/kueue-operator/networkpolicy"

	files, err := bindata.AssetDir(networkPolicyDir)
	if err != nil {
		return fmt.Errorf("failed to read networkpolicy from directory %q: %w", networkPolicyDir, err)
	}

	// TODO: does the order of the creation of these policies matter?
	// TODO: Since OLM does not support networkpolicy resource yet the
	// operator Pod is creating policies for self isolation (in addition
	// to operand isolation). let's say our operator creates the following
	// network policy manifests for self and the operand in the following
	// order: a) deny-all, b) allow-egress-api, c) allow egress cluster-dns,
	// and d) allow-ingress-metrics; while creating these manifests in order,
	// if there is a delay between a and b, long enough that deny-all takes
	// effect and creation of b fails. If this can happen then the operator
	// has lost access to the apiserver in a self inflicted manner. Should
	// the operator create the deny-all policy last to avoid this issue?
	for _, file := range files {
		assetPath := filepath.Join(networkPolicyDir, file)
		// TODO: move these resource helper functions to library-go
		want := utilresourceapply.ReadNetworkPolicyV1OrDie(bindata.MustAsset(assetPath))
		want.Namespace = c.operatorNamespace
		want.OwnerReferences = []metav1.OwnerReference{
			ownerReference,
		}

		if _, _, err := utilresourceapply.ApplyNetworkPolicy(c.ctx, c.kubeClient.NetworkingV1(), c.eventRecorder, want); err != nil {
			return err
		}
	}
	return nil
}

func (c *TargetConfigReconciler) manageOpenshiftClusterRolesBindingForKueue(ownerReference metav1.OwnerReference) (*rbacv1.ClusterRoleBinding, bool, error) {
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kueue-openshift-cluster-role-binding",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "kueue-controller-manager",
				Namespace: c.operatorNamespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "kueue-openshift-roles",
		},
	}

	clusterRoleBinding.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	return resourceapply.ApplyClusterRoleBinding(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, clusterRoleBinding)
}

func (c *TargetConfigReconciler) manageOpenshiftClusterRolesForKueue(ownerReference metav1.OwnerReference) (*rbacv1.ClusterRole, bool, error) {
	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"app.kubernetes.io/component": "controller",
				"app.kubernetes.io/name":      "kueue",
				"control-plane":               "controller-manager",
			},
			Name: "kueue-openshift-roles",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"config.openshift.io"},
				Resources: []string{"infrastructures", "apiservers"},
				Verbs:     []string{"get", "watch", "list"},
			},
		},
	}

	clusterRole.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	controller.EnsureOwnerRef(clusterRole, ownerReference)
	return resourceapply.ApplyClusterRole(c.ctx, c.kubeClient.RbacV1(), c.eventRecorder, clusterRole)
}

func (c *TargetConfigReconciler) manageCustomResources(ownerReference metav1.OwnerReference) error {
	crdDir := "assets/kueue-operator/crds"

	files, err := bindata.AssetDir(crdDir)
	if err != nil {
		return fmt.Errorf("failed to read crd directory: %w", err)
	}

	for _, file := range files {
		assetPath := filepath.Join(crdDir, file)
		required := resourceread.ReadCustomResourceDefinitionV1OrDie(bindata.MustAsset(assetPath))

		isAlphaVersion := false
		for _, version := range required.Spec.Versions {
			if strings.HasPrefix(version.Name, "v1alpha") {
				isAlphaVersion = true
				break
			}
		}

		if isAlphaVersion {
			klog.Infof("Skipping installation of alpha CRD: %s", required.Name)
			continue
		}

		required.OwnerReferences = []metav1.OwnerReference{
			ownerReference,
		}
		required.ObjectMeta.Annotations = cert.InjectCertAnnotation(required.GetAnnotations(), c.operatorNamespace)
		_, _, err := resourceapply.ApplyCustomResourceDefinitionV1(c.ctx, c.crdClient, c.eventRecorder, required)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *TargetConfigReconciler) manageDeployment(kueueoperator *kueuev1.Kueue, specAnnotations map[string]string, ownerReference metav1.OwnerReference) (*appsv1.Deployment, bool, error) {
	required := resourceread.ReadDeploymentV1OrDie(bindata.MustAsset("assets/kueue-operator/deployment.yaml"))
	required.Name = operatorclient.OperandName
	required.Namespace = c.operatorNamespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	// Add metrics certificate volume.
	metricsCertVolume := v1.Volume{
		Name: "metrics-certs",
		VolumeSource: v1.VolumeSource{
			Secret: &v1.SecretVolumeSource{
				SecretName: "metrics-server-cert",
			},
		},
	}
	required.Spec.Template.Spec.Volumes = append(required.Spec.Template.Spec.Volumes, metricsCertVolume)

	// Add volume mount to the container.
	metricsCertVolumeMount := v1.VolumeMount{
		Name:      "metrics-certs",
		MountPath: "/etc/kueue/metrics/certs",
		ReadOnly:  true,
	}
	required.Spec.Template.Spec.Containers[0].VolumeMounts = append(
		required.Spec.Template.Spec.Containers[0].VolumeMounts,
		metricsCertVolumeMount,
	)

	// add ReadOnlyRootFilesystem to Kueue deployment.
	// this will be fixed in upstream as of 0.12.
	required.Spec.Template.Spec.Containers[0].SecurityContext.ReadOnlyRootFilesystem = ptr.To(true)
	// Add HA configuration for Kueue deployment.
	var replicas int32 = 2
	required.Spec.Replicas = ptr.To(replicas)
	required.Spec.Strategy = appsv1.DeploymentStrategy{
		Type: appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{
			MaxUnavailable: ptr.To(intstr.FromInt(1)),
		},
	}
	required.Spec.Template.Spec.Affinity = &v1.Affinity{
		PodAntiAffinity: &v1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []v1.WeightedPodAffinityTerm{
				{
					Weight: 100,
					PodAffinityTerm: v1.PodAffinityTerm{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"control-plane":          "controller-manager",
								"app.kubernetes.io/name": "kueue",
							},
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				},
			},
		},
	}

	required.Spec.Template.Spec.Containers[0].Image = c.kueueImage
	switch kueueoperator.Spec.LogLevel {
	case operatorv1.Normal:
		required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, fmt.Sprintf("--zap-log-level=%d", 2))
	case operatorv1.Debug:
		required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, fmt.Sprintf("--zap-log-level=%d", 4))
	case operatorv1.Trace:
		required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, fmt.Sprintf("--zap-log-level=%d", 6))
	case operatorv1.TraceAll:
		required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, fmt.Sprintf("--zap-log-level=%d", 8))
	default:
		required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, fmt.Sprintf("--zap-log-level=%d", 2))
	}

	resourcemerge.MergeMap(ptr.To(false), &required.Spec.Template.Annotations, specAnnotations)

	deploy, flag, err := resourceapply.ApplyDeployment(
		c.ctx,
		c.kubeClient.AppsV1(),
		c.eventRecorder,
		required,
		resourcemerge.ExpectedDeploymentGeneration(required, kueueoperator.Status.Generations))
	if err != nil {
		klog.InfoS("Deployment error", "Deployment", deploy)
	}
	return deploy, flag, err
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
	err := c.sync()
	if err == nil {
		c.queue.Forget(dsKey)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("%v failed with : %v", dsKey, err))
	c.queue.AddRateLimited(dsKey)

	return true
}

func (c *TargetConfigReconciler) manageIssuerCR(ctx context.Context, kueue *kueuev1.Kueue) (*unstructured.Unstructured, bool, error) {
	gvr := schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "issuers",
	}

	required := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cert-manager.io/v1",
			"kind":       "Issuer",
			"metadata": map[string]interface{}{
				"ownerReferences": []interface{}{
					map[string]interface{}{
						"apiVersion": "operator.openshift.io/v1",
						"kind":       "Kueue",
						"name":       kueue.Name,
						"uid":        string(kueue.UID),
					},
				},
				"name":      "selfsigned",
				"namespace": c.operatorNamespace,
			},
			"spec": map[string]interface{}{
				"selfSigned": map[string]interface{}{},
			},
		},
	}

	return resourceapply.ApplyUnstructuredResourceImproved(ctx, c.dynamicClient, c.eventRecorder, required, c.resourceCache, gvr, nil, nil)
}

func (c *TargetConfigReconciler) manageCertificateWebhookCR(ctx context.Context, kueue *kueuev1.Kueue) (*unstructured.Unstructured, bool, error) {
	gvr := schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "certificates",
	}

	kueueServiceName := fmt.Sprintf("kueue-webhook-service.%s.svc", c.operatorNamespace)
	required := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cert-manager.io/v1",
			"kind":       "Certificate",
			"metadata": map[string]interface{}{
				"ownerReferences": []interface{}{
					map[string]interface{}{
						"apiVersion": "kueue.openshift.io/v1",
						"kind":       "Kueue",
						"name":       kueue.Name,
						"uid":        string(kueue.UID),
					},
				},
				"name":      "webhook-cert",
				"namespace": c.operatorNamespace,
			},
			"spec": map[string]interface{}{
				"secretName": "kueue-webhook-server-cert",
				"dnsNames": []interface{}{
					kueueServiceName,
				},
				"issuerRef": map[string]interface{}{
					"name": "selfsigned",
				},
			},
		},
	}

	return resourceapply.ApplyUnstructuredResourceImproved(ctx, c.dynamicClient, c.eventRecorder, required, c.resourceCache, gvr, nil, nil)
}

func (c *TargetConfigReconciler) manageMetricsCertificateCR(ctx context.Context, kueue *kueuev1.Kueue) (*unstructured.Unstructured, bool, error) {
	gvr := schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "certificates",
	}

	metricsServiceName := fmt.Sprintf("kueue-controller-manager-metrics-service.%s.svc", c.operatorNamespace)
	required := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cert-manager.io/v1",
			"kind":       "Certificate",
			"metadata": map[string]interface{}{
				"ownerReferences": []interface{}{
					map[string]interface{}{
						"apiVersion": "kueue.openshift.io/v1",
						"kind":       "Kueue",
						"name":       kueue.Name,
						"uid":        string(kueue.UID),
					},
				},
				"name":      "metrics-certs",
				"namespace": c.operatorNamespace,
			},
			"spec": map[string]interface{}{
				"dnsNames": []interface{}{
					metricsServiceName,
					metricsServiceName + ".cluster.local",
				},
				"commonName": "kueue-metrics",
				"issuerRef": map[string]interface{}{
					"kind": "Issuer",
					"name": "selfsigned",
				},
				"secretName": "metrics-server-cert",
			},
		},
	}

	return resourceapply.ApplyUnstructuredResourceImproved(ctx, c.dynamicClient, c.eventRecorder, required, c.resourceCache, gvr, nil, nil)
}

func (c *TargetConfigReconciler) manageServiceMonitor(ctx context.Context, kueue *kueuev1.Kueue) (*unstructured.Unstructured, bool, error) {

	// Create ServiceMonitor object
	serviceMonitor := monitoringv1.ServiceMonitor{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceMonitor",
			APIVersion: "monitoring.coreos.com/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kueue-metrics",
			Namespace: c.operatorNamespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "kueue.openshift.io/v1",
					Kind:       "Kueue",
					Name:       kueue.Name,
					UID:        kueue.UID,
				},
			},
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"control-plane": "controller-manager",
				},
			},
			Endpoints: []monitoringv1.Endpoint{
				{
					Interval:        "30s",
					Path:            "/metrics",
					Port:            "metrics", // Name of the port you want to monitor
					Scheme:          "https",
					BearerTokenFile: "/var/run/secrets/kubernetes.io/serviceaccount/token",
					TLSConfig: &monitoringv1.TLSConfig{
						SafeTLSConfig: monitoringv1.SafeTLSConfig{
							InsecureSkipVerify: ptr.To(false),
							CA: monitoringv1.SecretOrConfigMap{
								Secret: &v1.SecretKeySelector{
									LocalObjectReference: v1.LocalObjectReference{
										Name: "metrics-server-cert",
									},
									Key: "ca.crt",
								},
							},
							Cert: monitoringv1.SecretOrConfigMap{
								Secret: &v1.SecretKeySelector{
									LocalObjectReference: v1.LocalObjectReference{
										Name: "metrics-server-cert",
									},
									Key: "tls.crt",
								},
							},
							KeySecret: &v1.SecretKeySelector{
								LocalObjectReference: v1.LocalObjectReference{
									Name: "metrics-server-cert",
								},
								Key: "tls.key",
							},
							ServerName: ptr.To("kueue-controller-manager-metrics-service.openshift-kueue-operator.svc"),
						},
					},
				},
			},
		},
	}
	required := &unstructured.Unstructured{}
	convertObj2Unstructured(serviceMonitor, required)

	return resourceapply.ApplyServiceMonitor(ctx, c.dynamicClient, c.eventRecorder, required)
}

// eventHandler queues the operator to check spec and status
func (c *TargetConfigReconciler) eventHandler(item queueItem) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(item) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(item) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(item) },
	}
}

func isResourceRegistered(discoveryClient discovery.DiscoveryInterface, gvk schema.GroupVersionKind) (bool, error) {
	apiResourceLists, err := discoveryClient.ServerResourcesForGroupVersion(gvk.GroupVersion().String())
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	for _, apiResource := range apiResourceLists.APIResources {
		if apiResource.Kind == gvk.Kind {
			return true, nil
		}
	}
	return false, nil
}

// convertObj2Unstructured convert the k8s obj to unstructured obj.
// for example:
//
//	d := appsv1.Deployment{...}
//	u := new(unstructured.Unstructured)
//	err := convertObj2Unstructured(d, u)
func convertObj2Unstructured(k8sObj interface{}, u *unstructured.Unstructured) (err error) {
	var tmp []byte
	tmp, err = json.Marshal(k8sObj)
	if err != nil {
		return
	}
	err = u.UnmarshalJSON(tmp)
	return
}
