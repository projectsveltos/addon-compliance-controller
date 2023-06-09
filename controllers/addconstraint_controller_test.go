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
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2/klogr"

	"github.com/projectsveltos/addon-constraint-controller/controllers"
	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

var (
	deplReplicaSpec = `openapi: 3.0.0
info:
  title: Kubernetes Replica Validation
  version: 1.0.0

paths:
  /apis/apps/v1/namespaces/{namespace}/deployments/{deployment}:
    post:
      parameters:
        - in: path
          name: namespace
          required: true
          schema:
            type: string
            minimum: 1
          description: The namespace of the resource
        - in: path
          name: deployment
          required: true
          schema:
            type: string
            minimum: 1
          description: The name of the resource
      summary: Create/Update a new deployment
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Deployment'
      responses:
        '200':
          description: OK

components:
  schemas:
    Deployment:
      type: object
      properties:
        metadata:
          type: object
          properties:
            name:
              type: string
        spec:
          type: object
          properties:
            replicas:
              type: integer
              minimum: 3`

	nameSpec = `openapi: 3.0.0
info:
  title: Kubernetes Resource Validation
  version: 1.0.0
paths:
  '/apis/apps/v1/namespaces/{namespace}/deployments/{deployment}':
    post:
      summary: Create a Kubernetes Resource
      parameters:
        - name: namespace
          in: path
          required: true
          schema:
            type: string
        - name: deployment
          in: path
          required: true
          schema:
            $ref: '#/components/schemas/NameSchema'
      responses:
        '200':
          description: Successful operation
components:
  schemas:
    NameSchema:
      type: string
      maxLength: 10
`
)

