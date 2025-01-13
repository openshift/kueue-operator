// Code generated by client-gen. DO NOT EDIT.

package v1

import (
	context "context"

	configv1 "github.com/openshift/api/config/v1"
	applyconfigurationsconfigv1 "github.com/openshift/client-go/config/applyconfigurations/config/v1"
	scheme "github.com/openshift/client-go/config/clientset/versioned/scheme"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	gentype "k8s.io/client-go/gentype"
)

// OperatorHubsGetter has a method to return a OperatorHubInterface.
// A group's client should implement this interface.
type OperatorHubsGetter interface {
	OperatorHubs() OperatorHubInterface
}

// OperatorHubInterface has methods to work with OperatorHub resources.
type OperatorHubInterface interface {
	Create(ctx context.Context, operatorHub *configv1.OperatorHub, opts metav1.CreateOptions) (*configv1.OperatorHub, error)
	Update(ctx context.Context, operatorHub *configv1.OperatorHub, opts metav1.UpdateOptions) (*configv1.OperatorHub, error)
	// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
	UpdateStatus(ctx context.Context, operatorHub *configv1.OperatorHub, opts metav1.UpdateOptions) (*configv1.OperatorHub, error)
	Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error
	DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error
	Get(ctx context.Context, name string, opts metav1.GetOptions) (*configv1.OperatorHub, error)
	List(ctx context.Context, opts metav1.ListOptions) (*configv1.OperatorHubList, error)
	Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error)
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (result *configv1.OperatorHub, err error)
	Apply(ctx context.Context, operatorHub *applyconfigurationsconfigv1.OperatorHubApplyConfiguration, opts metav1.ApplyOptions) (result *configv1.OperatorHub, err error)
	// Add a +genclient:noStatus comment above the type to avoid generating ApplyStatus().
	ApplyStatus(ctx context.Context, operatorHub *applyconfigurationsconfigv1.OperatorHubApplyConfiguration, opts metav1.ApplyOptions) (result *configv1.OperatorHub, err error)
	OperatorHubExpansion
}

// operatorHubs implements OperatorHubInterface
type operatorHubs struct {
	*gentype.ClientWithListAndApply[*configv1.OperatorHub, *configv1.OperatorHubList, *applyconfigurationsconfigv1.OperatorHubApplyConfiguration]
}

// newOperatorHubs returns a OperatorHubs
func newOperatorHubs(c *ConfigV1Client) *operatorHubs {
	return &operatorHubs{
		gentype.NewClientWithListAndApply[*configv1.OperatorHub, *configv1.OperatorHubList, *applyconfigurationsconfigv1.OperatorHubApplyConfiguration](
			"operatorhubs",
			c.RESTClient(),
			scheme.ParameterCodec,
			"",
			func() *configv1.OperatorHub { return &configv1.OperatorHub{} },
			func() *configv1.OperatorHubList { return &configv1.OperatorHubList{} },
		),
	}
}