# Quota host implementation — finite source packet

Wave: `cpa-plugin-quota-router/foundation/host-8335`.
Owner: quota-foundation, same retained visible author and linked plugin worktree.
Dev-lead integrates/publishes; ci-delivery owns admission/execution. No new source
writer, worker session, host service, remote execution or live mutation.

## Exact custody

Source: `../host-foundation-8335/` (private nested Git/index, not the plugin index).
Maintained upstream base `router-for-me/CLIProxyAPI`
`8335eac731946bd4eff18f500653f93736df53d6`.
Independent `git write-tree` of the supplied archive exactly matched
`13942c68d7859e102308cf683e9aa820b7c39018` before edits.

- Private base snapshot commit: `4ec298738d9a710a9d2fd305d359d721634750a3`.
  This is a local snapshot commit, **not** an upstream commit ID.
- Candidate private commit: `a2020b618173204312d11610000e76b2444260d9`.
- Candidate tree: `a2422722564bfa47f0c917747df382dbde25107a`.
- `host-source.tar` SHA256:
  `ad9f8210a5c632d85a09cc53517dbfa1177a7c359d9af8b8cd20124d73317b99`.
- `host.patch` SHA256:
  `ce186dc967cf500dc16098b693c1c39206eeb5c49cac399d1d160a08fda7abba`.
- Complete file/lock/source custody: `SOURCE-IDENTITY.json`.

The plugin's actual index/refs remain at candidate `8c6efa015ecd75e9225cc99c7ff82626a2905b4d`
and test-only `8e531375c8efe54fb4de1ddf507c79a753a45c23`; archive digests were
rechecked unchanged. No changes to another host checkout, April fork, historical
4b5/1e1e diagnostic, installed gateway, plugin files, credentials or configuration.
The separately installed 7.3.4/8335 state is owner-reported context, not a live
observation or native qualification by this author.

## Implemented contract and caller map

1. `sdk/pluginapi/types.go`: explicit `scheduler_filter_v1`, minimal JSON request
   `{model,candidates:[{id,provider}]}`, result `{excluded_ids}`, optional
   `SchedulerFilter`, and shared bounded-count/unique/exact-membership validation.
   No token/path/body/header/arbitrary-auth-metadata fields.
2. `sdk/pluginabi/types.go`, `internal/pluginhost/rpc_schema.go`, `rpc_client.go`:
   `scheduler.filter`, separate response capability, additive `host_features`
   in register/reconfigure. Feature omitted in Home mode; a filter response without
   an offered feature is rejected. Old scheduler registration is unchanged.
3. `internal/pluginhost/scheduler_filter.go`: mandatory filter invocation is
   independent of scheduler priority; malformed/error/panic/fused/unavailable
   filters do not become an unhandled native fallback. Multiple filters are
   explicitly refused. A configured loaded filter with unsettled lifecycle stays
   a routing requirement even while its active record is withdrawn.
4. `sdk/cliproxy/auth/conductor_selection.go` and `scheduler_filter.go`: both
   single/mixed legacy selection paths reuse across-priority host availability;
   filter state lookup and callback occur outside the manager lock. Exclusions
   precede highest-tier narrowing; native affinity retains its across-tier list.
   An attempt-local allowed-ID context enters the existing native eligibility
   predicate, so builtin delegation/resync/retry cannot resurrect excluded or
   unoffered IDs. It does not change the auth store or implement a second scheduler.
5. `conductor_home.go`: the common Home dispatch-selection seam explicitly refuses
   active filters, including a runtime mode switch after registration.
6. `internal/pluginhost/quiesce.go`, `host.go`: disable/removal withdraws records
   then quiesces; replacement does not open its new library after failed/uncertain
   quiesce. One receipt owns the actual call after a configuration caller cancels.
   No reconfigure until settled success. No stale metadata fallback if rollback
   reconfigure fails. Cancelled replacement retains its cleanup token and needs a
   later explicit resume after actual cleanup; late completion does not auto-resume
   an obsolete configuration.
7. `client_guard.go`: register/reconfigure/quiesce are serialized through actual
   native completion, not caller cancellation. The existing guarded shutdown still
   joins active calls before native shutdown/free/close.

Legacy compatibility is explicit: old plugins reporting quiesce unsupported keep
the previous optional-method replacement behavior. Required negotiated filters
cannot use that fallback. A stuck C host callback can keep physical cleanup pending;
caller cancellation is bounded, but there is no timeout that declares a worker
joined or frees its callback table. Quiesce failures retain inactive library custody
until explicit shutdown; they do not permit unsafe replacement.

## Tests added or adapted (source, not execution)

Seventeen new test functions, with table subcases, cover:

- Valid/empty/all, foreign/empty/whitespace/duplicate/excess exclusions.
- High blocked/low eligible, native round-robin and fill-first delegation,
  mixed-provider selection, pinning, tried IDs, disabled/cooling candidates.
- Native affinity across high-tier recovery and blocked binding; attempt scope
  across native resync and a new request; exact model/suffix preservation.
