/*
Copyright 2023. projectsveltos.io. All rights reserved.

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

package controllers_test

import (
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/projectsveltos/addon-compliance-controller/controllers"
	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

var _ = Describe("AddonComplianceTransformation map functions", func() {
	It("RequeueAddonComplianceForReference returns AddonCompliance referencing a given ConfigMap", func() {
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      randomString(),
				Namespace: randomString(),
			},
		}

		controllers.AddTypeInformationToObject(scheme, configMap)

		addonConstraint0 := &libsveltosv1alpha1.AddonCompliance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      randomString(),
				Namespace: randomString(),
			},
			Spec: libsveltosv1alpha1.AddonComplianceSpec{
				OpenAPIValidationRefs: []libsveltosv1alpha1.OpenAPIValidationRef{
					{
						Namespace: configMap.Namespace,
						Name:      configMap.Name,
						Kind:      string(libsveltosv1alpha1.ConfigMapReferencedResourceKind),
					},
				},
			},
		}

		addonConstraint1 := &libsveltosv1alpha1.AddonCompliance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      randomString(),
				Namespace: randomString(),
			},
			Spec: libsveltosv1alpha1.AddonComplianceSpec{
				OpenAPIValidationRefs: []libsveltosv1alpha1.OpenAPIValidationRef{
					{
						Namespace: randomString(),
						Name:      configMap.Name,
						Kind:      string(libsveltosv1alpha1.ConfigMapReferencedResourceKind),
					},
				},
			},
		}

		initObjects := []client.Object{
			configMap,
			addonConstraint0,
			addonConstraint1,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()

		reconciler := getAddonComplianceReconciler(c)

		set := libsveltosset.Set{}
		key := corev1.ObjectReference{APIVersion: configMap.APIVersion,
			Kind: string(libsveltosv1alpha1.ConfigMapReferencedResourceKind), Namespace: configMap.Namespace, Name: configMap.Name}

		set.Insert(&corev1.ObjectReference{APIVersion: libsveltosv1alpha1.GroupVersion.String(),
			Kind: libsveltosv1alpha1.AddonComplianceKind, Namespace: addonConstraint0.Namespace, Name: addonConstraint0.Name})
		reconciler.ReferenceMap[key] = &set

		requests := controllers.RequeueAddonComplianceForReference(reconciler, configMap)
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].Name).To(Equal(addonConstraint0.Name))
		Expect(requests[0].Namespace).To(Equal(addonConstraint0.Namespace))

		set.Insert(&corev1.ObjectReference{APIVersion: libsveltosv1alpha1.GroupVersion.String(),
			Kind: libsveltosv1alpha1.AddonComplianceKind, Namespace: addonConstraint1.Namespace, Name: addonConstraint1.Name})
		reconciler.ReferenceMap[key] = &set

		requests = controllers.RequeueAddonComplianceForReference(reconciler, configMap)
		Expect(requests).To(HaveLen(2))
		Expect(requests).To(ContainElement(
			reconcile.Request{NamespacedName: types.NamespacedName{Namespace: addonConstraint0.Namespace, Name: addonConstraint0.Name}}))
		Expect(requests).To(ContainElement(
			reconcile.Request{NamespacedName: types.NamespacedName{Namespace: addonConstraint1.Namespace, Name: addonConstraint1.Name}}))
	})
})

var _ = Describe("AddonComplianceTransformation map functions", func() {
	It("RequeueAddonComplianceForFluxSources returns AddonCompliance referencing a given GitRepository", func() {
		gitRepo := &sourcev1.GitRepository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      randomString(),
				Namespace: randomString(),
			},
		}

		controllers.AddTypeInformationToObject(scheme, gitRepo)

		addonConstraint0 := &libsveltosv1alpha1.AddonCompliance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      randomString(),
				Namespace: randomString(),
			},
			Spec: libsveltosv1alpha1.AddonComplianceSpec{
				OpenAPIValidationRefs: []libsveltosv1alpha1.OpenAPIValidationRef{
					{
						Namespace: gitRepo.Namespace,
						Name:      gitRepo.Name,
						Kind:      sourcev1.GitRepositoryKind,
					},
				},
			},
		}

		addonConstraint1 := &libsveltosv1alpha1.AddonCompliance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      randomString(),
				Namespace: randomString(),
			},
			Spec: libsveltosv1alpha1.AddonComplianceSpec{
				OpenAPIValidationRefs: []libsveltosv1alpha1.OpenAPIValidationRef{
					{
						Namespace: gitRepo.Namespace,
						Name:      randomString(),
						Kind:      sourcev1.GitRepositoryKind,
					},
				},
			},
		}

		initObjects := []client.Object{
			gitRepo,
			addonConstraint0,
			addonConstraint1,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()

		reconciler := getAddonComplianceReconciler(c)

		set := libsveltosset.Set{}
		key := corev1.ObjectReference{APIVersion: gitRepo.APIVersion,
			Kind: sourcev1.GitRepositoryKind, Namespace: gitRepo.Namespace, Name: gitRepo.Name}

		set.Insert(&corev1.ObjectReference{APIVersion: libsveltosv1alpha1.GroupVersion.String(),
			Kind: libsveltosv1alpha1.AddonComplianceKind, Namespace: addonConstraint0.Namespace, Name: addonConstraint0.Name})
		reconciler.ReferenceMap[key] = &set

		requests := controllers.RequeueAddonComplianceForReference(reconciler, gitRepo)
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].Name).To(Equal(addonConstraint0.Name))
		Expect(requests[0].Namespace).To(Equal(addonConstraint0.Namespace))

		set.Insert(&corev1.ObjectReference{APIVersion: libsveltosv1alpha1.GroupVersion.String(),
			Kind: libsveltosv1alpha1.AddonComplianceKind, Namespace: addonConstraint1.Namespace, Name: addonConstraint1.Name})
		reconciler.ReferenceMap[key] = &set

		requests = controllers.RequeueAddonComplianceForReference(reconciler, gitRepo)
		Expect(requests).To(HaveLen(2))
		Expect(requests).To(ContainElement(
			reconcile.Request{NamespacedName: types.NamespacedName{Namespace: addonConstraint0.Namespace, Name: addonConstraint0.Name}}))
		Expect(requests).To(ContainElement(
			reconcile.Request{NamespacedName: types.NamespacedName{Namespace: addonConstraint1.Namespace, Name: addonConstraint1.Name}}))
	})
})
