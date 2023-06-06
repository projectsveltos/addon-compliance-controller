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

package controllers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sourcev1b2 "github.com/fluxcd/source-controller/api/v1beta2"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/projectsveltos/addon-constraint-controller/pkg/scope"
	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	"github.com/projectsveltos/libsveltos/lib/clusterproxy"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

const (
	// normalRequeueAfter is how long to wait before checking again to see if the cluster can be moved
	// to ready after or workload features (for instance ingress or reporter) have failed
	normalRequeueAfter = 20 * time.Second
)

// AddonConstraintReconciler reconciles a AddonConstraint object
type AddonConstraintReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	ConcurrentReconciles int

	// use a Mutex to update Map as MaxConcurrentReconciles is higher than one
	PolicyMux sync.Mutex

	// For each cluster contains current labels
	// This is needed in following scenario:
	// - AddonConstraint is created
	// - Cluster is created with labels matching AddonConstraint
	// - When first control plane machine in such cluster becomes available
	// we need Cluster labels to know which AddonConstraint to reconcile
	ClusterLabels map[corev1.ObjectReference]map[string]string

	// key: AddonConstraint; value AddonConstraint Selector
	AddonConstraints map[types.NamespacedName]libsveltosv1alpha1.Selector

	// key: Sveltos/CAPI Cluster; value: set of all AddonConstraints matching the Cluster
	ClusterMap map[corev1.ObjectReference]*libsveltosset.Set
	// key: AddonConstraint; value: set of Sveltos/CAPI Clusters matched
	AddonConstraintToClusterMap map[types.NamespacedName]*libsveltosset.Set

	// key: Referenced object; value: set of all AddonConstraints referencing the resource
	ReferenceMap map[corev1.ObjectReference]*libsveltosset.Set
	// key: AddonConstraint name; value: set of referenced resources
	AddonConstraintToReferenceMap map[types.NamespacedName]*libsveltosset.Set
}

//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=addonconstraints,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=addonconstraints/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=addonconstraints/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups="source.toolkit.fluxcd.io",resources=gitrepositories,verbs=get;watch;list
//+kubebuilder:rbac:groups="source.toolkit.fluxcd.io",resources=gitrepositories/status,verbs=get;watch;list
//+kubebuilder:rbac:groups="source.toolkit.fluxcd.io",resources=ocirepositories,verbs=get;watch;list
//+kubebuilder:rbac:groups="source.toolkit.fluxcd.io",resources=ocirepositories/status,verbs=get;watch;list
//+kubebuilder:rbac:groups="source.toolkit.fluxcd.io",resources=buckets,verbs=get;watch;list
//+kubebuilder:rbac:groups="source.toolkit.fluxcd.io",resources=buckets/status,verbs=get;watch;list
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;watch;list
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters/status,verbs=get;watch;list
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;watch;list
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines/status,verbs=get;watch;list
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=sveltosclusters,verbs=get;watch;list
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=sveltosclusters/status,verbs=get;watch;list

func (r *AddonConstraintReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	logger := ctrl.LoggerFrom(ctx)
	logger.V(logs.LogInfo).Info("Reconciling")

	// Fecth the AddonConstraint instance
	addonConstraint := &libsveltosv1alpha1.AddonConstraint{}
	if err := r.Get(ctx, req.NamespacedName, addonConstraint); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AddonConstraint")
		return reconcile.Result{}, errors.Wrapf(
			err,
			"Failed to fetch AddonConstraint %s",
			req.NamespacedName,
		)
	}

	logger = logger.WithValues("addonConstraint", req.String())

	addonConstraintScope, err := scope.NewAddonConstraintScope(scope.AddonConstraintScopeParams{
		Client:          r.Client,
		Logger:          logger,
		AddonConstraint: addonConstraint,
		ControllerName:  "clusterprofile",
	})
	if err != nil {
		logger.Error(err, "Failed to create addonConstraintScope")
		return reconcile.Result{}, errors.Wrapf(
			err,
			"unable to create clusterprofile scope for %s",
			req.NamespacedName,
		)
	}

	// Always close the scope when exiting this function so we can persist any AddonConstraint
	// changes.
	defer func() {
		if err := addonConstraintScope.Close(ctx); err != nil {
			reterr = err
		}
	}()

	// Handle deleted addonConstraint
	if !addonConstraint.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, addonConstraintScope)
	}

	// Handle non-deleted addonConstraint
	return r.reconcileNormal(ctx, addonConstraintScope)
}

