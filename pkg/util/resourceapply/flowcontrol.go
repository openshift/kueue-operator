package resourceapply

import (
	"context"
	"fmt"

	"github.com/openshift/library-go/pkg/operator/events"
	flowcontrolv1 "k8s.io/api/flowcontrol/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	flowcontrolclientv1 "k8s.io/client-go/kubernetes/typed/flowcontrol/v1"

	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcehelper"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"

	"k8s.io/klog/v2"
)

// TODO: move to library-go
var (
	flowcontrolScheme                = runtime.NewScheme()
	flowcontrolCodecs                = serializer.NewCodecFactory(flowcontrolScheme)
	priorityLevelConfigurationScheme = runtime.NewScheme()
	priorityLevelConfigurationCodecs = serializer.NewCodecFactory(priorityLevelConfigurationScheme)
)

func init() {
	if err := flowcontrolv1.AddToScheme(flowcontrolScheme); err != nil {
		panic(fmt.Errorf("failed to add to scheme: %w", err))
	}
	if err := flowcontrolv1.AddToScheme(priorityLevelConfigurationScheme); err != nil {
		panic(fmt.Errorf("failed to add to scheme: %w", err))
	}
}

func ReadFlowSchemaV1OrDie(bytes []byte) *flowcontrolv1.FlowSchema {
	obj, err := runtime.Decode(flowcontrolCodecs.UniversalDecoder(flowcontrolv1.SchemeGroupVersion), bytes)
	if err != nil {
		panic(fmt.Errorf("failed to decode raw bytes into FlowSchema object: %w", err))
	}
	return obj.(*flowcontrolv1.FlowSchema)
}

// ApplyFlowSchema applies the FlowSchema object specified in want to the
// cluster diff is true if there is a diff (between the current object on the
// cluster and the object specified in want) that needs to be applied.
func ApplyFlowSchema(ctx context.Context, getter flowcontrolclientv1.FlowSchemasGetter, recorder events.Recorder, want *flowcontrolv1.FlowSchema) (current *flowcontrolv1.FlowSchema, diff bool, err error) {
	client := getter.FlowSchemas()
	current, err = client.Get(ctx, want.Name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, false, err
		}

		copy := want.DeepCopy()
		current, err = client.Create(ctx, resourcemerge.WithCleanLabelsAndAnnotations(copy).(*flowcontrolv1.FlowSchema), metav1.CreateOptions{})
		resourcehelper.ReportCreateEvent(recorder, want, err)
		return current, false, err
	}

	copy := current.DeepCopy()
	resourcemerge.EnsureObjectMeta(&diff, &copy.ObjectMeta, want.ObjectMeta)
	if !diff && equality.Semantic.DeepEqual(current.Spec, want.Spec) {
		return current, false, nil
	}

	copy.Spec = *want.Spec.DeepCopy()
	if klog.V(2).Enabled() {
		klog.Infof("FlowSchema %q changes: %v", want.Name, resourceapply.JSONPatchNoError(current, copy))
	}

	current, err = client.Update(ctx, copy, metav1.UpdateOptions{})
	resourcehelper.ReportUpdateEvent(recorder, want, err)
	return current, true, err
}

func ReadPriorityLevelConfigurationV1OrDie(bytes []byte) *flowcontrolv1.PriorityLevelConfiguration {
	obj, err := runtime.Decode(priorityLevelConfigurationCodecs.UniversalDecoder(flowcontrolv1.SchemeGroupVersion), bytes)
	if err != nil {
		panic(fmt.Errorf("failed to decode raw bytes into PriorityLevelConfiguration object: %w", err))
	}
	return obj.(*flowcontrolv1.PriorityLevelConfiguration)
}

// ApplyPriorityLevelConfiguration applies the PriorityLevelConfiguration object specified in want to the
// cluster diff is true if there is a diff (between the current object on the
// cluster and the object specified in want) that needs to be applied.
func ApplyPriorityLevelConfiguration(ctx context.Context, getter flowcontrolclientv1.PriorityLevelConfigurationsGetter, recorder events.Recorder, want *flowcontrolv1.PriorityLevelConfiguration) (current *flowcontrolv1.PriorityLevelConfiguration, diff bool, err error) {
	client := getter.PriorityLevelConfigurations()
	current, err = client.Get(ctx, want.Name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, false, err
		}

		copy := want.DeepCopy()
		current, err = client.Create(ctx, resourcemerge.WithCleanLabelsAndAnnotations(copy).(*flowcontrolv1.PriorityLevelConfiguration), metav1.CreateOptions{})
		resourcehelper.ReportCreateEvent(recorder, want, err)
		return current, false, err
	}

	copy := current.DeepCopy()
	resourcemerge.EnsureObjectMeta(&diff, &copy.ObjectMeta, want.ObjectMeta)
	if !diff && equality.Semantic.DeepEqual(current.Spec, want.Spec) {
		return current, false, nil
	}

	copy.Spec = *want.Spec.DeepCopy()
	if klog.V(2).Enabled() {
		klog.Infof("PriorityLevelConfiguration %q changes: %v", want.Name, resourceapply.JSONPatchNoError(current, copy))
	}

	current, err = client.Update(ctx, copy, metav1.UpdateOptions{})
	resourcehelper.ReportUpdateEvent(recorder, want, err)
	return current, true, err
}
