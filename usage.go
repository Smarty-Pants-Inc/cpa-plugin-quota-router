package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

type usageResult struct {
	WeeklyPercentUsed float64
	ResetAt           time.Time
}

type usageFetcher func(context.Context, string, time.Duration) (usageResult, string)

type httpUsageFetcher struct {
	client   *http.Client
	endpoint string
}

func newHTTPUsageFetcher(endpoint string, transport http.RoundTripper) httpUsageFetcher {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return httpUsageFetcher{
		endpoint: endpoint,
		client: &http.Client{
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

type usageResponse struct {
	SevenDay *usageWindow `json:"seven_day"`
}

type usageWindow struct {
	Utilization *float64        `json:"utilization"`
	ResetsAt    json.RawMessage `json:"resets_at"`
}

func (f httpUsageFetcher) fetch(ctx context.Context, token string, timeout time.Duration) (usageResult, string) {
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, f.endpoint, nil)
	if err != nil {
		return usageResult{}, pollErrorHTTP
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", anthropicOAuthBeta)

	resp, err := f.client.Do(req)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return usageResult{}, pollErrorCancelled
		case errors.Is(err, context.DeadlineExceeded):
			return usageResult{}, pollErrorTimeout
		default:
			return usageResult{}, pollErrorNetwork
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return usageResult{}, pollErrorUnauthorized
		case http.StatusForbidden:
			return usageResult{}, pollErrorForbidden
		case http.StatusTooManyRequests:
			return usageResult{}, pollErrorRateLimited
		default:
			if resp.StatusCode >= 500 {
				return usageResult{}, pollErrorServer
			}
			return usageResult{}, pollErrorHTTP
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUsageResponseBytes+1))
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return usageResult{}, pollErrorCancelled
		case errors.Is(err, context.DeadlineExceeded):
			return usageResult{}, pollErrorTimeout
		default:
			return usageResult{}, pollErrorRead
		}
	}
	if len(body) > maxUsageResponseBytes {
		return usageResult{}, pollErrorBodyTooLarge
	}
	var payload usageResponse
	if json.Unmarshal(body, &payload) != nil {
		return usageResult{}, pollErrorInvalidJSON
	}
	if payload.SevenDay == nil {
		return usageResult{}, pollErrorInvalidWeekly
	}
	percentUsed, ok := normalizeWeeklyPercent(payload.SevenDay.Utilization)
	if !ok {
		return usageResult{}, pollErrorInvalidWeekly
	}
	resetAt, ok := parseResetTime(payload.SevenDay.ResetsAt)
	if !ok {
		return usageResult{}, pollErrorInvalidWeekly
	}
	return usageResult{WeeklyPercentUsed: percentUsed, ResetAt: resetAt}, ""
}

// Anthropic reports percentage points: 1.0 means 1%, not 100%.
func normalizeWeeklyPercent(raw *float64) (float64, bool) {
	if raw == nil || math.IsNaN(*raw) || math.IsInf(*raw, 0) || *raw < 0 || *raw > 100 {
		return 0, false
	}
	return *raw, true
}

func parseResetTime(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}, false
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(text))
	return parsed, err == nil
}
