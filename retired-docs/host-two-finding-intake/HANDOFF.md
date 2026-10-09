# Quota host — finite F1/F2 repair successor

Owner: same quota-foundation author/session, Astra medium unchanged. Integration
and review routing: dev-lead. No new agent, runtime service, CI dispatch, native
execution, push, activation or installed-gateway/config/account operation.
Envelope9479 and the CI transition remain independent; this packet does not gate
them. Return this exact affected delta to the SAME publication-operands reviewer
for F1/F2 closure, not a full paired review replay.

## Review and exact source custody

Full `quota-pair-review-1/HANDOFF.md` read and SHA256 independently verified:
`bc89f90390ad4f054f61b2a7e1430570185cead60eb5135ccaa1375459e4a06a`.
Verdict was findings, not ACCEPT-SOURCE. This repair is not self-acceptance.

Isolated successor: `.local/host-two-finding-repair/` under the same owned worktree.
It is a private detached source worktree, not another visible worker lane or an
installed/product checkout.

- Host successor commit: `3fcfa803a4aa2815d69c9b606fadf7ab179ff3c1`.
- Host successor tree: `266bd5bc6dc25911a8607b2b8e1be2f8a87991c2`.
- Reviewed parent: `a2020b618173204312d11610000e76b2444260d9`, tree
  `a2422722564bfa47f0c917747df382dbde25107a`.
- Maintained upstream base: `8335eac731946bd4eff18f500653f93736df53d6`, tree
  `13942c68d7859e102308cf683e9aa820b7c39018`. Its private snapshot is
  `4ec298738d9a710a9d2fd305d359d721634750a3`, not the upstream commit identity.
- `host-source.tar` SHA256:
  `2e0085d306aa106602f32959f2996187d4ca164a4a429c7743a47173e36f5a05`.
- `affected.patch` (reviewed a202 → successor) SHA256:
  `c38fdb42c85365d97142b02cc02614554eaf104877602218e611b187deadfac8`.
- `host-full.patch` (exact maintained8335 tree → successor) SHA256:
  `b97c8e574d4b1aadb2f8f67ec78b09d92926ab0bc69f871b12c4bbfc47c31f94`.

`SOURCE-IDENTITY.json` records all affected files, dependency digests and rechecked
frozen archives. Host a202 and consumer ea161 archive/patch bytes are unchanged;
legacy8c6 and red8e531 archives are also unchanged. Consumer source is untouched.
No source/dependency substitution for old8335 or the historical4b5/1e1e diagnostics.

## F1 — retained per-ID physical cleanup ownership

Production change: `internal/pluginhost/host.go` only.

`retainUnloadTargetsLocked` attaches every detached loaded/retired target to the
existing per-ID `pluginLoadRequest` exclusion before detaching the maps. When no
load is pending, the receipt starts with its load component already settled. When
cancelled initialization/replacement cleanup already owns the ID, detached clients
extend that exact receipt rather than replacing it.

The receipt tracks load settlement and the count of detached clients still owned.
`clearLoadingRequest` records actual load-cleanup settlement but cannot delete a
receipt with detached clients remaining. Each detached shutdown waits with a
background context through the guarded client's actual call join/native shutdown;
only its physical return releases that target's count. The last owner can remove
the per-ID exclusion only after the load component also settles. No cancellation
or map removal stands in for physical completion. Unload logging now follows
physical completion too.

Both `UnloadPluginContext` and `ShutdownAllContext` detach capabilities and bound
the caller's wait by its context. Cleanup continues with retained ID exclusion;
independent targets can settle independently. Repeated apply cannot open/reinit
that ID until every relevant owner settles. Existing initialization/replacement
cleanup, quiesce and explicit old-instance resume paths are not replaced by a new
service or registry.

Regressions in `host_test.go`:

- `TestHostContextualTeardownRetainsReloadExclusion`, both contextual APIs:
  held registered native call; repeated same-ID apply with exactly one open;
  separately held native shutdown; repeated apply again; detached retired client
  held beyond loaded-client settlement; busy/exclusion retained until both settle;
  exactly one old cleanup per client; fresh admission only afterward.
  The old assertion that busy should disappear while a call is held was replaced
  with these causal checks, not simply removed.
- `TestHostDetachedCleanupSharesPendingLoadToken`, both settlement orders:
  focused receipt fixture verifies that load cleanup cannot erase a detached
  client's ownership and detached shutdown cannot erase a pending load token.
