package util

import (
	kueuev1 "github.com/openshift/kueue-operator/pkg/apis/kueueoperator/v1"
)

// HasSourceOfType returns true when the Kueue configuration contains at least one source of the specified type.
func HasSourceOfType(resources kueuev1.Resources, sourceType kueuev1.DeviceClassSourceType) bool {
	for _, m := range resources.DeviceClassMappings {
		for _, s := range m.Sources {
			switch sourceType {
			case kueuev1.DeviceClassSourceTypeCounter:
				if s.Type == kueuev1.DeviceClassSourceTypeCounter {
					return true
				}
			case kueuev1.DeviceClassSourceTypeCapacity:
				if s.Type == kueuev1.DeviceClassSourceTypeCapacity {
					return true
				}
			}
		}
	}
	return false
}
