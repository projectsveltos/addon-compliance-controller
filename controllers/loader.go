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
// Addon-compliance controller uses following logic to understand when to annotate a cluster:
// - SveltosCluster/Cluster reconcilers insert an entry for a cluster in the managerInstance.addonConstraints
// map (if one does not exist already) with all the AddonCompliances matching the cluster at that very
// precise time;
// - SveltosCluster/Cluster reconcilers annotate the cluster if there is no AddonCompliance matching
// it;
// - AddonCompliance reconciler will fail if it finds an AddonCompliance is a match for a cluster
// and there is no entry for this cluster in managerInstance.addonConstraints map;
// - AddonCompliance reconciler, at the end of each successufl reconciliation, walks the
// managerInstance.addonConstraints maps and removes the reconciled instance in any set it is currently
// present. Any cluster whose corresponding set reduces to zero, it is annotate.

var (
	getManagerLock  = &sync.Mutex{}
	managerInstance *manager
)

type manager struct {
	muMap *sync.RWMutex

	// This map contains a list of AddonCompliances for each cluster.
	//
	// During the reconciliation process of Cluster/SveltosCluster controllers,
	// they check if an entry exists for the cluster in this map.
	// If an entry is present, no further action is taken.
	// If there is no entry for the cluster, a new entry is added with all the AddonCompliances
	// that match the cluster at that moment.

	// When the AddonCompliance controller reconciles an instance, it retrieves all the clusters that
	// are a match for the instance. If a matching cluster is found but there is no corresponding entry
	// in this map, the reconciler returns an error.
	// This check is necessary to prevent a situation where an AddonCompliance instance is initially added
	// as a match for a cluster but is never removed because the AddonCompliance was already reconciled.

	// Conversely, if an AddonCompliance is reconciled successfully, it is removed from any entry in this map.
	// If all AddonCompliances are removed from this map for a specific cluster, it indicates that all the
	// existing AddonCompliances (at the time the cluster was created) have been loaded. This signifies that
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

// GetNumberOfAddonCompliance returns, per cluster, number of addoncompliances yet
// to be evaluated
func (m *manager) GetNumberOfAddonCompliance(cluster *corev1.ObjectReference) int {
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

// RemoveAddonCompliance walks addonConstraints map and removes all references to addonConstraints
func (m *manager) RemoveAddonCompliance(addonConstraint *corev1.ObjectReference) []*corev1.ObjectReference {
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
