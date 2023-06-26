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

package scope_test

import (
	"context"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2/klogr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/projectsveltos/addon-compliance-controller/pkg/scope"
	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
)

const addonConstraintNamePrefix = "scope-"

var _ = Describe("AddonComplianceScope", func() {
	var addonConstraint *libsveltosv1alpha1.AddonCompliance
	var c client.Client

	BeforeEach(func() {
		addonConstraint = &libsveltosv1alpha1.AddonCompliance{
			ObjectMeta: metav1.ObjectMeta{
				Name: addonConstraintNamePrefix + randomString(),
			},
		}
		scheme := setupScheme()
		initObjects := []client.Object{addonConstraint}
		c = fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()
	})

	It("Return nil,error if AddonCompliance is not specified", func() {
		params := scope.AddonComplianceScopeParams{
			Client: c,
			Logger: klogr.New(),
		}

		scope, err := scope.NewAddonComplianceScope(params)
		Expect(err).To(HaveOccurred())
		Expect(scope).To(BeNil())
	})

	It("Return nil,error if client is not specified", func() {
		params := scope.AddonComplianceScopeParams{
			AddonCompliance: addonConstraint,
			Logger:          klogr.New(),
		}

		scope, err := scope.NewAddonComplianceScope(params)
		Expect(err).To(HaveOccurred())
		Expect(scope).To(BeNil())
	})

	It("Name returns AddonCompliance Name", func() {
		params := scope.AddonComplianceScopeParams{
			Client:          c,
			AddonCompliance: addonConstraint,
			Logger:          klogr.New(),
		}

		scope, err := scope.NewAddonComplianceScope(params)
		Expect(err).ToNot(HaveOccurred())
		Expect(scope).ToNot(BeNil())

		Expect(scope.Name()).To(Equal(addonConstraint.Name))
	})

	It("GetSelector returns AddonCompliance ClusterSelector", func() {
		addonConstraint.Spec.ClusterSelector = libsveltosv1alpha1.Selector("zone=east")
		params := scope.AddonComplianceScopeParams{
			Client:          c,
			AddonCompliance: addonConstraint,
			Logger:          klogr.New(),
		}

		scope, err := scope.NewAddonComplianceScope(params)
		Expect(err).ToNot(HaveOccurred())
		Expect(scope).ToNot(BeNil())

		Expect(scope.GetSelector()).To(Equal(string(addonConstraint.Spec.ClusterSelector)))
	})

	It("SetMatchingClusters sets AddonCompliance.Status.MatchingCluster", func() {
		params := scope.AddonComplianceScopeParams{
			Client:          c,
			AddonCompliance: addonConstraint,
			Logger:          klogr.New(),
		}

		scope, err := scope.NewAddonComplianceScope(params)
		Expect(err).ToNot(HaveOccurred())
		Expect(scope).ToNot(BeNil())

		matchingClusters := []corev1.ObjectReference{
			{
				Namespace: "t-" + randomString(),
				Name:      "c-" + randomString(),
			},
		}
		scope.SetMatchingClusterRefs(matchingClusters)
		Expect(reflect.DeepEqual(addonConstraint.Status.MatchingClusterRefs, matchingClusters)).To(BeTrue())
	})

	It("SetFailureMessage sets AddonCompliance.Status.FailureMessage", func() {
		params := scope.AddonComplianceScopeParams{
			Client:          c,
			AddonCompliance: addonConstraint,
			Logger:          klogr.New(),
		}

		scope, err := scope.NewAddonComplianceScope(params)
		Expect(err).ToNot(HaveOccurred())
		Expect(scope).ToNot(BeNil())

		failureMessage := randomString()
		scope.SetFailureMessage(&failureMessage)
		Expect(addonConstraint.Status.FailureMessage).ToNot(BeNil())
		Expect(reflect.DeepEqual(*addonConstraint.Status.FailureMessage, failureMessage)).To(BeTrue())

		scope.SetFailureMessage(nil)
		Expect(addonConstraint.Status.FailureMessage).To(BeNil())
	})

	It("Close updates AddonCompliance", func() {
		params := scope.AddonComplianceScopeParams{
			Client:          c,
			AddonCompliance: addonConstraint,
			Logger:          klogr.New(),
		}

		scope, err := scope.NewAddonComplianceScope(params)
		Expect(err).ToNot(HaveOccurred())
		Expect(scope).ToNot(BeNil())

		addonConstraint.Labels = map[string]string{"clusters": "hr"}
		Expect(scope.Close(context.TODO())).To(Succeed())

		currentAddonCompliance := &libsveltosv1alpha1.AddonCompliance{}
		Expect(c.Get(context.TODO(), types.NamespacedName{Name: addonConstraint.Name}, currentAddonCompliance)).To(Succeed())
		Expect(currentAddonCompliance.Labels).ToNot(BeNil())
		Expect(len(currentAddonCompliance.Labels)).To(Equal(1))
		v, ok := currentAddonCompliance.Labels["clusters"]
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("hr"))
	})

	It("UpdateLabels updates AddonCompliance labels with matching clusters", func() {
		params := scope.AddonComplianceScopeParams{
			Client:          c,
			AddonCompliance: addonConstraint,
			Logger:          klogr.New(),
		}

		scope, err := scope.NewAddonComplianceScope(params)
		Expect(err).ToNot(HaveOccurred())
		Expect(scope).ToNot(BeNil())

		matchingClusters := []corev1.ObjectReference{
			{Namespace: randomString(), Name: randomString(),
				Kind: libsveltosv1alpha1.SveltosClusterKind, APIVersion: libsveltosv1alpha1.GroupVersion.String()},
			{Namespace: randomString(), Name: randomString(),
				Kind: libsveltosv1alpha1.SveltosClusterKind, APIVersion: libsveltosv1alpha1.GroupVersion.String()},
		}
		scope.UpdateLabels(matchingClusters)
		Expect(scope.AddonCompliance.Labels).ToNot(BeNil())
		Expect(len(scope.AddonCompliance.Labels)).To(Equal(len(matchingClusters)))
	})
})
