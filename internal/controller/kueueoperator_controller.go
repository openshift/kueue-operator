/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logr "sigs.k8s.io/controller-runtime/pkg/log"

	kueuev1beta1 "github.com/openshift/kueue-operator/api/v1beta1"
	"github.com/openshift/kueue-operator/internal/configmap"
	"github.com/openshift/kueue-operator/internal/deployment"
	"github.com/openshift/kueue-operator/internal/secret"
	"github.com/openshift/kueue-operator/internal/service"
)

// KueueReconciler reconciles a Kueue object
type KueueReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=operator.openshift.io ,resources=kueue,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=operator.openshift.io ,resources=kueue/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=operator.openshift.io ,resources=kueue/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the KueueOperator object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.3/pkg/reconcile
func (r *KueueReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logr.FromContext(ctx)
	// Fetch the KueueOperator instance
	kueueOperand := &kueuev1beta1.Kueue{}
	err := r.Get(ctx, req.NamespacedName, kueueOperand)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("Kueue Operator resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get Kueue Operator")
		return ctrl.Result{}, err
	}

	if kueueOperand.Spec.Kueue == nil {
		return ctrl.Result{}, nil
	}

	// Right now we will assume operator and kueue must be installed in the same namespace.

	namespace := kueueOperand.GetNamespace()
	serviceList := service.BuildService(namespace)

	if err := r.createServices(ctx, serviceList); err != nil {
		log.Error(err, "Kueue services unable to be created")
		return ctrl.Result{}, err
	}

	secret := secret.BuildSecret(namespace)

	if err := r.createSecret(ctx, secret); err != nil {
		log.Error(err, "Kueue secrets unable to be created")
		return ctrl.Result{}, err
	}

	// set new config map
	newCfgMap, err := configmap.BuildConfigMap(namespace, kueueOperand.Spec.Kueue.Config)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.createConfigMap(ctx, newCfgMap); err != nil {
		log.Error(err, "unable to create config map")
		return ctrl.Result{}, err
	}

	deployment := deployment.BuildDeployment(namespace, kueueOperand.Spec.Kueue.Image)

	if err := r.createDeployment(ctx, deployment); err != nil {
		log.Error(err, "Kueue deployment unable to be created")
		return ctrl.Result{}, err
	}

	if err := r.waitForDeploymentReady(ctx, deployment, 5*time.Minute); err != nil {
		log.Error(err, "Kueue deployment not ready")
		return ctrl.Result{}, err
	}
	kueueOperand.Status.KueueReady = true
	return ctrl.Result{}, r.Update(ctx, kueueOperand, &client.UpdateOptions{})

}

func (r *KueueReconciler) createConfigMap(ctx context.Context, cfgMap *corev1.ConfigMap) error {
	err := r.Get(ctx, types.NamespacedName{Namespace: cfgMap.GetNamespace(), Name: cfgMap.GetName()}, cfgMap, &client.GetOptions{})
	if errors.IsNotFound(err) {
		return r.Create(ctx, cfgMap, &client.CreateOptions{})
	}
	return nil
}

func (r *KueueReconciler) createServices(ctx context.Context, serviceList []*corev1.Service) error {
	log := logr.FromContext(ctx)
	for _, val := range serviceList {
		err := r.Get(ctx, types.NamespacedName{Namespace: val.Namespace, Name: val.Name}, val, &client.GetOptions{})
		if errors.IsNotFound(err) {
			if err := r.Create(ctx, val, &client.CreateOptions{}); err != nil {
				log.Info("Unable to create service", "Service", val)
				log.Error(err, "unable to create service")
				return nil
			}
		} else {
			return err
		}
	}
	return nil
}

func (r *KueueReconciler) createSecret(ctx context.Context, secret *corev1.Secret) error {
	log := logr.FromContext(ctx)
	err := r.Get(ctx, types.NamespacedName{Namespace: secret.Namespace, Name: secret.Name}, secret, &client.GetOptions{})
	if errors.IsNotFound(err) {
		if err := r.Create(ctx, secret, &client.CreateOptions{}); err != nil {
			log.Error(err, "unable to create secret")
			return err
		}
	}
	return nil
}

func (r *KueueReconciler) createDeployment(ctx context.Context, deployment *appsv1.Deployment) error {
	log := logr.FromContext(ctx)
	err := r.Get(ctx, types.NamespacedName{Namespace: deployment.Namespace, Name: deployment.Name}, deployment, &client.GetOptions{})
	if errors.IsNotFound(err) {
		if err := r.Create(ctx, deployment, &client.CreateOptions{}); err != nil {
			log.Error(err, "unable to create deployment")
			return err
		}
	}
	return nil
}

func (r *KueueReconciler) waitForDeploymentReady(ctx context.Context, deployment *appsv1.Deployment, timeout time.Duration) error {
	return wait.PollUntilContextCancel(ctx, timeout, true, func(ctx context.Context) (bool, error) {
		tempDep := &appsv1.Deployment{}
		err := r.Get(ctx, types.NamespacedName{Namespace: deployment.Namespace, Name: deployment.Name}, tempDep, &client.GetOptions{})
		if err != nil {
			return false, err
		}

		// Check if the deployment is ready
		if deployment.Status.ReadyReplicas == *deployment.Spec.Replicas {
			return true, nil
		}

		return false, nil
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *KueueReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kueuev1beta1.Kueue{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.Secret{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
