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
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/projectsveltos/addon-constraint-controller/controllers"
	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

const (
	ClusterKind = "Cluster"
)

var _ = Describe("Cluster Reconciler", func() {

	It("Cluster Reconcile asks manager to track cluster and adds annotation", func() {
		controllers.Reset()
		manager := controllers.GetManager()

		cluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: randomString(),
				Name:      randomString(),
				Labels: map[string]string{
					randomString(): randomString(),
				},
			},
		}

		initObjects := []client.Object{
			cluster,
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()

		clusterReconciler := &controllers.ClusterReconciler{
			Client: c,
		}
		clustertName := client.ObjectKey{
			Name:      cluster.Name,
			Namespace: cluster.Namespace,
		}
		_, err := clusterReconciler.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: clustertName,
		})
		Expect(err).To(BeNil())

		clusterInfo := &corev1.ObjectReference{
			Namespace:  cluster.Namespace,
			Name:       cluster.Name,
			Kind:       ClusterKind,
			APIVersion: clusterv1.GroupVersion.String(),
		}
		Expect(manager.HasEntryForCluster(clusterInfo)).To(BeTrue())
		Expect(manager.GetNumberOfAddonConstraint(clusterInfo)).To(Equal(0))

		// Since there is no addonConstraint matching cluster, expect cluster to have
		// annotation now
		currentCluster := &clusterv1.Cluster{}
		Expect(c.Get(context.TODO(),
			types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name},
			currentCluster)).To(Succeed())
		Expect(currentCluster.GetAnnotations()).ToNot(BeNil())
		_, ok := currentCluster.Annotations[libsveltosv1alpha1.GetClusterAnnotation()]
		Expect(ok).To(BeTrue())
	})

	It("Reconcile asks manager to track cluster", func() {
		controllers.Reset()
		manager := controllers.GetManager()

		key := randomString()
		value := randomString()
		selector := libsveltosv1alpha1.Selector(fmt.Sprintf("%s=%s", key, value))

		cluster := &clusterv1.Cluster{
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
			cluster,
			addonConstraint,
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()

		clusterReconciler := &controllers.ClusterReconciler{
			Client: c,
		}
		clustertName := client.ObjectKey{
			Name:      cluster.Name,
			Namespace: cluster.Namespace,
		}
		_, err := clusterReconciler.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: clustertName,
		})
		Expect(err).To(BeNil())

		clusterInfo := &corev1.ObjectReference{
			Namespace:  cluster.Namespace,
			Name:       cluster.Name,
			Kind:       ClusterKind,
			APIVersion: clusterv1.GroupVersion.String(),
		}
		Expect(manager.HasEntryForCluster(clusterInfo)).To(BeTrue())
		Expect(manager.GetNumberOfAddonConstraint(clusterInfo)).To(Equal(1))

		// Since there is addonConstraint matching cluster, expect cluster to NOT have
		// annotation now
		currentCluster := &clusterv1.Cluster{}
		Expect(c.Get(context.TODO(),
			types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name},
			currentCluster)).To(Succeed())
		Expect(currentCluster.GetAnnotations()).To(BeNil())
	})

	It("Cluster Reconcile for not found cluster, asks manager to stop trackin cluster", func() {
		controllers.Reset()
		manager := controllers.GetManager()

		clusterInfo := &corev1.ObjectReference{
			Namespace:  randomString(),
			Name:       randomString(),
			Kind:       ClusterKind,
			APIVersion: clusterv1.GroupVersion.String(),
		}

		m := manager.GetMap()
		s := libsveltosset.Set{}
		(*m)[*clusterInfo] = &s

		clustertName := client.ObjectKey{
			Name:      clusterInfo.Name,
			Namespace: clusterInfo.Namespace,
		}
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		clusterReconciler := &controllers.ClusterReconciler{
			Client: c,
		}

		_, err := clusterReconciler.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: clustertName,
		})
		Expect(err).To(BeNil())

		Expect(manager.HasEntryForCluster(clusterInfo)).To(BeFalse())
	})
})
