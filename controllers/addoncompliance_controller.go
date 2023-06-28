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
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/projectsveltos/addon-compliance-controller/pkg/scope"
	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	"github.com/projectsveltos/libsveltos/lib/clusterproxy"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

const (
	// deleteRequeueAfter is how long to wait before checking again to see if the cluster still has
	// children during deletion.
	deleteRequeueAfter = 20 * time.Second

	// normalRequeueAfter is how long to wait before checking again to see if the cluster can be moved
	// to ready after or workload features (for instance ingress or reporter) have failed
	normalRequeueAfter = 20 * time.Second
)

// AddonComplianceReconciler reconciles a AddonCompliance object
type AddonComplianceReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	ConcurrentReconciles int

	// use a Mutex to update Map as MaxConcurrentReconciles is higher than one
	PolicyMux sync.Mutex

	// For each cluster contains current labels
	// This is needed in following scenario:
	// - AddonCompliance is created
	// - Cluster is created with labels matching AddonCompliance
	// - When first control plane machine in such cluster becomes available
	// we need Cluster labels to know which AddonCompliance to reconcile
	ClusterLabels map[corev1.ObjectReference]map[string]string

	// key: AddonCompliance; value AddonCompliance Selector
	AddonCompliances map[types.NamespacedName]libsveltosv1alpha1.Selector

	// key: Sveltos/CAPI Cluster; value: set of all AddonCompliances matching the Cluster
	ClusterMap map[corev1.ObjectReference]*libsveltosset.Set
	// key: AddonCompliance; value: set of Sveltos/CAPI Clusters matched
	AddonComplianceToClusterMap map[types.NamespacedName]*libsveltosset.Set

	// key: Referenced object; value: set of all AddonCompliances referencing the resource
	ReferenceMap map[corev1.ObjectReference]*libsveltosset.Set
	// key: AddonCompliance name; value: set of referenced resources
	AddonComplianceToReferenceMap map[types.NamespacedName]*libsveltosset.Set
}

//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=addoncompliances,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=addoncompliances/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=addoncompliances/finalizers,verbs=update
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

func (r *AddonComplianceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	logger := ctrl.LoggerFrom(ctx)
	logger.V(logs.LogInfo).Info("Reconciling")

	// Fecth the AddonCompliance instance
	addonConstraint := &libsveltosv1alpha1.AddonCompliance{}
	if err := r.Get(ctx, req.NamespacedName, addonConstraint); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "Failed to fetch AddonCompliance")
		return reconcile.Result{}, errors.Wrapf(
			err,
			"Failed to fetch AddonCompliance %s",
			req.NamespacedName,
		)
	}

	logger = logger.WithValues("addonConstraint", req.String())
	addonConstraintScope, err := scope.NewAddonComplianceScope(scope.AddonComplianceScopeParams{
		Client:          r.Client,
		Logger:          logger,
		AddonCompliance: addonConstraint,
		ControllerName:  "addoncompliance",
	})
	if err != nil {
		logger.Error(err, "Failed to create addonConstraintScope")
		return reconcile.Result{}, errors.Wrapf(
			err,
			"unable to create addoncompliance scope for %s",
			req.NamespacedName,
		)
	}

	// Always close the scope when exiting this function so we can persist any AddonCompliance
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

func (r *AddonComplianceReconciler) reconcileDelete(
	ctx context.Context,
	addonConstraintScope *scope.AddonComplianceScope,
) (reconcile.Result, error) {

	logger := addonConstraintScope.Logger
	logger.V(logs.LogInfo).Info("Reconciling AddonCompliance delete")

	r.cleanMaps(addonConstraintScope)

	err := r.annotateClusters(ctx, addonConstraintScope, logger)
	if err != nil {
		return reconcile.Result{Requeue: true, RequeueAfter: deleteRequeueAfter}, nil
	}

	if controllerutil.ContainsFinalizer(addonConstraintScope.AddonCompliance, libsveltosv1alpha1.AddonComplianceFinalizer) {
		controllerutil.RemoveFinalizer(addonConstraintScope.AddonCompliance, libsveltosv1alpha1.AddonComplianceFinalizer)
	}

	logger.V(logs.LogInfo).Info("Reconcile delete success")
	return reconcile.Result{}, nil
}

