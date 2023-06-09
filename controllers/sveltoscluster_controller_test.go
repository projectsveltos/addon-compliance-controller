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
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2/klogr"

	"github.com/projectsveltos/addon-constraint-controller/controllers"
	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

var _ = Describe("SveltosCluster Reconciler", func() {
	It("shouldAddClusterEntry returns false if SveltosCluster has annotation", func() {
		sveltosCluster := &libsveltosv1alpha1.SveltosCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: randomString(),
				Name:      randomString(),
				Annotations: map[string]string{
					libsveltosv1alpha1.GetClusterAnnotation(): "ok",
				},
			},
		}

		Expect(controllers.ShouldAddClusterEntry(sveltosCluster,
			libsveltosv1alpha1.ClusterTypeSveltos)).To(BeFalse())
	})

	It("shouldAddClusterEntry returns false if SveltosCluster is already tracked by manager", func() {
		sveltosCluster := &libsveltosv1alpha1.SveltosCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: randomString(),
				Name:      randomString(),
			},
		}

		sveltosClusterInfo := &corev1.ObjectReference{
			Kind:       libsveltosv1alpha1.SveltosClusterKind,
			Namespace:  sveltosCluster.Namespace,
			Name:       sveltosCluster.Name,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		controllers.Reset()
		manager := controllers.GetManager()
		s := libsveltosset.Set{}
		manager.AddClusterEntry(sveltosClusterInfo, &s)

		Expect(controllers.ShouldAddClusterEntry(sveltosCluster,
			libsveltosv1alpha1.ClusterTypeSveltos)).To(BeFalse())
	})

	It("shouldAddClusterEntry returns true when cluster has no annotation nor is tracked by manager", func() {
		sveltosCluster := &libsveltosv1alpha1.SveltosCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: randomString(),
				Name:      randomString(),
			},
		}

		Expect(controllers.ShouldAddClusterEntry(sveltosCluster,
			libsveltosv1alpha1.ClusterTypeSveltos)).To(BeTrue())
	})

	It("addClusterEntry asks manager to track cluster", func() {
		controllers.Reset()
		manager := controllers.GetManager()

		key := randomString()
		value := randomString()
		selector := libsveltosv1alpha1.Selector(fmt.Sprintf("%s=%s", key, value))

		sveltosCluster := &libsveltosv1alpha1.SveltosCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: randomString(),
				Name:      randomString(),
				Labels:    map[string]string{key: value},
			},
		}

		addonConstraint := &libsveltosv1alpha1.AddonConstraint{
			ObjectMeta: metav1.ObjectMeta{
				Name: randomString(),
			},
			Spec: libsveltosv1alpha1.AddonConstraintSpec{
				ClusterSelector: selector,
			},
		}

		initObjects := []client.Object{
			sveltosCluster,
			addonConstraint,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()
		Expect(controllers.AddClusterEntry(context.TODO(), c, sveltosCluster.Namespace, sveltosCluster.Name,
			libsveltosv1alpha1.ClusterTypeSveltos, sveltosCluster.Labels, klogr.New())).To(Succeed())

		clusterInfo := &corev1.ObjectReference{
			Namespace:  sveltosCluster.Namespace,
			Name:       sveltosCluster.Name,
			Kind:       libsveltosv1alpha1.SveltosClusterKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		Expect(manager.HasEntryForCluster(clusterInfo)).To(BeTrue())
		Expect(manager.GetNumberOfAddonConstraint(clusterInfo)).To(Equal(1))
	})

	It("annotateCluster adds annotation on cluster", func() {
		sveltosCluster := &libsveltosv1alpha1.SveltosCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: randomString(),
				Name:      randomString(),
			},
		}

		initObjects := []client.Object{
			sveltosCluster,
		}

		clusterInfo := &corev1.ObjectReference{
			Namespace:  sveltosCluster.Namespace,
			Name:       sveltosCluster.Name,
			Kind:       libsveltosv1alpha1.SveltosClusterKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()
		Expect(controllers.AnnotateCluster(context.TODO(), c, clusterInfo)).To(Succeed())

		currentSveltosCluster := &libsveltosv1alpha1.SveltosCluster{}
		Expect(c.Get(context.TODO(),
			types.NamespacedName{Namespace: sveltosCluster.Namespace, Name: sveltosCluster.Name},
			currentSveltosCluster)).To(Succeed())
		Expect(currentSveltosCluster.GetAnnotations()).ToNot(BeNil())
		_, ok := currentSveltosCluster.Annotations[libsveltosv1alpha1.GetClusterAnnotation()]
		Expect(ok).To(BeTrue())
	})

	It("SveltosCluster Reconcile asks manager to track cluster and adds annotation", func() {
		controllers.Reset()
		manager := controllers.GetManager()

		sveltosCluster := &libsveltosv1alpha1.SveltosCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: randomString(),
				Name:      randomString(),
			},
		}

		initObjects := []client.Object{
			sveltosCluster,
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()

		sveltosClusterReconciler := &controllers.SveltosClusterReconciler{
			Client: c,
		}
		sveltosClustertName := client.ObjectKey{
			Name:      sveltosCluster.Name,
			Namespace: sveltosCluster.Namespace,
		}
		_, err := sveltosClusterReconciler.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: sveltosClustertName,
		})
		Expect(err).To(BeNil())

		clusterInfo := &corev1.ObjectReference{
			Namespace:  sveltosCluster.Namespace,
			Name:       sveltosCluster.Name,
			Kind:       libsveltosv1alpha1.SveltosClusterKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}
		Expect(manager.HasEntryForCluster(clusterInfo)).To(BeTrue())
		Expect(manager.GetNumberOfAddonConstraint(clusterInfo)).To(Equal(0))

		// Since there is no addonConstraint matching cluster, expect cluster to have
		// annotation now
		currentSveltosCluster := &libsveltosv1alpha1.SveltosCluster{}
		Expect(c.Get(context.TODO(),
			types.NamespacedName{Namespace: sveltosCluster.Namespace, Name: sveltosCluster.Name},
			currentSveltosCluster)).To(Succeed())
		Expect(currentSveltosCluster.GetAnnotations()).ToNot(BeNil())
		_, ok := currentSveltosCluster.Annotations[libsveltosv1alpha1.GetClusterAnnotation()]
		Expect(ok).To(BeTrue())
	})

	It("SveltosCluster Reconcile asks manager to track cluster", func() {
		controllers.Reset()
		manager := controllers.GetManager()

		key := randomString()
		value := randomString()
		selector := libsveltosv1alpha1.Selector(fmt.Sprintf("%s=%s", key, value))

		sveltosCluster := &libsveltosv1alpha1.SveltosCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: randomString(),
				Name:      randomString(),
				Labels:    map[string]string{key: value},
			},
		}

		// Add an AddonConstraint instance matching cluster
		addonConstraint := &libsveltosv1alpha1.AddonConstraint{
			ObjectMeta: metav1.ObjectMeta{
				Name: randomString(),
			},
			Spec: libsveltosv1alpha1.AddonConstraintSpec{
				ClusterSelector: selector,
			},
		}

		initObjects := []client.Object{
			sveltosCluster,
			addonConstraint,
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()

		sveltosClusterReconciler := &controllers.SveltosClusterReconciler{
			Client: c,
		}
		sveltosClustertName := client.ObjectKey{
			Name:      sveltosCluster.Name,
			Namespace: sveltosCluster.Namespace,
		}
		_, err := sveltosClusterReconciler.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: sveltosClustertName,
		})
		Expect(err).To(BeNil())

		clusterInfo := &corev1.ObjectReference{
			Namespace:  sveltosCluster.Namespace,
			Name:       sveltosCluster.Name,
			Kind:       libsveltosv1alpha1.SveltosClusterKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}
		Expect(manager.HasEntryForCluster(clusterInfo)).To(BeTrue())
		Expect(manager.GetNumberOfAddonConstraint(clusterInfo)).To(Equal(1))

		// Since there is addonConstraint matching cluster, expect cluster to NOT have
		// annotation now
		currentSveltosCluster := &libsveltosv1alpha1.SveltosCluster{}
		Expect(c.Get(context.TODO(),
			types.NamespacedName{Namespace: sveltosCluster.Namespace, Name: sveltosCluster.Name},
			currentSveltosCluster)).To(Succeed())
		Expect(currentSveltosCluster.GetAnnotations()).To(BeNil())
	})

	It("SveltosCluster Reconcile for not found cluster, asks manager to stop trackin cluster", func() {
		controllers.Reset()
		manager := controllers.GetManager()

		clusterInfo := &corev1.ObjectReference{
			Namespace:  randomString(),
			Name:       randomString(),
			Kind:       libsveltosv1alpha1.SveltosClusterKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		m := manager.GetMap()
		s := libsveltosset.Set{}
		(*m)[*clusterInfo] = &s

		sveltosClustertName := client.ObjectKey{
			Name:      clusterInfo.Name,
			Namespace: clusterInfo.Namespace,
		}
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		sveltosClusterReconciler := &controllers.SveltosClusterReconciler{
			Client: c,
		}

		_, err := sveltosClusterReconciler.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: sveltosClustertName,
		})
		Expect(err).To(BeNil())

		Expect(manager.HasEntryForCluster(clusterInfo)).To(BeFalse())
	})
})
