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
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	"github.com/openshift/library-go/pkg/crypto"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	configapi "sigs.k8s.io/kueue/apis/config/v1beta2"
)

// tlsGroupToCurveID maps OpenShift TLSGroup names (github.com/openshift/api
// config/v1 TLSGroup, gated by the TLSGroupPreferences feature gate) to the
// numeric IANA "TLS Supported Groups" identifiers expected by Kueue's
// TLSOptions.CurvePreferences field (kubernetes-sigs/kueue#11832), which map
// 1:1 with Go's crypto/tls.CurveID constants.
var tlsGroupToCurveID = map[configv1.TLSGroup]int32{
	configv1.TLSGroupSecP256r1:          23,   // tls.CurveP256
	configv1.TLSGroupSecP384r1:          24,   // tls.CurveP384
	configv1.TLSGroupSecP521r1:          25,   // tls.CurveP521
	configv1.TLSGroupX25519:             29,   // tls.X25519
	configv1.TLSGroupSecP256r1MLKEM768:  4587, // tls.SecP256r1MLKEM768
	configv1.TLSGroupX25519MLKEM768:     4588, // tls.X25519MLKEM768
	configv1.TLSGroupSecP384r1MLKEM1024: 4589, // tls.SecP384r1MLKEM1024
}

// FetchAPIServerTLSProfile fetches the TLS security profile from the APIServer CR.
// Returns nil if the cluster has no TLS profile configured (defaults to Intermediate).
func FetchAPIServerTLSProfile(ctx context.Context, client configclient.Interface) (*configv1.TLSSecurityProfile, error) {
	apiServer, err := client.ConfigV1().APIServers().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get APIServer CR: %w", err)
	}
	return apiServer.Spec.TLSSecurityProfile, nil
}

// TLSOptionsFromProfile resolves an OpenShift TLSSecurityProfile to kueue TLSOptions.
// If profile is nil, the Intermediate profile is used as the default.
// Cipher suites are converted from OpenSSL names to IANA format, and groups
// (supported TLS key-exchange curves) are converted to the numeric IANA
// Supported Group IDs expected by Kueue's CurvePreferences field.
// Returns the TLS options, a list of cipher suite names that could not be
// mapped to IANA format (unmapped ciphers), a list of group names that could
// not be mapped to a curve ID (unmapped groups), and an error if the profile
// uses a TLS version below 1.2, which is not supported by Kueue.
func TLSOptionsFromProfile(profile *configv1.TLSSecurityProfile) (*configapi.TLSOptions, []string, []string, error) {
	profileSpec, err := getProfileSpec(profile)
	if err != nil {
		return nil, nil, nil, err
	}

	if profileSpec.MinTLSVersion == configv1.VersionTLS10 || profileSpec.MinTLSVersion == configv1.VersionTLS11 {
		profileType := "Custom"
		if profile != nil {
			profileType = string(profile.Type)
		}
		return nil, nil, nil, fmt.Errorf("TLS profile %q uses minimum version %s which is not supported by Kueue (minimum supported: VersionTLS12)", profileType, profileSpec.MinTLSVersion)
	}

	opts := &configapi.TLSOptions{
		MinVersion: string(profileSpec.MinTLSVersion),
	}

	// TLS 1.3 cipher suites are not configurable in Go's crypto/tls.
	// Only set cipher suites for TLS 1.2 and below.
	var unmappedCiphers []string
	if profileSpec.MinTLSVersion != configv1.VersionTLS13 {
		opts.CipherSuites = crypto.OpenSSLToIANACipherSuites(profileSpec.Ciphers)
		unmappedCiphers = findUnmappedCiphers(profileSpec.Ciphers)
	}

	// Groups (curve preferences) apply regardless of TLS version.
	var unmappedGroups []string
	if len(profileSpec.Groups) > 0 {
		opts.CurvePreferences, unmappedGroups = convertGroupsToCurvePreferences(profileSpec.Groups)
	}

	return opts, unmappedCiphers, unmappedGroups, nil
}

// findUnmappedCiphers returns cipher suite names from the input that cannot be
// mapped from OpenSSL to IANA format. It identifies each cipher individually by
// checking whether OpenSSLToIANACipherSuites produces a result for it.
func findUnmappedCiphers(ciphers []string) []string {
	var unmapped []string
	for _, c := range ciphers {
		if mapped := crypto.OpenSSLToIANACipherSuites([]string{c}); len(mapped) == 0 {
			unmapped = append(unmapped, c)
		}
	}
	return unmapped
}

// convertGroupsToCurvePreferences converts a list of OpenShift TLSGroup names
// to their corresponding numeric IANA TLS Supported Group IDs, as expected by
// Kueue's TLSOptions.CurvePreferences. Groups that cannot be mapped are
// returned separately so callers can surface a warning without failing.
func convertGroupsToCurvePreferences(groups []configv1.TLSGroup) ([]int32, []string) {
	var curves []int32
	var unmapped []string
	for _, g := range groups {
		if id, ok := tlsGroupToCurveID[g]; ok {
			curves = append(curves, id)
		} else {
			unmapped = append(unmapped, string(g))
		}
	}
	return curves, unmapped
}

// getProfileSpec resolves a TLSSecurityProfile to its TLSProfileSpec.
func getProfileSpec(profile *configv1.TLSSecurityProfile) (*configv1.TLSProfileSpec, error) {
	if profile == nil {
		return configv1.TLSProfiles[configv1.TLSProfileIntermediateType], nil
	}

	if profile.Type == configv1.TLSProfileCustomType {
		if profile.Custom == nil {
			return nil, fmt.Errorf("custom TLS profile specified but custom configuration is nil")
		}
		return &profile.Custom.TLSProfileSpec, nil
	}

	spec, ok := configv1.TLSProfiles[profile.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported TLS profile type: %q", profile.Type)
	}
	return spec, nil
}