func (r *AddonConstraintReconciler) reconcileDelete(
	ctx context.Context,
	addonConstraintScope *scope.AddonConstraintScope,
) (reconcile.Result, error) {

	logger := addonConstraintScope.Logger
	logger.V(logs.LogInfo).Info("Reconciling AddonConstraint delete")

	if controllerutil.ContainsFinalizer(addonConstraintScope.AddonConstraint, libsveltosv1alpha1.AddonConstraintFinalizer) {
		controllerutil.RemoveFinalizer(addonConstraintScope.AddonConstraint, libsveltosv1alpha1.AddonConstraintFinalizer)
	}

	r.cleanMaps(addonConstraintScope)

	logger.V(logs.LogInfo).Info("Reconcile delete success")
	return reconcile.Result{}, nil
}

func (r *AddonConstraintReconciler) reconcileNormal(
	ctx context.Context,
	addonConstraintScope *scope.AddonConstraintScope,
) (reconcile.Result, error) {

	logger := addonConstraintScope.Logger
	logger.V(logs.LogInfo).Info("Reconciling AddonConstraint")

	if !controllerutil.ContainsFinalizer(addonConstraintScope.AddonConstraint, libsveltosv1alpha1.AddonConstraintFinalizer) {
		if err := r.addFinalizer(ctx, addonConstraintScope); err != nil {
			return reconcile.Result{}, err
		}
	}

	matchingCluster, err := r.getMatchingClusters(ctx, addonConstraintScope, logger)
	if err != nil {
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}
	addonConstraintScope.SetMatchingClusterRefs(matchingCluster)

	addonConstraintScope.UpdateLabels(matchingCluster)

	r.updateMaps(addonConstraintScope, logger)

	var validations map[string][]byte
	validations, err = r.collectOpenapiValidations(ctx, addonConstraintScope, logger)
	if err != nil {
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}
	addonConstraintScope.AddonConstraint.Status.OpenapiValidations = validations

	logger.V(logs.LogInfo).Info("Reconcile success")
	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AddonConstraintReconciler) SetupWithManager(mgr ctrl.Manager) (controller.Controller, error) {
	c, err := ctrl.NewControllerManagedBy(mgr).
		For(&libsveltosv1alpha1.AddonConstraint{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: r.ConcurrentReconciles,
		}).
		Build(r)
	if err != nil {
		return nil, errors.Wrap(err, "error creating controller")
	}

	// When ConfigMap changes, according to ConfigMapPredicates,
	// one or more AddonConstraints need to be reconciled.
	err = c.Watch(&source.Kind{Type: &corev1.ConfigMap{}},
		handler.EnqueueRequestsFromMapFunc(r.requeueAddonConstraintForReference),
		ConfigMapPredicates(mgr.GetLogger().WithValues("predicate", "configmappredicate")),
	)
	if err != nil {
		return nil, err
	}

	// When Secret changes, according to SecretPredicates,
	// one or more AddonConstraints need to be reconciled.
	err = c.Watch(&source.Kind{Type: &corev1.Secret{}},
		handler.EnqueueRequestsFromMapFunc(r.requeueAddonConstraintForReference),
		SecretPredicates(mgr.GetLogger().WithValues("predicate", "secretpredicate")),
	)
	if err != nil {
		return nil, err
	}

	// When projectsveltos cluster changes, according to SveltosClusterPredicates,
	// one or more AddonConstraints need to be reconciled.
	err = c.Watch(&source.Kind{Type: &libsveltosv1alpha1.SveltosCluster{}},
		handler.EnqueueRequestsFromMapFunc(r.requeueAddonConstraintForCluster),
		SveltosClusterPredicates(mgr.GetLogger().WithValues("predicate", "sveltosclusterpredicate")),
	)

	return c, err
}

