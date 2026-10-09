# Quota foundation — implementation custody

Wave: `cpa-plugin-quota-router/foundation/`. Owner: quota-foundation. Integration,
publication and all shared runtime control: dev-lead.

## Recovery receipt — source mission accepted; no source work outstanding

Consumed the completed same-reviewer return at Smarty Dev
`.local/astra-architecture-20260916/dev-foundation-intake/quota-host-repair-review-1/HANDOFF.md`;
verified SHA256 `50004e07b23a1b6dccd0611b21912a8e8f09ddc0618be140151c90ef0a0abbcd`.
**F1 CLOSED-SOURCE; F2 CLOSED-SOURCE**, no residual correction requested for host
`3fcfa803a4aa2815d69c9b606fadf7ab179ff3c1` with unchanged consumer
`ea1611e80c43029aa59b37e3ec3a94ef43e852c8`. Dev's
`QUOTA-3FC-ACCEPTED-CI-HANDOFF.md` already received this acceptance and assigned
qualification to the existing CI owner. Do not redispatch review or invent repairs.

Consumed CI's `quota-format-ready-reconciliation-1/HANDOFF.md` under its existing
`.local/ci-rollout-20260913/`: execution of `quota-pair-3fc-format1` and
`quota-consumer-ea1611e-format1` is unestablished, not proved never-run. The unrelated
Gateway formatter receipt is not quota evidence. No matching later quota return
was found in that retained packet tree during recovery. Specific next operand:
ci-delivery joins its authoritative once-intent history and returns the actual
quota formatting result/delta or preserves uncertainty without replay. This author
has no returned formatting/test repair to apply. Native/test/release acceptance
remains distinct from source closure. Existing allocation, Source1/manual holds,
source ownership and uncertain effects are unchanged. Stop source work; remain
available for actual findings. No native operation or new dispatch performed.

## Latest host repair successor

Same-author F1/F2 source repair is frozen at
`3fcfa803a4aa2815d69c9b606fadf7ab179ff3c1`, tree
`266bd5bc6dc25911a8607b2b8e1be2f8a87991c2`.
See `.local/host-two-finding-intake/HANDOFF.md` for full/affected patches, archive,
regressions and exact UNRUN limits. The next review is affected closure by the
same publication-operands reviewer via Dev, not a full paired replay. Frozen
hosta202 and consumerea161 and all earlier operands below remain unchanged.
No execution, CI dispatch, source acceptance or runtime claim accompanies the repair.

## Early boundary input (2026-09-16)

Clean base independently verified: `17dae28d4e37a0dd156a1c2a5456cb3a67048602`.
`v0.5.0` differs only in README. Full Chief synthesis SHA256
`3dfb08ae1f464eaac986fbde53f889c31679b33b29652ac48d39bdab8f733961` and unchanged
repository review `9992fa5c4d395f213a19468f321a77f2dddf22ef8a89d6c1f46dfca26a6bc137`
read, including source evidence and shared CI/credentials/workflow guidance.

Test-only commit: `8e531375c8efe54fb4de1ddf507c79a753a45c23`.
Tree: `da91d3b3d6292ff0119dd435c94c10b1df48c2cf`.
Archive: `.local/foundation/red-source.tar`.
SHA256: `d777640fed6750bdcbc758f58bd8ac4fc5ffe91d1f80cc7b6500b51951300c4c`.
No production source changed in this operand. `git diff --check` passed.
Go/gofmt not found on current Dev1 PATH. No Go/C build or test run attempted.

## Finite CI request (not execution or admission)