var _ = Describe("AddonConstraint Controller", func() {
	var namespace string
	var addonConstraint *libsveltosv1alpha1.AddonConstraint

	BeforeEach(func() {
		namespace = "reconcile" + randomString()

		addonConstraint = &libsveltosv1alpha1.AddonConstraint{
			ObjectMeta: metav1.ObjectMeta{
				Name: randomString(),
			},
		}
	})

	It("getCurrentReferences collects all OpenAPIValidationRef referenced objects", func() {
		addonConstraint.Spec.OpenAPIValidationRefs = []libsveltosv1alpha1.OpenAPIValidationRef{
			{
				Namespace: namespace,
				Name:      randomString(),
				Kind:      string(libsveltosv1alpha1.ConfigMapReferencedResourceKind),
			},
			{
				Namespace: namespace,
				Name:      randomString(),
				Kind:      sourcev1.GitRepositoryKind,
			},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		addonConstraintScope := getAddonConstraintScope(c, klogr.New(), addonConstraint)
		reconciler := getAddonConstraintReconciler(c)
		set := controllers.GetCurrentReferences(reconciler, addonConstraintScope)
		Expect(set.Len()).To(Equal(2))
		items := set.Items()
		foundConfigMap := false
		foundGitRepository := false
		for i := range items {
			if items[i].Kind == sourcev1.GitRepositoryKind {
				foundGitRepository = true
			} else if items[i].Kind == string(libsveltosv1alpha1.ConfigMapReferencedResourceKind) {
				foundConfigMap = true
			}
		}
		Expect(foundConfigMap).To(BeTrue())
		Expect(foundGitRepository).To(BeTrue())
	})

	It("getMatchingClusters finds matching clusters", func() {
		selector := libsveltosv1alpha1.Selector("env=qa,zone=west")
		addonConstraint.Spec.ClusterSelector = selector

		matchingCluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      randomString(),
				Namespace: namespace,
				Labels: map[string]string{
					"env":  "qa",
					"zone": "west",
				},
			},
		}

		nonMatchingCluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      randomString(),
				Namespace: namespace,
				Labels: map[string]string{
					"zone": "west",
				},
			},
		}

		initObjects := []client.Object{
			matchingCluster,
			nonMatchingCluster,
			addonConstraint,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()

		addonConstraintScope := getAddonConstraintScope(c, klogr.New(), addonConstraint)
		reconciler := getAddonConstraintReconciler(c)

		// Only clusterSelector is, so only matchingCluster is a match
		matching, err := controllers.GetMatchingClusters(reconciler, context.TODO(), addonConstraintScope, klogr.New())
		Expect(err).To(BeNil())
		Expect(len(matching)).To(Equal(1))
	})

	It("updateReferenceMap updates internal map containg mapping between AddonConstraint and referenced resources", func() {
		gitRepository := &corev1.ObjectReference{
			Kind:       sourcev1.GitRepositoryKind,
			Namespace:  namespace,
			Name:       randomString(),
			APIVersion: sourcev1.SchemeBuilder.GroupVersion.String(),
		}
		configMap := &corev1.ObjectReference{
			Kind:       string(libsveltosv1alpha1.ConfigMapReferencedResourceKind),
			Namespace:  namespace,
			Name:       randomString(),
			APIVersion: corev1.SchemeGroupVersion.String(),
		}

		addonConstraintInfo := &corev1.ObjectReference{
			Namespace:  addonConstraint.Namespace,
			Name:       addonConstraint.Name,
			Kind:       libsveltosv1alpha1.AddonConstraintKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		addonConstraint.Spec.OpenAPIValidationRefs = []libsveltosv1alpha1.OpenAPIValidationRef{
			{
				Namespace: configMap.Namespace,
				Name:      configMap.Name,
				Kind:      string(libsveltosv1alpha1.ConfigMapReferencedResourceKind),
			},
			{
				Namespace: gitRepository.Namespace,
				Name:      gitRepository.Name,
				Kind:      sourcev1.GitRepositoryKind,
			},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		addonConstraintScope := getAddonConstraintScope(c, klogr.New(), addonConstraint)
		reconciler := getAddonConstraintReconciler(c)
		controllers.UpdateReferenceMap(reconciler, addonConstraintScope, klogr.New())

		Expect(len(reconciler.ReferenceMap)).To(Equal(2))

		v, ok := reconciler.ReferenceMap[*gitRepository]
		Expect(ok).To(BeTrue())
		Expect(v.Len()).To(Equal(1))
		items := v.Items()
		Expect(items).To(ContainElement(*addonConstraintInfo))

		v, ok = reconciler.ReferenceMap[*configMap]
		Expect(ok).To(BeTrue())
		Expect(v.Len()).To(Equal(1))
		items = v.Items()
		Expect(items).To(ContainElement(*addonConstraintInfo))

		references, ok := reconciler.AddonConstraintToReferenceMap[types.NamespacedName{Name: addonConstraintInfo.Name}]
		Expect(ok).To(BeTrue())
		Expect(references.Len()).To(Equal(2))
		Expect(references.Items()).To(ContainElement(*gitRepository))
		Expect(references.Items()).To(ContainElement(*configMap))
	})

	It("updateClusterMap updates internal map containg mapping between AddonConstraint and matching clusters", func() {
		addonConstraintInfo := &corev1.ObjectReference{
			Namespace:  addonConstraint.Namespace,
			Name:       addonConstraint.Name,
			Kind:       libsveltosv1alpha1.AddonConstraintKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		selector := libsveltosv1alpha1.Selector("env=qa,zone=west")
		addonConstraint.Spec.ClusterSelector = selector

		matchingCluster := &corev1.ObjectReference{
			Kind:       "Cluster",
			Namespace:  namespace,
			Name:       randomString(),
			APIVersion: clusterv1.GroupVersion.String(),
		}

		addonConstraint.Status.MatchingClusterRefs = []corev1.ObjectReference{
			*matchingCluster,
		}

		initObjects := []client.Object{
			addonConstraint,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()
		addonConstraintScope := getAddonConstraintScope(c, klogr.New(), addonConstraint)
		reconciler := getAddonConstraintReconciler(c)

		// Only clusterSelector is, so only matchingCluster is a match
		controllers.UpdateClusterMap(reconciler, addonConstraintScope, klogr.New())

		v, ok := reconciler.ClusterMap[*matchingCluster]
		Expect(ok).To(BeTrue())
		Expect(v.Len()).To(Equal(1))
		items := v.Items()
		Expect(items).To(ContainElement(*addonConstraintInfo))

		clusters, ok := reconciler.AddonConstraintToClusterMap[types.NamespacedName{Name: addonConstraintInfo.Name}]
		Expect(ok).To(BeTrue())
		Expect(clusters.Len()).To(Equal(1))
		Expect(clusters.Items()).To(ContainElement(*matchingCluster))
	})

	It("cleanMaps removes any entry for a given AddonConstraint", func() {
		matchingCluster := &corev1.ObjectReference{
			Kind:       "Cluster",
			Namespace:  namespace,
			Name:       randomString(),
			APIVersion: clusterv1.GroupVersion.String(),
		}

		gitRepository := &corev1.ObjectReference{
			Kind:       sourcev1.GitRepositoryKind,
			Namespace:  namespace,
			Name:       randomString(),
			APIVersion: sourcev1.SchemeBuilder.GroupVersion.String(),
		}
		configMap := &corev1.ObjectReference{
			Kind:       string(libsveltosv1alpha1.ConfigMapReferencedResourceKind),
			Namespace:  namespace,
			Name:       randomString(),
			APIVersion: corev1.SchemeGroupVersion.String(),
		}

		addonConstraintInfo := &corev1.ObjectReference{
			Namespace:  addonConstraint.Namespace,
			Name:       addonConstraint.Name,
			Kind:       libsveltosv1alpha1.AddonConstraintKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		addonConstraintScope := getAddonConstraintScope(c, klogr.New(), addonConstraint)
		reconciler := getAddonConstraintReconciler(c)

		addonConstraintName := types.NamespacedName{Name: addonConstraintScope.Name()}

		addonConstraintSet := &libsveltosset.Set{}
		addonConstraintSet.Insert(addonConstraintInfo)

		currentReferences := &libsveltosset.Set{}
		currentReferences.Insert(gitRepository)
		currentReferences.Insert(configMap)
		reconciler.AddonConstraintToReferenceMap[addonConstraintName] = currentReferences
		reconciler.ReferenceMap[*gitRepository] = addonConstraintSet
		reconciler.ReferenceMap[*configMap] = addonConstraintSet

		currentClusters := &libsveltosset.Set{}
		currentClusters.Insert(matchingCluster)
		reconciler.AddonConstraintToClusterMap[addonConstraintName] = currentClusters
		reconciler.ClusterMap[*matchingCluster] = addonConstraintSet

		controllers.CleanMaps(reconciler, addonConstraintScope)

		// No more entry for the AddonConstrain instance
		_, ok := reconciler.AddonConstraintToReferenceMap[addonConstraintName]
		Expect(ok).To(BeFalse())
		_, ok = reconciler.AddonConstraintToClusterMap[addonConstraintName]
		Expect(ok).To(BeFalse())

		// Any referenced object will remove being referenced by the AddonConstrain item
		// Since no other AddonConstraint is referencing, entry will be removed
		_, ok = reconciler.ReferenceMap[*gitRepository]
		Expect(ok).To(BeFalse())
		_, ok = reconciler.ReferenceMap[*configMap]
		Expect(ok).To(BeFalse())
		_, ok = reconciler.ClusterMap[*matchingCluster]
		Expect(ok).To(BeFalse())
	})

	It("collectContentOfConfigMap returns openapi policy", func() {
		configMap := createConfigMapWithPolicy(randomString(), randomString(), []string{nameSpec, deplReplicaSpec}...)

		initObjects := []client.Object{configMap}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()
		reconciler := getAddonConstraintReconciler(c)

		ref := &corev1.ObjectReference{
			Kind:       string(libsveltosv1alpha1.ConfigMapReferencedResourceKind),
			APIVersion: corev1.SchemeGroupVersion.String(),
			Namespace:  configMap.Namespace,
			Name:       configMap.Name,
		}

		u, err := controllers.CollectContentOfConfigMap(reconciler, context.TODO(), ref, klogr.New())
		Expect(err).To(BeNil())
		Expect(len(u)).To(Equal(2))
	})

	It("collectContentOfSecret returns openapi policy", func() {
		secret := createSecretWithPolicy(randomString(), randomString(), []string{nameSpec, deplReplicaSpec}...)

		initObjects := []client.Object{secret}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()
		reconciler := getAddonConstraintReconciler(c)

		ref := &corev1.ObjectReference{
			Kind:       string(libsveltosv1alpha1.SecretReferencedResourceKind),
			APIVersion: corev1.SchemeGroupVersion.String(),
			Namespace:  secret.Namespace,
			Name:       secret.Name,
		}

		u, err := controllers.CollectContentOfSecret(reconciler, context.TODO(), ref, klogr.New())
		Expect(err).To(BeNil())
		Expect(len(u)).To(Equal(2))
	})

	It("collectOpenapiValidations updates AddonConstraint status", func() {
		configMap := createConfigMapWithPolicy(randomString(), randomString(), []string{nameSpec, deplReplicaSpec}...)

		addonConstraint.Spec.OpenAPIValidationRefs = []libsveltosv1alpha1.OpenAPIValidationRef{
			{Namespace: configMap.Namespace, Name: configMap.Name,
				Kind: string(libsveltosv1alpha1.ConfigMapReferencedResourceKind)},
		}

		initObjects := []client.Object{configMap, addonConstraint}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()
		addonConstraintScope := getAddonConstraintScope(c, klogr.New(), addonConstraint)
		reconciler := getAddonConstraintReconciler(c)

		result, err := controllers.CollectOpenapiValidations(reconciler, context.TODO(),
			addonConstraintScope, klogr.New())
		Expect(err).To(BeNil())
		Expect(result).ToNot(BeNil())
		Expect(len(result)).To(Equal(2))
	})

	It("Reconciliation updates manager.addonConstraints", func() {
		sveltosCluster := &libsveltosv1alpha1.SveltosCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: randomString(),
				Name:      randomString(),
			},
		}

		clusterRef := corev1.ObjectReference{
			Namespace:  sveltosCluster.Namespace,
			Name:       sveltosCluster.Name,
			Kind:       libsveltosv1alpha1.SveltosClusterKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		initObjects := []client.Object{
			addonConstraint,
			sveltosCluster,
		}

		addonConstraintInfo := &corev1.ObjectReference{
			Name:       addonConstraint.Name,
			Kind:       libsveltosv1alpha1.AddonConstraintKind,
			APIVersion: libsveltosv1alpha1.GroupVersion.String(),
		}

		// Set managet so that for cluster, this AddonConstraint instance
		// was marked as a match
		controllers.Reset()
		manager := controllers.GetManager()
		m := manager.GetMap()
		s := &libsveltosset.Set{}
		s.Insert(addonConstraintInfo)
		(*m)[clusterRef] = s

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()
		reconciler := getAddonConstraintReconciler(c)

		addonConstraintName := client.ObjectKey{
			Name: addonConstraint.Name,
		}
		// Reconcile. After reconciliation expect that:
		// 1. AddonConstraint instance is not marked as match for cluster in manager anymore
		// 2. cluster has been annotate
		_, err := reconciler.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: addonConstraintName,
		})
		Expect(err).ToNot(HaveOccurred())

		m = manager.GetMap()
		v, ok := (*m)[clusterRef]
		Expect(ok).To(BeTrue())
		Expect(v.Len()).To(BeZero())

		currentSveltosCluster := &libsveltosv1alpha1.SveltosCluster{}
		Expect(c.Get(context.TODO(),
			types.NamespacedName{Namespace: sveltosCluster.Namespace, Name: sveltosCluster.Name}, currentSveltosCluster)).To(Succeed())
		Expect(currentSveltosCluster.Annotations).ToNot(BeNil())
		_, ok = currentSveltosCluster.Annotations[libsveltosv1alpha1.GetClusterAnnotation()]
		Expect(ok).To(BeTrue())
	})
})

// createConfigMapWithPolicy creates a configMap with Data policies
func createConfigMapWithPolicy(namespace, configMapName string, policyStrs ...string) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      configMapName,
		},
		Data: map[string]string{},
	}
	for i := range policyStrs {
		key := fmt.Sprintf("policy%d.yaml", i)
		if utf8.Valid([]byte(policyStrs[i])) {
			cm.Data[key] = policyStrs[i]
		} else {
			cm.BinaryData[key] = []byte(policyStrs[i])
		}
	}

	controllers.AddTypeInformationToObject(scheme, cm)

	return cm
}

// createSecretWithPolicy creates a Secret with Data containing base64 encoded policies
func createSecretWithPolicy(namespace, configMapName string, policyStrs ...string) *corev1.Secret {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      configMapName,
		},
		Type: libsveltosv1alpha1.ClusterProfileSecretType,
		Data: map[string][]byte{},
	}
	for i := range policyStrs {
		key := fmt.Sprintf("policy%d.yaml", i)
		secret.Data[key] = []byte(policyStrs[i])
	}

	controllers.AddTypeInformationToObject(scheme, secret)

	return secret
}
