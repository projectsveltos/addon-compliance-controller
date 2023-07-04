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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"

	"github.com/projectsveltos/addon-compliance-controller/controllers"
	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

var _ = Describe("AddonCompliance Controller", func() {
	It("HasEntryForCluster returns true only when entry is present", func() {
		controllers.Reset()
		manager := controllers.GetManager()

		cluster := &corev1.ObjectReference{
			Namespace:  randomString(),
			Name:       randomString(),
			Kind:       libsveltosv1alpha1.SveltosClusterKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		Expect(manager.HasEntryForCluster(cluster)).To(BeFalse())
		s := &libsveltosset.Set{}
		m := manager.GetMap()
		(*m)[*cluster] = s

		Expect(manager.HasEntryForCluster(cluster)).To(BeTrue())
	})

	It("AddClusterEntry creates an entry for cluster", func() {
		controllers.Reset()
		manager := controllers.GetManager()

		cluster := &corev1.ObjectReference{
			Namespace:  randomString(),
			Name:       randomString(),
			Kind:       libsveltosv1alpha1.SveltosClusterKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		Expect(manager.HasEntryForCluster(cluster)).To(BeFalse())
		s := &libsveltosset.Set{}
		manager.AddClusterEntry(cluster, s)

		Expect(manager.HasEntryForCluster(cluster)).To(BeTrue())
	})

	It("RemoveClusterEntry removes an entry for cluster", func() {
		controllers.Reset()
		manager := controllers.GetManager()

		cluster := &corev1.ObjectReference{
			Namespace:  randomString(),
			Name:       randomString(),
			Kind:       libsveltosv1alpha1.SveltosClusterKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		s := &libsveltosset.Set{}
		m := manager.GetMap()
		(*m)[*cluster] = s

		Expect(manager.HasEntryForCluster(cluster)).To(BeTrue())
		manager.RemoveClusterEntry(cluster)
		Expect(manager.HasEntryForCluster(cluster)).To(BeFalse())
	})

	It("RemoveAddonCompliance removes AddonCompliance properly", func() {
		controllers.Reset()
		manager := controllers.GetManager()

		cluster1 := &corev1.ObjectReference{
			Namespace:  randomString(),
			Name:       randomString(),
			Kind:       libsveltosv1alpha1.SveltosClusterKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		cluster2 := &corev1.ObjectReference{
			Namespace:  randomString(),
			Name:       randomString(),
			Kind:       libsveltosv1alpha1.SveltosClusterKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		addonConstraint := &corev1.ObjectReference{
			Name:       randomString(),
			Kind:       libsveltosv1alpha1.AddonComplianceKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		m := manager.GetMap()
		emptytSet := libsveltosset.Set{}
		(*m)[*cluster1] = &emptytSet
		s := libsveltosset.Set{}
		s.Insert(addonConstraint)
		(*m)[*cluster2] = &s

		Expect(manager.GetNumberOfAddonCompliance(cluster1)).To(Equal(0))
		Expect(manager.GetNumberOfAddonCompliance(cluster2)).To(Equal(1))

		// addonConstraint was a match only for cluster2. So expect
		// cluster2 to be present in result.
		result := manager.RemoveAddonCompliance(addonConstraint)
		Expect(len(result)).To(Equal(1))
		Expect(result).To(ContainElement(cluster2))

		m = manager.GetMap()
		Expect((*m)[*cluster2].Len()).To(BeZero())
	})

	It("GetNumberOfAddonCompliance returns number of addoncompliance yet to be evaluated", func() {
		controllers.Reset()
		manager := controllers.GetManager()

		cluster := &corev1.ObjectReference{
			Namespace:  randomString(),
			Name:       randomString(),
			Kind:       libsveltosv1alpha1.SveltosClusterKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		Expect(manager.GetNumberOfAddonCompliance(cluster)).To(BeZero())

		addonConstraint1 := &corev1.ObjectReference{
			Name:       randomString(),
			Kind:       libsveltosv1alpha1.AddonComplianceKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		m := manager.GetMap()
		(*m)[*cluster] = &libsveltosset.Set{}
		s := libsveltosset.Set{}
		(*m)[*cluster] = &s

		s.Insert(addonConstraint1)
		Expect(manager.GetNumberOfAddonCompliance(cluster)).To(Equal(1))

		addonConstraint2 := &corev1.ObjectReference{
			Name:       randomString(),
			Kind:       libsveltosv1alpha1.AddonComplianceKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		s.Insert(addonConstraint2)
		Expect(manager.GetNumberOfAddonCompliance(cluster)).To(Equal(2))

		s.Erase(addonConstraint1)
		Expect(manager.GetNumberOfAddonCompliance(cluster)).To(Equal(1))

		s.Erase(addonConstraint2)
		Expect(manager.GetNumberOfAddonCompliance(cluster)).To(BeZero())
	})
})
