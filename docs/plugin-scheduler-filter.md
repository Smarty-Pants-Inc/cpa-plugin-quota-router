# Optional scheduler filter v1

This source adds the optional `scheduler_filter_v1` feature. Native C ABI 1 and
JSON schema 6 remain unchanged. A host does not infer support from a filename,
version label, unknown JSON field or successful library load.

## Negotiation

In `plugin.register` and `plugin.reconfigure`, a supporting host sends:

```json
{"schema_version":6,"config_yaml":"...","host_features":["scheduler_filter_v1"]}
```

`config_yaml` retains the existing base64-encoded byte format. Home mode omits the
feature. A filter-only plugin must inspect the feature before starting its worker
or making callbacks, and return an `unsupported_host` error if it is absent.
Missing/zero old-host fields are not support. The plugin advertises:

```json
{"capabilities":{"scheduler_filter_v1":true,"scheduler":false}}
```

The host rejects an advertised filter if it did not offer the feature. Existing
plugins ignore the additive request field and retain the old scheduler protocol.
The frozen quota-router plugin using `scheduler.pick` is still a legacy plugin;
it does not become a filter merely by running on this host.

## Request and result

`scheduler.filter` receives only the exact route-model string and admitted
candidate ID/provider pairs:

```json
{"model":"configured-model","candidates":[{"id":"host-auth-id","provider":"claude"}]}
```

It returns the existing OK envelope with:

```json
{"excluded_ids":["host-auth-id"]}
```

The host checks response count, membership and uniqueness against the original
offer. Empty, whitespace-modified, duplicate and foreign IDs are errors. The
plugin cannot add or select an account. No credential path, token, headers,
request body or arbitrary account metadata crosses this callback. The callback
uses a copied offer so a native SDK adapter cannot rewrite the validation set.

The auth manager first applies its native request/security/pinning/tried/model/
executor/disabled/cooldown rules across priority tiers. It invokes the filter
outside its lock, then supplies the remaining highest tier to legacy schedulers
and ordinary native selectors. Native affinity keeps its existing across-tier
binding validation. Native round-robin/weighted/fill-first implementations remain
unchanged.

An attempt-local allowed-ID context follows builtin delegation and native
scheduler resync/retry. Delegation cannot recover an excluded or previously
unoffered account. Each new selection attempt obtains a fresh filter decision;
the context does not mutate the auth store or leak into another request. A
pinned excluded account does not permit a different account. No remaining
candidate returns `quota_router_exhausted` (503), not unhandled fallback.

Filter errors, malformed responses, panic, fused state and unsettled required
filter lifecycle fail the current selection. Unknown quota information remains
the plugin's best-effort fail-open policy: it returns no exclusion for that
candidate, rather than bypassing a failed filter invocation. Multiple active
filters are explicitly unsupported; the host does not silently select one by
priority. A higher-priority legacy scheduler cannot shadow the filter. Active
Home dispatch refuses filter enforcement even after a runtime mode change.

## Lifecycle ownership

The feature includes required `plugin.quiesce` support. Disable/removal withdraws
active capabilities, then asks each retained loaded library to quiesce. Replacement
withdraws the old record and does not open a new library until quiesce succeeds.
A desired filter remains a routing requirement while its active record is
withdrawn: uncertainty is not permission to route without it.

A `pluginQuiesce` receipt owns one actual call per loaded runtime. A cancelled
configuration caller can stop waiting; it does not erase or duplicate that call.
Later configuration cannot reconfigure the old instance until the receipt has
settled successfully. Failure/panic of required quiesce leaves the library
retained and inactive; final shutdown is still available. There is no timeout
that frees the callback table while a worker might use it.

The guarded native client serializes register/reconfigure/quiesce until each
actual native call returns, not until its caller stops waiting. Shutdown retains
its existing active-call join before native shutdown, callback-table free and
library close. If replacement registration/cleanup is cancelled, the load token
remains until actual cleanup. There is no automatic late rollback into a possibly
obsolete configuration. A later explicit configuration can resume the old library
once cleanup and quiesce have settled. A completed failed replacement still rolls
back only after cleanup and a successful old-library reconfigure; stale metadata
is not a resume receipt.

Compatibility limit: legacy plugins that report quiesce as unsupported retain
the old optional-method replacement behavior. This does **not** prove worker
quiescence for those plugins. Negotiated filters cannot use that fallback.
The C callback ABI has no cancellation context, so physical completion can remain
pending indefinitely for a stuck callback. Caller cancellation is bounded;
physical cleanup is honestly pending and retains exclusion/custody.

## Qualification

Focused SDK, manager, RPC and lifecycle tests are source inputs. They use fake
plugins/providers and do not qualify a C-shared artifact. Required release checks
must bind exact host and plugin commits, Go/C toolchains, native OS/architecture,
packaged library/gateway hashes and actual native load/callback/lifecycle results.
Old-host/old-plugin, new-host/old-plugin and new-host/new-filter pairs have different
claims. Refused new-filter/old-host negotiation needs a real filter consumer test,
not only the synthetic host fixture. No platform or installed runtime is qualified
by this source document.
