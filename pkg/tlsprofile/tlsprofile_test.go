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
		name                    string
		profile                 *configv1.TLSSecurityProfile
		expectedMinVersion      string
		expectCiphers           bool
		expectError             bool
		expectedUnmappedCiphers []string
		expectedCurves          []int32
		expectedUnmappedGroups  []string
	}{
		{
			name:               "nil profile defaults to Intermediate",
			profile:            nil,
			expectedMinVersion: "VersionTLS12",
			expectCiphers:      true,
			expectedCurves:     []int32{4588, 29, 23, 24},
		},
		{
			name: "Intermediate profile",
			profile: &configv1.TLSSecurityProfile{
				Type:         configv1.TLSProfileIntermediateType,
				Intermediate: &configv1.IntermediateTLSProfile{},
			},
			expectedMinVersion: "VersionTLS12",
			expectCiphers:      true,
			expectedCurves:     []int32{4588, 29, 23, 24},
		},
		{
			name: "Modern profile has no cipher suites (TLS 1.3)",
			profile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileModernType,
				Modern: &configv1.ModernTLSProfile{},
			},
			expectedMinVersion: "VersionTLS13",
			expectCiphers:      false,
			expectedCurves:     []int32{4588, 29, 23, 24},
		},
		{
			name: "Custom profile with TLS 1.2",
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
		{
			name: "Custom profile with TLS 1.3 has no cipher suites",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						Ciphers:       []string{"TLS_AES_128_GCM_SHA256"},
						MinTLSVersion: configv1.VersionTLS13,
					},
				},
			},
			expectedMinVersion: "VersionTLS13",
			expectCiphers:      false,
		},
		{
			name: "Custom profile with invalid ciphers reports unmapped",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						Ciphers: []string{
							"ECDHE-RSA-AES128-GCM-SHA256",
							"TLS_ALICE_POLY1305_SHA256",
							"INVALID-CIPHER",
						},
						MinTLSVersion: configv1.VersionTLS12,
					},
				},
			},
			expectedMinVersion:      "VersionTLS12",
			expectCiphers:           true,
			expectedUnmappedCiphers: []string{"TLS_ALICE_POLY1305_SHA256", "INVALID-CIPHER"},
		},
		{
			name: "Custom profile with all invalid ciphers",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						Ciphers:       []string{"BOGUS-CIPHER-1", "BOGUS-CIPHER-2"},
						MinTLSVersion: configv1.VersionTLS12,
					},
				},
			},
			expectedMinVersion:      "VersionTLS12",
			expectCiphers:           false,
			expectedUnmappedCiphers: []string{"BOGUS-CIPHER-1", "BOGUS-CIPHER-2"},
		},
		{
			name: "Custom profile with groups maps to curve preferences",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
						MinTLSVersion: configv1.VersionTLS12,
						Groups:        []configv1.TLSGroup{configv1.TLSGroupX25519, configv1.TLSGroupSecP256r1},
					},
				},
			},
			expectedMinVersion: "VersionTLS12",
			expectCiphers:      true,
			expectedCurves:     []int32{29, 23},
		},
		{
			name: "Custom profile with invalid group reports unmapped",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
						MinTLSVersion: configv1.VersionTLS12,
						Groups:        []configv1.TLSGroup{configv1.TLSGroupX25519, configv1.TLSGroup("bogus-group")},
					},
				},
			},
			expectedMinVersion:     "VersionTLS12",
			expectCiphers:          true,
			expectedCurves:         []int32{29},
			expectedUnmappedGroups: []string{"bogus-group"},
		},
		{
			name: "Old profile returns error (TLS 1.0 unsupported)",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
				Old:  &configv1.OldTLSProfile{},
			},
			expectError: true,
		},
		{
			name: "Custom profile with TLS 1.0 returns error",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
						MinTLSVersion: configv1.VersionTLS10,
					},
				},
			},
			expectError: true,
		},
		{
			name: "Custom profile with TLS 1.1 returns error",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
						MinTLSVersion: configv1.VersionTLS11,
					},
				},
			},
			expectError: true,
		},
		{
			name: "Custom profile with nil Custom returns error",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
			},
			expectError: true,
		},
		{
			name: "Unknown profile type returns error",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileType("Unknown"),
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, unmappedCiphers, unmappedGroups, err := TLSOptionsFromProfile(tt.profile)
			if tt.expectError {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if opts == nil {
				t.Fatal("expected non-nil TLSOptions")
			}
			if opts.MinVersion != tt.expectedMinVersion {
				t.Errorf("expected MinVersion %q, got %q", tt.expectedMinVersion, opts.MinVersion)
			}
			if tt.expectCiphers && len(opts.CipherSuites) == 0 {
				t.Error("expected non-empty CipherSuites")
			}
			if !tt.expectCiphers && len(opts.CipherSuites) != 0 {
				t.Errorf("expected empty CipherSuites for TLS 1.3, got %v", opts.CipherSuites)
			}
			if len(tt.expectedUnmappedCiphers) != len(unmappedCiphers) {
				t.Errorf("expected %d unmapped ciphers, got %d: %v", len(tt.expectedUnmappedCiphers), len(unmappedCiphers), unmappedCiphers)
			} else {
				for i, expected := range tt.expectedUnmappedCiphers {
					if unmappedCiphers[i] != expected {
						t.Errorf("expected unmapped cipher %d to be %q, got %q", i, expected, unmappedCiphers[i])
					}
				}
			}
			if len(tt.expectedCurves) != len(opts.CurvePreferences) {
				t.Errorf("expected %d curve preferences, got %d: %v", len(tt.expectedCurves), len(opts.CurvePreferences), opts.CurvePreferences)
			} else {
				for i, expected := range tt.expectedCurves {
					if opts.CurvePreferences[i] != expected {
						t.Errorf("expected curve preference %d to be %d, got %d", i, expected, opts.CurvePreferences[i])
					}
				}
			}
			if len(tt.expectedUnmappedGroups) != len(unmappedGroups) {
				t.Errorf("expected %d unmapped groups, got %d: %v", len(tt.expectedUnmappedGroups), len(unmappedGroups), unmappedGroups)
			} else {
				for i, expected := range tt.expectedUnmappedGroups {
					if unmappedGroups[i] != expected {
						t.Errorf("expected unmapped group %d to be %q, got %q", i, expected, unmappedGroups[i])
					}
				}
			}
		})
	}
}

func TestGetProfileSpec(t *testing.T) {
	t.Run("unknown profile type returns error", func(t *testing.T) {
		profile := &configv1.TLSSecurityProfile{
			Type: configv1.TLSProfileType("Unknown"),
		}
		_, err := getProfileSpec(profile)
		if err == nil {
			t.Error("expected error for unknown profile type, got nil")
		}
	})

	t.Run("custom profile with nil Custom returns error", func(t *testing.T) {
		profile := &configv1.TLSSecurityProfile{
			Type: configv1.TLSProfileCustomType,
		}
		_, err := getProfileSpec(profile)
		if err == nil {
			t.Error("expected error for custom profile with nil Custom, got nil")
		}
	})
}
