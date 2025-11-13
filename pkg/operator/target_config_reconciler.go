package operator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	applyoperatorv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	openshiftrouteclientset "github.com/openshift/client-go/route/clientset/versioned"
	"github.com/openshift/kueue-operator/bindata"
	kueuev1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
	"github.com/openshift/kueue-operator/pkg/cert"
	"github.com/openshift/kueue-operator/pkg/configmap"
	applyconfigurationkueueoperatorv1 "github.com/openshift/kueue-operator/pkg/generated/applyconfiguration/kueueoperator/v1"
	kueueconfigclient "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned/typed/kueueoperator/v1"
	operatorclientinformers "github.com/openshift/kueue-operator/pkg/generated/informers/externalversions/kueueoperator/v1"
	"github.com/openshift/kueue-operator/pkg/namespace"
	"github.com/openshift/kueue-operator/pkg/operator/operatorclient"
	utilresourceapply "github.com/openshift/kueue-operator/pkg/util/resourceapply"
	"github.com/openshift/kueue-operator/pkg/webhook"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextinformer "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	apiregistrationv1client "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"
	apiregistrationinformers "k8s.io/kube-aggregator/pkg/client/informers/externalversions"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/openshift/library-go/pkg/controller"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	flowcontrolv1 "k8s.io/api/flowcontrol/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/utils/ptr"

	rbacv1 "k8s.io/api/rbac/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilerror "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
	"sigs.k8s.io/yaml"
)

const (
	KueueConfigMap = "kueue-manager-config"
	KueueFinalizer = "kueue.openshift.io/finalizer"
)

type TargetConfigReconciler struct {
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
	crdInformer                apiextinformer.SharedInformerFactory
	kubeInformer               informers.SharedInformerFactory
	operatorNamespace          string
	resourceCache              resourceapply.ResourceCache
	kueueImage                 string
	serviceMonitorSupport      bool
	apiRegistrationClient      apiregistrationv1client.ApiregistrationV1Interface
	isOpenShift                bool
}

