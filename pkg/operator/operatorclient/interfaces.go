package operatorclient

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/apiserver/jsonpatch"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	applyconfiguration "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	kueueapplyconfiguration "github.com/openshift/kueue-operator/pkg/generated/applyconfiguration/kueueoperator/v1alpha1"
	operatorconfigclientv1alpha1 "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned/typed/kueueoperator/v1alpha1"
	"github.com/openshift/kueue-operator/pkg/namespace"
)

const (
	OperatorConfigName = "cluster"
	OperandName        = "kueue"
)

var _ v1helpers.OperatorClient = &KueueClient{}

type KueueClient struct {
	Ctx            context.Context
	SharedInformer cache.SharedIndexInformer
	OperatorClient operatorconfigclientv1alpha1.KueueV1alpha1Interface
}

func (c KueueClient) Informer() cache.SharedIndexInformer {
	return c.SharedInformer
}

func (c KueueClient) GetOperatorState() (spec *operatorv1.OperatorSpec, status *operatorv1.OperatorStatus, resourceVersion string, err error) {
	instance, err := c.OperatorClient.Kueues(namespace.GetNamespace()).Get(c.Ctx, OperatorConfigName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, "", err
	}
	return &instance.Spec.OperatorSpec, &instance.Status.OperatorStatus, instance.ResourceVersion, nil
}

func (c KueueClient) PatchOperatorStatus(ctx context.Context, patch *jsonpatch.PatchSet) error {
	return nil
}

func (c KueueClient) GetOperatorStateWithQuorum(ctx context.Context) (*operatorv1.OperatorSpec, *operatorv1.OperatorStatus, string, error) {
	return c.GetOperatorState()
}

func (c *KueueClient) UpdateOperatorSpec(ctx context.Context, resourceVersion string, spec *operatorv1.OperatorSpec) (out *operatorv1.OperatorSpec, newResourceVersion string, err error) {
	original, err := c.OperatorClient.Kueues(namespace.GetNamespace()).Get(ctx, OperatorConfigName, metav1.GetOptions{})
	if err != nil {
		return nil, "", err
	}
	copy := original.DeepCopy()
	copy.ResourceVersion = resourceVersion
	copy.Spec.OperatorSpec = *spec

	ret, err := c.OperatorClient.Kueues(namespace.GetNamespace()).Update(ctx, copy, v1.UpdateOptions{})
	if err != nil {
		return nil, "", err
	}

	return &ret.Spec.OperatorSpec, ret.ResourceVersion, nil
}

func (c *KueueClient) UpdateOperatorStatus(ctx context.Context, resourceVersion string, status *operatorv1.OperatorStatus) (out *operatorv1.OperatorStatus, err error) {
	original, err := c.OperatorClient.Kueues(namespace.GetNamespace()).Get(ctx, OperatorConfigName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	copy := original.DeepCopy()
	copy.ResourceVersion = resourceVersion
	copy.Status.OperatorStatus = *status

	ret, err := c.OperatorClient.Kueues(namespace.GetNamespace()).UpdateStatus(ctx, copy, v1.UpdateOptions{})
	if err != nil {
		return nil, err
	}
	return &ret.Status.OperatorStatus, nil
}

func (c *KueueClient) GetObjectMeta() (meta *metav1.ObjectMeta, err error) {
	instance, err := c.OperatorClient.Kueues(namespace.GetNamespace()).Get(c.Ctx, OperatorConfigName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &instance.ObjectMeta, nil
}

func (c *KueueClient) ApplyOperatorSpec(ctx context.Context, fieldManager string, desiredConfiguration *applyconfiguration.OperatorSpecApplyConfiguration) error {
	if desiredConfiguration == nil {
		return fmt.Errorf("applyConfiguration must have a value")
	}

	desiredSpec := &kueueapplyconfiguration.KueueOperandSpecApplyConfiguration{
		OperatorSpecApplyConfiguration: *desiredConfiguration,
	}
	desired := kueueapplyconfiguration.Kueue(OperatorConfigName, namespace.GetNamespace())
	desired.WithSpec(desiredSpec)

	instance, err := c.OperatorClient.Kueues(namespace.GetNamespace()).Get(ctx, OperatorConfigName, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
	// do nothing and proceed with the apply
	case err != nil:
		return fmt.Errorf("unable to get operator configuration: %w", err)
	default:
		original, err := kueueapplyconfiguration.ExtractKueue(instance, fieldManager)
		if err != nil {
			return fmt.Errorf("unable to extract operator configuration from spec: %w", err)
		}
		if equality.Semantic.DeepEqual(original, desired) {
			return nil
		}
	}

	_, err = c.OperatorClient.Kueues(namespace.GetNamespace()).Apply(ctx, desired, v1.ApplyOptions{
		Force:        true,
		FieldManager: fieldManager,
	})
	if err != nil {
		return fmt.Errorf("unable to Apply for operator using fieldManager %q: %w", fieldManager, err)
	}

	return nil
}

func (c *KueueClient) ApplyOperatorStatus(ctx context.Context, fieldManager string, desiredConfiguration *applyconfiguration.OperatorStatusApplyConfiguration) error {
	if desiredConfiguration == nil {
		return fmt.Errorf("applyConfiguration must have a value")
	}

	desiredStatus := &kueueapplyconfiguration.KueueStatusApplyConfiguration{
		OperatorStatusApplyConfiguration: *desiredConfiguration,
	}
	desired := kueueapplyconfiguration.Kueue(OperatorConfigName, namespace.GetNamespace())
	desired.WithStatus(desiredStatus)

	instance, err := c.OperatorClient.Kueues(namespace.GetNamespace()).Get(ctx, OperatorConfigName, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		// do nothing and proceed with the apply
		v1helpers.SetApplyConditionsLastTransitionTime(clock.RealClock{}, &desired.Status.Conditions, nil)
	case err != nil:
		return fmt.Errorf("unable to get operator configuration: %w", err)
	default:
		original, err := kueueapplyconfiguration.ExtractKueueStatus(instance, fieldManager)
		if err != nil {
			return fmt.Errorf("unable to extract operator configuration from status: %w", err)
		}
		if equality.Semantic.DeepEqual(original, desired) {
			return nil
		}
		if original.Status != nil {
			v1helpers.SetApplyConditionsLastTransitionTime(clock.RealClock{}, &desired.Status.Conditions, original.Status.Conditions)
		} else {
			v1helpers.SetApplyConditionsLastTransitionTime(clock.RealClock{}, &desired.Status.Conditions, nil)
		}
	}

	_, err = c.OperatorClient.Kueues(namespace.GetNamespace()).ApplyStatus(ctx, desired, v1.ApplyOptions{
		Force:        true,
		FieldManager: fieldManager,
	})
	if err != nil {
		return fmt.Errorf("unable to ApplyStatus for operator using fieldManager %q: %w", fieldManager, err)
	}

	return nil
}
