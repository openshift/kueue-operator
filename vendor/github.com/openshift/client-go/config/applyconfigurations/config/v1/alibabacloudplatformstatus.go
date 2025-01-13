// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

// AlibabaCloudPlatformStatusApplyConfiguration represents a declarative configuration of the AlibabaCloudPlatformStatus type for use
// with apply.
type AlibabaCloudPlatformStatusApplyConfiguration struct {
	Region          *string                                     `json:"region,omitempty"`
	ResourceGroupID *string                                     `json:"resourceGroupID,omitempty"`
	ResourceTags    []AlibabaCloudResourceTagApplyConfiguration `json:"resourceTags,omitempty"`
}

// AlibabaCloudPlatformStatusApplyConfiguration constructs a declarative configuration of the AlibabaCloudPlatformStatus type for use with
// apply.
func AlibabaCloudPlatformStatus() *AlibabaCloudPlatformStatusApplyConfiguration {
	return &AlibabaCloudPlatformStatusApplyConfiguration{}
}

// WithRegion sets the Region field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Region field is set to the value of the last call.
func (b *AlibabaCloudPlatformStatusApplyConfiguration) WithRegion(value string) *AlibabaCloudPlatformStatusApplyConfiguration {
	b.Region = &value
	return b
}

// WithResourceGroupID sets the ResourceGroupID field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ResourceGroupID field is set to the value of the last call.
func (b *AlibabaCloudPlatformStatusApplyConfiguration) WithResourceGroupID(value string) *AlibabaCloudPlatformStatusApplyConfiguration {
	b.ResourceGroupID = &value
	return b
}

// WithResourceTags adds the given value to the ResourceTags field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the ResourceTags field.
func (b *AlibabaCloudPlatformStatusApplyConfiguration) WithResourceTags(values ...*AlibabaCloudResourceTagApplyConfiguration) *AlibabaCloudPlatformStatusApplyConfiguration {
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithResourceTags")
		}
		b.ResourceTags = append(b.ResourceTags, *values[i])
	}
	return b
}