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

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sourcev1b2 "github.com/fluxcd/source-controller/api/v1beta2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2/textlogger"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

func (r *AddonComplianceReconciler) requeueAddonComplianceForFluxSources(
	ctx context.Context, o client.Object,
) []reconcile.Request {

	logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))).WithValues(
		"reference", fmt.Sprintf("%s/%s", o.GetNamespace(), o.GetName()))

	logger.V(logs.LogDebug).Info("reacting to flux source change")

	r.PolicyMux.Lock()
	defer r.PolicyMux.Unlock()

	// Following is needed as o.GetObjectKind().GroupVersionKind().Kind is not set
	var key corev1.ObjectReference
	switch o.(type) {
	case *sourcev1.GitRepository:
		key = corev1.ObjectReference{
			APIVersion: sourcev1.GroupVersion.String(),
			Kind:       sourcev1.GitRepositoryKind,
			Namespace:  o.GetNamespace(),
			Name:       o.GetName(),
		}
	case *sourcev1b2.OCIRepository:
		key = corev1.ObjectReference{
			APIVersion: sourcev1b2.GroupVersion.String(),
			Kind:       sourcev1b2.OCIRepositoryKind,
			Namespace:  o.GetNamespace(),
			Name:       o.GetName(),
		}
	case *sourcev1b2.Bucket:
		key = corev1.ObjectReference{
			APIVersion: sourcev1b2.GroupVersion.String(),
			Kind:       sourcev1b2.BucketKind,
			Namespace:  o.GetNamespace(),
			Name:       o.GetName(),
		}
	default:
		key = corev1.ObjectReference{
			APIVersion: o.GetObjectKind().GroupVersionKind().GroupVersion().String(),
			Kind:       o.GetObjectKind().GroupVersionKind().Kind,
			Namespace:  o.GetNamespace(),
			Name:       o.GetName(),
		}
	}

	logger.V(logs.LogDebug).Info(fmt.Sprintf("referenced key: %s", key))

	requests := make([]ctrl.Request, r.getReferenceMapForEntry(&key).Len())

	consumers := r.getReferenceMapForEntry(&key).Items()
	for i := range consumers {
		logger.V(logs.LogDebug).Info(fmt.Sprintf("requeue consumer: %s", consumers[i]))
		requests[i] = ctrl.Request{
			NamespacedName: client.ObjectKey{
				Name:      consumers[i].Name,
				Namespace: consumers[i].Namespace,
			},
		}
	}

	return requests
}

func (r *AddonComplianceReconciler) requeueAddonComplianceForReference(
	ctx context.Context, o client.Object,
) []reconcile.Request {

	logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))).WithValues(
		"reference", fmt.Sprintf("%s/%s", o.GetNamespace(), o.GetName()))

	logger.V(logs.LogDebug).Info("reacting to configMap/secret change")

	r.PolicyMux.Lock()
	defer r.PolicyMux.Unlock()

	// Following is needed as o.GetObjectKind().GroupVersionKind().Kind is not set
	var key corev1.ObjectReference
	switch o.(type) {
	case *corev1.ConfigMap:
		key = corev1.ObjectReference{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       string(libsveltosv1alpha1.ConfigMapReferencedResourceKind),
			Namespace:  o.GetNamespace(),
			Name:       o.GetName(),
		}
	case *corev1.Secret:
		key = corev1.ObjectReference{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       string(libsveltosv1alpha1.SecretReferencedResourceKind),
			Namespace:  o.GetNamespace(),
			Name:       o.GetName(),
		}
	default:
		key = corev1.ObjectReference{
			APIVersion: o.GetObjectKind().GroupVersionKind().GroupVersion().String(),
			Kind:       o.GetObjectKind().GroupVersionKind().Kind,
			Namespace:  o.GetNamespace(),
			Name:       o.GetName(),
		}
	}

	logger.V(logs.LogDebug).Info(fmt.Sprintf("referenced key: %s", key))

	requests := make([]ctrl.Request, r.getReferenceMapForEntry(&key).Len())
	consumers := r.getReferenceMapForEntry(&key).Items()
	for i := range consumers {
		l := logger.WithValues("addoncompliance", consumers[i].Name)
		l.V(logs.LogDebug).Info("queuing AddonCompliance")
		requests[i] = ctrl.Request{
			NamespacedName: client.ObjectKey{
				Name:      consumers[i].Name,
				Namespace: consumers[i].Namespace,
			},
		}
	}

	return requests
}

