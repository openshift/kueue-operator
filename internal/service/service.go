/*
Copyright 2024.

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

package service

import (
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func BuildService(namespace string) []*corev1.Service {
	ret := []*corev1.Service{}
	ret = append(ret,
		&corev1.Service{
			ObjectMeta: v1.ObjectMeta{
				Labels: map[string]string{
					"app.kubernetes.io/component": "controller",
					"app.kubernetes.io/name":      "kueue",
					"control-plane":               "controller-manager",
				},
				Name:      "kueue-controller-manager-metrics-service",
				Namespace: namespace,
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{
					{
						Name:       "https",
						Port:       8443,
						Protocol:   corev1.ProtocolTCP,
						TargetPort: intstr.FromString("https"),
					},
				},
				Selector: map[string]string{"control-plane": "controller-manager"}},
		},
		&corev1.Service{
			ObjectMeta: v1.ObjectMeta{
				Labels: map[string]string{
					"app.kubernetes.io/component": "controller",
					"app.kubernetes.io/name":      "kueue",
					"control-plane":               "controller-manager",
				},
				Name:      "kueue-visibility-server",
				Namespace: namespace,
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{
					{
						Name:       "https",
						Port:       443,
						Protocol:   corev1.ProtocolTCP,
						TargetPort: intstr.FromInt32(8082),
					},
				},
				Selector: map[string]string{"control-plane": "controller-manager"}},
		},
		&corev1.Service{
			ObjectMeta: v1.ObjectMeta{
				Labels: map[string]string{
					"app.kubernetes.io/component": "controller",
					"app.kubernetes.io/name":      "kueue",
					"control-plane":               "controller-manager",
				},
				Name:      "kueue-webhook-service",
				Namespace: namespace,
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{
					{
						Name:       "https",
						Port:       443,
						Protocol:   corev1.ProtocolTCP,
						TargetPort: intstr.FromInt32(9443),
					},
				},
				Selector: map[string]string{"control-plane": "controller-manager"}},
		},
	)
	return ret
}