func (r *AddonConstraintReconciler) WatchForCAPI(mgr ctrl.Manager, c controller.Controller) error {
	// When cluster-api cluster changes, according to ClusterPredicates,
	// one or more AddonConstraints need to be reconciled.
	if err := c.Watch(&source.Kind{Type: &clusterv1.Cluster{}},
		handler.EnqueueRequestsFromMapFunc(r.requeueAddonConstraintForCluster),
		ClusterPredicates(mgr.GetLogger().WithValues("predicate", "clusterpredicate")),
	); err != nil {
		return err
	}
	// When cluster-api machine changes, according to ClusterPredicates,
	// one or more AddonConstraints need to be reconciled.
	if err := c.Watch(&source.Kind{Type: &clusterv1.Machine{}},
		handler.EnqueueRequestsFromMapFunc(r.requeueAddonConstraintForMachine),
		MachinePredicates(mgr.GetLogger().WithValues("predicate", "machinepredicate")),
	); err != nil {
		return err
	}

	return nil
}

func (r *AddonConstraintReconciler) WatchForFlux(mgr ctrl.Manager, c controller.Controller) error {
	// When a Flux source (GitRepository/OCIRepository/Bucket) changes, one or more ClusterSummaries
	// need to be reconciled.

	err := c.Watch(&source.Kind{Type: &sourcev1.GitRepository{}},
		handler.EnqueueRequestsFromMapFunc(r.requeueAddonConstraintForFluxSources),
		FluxSourcePredicates(r.Scheme, mgr.GetLogger().WithValues("predicate", "fluxsourcepredicate")),
	)
	if err != nil {
		return err
	}

	err = c.Watch(&source.Kind{Type: &sourcev1b2.OCIRepository{}},
		handler.EnqueueRequestsFromMapFunc(r.requeueAddonConstraintForFluxSources),
		FluxSourcePredicates(r.Scheme, mgr.GetLogger().WithValues("predicate", "fluxsourcepredicate")),
	)
	if err != nil {
		return err
	}

	return c.Watch(&source.Kind{Type: &sourcev1b2.Bucket{}},
		handler.EnqueueRequestsFromMapFunc(r.requeueAddonConstraintForFluxSources),
		FluxSourcePredicates(r.Scheme, mgr.GetLogger().WithValues("predicate", "fluxsourcepredicate")),
	)
}

func (r *AddonConstraintReconciler) getReferenceMapForEntry(entry *corev1.ObjectReference) *libsveltosset.Set {
	s := r.ReferenceMap[*entry]
	if s == nil {
		s = &libsveltosset.Set{}
		r.ReferenceMap[*entry] = s
	}
	return s
}

func (r *AddonConstraintReconciler) getClusterMapForEntry(entry *corev1.ObjectReference) *libsveltosset.Set {
	s := r.ClusterMap[*entry]
	if s == nil {
		s = &libsveltosset.Set{}
		r.ClusterMap[*entry] = s
	}
	return s
}

func (r *AddonConstraintReconciler) updateMaps(addonConstraintScope *scope.AddonConstraintScope, logger logr.Logger) {
	logger.V(logs.LogDebug).Info("update policy map")

	r.updateReferenceMap(addonConstraintScope, logger)

	r.updateClusterMap(addonConstraintScope, logger)

	addonConstraintName := types.NamespacedName{Name: addonConstraintScope.Name()}
	r.AddonConstraints[addonConstraintName] = addonConstraintScope.AddonConstraint.Spec.ClusterSelector
}

