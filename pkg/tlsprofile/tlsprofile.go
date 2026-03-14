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
// Cipher suites are converted from OpenSSL names to IANA format.
func TLSOptionsFromProfile(profile *configv1.TLSSecurityProfile) *configapi.TLSOptions {
	profileSpec := getProfileSpec(profile)
	ianaCiphers := crypto.OpenSSLToIANACipherSuites(profileSpec.Ciphers)

	return &configapi.TLSOptions{
		MinVersion:   string(profileSpec.MinTLSVersion),
		CipherSuites: ianaCiphers,
	}
}

// getProfileSpec resolves a TLSSecurityProfile to its TLSProfileSpec.
func getProfileSpec(profile *configv1.TLSSecurityProfile) *configv1.TLSProfileSpec {
	if profile == nil {
		return configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	}

	if profile.Type == configv1.TLSProfileCustomType && profile.Custom != nil {
		return &profile.Custom.TLSProfileSpec
	}

	spec, ok := configv1.TLSProfiles[profile.Type]
	if !ok {
		return configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	}
	return spec
}