func (r *AddonComplianceReconciler) reconcileNormal(
	ctx context.Context,
	addonConstraintScope *scope.AddonComplianceScope,
) (reconcile.Result, error) {

	logger := addonConstraintScope.Logger
	logger.V(logs.LogInfo).Info("Reconciling AddonCompliance")

	if !controllerutil.ContainsFinalizer(addonConstraintScope.AddonCompliance, libsveltosv1alpha1.AddonComplianceFinalizer) {
		if err := r.addFinalizer(ctx, addonConstraintScope); err != nil {
			return reconcile.Result{}, err
		}
	}

	matchingCluster, err := r.getMatchingClusters(ctx, addonConstraintScope, logger)
	if err != nil {
		failureMsg := err.Error()
		addonConstraintScope.SetFailureMessage(&failureMsg)
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}
	addonConstraintScope.SetMatchingClusterRefs(matchingCluster)

	addonConstraintScope.UpdateLabels(matchingCluster)

	r.updateMaps(addonConstraintScope, logger)

	var validations map[string][]byte
	validations, err = r.collectOpenapiValidations(ctx, addonConstraintScope, logger)
	if err != nil {
		failureMsg := err.Error()
		addonConstraintScope.SetFailureMessage(&failureMsg)
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}
	addonConstraintScope.AddonCompliance.Status.OpenapiValidations = validations

	validations, err = r.collectLuaValidations(ctx, addonConstraintScope, logger)
	if err != nil {
		failureMsg := err.Error()
		addonConstraintScope.SetFailureMessage(&failureMsg)
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}
	addonConstraintScope.AddonCompliance.Status.LuaValidations = validations

	err = r.annotateClusters(ctx, addonConstraintScope, logger)
	if err != nil {
		failureMsg := err.Error()
		addonConstraintScope.SetFailureMessage(&failureMsg)
		return reconcile.Result{Requeue: true, RequeueAfter: deleteRequeueAfter}, nil
	}

	addonConstraintScope.SetFailureMessage(nil)
	logger.V(logs.LogInfo).Info("Reconcile success")
	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AddonComplianceReconciler) SetupWithManager(mgr ctrl.Manager) (controller.Controller, error) {
	c, err := ctrl.NewControllerManagedBy(mgr).
		For(&libsveltosv1alpha1.AddonCompliance{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: r.ConcurrentReconciles,
		}).
		Build(r)
	if err != nil {
		return nil, errors.Wrap(err, "error creating controller")
	}

	// When ConfigMap changes, according to ConfigMapPredicates,
	// one or more AddonCompliances need to be reconciled.
	err = c.Watch(source.Kind(mgr.GetCache(), &corev1.ConfigMap{}),
		handler.EnqueueRequestsFromMapFunc(r.requeueAddonComplianceForReference),
		ConfigMapPredicates(mgr.GetLogger().WithValues("predicate", "configmappredicate")),
	)
	if err != nil {
		return nil, err
	}

	// When Secret changes, according to SecretPredicates,
	// one or more AddonCompliances need to be reconciled.
	err = c.Watch(source.Kind(mgr.GetCache(), &corev1.Secret{}),
		handler.EnqueueRequestsFromMapFunc(r.requeueAddonComplianceForReference),
		SecretPredicates(mgr.GetLogger().WithValues("predicate", "secretpredicate")),
	)
	if err != nil {
		return nil, err
	}

	// When projectsveltos cluster changes, according to SveltosClusterPredicates,
	// one or more AddonCompliances need to be reconciled.
	err = c.Watch(source.Kind(mgr.GetCache(), &libsveltosv1alpha1.SveltosCluster{}),
		handler.EnqueueRequestsFromMapFunc(r.requeueAddonComplianceForCluster),
		SveltosClusterPredicates(mgr.GetLogger().WithValues("predicate", "sveltosclusterpredicate")),
	)

	return c, err
}