func (r *AddonConstraintReconciler) updateReferenceMap(addonConstraintScope *scope.AddonConstraintScope,
	logger logr.Logger) {

	logger.V(logs.LogDebug).Info("update reference map")
	currentReferences := r.getCurrentReferences(addonConstraintScope)

	r.PolicyMux.Lock()
	defer r.PolicyMux.Unlock()

	addonConstraintInfo := getKeyFromObject(r.Scheme, addonConstraintScope.AddonConstraint)
	addonConstraintName := types.NamespacedName{Name: addonConstraintScope.Name()}

	// Get list of References not referenced anymore by AddonConstraint
	var toBeRemoved []corev1.ObjectReference

	if v, ok := r.AddonConstraintToReferenceMap[addonConstraintName]; ok {
		toBeRemoved = v.Difference(currentReferences)
	}

	// For each currently referenced instance, add AddonConstraint as consumer
	for _, referencedResource := range currentReferences.Items() {
		tmpResource := referencedResource
		r.getReferenceMapForEntry(&tmpResource).Insert(addonConstraintInfo)
	}

	// For each resource not reference anymore, remove AddonConstraint as consumer
	for i := range toBeRemoved {
		referencedResource := toBeRemoved[i]
		r.getReferenceMapForEntry(&referencedResource).Erase(addonConstraintInfo)
	}

	// Update list of resources currently referenced by AddonConstraint
	r.AddonConstraintToReferenceMap[addonConstraintName] = currentReferences
}

func (r *AddonConstraintReconciler) updateClusterMap(addonConstraintScope *scope.AddonConstraintScope, logger logr.Logger) {
	logger.V(logs.LogDebug).Info("update cluster map")

	currentClusters := &libsveltosset.Set{}
	for i := range addonConstraintScope.AddonConstraint.Status.MatchingClusterRefs {
		cluster := addonConstraintScope.AddonConstraint.Status.MatchingClusterRefs[i]
		clusterInfo := &corev1.ObjectReference{Namespace: cluster.Namespace, Name: cluster.Name, Kind: cluster.Kind, APIVersion: cluster.APIVersion}
		currentClusters.Insert(clusterInfo)
	}

	r.PolicyMux.Lock()
	defer r.PolicyMux.Unlock()

	addonConstraintInfo := getKeyFromObject(r.Scheme, addonConstraintScope.AddonConstraint)
	addonConstraintName := types.NamespacedName{Name: addonConstraintScope.Name()}

	// Get list of Clusters not matched anymore by AddonConstraint
	var toBeRemoved []corev1.ObjectReference
	if v, ok := r.AddonConstraintToClusterMap[addonConstraintName]; ok {
		toBeRemoved = v.Difference(currentClusters)
	}

	// For each currently matching Cluster, add AddonConstraint as consumer
	for i := range addonConstraintScope.AddonConstraint.Status.MatchingClusterRefs {
		cluster := addonConstraintScope.AddonConstraint.Status.MatchingClusterRefs[i]
		clusterInfo := &corev1.ObjectReference{Namespace: cluster.Namespace, Name: cluster.Name, Kind: cluster.Kind, APIVersion: cluster.APIVersion}
		r.getClusterMapForEntry(clusterInfo).Insert(addonConstraintInfo)
	}

	// For each Cluster not matched anymore, remove AddonConstraint as consumer
	for i := range toBeRemoved {
		clusterName := toBeRemoved[i]
		r.getClusterMapForEntry(&clusterName).Erase(addonConstraintInfo)
	}

	r.AddonConstraintToClusterMap[addonConstraintName] = currentClusters
}

