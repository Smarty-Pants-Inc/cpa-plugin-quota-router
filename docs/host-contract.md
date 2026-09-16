# Host eligibility and lifecycle contract proposal

Status: **integration-owner-accepted direction, not an implemented eligibility
API**. On 2026-09-16 dev-lead accepted the exact maintained source destination and
minimal explicitly negotiated filter direction below. No host source writer is
assigned. The current plugin still registers `scheduler.pick`. Discovery and
optional quiesce support can be reviewed independently. Do not ship a filter-only
plugin against an unsupported host or treat this document as activation authority.

## Maintained integration destination

Read-only upstream inspection on 2026-09-16 selected:

- Repository: `https://github.com/router-for-me/CLIProxyAPI`, maintained `main`.
- Commit: `8335eac731946bd4eff18f500653f93736df53d6`.
- Tree: `13942c68d7859e102308cf683e9aa820b7c39018`.
- Commit date: 2026-09-15T12:08:12Z. Repository metadata was not archived or disabled.
- Native ABI 1; JSON schema 6. The plugin's SDK pin remains v7.2.100.

Proposed host destination: an upstream-targeted change based on that exact main
commit, reconciled by the gateway integration owner before implementation. It is
not the historical April Smarty fork and not the frozen `4b5f1eab` tool-prefix
regression archive. A new head must be re-inspected before rebasing the proposal.
No host source has been changed by this plugin task.

## Existing seams and remaining gap

At the selected host:

- `sdk/cliproxy/auth/conductor_selection.go` already separates
  `availableAuthsForRouteModelAcrossPriorities` from `highestPriorityAuths` and
  `availableAuthsForSelector`. Reuse that availability pass.
- `pickNextLegacy` and `pickNextMixedLegacy` enforce request eligibility,
  pinning, tried IDs, executor/model support and disabled state, then compute
  availability. They call the scheduler with only the top priority tier.
- The native affinity selector receives lower tiers where needed, while the
  plugin scheduler does not. Preserve this distinction after exclusions.
- `sdk/pluginapi/types.go` and `sdk/pluginabi/types.go` expose only an auth-ID
  selection or named builtin delegation, not candidate exclusions.
- `internal/pluginhost/scheduler.go` uses only the highest-priority scheduler
  plugin. An unhandled return does not chain scheduler plugins.
- A named builtin delegate reselects from host state. It cannot enforce an
  exclusion set unless that set remains attached to that same selection attempt.
- Home routing bypasses local selection. It is not quota-qualified by this seam.

## Minimum optional operation to agree before host source edits

Proposed operation: `scheduler.filter`, negotiated by an explicit versioned host
feature `scheduler_filter_v1` in register/reconfigure requests. The registration
response advertises the matching filter capability separately from `scheduler`.
Names and schema allocation require gateway-owner agreement; they are not current
upstream fields. Keep native ABI 1 if its layout need not change.

Request fields: the route's exact requested model and admitted candidate pairs
`{id, provider}`. Do not send token material, credential paths, request bodies,
headers, arbitrary auth metadata or a guessed provider quota identity. Each
candidate has already passed host request/security/pinning/tried/model/cooldown
checks **across priority tiers**. The plugin returns only `excluded_ids`.

Host rules:

1. Call the filter outside the manager lock. Validate every excluded ID as a
   unique member of the offered set; bound the response by that set's size.
2. Subtract exclusions, then use the existing priority/distribution/affinity
   logic. The plugin cannot select or add an account. Do not implement a second
   selector in the plugin.
3. Reapply the filter on each real selection/retry path. Legacy scheduler plugins
   receive only the remaining eligible candidates. Their builtin-delegate path
   must retain the same exclusions; it must not reintroduce excluded IDs.
4. A pinned excluded account does not authorize another account. Empty remaining
   candidates produce explicit quota exhaustion, not an unhandled fallback.
5. Separate mandatory filter invocation from legacy scheduler precedence. A
   higher-priority scheduler must not silently shadow quota filtering. Multiple
   filter composition is not needed for this first pair; refuse unsupported
   combinations rather than inventing a plugin pipeline.
6. Preserve configured selector behavior, including its existing affinity rule
   for a valid lower-priority binding. Revalidate native eligibility as required
   by the manager's concurrency contract. Never mutate auth disabled/cooldown
   state as a substitute for exclusions.
7. Unsupported active Home mode must be explicit at registration/configuration;
   do not advertise filter enforcement for bypass paths.

Plugin rules: exact case-insensitive configured-model matching; exclude only
known-blocked physical Claude credentials within the offered set. Unknown and
reset-expired samples fail open. Other provider candidates remain untouched.
Do not infer protection for an unconfigured alias or suffix. Mixed routes can
filter their Claude subset only after that exact behavior is covered by pair
acceptance. The current legacy plugin still leaves multi-provider routes alone.

Negotiation is required **before starting the worker or registering capabilities**.
A future filter-only plugin must return `unsupported_host` when the host feature
is missing or too old, including a missing/zero legacy request. An unknown JSON
field or a host ignoring a response capability is not negotiation. Keep old host /
old plugin and new host / old plugin paths unchanged. Do not silently fall back
from the new filter plugin to lexical selection.

A trustworthy opaque credential revision would improve list/get/sample consistency,
but none was found in this host contract. This proposal does not invent one. The
plugin's conservative, eventually discovered file revision remains a documented
limit until an actual host/adapter revision contract is accepted.

## Lifecycle correspondence

The selected host already calls `plugin.quiesce` with `{}` before replacement,
and `plugin.reconfigure` on the previous library during replacement rollback.
This plugin implements that existing optional method without changing its SDK pin:
stop new refresh admission, cancel and **join** the one worker, retain config and
cache, then resume with fresh metadata discovery only on explicit enabled
register/reconfigure. Repeated quiesce/shutdown is safe. Cached quota decisions
remain available because the old host record can still be used during replacement.
Request-triggered refresh does not wait behind a lifecycle join.

A host auth/log callback has no cancellation context in the current C ABI. Quiesce
can therefore wait for that callback. It must not time out, detach the worker and
return success. `dynamicLibraryClient.Shutdown` calls plugin shutdown before freeing
its callback context/table and closing the library. Preserve that order.

The host's `guardedPluginClient` can return to its caller after context cancellation
while it retains cleanup ownership. That is not proof of a completed join. In
`Host.ApplyConfig`, failed/cancelled quiesce can still be followed by replacement;
global and instance disable remove active records without calling quiesce. Thus
plugin support alone does **not** qualify host disable or guarantee one worker
across replacement. The host owner must gate replacement on settled quiesce and
quiesce disabled/removed instances before retaining them. On uncertainty, retain
library/callback custody; do not free it or claim settled cleanup.

## Required paired checks

Use the real selected manager and native host/library harness, with synthetic
credentials/provider endpoints only:

- High-priority blocked A / low-priority eligible B; equal-tier native
  distribution; affinity; pinned blocked account; all excluded.
- Disabled/cooling/tried/model-ineligible accounts; mixed providers; exact
  model/alias/suffix handling; retries; scheduler coexistence; Home refusal.
- Duplicate/foreign excluded IDs; malformed/error filter responses; unsupported
  host negotiation before any plugin worker starts.
- Old/old, new-host/old-plugin, new/new, and refused new-plugin/old-host pairs.
- Disable/re-enable, config reload, blocked host callback, provider cancellation,
  quiesce, successful replacement, failed replacement rollback, final shutdown,
  and callback-table lifetime.

Current fake-host/RPC and Unix ABI tests are inputs to that qualification. They
are not a substitute for it, and no execution result is asserted in this document.
