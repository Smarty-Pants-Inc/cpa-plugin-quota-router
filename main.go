package main

import "time"

const (
	pluginName               = "quota-router"
	defaultCutoffPercentUsed = 50.0
	defaultProtectedModel    = "claude-fable-5"
	defaultPollInterval      = 5 * time.Minute
	defaultRequestTimeout    = 10 * time.Second
	defaultUsageEndpoint     = "https://api.anthropic.com/api/oauth/usage"
	anthropicOAuthBeta       = "oauth-2025-04-20"
	maxUsageResponseBytes    = 64 << 10
	exhaustedErrorCode       = "quota_router_exhausted"
	managementStatusRoute    = "/plugins/quota-router/status"
	managementStatusFullPath = "/v0/management" + managementStatusRoute
	pollErrorAuthGet         = "auth_get"
	pollErrorMissingToken    = "missing_token"
	pollErrorInvalidWeekly   = "invalid_weekly"
	pollErrorInvalidJSON     = "invalid_json"
	pollErrorBodyTooLarge    = "body_too_large"
	pollErrorTimeout         = "timeout"
	pollErrorCancelled       = "cancelled"
	pollErrorNetwork         = "network"
	pollErrorUnauthorized    = "unauthorized"
	pollErrorForbidden       = "forbidden"
	pollErrorRateLimited     = "rate_limited"
	pollErrorServer          = "server_error"
	pollErrorHTTP            = "http_error"
	pollErrorRead            = "read_error"
)

var pluginVersion = "0.5.0"

// Variable only so the C-shared integration test can inject a local fixture.
var usageEndpoint = defaultUsageEndpoint

var activeRuntime = newPluginRuntime(
	cgoHostClient{},
	newHTTPUsageFetcher(usageEndpoint, nil).fetch,
	time.Now,
)

func main() {}
