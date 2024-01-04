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

package fv_test

import (
	"context"
	"fmt"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
)

var (
	appLabel = `function evaluate()
  local hs = {}
  hs.valid = true
  hs.message = ""

  local deployments = {}
  local pattern = "^prod"
  
  -- Separate deployments and services from the resources
  for _, resource in ipairs(resources) do
    if resource.kind == "Deployment" then
      table.insert(deployments, resource)
    end
  end

  -- Check for each deployment if there is a 'app' label
  for _, deployment in ipairs(deployments) do
    if deployment.metadata.labels == nil then
	  hs.valid = false
	else if not deployment.metadata.labels['app'] then
	  hs.valid = false
	end
  end

  return hs
end`

	deploymentReplicaCheck = `
function evaluate()
  local hs = {}
  hs.valid = true
  hs.message = ""

  local deployments = {}
  local pattern = "^prod"
  
  -- Separate deployments and services from the resources
  for _, resource in ipairs(resources) do
    if resource.kind == "Deployment" then
      table.insert(deployments, resource)
    end
  end

  -- Check for each deployment if there is a matching service
  for _, deployment in ipairs(deployments) do
    local deploymentInfo = deployment.metadata.namespace .. "/" .. deployment.metadata.name
    
    if not string.match(deployment.metadata.name, pattern) then
      hs.message = "No matching service found for deployment: " .. deploymentInfo
      hs.valid = false
      break
    end
  end

  return hs
end
`
)

var _ = Describe("AddonCompliance with ConfigMap", Serial, func() {
	const (
		namePrefix = "cm-"
	)

	It("Process a ConfigMap with Lua policies", Label("FV"), func() {
		configMap := createConfigMapWithPolicy(randomString(), randomString(), []string{appLabel, deploymentReplicaCheck}...)
		verifyYttSourceWithConfigMap(namePrefix, configMap, 2)
	})
})

func verifyYttSourceWithConfigMap(namePrefix string, configMap *corev1.ConfigMap, expectedResources int) {
	Byf("Creating namespace %s", configMap.Namespace)
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: configMap.Namespace,
		},
	}
	Expect(k8sClient.Create(context.TODO(), ns)).To(Succeed())

	Byf("Creating ConfigMap %s/%s", configMap.Namespace, configMap.Name)
	Expect(k8sClient.Create(context.TODO(), configMap)).To(Succeed())

	Byf("Creating a AddonCompliance referencing this ConfigMap")
	addonConstraint := getAddonCompliance(namePrefix, map[string]string{key: value})
	addonConstraint.Spec.LuaValidationRefs = []libsveltosv1alpha1.LuaValidationRef{
		{
			Namespace: configMap.Namespace,
			Name:      configMap.Name,
			Kind:      configMapKind,
		},
	}
	Expect(k8sClient.Create(context.TODO(), addonConstraint)).To(Succeed())

	Byf("Verifying AddonCompliance %s Status", addonConstraint.Name)
	Eventually(func() bool {
		currentAddonCompliance := &libsveltosv1alpha1.AddonCompliance{}
		err := k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: addonConstraint.Name},
			currentAddonCompliance)
		if err != nil {
			return false
		}
		if currentAddonCompliance.Status.LuaValidations == nil {
			return false
		}
		return true
	}, timeout, pollingInterval).Should(BeTrue())

	Byf("Verifying AddonCompliance %s Status.LuaValidations", addonConstraint.Name)
	currentAddonCompliance := &libsveltosv1alpha1.AddonCompliance{}
	Expect(k8sClient.Get(context.TODO(),
		types.NamespacedName{Namespace: addonConstraint.Namespace, Name: addonConstraint.Name},
		currentAddonCompliance)).To(Succeed())
	Expect(len(currentAddonCompliance.Status.LuaValidations)).To(Equal(expectedResources))

	Byf("Verifying AddonCompliance %s Status.MatchingClusterRefs", addonConstraint.Name)
	Expect(len(currentAddonCompliance.Status.MatchingClusterRefs)).To(Equal(1))

	Byf("Updating ConfigMap %s/%s", configMap.Namespace, configMap.Name)
	newConfigMap := createConfigMapWithPolicy(configMap.Namespace, configMap.Name, []string{appLabel}...)
	currentConfigMap := &corev1.ConfigMap{}
	Expect(k8sClient.Get(context.TODO(),
		types.NamespacedName{Namespace: configMap.Namespace, Name: configMap.Name}, currentConfigMap)).To(Succeed())
	currentConfigMap.Data = newConfigMap.Data
	Expect(k8sClient.Update(context.TODO(), currentConfigMap)).To(Succeed())

	Byf("Verifying AddonCompliance %s Status", addonConstraint.Name)
	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: addonConstraint.Name},
			currentAddonCompliance)
		if err != nil {
			return false
		}
		if currentAddonCompliance.Status.LuaValidations == nil {
			return false
		}
		return len(currentAddonCompliance.Status.LuaValidations) == 1
	}, timeout, pollingInterval).Should(BeTrue())

	Byf("Deleting AddonCompliance %s", addonConstraint.Name)
	Expect(k8sClient.Get(context.TODO(),
		types.NamespacedName{Namespace: addonConstraint.Namespace, Name: addonConstraint.Name},
		currentAddonCompliance)).To(Succeed())
	Expect(k8sClient.Delete(context.TODO(), currentAddonCompliance)).To(Succeed())

	Byf("Deleting Namespace %s", ns.Name)
	currentNs := &corev1.Namespace{}
	Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: ns.Name}, currentNs)).To(Succeed())
	Expect(k8sClient.Delete(context.TODO(), currentNs)).To(Succeed())
}

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

	addTypeInformationToObject(scheme, cm)

	return cm
}