// computeSpecHash computes a SHA256 hash of the given object's spec.
// This is used to detect spec changes while ignoring status changes.
// For objects without a spec field (like ConfigMaps), it hashes the entire object.
func computeSpecHash(obj interface{}) (string, error) {
	jsonBytes, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(jsonBytes)
	return fmt.Sprintf("%x", hash), nil
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
	apiRegistrationClient apiregistrationv1client.ApiregistrationV1Interface,
	crdInformer apiextinformer.SharedInformerFactory,
	apiregistrationInformer apiregistrationinformers.SharedInformerFactory,
	kubeInformer informers.SharedInformerFactory,
	eventRecorder events.Recorder,
	kueueImage string,
) (factory.Controller, error) {
	c := &TargetConfigReconciler{
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
		crdInformer:                crdInformer,
		kubeInformer:               kubeInformer,
		operatorNamespace:          namespace.GetNamespace(),
		resourceCache:              resourceapply.NewResourceCache(),
		kueueImage:                 kueueImage,
		serviceMonitorSupport:      false,
		apiRegistrationClient:      apiRegistrationClient,
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

	// check for ServiceMonitor support
	c.serviceMonitorSupport, err = isResourceRegistered(c.discoveryClient, schema.GroupVersionKind{
		Kind:    "ServiceMonitor",
		Group:   "monitoring.coreos.com",
		Version: "v1",
	})
	if err != nil {
		klog.Errorf("unable to check if ServiceMonitor CRD is installed: %v", err)
		return nil, err
	}

	// Detect platform type (OpenShift vs kind/vanilla k8s)
	c.isOpenShift = c.detectOpenShift()

	// Create operator ServiceMonitor and Prometheus RBAC if CRD is available
	if c.serviceMonitorSupport {
		// Create Prometheus RBAC first (needed for Prometheus to scrape metrics)
		if err := c.ensurePrometheusRBAC(ctx); err != nil {
			klog.Errorf("Failed to create Prometheus RBAC: %v", err)
			c.eventRecorder.Warningf(
				"PrometheusRBACCreateFailed",
				"Failed to create Prometheus RBAC: %v", err,
			)
			// Don't fail controller startup if RBAC creation fails
		} else {
			klog.Info("Prometheus RBAC ensured successfully")
		}

		// Create ServiceMonitor
		if err := c.ensureOperatorServiceMonitor(ctx); err != nil {
			klog.Errorf("Failed to create operator ServiceMonitor: %v", err)
			c.eventRecorder.Warningf(
				"ServiceMonitorCreateFailed",
				"Failed to create operator ServiceMonitor: %v", err,
			)
			// Don't fail controller startup if ServiceMonitor creation fails
			// This is optional functionality
		} else {
			klog.Info("Operator ServiceMonitor ensured successfully")
		}
	} else {
		klog.Info("ServiceMonitor CRD not available, skipping operator monitoring setup")
	}

	// Ensure operator NetworkPolicies
	if err := c.ensureOperatorNetworkPolicies(ctx); err != nil {
		klog.Errorf("Failed to create operator NetworkPolicies: %v", err)
		c.eventRecorder.Warningf(
			"NetworkPolicyCreateFailed",
			"Failed to create operator NetworkPolicies: %v", err,
		)
		// Don't fail controller startup if NetworkPolicy creation fails
	} else {
		klog.Info("Operator NetworkPolicies ensured successfully")
	}

	return factory.New().WithInformers(
		kueueClient.Informer(),
		kubeInformersForNamespaces.InformersFor(c.operatorNamespace).Apps().V1().Deployments().Informer(),
		// RBAC informers for caching
		c.kubeInformer.Rbac().V1().ClusterRoles().Informer(),
		c.kubeInformer.Rbac().V1().ClusterRoleBindings().Informer(),
		c.kubeInformer.Rbac().V1().Roles().Informer(),
		c.kubeInformer.Rbac().V1().RoleBindings().Informer(),
		// CRD informer for caching
		c.crdInformer.Apiextensions().V1().CustomResourceDefinitions().Informer(),
		// Webhook configuration informers for caching
		kubeInformersForNamespaces.InformersFor(c.operatorNamespace).Admissionregistration().V1().MutatingWebhookConfigurations().Informer(),
		kubeInformersForNamespaces.InformersFor(c.operatorNamespace).Admissionregistration().V1().ValidatingWebhookConfigurations().Informer(),
		// Flow control informers for caching
		c.kubeInformer.Flowcontrol().V1().PriorityLevelConfigurations().Informer(),
		// Namespaced resource informers for caching
		kubeInformersForNamespaces.InformersFor(c.operatorNamespace).Core().V1().ConfigMaps().Informer(),
		kubeInformersForNamespaces.InformersFor(c.operatorNamespace).Core().V1().Secrets().Informer(),
		kubeInformersForNamespaces.InformersFor(c.operatorNamespace).Core().V1().Services().Informer(),
		kubeInformersForNamespaces.InformersFor(c.operatorNamespace).Core().V1().ServiceAccounts().Informer(),
		kubeInformersForNamespaces.InformersFor(c.operatorNamespace).Networking().V1().NetworkPolicies().Informer(),
		kubeInformer.Flowcontrol().V1().FlowSchemas().Informer(),
		apiregistrationInformer.Apiregistration().V1().APIServices().Informer(),
	).ResyncEvery(5*time.Minute).
		WithSync(c.sync).
		ToController("KueueOperator", c.eventRecorder), nil
}

func (c *TargetConfigReconciler) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	// Get Kueue from informer cache first so we can update status if needed.
	obj, exists, err := c.kueueClient.Informer().GetIndexer().GetByKey(operatorclient.OperatorConfigName)
	if err != nil {
		c.eventRecorder.Eventf("unconfigured", "unable to get operator configuration from cache")
		klog.ErrorS(err, "unable to get operator configuration from cache", "kueue", operatorclient.OperatorConfigName)
		return nil
	}
	if !exists {
		c.eventRecorder.Eventf("unconfigured", "operator configuration not found")
		klog.ErrorS(errors.NewNotFound(schema.GroupResource{Group: "kueue.openshift.io", Resource: "kueues"}, operatorclient.OperatorConfigName), "operator configuration not found", "kueue", operatorclient.OperatorConfigName)
		return nil
	}

	kueue, ok := obj.(*kueuev1.Kueue)
	if !ok {
		c.eventRecorder.Eventf("unconfigured", "unable to convert cached object to Kueue")
		klog.Errorf("unable to convert cached object to Kueue type")
		return nil
	}

	found, err := c.isResourceRegisteredCached(schema.GroupVersionKind{
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
		c.eventRecorder.Eventf("CertManagerMissing", "cert-manager is not installed")

		// Update Kueue CR status with Degraded condition
		conditions := c.buildCertManagerMissingConditions()
		if err := c.updateKueueStatus(ctx, kueue, conditions, nil); err != nil {
			klog.Errorf("failed to update status: %v", err)
		}
		return nil
	}

	ownerReference := metav1.OwnerReference{
		APIVersion: "kueue.openshift.io/v1",
		Kind:       "Kueue",
		Name:       kueue.Name,
		UID:        kueue.UID,
	}

	if err := c.addFinalizerToKueueInstance(ctx, kueue); err != nil {
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
			c.cleanUpResources,
		}

		for _, step := range cleanupResources {
			if err := step(ctx); err != nil {
				return err
			}
		}

		klog.Info("Finished cleanup. Proceeding with finalizer removal.")

		if err := c.removeFinalizerFromKueueInstance(ctx, kueue); err != nil {
			klog.Errorf("Failed to remove finalizer from Kueue instance %s: %v", kueue.Name, err)
		} else {
			klog.Infof("Finalizer successfully removed from Kueue instance %s", kueue.Name)
		}

		return nil
	}

	issuer, _, err := c.manageIssuerCR(ctx, kueue)
	if err != nil {
		klog.Errorf("unable to manage issuer err: %v", err)
		return err
	}
	hash, err := computeSpecHash(issuer.Object["spec"])
	if err != nil {
		return fmt.Errorf("failed to hash Issuer spec: %w", err)
	}
	specAnnotations["issuer/"+issuer.GetName()] = hash

	certificateData := []struct {
		dnsNames        []interface{}
		commonName      string
		secretName      string
		certificateName string
	}{
		{
			dnsNames: []interface{}{
				fmt.Sprintf("kueue-webhook-service.%s.svc", c.operatorNamespace),
				fmt.Sprintf("kueue-webhook-service.%s.svc.cluster.local", c.operatorNamespace),
			},
			commonName:      "",
			secretName:      "kueue-webhook-server-cert",
			certificateName: "webhook-cert",
		},
		{
			dnsNames: []interface{}{
				fmt.Sprintf("kueue-controller-manager-metrics-service.%s.svc", c.operatorNamespace),
				fmt.Sprintf("kueue-controller-manager-metrics-service.%s.svc.cluster.local", c.operatorNamespace),
			},
			commonName:      "kueue-metrics",
			secretName:      "metrics-server-cert",
			certificateName: "metrics-certs",
		},
		{
			dnsNames: []interface{}{
				fmt.Sprintf("kueue-visibility-server.%s.svc", c.operatorNamespace),
				fmt.Sprintf("kueue-visibility-server.%s.svc.cluster.local", c.operatorNamespace),
			},
			commonName:      "kueue-visibility-server",
			secretName:      "kueue-visibility-server-cert",
			certificateName: "kueue-visibility-server-cert",
		},
	}

	for _, certificate := range certificateData {
		cert, _, err := c.manageCertificateCR(ctx, kueue, certificate.dnsNames, certificate.commonName, certificate.secretName, certificate.certificateName)
		if err != nil {
			klog.Errorf("unable to manage certificate err: %v", err)
			return err
		}
		hash, err = computeSpecHash(cert.Object["spec"])
		if err != nil {
			return fmt.Errorf("failed to hash Certificate spec: %w", err)
		}
		specAnnotations["certificate/"+cert.GetName()] = hash
	}

	// Wait for the webhook certificate to be ready before creating webhooks
	// This prevents webhook timeout errors when the certificate isn't provisioned yet
	err = cert.WaitForCertificateReady(ctx, c.dynamicClient, c.operatorNamespace, "webhook-cert", 2*time.Minute)
	if err != nil {
		klog.Warningf("Webhook certificate not ready yet: %v - will retry on next reconciliation", err)
		return err
	}

	err = cert.WaitForCertificateReady(ctx, c.dynamicClient, c.operatorNamespace, "metrics-certs", 2*time.Minute)
	if err != nil {
		klog.Warningf("Metrics certificate not ready yet: %v - will retry on next reconciliation", err)
		return err
	}

	err = cert.WaitForCertificateReady(ctx, c.dynamicClient, c.operatorNamespace, "kueue-visibility-server-cert", 2*time.Minute)
	if err != nil {
		klog.Warningf("Kueue Visibility certificate not ready yet: %v - will retry on next reconciliation", err)
		return err
	}

	cm, _, err := c.manageConfigMap(ctx, kueue)
	if err != nil {
		return err
	}
	if cm != nil {
		hash, err = computeSpecHash(cm.Data)
		if err != nil {
			return fmt.Errorf("failed to hash ConfigMap data: %w", err)
		}
		specAnnotations["configmap/"+cm.Name] = hash
	}

	sa, _, err := c.manageServiceAccount(ctx, ownerReference)
	if err != nil {
		klog.Error("unable to manage service account")
		return err
	}
	// ServiceAccount doesn't have a spec field, hash the entire object
	hash, err = computeSpecHash(sa)
	if err != nil {
		return fmt.Errorf("failed to hash ServiceAccount: %w", err)
	}
	specAnnotations["serviceaccounts/"+sa.Name] = hash

	leaderRole, _, err := c.manageRole(ctx, "assets/kueue-operator/role-leader-election.yaml", ownerReference)
	if err != nil {
		klog.Error("unable to create role leader-election")
		return err
	}
	hash, err = computeSpecHash(leaderRole.Rules)
	if err != nil {
		return fmt.Errorf("failed to hash Role rules: %w", err)
	}
	specAnnotations["role/"+leaderRole.Name] = hash

	roleBindingLeader, _, err := c.manageRoleBindings(ctx, "assets/kueue-operator/rolebinding-leader-election.yaml", ownerReference, true)
	if err != nil {
		klog.Error("unable to bind role leader-election")
		return err
	}
	hash, err = computeSpecHash(roleBindingLeader)
	if err != nil {
		return fmt.Errorf("failed to hash RoleBinding: %w", err)
	}
	specAnnotations["rolebinding/"+roleBindingLeader.Name] = hash

	managerSecretsRole, _, err := c.manageRole(ctx, "assets/kueue-operator/role-manager-secrets.yaml", ownerReference)
	if err != nil {
		klog.Error("unable to create role manager-secrets")
		return err
	}
	hash, err = computeSpecHash(managerSecretsRole.Rules)
	if err != nil {
		return fmt.Errorf("failed to hash Role rules: %w", err)
	}
	specAnnotations["role/"+managerSecretsRole.Name] = hash

	roleBindingManagerSecrets, _, err := c.manageRoleBindings(ctx, "assets/kueue-operator/rolebinding-manager-secrets.yaml", ownerReference, true)
	if err != nil {
		klog.Error("unable to bind role manager-secrets")
		return err
	}
	hash, err = computeSpecHash(roleBindingManagerSecrets)
	if err != nil {
		return fmt.Errorf("failed to hash RoleBinding: %w", err)
	}
	specAnnotations["rolebinding/"+roleBindingManagerSecrets.Name] = hash

	if c.serviceMonitorSupport {
		prometheusRole, _, err := c.manageRole(ctx, "assets/kueue-operator/role-prometheus.yaml", ownerReference)
		if err != nil {
			klog.Error("unable to create role prometheus")
			return err
		}
		hash, err = computeSpecHash(prometheusRole.Rules)
		if err != nil {
			return fmt.Errorf("failed to hash Role rules: %w", err)
		}
		specAnnotations["role/"+prometheusRole.Name] = hash

		prometheusRB, _, err := c.manageRoleBindings(ctx, "assets/kueue-operator/rolebinding-prometheus.yaml", ownerReference, false)
		if err != nil {
			klog.Error("unable to bind role prometheus")
			return err
		}
		hash, err = computeSpecHash(prometheusRB)
		if err != nil {
			return fmt.Errorf("failed to hash RoleBinding: %w", err)
		}
		specAnnotations["rolebinding/"+prometheusRB.Name] = hash

		controllerService, _, err := c.manageService(ctx, "assets/kueue-operator/controller-manager-metrics-service.yaml", ownerReference)
		if err != nil {
			klog.Error("unable to manage metrics service")
			return err
		}
		hash, err = computeSpecHash(controllerService.Spec)
		if err != nil {
			return fmt.Errorf("failed to hash Service spec: %w", err)
		}
		specAnnotations["service/"+controllerService.Name] = hash

		promCRB, _, err := c.manageClusterRoleBindingsWithoutNamespaceOverride(ctx, "assets/kueue-operator/clusterrolebinding-metrics-monitoring.yaml", ownerReference)
		if err != nil {
			klog.Error("unable to manage metrics monitoring cluster role binding")
			return err
		}
		hash, err = computeSpecHash(promCRB)
		if err != nil {
			return fmt.Errorf("failed to hash ClusterRoleBinding: %w", err)
		}
		specAnnotations["clusterrolebinding/"+promCRB.Name] = hash
	}

	visbilityService, _, err := c.manageService(ctx, "assets/kueue-operator/visibility-server.yaml", ownerReference)
	if err != nil {
		klog.Error("unable to manage visbility service")
		return err
	}
	hash, err = computeSpecHash(visbilityService.Spec)
	if err != nil {
		return fmt.Errorf("failed to hash Service spec: %w", err)
	}
	specAnnotations["service/"+visbilityService.Name] = hash

	// From here, we will create our cluster wide resources.
	err = c.manageAPIService(ctx, specAnnotations, ownerReference)
	if err != nil {
		klog.Error("unable to manage visibility apiservice")
		return err
	}

	err = c.managePriorityLevelConfiguration(ctx, specAnnotations, ownerReference)
	if err != nil {
		klog.Error("unable to manage visibility prioritylevelconfiguration")
		return err
	}

	err = c.manageFlowSchema(ctx, specAnnotations, ownerReference)
	if err != nil {
		klog.Error("unable to manage visibility flowschema")
		return err
	}

	if err := c.manageCustomResources(ctx, specAnnotations); err != nil {
		klog.Error("unable to manage custom resource")
		return err
	}

	if err := c.manageNetworkPolicies(ctx, specAnnotations, ownerReference); err != nil {
		klog.Error("unable to manage network policies")
		return err
	}

	if err := c.manageClusterRoles(ctx, specAnnotations, ownerReference); err != nil {
		klog.Error("unable to manage cluster roles")
		return err
	}

	clusterRole, _, err := c.manageOpenshiftClusterRolesForKueue(ctx, ownerReference)
	if err != nil {
		klog.Error("unable to manage openshift cluster roles")
		return err
	}
	hash, err = computeSpecHash(clusterRole.Rules)
	if err != nil {
		return fmt.Errorf("failed to hash ClusterRole rules: %w", err)
	}
	specAnnotations["clusterrole/"+clusterRole.Name] = hash

	clusterRoleBindingForKueue, _, err := c.manageOpenshiftClusterRolesBindingForKueue(ctx, ownerReference)
	if err != nil {
		klog.Error("unable to manage openshift cluster roles binding")
		return err
	}
	hash, err = computeSpecHash(clusterRoleBindingForKueue)
	if err != nil {
		return fmt.Errorf("failed to hash ClusterRoleBinding: %w", err)
	}
	specAnnotations["clusterrolebinding/"+clusterRoleBindingForKueue.Name] = hash

	proxyRB, _, err := c.manageClusterRoleBindings(ctx, "assets/kueue-operator/clusterrolebinding-proxy.yaml", ownerReference)
	if err != nil {
		klog.Error("unable to manage kube proxy cluster roles")
		return err
	}
	hash, err = computeSpecHash(proxyRB)
	if err != nil {
		return fmt.Errorf("failed to hash ClusterRoleBinding: %w", err)
	}
	specAnnotations["clusterrolebinding/"+proxyRB.Name] = hash

	managerRB, _, err := c.manageClusterRoleBindings(ctx, "assets/kueue-operator/clusterrolebinding-manager.yaml", ownerReference)
	if err != nil {
		klog.Error("unable to manage cluster role kueue-manager")
		return err
	}
	hash, err = computeSpecHash(managerRB)
	if err != nil {
		return fmt.Errorf("failed to hash ClusterRoleBinding: %w", err)
	}
	specAnnotations["clusterrolebinding/"+managerRB.Name] = hash

	if c.serviceMonitorSupport {
		metricsRB, _, err := c.manageClusterRoleBindings(ctx, "assets/kueue-operator/clusterrolebinding-metrics.yaml", ownerReference)
		if err != nil {
			klog.Error("unable to manage cluster role kueue-manager")
			return err
		}
		hash, err = computeSpecHash(metricsRB)
		if err != nil {
			return fmt.Errorf("failed to hash ClusterRoleBinding: %w", err)
		}
		specAnnotations["clusterrolebinding/"+metricsRB.Name] = hash

		metricsAuthRB, _, err := c.manageClusterRoleBindings(ctx, "assets/kueue-operator/clusterrolebinding-metrics-auth.yaml", ownerReference)
		if err != nil {
			klog.Error("unable to manage metrics auth cluster role binding")
			return err
		}
		hash, err = computeSpecHash(metricsAuthRB)
		if err != nil {
			return fmt.Errorf("failed to hash ClusterRoleBinding: %w", err)
		}
		specAnnotations["clusterrolebinding/"+metricsAuthRB.Name] = hash
	}

	roleBindingVisibility, _, err := c.manageSystemRoleBindings(ctx, "assets/kueue-operator/rolebinding-visibility-server-auth-reader.yaml", ownerReference, true)
	if err != nil {
		klog.Error("unable to bind role binding for visibility")
		return err
	}
	hash, err = computeSpecHash(roleBindingVisibility)
	if err != nil {
		return fmt.Errorf("failed to hash RoleBinding: %w", err)
	}
	specAnnotations["rolebinding/"+roleBindingVisibility.Name] = hash

	kueueWH, _, err := c.manageMutatingWebhook(ctx, kueue, ownerReference)
	if err != nil {
		klog.Error("unable to manage mutating webhook")
		return err
	}
	hash, err = computeSpecHash(kueueWH.Webhooks)
	if err != nil {
		return fmt.Errorf("failed to hash MutatingWebhookConfiguration webhooks: %w", err)
	}
	specAnnotations["mutatingwebhook/"+kueueWH.Name] = hash

	kueueVWH, _, err := c.manageValidatingWebhook(ctx, kueue, ownerReference)
	if err != nil {
		klog.Error("unable to manage validating webhook")
		return err
	}
	hash, err = computeSpecHash(kueueVWH.Webhooks)
	if err != nil {
		return fmt.Errorf("failed to hash ValidatingWebhookConfiguration webhooks: %w", err)
	}
	specAnnotations["validatingwebhook/"+kueueVWH.Name] = hash

	webhookService, _, err := c.manageService(ctx, "assets/kueue-operator/webhook-service.yaml", ownerReference)
	if err != nil {
		klog.Error("unable to manage webhook service")
		return err
	}
	hash, err = computeSpecHash(webhookService.Spec)
	if err != nil {
		return fmt.Errorf("failed to hash Service spec: %w", err)
	}
	specAnnotations["service/"+webhookService.Name] = hash

	if c.serviceMonitorSupport {
		serviceMonitor, _, err := c.manageServiceMonitor(ctx, kueue)
		if err != nil {
			return err
		}
		hash, err = computeSpecHash(serviceMonitor.Object["spec"])
		if err != nil {
			return fmt.Errorf("failed to hash ServiceMonitor spec: %w", err)
		}
		specAnnotations["servicemonitor/"+serviceMonitor.GetName()] = hash
	}

	deployment, _, err := c.manageDeployment(ctx, kueue, specAnnotations, ownerReference)
	if err != nil {
		klog.Error("unable to manage deployment")
		return err
	}

	conditions := c.buildOperatorConditions(deployment)
	return c.updateKueueStatus(ctx, kueue, conditions, &deployment.Status.ReadyReplicas)
}

