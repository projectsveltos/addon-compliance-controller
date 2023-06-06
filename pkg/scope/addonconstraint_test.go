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

	"github.com/projectsveltos/addon-constraint-controller/pkg/scope"
	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
)

const addonConstraintNamePrefix = "scope-"

var _ = Describe("AddonConstraintScope", func() {
	var addonConstraint *libsveltosv1alpha1.AddonConstraint
	var c client.Client

	BeforeEach(func() {
		addonConstraint = &libsveltosv1alpha1.AddonConstraint{
			ObjectMeta: metav1.ObjectMeta{
				Name: addonConstraintNamePrefix + randomString(),
			},
		}
		scheme := setupScheme()
		initObjects := []client.Object{addonConstraint}
		c = fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()
	})

	It("Return nil,error if AddonConstraint is not specified", func() {
		params := scope.AddonConstraintScopeParams{
			Client: c,
			Logger: klogr.New(),
		}

		scope, err := scope.NewAddonConstraintScope(params)
		Expect(err).To(HaveOccurred())
		Expect(scope).To(BeNil())
	})

	It("Return nil,error if client is not specified", func() {
		params := scope.AddonConstraintScopeParams{
			AddonConstraint: addonConstraint,
			Logger:          klogr.New(),
		}

		scope, err := scope.NewAddonConstraintScope(params)
		Expect(err).To(HaveOccurred())
		Expect(scope).To(BeNil())
	})

	It("Name returns AddonConstraint Name", func() {
		params := scope.AddonConstraintScopeParams{
			Client:          c,
			AddonConstraint: addonConstraint,
			Logger:          klogr.New(),
		}

		scope, err := scope.NewAddonConstraintScope(params)
		Expect(err).ToNot(HaveOccurred())
		Expect(scope).ToNot(BeNil())

		Expect(scope.Name()).To(Equal(addonConstraint.Name))
	})

	It("GetSelector returns AddonConstraint ClusterSelector", func() {
		addonConstraint.Spec.ClusterSelector = libsveltosv1alpha1.Selector("zone=east")
		params := scope.AddonConstraintScopeParams{
			Client:          c,
			AddonConstraint: addonConstraint,
			Logger:          klogr.New(),
		}

		scope, err := scope.NewAddonConstraintScope(params)
		Expect(err).ToNot(HaveOccurred())
		Expect(scope).ToNot(BeNil())

		Expect(scope.GetSelector()).To(Equal(string(addonConstraint.Spec.ClusterSelector)))
	})

	It("SetMatchingClusters sets AddonConstraint.Status.MatchingCluster", func() {
		params := scope.AddonConstraintScopeParams{
			Client:          c,
			AddonConstraint: addonConstraint,
			Logger:          klogr.New(),
		}

		scope, err := scope.NewAddonConstraintScope(params)
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

	It("Close updates AddonConstraint", func() {
		params := scope.AddonConstraintScopeParams{
			Client:          c,
			AddonConstraint: addonConstraint,
			Logger:          klogr.New(),
		}

		scope, err := scope.NewAddonConstraintScope(params)
		Expect(err).ToNot(HaveOccurred())
		Expect(scope).ToNot(BeNil())

		addonConstraint.Labels = map[string]string{"clusters": "hr"}
		Expect(scope.Close(context.TODO())).To(Succeed())

		currentAddonConstraint := &libsveltosv1alpha1.AddonConstraint{}
		Expect(c.Get(context.TODO(), types.NamespacedName{Name: addonConstraint.Name}, currentAddonConstraint)).To(Succeed())
		Expect(currentAddonConstraint.Labels).ToNot(BeNil())
		Expect(len(currentAddonConstraint.Labels)).To(Equal(1))
		v, ok := currentAddonConstraint.Labels["clusters"]
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("hr"))
	})

	It("UpdateLabels updates AddonConstraint labels with matching clusters", func() {
		params := scope.AddonConstraintScopeParams{
			Client:          c,
			AddonConstraint: addonConstraint,
			Logger:          klogr.New(),
		}

		scope, err := scope.NewAddonConstraintScope(params)
		Expect(err).ToNot(HaveOccurred())
		Expect(scope).ToNot(BeNil())

		matchingClusters := []corev1.ObjectReference{
			{Namespace: randomString(), Name: randomString(),
				Kind: libsveltosv1alpha1.SveltosClusterKind, APIVersion: libsveltosv1alpha1.GroupVersion.String()},
			{Namespace: randomString(), Name: randomString(),
				Kind: libsveltosv1alpha1.SveltosClusterKind, APIVersion: libsveltosv1alpha1.GroupVersion.String()},
		}
		scope.UpdateLabels(matchingClusters)
		Expect(scope.AddonConstraint.Labels).ToNot(BeNil())
		Expect(len(scope.AddonConstraint.Labels)).To(Equal(len(matchingClusters)))
	})
})