func (r *AddonConstraintReconciler) cleanMaps(addonConstraintScope *scope.AddonConstraintScope) {
	r.PolicyMux.Lock()
	defer r.PolicyMux.Unlock()

	addonConstraintName := types.NamespacedName{Name: addonConstraintScope.Name()}
	delete(r.AddonConstraintToClusterMap, addonConstraintName)
	delete(r.AddonConstraintToReferenceMap, addonConstraintName)

	addonConstraintInfo := getKeyFromObject(r.Scheme, addonConstraintScope.AddonConstraint)

	for objRef := range r.ReferenceMap {
		addonConstraintSet := r.ReferenceMap[objRef]
		addonConstraintSet.Erase(addonConstraintInfo)
		if addonConstraintSet.Len() == 0 {
			delete(r.ReferenceMap, objRef)
		}
	}

	for objRef := range r.ClusterMap {
		addonConstraintSet := r.ClusterMap[objRef]
		addonConstraintSet.Erase(addonConstraintInfo)
		if addonConstraintSet.Len() == 0 {
			delete(r.ClusterMap, objRef)
		}
	}
}

func (r *AddonConstraintReconciler) addFinalizer(ctx context.Context, addonConstraintScope *scope.AddonConstraintScope) error {
	// If the SveltosCluster doesn't have our finalizer, add it.
	controllerutil.AddFinalizer(addonConstraintScope.AddonConstraint, libsveltosv1alpha1.AddonConstraintFinalizer)
	// Register the finalizer immediately to avoid orphaning clusterprofile resources on delete
	if err := addonConstraintScope.PatchObject(ctx); err != nil {
		addonConstraintScope.Error(err, "Failed to add finalizer")
		return errors.Wrapf(
			err,
			"Failed to add finalizer for %s",
			addonConstraintScope.Name(),
		)
	}
	return nil
}

func (r *AddonConstraintReconciler) getMatchingClusters(ctx context.Context,
	addonConstraintScope *scope.AddonConstraintScope, logger logr.Logger) ([]corev1.ObjectReference, error) {

	logger.V(logs.LogDebug).Info("finding matching clusters")
	var matchingCluster []corev1.ObjectReference
	var err error
	if addonConstraintScope.GetSelector() != "" {
		parsedSelector, _ := labels.Parse(addonConstraintScope.GetSelector())
		matchingCluster, err = clusterproxy.GetMatchingClusters(ctx, r.Client, parsedSelector, logger)
		if err != nil {
			return nil, err
		}
	}

	matchingCluster = append(matchingCluster, addonConstraintScope.AddonConstraint.Spec.ClusterRefs...)

	return matchingCluster, nil
}

func (r *AddonConstraintReconciler) getCurrentReferences(addonConstraintScope *scope.AddonConstraintScope) *libsveltosset.Set {
	currentReferences := &libsveltosset.Set{}

	for i := range addonConstraintScope.AddonConstraint.Spec.OpenAPIValidationRefs {
		referencedNamespace := addonConstraintScope.AddonConstraint.Spec.OpenAPIValidationRefs[i].Namespace
		referencedName := addonConstraintScope.AddonConstraint.Spec.OpenAPIValidationRefs[i].Name

		apiVersion := getOpenapiReferenceAPIVersion(&addonConstraintScope.AddonConstraint.Spec.OpenAPIValidationRefs[i])
		currentReferences.Insert(&corev1.ObjectReference{
			APIVersion: apiVersion,
			Kind:       addonConstraintScope.AddonConstraint.Spec.OpenAPIValidationRefs[i].Kind,
			Namespace:  referencedNamespace,
			Name:       referencedName,
		})
	}
	return currentReferences
}