- Callback outside manager lock; copied offer cannot alter validation authority.
- Filter vs scheduler precedence; multiple/fused filters; malformed JSON/RPC
  errors; absent feature/Home refusal before a **synthetic** consumer starts.
- Global/instance disable, repeat disable, explicit re-enable, failed/unsupported/
  panicked required quiesce, cancelled blocked native callback, retained custody,
  exactly one quiesce and explicit settled resume.
- Native lifecycle serialization after caller cancellation.

Two existing cancellation/rollback tests now require explicit resume only after
replacement cleanup; the old unsupported-quiesce compatibility test remains.
Existing successful quiesce/replacement, failed-replacement rollback, unload and
callback guard tests remain in the requested affected package scope.

## Observed checks versus UNRUN

Observed PASS: exact base tree match; unchanged frozen plugin archive hashes;
`git diff --check` before candidate commit; private host tracked state clean.

**UNRUN:** gofmt, Go compilation/tests/race/vet, server build, native load/ABI,
real packaged host/plugin pair. Go/gofmt are absent on current Dev1 PATH. No build
or substitute test was attempted here. Formatting is a real pending check; return
a native gofmt delta for this source owner to apply, rather than labeling it passed.
No CI admission/execution receipt has yet been received for this packet.

## Finite CI request

Use only an existing admitted route with candidate isolation and exact Go/native
closure. No new paid use, new CI service, persistent trusted-main admission or
interruption of the historical gateway baseline. Source edits proceed independently
of that baseline and policy CI. Use fresh private runtime/temp state, no provider/
vault/developer credentials, and the exact unchanged go.mod/go.sum in this archive.

First run native formatting diagnostics and return any delta:

```sh
gofmt -d internal/pluginhost/{client_guard,host,host_test,rpc_client,rpc_schema,quiesce,quiesce_test,scheduler_filter,scheduler_filter_test}.go sdk/cliproxy/auth/{conductor,conductor_home,conductor_selection,scheduler_filter,scheduler_filter_test}.go sdk/pluginabi/types.go sdk/pluginapi/{types,scheduler_filter_test}.go
```

Focused behavior command, exact source above (format changes require a new source
receipt; do not relabel those bytes as this frozen tree):

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOWORK=off CGO_ENABLED=1 GOFLAGS=-mod=readonly \
  go test -count=1 -timeout=90s ./sdk/pluginapi ./sdk/cliproxy/auth ./internal/pluginhost \
  -run 'Test(ValidateSchedulerExclusions|SchedulerFilter|HostSchedulerFilter|HostFilter|GuardedLifecycle|HostApplyConfigQuiesces|HostApplyConfigRollsBack|HostApplyConfigRequiresExplicitResume|HostApplyConfigShutsDownSuccessfulReplacement|HostApplyConfigFallsBackWhenQuiesceUnsupported|HostCallQuiesce|HostApplyConfigSerializesLifecycle)'
```

Affected package full/race/vet follow the focused result, plus the upstream-required
server compile. These are CI instructions, not Dev1 execution authority:

```sh
go test -count=1 -timeout=180s ./sdk/pluginapi ./sdk/pluginabi ./sdk/cliproxy/auth ./internal/pluginhost
go test -race -count=1 -timeout=180s ./sdk/pluginapi ./sdk/pluginabi ./sdk/cliproxy/auth ./internal/pluginhost
go vet ./sdk/pluginapi ./sdk/pluginabi ./sdk/cliproxy/auth ./internal/pluginhost
go build -o "$PRIVATE_OUTPUT/cli-proxy-api" ./cmd/server
```

Retain native OS/architecture, Go/C identity, source digest, command/exit/full output,
resource and cleanup receipt. A failed/skipped/unrun check is not a pass. Use the
same offline/mod-readonly environment and existing admitted resource limits.

## Integration and exact-pair blockers

This is a bounded **host-source implementation packet**, not a new plugin release.
The frozen plugin still registers `scheduler.pick`, so it does not yet consume
`scheduler.filter`. The host's synthetic negotiating fixture proves only the test
contract when executed. It is not an actual filter-only plugin startup or native
pair receipt. The next coupled plugin packet must implement the agreed consumer,
refuse absent features before worker startup, and qualify its real request/worker
behavior. Do not quietly relabel plugin8c6 as that consumer.

Dev-lead integrates the exact host patch on maintained8335 and owns any further
coupled consumer assignment/review. Required release acceptance includes real
old/old, new-host/old-plugin, new/new and refused new-plugin/old-host combinations;
real disable/quiesce/reconfigure/replace/failure/shutdown and callback lifetime;
exact packaged bytes, native platform/toolchain/runtime closure, immutable asset
receiving and retained prior qualified pair. Source tests do not qualify Linux
amd64 live use or any other native platform.

Finite host source scope is now frozen. Await actual CI/formatting/review results
or the next concrete coupled consumer authorization; remain available. No push,
PR, service operation, live RPC experiment or activation was performed.