func (r *AddonComplianceReconciler) WatchForCAPI(mgr ctrl.Manager, c controller.Controller) error {
	// When cluster-api cluster changes, according to ClusterPredicates,
	// one or more AddonCompliances need to be reconciled.
	if err := c.Watch(source.Kind(mgr.GetCache(), &clusterv1.Cluster{}),
		handler.EnqueueRequestsFromMapFunc(r.requeueAddonComplianceForCluster),
		ClusterPredicates(mgr.GetLogger().WithValues("predicate", "clusterpredicate")),
	); err != nil {
		return err
	}
	// When cluster-api machine changes, according to ClusterPredicates,
	// one or more AddonCompliances need to be reconciled.
	if err := c.Watch(source.Kind(mgr.GetCache(), &clusterv1.Machine{}),
		handler.EnqueueRequestsFromMapFunc(r.requeueAddonComplianceForMachine),
		MachinePredicates(mgr.GetLogger().WithValues("predicate", "machinepredicate")),
	); err != nil {
		return err
	}

	return nil
}

func (r *AddonComplianceReconciler) WatchForFlux(mgr ctrl.Manager, c controller.Controller) error {
	// When a Flux source (GitRepository/OCIRepository/Bucket) changes, one or more addonConstraints
	// need to be reconciled.

	err := c.Watch(source.Kind(mgr.GetCache(), &sourcev1.GitRepository{}),
		handler.EnqueueRequestsFromMapFunc(r.requeueAddonComplianceForFluxSources),
		FluxSourcePredicates(r.Scheme, mgr.GetLogger().WithValues("predicate", "fluxsourcepredicate")),
	)
	if err != nil {
		return err
	}

	err = c.Watch(source.Kind(mgr.GetCache(), &sourcev1b2.OCIRepository{}),
		handler.EnqueueRequestsFromMapFunc(r.requeueAddonComplianceForFluxSources),
		FluxSourcePredicates(r.Scheme, mgr.GetLogger().WithValues("predicate", "fluxsourcepredicate")),
	)
	if err != nil {
		return err
	}

	return c.Watch(source.Kind(mgr.GetCache(), &sourcev1b2.Bucket{}),
		handler.EnqueueRequestsFromMapFunc(r.requeueAddonComplianceForFluxSources),
		FluxSourcePredicates(r.Scheme, mgr.GetLogger().WithValues("predicate", "fluxsourcepredicate")),
	)
}

func (r *AddonComplianceReconciler) getReferenceMapForEntry(entry *corev1.ObjectReference) *libsveltosset.Set {
	s := r.ReferenceMap[*entry]
	if s == nil {
		s = &libsveltosset.Set{}
		r.ReferenceMap[*entry] = s
	}
	return s
}

func (r *AddonComplianceReconciler) getClusterMapForEntry(entry *corev1.ObjectReference) *libsveltosset.Set {
	s := r.ClusterMap[*entry]
	if s == nil {
		s = &libsveltosset.Set{}
		r.ClusterMap[*entry] = s
	}
	return s
}

func (r *AddonComplianceReconciler) updateMaps(addonConstraintScope *scope.AddonComplianceScope, logger logr.Logger) {
	logger.V(logs.LogDebug).Info("update policy map")

	r.updateReferenceMap(addonConstraintScope, logger)

	r.updateClusterMap(addonConstraintScope, logger)

	addonConstraintName := types.NamespacedName{Name: addonConstraintScope.Name()}
	r.AddonCompliances[addonConstraintName] = addonConstraintScope.AddonCompliance.Spec.ClusterSelector
}