Use the existing CI owner and admitted candidate-isolation route; no automatic
paid fallback or persistent trusted-main runner admission. Prefer reuse of prepared
Go 1.26.4 and C toolchain, subject to CI's exact platform/source acceptance. Keep
fresh private HOME/temp state, no provider/vault/developer credentials. Dependency
pins are the archive's unchanged go.mod/go.sum. Fake-host/provider cases only:

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=1 go test -count=1 -timeout=30s -run '^(TestBlockedRequestDiscoversReplacement|TestUnsupportedRequestsRetainDiscoveryThrottle|TestConcurrentBlockedRequestsCoalesceDiscovery)$' .
```

Expected source-derived failures (NOT yet observed test results):
- all-blocked replacement: no worker trigger, wait for second fetch expires;
- unsupported sequential requests: cache pruning erases throttle, excess list calls;
- concurrent blocked requests: zero post-startup list calls instead of one.

Retain complete output, native toolchain/platform/source identity and exit status.
This focused command does not invoke process/C-shared harness tests. It still needs
CGO to compile the package ABI. ARM64 results do not qualify Linux amd64 live ABI.

## Frozen plugin candidate — awaiting actual CI execution

Commit: `8c6efa015ecd75e9225cc99c7ff82626a2905b4d`.
Tree: `9e97c097bb2c62e529a8410b224d267928450742`.
Archive: `.local/foundation/candidate-source.tar`.
SHA256: `72147467d3842beb2ad6fc4b22d3f35d049d7f50e5480e939d2ebd88bba8daac`.
Both commits are local only, not pushed, reviewed, landed or installed.
Tracked source is clean; `.local/foundation/` holds needed private evidence.

Implemented:
- Request-triggered metadata discovery even when all candidates are blocked.
  One global attempt throttle survives cache pruning and unknown/unsupported IDs.
- One existing worker, coalescing, no idle polling. Usage refresh only for selected
  due credentials or new/changed credentials. Unchanged blocked accounts sleep
  until reset. No host/provider operation on the synchronous pick path.
- Separate credential revision, discovery attempt/success, usage attempt and
  sample timestamps. Conservative invalidation; stale observed-revision results
  (success or failure) cannot replace the current revision. No guessed stable
  quota identity or atomic host-snapshot claim.
- Additive sanitized discovery/attempt status; expired sample timestamp retained.
- Existing optional `plugin.quiesce`: stop refresh admission, cancel and join,
  retain cached decisions/config, explicit reconfigure resumes discovery. No
  timeout/detach. TryLock keeps pick off a blocked lifecycle join.
- Request-worker regressions, revision/callback lifetime/RPC quiesce tests, and
  existing Unix C harness extended through quiesce/reconfigure.
- Exact-pair release receiving requirements; normal release upload no longer
  clobbers versioned assets. No workflow dispatch or release performed.

Static check: `git diff --check` passed before commit. No Go test, compiler,
formatter or native ABI result is claimed. Local Go/gofmt unavailable; no heavy
Dev1 operation attempted. CI execution remains an external prerequisite, not a
source pass. Preserve the red input above before running the repaired input.

Candidate finite check commands (prepared dependencies, CI-owned placement):

```sh
# Formatting/parsing only; return a formatting patch if needed to the source owner.
gofmt -l abi.go abi_integration_test.go cache.go config.go discovery_test.go lifecycle_test.go main_test.go runtime.go scheduler.go
# Fake-host/provider plus existing unit/loopback adapter tests, excluding builds.
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=1 go test -count=1 -timeout=60s -skip '^(TestCSharedABIBoundaries|TestCLIProxyAPIProcessEndToEnd)$' .
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=1 go test -race -count=1 -timeout=60s -skip '^TestCSharedABIBoundaries$' .
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=1 go vet ./...
```

`!race` excludes process E2E from the race build. The complete required make test,
make test-race, make vet and native package/pair gates remain pending separately.
No broad five-platform workflow is requested by these focused inputs.

## Maintained host correspondence and accepted next boundary

Fresh scoped read-only GitHub inspection selected maintained upstream
`router-for-me/CLIProxyAPI` main `8335eac731946bd4eff18f500653f93736df53d6`,
tree `13942c68d7859e102308cf683e9aa820b7c39018`. Metadata is in
`upstream-head.json`; archive `upstream-source.tar.gz` SHA256
`10da3a9e176b882d9b297dd5f0eeecfdd5e8fb99638184e45bb636d866d3f178`.
Extracted source is read-only diagnostic custody, not a linked host writing lane.

Dev-lead explicitly accepted this exact HOST SOURCE proposal destination and
minimal negotiated scheduler.filter v1 direction. `docs/host-contract.md` names
actual reuse seams and the boundary at the plugin freeze. A later explicit dispatch
assigned bounded host source to this SAME author. See the host implementation
packet at `.local/host-foundation-intake/HANDOFF.md` from this worktree. Host source is now
implemented in isolated `.local/host-foundation-8335`; the frozen plugin source and
archives remain unchanged. No filter-only consumer activation or new source owner.

The inspected UNPATCHED host skips quiesce on global/instance disable and does not
require successful quiesce before replacement. The isolated frozen host packet
implements those obligations and the negotiated API; it is not an installed host
or native qualification. The C callback still has no cancellation context; physical
settlement can remain pending. Exact pair manager/native-load/callback-custody
checks remain mandatory. See the host packet for implementation custody.

## Ownership, blockers, next action

- Source owner quota-foundation retains these commits; dev-lead sole integrator,
  publisher and shared runtime owner. Chief controls native model/effort changes;
  no model controls or fallback by this owner.
- CI owner was sent the early exact request through the existing Herdr agent
  surface. Tool submission was accepted; no execution/admission receipt or actual
  test output received. Candidate input follows through the same owner.
- Next CI action: admit the finite red/candidate inputs, return exact native
  command/output/source/formatting evidence. Source owner fixes actual failures.
- Host source action advanced under explicit assignment: same owner produced
  private host candidate `a2020b618173204312d11610000e76b2444260d9`, tree
  `a2422722564bfa47f0c917747df382dbde25107a`. New exact source/patch/check/caller
  custody is in `.local/host-foundation-intake/HANDOFF.md`. Formatting and actual
  host tests remain UNRUN pending the existing CI owner; no host patch integrated
  or activated. The frozen legacy plugin is not a new filter consumer.
- Historical gateway, frozen urgent tool-prefix packet, mapped live plugin,
  configuration, provider accounts, root checkout and Herdr layout unchanged.
- The later explicit consumer assignment is also complete as isolated SOURCE:
  `.local/filter-consumer-intake/HANDOFF.md`, private candidate
  `ea1611e80c43029aa59b37e3ec3a94ef43e852c8`, tree
  `bb8bc7eff7a7bfbf968318e8fe012f100843c675`. Its source is in
  `.local/filter-consumer-v1`; it does not alter any frozen plugin/host input.
  It implements real negotiated filtering, worker/RPC tests and four explicit
  native process-pair commands. Formatting and execution remain UNRUN; this slice
  made no CI dispatch. The frozen root plugin above remains legacy by design.
- Source work is finite and frozen pending review and actual check findings. No
  runtime, publication or native-platform support claim; no polling/check loop.
