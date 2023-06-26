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

package scope

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	"github.com/projectsveltos/libsveltos/lib/clusterproxy"
)

// AddonComplianceScopeParams defines the input parameters used to create a new AddonCompliance Scope.
type AddonComplianceScopeParams struct {
	Client          client.Client
	Logger          logr.Logger
	AddonCompliance *libsveltosv1alpha1.AddonCompliance
	ControllerName  string
}

// NewAddonComplianceScope creates a new AddonCompliance Scope from the supplied parameters.
// This is meant to be called for each reconcile iteration.
func NewAddonComplianceScope(params AddonComplianceScopeParams) (*AddonComplianceScope, error) {
	if params.Client == nil {
		return nil, errors.New("client is required when creating a AddonComplianceScope")
	}
	if params.AddonCompliance == nil {
		return nil, errors.New("failed to generate new scope from nil AddonCompliance")
	}

	helper, err := patch.NewHelper(params.AddonCompliance, params.Client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to init patch helper")
	}
	return &AddonComplianceScope{
		Logger:          params.Logger,
		client:          params.Client,
		AddonCompliance: params.AddonCompliance,
		patchHelper:     helper,
		controllerName:  params.ControllerName,
	}, nil
}

// AddonComplianceScope defines the basic context for an actuator to operate upon.
type AddonComplianceScope struct {
	logr.Logger
	client          client.Client
	patchHelper     *patch.Helper
	AddonCompliance *libsveltosv1alpha1.AddonCompliance
	controllerName  string
}

// PatchObject persists the feature configuration and status.
func (s *AddonComplianceScope) PatchObject(ctx context.Context) error {
	return s.patchHelper.Patch(
		ctx,
		s.AddonCompliance,
	)
}

// Close closes the current scope persisting the addonConstraint configuration and status.
func (s *AddonComplianceScope) Close(ctx context.Context) error {
	return s.PatchObject(ctx)
}

// Name returns the AddonCompliance name.
func (s *AddonComplianceScope) Name() string {
	return s.AddonCompliance.Name
}

// ControllerName returns the name of the controller that
// created the AddonComplianceScope.
func (s *AddonComplianceScope) ControllerName() string {
	return s.controllerName
}

// GetSelector returns the ClusterSelector
func (s *AddonComplianceScope) GetSelector() string {
	return string(s.AddonCompliance.Spec.ClusterSelector)
}

// SetMatchingClusterRefs sets the feature status.
func (s *AddonComplianceScope) SetMatchingClusterRefs(matchingClusters []corev1.ObjectReference) {
	s.AddonCompliance.Status.MatchingClusterRefs = matchingClusters
}

// SetFailureMessage sets the failureMessage .
func (s *AddonComplianceScope) SetFailureMessage(failureMessage *string) {
	s.AddonCompliance.Status.FailureMessage = failureMessage
}

// UpdateLabels updates AddonCompliance labels using matching clusters
func (s *AddonComplianceScope) UpdateLabels(matchingClusters []corev1.ObjectReference) {
	labels := make(map[string]string)

	for i := range matchingClusters {
		cluster := &matchingClusters[i]
		clusterType := clusterproxy.GetClusterType(cluster)
		l := libsveltosv1alpha1.GetClusterLabel(cluster.Namespace, cluster.Name, &clusterType)
		labels[l] = "ok"
	}

	s.AddonCompliance.Labels = labels
}
