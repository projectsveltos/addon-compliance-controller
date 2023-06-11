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

// AddonConstraintScopeParams defines the input parameters used to create a new AddonConstraint Scope.
type AddonConstraintScopeParams struct {
	Client          client.Client
	Logger          logr.Logger
	AddonConstraint *libsveltosv1alpha1.AddonConstraint
	ControllerName  string
}

// NewAddonConstraintScope creates a new AddonConstraint Scope from the supplied parameters.
// This is meant to be called for each reconcile iteration.
func NewAddonConstraintScope(params AddonConstraintScopeParams) (*AddonConstraintScope, error) {
	if params.Client == nil {
		return nil, errors.New("client is required when creating a AddonConstraintScope")
	}
	if params.AddonConstraint == nil {
		return nil, errors.New("failed to generate new scope from nil AddonConstraint")
	}

	helper, err := patch.NewHelper(params.AddonConstraint, params.Client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to init patch helper")
	}
	return &AddonConstraintScope{
		Logger:          params.Logger,
		client:          params.Client,
		AddonConstraint: params.AddonConstraint,
		patchHelper:     helper,
		controllerName:  params.ControllerName,
	}, nil
}

// AddonConstraintScope defines the basic context for an actuator to operate upon.
type AddonConstraintScope struct {
	logr.Logger
	client          client.Client
	patchHelper     *patch.Helper
	AddonConstraint *libsveltosv1alpha1.AddonConstraint
	controllerName  string
}

// PatchObject persists the feature configuration and status.
func (s *AddonConstraintScope) PatchObject(ctx context.Context) error {
	return s.patchHelper.Patch(
		ctx,
		s.AddonConstraint,
	)
}

// Close closes the current scope persisting the addonConstraint configuration and status.
func (s *AddonConstraintScope) Close(ctx context.Context) error {
	return s.PatchObject(ctx)
}

// Name returns the AddonConstraint name.
func (s *AddonConstraintScope) Name() string {
	return s.AddonConstraint.Name
}

// ControllerName returns the name of the controller that
// created the AddonConstraintScope.
func (s *AddonConstraintScope) ControllerName() string {
	return s.controllerName
}

// GetSelector returns the ClusterSelector
func (s *AddonConstraintScope) GetSelector() string {
	return string(s.AddonConstraint.Spec.ClusterSelector)
}

// SetMatchingClusterRefs sets the feature status.
func (s *AddonConstraintScope) SetMatchingClusterRefs(matchingClusters []corev1.ObjectReference) {
	s.AddonConstraint.Status.MatchingClusterRefs = matchingClusters
}

// SetFailureMessage sets the failureMessage .
func (s *AddonConstraintScope) SetFailureMessage(failureMessage *string) {
	s.AddonConstraint.Status.FailureMessage = failureMessage
}

// UpdateLabels updates AddonConstraint labels using matching clusters
func (s *AddonConstraintScope) UpdateLabels(matchingClusters []corev1.ObjectReference) {
	labels := make(map[string]string)

	for i := range matchingClusters {
		cluster := &matchingClusters[i]
		clusterType := clusterproxy.GetClusterType(cluster)
		l := libsveltosv1alpha1.GetClusterLabel(cluster.Namespace, cluster.Name, &clusterType)
		labels[l] = "ok"
	}

	s.AddonConstraint.Labels = labels
}
