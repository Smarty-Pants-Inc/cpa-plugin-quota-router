# Real negotiated quota-filter consumer — frozen source packet

Owner: quota-foundation, same visible author/worktree, Astra medium unchanged.
Authority: `QUOTA-FILTER-CONSUMER-ASSIGNMENT.md` in Smarty Dev's
`.local/astra-architecture-20260916/dev-foundation-intake/`.
Dev-lead owns review/composition/publication and any future activation.
**No CI dispatch, live RPC, runtime/config/account mutation, push or new lane.**

## Exact custody

Source directory: `.local/filter-consumer-v1/` beneath the owned quota-foundation
worktree. It has its own private source index, not the plugin or host index.

- Base plugin: `8c6efa015ecd75e9225cc99c7ff82626a2905b4d`, tree
  `9e97c097bb2c62e529a8410b224d267928450742` (independently matched before edits).
- Private base snapshot commit: `24407efc30f292ad945ff2f85d113a065e2bf7f8`.
  This is not an upstream/plugin repository commit.
- Candidate private commit: `ea1611e80c43029aa59b37e3ec3a94ef43e852c8`.
- Candidate tree: `bb8bc7eff7a7bfbf968318e8fe012f100843c675`.
- `consumer-source.tar` SHA256:
  `01431fbdbd6727d5e4617de7fc7f397ff12c892f331d2424ea4c08bca3682222`.
- `consumer.patch` SHA256:
  `352fbf86b10013b5316b8dedb498d75351b8cbc054b3419c723122015e51ddad`.
- Full hashes, pins, file list and frozen-input custody: `SOURCE-IDENTITY.json`.

The host packet remains exactly `a2020b618173204312d11610000e76b2444260d9`, tree
`a2422722564bfa47f0c917747df382dbde25107a`, archive SHA256
`ad9f8210a5c632d85a09cc53517dbfa1177a7c359d9af8b8cd20124d73317b99` and patch SHA256
`ce186dc967cf500dc16098b693c1c39206eeb5c49cac399d1d160a08fda7abba`.
Frozen plugin8c6/8e531 archives and maintained8335 upstream archive were rechecked
unchanged. No historical 4b5/1e1e diagnostic or April fork edits.

Go directive remains `1.26.0`; CGO is required. SDK pin remains
`github.com/router-for-me/CLIProxyAPI/v7 v7.2.100`. No dependency changes/replaces.
Consumer go.mod SHA256 `d1859fbfc4842ec3aa2afd2dea6b0713a4c8969401f80cb7d78636a2ea9d06e5`;
go.sum SHA256 `7d042b83fcdd79be5c0a573f6e5078517d828ce03d60e8294293b29d618f7a91`.
Default metadata is `0.5.0-filter-v1-dev`, explicitly unreleased, not a new tag.

## Implemented behavior

- `abi.go` checks explicit `host_features` before applying configuration or starting
  any worker. Empty/null/missing/unsupported features return `unsupported_host`.
  Feature loss on an existing instance stops filter admission, joins existing work,
  then refuses. A stuck callback can therefore keep refusal pending; no fake join.
- `config.go` advertises only `scheduler_filter_v1` and `management_api`.
  `scheduler.pick` is removed. Additive local wire types avoid changing unrelated
  SDK pins. Native ABI 1 / schema 6 remain unchanged.
- `scheduler.go` validates the complete offered set first, then returns only
  `excluded_ids`. IDs are nonempty, exact and unique; a provider is required.
  Results are bounded by offered count and cannot include foreign IDs. Invalid
  offers fail before queueing. Unknown/reset-expired quota fails open, but protocol
  errors are not successful empty responses. Mixed providers retain non-Claude IDs.
- No picker, lexical tie-breaker, priority comparison, affinity, distribution,
  pinning, retry or delegation logic remains in the plugin. All-blocked reports all
  exclusions; the host owns exhaustion. The frozen host retains those native paths.
- The existing single worker/discovery throttle/revision/cache/reset/quiesce code
  is reused. Filtering now refreshes due offered eligible credentials, since only
  the host knows which will be selected afterward. This necessary trigger change
  is not an idle poll or unchanged-unoffered provider sweep. All-blocked recovery
  and unsupported-ID throttling remain covered.
- Quiesce keeps cached decisions and physically joins the worker; no automatic
  resume. Explicit negotiated reconfigure resumes it. Direct disabled config joins
  and clears cache. Host-level disable/replacement/caller-cancellation receipts,
  native lifecycle serialization and library/callback custody stay host-owned.

## Test source and current evidence

The existing quota/provider/discovery/revision/lifecycle tests were adapted to
exclusions instead of preserving a test-only picker. Old priority/lexical winner
assertions were removed, not presented as native selection coverage.

`filter_rpc_test.go` exercises the real `handleMethod`/worker path: feature refusal
before callbacks/startup, filter-only registration, duplicate/invalid offers,
unknown/mixed/exact-model handling, unoffered IDs, all due offered refreshes,
request-triggered credential replacement recovery, repeated reconfigure without
worker duplication, and held host callbacks through quiesce/disable/feature loss
until physical join followed by explicit resume. It does not claim to instantiate
two native host libraries or simulate a host context inside the C callback ABI.

The C-shared harness now checks absent-feature refusal with zero host callbacks,
then supported startup, quiesce/reconfigure and the actual filter response shape.
The process harness uses explicit source pairs rather than silently building the
old SDK module as the host. New/new gives the blocked credential higher priority
and a distinct loopback proxy to detect pre-filter narrowing or retry resurrection;
it also exercises host disable/explicit re-enable. Old-host/new-consumer checks
actual refusal, absent status route and zero provider usage. All fixtures are local
synthetic inputs; no developer credentials or real provider account is needed.

