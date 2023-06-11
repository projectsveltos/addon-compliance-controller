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
	"sync"

	corev1 "k8s.io/api/core/v1"

	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

// Cluster addons are deployed by addon controller.
// Cluster addon constraints are loaded by addon-constraint controller.
//
// When NEW cluster is created it might match both a ClusterProfile and an AddonConstrain:
// - Matching a ClusterProfile means addons need to be deployed (by addon controller).
// - Matching an AddonConstraint means some validations are defined and any addon deployed in
// this cluster should satisfy those validations.
//
// Addon controller and addon-constraint controller needs to act in sync so that when a new
// cluster is discovered:
// - addon-constraint controller first loads all addonConstraint instances;
// - only after that, addon controller starts deploying addons in the newly discovered cluster.
//
// This is achieved by:
// - addon-constraint controller adding an annotation on a cluster;
// - addon controller deploying addons only on clusters with such annotation set.
//
// Addon-constraint controller uses following logic to understand when to annotate a cluster:
// - SveltosCluster/Cluster reconcilers insert an entry for a cluster in the managerInstance.addonConstraints
// map (if one does not exist already) with all the AddonConstraints matching the cluster at that very
// precise time;
// - SveltosCluster/Cluster reconcilers annotate the cluster if there is no AddonConstraint matching
// it;
// - AddonConstraint reconciler will fail if it finds an AddonConstraint is a match for a cluster
// and there is no entry for this cluster in managerInstance.addonConstraints map;
// - AddonConstraint reconciler, at the end of each successufl reconciliation, walks the
// managerInstance.addonConstraints maps and removes the reconciled instance in any set it is currently
// present. Any cluster whose corresponding set reduces to zero, it is annotate.

var (
	getManagerLock  = &sync.Mutex{}
	managerInstance *manager
)

type manager struct {
	muMap *sync.RWMutex

	// This map contains a list of AddonConstraints for each cluster.
	//
	// During the reconciliation process of Cluster/SveltosCluster controllers,
	// they check if an entry exists for the cluster in this map.
	// If an entry is present, no further action is taken.
	// If there is no entry for the cluster, a new entry is added with all the AddonConstraints
	// that match the cluster at that moment.

	// When the AddonConstraint controller reconciles an instance, it retrieves all the clusters that
	// are a match for the instance. If a matching cluster is found but there is no corresponding entry
	// in this map, the reconciler returns an error.
	// This check is necessary to prevent a situation where an AddonConstraint instance is initially added
	// as a match for a cluster but is never removed because the AddonConstraint was already reconciled.

	// Conversely, if an AddonConstraint is reconciled successfully, it is removed from any entry in this map.
	// If all AddonConstraints are removed from this map for a specific cluster, it indicates that all the
	// existing AddonConstraints (at the time the cluster was created) have been loaded. This signifies that
	// the addon deployment can begin, and the cluster is annotated to indicate that it is ready for the addon
	// to be deployed.

	addonConstraints map[corev1.ObjectReference]*libsveltosset.Set
}

// initializeManager initializes a manager
func initializeManager() {
	if managerInstance == nil {
		getManagerLock.Lock()
		defer getManagerLock.Unlock()
		if managerInstance == nil {
			managerInstance = &manager{}

			managerInstance.muMap = &sync.RWMutex{}
			managerInstance.addonConstraints = make(map[corev1.ObjectReference]*libsveltosset.Set)
		}
	}
}

// GetManager returns the manager instance implementing the ClassifierInterface.
// Returns nil if manager has not been initialized yet
func GetManager() *manager {
	initializeManager()
	return managerInstance
}

func (m *manager) AddClusterEntry(cluster *corev1.ObjectReference, addonConstrains *libsveltosset.Set) {
	clusterKey := corev1.ObjectReference{
		Namespace:  cluster.Namespace,
		Name:       cluster.Name,
		Kind:       cluster.Kind,
		APIVersion: cluster.APIVersion,
	}

	m.muMap.Lock()
	defer m.muMap.Unlock()

	m.addonConstraints[clusterKey] = addonConstrains
}

func (m *manager) RemoveClusterEntry(cluster *corev1.ObjectReference) {
	clusterKey := corev1.ObjectReference{
		Namespace:  cluster.Namespace,
		Name:       cluster.Name,
		Kind:       cluster.Kind,
		APIVersion: cluster.APIVersion,
	}

	m.muMap.Lock()
	defer m.muMap.Unlock()

	delete(m.addonConstraints, clusterKey)
}

// HasEntryForCluster returns true if an entry for this cluster is found in
// addonConstraints
func (m *manager) HasEntryForCluster(cluster *corev1.ObjectReference) bool {
	clusterKey := corev1.ObjectReference{
		Namespace:  cluster.Namespace,
		Name:       cluster.Name,
		Kind:       cluster.Kind,
		APIVersion: cluster.APIVersion,
	}

	m.muMap.Lock()
	defer m.muMap.Unlock()

	_, ok := m.addonConstraints[clusterKey]
	return ok
}

// GetNumberOfAddonConstraint returns, per cluster, number of addonconstraints yet
// to be evaluated
func (m *manager) GetNumberOfAddonConstraint(cluster *corev1.ObjectReference) int {
	clusterKey := corev1.ObjectReference{
		Namespace:  cluster.Namespace,
		Name:       cluster.Name,
		Kind:       cluster.Kind,
		APIVersion: cluster.APIVersion,
	}

	m.muMap.Lock()
	defer m.muMap.Unlock()

	v := m.addonConstraints[clusterKey]
	if v == nil {
		return 0
	}

	return v.Len()
}

// RemoveAddonConstraint walks addonConstraints map and removes all references to addonConstraints
func (m *manager) RemoveAddonConstraint(addonConstraint *corev1.ObjectReference) []*corev1.ObjectReference {
	clusterToAnnotate := make([]*corev1.ObjectReference, 0)
	m.muMap.Lock()
	defer m.muMap.Unlock()

	for key := range m.addonConstraints {
		if m.addonConstraints[key].Len() == 0 {
			continue
		}
		m.addonConstraints[key].Erase(addonConstraint)
		if m.addonConstraints[key].Len() == 0 {
			keyCopy := key
			clusterToAnnotate = append(clusterToAnnotate, &keyCopy)
		}
	}

	return clusterToAnnotate
}
