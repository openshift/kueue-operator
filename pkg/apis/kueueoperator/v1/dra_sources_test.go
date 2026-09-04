package v1

import "testing"

func TestHasDRASources(t *testing.T) {
	counterResources := Resources{DeviceClassMappings: []DeviceClassMapping{{Sources: []DeviceClassSourceConfig{{Type: DeviceClassSourceTypeCounter}}}}}
	capacityResources := Resources{DeviceClassMappings: []DeviceClassMapping{{Sources: []DeviceClassSourceConfig{{Type: DeviceClassSourceTypeCapacity}}}}}
	noSourceResources := Resources{DeviceClassMappings: []DeviceClassMapping{{Name: "gpu.memory"}}}

	tests := map[string]struct {
		resources    Resources
		wantCounter  bool
		wantCapacity bool
	}{
		"counter source": {
			resources:    counterResources,
			wantCounter:  true,
			wantCapacity: false,
		},
		"capacity source": {
			resources:    capacityResources,
			wantCounter:  false,
			wantCapacity: true,
		},
		"no sources": {
			resources:    noSourceResources,
			wantCounter:  false,
			wantCapacity: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := HasCounterSources(tc.resources); got != tc.wantCounter {
				t.Fatalf("HasCounterSources() = %v, want %v", got, tc.wantCounter)
			}
			if got := HasCapacitySources(tc.resources); got != tc.wantCapacity {
				t.Fatalf("HasCapacitySources() = %v, want %v", got, tc.wantCapacity)
			}
		})
	}
}
