# CLIProxyAPI Anthropic Router

Quota-aware Anthropic account routing for [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI). The plugin pauses configured protected models on an OAuth account when Anthropic's seven-day utilization reaches a cutoff, while leaving other models on CLIProxyAPI's native scheduler.

The default specifically protects `claude-fable-5` at 50%. This matches Anthropic's [June 30 redeployment announcement](https://www.anthropic.com/news/redeploying-fable-5), which included Fable 5 for up to 50% of weekly usage through July 7, 2026; Anthropic said access would use usage credits afterward, so both the model and cutoff remain configurable.

## Install

Download the archive for your platform from [Releases](https://github.com/Smarty-Pants-Inc/cliproxyapi-anthropic-router/releases), extract the library into CLIProxyAPI's plugin directory, and configure it by plugin ID:

```yaml
plugins:
  enabled: true
  configs:
    cliproxyapi-anthropic-router:
      enabled: true
      priority: 100
      protected-models: [claude-fable-5]
      cutoff-percent-used: 50
      poll-interval: 5m
      request-timeout: 10s
```

The library basename must be `cliproxyapi-anthropic-router` with `.dylib`, `.so`, or `.dll` for the host platform.

## Behavior

- Refreshes Anthropic's seven-day usage once per enabled physical Claude OAuth credential on the configured interval; request routing itself reads only the in-memory snapshot.
- A rejection-only design cannot enforce a pre-exhaustion cutoff: the rejection arrives only after the hard limit is reached.
- Applies only to exact, case-insensitive `protected-models` matches.
- Blocks an account at or above `cutoff-percent-used`; unknown or stale quota state fails open.
- Never changes auth files or CLIProxyAPI's permanent disabled state.
- Exposes authenticated status at `GET /v0/management/plugins/cliproxyapi-anthropic-router/status`.

## Build and test

Requires Go 1.26 and CLIProxyAPI v7.2.100 or newer.

```bash
make test
make build
```

Release tags matching `v*` build store-compatible archives and `checksums.txt` through GitHub Actions.

Created and published by [Smarty Pants Inc](https://github.com/Smarty-Pants-Inc). MIT licensed.
