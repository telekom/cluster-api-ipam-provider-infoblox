# Logging reference

This page lists what the Infoblox IPAM provider logs: the binary, the flags that set the level and the format, every message of this module, the fields, and which fields can hold personal data. The message catalogue and the field table are generated from the code by `go run ./hack/logref`; `TestLoggingReferenceUpToDate` in `hack/logref` fails when they differ from the code.

## Binaries

| Binary | Built from | Runs as | Writes to |
|---|---|---|---|
| `manager` | `main.go` | container `manager` of the Deployment `capi-ipam-infoblox-controller-manager` in `capi-ipam-infoblox-system` (`config/default`) | stderr |

The repository builds no other binary. `hack/` and `test/` hold tools and tests only.

## Levels and format

| Flag | Values | Default | Same value as |
|---|---|---|---|
| `--log-level` | `debug`, `info`, `warn`, `error` | `info` | `--zap-log-level` |
| `--log-format` | `text`, `json` | `text` | `--zap-encoder` (`text` is `console`) |

Any other value is rejected at start-up. When a `--log-*` flag and its `--zap-*` flag are both given, the later one on the command line wins. The manifests in `config/` set neither, so a deployment logs at `info` in `text`.

| `--log-level` | zap level | What it adds |
|---|---|---|
| `error` | error | errors only: start-up failures, failed allocations and releases, failed Infoblox calls (as `Reconciler error` of controller-runtime) |
| `warn` | warn | nothing beyond `error`: this module logs no warnings |
| `info` | info | start-up, host record changes, allocated and released addresses, paused claims, pools waiting for claims |
| `debug` | debug (logr verbosity 1) | successful address verification |

`--zap-log-level=<n>` with `n` of 2 and higher also enables the lines of libraries at logr verbosity `n`, for example the admission requests of controller-runtime at 5. These lines are not part of the catalogue below.

Stack traces are off below panic; `--zap-stacktrace-level` changes that. Lines that repeat the same message more than 100 times per second are sampled (controller-runtime production default) unless `--zap-log-level` is 2 or higher or `--zap-devel` is given.

## Line format

`text` (zap console encoder), one line per entry:

```
2026-11-17T17:41:35+01:00	ERROR	setup	max-concurrent-reconciles must be greater than zero	{"value": 0, "error": "invalid argument"}
```

`json`, one object per line:

```json
{"level":"error","ts":"2026-11-17T17:41:35+01:00","logger":"setup","msg":"max-concurrent-reconciles must be greater than zero","value":0,"error":"invalid argument"}
```

| Key | Content |
|---|---|
| `ts` | time, RFC 3339 (`--zap-time-encoding` changes it) |
| `level` | `debug`, `info`, `error` (upper case in text); a logr verbosity `n` above 1 is written as `Level(-n)` (`LEVEL(-n)` in text) |
| `logger` | logger name, for example `setup` for the start-up lines of `main.go`; reconcile lines of this module have none |
| `msg` | the message |
| `stacktrace` | only with `--zap-stacktrace-level` |

