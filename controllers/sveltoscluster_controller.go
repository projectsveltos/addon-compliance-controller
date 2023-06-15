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

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	"github.com/projectsveltos/libsveltos/lib/clusterproxy"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

// SveltosClusterReconciler reconciles a SveltosCluster object
type SveltosClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=sveltosclusters,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=sveltosclusters/status,verbs=get;list;watch

func (r *SveltosClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := ctrl.LoggerFrom(ctx)
	logger.V(logs.LogInfo).Info("Reconciling SveltosCluster")

	// Fecth the SveltosCluster instance
	sveltosCluster := &libsveltosv1alpha1.SveltosCluster{}
	if err := r.Get(ctx, req.NamespacedName, sveltosCluster); err != nil {
		if apierrors.IsNotFound(err) {
			removeClusterEntry(req.Namespace, req.Name, libsveltosv1alpha1.ClusterTypeSveltos)
			return reconcile.Result{}, nil
		}
		logger.Error(err, "Failed to fetch SveltosCluster")
		return reconcile.Result{}, errors.Wrapf(
			err,
			"Failed to fetch SveltosCluster %s",
			req.NamespacedName,
		)
	}

	// Handle deleted SveltosCluster
	if !sveltosCluster.DeletionTimestamp.IsZero() {
		removeClusterEntry(req.Namespace, req.Name, libsveltosv1alpha1.ClusterTypeSveltos)
		return reconcile.Result{}, nil
	}

	if shouldAddClusterEntry(sveltosCluster, libsveltosv1alpha1.ClusterTypeSveltos) {
		if err := addClusterEntry(ctx, r.Client, req.Namespace, req.Name,
			libsveltosv1alpha1.ClusterTypeSveltos, sveltosCluster.Labels, logger); err != nil {
			return reconcile.Result{}, err
		}

		manager := GetManager()
		clusterInfo := getClusterInfo(sveltosCluster.Namespace, sveltosCluster.Name, libsveltosv1alpha1.ClusterTypeSveltos)
		if manager.GetNumberOfAddonConstraint(clusterInfo) == 0 {
			// if there is no AddonConstraint instance matching this cluster,
			// cluster is ready to have addons deployed. So annotate it.
			if err := annotateCluster(ctx, r.Client, clusterInfo); err != nil {
				return reconcile.Result{}, err
			}
		}
	}

	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SveltosClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&libsveltosv1alpha1.SveltosCluster{}).
		Complete(r)
}

func removeClusterEntry(clusterNamespace, clusterName string, clusterType libsveltosv1alpha1.ClusterType) {

	cluster := getClusterInfo(clusterNamespace, clusterName, clusterType)

	manager := GetManager()
	manager.RemoveClusterEntry(cluster)
}

// shouldAddClusterEntry checks whether this is first time addon-constraint sees this cluster.
// Return false if either one of following is verified:
// - cluster has "addon-constraints-ready" annotation or
// - manager has an entry for this cluster already
func shouldAddClusterEntry(cluster client.Object, clusterType libsveltosv1alpha1.ClusterType) bool {
	if cluster.GetAnnotations() != nil {
		if _, ok := cluster.GetAnnotations()[libsveltosv1alpha1.GetClusterAnnotation()]; ok {
			return false
		}
	}

	clusterInfo := getClusterInfo(cluster.GetNamespace(), cluster.GetName(), clusterType)
	manager := GetManager()
	return !manager.HasEntryForCluster(clusterInfo)
}

// addClusterEntry instructs manager to track this cluster.
func addClusterEntry(ctx context.Context, c client.Client,
	clusterNamespace, clusterName string, clusterType libsveltosv1alpha1.ClusterType,
	clusterLabels map[string]string, logger logr.Logger) error {

	addonConstraints := &libsveltosv1alpha1.AddonConstraintList{}
	if err := c.List(ctx, addonConstraints); err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to collect addonConstraints: %v", err))
		return err
	}

	s := &libsveltosset.Set{}
	for i := range addonConstraints.Items {
		ac := &addonConstraints.Items[i]
		parsedSelector, _ := labels.Parse(string(ac.Spec.ClusterSelector))
		if parsedSelector.Matches(labels.Set(clusterLabels)) {
			s.Insert(getKeyFromObject(c.Scheme(), ac))
		}
	}

	cluster := getClusterInfo(clusterNamespace, clusterName, clusterType)
	manager := GetManager()
	manager.AddClusterEntry(cluster, s)

	return nil
}

func annotateCluster(ctx context.Context, c client.Client, ref *corev1.ObjectReference) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster, err := clusterproxy.GetCluster(ctx, c, ref.Namespace, ref.Name, clusterproxy.GetClusterType(ref))
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
		return c.Update(ctx, cluster)
	})
	return err
}

func getClusterInfo(clusterNamespace, clusterName string, clusterType libsveltosv1alpha1.ClusterType) *corev1.ObjectReference {
	cluster := &corev1.ObjectReference{
		Namespace: clusterNamespace,
		Name:      clusterName,
	}
	if clusterType == libsveltosv1alpha1.ClusterTypeSveltos {
		cluster.Kind = libsveltosv1alpha1.SveltosClusterKind
		cluster.APIVersion = libsveltosv1alpha1.GroupVersion.String()
	} else {
		cluster.Kind = "Cluster"
		cluster.APIVersion = clusterv1.GroupVersion.String()
	}

	return cluster
}
