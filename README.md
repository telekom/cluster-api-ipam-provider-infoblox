# Cluster API IPAM Provider Infoblox

This is an IPAM provider for Cluster API that integrates with Infoblox NIOS for IP address management and DNS.
It allows to allocate addresses from subnets configured in Infoblox and allows to add DNS entries for those allocated addresses as well.

## Deploying

This provider can be installed using `clusterctl install`. Since it's not yet added to the integrated list of providers, you'll need to use the following configuration (or add it to your existing one).

```
providers:
  - name: "infoblox"
    url: "${HOME}/projects/cluster-api-ipam-provider-infoblox/out/ipam-components.yaml"
    type: "IPAMProvider"
```

Make sure the url points to the correct `ipam-components.yaml` which you can download on the relases page. Alternatively you can generate yourself by running `make release`.

You can then install the provider by adding `--ipam infoblox` to a `clusterctl install` command.

```
clusterctl install --ipam infoblox
```

## Configuring Infoblox Instances

Next, an `InfobloxInstance` needs to be configured, which contains connection details and credentials to connect to your Infoblox instance.

The credentials need to be provided as a `Secret`, which is referenced by the `InfobloxInstance`. It needs to contain either `username/password` or `clientCert/clientKey`.

Both the secret needs to be created in the same namespace as the provider (default: `capi-ipam-infoblox-system`). The `InfobloxInstance` is global.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: production-credentials
  namespace: capi-ipam-infoblox-system
stringData:
  username: '<username>'
  password: '<password>'
#or
  clientCert: '<cert>'
  clientKey: '<key>'
```

```yaml
apiVersion: ipam.cluster.x-k8s.io/v1beta1
kind: InfobloxInstance
metadata:
  name: production
spec:
  host: "some.host.com"             # address of the Infoblox server
  port: "443"                       # port of the Infoblox server
  credentialsSecretRef:
    name: production-credentials
  disableTLSVerification: true      # disable TLSVerification
  customCAPath: "/some/path/ca.crt" # path to a file which contians list of custom Certificate Authorities that can be used to verify SSL certifcates if 'disableTLSVerification' is set to 'false'. Host's default authorities will be used if not specified.
  defaultNetworkView: "some-view"   # default network view
  wapiVersion: "2.12"               # Web API Version of the Infoblox server
```

## Usage

To use Infoblox for assigning IP addresses to nodes, create an InfobloxIPPool. It contains a reference to the InfobloxInstance and one or more subnets managed by that instance that should be used to allocate addresses.

```yaml
apiVersion: ipam.cluster.x-k8s.io/v1beta1
kind: InfobloxIPPool
metadata:
  name: example-pool
  namespace: tenant-clusters-bonn
spec:
  instance:
    name: "production"              # name of the InfobloxInstance
  networkView: "datacenter-network" # Infoblox network view that will be used
  subnets:                          # list of the subnets in the network view we want to get IP addresses from
    - cidr: "10.0.0.0/24"           # subnet CIDR
      gateway: "10.0.0.1"           # gateway that should ba assigned to the IP Address claim
```

Now, whenever `IPAddressClaim` that references `example-pool` will be created, a host record will be created in the subnet specified by the pool on the InfobloxInstance `production` to allocate an IP Address.

If multiple subnets are specified, the host record will be created in the first subnet with available IP addresses.

> [!NOTE]
> You can find all the example files described above in [config/samples](./config/samples).

### Creating DNS Entries

Since Infoblox also includes DNS management, host records can also reference a DNS zone to create DNS entries for each host.

In order for these records to be useful, the host record should be named after the hostname of the server it is created for. Unfortunately Cluster API currently offers no common way to set hostnames for machines. While bootstrap providers are likely to provide some way of setting it, there is no way to predict what the hostname will be.

If you need to create DNS records for your machines, you'll therefore be required to follow a convention if you want your hostname to match the DNS record.

We've currently only implemented one strategy for identifying the hostname of a machine, since it's the one we (Deutsche Telekom) are using. In case you have other requirements, we're open to accept contributions for new strategies. Please open an issue if you're interested.

Our strategy uses the name of the CAPI `Machine` as the hostname. To determine the Machine name the provider follows the owner chain from the `IPAddressClaim` via the infrastructure provider resources to the `Machine`. This is used by searching through the owner references up to a depth of five.

To enable setting DNS entries, set the `spec.dnsZone` parameter on the `InfobloxIPPool` to your desired zone. The resulting DNS entries will then be `<machine name>.<dnsZone>`. The DNS view will be set to `default.<dnsZone>`.

## Running Tests

### E2E tests

In order to run end-to-end tests, an Infoblox instance needs to be provided. Configuration is done using environment variables. See [.testenv.example](./.testenv.example) for an example.

To execute e2e test simply setup required enironment variables and run `make test-infoblox`.

### Unit tests

Unit tests can be run using `make test` command.

To execute unit tests [controller-gen](https://book.kubebuilder.io/reference/controller-gen) is required. You can install it using `make controller-gen` command.

> NOTE: you can run both unit tests and e2e tests usin `make test-all`.

## Licensing

Copyright (c) 2024 Deutsche Telekom AG.

Licensed under the **Apache License, Version 2.0** (the "License"); you may not use this file except in compliance with the License.

You may obtain a copy of the License at https://www.apache.org/licenses/LICENSE-2.0.

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the [LICENSE](./LICENSE) for the specific language governing permissions and limitations under the License.

### Dependency Licenses

You can find the licenses of used Go dependencies as a `licenses.tar.gz` archive as part of our [releases](https://github.com/telekom/cluster-api-ipam-provider-infoblox/releases) and in the `/license` directory contained in our container images available at [ghcr.io/telekom/cluster-api-ipam-provider-infoblox](https://ghcr.io/telekom/cluster-api-ipam-provider-infoblox).