func (r *AddonComplianceReconciler) updateReferenceMap(addonConstraintScope *scope.AddonComplianceScope,
	logger logr.Logger) {

	logger.V(logs.LogDebug).Info("update reference map")
	currentReferences := r.getCurrentReferences(addonConstraintScope)

	r.PolicyMux.Lock()
	defer r.PolicyMux.Unlock()

	addonConstraintInfo := getKeyFromObject(r.Scheme, addonConstraintScope.AddonCompliance)
	addonConstraintName := types.NamespacedName{Name: addonConstraintScope.Name()}

	// Get list of References not referenced anymore by AddonCompliance
	var toBeRemoved []corev1.ObjectReference

	if v, ok := r.AddonComplianceToReferenceMap[addonConstraintName]; ok {
		toBeRemoved = v.Difference(currentReferences)
	}

	// For each currently referenced instance, add AddonCompliance as consumer
	for _, referencedResource := range currentReferences.Items() {
		tmpResource := referencedResource
		r.getReferenceMapForEntry(&tmpResource).Insert(addonConstraintInfo)
	}

	// For each resource not reference anymore, remove AddonCompliance as consumer
	for i := range toBeRemoved {
		referencedResource := toBeRemoved[i]
		r.getReferenceMapForEntry(&referencedResource).Erase(addonConstraintInfo)
	}

	// Update list of resources currently referenced by AddonCompliance
	r.AddonComplianceToReferenceMap[addonConstraintName] = currentReferences
}

func (r *AddonComplianceReconciler) updateClusterMap(addonConstraintScope *scope.AddonComplianceScope, logger logr.Logger) {
	logger.V(logs.LogDebug).Info("update cluster map")

	currentClusters := &libsveltosset.Set{}
	for i := range addonConstraintScope.AddonCompliance.Status.MatchingClusterRefs {
		cluster := addonConstraintScope.AddonCompliance.Status.MatchingClusterRefs[i]
		clusterInfo := &corev1.ObjectReference{Namespace: cluster.Namespace, Name: cluster.Name, Kind: cluster.Kind, APIVersion: cluster.APIVersion}
		currentClusters.Insert(clusterInfo)
	}

	r.PolicyMux.Lock()
	defer r.PolicyMux.Unlock()

	addonConstraintInfo := getKeyFromObject(r.Scheme, addonConstraintScope.AddonCompliance)
	addonConstraintName := types.NamespacedName{Name: addonConstraintScope.Name()}

	// Get list of Clusters not matched anymore by AddonCompliance
	var toBeRemoved []corev1.ObjectReference
	if v, ok := r.AddonComplianceToClusterMap[addonConstraintName]; ok {
		toBeRemoved = v.Difference(currentClusters)
	}

	// For each currently matching Cluster, add AddonCompliance as consumer
	for i := range addonConstraintScope.AddonCompliance.Status.MatchingClusterRefs {
		cluster := addonConstraintScope.AddonCompliance.Status.MatchingClusterRefs[i]
		clusterInfo := &corev1.ObjectReference{Namespace: cluster.Namespace, Name: cluster.Name, Kind: cluster.Kind, APIVersion: cluster.APIVersion}
		r.getClusterMapForEntry(clusterInfo).Insert(addonConstraintInfo)
	}

	// For each Cluster not matched anymore, remove AddonCompliance as consumer
	for i := range toBeRemoved {
		clusterName := toBeRemoved[i]
		r.getClusterMapForEntry(&clusterName).Erase(addonConstraintInfo)
	}

	r.AddonComplianceToClusterMap[addonConstraintName] = currentClusters
}

