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
	"regexp"
	"strings"

	"github.com/fluxcd/pkg/http/fetch"
	"github.com/fluxcd/pkg/tar"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sourcev1b2 "github.com/fluxcd/source-controller/api/v1beta2"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

const (
	separator string = "---\n"
)

//+kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=debuggingconfigurations,verbs=get;list;watch

func InitScheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := apiextensionsv1.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := clusterv1.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := sourcev1.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := sourcev1b2.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := libsveltosv1alpha1.AddToScheme(s); err != nil {
		return nil, err
	}
	return s, nil
}

// getKeyFromObject returns the Key that can be used in the internal reconciler maps.
func getKeyFromObject(scheme *runtime.Scheme, obj client.Object) *corev1.ObjectReference {
	addTypeInformationToObject(scheme, obj)

	apiVersion, kind := obj.GetObjectKind().GroupVersionKind().ToAPIVersionAndKind()

	return &corev1.ObjectReference{
		Namespace:  obj.GetNamespace(),
		Name:       obj.GetName(),
		Kind:       kind,
		APIVersion: apiVersion,
	}
}

func addTypeInformationToObject(scheme *runtime.Scheme, obj client.Object) {
	gvks, _, err := scheme.ObjectKinds(obj)
	if err != nil {
		panic(1)
	}

	for _, gvk := range gvks {
		if gvk.Kind == "" {
			continue
		}
		if gvk.Version == "" || gvk.Version == runtime.APIVersionInternal {
			continue
		}
		obj.GetObjectKind().SetGroupVersionKind(gvk)
		break
	}
}

func getReferenceAPIVersion(kind string) string {
	switch kind {
	case string(libsveltosv1alpha1.ConfigMapReferencedResourceKind):
		return corev1.SchemeGroupVersion.String()
	case string(libsveltosv1alpha1.SecretReferencedResourceKind):
		return corev1.SchemeGroupVersion.String()
	case sourcev1b2.OCIRepositoryKind:
	case sourcev1b2.BucketKind:
		return sourcev1b2.GroupVersion.String()
	case sourcev1.GitRepositoryKind:
		return sourcev1.GroupVersion.String()
	}

	return ""
}

// getConfigMap retrieves any ConfigMap from the given name and namespace.
func getConfigMap(ctx context.Context, c client.Client, configmapName types.NamespacedName) (*corev1.ConfigMap, error) {
	configMap := &corev1.ConfigMap{}
	configMapKey := client.ObjectKey{
		Namespace: configmapName.Namespace,
		Name:      configmapName.Name,
	}
	if err := c.Get(ctx, configMapKey, configMap); err != nil {
		return nil, err
	}

	return configMap, nil
}

// getSecret retrieves any Secret from the given secret name and namespace.
func getSecret(ctx context.Context, c client.Client, secretName types.NamespacedName) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	secretKey := client.ObjectKey{
		Namespace: secretName.Namespace,
		Name:      secretName.Name,
	}
	if err := c.Get(ctx, secretKey, secret); err != nil {
		return nil, err
	}

	if secret.Type != libsveltosv1alpha1.ClusterProfileSecretType {
		return nil, libsveltosv1alpha1.ErrSecretTypeNotSupported
	}

	return secret, nil
}

// removeCommentsAndEmptyLines removes any line containing just YAML comments
// and any empty lines
func removeCommentsAndEmptyLines(text string) string {
	commentLine := regexp.MustCompile(`(?m)^\s*#([^#].*?)$`)
	result := commentLine.ReplaceAllString(text, "")
	emptyLine := regexp.MustCompile(`(?m)^\s*$`)
	result = emptyLine.ReplaceAllString(result, "")
	return result
}

// collectContent collect policies contained in data map and validates content.
// data might have one or more keys. Each key might contain a single policy
// or multiple policies separated by '---'
// Returns an error if one occurred. Otherwise it returns a slice of []byte.
func collectContent(data map[string]string) [][]byte {
	policies := make([][]byte, 0)

	for k := range data {
		elements := strings.Split(data[k], separator)
		for i := range elements {
			section := removeCommentsAndEmptyLines(elements[i])
			if section == "" {
				continue
			}

			policies = append(policies, []byte(section))
		}
	}

	return policies
}

func prepareFileSystemWithFluxSource(ctx context.Context, c client.Client,
	ref *corev1.ObjectReference, logger logr.Logger) (string, error) {

	fluxSource, err := getSource(ctx, c, ref)
	if err != nil {
		return "", err
	}

	if fluxSource == nil {
		return "", fmt.Errorf("source %s %s/%s not found",
			ref.Kind, ref.Namespace, ref.Name)
	}

	if fluxSource.GetArtifact() == nil {
		msg := "Source is not ready, artifact not found"
		logger.V(logs.LogInfo).Info(msg)
		return "", err
	}

	// Create tmp dir.
	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("kustomization-%s-%s",
		ref.Namespace, ref.Name))
	if err != nil {
		err = fmt.Errorf("tmp dir error: %w", err)
		return "", err
	}

	artifactFetcher := fetch.NewArchiveFetcher(
		1,
		tar.UnlimitedUntarSize,
		tar.UnlimitedUntarSize,
		os.Getenv("SOURCE_CONTROLLER_LOCALHOST"),
	)

	// Download artifact and extract files to the tmp dir.
	err = artifactFetcher.Fetch(fluxSource.GetArtifact().URL, fluxSource.GetArtifact().Digest, tmpDir)
	if err != nil {
		return "", err
	}

	return tmpDir, nil
}

func getSource(ctx context.Context, c client.Client, ref *corev1.ObjectReference) (sourcev1.Source, error) {
	var src sourcev1.Source
	namespacedName := types.NamespacedName{
		Namespace: ref.Namespace,
		Name:      ref.Name,
	}

	switch ref.Kind {
	case sourcev1.GitRepositoryKind:
		var repository sourcev1.GitRepository
		err := c.Get(ctx, namespacedName, &repository)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("unable to get source '%s': %w", namespacedName, err)
		}
		src = &repository
	case sourcev1b2.OCIRepositoryKind:
		var repository sourcev1b2.OCIRepository
		err := c.Get(ctx, namespacedName, &repository)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil, nil
			}
			return src, fmt.Errorf("unable to get source '%s': %w", namespacedName, err)
		}
		src = &repository
	case sourcev1b2.BucketKind:
		var bucket sourcev1b2.Bucket
		err := c.Get(ctx, namespacedName, &bucket)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil, nil
			}
			return src, fmt.Errorf("unable to get source '%s': %w", namespacedName, err)
		}
		src = &bucket
	default:
		return src, fmt.Errorf("source `%s` kind '%s' not supported",
			ref.Name, ref.Kind)
	}

	return src, nil
}

func walkDir(root string, logger logr.Logger) (map[string]string, error) {
	fileContents := make(map[string]string)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.Mode().IsRegular() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			fileContents[path] = string(content)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return fileContents, nil
}
