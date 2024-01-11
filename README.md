# Cluster API IPAM Provider Infoblox

This is an IPAM provider for Cluster API that integrates with Infoblox NIOS for IP address management and DNS.
It allows to allocate addresses from subnets configured in Infoblox. Since it creates Host resources in Infoblox, it can also be used to configure DNS entries for hosts at the same time.

**NOTE: This provider is still under heavy development so some issues might occur**

## Deploying

### Installing

You can use Makefile to deploy Cluster API IMAP Provider Infoblox. First install all required CRDs using:

```bash
  make install
```

Deploy [cert-manager](https://cert-manager.io):

```bash
  kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.13.3/cert-manager.yaml
```

Apply required Cluster API CRDs:

```bash
  kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/cluster-api/release-1.6/config/crd/bases/ipam.cluster.x-k8s.io_ipaddresses.yaml

  kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/cluster-api/release-1.6/config/crd/bases/ipam.cluster.x-k8s.io_ipaddressclaims.yaml

  kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/cluster-api/release-1.6/config/crd/bases/cluster.x-k8s.io_clusters.yaml
```

Then, deploy provider itself with:

```bash
  make deploy
```

### Configuring Infoblox Instances

Next thing is to define Infoblox Instances (servers) that will be used by the provider. To connect to the instance, passwrd and username, or certificate and key pair is required. Those should be specified using Kubernetes `secret` deployed in the same `namesapce` as the provider's pods:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: some-credentials
  namespace: caip-infoblox-system
type: kubernetes.io/basic-auth
stringData:
  username: <username>
  password: <password>
#or
  clientCert: <cert>
  clientKey: <key>
```

Next, Infoblox Instance should be defined using `InfobloxInstance` CRD. Ypu can see example, with values explained below.

```yaml
apiVersion: ipam.cluster.x-k8s.io/v1beta1
kind: InfobloxInstance
metadata:
  name: infobloxinstance-sample     # name of the instance object.
  namespace: caip-infoblox-system   # namespace of the instance object.
spec:
  credentialsSecretRef:             # reference to the credentials .
    name: some-credentials          # name of the credentials.
  defaultNetworkView: "some-view"   # default Ifoblox network view.
  host: "some.host.com"             # address of the Infoblox server.
  disableTLSVerification: false     # disable/enable SSL verification.
  customCAPath: "/some/path/ca.crt" # path to a file which contians list of custom Certificate Authorities that can be used to verify SSL certifcates if 'disableTLSVerification' is set to 'false'. Host's default authorities will be used if not specified.
  port: "443"                       # network port to be used.
  wapiVersion: "2.12"               # Infoblox Web API version.
```

## Usage

This provider comes with a `InfobloxIPPool` resource to specify the pools from which addresses should be assigned. Here is example definition of `InfobloxIPPool` named `infobloxippool-sample` deployed in `caip-infoblox-system` with additional explanation:

```yaml
apiVersion: ipam.cluster.x-k8s.io/v1beta1
kind: InfobloxIPPool
metadata:
  name: infobloxippool-sample
  namespace: caip-infoblox-system
spec:
  instance:
    name: "infobloxinstance-sample" # name of the instance that should be used by pool
  networkView: "some-view"          # Infoblox network view that will be used
  subnets:                          # list of the subnets in the network view we want to get IP addresses from
    - cidr: "10.0.0.0/24"           # subnet CIDR
      gateway: "10.0.0.1"           # gateway that should ba assigned to the IP Address claim
  dnsZone: ""                       # Infoblox's DNS zone
```

Now, whenever `IPAddressClaim` that references `infobloxippool-sample` will be created, Infoblox instance `infobloxinstance-sample` will be queried to get next free IP from the `10.0.0.0/24` subnet.

> NOTE: You can find all the example files described above in [config/samples](./config/samples).

## Running Tests

### E2E tests

In order to run end-to-end tests, an Infoblox instance needs to be provided. Configuration is done using environment variables. See [.testenv.example](./.testenv.example) for an example.

To execute e2e test simply setup required enironment variables and run `make test-infoblox`.

### Unit tests

Unit tests can be run using `make test` command.

To execute unit tests [controller-gen](https://book.kubebuilder.io/reference/controller-gen) is required. You can install it using `make controller-gen` command.

> NOTE: you can run both unit tests and e2e tests usin `make test-all`.

## Licensing

Copyright (c) 2023 Deutsche Telekom AG.

Licensed under the **Apache License, Version 2.0** (the "License"); you may not use this file except in compliance with the License.

You may obtain a copy of the License at https://www.apache.org/licenses/LICENSE-2.0.

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the [LICENSE](./LICENSE) for the specific language governing permissions and limitations under the License.

### Dependency Licenses

You can find the licenses of used Go dependencies as a `licenses.tar.gz` archive as part of our [releases](https://github.com/telekom/cluster-api-ipam-provider-infoblox/releases) and in the `/license` directory contained in our container images available at [ghcr.io/telekom/cluster-api-ipam-provider-infoblox](https://ghcr.io/telekom/cluster-api-ipam-provider-infoblox).