- The existing blocked-call loader/client fixtures gained open counting, a fresh
  replacement client and a separate shutdown barrier. Other users retain their
  previous default behavior. Existing failed-init/cancelled-replacement/resume
  tests remain in the required full affected-package check.

These are Go fixture source assertions. Actual Unix same-library stored callback
identity and native packaged replacement/shutdown proof remain separate gates.

## F2 — explicit wire decision, not a missing-result permission

Production change: `internal/pluginhost/scheduler_filter.go` only.

The filter adapter alone receives the generic RPC result as `json.RawMessage` and
requires a result object with a present, non-null `excluded_ids` array. Missing
result, null result, a missing field and a null list fail. An explicit empty `[]`
is valid. This strict null-list choice is documented in
`docs/plugin-scheduler-filter.md`. The generic legacy RPC decoder and typed SDK
validator remain unchanged; in-process typed filters can still use nil as their
empty slice. The frozen consumer already emits `[]`, so it needs no correction.

Regressions:

- Expanded `TestHostSchedulerFilterRPCMalformedResponseIsNotUnhandled` includes
  missing/null result, missing/wrong field, null list, non-object result, numeric
  IDs, an error envelope and invalid JSON. It goes through the RPC adapter and
  host filter boundary and requires an error, never routing permission.
- `TestHostSchedulerFilterRPCExplicitEmptyArray` retains successful empty-array
  behavior through that same boundary.
- `TestSchedulerFilterErrorCannotReachSelection` uses the existing manager and
  tracking-selector fixtures for single and mixed providers; an error must reach
  neither native selection nor the legacy scheduler, and return no auth.

No native priority/affinity/pinning/retry/delegation policy or consumer wire code
changed. The RPC and manager checks are composed source regressions, not a claim
of an executed native malformed-peer pair test.

## Checks and finite execution commands — NOT dispatched

Observed: review/frozen-input hashes match; source diff whitespace check PASS;
successor source is committed and clean. Go/gofmt are unavailable on this Dev1
PATH. **Formatting, compilation, tests, race, vet, four process pairs, native
callback identity and packaged/platform receiving are all UNRUN.** No source
expectation below is an observed pass.

Existing CI owner retains admission, offline dependencies/toolchain, isolation and
execution. On an admitted exact-source native target, first return formatting delta:

```sh
gofmt -d internal/pluginhost/host.go internal/pluginhost/host_test.go internal/pluginhost/scheduler_filter.go internal/pluginhost/scheduler_filter_test.go sdk/cliproxy/auth/scheduler_filter_test.go
```

Focused regression command (existing prepared Go/C closure, no module mutation):

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOWORK=off CGO_ENABLED=1 GOFLAGS=-mod=readonly \
  go test -count=1 -timeout=90s ./internal/pluginhost ./sdk/cliproxy/auth \
  -run '^(TestHostContextualTeardownRetainsReloadExclusion|TestHostDetachedCleanupSharesPendingLoadToken|TestHostSchedulerFilterRPCMalformedResponseIsNotUnhandled|TestHostSchedulerFilterRPCExplicitEmptyArray|TestSchedulerFilterErrorCannotReachSelection)$'
```

Then retain the existing full affected-package unit/race/vet/server checks from the
host packet. They include failed initialization/replacement, cleanup and explicit
resume cases; focused new checks do not replace them. Any formatting fix changes
source bytes and requires its own successor receipt before acceptance.

## Same reviewer closure and retained pair gates

Next: Dev supplies this archive plus `affected.patch` and this handoff to the same
publication-operands reviewer. Review the six affected files and relevant callers
for F1/F2 closure; do not replay the completed full pair review without a new reason.
This source owner remains available for findings and actual CI failures.

The consumer packet's four explicit process commands remain required and UNRUN:
old-old (old8335/old8c6), new-old (THIS host/old8c6), new-new (THIS host/ea161), and
old-new-refused (old8335/ea161). For successor execution, extract THIS `host-source.tar`
into a fresh `host-new` source directory; retain the exact old-host/old-plugin and
unchanged consumer operands. Preserve original a202 evidence; do not overwrite or
relabel its archive. Pair labels alone are not source verification.

Native platform/toolchain identity, actual same-library callback-table custody,
all required lifecycle/failure paths, exact packaged artifact receiving, immutable
publication, retained qualified rollback pair and explicit activation remain gates.
No CI, source acceptance or runtime support is inferred from this repair packet.