func (c *TargetConfigReconciler) buildOperatorConditions(deployment *appsv1.Deployment) []*applyoperatorv1.OperatorConditionApplyConfiguration {
	desired := int32(1)
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}
	ready := deployment.Status.ReadyReplicas

	// Available condition
	availableCond := applyoperatorv1.OperatorCondition().
		WithType("Available").
		WithStatus(operatorv1.ConditionFalse).
		WithReason("NotEnoughReplicas").
		WithMessage(fmt.Sprintf("%d/%d replicas are ready", ready, desired))

	if ready == desired && ready > 0 {
		availableCond = availableCond.
			WithStatus(operatorv1.ConditionTrue).
			WithReason("AllReplicasReady").
			WithMessage(fmt.Sprintf("All %d replicas are ready", ready))
	}

	// Progressing condition
	progressingCond := applyoperatorv1.OperatorCondition().
		WithType("Progressing").
		WithStatus(operatorv1.ConditionTrue).
		WithReason("Reconciling").
		WithMessage("Deployment is reconciling")
	if ready == desired && ready > 0 {
		progressingCond = progressingCond.
			WithStatus(operatorv1.ConditionFalse).
			WithReason("AsExpected").
			WithMessage("Deployment is up to date")
	}

	// Degraded condition - check for partial failure (some replicas unavailable) or complete failure (no replicas ready).
	degradedCond := applyoperatorv1.OperatorCondition().
		WithType("Degraded").
		WithStatus(operatorv1.ConditionFalse).
		WithReason("AsExpected").
		WithMessage("")
	if deployment.Status.UnavailableReplicas > 0 {
		degradedCond = degradedCond.WithStatus(operatorv1.ConditionTrue).
			WithReason("UnavailableReplicas").
			WithMessage(fmt.Sprintf("%d replicas unavailable", deployment.Status.UnavailableReplicas))
	} else if ready == 0 && desired > 0 {
		degradedCond = degradedCond.WithStatus(operatorv1.ConditionTrue).
			WithReason("NoReplicasReady").
			WithMessage(fmt.Sprintf("No replicas ready (desired: %d)", desired))
	}

	// cert-manager is installed and available.
	certManagerCond := applyoperatorv1.OperatorCondition().
		WithType("CertManagerAvailable").
		WithStatus(operatorv1.ConditionTrue).
		WithReason("CertManagerInstalled").
		WithMessage("cert-manager is installed")

	return []*applyoperatorv1.OperatorConditionApplyConfiguration{
		availableCond,
		progressingCond,
		degradedCond,
		certManagerCond,
	}
}

// buildCertManagerMissingConditions creates operator conditions when cert-manager is not installed.
func (c *TargetConfigReconciler) buildCertManagerMissingConditions() []*applyoperatorv1.OperatorConditionApplyConfiguration {
	degradedCond := applyoperatorv1.OperatorCondition().
		WithType("Degraded").
		WithStatus(operatorv1.ConditionTrue).
		WithReason("MissingDependency").
		WithMessage("please make sure that cert-manager is installed on your cluster")

	availableCond := applyoperatorv1.OperatorCondition().
		WithType("Available").
		WithStatus(operatorv1.ConditionFalse).
		WithReason("MissingDependency").
		WithMessage("cert-manager is required but not installed")

	progressingCond := applyoperatorv1.OperatorCondition().
		WithType("Progressing").
		WithStatus(operatorv1.ConditionFalse).
		WithReason("MissingDependency").
		WithMessage("waiting for cert-manager to be installed")

	return []*applyoperatorv1.OperatorConditionApplyConfiguration{
		availableCond,
		progressingCond,
		degradedCond,
	}
}