Observed PASS: base tree equality, frozen-input/archive integrity, clean private
source state and `git diff --check` before commit.

**UNRUN:** gofmt, compile/unit/race/vet, C-shared ABI and all four process pairs.
Go/gofmt are absent from this Dev1 PATH; no heavy build was attempted. No CI request
was sent for this slice. Formatting and execution remain independently pending.
Return native formatting changes as a patch/new source receipt; do not relabel
formatted bytes with this frozen candidate tree/hash.

## Finite commands for independently admitted CI — not dispatched

Use the existing owner's isolated prepared native Go/C/dependency closure, offline
module access, private temp/HOME/runtime state and resource limits. No new runner,
paid capacity, global setup or fallback. Retain command, exit, complete output,
native OS/architecture, Go/C identity and actual source/artifact hashes.

In the exact extracted consumer source:

```sh
gofmt -d abi.go abi_integration_test.go config.go discovery_test.go filter_rpc_test.go lifecycle_test.go main.go main_test.go process_e2e_test.go runtime.go scheduler.go
# In a CI job-local shell only; the toolchain/modules must already be prepared.
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOWORK=off CGO_ENABLED=1 GOFLAGS=-mod=readonly
go test -count=1 -timeout=60s -skip '^(TestCSharedABIBoundaries|TestCLIProxyAPIProcessEndToEnd)$' .
go test -race -count=1 -timeout=60s -skip '^TestCSharedABIBoundaries$' .
go vet ./...
go test -v -count=1 -timeout=5m -run '^TestCSharedABIBoundaries$' .
```

Prepare **fresh** source directories under the CI job's private workspace; verify
archive digests from SOURCE-IDENTITY.json before extraction. No existing checkout
or installation is modified. Here `$INPUTS` is this owned worktree's received
`.local` packet tree and `$PAIR_ROOT` is a fresh empty CI source directory:

```sh
mkdir -p "$PAIR_ROOT"/{host-old,host-new,plugin-old,plugin-new}
tar -xzf "$INPUTS/foundation/upstream-source.tar.gz" --strip-components=1 -C "$PAIR_ROOT/host-old"
tar -xf "$INPUTS/host-foundation-intake/host-source.tar" -C "$PAIR_ROOT/host-new"
tar -xf "$INPUTS/foundation/candidate-source.tar" -C "$PAIR_ROOT/plugin-old"
tar -xf "$INPUTS/filter-consumer-intake/consumer-source.tar" -C "$PAIR_ROOT/plugin-new"
cd "$PAIR_ROOT/plugin-new"

QUOTA_TEST_PAIR=old-old QUOTA_TEST_HOST_SOURCE="$PAIR_ROOT/host-old" QUOTA_TEST_PLUGIN_SOURCE="$PAIR_ROOT/plugin-old" \
  go test -v -count=1 -timeout=10m -run '^TestCLIProxyAPIProcessEndToEnd$' .
QUOTA_TEST_PAIR=new-old QUOTA_TEST_HOST_SOURCE="$PAIR_ROOT/host-new" QUOTA_TEST_PLUGIN_SOURCE="$PAIR_ROOT/plugin-old" \
  go test -v -count=1 -timeout=10m -run '^TestCLIProxyAPIProcessEndToEnd$' .
QUOTA_TEST_PAIR=new-new QUOTA_TEST_HOST_SOURCE="$PAIR_ROOT/host-new" QUOTA_TEST_PLUGIN_SOURCE="$PAIR_ROOT/plugin-new" \
  go test -v -count=1 -timeout=10m -run '^TestCLIProxyAPIProcessEndToEnd$' .
QUOTA_TEST_PAIR=old-new-refused QUOTA_TEST_HOST_SOURCE="$PAIR_ROOT/host-old" QUOTA_TEST_PLUGIN_SOURCE="$PAIR_ROOT/plugin-new" \
  go test -v -count=1 -timeout=10m -run '^TestCLIProxyAPIProcessEndToEnd$' .
```

`$PAIR_ROOT` must be absolute. “Old host” here means exact unpatched maintained8335,
NOT the historical 4b5/1e1e baseline. “Old plugin” means frozen8c6 legacy scheduler,
NOT a relabeled filter consumer. SDK7.2.100 remains a compile dependency only.
Unset process inputs produce SKIP, never pair acceptance. The fixture logs built
host/plugin digests, but overrides the provider usage endpoint and removes its temp
binaries afterward; these are fixture bytes, not distributable release artifacts.

Also run the frozen host packet's full affected-package/race/vet/server checks and
manager/lifecycle cases against its exact source. A parent `-race` run excludes the
`!race` process test and does not instrument a separately built C-shared library.
The Unix C harness skips Windows; native platform qualification remains explicit.

## Remaining gates and next action

Source consumer scope is finite and frozen. No authorship blocker remains. Await
independent review and actual formatting/test findings; this same owner remains
available for corrections. No repeated polling or CI dispatch by this owner.

Host source intake, consumer source intake, fixture execution, packaged-byte native
receiving, review/landing and activation are different gates. The process fixture
adds native smoke/selection/disable coverage, not full packaged replacement,
failed-replacement/cancelled-cleanup/shutdown callback-custody proof. The frozen
host lifecycle tests and actual exact packaged pair receiving remain required.
The existing release/rollback rules still apply; no live or platform-support claim.
