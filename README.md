# Cluster API IPAM Provider Infoblox

This is an IPAM provider for Cluster API that integrates with Infoblox NIOS for IP address management and DNS.
It allows to allocate addresses from subnets configured in Infoblox and allows to add DNS entries for those allocated addresses as well.

## Deploying

This provider can be installed using `clusterctl install`. Since it's not yet added to the integrated list of providers, you'll need to use the following configuration (or add it to your existing one).

```
providers:
  - name: "infoblox"
    url: "${HOME}/projects/cluster-api-ipam-provider-infoblox/out/ipam-infoblox/v<release-version>/ipam-components.yaml"
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
  defaultDNSView: "some-dns-view"   # default DNS view
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
  dnsView: "some-dns-view"          # DNS view for this pool (optional)
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

To enable setting DNS entries, set the `spec.dnsZone` parameter on the `InfobloxIPPool` to your desired zone. The resulting DNS entries will then be `<machine name>.<dnsZone>`. 

The DNS view is determined in the following priority order:
1. **Pool.spec.dnsView** - if explicitly set on the pool
2. **Instance.spec.defaultDNSView** - if not set on pool but set on the instance  
3. **Derived from networkView** - if neither is set, follows the pattern:
   - If `networkView` is `"default"` or empty → DNS view is `"default"`
   - Otherwise → DNS view is `"default.<networkView>"` (e.g., `networkView: "production"` → DNS view `"default.production"`)

The DNS view is only used, and only checked for existence, if the pool has a `dnsZone`. Without one, host records are created with DNS disabled.

## Running Tests

| Command | Runs | Requirements |
|---|---|---|
| `make test` | Unit tests and envtest based controller and webhook tests | None, `controller-gen` and the envtest binaries are downloaded by the Makefile |
| `make test-infoblox` | Integration tests in `test/integration/infoblox` against a live Infoblox instance | A configured Infoblox instance, see below |
| `make test-all` | Both of the above | Both of the above |

Extra `go test` flags can be passed with `TEST_ARGS`. The default is `-race -shuffle=on`, so a failing order can be replayed with `make test TEST_ARGS="-shuffle=<seed>"`.

### Tests against a live Infoblox instance

The integration tests in [test/integration/infoblox](./test/integration/infoblox) create and delete network views, networks and host records on a real Infoblox instance.
They only use the public API of `pkg/infoblox` and are guarded by the `//go:build infoblox` build tag, so they are compiled only when the tag is set, which `make test-infoblox` and `make test-all` do.
The instance is configured with environment variables, see [.testenv.example](./.testenv.example).

### Testing guidelines

- **Every test is independent.** A test passes on its own, in the full suite and in any order. It never relies on objects, state or allocations another test left behind.
- **Unit tests** use the standard `testing` package with Gomega (`NewWithT`), preferably table driven. They build their own mocks and clients per test and share no mutable package-level state.
- **Controller tests** use Ginkgo with envtest and call `Reconcile` directly. The suite only provides the API server and clients, each spec builds the reconciler and mocks it needs.
  - Each spec works in its own namespace (`createNamespace()`), cluster-scoped objects get names unique to the spec.
  - Each spec removes what it created with `DeferCleanup`.
- **Prefer the direct client** (`client.New`) over a cache-backed client. It is read-your-writes consistent, so plain `Expect` assertions work without polling.
  Use a cache-backed client only where the code under test needs it, e.g. to list objects through a field index.
- **`Eventually`/`Consistently` are for asynchronous behaviour only**, such as waiting for an informer cache or a running manager. They must not hide ordering problems or be used as a retry loop.
- **Shared state between specs is an explicit design decision.** A scenario whose steps build on each other uses an `Ordered` container and a comment explaining why. A normal test never depends on another test implicitly.
- **Mocks are generated** with the `mockgen` version pinned in `go.mod`, from the `//go:generate` directives, by `make generate`. Do not edit them by hand, CI fails if they are out of date.
  `pkg/infoblox/ibmock` mocks the `infoblox.Client` for its consumers, `pkg/infoblox/ibclientmock` mocks the Infoblox API the client itself uses.

## Licensing

Copyright (c) 2024 Deutsche Telekom AG.

Licensed under the **Apache License, Version 2.0** (the "License"); you may not use this file except in compliance with the License.

You may obtain a copy of the License at https://www.apache.org/licenses/LICENSE-2.0.

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the [LICENSE](./LICENSE) for the specific language governing permissions and limitations under the License.

### Dependency Licenses

You can find the licenses of used Go dependencies as a `licenses.tar.gz` archive as part of our [releases](https://github.com/telekom/cluster-api-ipam-provider-infoblox/releases) and in the `/license` directory contained in our container images available at [ghcr.io/telekom/cluster-api-ipam-provider-infoblox](https://ghcr.io/telekom/cluster-api-ipam-provider-infoblox).