// updateKueueStatus updates the Kueue CR status with the provided conditions.
func (c *TargetConfigReconciler) updateKueueStatus(ctx context.Context, kueue *kueuev1.Kueue, conditions []*applyoperatorv1.OperatorConditionApplyConfiguration, readyReplicas *int32) error {
	status := applyconfigurationkueueoperatorv1.KueueStatus().WithConditions(conditions...)

	// Set ReadyReplicas if provided
	if readyReplicas != nil {
		status.ReadyReplicas = readyReplicas
	}

	// Set lastTransitionTime properly by comparing with existing conditions
	var existingConditions []applyoperatorv1.OperatorConditionApplyConfiguration
	if len(kueue.Status.Conditions) > 0 {
		extracted, err := applyconfigurationkueueoperatorv1.ExtractKueueStatus(kueue, "kueue-operator")
		if err == nil && extracted.Status != nil {
			existingConditions = extracted.Status.Conditions
		}
	}
	v1helpers.SetApplyConditionsLastTransitionTime(clock.RealClock{}, &status.Conditions, existingConditions)

	config := applyconfigurationkueueoperatorv1.Kueue("cluster").WithStatus(status)
	_, err := c.operatorClient.Kueues().ApplyStatus(ctx, config, metav1.ApplyOptions{FieldManager: "kueue-operator"})
	return err
}