func (r *AddonComplianceReconciler) cleanMaps(addonConstraintScope *scope.AddonComplianceScope) {
	r.PolicyMux.Lock()
	defer r.PolicyMux.Unlock()

	addonConstraintName := types.NamespacedName{Name: addonConstraintScope.Name()}
	delete(r.AddonComplianceToClusterMap, addonConstraintName)
	delete(r.AddonComplianceToReferenceMap, addonConstraintName)

	addonConstraintInfo := getKeyFromObject(r.Scheme, addonConstraintScope.AddonCompliance)

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

func (r *AddonComplianceReconciler) addFinalizer(ctx context.Context, addonConstraintScope *scope.AddonComplianceScope) error {
	// If the SveltosCluster doesn't have our finalizer, add it.
	controllerutil.AddFinalizer(addonConstraintScope.AddonCompliance, libsveltosv1alpha1.AddonComplianceFinalizer)
	// Register the finalizer immediately to avoid orphaning addoncompliance resources on delete
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

func (r *AddonComplianceReconciler) getMatchingClusters(ctx context.Context,
	addonConstraintScope *scope.AddonComplianceScope, logger logr.Logger) ([]corev1.ObjectReference, error) {

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

	matchingCluster = append(matchingCluster, addonConstraintScope.AddonCompliance.Spec.ClusterRefs...)

	return matchingCluster, nil
}

func (r *AddonComplianceReconciler) getCurrentReferences(addonConstraintScope *scope.AddonComplianceScope) *libsveltosset.Set {
	currentReferences := &libsveltosset.Set{}

	for i := range addonConstraintScope.AddonCompliance.Spec.OpenAPIValidationRefs {
		referencedNamespace := addonConstraintScope.AddonCompliance.Spec.OpenAPIValidationRefs[i].Namespace
		referencedName := addonConstraintScope.AddonCompliance.Spec.OpenAPIValidationRefs[i].Name

		apiVersion := getReferenceAPIVersion(addonConstraintScope.AddonCompliance.Spec.OpenAPIValidationRefs[i].Kind)
		currentReferences.Insert(&corev1.ObjectReference{
			APIVersion: apiVersion,
			Kind:       addonConstraintScope.AddonCompliance.Spec.OpenAPIValidationRefs[i].Kind,
			Namespace:  referencedNamespace,
			Name:       referencedName,
		})
	}
	for i := range addonConstraintScope.AddonCompliance.Spec.LuaValidationRefs {
		referencedNamespace := addonConstraintScope.AddonCompliance.Spec.LuaValidationRefs[i].Namespace
		referencedName := addonConstraintScope.AddonCompliance.Spec.LuaValidationRefs[i].Name

		apiVersion := getReferenceAPIVersion(addonConstraintScope.AddonCompliance.Spec.LuaValidationRefs[i].Kind)
		currentReferences.Insert(&corev1.ObjectReference{
			APIVersion: apiVersion,
			Kind:       addonConstraintScope.AddonCompliance.Spec.LuaValidationRefs[i].Kind,
			Namespace:  referencedNamespace,
			Name:       referencedName,
		})
	}

	return currentReferences
}

func (r *AddonComplianceReconciler) collectOpenapiValidations(ctx context.Context, addonConstraintScope *scope.AddonComplianceScope,
	logger logr.Logger) (map[string][]byte, error) {

	logger.V(logs.LogDebug).Info("collect openapi validations")
	validations := make(map[string][]byte)
	for i := range addonConstraintScope.AddonCompliance.Spec.OpenAPIValidationRefs {
		ref := &addonConstraintScope.AddonCompliance.Spec.OpenAPIValidationRefs[i]
		currentValidation, err := r.collectValidations(ctx, ref.Kind, ref.Namespace, ref.Name, ref.Path, logger)
		if err != nil {
			return nil, err
		}
		for k := range currentValidation {
			loader := &openapi3.Loader{Context: ctx, IsExternalRefsAllowed: true}

			// Load the OpenAPI specification from the content
			doc, err := loader.LoadFromData([]byte(currentValidation[k]))
			if err != nil {
				logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to loadFromData: %v", err))
				return nil, err
			}

			err = doc.Validate(ctx)
			if err != nil {
				logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to validate: %v", err))
				return nil, err
			}

			validations[k] = currentValidation[k]
		}

	}

	return validations, nil
}

func (r *AddonComplianceReconciler) collectLuaValidations(ctx context.Context,
	addonConstraintScope *scope.AddonComplianceScope, logger logr.Logger) (map[string][]byte, error) {

	logger.V(logs.LogDebug).Info("collect lua validations")
	validations := make(map[string][]byte)
	for i := range addonConstraintScope.AddonCompliance.Spec.LuaValidationRefs {
		ref := &addonConstraintScope.AddonCompliance.Spec.LuaValidationRefs[i]
		currentValidation, err := r.collectValidations(ctx, ref.Kind, ref.Namespace, ref.Name, ref.Path, logger)
		if err != nil {
			return nil, err
		}
		for k := range currentValidation {
			validations[k] = currentValidation[k]
		}
	}

	return validations, nil
}

func (r *AddonComplianceReconciler) collectValidations(ctx context.Context,
	kind, referencedNamespace, referencedName, path string, logger logr.Logger) (map[string][]byte, error) {

	validations := make(map[string][]byte)
	apiVersion := getReferenceAPIVersion(kind)

	var err error

	objRef := &corev1.ObjectReference{
		APIVersion: apiVersion,
		Kind:       kind,
		Namespace:  referencedNamespace,
		Name:       referencedName,
	}

	var tmpValidations [][]byte
	switch kind {
	case string(libsveltosv1alpha1.ConfigMapReferencedResourceKind):
		tmpValidations, err = r.collectContentOfConfigMap(ctx, objRef, logger)
	case string(libsveltosv1alpha1.SecretReferencedResourceKind):
		tmpValidations, err = r.collectContentOfSecret(ctx, objRef, logger)
	case sourcev1b2.OCIRepositoryKind:
	case sourcev1b2.BucketKind:
	case sourcev1.GitRepositoryKind:
		tmpValidations, err = r.collectContentFromFluxSource(ctx, objRef, path, logger)
	}

	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to collect content of %s:%s/%s. Err: %v",
			kind, referencedNamespace, referencedName, err))
		return nil, err
	}

	for i := range tmpValidations {
		key := fmt.Sprintf("%s:%s/%s-%d", kind, referencedNamespace, referencedName, i)
		validations[key] = tmpValidations[i]
	}

	return validations, nil
}

