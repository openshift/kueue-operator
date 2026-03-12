/*
Copyright 2025.

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

package tlsprofile

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
)

func TestTLSOptionsFromProfile(t *testing.T) {
	tests := []struct {
		name               string
		profile            *configv1.TLSSecurityProfile
		expectedMinVersion string
		expectCiphers      bool
	}{
		{
			name:               "nil profile defaults to Intermediate",
			profile:            nil,
			expectedMinVersion: "VersionTLS12",
			expectCiphers:      true,
		},
		{
			name: "Intermediate profile",
			profile: &configv1.TLSSecurityProfile{
				Type:         configv1.TLSProfileIntermediateType,
				Intermediate: &configv1.IntermediateTLSProfile{},
			},
			expectedMinVersion: "VersionTLS12",
			expectCiphers:      true,
		},
		{
			name: "Modern profile",
			profile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileModernType,
				Modern: &configv1.ModernTLSProfile{},
			},
			expectedMinVersion: "VersionTLS13",
			expectCiphers:      true,
		},
		{
			name: "Custom profile",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
						MinTLSVersion: configv1.VersionTLS12,
					},
				},
			},
			expectedMinVersion: "VersionTLS12",
			expectCiphers:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := TLSOptionsFromProfile(tt.profile)
			if opts == nil {
				t.Fatal("expected non-nil TLSOptions")
			}
			if opts.MinVersion != tt.expectedMinVersion {
				t.Errorf("expected MinVersion %q, got %q", tt.expectedMinVersion, opts.MinVersion)
			}
			if tt.expectCiphers && len(opts.CipherSuites) == 0 {
				t.Error("expected non-empty CipherSuites")
			}
		})
	}
}

func TestGetProfileSpec(t *testing.T) {
	t.Run("unknown profile type falls back to Intermediate", func(t *testing.T) {
		profile := &configv1.TLSSecurityProfile{
			Type: configv1.TLSProfileType("Unknown"),
		}
		spec := getProfileSpec(profile)
		if string(spec.MinTLSVersion) != "VersionTLS12" {
			t.Errorf("expected Intermediate fallback (VersionTLS12), got %s", spec.MinTLSVersion)
		}
	})
}
