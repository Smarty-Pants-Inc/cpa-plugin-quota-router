# CLIProxyAPI Quota Router

Quota-aware account routing for [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI). The plugin routes configured protected models away from OAuth accounts when provider usage reaches a configurable cutoff, while leaving other models on CLIProxyAPI's native scheduler. This release supports Anthropic's seven-day usage quota; the provider-specific usage adapter can expand when another provider exposes equivalent data.

The default specifically protects `claude-fable-5` at 50%. This matches Anthropic's [June 30 redeployment announcement](https://www.anthropic.com/news/redeploying-fable-5), which included Fable 5 for up to 50% of weekly usage through July 7, 2026; Anthropic said access would use usage credits afterward, so both the model and cutoff remain configurable.

## Negotiated filter candidate — not released

This source requires the explicitly advertised `scheduler_filter_v1` host feature.
It refuses an unsupported host before starting a worker; it does not fall back to
`scheduler.pick`. Published legacy archives are not this consumer. The exact
source pairing and remaining gates are in [the host contract](docs/host-contract.md).
Do not install this candidate without native pair qualification and activation
authority. The retained configuration shape is:

```yaml
plugins:
  enabled: true
  configs:
    quota-router:
      enabled: true
      protected-models: [claude-fable-5]
      cutoff-percent-used: 50
      poll-interval: 5m
      request-timeout: 10s
```

The library basename must be `quota-router` or an approved semver-suffixed form,
with `.dylib`, `.so`, or `.dll` for the host platform. Plugin priority cannot shadow
this required filter or determine native account selection.

The packaging matrix includes Darwin (`amd64`, `arm64`), Linux (`amd64`, `arm64`), and Windows (`amd64`). A build for a platform is not proof of native load or compatibility with every gateway version. See [paired release qualification](docs/release-qualification.md).

`poll-interval` bounds request-triggered metadata discovery and cached-usage refresh. It is not a continuous polling timer.

## Configuration

| Field | Default | Meaning |
| --- | --- | --- |
| `protected-models` | `[claude-fable-5]` | Exact, case-insensitive model IDs governed by the cutoff. |
| `cutoff-percent-used` | `50` | Blocks an eligible account at or above this seven-day utilization percentage. |
| `poll-interval` | `5m` | Minimum time between discovery attempts, and minimum usage age before an offered eligible account is refreshed. |
| `request-timeout` | `10s` | Timeout for the provider usage request, expressed as a Go duration. |

## Behavior

- Discovers enabled physical Claude OAuth credentials when the worker starts. Protected requests queue metadata discovery when due, including requests for which every candidate is quota-blocked. There is no idle polling.
- Discovery attempts, including failures and unsupported candidate IDs, share one throttle that survives account-cache pruning. Configuration changes alone do not reset it.
- Discovery removes absent credentials and clears quota samples on an observed credential revision change. It queues usage reads for new or changed credentials, not an all-account provider sweep.
- A protected filter request queues due usage refreshes for offered eligible physical credentials. The plugin cannot know which one native selection will choose. The current exclusions use memory only; unchanged, unoffered credentials do not become a provider sweep.
- One worker coalesces discovery and usage work. An unchanged blocked credential gets no provider refresh before its reported reset time.
- Required `plugin.quiesce` cancels and joins refresh work, retaining cached decisions until explicit supported reconfiguration resumes discovery. Direct disabled reconfiguration also clears the cache. Losing the negotiated feature joins existing work and refuses reconfiguration. A stuck host callback keeps quiesce pending; caller cancellation is not a successful join.
- A rejection-only design cannot enforce a pre-exhaustion cutoff: the rejection arrives only after the hard limit is reached.
- Applies only to exact, case-insensitive `protected-models` matches.
- Blocks an account at or above `cutoff-percent-used`; unknown or reset-expired quota state fails open while a refresh is queued.
- Never changes auth files or CLIProxyAPI's permanent disabled state.
- Exposes authenticated status at `GET /v0/management/plugins/quota-router/status`. `discovery` reports the last metadata attempt, success and error category. Each account reports its last usage attempt and sample time; an expired sample retains its timestamp but is not known.

This is **best-effort, fail-open routing**, not a strict spending cap. File size,
modification time, path and host account metadata form a conservative credential
revision, not a stable provider quota identity. Even a token-only rewrite can clear
a sample and temporarily fail open. A result cannot overwrite a different revision
already observed by this worker, but discovery/list/get/fetch is not an atomic host
snapshot. An unobserved file change can remain undetected until the next bounded
request-triggered discovery pass. No revision value or physical path is exposed in
status.

`scheduler.filter` returns only unique `excluded_ids` from the host's offered
ID/provider pairs. Invalid candidate sets are refused before work is queued.
Unknown/reset-expired quota fails open; protocol/lifecycle failure is not an empty
exclusion response. All blocked candidates are returned as exclusions, leaving
exhaustion and native priorities, distribution, affinity, pinning, retries and
delegation to the host. Mixed-provider offers never exclude non-Claude accounts.
There is no picker, lexical tie-breaker or native scheduler implementation here.

## Security

- Reads eligible Claude credentials through CLIProxyAPI's host API and never writes auth files.
- Sends the access token only to Anthropic's fixed HTTPS usage endpoint and refuses redirects.
- Never logs access tokens, refresh tokens, or provider response bodies.
- Exposes status only through CLIProxyAPI's authenticated Management API.

## Build and test

Requires Go 1.26 and CGO. The SDK dependency remains pinned to CLIProxyAPI
v7.2.100; the additive negotiated JSON fields use small local wire types. The SDK
pin is not the process-test host. Native process tests require explicit
`QUOTA_TEST_HOST_SOURCE`, `QUOTA_TEST_PLUGIN_SOURCE` and `QUOTA_TEST_PAIR` inputs;
without them that test is skipped, not passed. Pair values are `old-old`, `new-old`,
`new-new` and `old-new-refused`. Exact input/command custody accompanies the source
packet. No native execution or release acceptance is implied by these tests.

```bash
make test
make test-race
make vet
make build
```

Release tags matching `v*` build store-compatible archives and `checksums.txt` through GitHub Actions. Normal publication refuses to overwrite existing assets; a rerun is not authority to change the bytes of a published version.

Created and published by [Smarty Pants Inc](https://github.com/Smarty-Pants-Inc). MIT licensed.