All other keys are fields; see [Fields](#fields).

## Lines of libraries

These lines use the same logger, level and format, but their messages are not in the catalogue:

- **controller-runtime**: start-up, leader election, `Reconciler error` with the `error` of a failed reconciliation, webhook server. Reconcile lines carry `controller`, `controllerGroup`, `controllerKind`, `<Kind>`, `namespace`, `name` and `reconcileID`.
- **client-go** (klog, routed into the same logger): API server warnings and client errors, at klog verbosity 0.
- **`sigs.k8s.io/cluster-api-ipam-provider-in-cluster/pkg/ipamutil`** (`ClaimReconciler`): paused clusters and pools, a missing pool, and `IPAddress <namespace>/<name> (<ip>) has been <created|updated>`. That message holds the allocated IP address in its text.

The output of the Infoblox client library (`github.com/infobloxopen/infoblox-go-client`, standard library `log`) is discarded in `main.go`.

## Personal data

The classes and data subjects are those of the T-CaaS logging contract:

| Personal data | Meaning |
|---|---|
| `none` | no personal data |
| `tenant-pii` | names chosen by a tenant user: namespaces and object names, which can contain a person's or a customer's name, and user names |
| `customer-data` | IP addresses, subnets and DNS names |

| Data subject | Meaning |
|---|---|
| `none` | no person |
| `tenant-user` | staff of a tenant with access to a cluster, who owns the machines, claims and pools |

Every field is classified in the [Fields](#fields) table; the classification lives in `fieldClasses` in `hack/logref/main.go`, and the scan fails for a field without one.

- **At the default level** (`info`) lines hold tenant object names and namespaces, machine host names and allocated IP addresses: `tenant-pii` and `customer-data`, data subject `tenant-user`.
- **`debug`** adds no other kind of personal data.
- **Free text**: the `error` field and the ipamutil message `IPAddress ... has been ...` can repeat a host name, an IP address or an Infoblox response: `customer-data`, data subject `tenant-user`.
- **`--zap-log-level=5` and higher** adds the user name of admission requests (`user`): `tenant-pii`, data subject `tenant-user`.
- **Never logged**: the credentials of an InfobloxInstance (the Secret with the WAPI user name and password), and Infoblox request or response bodies outside error text.

## Message catalogue

<!-- BEGIN GENERATED: go run ./hack/logref -->

### Messages

The literal message of each log call of this module, by package. `debug` lines are logged at logr verbosity 1.

#### `internal/controllers`

| Level | Message | Fields |
|---|---|---|
| error | `failed to list IPAddressClaims for InfobloxIPPool` | `name`, `namespace` |
| error | `failed to parse subnet` |  |
| error | `unable to ensure address allocated` |  |
| info | `Claim holds no address, nothing to release` |  |
| info | `IPAddressClaim is paused, skipping reconciliation` | `IPAddressClaim` |
| info | `Successfully released address` |  |
| info | `new address allocated` | `address` |
| info | `waiting for IPAddressClaims referencing this pool to be deleted` | `claims` |
| debug | `address verification successful` | `address` |

#### `main.go`

| Level | Message | Fields |
|---|---|---|
| error | `failed to setup indexes` |  |
| error | `max-concurrent-reconciles must be greater than zero` | `value` |
| error | `problem running manager` |  |
| error | `unable to create controller` | `controller` |
| error | `unable to create webhook` | `webhook` |
| error | `unable to get manager options` |  |
| error | `unable to set up health check` |  |
| error | `unable to set up ready check` |  |
| error | `unable to start manager` |  |
| info | `starting manager` | `version` |

#### `pkg/infoblox`

| Level | Message | Fields |
|---|---|---|
| info | `Creating Infoblox host record` | `hostname` |
| info | `Deleting Infoblox host record` | `hostname` |
| info | `Fetching Infoblox host record` | `hostname` |
| info | `Updating Infoblox host record` | `hostname` |

### Fields

The fields of the lines of this module, and the fields that libraries add to them. "Set by" names where the field comes from: a log call of this module, a `WithValues` call of this module (the field is on every later line of that logger), or a library.

| Field | Set by | Personal data | Data subject | Meaning |
|---|---|---|---|---|
| `IPAddressClaim` | log call, controller-runtime | tenant-pii | tenant-user | name of the IPAddressClaim; from controller-runtime also `{name, namespace}` of the reconciled claim |
| `InfobloxIPPool` | controller-runtime | tenant-pii | tenant-user | `{name, namespace}` of the reconciled or validated InfobloxIPPool |
| `InfobloxInstance` | controller-runtime | none | none | `{name, namespace}` of the reconciled InfobloxInstance, an object of the operator |
| `Pool` | ipamutil | tenant-pii | tenant-user | name of the pool of a paused IPAddressClaim |
| `address` | log call, `WithValues`, ipamutil | customer-data | tenant-user | IP address allocated to a machine of a tenant cluster; from ipamutil the name of the IPAddress object |
| `claims` | log call | none | none | number of IPAddressClaims that still reference a pool |
| `controller` | log call, controller-runtime | none | none | name of a controller |
| `controllerGroup` | controller-runtime | none | none | API group of the reconciled kind |
| `controllerKind` | controller-runtime | none | none | reconciled kind |
| `dnsView` | `WithValues` | none | none | Infoblox DNS view, set by the operator |
| `error` | zapr | customer-data | tenant-user | error text of an Error line; can repeat a host name, an address or an Infoblox response |
| `errorCauses` | zap | customer-data | tenant-user | the errors of an aggregated error, as `error` |
| `errorVerbose` | zap | customer-data | tenant-user | `error` with a stack trace, for errors that format one |
| `hostname` | log call, `WithValues` | customer-data | tenant-user | host name of the Infoblox host record: the Machine name, with the DNS zone of the pool |
| `instance` | `WithValues` | none | none | name of the InfobloxInstance, an object of the operator |
| `name` | log call, controller-runtime | tenant-pii | tenant-user | name of the reconciled object or of an InfobloxIPPool |
| `namespace` | log call, controller-runtime | tenant-pii | tenant-user | namespace of the reconciled object or of an InfobloxIPPool |
| `networkView` | `WithValues` | none | none | Infoblox network view, set by the operator |
| `reconcileID` | controller-runtime | none | none | ID of one reconciliation |
| `requestID` | controller-runtime | none | none | UID of an admission request (`--zap-log-level=5` and higher) |
| `resource` | controller-runtime | none | none | resource of an admission request (`--zap-log-level=5` and higher) |
| `subnet` | `WithValues` | customer-data | tenant-user | subnet of an InfobloxIPPool that addresses are allocated from |
| `user` | controller-runtime | tenant-pii | tenant-user | user name of an admission request (`--zap-log-level=5` and higher) |
| `value` | log call | none | none | invalid value of a flag |
| `version` | log call | none | none | version of the controller |
| `webhook` | log call | none | none | name of a webhook |
| `webhookGroup` | controller-runtime | none | none | API group of the validated kind |
| `webhookKind` | controller-runtime | none | none | validated kind |

<!-- END GENERATED -->

## Keeping this document current

- A change that adds, removes or changes a log call, or adds a field, runs `go run ./hack/logref` and commits the result. `TestLoggingReferenceUpToDate` runs in `make test` (the `test` check) and fails otherwise.
- The scan fails for a log call without a literal message or V level, for a field without an entry in `fieldClasses`, for an entry of this module's code that no log call uses any more, and for a file other than `main.go` that imports the standard library `log` package.
- The prose sections above are not generated. A change of a flag, a default or a library that logs updates them in the same pull request.
- The scan matches calls whose receiver expression contains `log` (`logger`, `setupLog`, `klog`, `log.FromContext(ctx)`) and the `Info` and `Error` methods. It does not see a logger held in a variable with another name (`l := logger`), klog's `InfoS` and `ErrorS`, or a direct write with `fmt`; a change that introduces one adds its messages to the catalogue by hand and extends the scan.
