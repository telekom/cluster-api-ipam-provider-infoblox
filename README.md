# Cluster API IPAM Provider Infoblox

This is an IPAM provider for Cluster API that integrates with Infoblox NIOS for IP address management and DNS.
It allows to allocate addresses from subnets configured in Infoblox. Since it creates Host resources in Infoblox, it can also be used to configure DNS entries for hosts at the same time.

**NOTE: This provider is still under heavy development and not ready for use.**

## Usage

This provider comes with a `InfobloxIPPool` resource to specify the pools from which addresses should be assigned.

```yaml
apiVersion: ipam.cluster.x-k8s.io/v1alpha1
kind: InfobloxIPPool
metadata:
  name: infobloxippool-sample
spec: #tbd
```

## Running Tests

In order to run end-to-end tests, an Infoblox instance needs to be provided. Configuration is done using environment variables. See [.testenv.example](./.testenv.example) for an example.

## Licensing

Copyright (c) 2022 Deutsche Telekom AG.

Licensed under the **Apache License, Version 2.0** (the "License"); you may not use this file except in compliance with the License.

You may obtain a copy of the License at https://www.apache.org/licenses/LICENSE-2.0.

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the [LICENSE](./LICENSE) for the specific language governing permissions and limitations under the License.

### Dependency Licenses

You can find the licenses of used Go dependencies as a `licenses.tar.gz` archive as part of our [releases](https://github.com/telekom/cluster-api-ipam-provider-infoblox/releases) and in the `/license` directory contained in our container images available at [ghcr.io/telekom/cluster-api-ipam-provider-infoblox](https://ghcr.io/telekom/cluster-api-ipam-provider-infoblox).
