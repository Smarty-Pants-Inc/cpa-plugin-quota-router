# CLIProxyAPI Quota Router

Quota-aware account routing for [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI). The plugin routes configured protected models away from OAuth accounts when provider usage reaches a configurable cutoff, while leaving other models on CLIProxyAPI's native scheduler. This release supports Anthropic's seven-day usage quota; the provider-specific usage adapter can expand when another provider exposes equivalent data.

The default specifically protects `claude-fable-5` at 50%. This matches Anthropic's [June 30 redeployment announcement](https://www.anthropic.com/news/redeploying-fable-5), which included Fable 5 for up to 50% of weekly usage through July 7, 2026; Anthropic said access would use usage credits afterward, so both the model and cutoff remain configurable.

## Install

Download the archive for your platform from [Releases](https://github.com/Smarty-Pants-Inc/cpa-plugin-quota-router/releases), extract the library into CLIProxyAPI's plugin directory, and configure it by plugin ID:

```yaml
plugins:
  enabled: true
  configs:
    quota-router:
      enabled: true
      priority: 100
      protected-models: [claude-fable-5]
      cutoff-percent-used: 50
      poll-interval: 5m
      blocked-refresh-interval: 6h
      request-timeout: 10s
```

The library basename must be `quota-router` or a semver-suffixed form such as `quota-router-v0.5.0`, with `.dylib`, `.so`, or `.dll` for the host platform.

Published releases include Darwin (`amd64`, `arm64`), Linux (`amd64`, `arm64`), and Windows (`amd64`) builds.

`poll-interval` is the minimum cached-usage age before another refresh, not a continuous polling timer.

## Configuration

| Field | Default | Meaning |
| --- | --- | --- |
| `protected-models` | `[claude-fable-5]` | Exact, case-insensitive model IDs governed by the cutoff. |
| `cutoff-percent-used` | `50` | Blocks an eligible account at or above this seven-day utilization percentage. |
| `poll-interval` | `5m` | Minimum cache age before a protected request queues another refresh; a passed reset allows an immediate first-use refresh, with failed post-reset retries throttled from the attempt. |
| `blocked-refresh-interval` | `6h` | Minimum time since the last sample or refresh attempt before a protected request rechecks a blocked account. `0` disables this backstop. |
| `request-timeout` | `10s` | Timeout for the provider usage request, expressed as a Go duration. |

## Behavior

- Refreshes enabled physical Claude OAuth credentials when the worker starts; startup reconfiguration retries discovery only while the cache is empty. There is no time-driven polling.
- When a protected-model request selects an account whose cached usage is at least `poll-interval` old, queues one asynchronous refresh for that account while routing the current request from memory.
- Coalesces concurrent refreshes. A blocked account remains excluded before its reported reset, but protected requests queue a re-read after `blocked-refresh-interval` since its last sample or refresh attempt. This backstop can detect an early provider reset; it is not a continuous polling timer.
- A rejection-only design cannot enforce a pre-exhaustion cutoff: the rejection arrives only after the hard limit is reached.
- Applies only to exact, case-insensitive `protected-models` matches.
- Blocks an account at or above `cutoff-percent-used`. Truly unknown quota state retains the existing fail-open policy, but an exhausted pre-reset sample becomes **reset-pending**, not eligible, when its reset passes. First use queues a reread even if the pre-reset sample is younger than `poll-interval`; subsequent failed attempts remain rate-limited. Status reports reset-pending accounts as unknown but blocked.
- Normally eligible accounts are selected without waiting for reset-pending accounts. If none are available, selection synchronously queues or joins the existing coalesced worker reread, waiting at most `min(request-timeout, 2s)` for all reset-pending candidates. Only a successful reread below the cutoff enables the seat. A fresh exhausted reading remains blocked, even if its reported reset is already past; the blocked-refresh backstop can retry it later.
- **No quota-host outage fallback:** if every candidate remains reset-pending and no successful below-cutoff fresh read is available, fail closed through the existing no-seat error envelope (`quota_router_exhausted`), with message `no verified seat: all candidate seats await a post-reset quota read; quota host error: <safe category>`. Emit one decision WARN per pick, naming the seats and safe categories (multiple categories are sorted; unfinished reads use `read_pending`, and absent/unrecognized errors use `unverified`). Malformed or type-invalid local credential JSON is categorized as `invalid local credential`, never as a host outage (`auth_get`); this safe local category is also included in the no-seat error. Existing worker refresh-failure logs remain separate. Cached blocks are retained, and failed retries remain throttled; once the host recovers, the next retry-eligible pick can select a seat only after a successful below-cutoff read. All seats being reset-pending at once while the quota host is down is a narrow window because accounts reset at different times, and an unverified post-reset seat must never be used. An unfinished reread, unusable local credential, cancellation, or fresh exhausted result never enables a seat.
- Never changes auth files or CLIProxyAPI's permanent disabled state.
- Exposes authenticated status at `GET /v0/management/plugins/quota-router/status`.

## Security

- Reads eligible Claude credentials through CLIProxyAPI's host API and never writes auth files.
- Sends the access token only to Anthropic's fixed HTTPS usage endpoint and refuses redirects.
- Never logs access tokens, refresh tokens, or provider response bodies.
- Exposes status only through CLIProxyAPI's authenticated Management API.

## Build and test

Requires Go 1.26 and CLIProxyAPI v7.2.100 or newer.

```bash
make test
make test-race
make vet
make build
```

Release tags matching `v*` build store-compatible archives and `checksums.txt` through GitHub Actions.

Created and published by [Smarty Pants Inc](https://github.com/Smarty-Pants-Inc). MIT licensed.