func (r *AddonComplianceReconciler) collectContentOfConfigMap(ctx context.Context, ref *corev1.ObjectReference,
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

func (r *AddonComplianceReconciler) collectContentOfSecret(ctx context.Context, ref *corev1.ObjectReference,
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

func (r *AddonComplianceReconciler) collectContentFromFluxSource(ctx context.Context, ref *corev1.ObjectReference,
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

func (r *AddonComplianceReconciler) annotateClusters(ctx context.Context,
	addonConstraintScope *scope.AddonComplianceScope, logger logr.Logger) error {

	m := GetManager()
	addonConstraint := getKeyFromObject(r.Scheme, addonConstraintScope.AddonCompliance)
	// Returns the list of cluster to annotate
	clustersToAnnotate := m.RemoveAddonCompliance(addonConstraint)
	for i := range clustersToAnnotate {
		if err := r.annotateCluster(ctx, clustersToAnnotate[i]); err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to annotagte cluster %s:%s/%s",
				clustersToAnnotate[i].Kind, clustersToAnnotate[i].Namespace, clustersToAnnotate[i].Name))
			return err
		}
	}

	return nil
}

// Cluster addons are deployed by addon controller.
// Cluster addon compliances are loaded by addon-compliance controller.
//
// When NEW cluster is created it might match both a ClusterProfile and an AddonConstrain:
// - Matching a ClusterProfile means addons need to be deployed (by addon controller).
// - Matching an AddonCompliance means some validations are defined and any addon deployed in
// this cluster should satisfy those validations.
//
// Addon controller and addon-compliance controller needs to act in sync so that when a new
// cluster is discovered:
// - addon-compliance controller first loads all addonConstraint instances;
// - only after that, addon controller starts deploying addons in the newly discovered cluster.
//
// This is achieved by:
// - addon-compliance controller adding an annotation on a cluster;
// - addon controller deploying addons only on clusters with such annotation set.
//
// Addon-compliance controller will add this annotation to any cluster it is aware of
// (if addon-constrain controller is aware of a cluster we can assume validations for such cluster
// are loaded).
// Addon controller won't deploy any addons till this annotation is set on a cluster.
// To know more please refer to loader.go

func (r *AddonComplianceReconciler) annotateCluster(ctx context.Context,
	ref *corev1.ObjectReference) error {

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster, err := clusterproxy.GetCluster(ctx, r.Client, ref.Namespace, ref.Name, clusterproxy.GetClusterType(ref))
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		annotations := cluster.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}
		if _, ok := annotations[libsveltosv1alpha1.GetClusterAnnotation()]; ok {
			return nil
		}
		annotations[libsveltosv1alpha1.GetClusterAnnotation()] = "ok"
		cluster.SetAnnotations(annotations)
		return r.Update(ctx, cluster)
	})
	return err
}