func (c *TargetConfigReconciler) updateFinalizer(ctx context.Context, kueue *kueuev1.Kueue, add bool) error {
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
		original, err := c.operatorClient.Kueues().Get(ctx, kueue.Name, metav1.GetOptions{})
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
			ctx,
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

func (c *TargetConfigReconciler) addFinalizerToKueueInstance(ctx context.Context, kueue *kueuev1.Kueue) error {
	return c.updateFinalizer(ctx, kueue, true)
}

func (c *TargetConfigReconciler) removeFinalizerFromKueueInstance(ctx context.Context, kueue *kueuev1.Kueue) error {
	return c.updateFinalizer(ctx, kueue, false)
}

func (c *TargetConfigReconciler) cleanUpResources(ctx context.Context) error {
	var errorList []error

	klog.Infof("Deleting ConfigMap: %s/%s", c.operatorNamespace, "kueue-manager-config")
	err := retry.OnError(retry.DefaultBackoff, errors.IsTooManyRequests, func() error {
		return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			return c.kubeClient.CoreV1().ConfigMaps(c.operatorNamespace).Delete(ctx, "kueue-manager-config", metav1.DeleteOptions{})
		})
	})
	if err != nil {
		klog.Errorf("Failed to delete ConfigMap %s/%s: %v", c.operatorNamespace, "kueue-manager-config", err)
		errorList = append(errorList, err)
	} else {
		klog.Infof("Successfully deleted ConfigMap: %s/%s", c.operatorNamespace, "kueue-manager-config")
	}

	klog.Infof("Deleting Secret: %s/%s", c.operatorNamespace, "kueue-webhook-server-cert")
	err = retry.OnError(retry.DefaultBackoff, errors.IsTooManyRequests, func() error {
		return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			return c.kubeClient.CoreV1().Secrets(c.operatorNamespace).Delete(ctx, "kueue-webhook-server-cert", metav1.DeleteOptions{})
		})
	})
	if err != nil {
		klog.Errorf("Failed to delete Secret %s/%s: %v", c.operatorNamespace, "kueue-webhook-server-cert", err)
		errorList = append(errorList, err)
	} else {
		klog.Infof("Successfully deleted Secret: %s/%s", c.operatorNamespace, "kueue-webhook-server-cert")
	}

	klog.Infof("Deleting Secret: %s/%s", c.operatorNamespace, "metrics-server-cert")
	err = retry.OnError(retry.DefaultBackoff, errors.IsTooManyRequests, func() error {
		return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			return c.kubeClient.CoreV1().Secrets(c.operatorNamespace).Delete(ctx, "metrics-server-cert", metav1.DeleteOptions{})
		})
	})
	if err != nil {
		klog.Errorf("Failed to delete Secret %s/%s: %v", c.operatorNamespace, "metrics-server-cert", err)
		errorList = append(errorList, err)
	} else {
		klog.Infof("Successfully deleted Secret: %s/%s", c.operatorNamespace, "metrics-server-cert")
	}

	if len(errorList) > 0 {
		return utilerror.NewAggregate(errorList)
	}
	return nil
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
	bundleClusterRoleNames := []string{
		"kueue-batch-user-role",
		"kueue-batch-admin-role",
	}
	for _, role := range clusterRoleList.Items {
		if !strings.Contains(role.Name, "kueue") || strings.Contains(role.Name, "kueue-operator") || strings.Contains(role.Name, "openshift.io") || slices.Contains(bundleClusterRoleNames, role.Name) {
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

func (c *TargetConfigReconciler) manageConfigMap(ctx context.Context, kueue *kueuev1.Kueue) (*v1.ConfigMap, bool, error) {
	required, err := c.kubeClient.CoreV1().ConfigMaps(c.operatorNamespace).Get(ctx, KueueConfigMap, metav1.GetOptions{})

	if errors.IsNotFound(err) {
		return c.buildAndApplyConfigMap(ctx, nil, kueue.Spec.Config)
	} else if err != nil {
		klog.Errorf("Cannot load ConfigMap %s/kueue-manager-config for the kueue operator", c.operatorNamespace)
		return nil, false, err
	}
	return c.buildAndApplyConfigMap(ctx, required, kueue.Spec.Config)
}

func (c *TargetConfigReconciler) buildAndApplyConfigMap(ctx context.Context, oldCfgMap *v1.ConfigMap, kueueCfg kueuev1.KueueConfiguration) (*v1.ConfigMap, bool, error) {
	cfgMap, buildErr := configmap.BuildConfigMap(c.operatorNamespace, kueueCfg)
	if buildErr != nil {
		klog.Errorf("Cannot build configmap %s for kueue", c.operatorNamespace)
		return nil, false, buildErr
	}
	if oldCfgMap != nil && oldCfgMap.Data["controller_manager_config.yaml"] == cfgMap.Data["controller_manager_config.yaml"] {
		klog.V(4).Infof("Skipping ConfigMap %s/%s - no changes detected", c.operatorNamespace, KueueConfigMap)
		return oldCfgMap, false, nil
	}
	klog.InfoS("Configmap difference detected", "Namespace", c.operatorNamespace, "ConfigMap", KueueConfigMap)
	return resourceapply.ApplyConfigMapImproved(ctx, c.kubeClient.CoreV1(), c.eventRecorder, cfgMap, c.resourceCache)
}

func (c *TargetConfigReconciler) manageServiceAccount(ctx context.Context, ownerReference metav1.OwnerReference) (*v1.ServiceAccount, bool, error) {
	required := resourceread.ReadServiceAccountV1OrDie(bindata.MustAsset("assets/kueue-operator/serviceaccount.yaml"))
	required.Namespace = c.operatorNamespace
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	controller.EnsureOwnerRef(required, ownerReference)

	return resourceapply.ApplyServiceAccountImproved(ctx, c.kubeClient.CoreV1(), c.eventRecorder, required, c.resourceCache)
}

func (c *TargetConfigReconciler) manageFlowSchema(ctx context.Context, specAnnotations map[string]string, ownerReference metav1.OwnerReference) error {
	flowSchemaFilePath := "assets/kueue-operator/flowschema.yaml"

	// TODO: move these resource helper functions to library-go
	want := utilresourceapply.ReadFlowSchemaV1OrDie(bindata.MustAsset(flowSchemaFilePath))
	want.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	flowSchema, _, err := utilresourceapply.ApplyFlowSchema(ctx, c.kubeClient.FlowcontrolV1(), c.eventRecorder, want)
	if err != nil {
		return err
	}
	hash, err := computeSpecHash(flowSchema.Spec)
	if err != nil {
		return fmt.Errorf("failed to hash FlowSchema spec: %w", err)
	}
	specAnnotations["flowschema/"+flowSchema.Name] = hash
	return nil
}

func (c *TargetConfigReconciler) managePriorityLevelConfiguration(ctx context.Context, specAnnotations map[string]string, ownerReference metav1.OwnerReference) error {
	priorityLevelConfigurationFilePath := "assets/kueue-operator/prioritylevelconfiguration.yaml"

	// TODO: move these resource helper functions to library-go
	want := utilresourceapply.ReadPriorityLevelConfigurationV1OrDie(bindata.MustAsset(priorityLevelConfigurationFilePath))
	want.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	priorityLevelConfiguration, _, err := c.applyPriorityLevelConfigurationWithCache(ctx, want)
	if err != nil {
		return err
	}
	hash, err := computeSpecHash(priorityLevelConfiguration.Spec)
	if err != nil {
		return fmt.Errorf("failed to hash PriorityLevelConfiguration spec: %w", err)
	}
	specAnnotations["prioritylevelconfiguration/"+priorityLevelConfiguration.Name] = hash
	return nil
}

func (c *TargetConfigReconciler) manageMutatingWebhook(ctx context.Context, kueue *kueuev1.Kueue, ownerReference metav1.OwnerReference) (*admissionregistrationv1.MutatingWebhookConfiguration, bool, error) {
	required := resourceread.ReadMutatingWebhookConfigurationV1OrDie(bindata.MustAsset("assets/kueue-operator/mutatingwebhook.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}

	newWebhook := webhook.ModifyPodBasedMutatingWebhook(kueue.Spec.Config, required)
	for i := range newWebhook.Webhooks {
		newWebhook.Webhooks[i].ClientConfig.Service.Namespace = c.operatorNamespace
	}
	newWebhook.Annotations = cert.InjectCertAnnotation(newWebhook.Annotations, c.operatorNamespace)
	return resourceapply.ApplyMutatingWebhookConfigurationImproved(ctx, c.kubeClient.AdmissionregistrationV1(), c.eventRecorder, newWebhook, c.resourceCache)
}

func (c *TargetConfigReconciler) manageValidatingWebhook(ctx context.Context, kueue *kueuev1.Kueue, ownerReference metav1.OwnerReference) (*admissionregistrationv1.ValidatingWebhookConfiguration, bool, error) {
	required := resourceread.ReadValidatingWebhookConfigurationV1OrDie(bindata.MustAsset("assets/kueue-operator/validatingwebhook.yaml"))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	controller.EnsureOwnerRef(required, ownerReference)

	newWebhook := webhook.ModifyPodBasedValidatingWebhook(kueue.Spec.Config, required)
	for i := range newWebhook.Webhooks {
		newWebhook.Webhooks[i].ClientConfig.Service.Namespace = c.operatorNamespace
	}
	newWebhook.Annotations = cert.InjectCertAnnotation(newWebhook.Annotations, c.operatorNamespace)
	return resourceapply.ApplyValidatingWebhookConfigurationImproved(ctx, c.kubeClient.AdmissionregistrationV1(), c.eventRecorder, newWebhook, c.resourceCache)
}

func (c *TargetConfigReconciler) manageRoleBindings(ctx context.Context, assetPath string, ownerReference metav1.OwnerReference, setServiceAccountToOperatorNamespace bool) (*rbacv1.RoleBinding, bool, error) {
	return c.manageRoleBindingsByNamespace(ctx, c.operatorNamespace, assetPath, ownerReference, setServiceAccountToOperatorNamespace)
}

func (c *TargetConfigReconciler) manageSystemRoleBindings(ctx context.Context, assetPath string, ownerReference metav1.OwnerReference, setServiceAccountToOperatorNamespace bool) (*rbacv1.RoleBinding, bool, error) {
	return c.manageRoleBindingsByNamespace(ctx, "kube-system", assetPath, ownerReference, setServiceAccountToOperatorNamespace)
}

func (c *TargetConfigReconciler) manageRoleBindingsByNamespace(ctx context.Context, namespace string, assetPath string, ownerReference metav1.OwnerReference, setServiceAccountToOperatorNamespace bool) (*rbacv1.RoleBinding, bool, error) {
	required := resourceread.ReadRoleBindingV1OrDie(bindata.MustAsset(assetPath))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	required.Namespace = namespace
	if setServiceAccountToOperatorNamespace {
		for i := range required.Subjects {
			if required.Subjects[i].Kind != "ServiceAccount" {
				continue
			}
			required.Subjects[i].Namespace = c.operatorNamespace
		}
	}
	return c.applyRoleBindingWithCache(ctx, required)
}

func (c *TargetConfigReconciler) manageClusterRoleBindings(ctx context.Context, assetDir string, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRoleBinding, bool, error) {
	required := resourceread.ReadClusterRoleBindingV1OrDie(bindata.MustAsset(assetDir))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	for i := range required.Subjects {
		required.Subjects[i].Namespace = c.operatorNamespace
	}
	return c.applyClusterRoleBindingWithCache(ctx, required)
}

// manageClusterRoleBindingsWithoutNamespaceOverride manages ClusterRoleBindings without overriding subject namespaces.
// Use this for ClusterRoleBindings that reference service accounts in namespaces other than the operator namespace.
func (c *TargetConfigReconciler) manageClusterRoleBindingsWithoutNamespaceOverride(ctx context.Context, assetDir string, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRoleBinding, bool, error) {
	required := resourceread.ReadClusterRoleBindingV1OrDie(bindata.MustAsset(assetDir))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	// Note: We do NOT override subject namespaces here - they remain as specified in the asset file
	return c.applyClusterRoleBindingWithCache(ctx, required)
}

func (c *TargetConfigReconciler) manageRole(ctx context.Context, assetPath string, ownerReference metav1.OwnerReference) (*rbacv1.Role, bool, error) {
	required := resourceread.ReadRoleV1OrDie(bindata.MustAsset(assetPath))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	required.Namespace = c.operatorNamespace
	return c.applyRoleWithCache(ctx, required)
}

func (c *TargetConfigReconciler) manageService(ctx context.Context, assetPath string, ownerReference metav1.OwnerReference) (*v1.Service, bool, error) {
	required := resourceread.ReadServiceV1OrDie(bindata.MustAsset(assetPath))
	required.OwnerReferences = []metav1.OwnerReference{
		ownerReference,
	}
	required.Namespace = c.operatorNamespace
	return resourceapply.ApplyServiceImproved(ctx, c.kubeClient.CoreV1(), c.eventRecorder, required, c.resourceCache)
}

func (c *TargetConfigReconciler) manageAPIService(ctx context.Context, specAnnotations map[string]string, ownerReference metav1.OwnerReference) error {
	// Manage both v1beta1 and v1beta2 APIService versions
	apiServiceFiles := []string{
		"assets/kueue-operator/apiservice-v1beta1.visibility.kueue.x-k8s.io.yaml",
		"assets/kueue-operator/apiservice-v1beta2.visibility.kueue.x-k8s.io.yaml",
	}

	for _, assetPath := range apiServiceFiles {
		required := resourceread.ReadAPIServiceOrDie(bindata.MustAsset(assetPath))
		required.Spec.InsecureSkipTLSVerify = false
		required.Spec.Service.Namespace = c.operatorNamespace
		required.Spec.Service.Name = "kueue-visibility-server"
		required.Annotations = cert.InjectCertAnnotation(required.Annotations, c.operatorNamespace)
		newAnnotation := required.Annotations
		if newAnnotation == nil {
			newAnnotation = map[string]string{}
		}
		newAnnotation["cert-manager.io/inject-ca-from"] = fmt.Sprintf("%s/kueue-visibility-server-cert", c.operatorNamespace)
		required.Annotations = newAnnotation
		required.OwnerReferences = []metav1.OwnerReference{
			ownerReference,
		}
		apiService, _, err := resourceapply.ApplyAPIService(ctx, c.apiRegistrationClient, c.eventRecorder, required)
		if err != nil {
			return err
		}
		hash, err := computeSpecHash(apiService.Spec)
		if err != nil {
			return fmt.Errorf("failed to hash APIService spec: %w", err)
		}
		specAnnotations["apiservice/"+apiService.Name] = hash
	}
	return nil
}

func (c *TargetConfigReconciler) manageClusterRoles(ctx context.Context, specAnnotations map[string]string, ownerReference metav1.OwnerReference) error {
	clusterRoleDir := "assets/kueue-operator/clusterroles"

	files, err := bindata.AssetDir(clusterRoleDir)
	if err != nil {
		return fmt.Errorf("failed to read clusterroles directory: %w", err)
	}

	var hash string
	for _, file := range files {
		assetPath := filepath.Join(clusterRoleDir, file)
		required := resourceread.ReadClusterRoleV1OrDie(bindata.MustAsset(assetPath))
		if required.AggregationRule != nil {
			continue
		}
		required.OwnerReferences = []metav1.OwnerReference{
			ownerReference,
		}

		role, _, err := c.applyClusterRoleWithCache(ctx, required)
		if err != nil {
			return err
		}
		hash, err = computeSpecHash(role.Rules)
		if err != nil {
			return fmt.Errorf("failed to hash ClusterRole rules: %w", err)
		}
		specAnnotations["clusterrole/"+role.Name] = hash
	}
	return nil
}

// manageNetworkPolicies applies NetworkPolicies for the kueue operand (deployment) pods.
// These policies are applied during the sync loop and have owner references to the Kueue CR.
// For operator NetworkPolicies, see ensureOperatorNetworkPolicies() which is called during
// controller initialization.
func (c *TargetConfigReconciler) manageNetworkPolicies(ctx context.Context, specAnnotations map[string]string, ownerReference metav1.OwnerReference) error {
	// Use operand subdirectory - operator policies are applied separately during init
	networkPolicyDir := "assets/kueue-operator/networkpolicy/operand"

	files, err := bindata.AssetDir(networkPolicyDir)
	if err != nil {
		return fmt.Errorf("failed to read networkpolicy from directory %q: %w", networkPolicyDir, err)
	}

	// Sort files to ensure consistent ordering (allow policies before deny-all)
	slices.Sort(files)

	var hash string
	for _, file := range files {
		assetPath := filepath.Join(networkPolicyDir, file)
		// TODO: move these resource helper functions to library-go
		want := utilresourceapply.ReadNetworkPolicyV1OrDie(bindata.MustAsset(assetPath))
		want.Namespace = c.operatorNamespace
		want.OwnerReferences = []metav1.OwnerReference{
			ownerReference,
		}

		// Special handling for DNS egress policy based on platform
		if want.Name == "kueue-allow-egress-cluster-dns" {
			want = c.adjustDNSNetworkPolicyForPlatform(want)
		}

		// Special handling for visibility ingress/egress policy based on platform
		if want.Name == "kueue-allow-ingress-egress-visibility" {
			want = c.adjustVisibilityNetworkPolicyForPlatform(want)
		}

		// Special handling for webhook ingress/egress policy based on platform
		if want.Name == "kueue-allow-ingress-egress-webhook" {
			want = c.adjustWebhookNetworkPolicyForPlatform(want)
		}

		policy, _, err := c.applyNetworkPolicyWithCache(ctx, want)
		if err != nil {
			return err
		}
		hash, err = computeSpecHash(policy.Spec)
		if err != nil {
			return fmt.Errorf("failed to hash NetworkPolicy spec: %w", err)
		}
		specAnnotations["networkpolicy/"+policy.Name] = hash
	}
	return nil
}

// adjustDNSNetworkPolicyForPlatform modifies the DNS egress NetworkPolicy based on the detected platform.
// OpenShift uses openshift-dns namespace with dns.operator.openshift.io labels,
// while kind/vanilla k8s uses kube-system namespace with k8s-app=kube-dns label.
func (c *TargetConfigReconciler) adjustDNSNetworkPolicyForPlatform(policy *networkingv1.NetworkPolicy) *networkingv1.NetworkPolicy {
	if c.isOpenShift {
		// OpenShift configuration - the YAML already has the correct config
		return policy
	}

	// Modify the egress rule to target kube-system instead of openshift-dns
	if len(policy.Spec.Egress) > 0 && len(policy.Spec.Egress[0].To) > 0 {
		// Update namespace selector to kube-system
		if policy.Spec.Egress[0].To[0].NamespaceSelector != nil {
			policy.Spec.Egress[0].To[0].NamespaceSelector.MatchLabels = map[string]string{
				"kubernetes.io/metadata.name": "kube-system",
			}
		}

		// Update pod selector to match CoreDNS/kube-dns pods
		if policy.Spec.Egress[0].To[0].PodSelector != nil {
			policy.Spec.Egress[0].To[0].PodSelector.MatchLabels = map[string]string{
				"k8s-app": "kube-dns",
			}
		}
	}

	return policy
}

// adjustVisibilityNetworkPolicyForPlatform modifies the visibility ingress/egress NetworkPolicy based on the detected platform.
// The visibility API has RBAC controls, so it's safe to allow broad access.
// - OpenShift: Allow from all namespaces (namespaceSelector: {})
// - KIND/vanilla k8s: Allow from everywhere (empty peer {}) to include host network pods (kube-apiserver)
func (c *TargetConfigReconciler) adjustVisibilityNetworkPolicyForPlatform(policy *networkingv1.NetworkPolicy) *networkingv1.NetworkPolicy {
	if c.isOpenShift {
		// OpenShift: namespaceSelector: {} allows all namespaces, which is sufficient
		// The kube-apiserver is in a regular namespace (openshift-kube-apiserver)
		return policy
	}

	// For KIND/vanilla k8s, the kube-apiserver runs with hostNetwork: true.
	// namespaceSelector: {} doesn't match host network pods, so we remove the peer selectors
	// to allow from all sources (all namespaces + host network).
	// This is safe because the visibility API has RBAC controls.

	// Allow ingress from anywhere (all namespaces + host network)
	// Setting From to nil means "allow from all sources"
	if len(policy.Spec.Ingress) > 0 {
		policy.Spec.Ingress[0].From = nil
	}

	// Allow egress to anywhere (all namespaces + host network)
	// Setting To to nil means "allow to all destinations"
	if len(policy.Spec.Egress) > 0 {
		policy.Spec.Egress[0].To = nil
	}

	return policy
}

// adjustWebhookNetworkPolicyForPlatform modifies the webhook ingress/egress NetworkPolicy based on the detected platform.
// OpenShift uses openshift-kube-apiserver namespace with specific pod labels,
// while kind/vanilla k8s has the API server on host network, so we need to allow all traffic.
func (c *TargetConfigReconciler) adjustWebhookNetworkPolicyForPlatform(policy *networkingv1.NetworkPolicy) *networkingv1.NetworkPolicy {
	if c.isOpenShift {
		// OpenShift configuration - the YAML already has the correct config
		return policy
	}

	// For kind/vanilla k8s, the kube-apiserver runs with hostNetwork: true,
	// so it doesn't belong to any namespace. We need to allow all ingress/egress.

	// Remove peer selectors for ingress - allow from anywhere
	// Setting From to nil means "allow from all sources"
	if len(policy.Spec.Ingress) > 0 {
		policy.Spec.Ingress[0].From = nil
	}

	// Remove peer selectors for egress - allow to anywhere
	// Setting To to nil means "allow to all destinations"
	if len(policy.Spec.Egress) > 0 {
		policy.Spec.Egress[0].To = nil
	}

	return policy
}

// ensurePrometheusRBAC creates RBAC resources to allow Prometheus to scrape operator metrics.
// This includes a Role granting permissions to list pods/services/endpoints and a RoleBinding
// binding the prometheus-k8s service account to the Role.
func (c *TargetConfigReconciler) ensurePrometheusRBAC(ctx context.Context) error {
	klog.Info("Creating Prometheus RBAC resources...")

	// Create Role
	roleAssetPath := "assets/kueue-operator/prometheus-rbac/role.yaml"
	roleData, err := bindata.Asset(roleAssetPath)
	if err != nil {
		return fmt.Errorf("failed to load Prometheus Role asset: %w", err)
	}

	role := resourceread.ReadRoleV1OrDie(roleData)
	role.Namespace = c.operatorNamespace

	_, _, err = resourceapply.ApplyRole(ctx, c.kubeClient.RbacV1(), c.eventRecorder, role)
	if err != nil {
		return fmt.Errorf("failed to apply Prometheus Role: %w", err)
	}

	// Create RoleBinding
	roleBindingAssetPath := "assets/kueue-operator/prometheus-rbac/rolebinding.yaml"
	roleBindingData, err := bindata.Asset(roleBindingAssetPath)
	if err != nil {
		return fmt.Errorf("failed to load Prometheus RoleBinding asset: %w", err)
	}

	roleBinding := resourceread.ReadRoleBindingV1OrDie(roleBindingData)
	roleBinding.Namespace = c.operatorNamespace

	_, _, err = resourceapply.ApplyRoleBinding(ctx, c.kubeClient.RbacV1(), c.eventRecorder, roleBinding)
	if err != nil {
		return fmt.Errorf("failed to apply Prometheus RoleBinding: %w", err)
	}

	klog.Info("Prometheus RBAC resources created successfully")
	return nil
}

// ensureOperatorServiceMonitor creates a ServiceMonitor for the operator's metrics endpoint
// if the ServiceMonitor CRD is available. This is optional functionality that enables
// Prometheus integration on clusters with Prometheus Operator installed.
func (c *TargetConfigReconciler) ensureOperatorServiceMonitor(ctx context.Context) error {
	klog.Info("Creating operator ServiceMonitor...")

	assetPath := "assets/kueue-operator/servicemonitor/operator-metrics.yaml"
	data, err := bindata.Asset(assetPath)
	if err != nil {
		return fmt.Errorf("failed to load ServiceMonitor asset: %w", err)
	}

	// Parse the ServiceMonitor YAML
	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(data, obj); err != nil {
		return fmt.Errorf("failed to unmarshal ServiceMonitor: %w", err)
	}

	// Set the namespace
	obj.SetNamespace(c.operatorNamespace)

	// Create or update the ServiceMonitor using dynamic client
	gvr := schema.GroupVersionResource{
		Group:    "monitoring.coreos.com",
		Version:  "v1",
		Resource: "servicemonitors",
	}

	existing, err := c.dynamicClient.Resource(gvr).Namespace(c.operatorNamespace).Get(
		ctx,
		obj.GetName(),
		metav1.GetOptions{},
	)

	if errors.IsNotFound(err) {
		klog.Infof("Creating ServiceMonitor: %s", obj.GetName())
		_, err = c.dynamicClient.Resource(gvr).Namespace(c.operatorNamespace).Create(
			ctx,
			obj,
			metav1.CreateOptions{},
		)
		if err != nil {
			return fmt.Errorf("failed to create ServiceMonitor: %w", err)
		}
		klog.Info("Operator ServiceMonitor created successfully")
	} else if err != nil {
		return fmt.Errorf("failed to get ServiceMonitor: %w", err)
	} else {
		// Update if spec has changed
		existingSpec, _, _ := unstructured.NestedMap(existing.Object, "spec")
		requiredSpec, _, _ := unstructured.NestedMap(obj.Object, "spec")

		existingHash, _ := computeSpecHash(existingSpec)
		requiredHash, _ := computeSpecHash(requiredSpec)

		if existingHash != requiredHash {
			klog.Infof("Updating ServiceMonitor: %s", obj.GetName())
			obj.SetResourceVersion(existing.GetResourceVersion())
			_, err = c.dynamicClient.Resource(gvr).Namespace(c.operatorNamespace).Update(
				ctx,
				obj,
				metav1.UpdateOptions{},
			)
			if err != nil {
				return fmt.Errorf("failed to update ServiceMonitor: %w", err)
			}
			klog.Info("Operator ServiceMonitor updated successfully")
		} else {
			klog.V(4).Info("Operator ServiceMonitor already up to date")
		}
	}

	return nil
}

// ensureOperatorNetworkPolicies creates NetworkPolicies for the operator pod.
// These are applied once during controller initialization and do not have owner references
// since they need to persist even when no Kueue CR exists.
func (c *TargetConfigReconciler) ensureOperatorNetworkPolicies(ctx context.Context) error {
	klog.Info("Creating operator NetworkPolicies...")

	networkPolicyDir := "assets/kueue-operator/networkpolicy/operator"
	files, err := bindata.AssetDir(networkPolicyDir)
	if err != nil {
		return fmt.Errorf("failed to read networkpolicy from directory %q: %w", networkPolicyDir, err)
	}

	// Sort files to ensure consistent ordering (allow policies before deny-all)
	// Note: AssetDir already filters out directories, so only files are returned
	slices.Sort(files)

	for _, file := range files {
		assetPath := filepath.Join(networkPolicyDir, file)
		want := utilresourceapply.ReadNetworkPolicyV1OrDie(bindata.MustAsset(assetPath))
		want.Namespace = c.operatorNamespace

		// Apply without owner reference - these policies persist independently
		policy, updated, err := c.applyNetworkPolicyWithCache(ctx, want)
		if err != nil {
			return fmt.Errorf("failed to apply NetworkPolicy %s: %w", want.Name, err)
		}
		if updated {
			klog.Infof("Operator NetworkPolicy %s updated", policy.Name)
		} else {
			klog.V(4).Infof("Operator NetworkPolicy %s already up to date", policy.Name)
		}
	}

	klog.Info("Operator NetworkPolicies ensured successfully")
	return nil
}

func (c *TargetConfigReconciler) manageOpenshiftClusterRolesBindingForKueue(ctx context.Context, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRoleBinding, bool, error) {
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
	return c.applyClusterRoleBindingWithCache(ctx, clusterRoleBinding)
}

func (c *TargetConfigReconciler) manageOpenshiftClusterRolesForKueue(ctx context.Context, ownerReference metav1.OwnerReference) (*rbacv1.ClusterRole, bool, error) {
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
	return c.applyClusterRoleWithCache(ctx, clusterRole)
}

func (c *TargetConfigReconciler) manageCustomResources(ctx context.Context, specAnnotations map[string]string) error {
	crdDir := "assets/kueue-operator/crds"

	files, err := bindata.AssetDir(crdDir)
	if err != nil {
		return fmt.Errorf("failed to read crd directory: %w", err)
	}

	var hash string
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
			klog.V(3).Infof("Skipping installation of alpha CRD: %s", required.Name)
			continue
		}

		required.Annotations = cert.InjectCertAnnotation(required.GetAnnotations(), c.operatorNamespace)

		// Update conversion webhook namespace if it exists
		if required.Spec.Conversion != nil && required.Spec.Conversion.Strategy == apiextensionsv1.WebhookConverter {
			if required.Spec.Conversion.Webhook != nil &&
				required.Spec.Conversion.Webhook.ClientConfig != nil &&
				required.Spec.Conversion.Webhook.ClientConfig.Service != nil {
				required.Spec.Conversion.Webhook.ClientConfig.Service.Namespace = c.operatorNamespace
			}
		}

		crd, _, err := c.applyCustomResourceDefinitionWithCache(ctx, required)
		if err != nil {
			return err
		}
		hash, err = computeSpecHash(crd.Spec)
		if err != nil {
			return fmt.Errorf("failed to hash CRD spec: %w", err)
		}
		specAnnotations["crd/"+crd.Name] = hash
	}
	return nil
}

func (c *TargetConfigReconciler) manageDeployment(ctx context.Context, kueueoperator *kueuev1.Kueue, specAnnotations map[string]string, ownerReference metav1.OwnerReference) (*appsv1.Deployment, bool, error) {
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
	// Replace the visibility volume with the secret volume.
	for i, volume := range required.Spec.Template.Spec.Volumes {
		if volume.Name == "visibility" {
			required.Spec.Template.Spec.Volumes[i].EmptyDir = nil
			required.Spec.Template.Spec.Volumes[i].VolumeSource = v1.VolumeSource{
				Secret: &v1.SecretVolumeSource{
					SecretName: "kueue-visibility-server-cert",
					Optional:   ptr.To(false),
					Items: []v1.KeyToPath{
						{
							Key:  "ca.crt",
							Path: "ca.crt",
						},
						{
							Key:  "tls.crt",
							Path: "tls.crt",
						},
						{
							Key:  "tls.key",
							Path: "tls.key",
						},
					},
				},
			}
			break
		}
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

	required.Spec.Template.Spec.PriorityClassName = "system-cluster-critical"
	required.Spec.Template.Spec.Containers[0].Resources = v1.ResourceRequirements{
		Requests: v1.ResourceList{
			v1.ResourceCPU:    resource.MustParse("500m"),
			v1.ResourceMemory: resource.MustParse("512Mi"),
		},
	}

	required.Spec.Template.Spec.Containers[0].Image = c.kueueImage

	// Determine desired log level
	var logLevel int
	switch kueueoperator.Spec.LogLevel {
	case operatorv1.Normal:
		logLevel = 2
	case operatorv1.Debug:
		logLevel = 4
	case operatorv1.Trace:
		logLevel = 6
	case operatorv1.TraceAll:
		logLevel = 8
	default:
		logLevel = 2
	}

	// Search for existing --zap-log-level argument and replace it, or add if not found
	zapLogArg := fmt.Sprintf("--zap-log-level=%d", logLevel)
	found := false
	for i, arg := range required.Spec.Template.Spec.Containers[0].Args {
		if strings.HasPrefix(arg, "--zap-log-level=") {
			required.Spec.Template.Spec.Containers[0].Args[i] = zapLogArg
			found = true
			break
		}
	}
	if !found {
		required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, zapLogArg)
	}

	resourcemerge.MergeMap(ptr.To(false), &required.Spec.Template.Annotations, specAnnotations)

	deploy, updated, err := c.applyDeploymentWithCache(ctx,
		required,
		resourcemerge.ExpectedDeploymentGeneration(required, kueueoperator.Status.Generations))
	if err != nil {
		klog.InfoS("Deployment error", "Deployment", deploy)
		return nil, false, err
	}
	if updated {
		klog.V(2).Infof("Deployment %s/%s was updated (generation: %d)", deploy.Namespace, deploy.Name, deploy.Generation)
		resourcemerge.SetDeploymentGeneration(&kueueoperator.Status.Generations, deploy)
	} else {
		klog.V(4).Infof("Deployment %s/%s unchanged (generation: %d)", required.Namespace, required.Name, required.Generation)
	}
	return deploy, updated, err
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
						"apiVersion":         "kueue.openshift.io/v1",
						"kind":               "Kueue",
						"name":               kueue.Name,
						"uid":                string(kueue.UID),
						"controller":         false,
						"blockOwnerDeletion": false,
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

func (c *TargetConfigReconciler) manageCertificateCR(ctx context.Context, kueue *kueuev1.Kueue, dnsNames []interface{}, commonName, secretName, certificateName string) (*unstructured.Unstructured, bool, error) {
	gvr := schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "certificates",
	}
	required := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cert-manager.io/v1",
			"kind":       "Certificate",
			"metadata": map[string]interface{}{
				"ownerReferences": []interface{}{
					map[string]interface{}{
						"apiVersion":         "kueue.openshift.io/v1",
						"kind":               "Kueue",
						"name":               kueue.Name,
						"uid":                string(kueue.UID),
						"controller":         false,
						"blockOwnerDeletion": false,
					},
				},
				"name":      certificateName,
				"namespace": c.operatorNamespace,
			},
			"spec": map[string]interface{}{
				"dnsNames": dnsNames,
				"issuerRef": map[string]interface{}{
					"kind": "Issuer",
					"name": "selfsigned",
				},
				"secretName": secretName,
			},
		},
	}
	if commonName != "" {
		required.Object["spec"].(map[string]interface{})["commonName"] = commonName
	}

	// ApplyUnstructuredResourceImproved handles caching internally with resourceCache
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
					"app.kubernetes.io/component": "metrics-service",
					"app.kubernetes.io/name":      "kueue",
				},
			},
			Endpoints: []monitoringv1.Endpoint{
				{
					Interval:        "30s",
					Path:            "/metrics",
					Port:            "https", // Name of the port you want to monitor
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

// applyClusterRoleWithCache wraps ApplyClusterRole with caching support to reduce API server calls
// Uses informer lister to get cached data instead of live GET calls
func (c *TargetConfigReconciler) applyClusterRoleWithCache(ctx context.Context,
	required *rbacv1.ClusterRole,
) (*rbacv1.ClusterRole, bool, error) {
	// Try to get existing resource from informer cache (no API call!)
	existing, err := c.kubeInformer.Rbac().V1().ClusterRoles().Lister().Get(required.Name)
	if err != nil && !errors.IsNotFound(err) {
		return nil, false, err
	}

	// Check cache to see if we can skip this apply
	if existing != nil && c.resourceCache.SafeToSkipApply(required, existing) {
		klog.V(4).Infof("Skipping ClusterRole %q - no changes detected (cached)", required.Name)
		return existing, false, nil
	}

	// Cache miss or resource changed - proceed with apply
	role, updated, err := resourceapply.ApplyClusterRole(
		ctx,
		c.kubeClient.RbacV1(),
		c.eventRecorder,
		required,
	)

	// Update cache with the result
	if err == nil {
		c.resourceCache.UpdateCachedResourceMetadata(required, role)
	}

	return role, updated, err
}

// applyClusterRoleBindingWithCache wraps ApplyClusterRoleBinding with caching support
// Uses informer lister to get cached data instead of live GET calls
func (c *TargetConfigReconciler) applyClusterRoleBindingWithCache(ctx context.Context,
	required *rbacv1.ClusterRoleBinding,
) (*rbacv1.ClusterRoleBinding, bool, error) {
	// Try to get existing resource from informer cache (no API call!)
	existing, err := c.kubeInformer.Rbac().V1().ClusterRoleBindings().Lister().Get(required.Name)
	if err != nil && !errors.IsNotFound(err) {
		return nil, false, err
	}

	// Check cache to see if we can skip this apply
	if existing != nil && c.resourceCache.SafeToSkipApply(required, existing) {
		klog.V(4).Infof("Skipping ClusterRoleBinding %q - no changes detected (cached)", required.Name)
		return existing, false, nil
	}

	// Cache miss or resource changed - proceed with apply
	binding, updated, err := resourceapply.ApplyClusterRoleBinding(
		ctx,
		c.kubeClient.RbacV1(),
		c.eventRecorder,
		required,
	)

	// Update cache with the result
	if err == nil {
		c.resourceCache.UpdateCachedResourceMetadata(required, binding)
	}

	return binding, updated, err
}

// applyRoleWithCache wraps ApplyRole with caching support
// Uses informer lister to get cached data instead of live GET calls
func (c *TargetConfigReconciler) applyRoleWithCache(ctx context.Context,
	required *rbacv1.Role,
) (*rbacv1.Role, bool, error) {
	// Try to get existing resource from informer cache (no API call!)
	existing, err := c.kubeInformer.Rbac().V1().Roles().Lister().Roles(required.Namespace).Get(required.Name)
	if err != nil && !errors.IsNotFound(err) {
		return nil, false, err
	}

	// Check cache to see if we can skip this apply
	if existing != nil && c.resourceCache.SafeToSkipApply(required, existing) {
		klog.V(4).Infof("Skipping Role %s/%s - no changes detected (cached)",
			required.Namespace, required.Name)
		return existing, false, nil
	}

	// Cache miss or resource changed - proceed with apply
	role, updated, err := resourceapply.ApplyRole(
		ctx,
		c.kubeClient.RbacV1(),
		c.eventRecorder,
		required,
	)

	// Update cache with the result
	if err == nil {
		c.resourceCache.UpdateCachedResourceMetadata(required, role)
	}

	return role, updated, err
}

// applyRoleBindingWithCache wraps ApplyRoleBinding with caching support
// Uses informer lister to get cached data instead of live GET calls
func (c *TargetConfigReconciler) applyRoleBindingWithCache(ctx context.Context,
	required *rbacv1.RoleBinding,
) (*rbacv1.RoleBinding, bool, error) {
	// Try to get existing resource from informer cache (no API call!)
	existing, err := c.kubeInformer.Rbac().V1().RoleBindings().Lister().RoleBindings(required.Namespace).Get(required.Name)
	if err != nil && !errors.IsNotFound(err) {
		return nil, false, err
	}

	// Check cache to see if we can skip this apply
	if existing != nil && c.resourceCache.SafeToSkipApply(required, existing) {
		klog.V(4).Infof("Skipping RoleBinding %s/%s - no changes detected (cached)",
			required.Namespace, required.Name)
		return existing, false, nil
	}

	// Cache miss or resource changed - proceed with apply
	binding, updated, err := resourceapply.ApplyRoleBinding(
		ctx,
		c.kubeClient.RbacV1(),
		c.eventRecorder,
		required,
	)

	// Update cache with the result
	if err == nil {
		c.resourceCache.UpdateCachedResourceMetadata(required, binding)
	}

	return binding, updated, err
}

// applyNetworkPolicyWithCache wraps ApplyNetworkPolicy with caching support
// Uses informer lister to get cached data instead of live GET calls
func (c *TargetConfigReconciler) applyNetworkPolicyWithCache(ctx context.Context,
	required *networkingv1.NetworkPolicy,
) (*networkingv1.NetworkPolicy, bool, error) {
	// Try to get existing resource from informer cache (no API call!)
	existing, err := c.kubeInformersForNamespaces.InformersFor(required.Namespace).Networking().V1().NetworkPolicies().Lister().NetworkPolicies(required.Namespace).Get(required.Name)
	if err != nil && !errors.IsNotFound(err) {
		return nil, false, err
	}

	// Check cache to see if we can skip this apply
	if existing != nil && c.resourceCache.SafeToSkipApply(required, existing) {
		klog.V(4).Infof("Skipping NetworkPolicy %s/%s - no changes detected (cached)",
			required.Namespace, required.Name)
		return existing, false, nil
	}

	// Cache miss or resource changed - proceed with apply
	policy, updated, err := utilresourceapply.ApplyNetworkPolicy(
		ctx,
		c.kubeClient.NetworkingV1(),
		c.eventRecorder,
		required,
	)

	// Update cache with the result
	if err == nil {
		c.resourceCache.UpdateCachedResourceMetadata(required, policy)
	}

	return policy, updated, err
}

// applyCustomResourceDefinitionWithCache wraps ApplyCustomResourceDefinitionV1 with caching support
// Uses CRD informer lister to get cached data instead of live GET calls
func (c *TargetConfigReconciler) applyCustomResourceDefinitionWithCache(ctx context.Context,
	required *apiextensionsv1.CustomResourceDefinition,
) (*apiextensionsv1.CustomResourceDefinition, bool, error) {
	// Try to get existing resource from informer cache (no API call!)
	existing, err := c.crdInformer.Apiextensions().V1().CustomResourceDefinitions().Lister().Get(required.Name)
	if err != nil && !errors.IsNotFound(err) {
		return nil, false, err
	}

	// Preserve the caBundle from the existing CRD if it exists
	// This prevents us from overwriting cert-manager's injected caBundle
	if existing != nil &&
		existing.Spec.Conversion != nil &&
		existing.Spec.Conversion.Webhook != nil &&
		existing.Spec.Conversion.Webhook.ClientConfig != nil &&
		required.Spec.Conversion != nil &&
		required.Spec.Conversion.Webhook != nil &&
		required.Spec.Conversion.Webhook.ClientConfig != nil {
		required.Spec.Conversion.Webhook.ClientConfig.CABundle = existing.Spec.Conversion.Webhook.ClientConfig.CABundle
	}

	// Check cache to see if we can skip this apply
	if existing != nil && c.resourceCache.SafeToSkipApply(required, existing) {
		klog.V(4).Infof("Skipping CustomResourceDefinition %q - no changes detected (cached)", required.Name)
		return existing, false, nil
	}

	// Cache miss or resource changed - proceed with apply
	crd, updated, err := resourceapply.ApplyCustomResourceDefinitionV1(
		ctx,
		c.crdClient,
		c.eventRecorder,
		required,
	)

	// Update cache with the result
	if err == nil {
		c.resourceCache.UpdateCachedResourceMetadata(required, crd)
	}

	return crd, updated, err
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

// isResourceRegisteredCached checks if a CRD exists using the CRD informer cache
// This avoids API calls to the discovery client for CRD-based resources
func (c *TargetConfigReconciler) isResourceRegisteredCached(gvk schema.GroupVersionKind) (bool, error) {
	// Construct the CRD name from the GVK
	// CRD names follow the pattern: <plural>.<group>
	// We need to pluralize the kind (simple approach: lowercase + 's')
	plural := strings.ToLower(gvk.Kind) + "s"
	crdName := plural + "." + gvk.Group

	// Check if CRD exists in the informer cache
	_, err := c.crdInformer.Apiextensions().V1().CustomResourceDefinitions().Lister().Get(crdName)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// detectOpenShift detects whether the operator is running on OpenShift or vanilla Kubernetes (kind, etc.).
// It checks for the presence of OpenShift-specific API groups using the discovery client.
// This method should be fast and non-blocking (called at startup).
func (c *TargetConfigReconciler) detectOpenShift() bool {
	_, err := c.discoveryClient.ServerResourcesForGroupVersion(
		schema.GroupVersion{
			Group:   "project.openshift.io",
			Version: "v1",
		}.String(),
	)
	return err == nil
}

// applyDeploymentWithCache wraps ApplyDeployment with caching support to reduce API server calls
// Uses informer lister to get cached data instead of live GET calls
func (c *TargetConfigReconciler) applyDeploymentWithCache(ctx context.Context,
	required *appsv1.Deployment,
	expectedGeneration int64,
) (*appsv1.Deployment, bool, error) {
	// Try to get existing resource from informer cache (no API call!)
	existing, err := c.kubeInformersForNamespaces.InformersFor(required.Namespace).Apps().V1().Deployments().Lister().Deployments(required.Namespace).Get(required.Name)
	if err != nil && !errors.IsNotFound(err) {
		return nil, false, err
	}

	// Check cache to see if we can skip this apply
	if existing != nil && c.resourceCache.SafeToSkipApply(required, existing) {
		klog.V(4).Infof("Skipping Deployment %s/%s - no changes detected (cached)", required.Namespace, required.Name)
		return existing, false, nil
	}

	// Cache miss or resource changed - proceed with apply
	deployment, updated, err := resourceapply.ApplyDeployment(
		ctx,
		c.kubeClient.AppsV1(),
		c.eventRecorder,
		required,
		expectedGeneration,
	)

	// Update cache with the result
	if err == nil {
		c.resourceCache.UpdateCachedResourceMetadata(required, deployment)
	}

	return deployment, updated, err
}

// applyPriorityLevelConfigurationWithCache wraps ApplyPriorityLevelConfiguration with caching support
// Uses informer lister to get cached data instead of live GET calls
func (c *TargetConfigReconciler) applyPriorityLevelConfigurationWithCache(ctx context.Context,
	required *flowcontrolv1.PriorityLevelConfiguration,
) (*flowcontrolv1.PriorityLevelConfiguration, bool, error) {
	// Try to get existing resource from informer cache (no API call!)
	existing, err := c.kubeInformer.Flowcontrol().V1().PriorityLevelConfigurations().Lister().Get(required.Name)
	if err != nil && !errors.IsNotFound(err) {
		return nil, false, err
	}

	// Check cache to see if we can skip this apply
	if existing != nil && c.resourceCache.SafeToSkipApply(required, existing) {
		klog.V(4).Infof("Skipping PriorityLevelConfiguration %q - no changes detected (cached)", required.Name)
		return existing, false, nil
	}

	// Cache miss or resource changed - proceed with apply
	plc, updated, err := utilresourceapply.ApplyPriorityLevelConfiguration(
		ctx,
		c.kubeClient.FlowcontrolV1(),
		c.eventRecorder,
		required,
	)

	// Update cache with the result
	if err == nil {
		c.resourceCache.UpdateCachedResourceMetadata(required, plc)
	}

	return plc, updated, err
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
		return err
	}
	err = u.UnmarshalJSON(tmp)
	return err
}
