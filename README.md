[![CI](https://github.com/projectsveltos/addon-compliance-controller/actions/workflows/main.yaml/badge.svg)](https://github.com/projectsveltos/addon-compliance-controller/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/projectsveltos/addon-compliance-controller)](https://goreportcard.com/report/github.com/projectsveltos/addon-compliance-controller)
[![Slack](https://img.shields.io/badge/join%20slack-%23projectsveltos-brighteen)](https://join.slack.com/t/projectsveltos/shared_invite/zt-1hraownbr-W8NTs6LTimxLPB8Erj8Q6Q)
[![License](https://img.shields.io/badge/license-Apache-blue.svg)](LICENSE)

# libsveltos

<img src="https://raw.githubusercontent.com/projectsveltos/sveltos/main/docs/assets/logo.png" width="200">

Please refere to sveltos [documentation](https://projectsveltos.github.io/sveltos/).

## What this repository is
Sveltos has the ability to deploy various types of Kubernetes addons across multiple clusters. It supports Helm charts, Kustomize files, YAMLs, Jsonnet, and Carvel ytt. Sveltos can retrieve configuration from diverse sources, including Git repositories. Prior to deploying addons, Sveltos can be directed to validate them against a predefined set of openapi rules.

Within this repository, you'll find a Kubernetes controller that can fetch addon compliances from different sources and provide them to the addon controller. This enables the addon controller to validate addons before deployment, ensuring that no Kubernetes addons are deployed that violate your own rules.

Following is an example enforcing deployments have at least 3 replicas enforced in any cluster matching the label selector `env=production`

```yaml
apiVersion: lib.projectsveltos.io/v1alpha1
kind: AddonCompliance
metadata:
 name: depl-replica
spec:
  clusterSelector: env=production
  openAPIValidationRefs:
  - namespace: default
    name: openapi-deployment
    kind: ConfigMap
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: openapi-deployment
  namespace: default
data:
  openapi.yaml: |
    openapi: 3.0.0
    info:
      title: Kubernetes Replica Validation
      version: 1.0.0

    paths:
      /apis/apps/v1/namespaces/{namespace}/deployments:
        post:
          parameters:
            - in: path
              name: namespace
              required: true
              schema:
                type: string
                minimum: 1
              description: The namespace of the resource
          summary: Create/Update a new deployment
          requestBody:
            required: true
            content:
              application/json:
                schema:
                  $ref: '#/components/schemas/Deployment'
          responses:
            '200':
              description: OK
            '400':
              description: Invalid Deployment. Each deployment in a production cluster requires at least 3 replicas

    components:
      schemas:
        Deployment:
          type: object
          properties:
            metadata:
              type: object
              properties:
                name:
                  type: string
            spec:
              type: object
              properties:
                replicas:
                  type: integer
                  minimum: 3
```

## Contributing 

❤️ Your contributions are always welcome! If you want to contribute, have questions, noticed any bug or want to get the latest project news, you can connect with us in the following ways:

1. Open a bug/feature enhancement on github [![contributions welcome](https://img.shields.io/badge/contributions-welcome-brightgreen.svg?style=flat)](https://github.com/projectsveltos/sveltos-manager/issues)
2. Chat with us on the Slack in the #projectsveltos channel [![Slack](https://img.shields.io/badge/join%20slack-%23projectsveltos-brighteen)](https://join.slack.com/t/projectsveltos/shared_invite/zt-1hraownbr-W8NTs6LTimxLPB8Erj8Q6Q)
3. [Contact Us](mailto:support@projectsveltos.io)

## License

Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
