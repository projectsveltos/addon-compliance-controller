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

package controllers_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/klog/v2/textlogger"

	"github.com/projectsveltos/addon-compliance-controller/controllers"
)

var _ = Describe("Utilities", func() {

	It("collectContentOfSecret returns openapi policy", func() {
		// Create a temporary directory for testing
		dir, err := os.MkdirTemp("", "testdir")
		Expect(err).To(BeNil())
		defer os.RemoveAll(dir)

		// Create a few test files with content
		files := []struct {
			name    string
			content string
		}{
			{"depl.txt", deplReplicaSpec},
			{"name.txt", nameSpec},
		}

		for _, f := range files {
			filePath := filepath.Join(dir, f.name)
			err = os.WriteFile(filePath, []byte(f.content), 0600)
			Expect(err).To(BeNil())
		}

		var policies map[string]string
		policies, err = controllers.WalkDir(dir, textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))))
		Expect(err).To(BeNil())
		Expect(len(policies)).To(Equal(2))
	})
})
