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

package testutils

import (
	"fmt"
	"os"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	kueueclient "github.com/openshift/kueue-operator/pkg/generated/clientset/versioned"
	apiregistrationv1client "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	upstreamkueueclient "sigs.k8s.io/kueue/client-go/clientset/versioned"
	visibilityv1beta1 "sigs.k8s.io/kueue/client-go/clientset/versioned/typed/visibility/v1beta1"
)

type TestClients struct {
	GenericClient         client.Client
	KubeClient            *kubernetes.Clientset
	APIExtClient          *apiextv1.ApiextensionsV1Client
	KueueClient           *kueueclient.Clientset
	UpstreamKueueClient   *upstreamkueueclient.Clientset
	DynamicClient         dynamic.Interface
	ApiregistrationClient *apiregistrationv1client.ApiregistrationV1Client
	RestConfig            *rest.Config
}

func NewTestClients() *TestClients {
	kubeconfig := os.Getenv("KUBECONFIG")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		klog.Fatalf("Unable to build config: %v", err)
	}

	return &TestClients{
		GenericClient:         getGenericClient(config),
		KubeClient:            getKubeClient(config),
		APIExtClient:          getAPIExtClient(config),
		KueueClient:           getKueueClient(config),
		UpstreamKueueClient:   getUpstreamKueueClient(config),
		DynamicClient:         getDynamicClient(config),
		ApiregistrationClient: getApiregistrationClient(config),
		RestConfig:            config,
	}
}

func getKubeClient(config *rest.Config) *kubernetes.Clientset {
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Unable to build kube client: %v", err)
	}
	return client
}

func getAPIExtClient(config *rest.Config) *apiextv1.ApiextensionsV1Client {
	client, err := apiextv1.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Unable to build api ext client: %v", err)
	}
	return client
}

func getKueueClient(config *rest.Config) *kueueclient.Clientset {
	client, err := kueueclient.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Unable to build kueue client: %v", err)
	}
	return client
}

func getGenericClient(config *rest.Config) client.Client {
	client, err := client.New(config, client.Options{})
	if err != nil {
		klog.Fatalf("Unable to build generic kubernetes client: %v", err)
	}
	return client
}

func getUpstreamKueueClient(config *rest.Config) *upstreamkueueclient.Clientset {
	client, err := upstreamkueueclient.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Unable to build upstream kueue client: %v", err)
	}
	return client
}

func GetVisibilityClient(user string) (visibilityv1beta1.VisibilityV1beta1Interface, error) {
	cfg, err := config.GetConfigWithContext("")
	if err != nil {
		return nil, fmt.Errorf("unable to get kubeconfig: %w", err)
	}

	if user != "" {
		cfg.Impersonate.UserName = user
	}

	kueueClient := getUpstreamKueueClient(cfg)
	return kueueClient.VisibilityV1beta1(), nil
}

func getDynamicClient(config *rest.Config) dynamic.Interface {
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Unable to build dynamic client: %v", err)
	}
	return client
}

func getApiregistrationClient(config *rest.Config) *apiregistrationv1client.ApiregistrationV1Client {
	client, err := apiregistrationv1client.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Unable to build apiregistration client: %v", err)
	}
	return client
}