func (r *AddonConstraintReconciler) collectOpenapiValidations(ctx context.Context, addonConstraintScope *scope.AddonConstraintScope,
	logger logr.Logger) (map[string][]byte, error) {

	logger.V(logs.LogDebug).Info("collect openapi validations")
	validations := make(map[string][]byte)
	for i := range addonConstraintScope.AddonConstraint.Spec.OpenAPIValidationRefs {
		ref := &addonConstraintScope.AddonConstraint.Spec.OpenAPIValidationRefs[i]
		referencedNamespace := ref.Namespace
		referencedName := ref.Name
		apiVersion := getOpenapiReferenceAPIVersion(&addonConstraintScope.AddonConstraint.Spec.OpenAPIValidationRefs[i])

		var err error

		objRef := &corev1.ObjectReference{
			APIVersion: apiVersion,
			Kind:       addonConstraintScope.AddonConstraint.Spec.OpenAPIValidationRefs[i].Kind,
			Namespace:  referencedNamespace,
			Name:       referencedName,
		}

		var tmpValidations [][]byte
		switch ref.Kind {
		case string(libsveltosv1alpha1.ConfigMapReferencedResourceKind):
			tmpValidations, err = r.collectContentOfConfigMap(ctx, objRef, logger)
		case string(libsveltosv1alpha1.SecretReferencedResourceKind):
			tmpValidations, err = r.collectContentOfSecret(ctx, objRef, logger)
		case sourcev1b2.OCIRepositoryKind:
		case sourcev1b2.BucketKind:
		case sourcev1.GitRepositoryKind:
			tmpValidations, err = r.collectContentFromFluxSource(ctx, objRef, ref.Path, logger)
		}

		if err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to collect content of %s:%s/%s. Err: %v",
				ref.Kind, ref.Namespace, ref.Name, err))
			return nil, err
		}

		for i := range tmpValidations {
			key := fmt.Sprintf("%s:%s/%s-%d", ref.Kind, ref.Namespace, ref.Name, i)
			validations[key] = tmpValidations[i]
		}
	}

	return validations, nil
}

func (r *AddonConstraintReconciler) collectContentOfConfigMap(ctx context.Context, ref *corev1.ObjectReference,
	logger logr.Logger) ([][]byte, error) {

	logger = logger.WithValues("resource", fmt.Sprintf("%s:%s/%s", ref.Kind, ref.Namespace, ref.Name))
	configMap, err := getConfigMap(ctx, r.Client, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		logger.V(logs.LogInfo).Info("failed to get resource")
		return nil, err
	}

	var policies [][]byte
	policies, err = collectContent(ctx, configMap.Data, logger)
	if err != nil {
		return nil, err
	}

	return policies, nil
}

func (r *AddonConstraintReconciler) collectContentOfSecret(ctx context.Context, ref *corev1.ObjectReference,
	logger logr.Logger) ([][]byte, error) {

	logger = logger.WithValues("resource", fmt.Sprintf("%s:%s/%s", ref.Kind, ref.Namespace, ref.Name))
	secret, err := getSecret(ctx, r.Client, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		logger.V(logs.LogInfo).Info("failed to get resource")
		return nil, err
	}

	data := make(map[string]string)
	for key, value := range secret.Data {
		data[key] = string(value)
	}

	var policies [][]byte
	policies, err = collectContent(ctx, data, logger)
	if err != nil {
		return nil, err
	}

	return policies, nil
}

func (r *AddonConstraintReconciler) collectContentFromFluxSource(ctx context.Context, ref *corev1.ObjectReference,
	path string, logger logr.Logger) ([][]byte, error) {

	tmpDir, err := prepareFileSystemWithFluxSource(ctx, r.Client, ref, logger)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		logger.V(logs.LogInfo).Info("failed to get resource")
		return nil, err
	}

	if tmpDir == "" {
		return nil, nil
	}

	defer os.RemoveAll(tmpDir)

	// check build path exists
	dirPath := filepath.Join(tmpDir, path)
	_, err = os.Stat(dirPath)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("path not found: %v", err))
		return nil, err
	}

	var fileContents map[string]string
	fileContents, err = walkDir(dirPath, logger)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get file contents: %v", err))
		return nil, err
	}

	var policies [][]byte
	policies, err = collectContent(ctx, fileContents, logger)
	if err != nil {
		return nil, err
	}

	return policies, nil
}
