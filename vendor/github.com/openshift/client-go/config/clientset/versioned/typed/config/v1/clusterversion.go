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

// ClusterVersionsGetter has a method to return a ClusterVersionInterface.
// A group's client should implement this interface.
type ClusterVersionsGetter interface {
	ClusterVersions() ClusterVersionInterface
}

// ClusterVersionInterface has methods to work with ClusterVersion resources.
type ClusterVersionInterface interface {
	Create(ctx context.Context, clusterVersion *configv1.ClusterVersion, opts metav1.CreateOptions) (*configv1.ClusterVersion, error)
	Update(ctx context.Context, clusterVersion *configv1.ClusterVersion, opts metav1.UpdateOptions) (*configv1.ClusterVersion, error)
	// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
	UpdateStatus(ctx context.Context, clusterVersion *configv1.ClusterVersion, opts metav1.UpdateOptions) (*configv1.ClusterVersion, error)
	Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error
	DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error
	Get(ctx context.Context, name string, opts metav1.GetOptions) (*configv1.ClusterVersion, error)
	List(ctx context.Context, opts metav1.ListOptions) (*configv1.ClusterVersionList, error)
	Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error)
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (result *configv1.ClusterVersion, err error)
	Apply(ctx context.Context, clusterVersion *applyconfigurationsconfigv1.ClusterVersionApplyConfiguration, opts metav1.ApplyOptions) (result *configv1.ClusterVersion, err error)
	// Add a +genclient:noStatus comment above the type to avoid generating ApplyStatus().
	ApplyStatus(ctx context.Context, clusterVersion *applyconfigurationsconfigv1.ClusterVersionApplyConfiguration, opts metav1.ApplyOptions) (result *configv1.ClusterVersion, err error)
	ClusterVersionExpansion
}

// clusterVersions implements ClusterVersionInterface
type clusterVersions struct {
	*gentype.ClientWithListAndApply[*configv1.ClusterVersion, *configv1.ClusterVersionList, *applyconfigurationsconfigv1.ClusterVersionApplyConfiguration]
}

// newClusterVersions returns a ClusterVersions
func newClusterVersions(c *ConfigV1Client) *clusterVersions {
	return &clusterVersions{
		gentype.NewClientWithListAndApply[*configv1.ClusterVersion, *configv1.ClusterVersionList, *applyconfigurationsconfigv1.ClusterVersionApplyConfiguration](
			"clusterversions",
			c.RESTClient(),
			scheme.ParameterCodec,
			"",
			func() *configv1.ClusterVersion { return &configv1.ClusterVersion{} },
			func() *configv1.ClusterVersionList { return &configv1.ClusterVersionList{} },
		),
	}
}