func (r *AddonComplianceReconciler) requeueAddonComplianceForCluster(
	ctx context.Context, o client.Object,
) []reconcile.Request {

	cluster := o
	logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))).WithValues(
		"cluster", fmt.Sprintf("%s/%s", cluster.GetNamespace(), cluster.GetName()))

	logger.V(logs.LogDebug).Info("reacting to Cluster change")

	r.PolicyMux.Lock()
	defer r.PolicyMux.Unlock()

	addTypeInformationToObject(r.Scheme, cluster)

	apiVersion, kind := cluster.GetObjectKind().GroupVersionKind().ToAPIVersionAndKind()

	clusterInfo := corev1.ObjectReference{APIVersion: apiVersion, Kind: kind,
		Namespace: cluster.GetNamespace(), Name: cluster.GetName()}

	r.ClusterLabels[clusterInfo] = o.GetLabels()

	// Get all AddonCompliance previously matching this cluster and reconcile those
	requests := make([]ctrl.Request, r.getClusterMapForEntry(&clusterInfo).Len())
	consumers := r.getClusterMapForEntry(&clusterInfo).Items()

	for i := range consumers {
		l := logger.WithValues("addoncompliance", consumers[i].Name)
		l.V(logs.LogDebug).Info("queuing AddonCompliance")
		requests[i] = ctrl.Request{
			NamespacedName: client.ObjectKey{
				Name: consumers[i].Name,
			},
		}
	}

	// Iterate over all current AddonCompliance and reconcile the AddonCompliance now
	// matching the Cluster
	for k := range r.AddonCompliances {
		addonConstraintSelector := r.AddonCompliances[k]
		parsedSelector, err := labels.Parse(string(addonConstraintSelector))
		if err != nil {
			// When clusterSelector is fixed, this AddonCompliance instance
			// will be reconciled
			continue
		}
		if parsedSelector.Matches(labels.Set(cluster.GetLabels())) {
			l := logger.WithValues("addonConstraint", k.Name)
			l.V(logs.LogDebug).Info("queuing AddonCompliance")
			requests = append(requests, ctrl.Request{
				NamespacedName: client.ObjectKey{
					Name: k.Name,
				},
			})
		}
	}

	return requests
}

func (r *AddonComplianceReconciler) requeueAddonComplianceForMachine(
	ctx context.Context, o client.Object,
) []reconcile.Request {

	machine := o.(*clusterv1.Machine)
	logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))).WithValues(
		"machine", fmt.Sprintf("%s/%s", machine.GetNamespace(), machine.GetName()))

	addTypeInformationToObject(r.Scheme, machine)

	logger.V(logs.LogDebug).Info("reacting to CAPI Machine change")

	ClusterNameLabel, ok := machine.Labels[clusterv1.ClusterNameLabel]
	if !ok {
		logger.V(logs.LogVerbose).Info("Machine has not ClusterNameLabel")
		return nil
	}

	r.PolicyMux.Lock()
	defer r.PolicyMux.Unlock()

	clusterInfo := corev1.ObjectReference{APIVersion: machine.APIVersion, Kind: "Cluster", Namespace: machine.Namespace, Name: ClusterNameLabel}

	// Get all AddonCompliance previously matching this cluster and reconcile those
	requests := make([]ctrl.Request, r.getClusterMapForEntry(&clusterInfo).Len())
	consumers := r.getClusterMapForEntry(&clusterInfo).Items()

	for i := range consumers {
		requests[i] = ctrl.Request{
			NamespacedName: client.ObjectKey{
				Name: consumers[i].Name,
			},
		}
	}

	// Get Cluster labels
	if clusterLabels, ok := r.ClusterLabels[clusterInfo]; ok {
		// Iterate over all current AddonCompliance and reconcile the AddonCompliance now
		// matching the Cluster
		for k := range r.AddonCompliances {
			addonConstraintSelector := r.AddonCompliances[k]
			parsedSelector, err := labels.Parse(string(addonConstraintSelector))
			if err != nil {
				// When clusterSelector is fixed, this AddonCompliance instance
				// will be reconciled
				continue
			}
			if parsedSelector.Matches(labels.Set(clusterLabels)) {
				l := logger.WithValues("addonConstraint", k.Name)
				l.V(logs.LogDebug).Info("queuing AddonCompliance")
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKey{
						Name: k.Name,
					},
				})
			}
		}
	}

	return requests
}
