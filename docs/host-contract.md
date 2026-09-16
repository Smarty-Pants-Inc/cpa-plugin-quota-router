# Negotiated filter consumer contract

This consumer is based on frozen legacy plugin
`8c6efa015ecd75e9225cc99c7ff82626a2905b4d`, tree
`9e97c097bb2c62e529a8410b224d267928450742`. The legacy candidate and test-only
`8e531375c8efe54fb4de1ddf507c79a753a45c23` remain separate evidence.

Its required host source is frozen private candidate
`a2020b618173204312d11610000e76b2444260d9`, tree
`a2422722564bfa47f0c917747df382dbde25107a`, based on maintained upstream
`router-for-me/CLIProxyAPI` `8335eac731946bd4eff18f500653f93736df53d6`, tree
`13942c68d7859e102308cf683e9aa820b7c39018`. This does not modify or qualify an
installed gateway. The historical 4b5/1e1e diagnostic and April fork remain distinct.

## Wire contract

C ABI 1 and JSON schema 6 are unchanged. In `plugin.register` and
`plugin.reconfigure`, the host sends `host_features:["scheduler_filter_v1"]`
with the existing base64 `config_yaml`. Absent or unknown features yield an
`unsupported_host` envelope before config application or worker startup. If an
existing instance loses the feature, it first stops admission and joins its worker,
then reports refusal. It never starts a legacy picker as fallback.

Registration advertises `scheduler_filter_v1:true` and `management_api:true`,
not `scheduler:true`. `scheduler.pick` is no longer implemented. `scheduler.filter`
is refused until successful explicit negotiation. It receives:

```json
{"model":"configured-model","candidates":[{"id":"auth-id","provider":"claude"}]}
```

It returns the existing OK envelope containing exactly:

```json
{"excluded_ids":["auth-id"]}
```

IDs must be nonempty, whitespace-exact and unique, with a nonempty provider.
Invalid offers return `invalid_candidates` without queuing work. Responses are
bounded by the offered count, duplicate-free, and cannot contain an unoffered ID.
Only offered Claude candidates for exact configured models can be excluded.
Case-insensitive model matching and outer whitespace handling are unchanged;
suffixes/aliases are not canonicalized. Empty exclusions mean no known quota block,
not permission to add candidates. All-blocked returns all exclusions; only the host
turns exhaustion into its routing error. No priority, account selection, body,
headers, tokens, paths or arbitrary auth metadata enters this wire request.

## Native ownership and worker reuse

The paired host offers all priority tiers after native admission rules and applies
exclusions before native selection. Its existing scheduler owns highest-tier
choice, weights, round-robin, fill-first and affinity. Its attempt-local allowed-ID
scope prevents builtin delegation/resync/retry from resurrecting excluded or
unoffered candidates. Filter precedence is separate from legacy scheduler priority.
Home does not advertise support and refuses active filter enforcement.

This consumer makes only memory decisions in the callback. Due discovery and
usage refresh reuse the existing single worker, queue coalescing, independent
metadata throttle, credential revision guards, fail-open quota policy and reset
handling. Since selection now follows filtering, due usage refresh covers offered
eligible credentials rather than a plugin-selected credential. That is the only
necessary admission change; there is still no idle timer or provider sweep of
unchanged unoffered accounts. All-blocked requests still trigger bounded metadata
discovery. Unsupported IDs cannot erase the discovery throttle.

`plugin.quiesce` cancels and physically joins the worker, retains cache/configuration,
and permits cached filtering while the host withdraws the old record. It never
resumes automatically. A later supported reconfigure resumes through the existing
worker lifecycle. Direct disabled plugin configuration joins and clears cache.
Host-level disable instead withdraws records and quiesces the retained instance.
The host owns cancellation receipts, lifecycle-call serialization, replacement
admission and callback-table/library custody. A C callback has no cancellation
context: a stuck callback keeps physical settlement pending. No timeout or detached
worker is treated as joined, and unsupported-feature refusal during such a join
can remain pending too.

## Evidence boundaries

Real consumer RPC-to-worker tests cover negotiation before startup, offered-only
exclusions, exact models, mixed providers, replacement discovery, and retained
lifecycle ownership through quiesce/disable/feature loss. The C-shared harness adds
native absent-feature refusal with zero host callbacks, supported reconfiguration
and filter-wire checks. These tests are source inputs until actually executed.

The isolated process harness accepts all four explicit source combinations. New/new
also gives the more-used blocked account higher native priority and a distinct
loopback proxy, to detect selection before exclusions or retry resurrection. It
checks disable and explicit re-enable. Rejected old-host/new-consumer checks actual
host refusal, no plugin status route and no provider usage. The host packet's
manager/lifecycle tests remain required for the broader affinity, pinning,
replacement failure and cancelled callback cases.

Those fixtures are not complete packaged-release qualification. In particular,
process tests override the usage endpoint for loopback fixtures and do not test
signed/distributed release bytes. Exact packaged-library receiving, real native
replacement/shutdown/callback-custody coverage, independent review and explicit
activation remain gates. See [release qualification](release-qualification.md).
