package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type fakeHost struct {
	mu        sync.Mutex
	entries   []pluginapi.HostAuthFileEntry
	authJSON  map[string]json.RawMessage
	getErrors map[string]error
	listError error
	listCalls int
	getCalls  []string
	logs      []string
}

func (h *fakeHost) listAuth() ([]pluginapi.HostAuthFileEntry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.listCalls++
	if h.listError != nil {
		return nil, h.listError
	}
	return append([]pluginapi.HostAuthFileEntry(nil), h.entries...), nil
}

func (h *fakeHost) getAuth(authIndex string) (json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.getCalls = append(h.getCalls, authIndex)
	if err := h.getErrors[authIndex]; err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), h.authJSON[authIndex]...), nil
}

func (h *fakeHost) log(level, message string, fields map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{"level": level, "message": message, "fields": fields})
	h.logs = append(h.logs, string(raw))
}

func (h *fakeHost) counts() (int, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.listCalls, len(h.getCalls)
}

func (h *fakeHost) logText() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.logs, "\n")
}

type fetchReply struct {
	result   usageResult
	category string
}

type fakeFetcher struct {
	mu      sync.Mutex
	replies map[string][]fetchReply
	calls   []string
}

func (f *fakeFetcher) fetch(_ context.Context, token string, _ time.Duration) (usageResult, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, token)
	replies := f.replies[token]
	if len(replies) == 0 {
		return usageResult{}, pollErrorNetwork
	}
	reply := replies[0]
	if len(replies) > 1 {
		f.replies[token] = replies[1:]
	}
	return reply.result, reply.category
}

func (f *fakeFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeFetcher) callTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

type testClock struct {
	mu    sync.Mutex
	value time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

func (c *testClock) set(value time.Time) {
	c.mu.Lock()
	c.value = value
	c.mu.Unlock()
}

func newTestRuntime(host hostClient, fetch usageFetcher, now time.Time) *pluginRuntime {
	return newPluginRuntime(host, fetch, func() time.Time { return now })
}

func claudeRequest(candidates ...pluginapi.SchedulerAuthCandidate) pluginapi.SchedulerPickRequest {
	return claudeModelRequest(defaultProtectedModel, candidates...)
}

func claudeModelRequest(model string, candidates ...pluginapi.SchedulerAuthCandidate) pluginapi.SchedulerPickRequest {
	return pluginapi.SchedulerPickRequest{
		Provider:   "claude",
		Providers:  []string{"claude"},
		Model:      model,
		Candidates: candidates,
	}
}

func candidate(id string, priority int) pluginapi.SchedulerAuthCandidate {
	return pluginapi.SchedulerAuthCandidate{ID: id, Provider: "claude", Priority: priority}
}

func physicalEntry(id, index string) pluginapi.HostAuthFileEntry {
	return pluginapi.HostAuthFileEntry{
		ID:        id,
		AuthIndex: index,
		Name:      id + ".json",
		Provider:  "claude",
		Path:      "/fixtures/" + id + ".json",
	}
}

func disabledEntry(id, index string) pluginapi.HostAuthFileEntry {
	entry := physicalEntry(id, index)
	entry.Disabled = true
	return entry
}

func credentialJSON(token string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"type":          "claude",
		"access_token":  token,
		"refresh_token": "refresh-must-stay-secret",
	})
	return raw
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func TestCurrentCutoffIsDerivedFromConfig(t *testing.T) {
	now := time.Now().UTC()
	runtime := newTestRuntime(&fakeHost{}, nil, now)
	runtime.cache.recordSuccess("auth-a", 55, now.Add(time.Hour), now)
	if _, decisionError := runtime.pick(claudeRequest(candidate("auth-a", 0))); decisionError == nil {
		t.Fatal("55% sample should be blocked at the default cutoff")
	}
	cfg := defaultPluginConfig()
	cfg.CutoffPercentUsed = 60
	runtime.config.Store(&cfg)
	response, decisionError := runtime.pick(claudeRequest(candidate("auth-a", 0)))
	if decisionError != nil || response.AuthID != "auth-a" {
		t.Fatalf("response = %#v, error = %#v", response, decisionError)
	}
}

func TestDisabledConfigLeavesSchedulerUnhandled(t *testing.T) {
	runtime := newTestRuntime(&fakeHost{}, nil, time.Now())
	cfg := defaultPluginConfig()
	cfg.Enabled = false
	runtime.config.Store(&cfg)
	response, decisionError := runtime.pick(claudeRequest(candidate("auth-a", 0)))
	if decisionError != nil || response.Handled {
		t.Fatalf("response = %#v, error = %#v; want unhandled", response, decisionError)
	}
}

func TestSchedulerOnlyHandlesProtectedModels(t *testing.T) {
	now := time.Now().UTC()
	runtime := newTestRuntime(&fakeHost{}, nil, now)
	runtime.cache.recordSuccess("auth-a", 80, now.Add(time.Hour), now)

	for _, model := range []string{defaultProtectedModel, " CLAUDE-FABLE-5 "} {
		response, decisionError := runtime.pick(claudeModelRequest(model, candidate("auth-a", 0)))
		if response.Handled || decisionError == nil || decisionError.Code != exhaustedErrorCode {
			t.Fatalf("protected model %q: response=%#v error=%#v", model, response, decisionError)
		}
	}

	response, decisionError := runtime.pick(claudeModelRequest("claude-haiku-4-5-20251001", candidate("auth-a", 0)))
	if decisionError != nil || response.Handled {
		t.Fatalf("unprotected model: response=%#v error=%#v", response, decisionError)
	}

	cfg := defaultPluginConfig()
	cfg.ProtectedModels = []string{"claude-sonnet-4-6"}
	runtime.config.Store(&cfg)
	response, decisionError = runtime.pick(claudeModelRequest(defaultProtectedModel, candidate("auth-a", 0)))
	if decisionError != nil || response.Handled {
		t.Fatalf("removed protected model: response=%#v error=%#v", response, decisionError)
	}
	response, decisionError = runtime.pick(claudeModelRequest("claude-sonnet-4-6", candidate("auth-a", 0)))
	if response.Handled || decisionError == nil || decisionError.Code != exhaustedErrorCode {
		t.Fatalf("configured protected model: response=%#v error=%#v", response, decisionError)
	}
}

func TestDisableClearsCachedQuota(t *testing.T) {
	now := time.Now().UTC()
	runtime := newTestRuntime(&fakeHost{}, nil, now)
	runtime.cache.recordSuccess("auth-a", 80, now.Add(time.Hour), now)
	cfg := defaultPluginConfig()
	cfg.Enabled = false
	runtime.applyConfig(cfg)
	if sample := runtime.cache.snapshot("auth-a"); sample != (quotaSample{}) {
		t.Fatalf("disabled cache = %#v", sample)
	}
}

func TestWhitespaceAuthIDIsIgnored(t *testing.T) {
	now := time.Now().UTC()
	runtime := newTestRuntime(&fakeHost{}, nil, now)
	runtime.cache.recordSuccess("auth-a", 80, now.Add(time.Hour), now)
	response, decisionError := runtime.pick(claudeRequest(
		candidate("auth-a", 0),
		candidate(" auth-a ", 100),
	))
	if decisionError == nil || response.Handled {
		t.Fatalf("whitespace auth was selected: response=%#v error=%#v", response, decisionError)
	}
}

func TestCutoffBoundary(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		percent   float64
		wantAuth  string
		wantError bool
	}{
		{name: "49.9 remains eligible", percent: 49.9, wantAuth: "auth-a"},
		{name: "exactly 50 is blocked", percent: 50, wantError: true},
		{name: "above 50 is blocked", percent: 75, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := newTestRuntime(&fakeHost{}, nil, now)
			runtime.cache.recordSuccess("auth-a", test.percent, now.Add(time.Hour), now)
			response, decisionError := runtime.pick(claudeRequest(candidate("auth-a", 0)))
			if test.wantError {
				if decisionError == nil || decisionError.Code != exhaustedErrorCode {
					t.Fatalf("decision error = %#v, want %s", decisionError, exhaustedErrorCode)
				}
				return
			}
			if decisionError != nil {
				t.Fatalf("decision error = %v", decisionError)
			}
			if response.AuthID != test.wantAuth || !response.Handled {
				t.Fatalf("response = %#v, want handled auth %q", response, test.wantAuth)
			}
		})
	}
}

func TestActualEndpointUtilizationScaleIsPercentPoints(t *testing.T) {
	value := 0.5
	got, ok := normalizeWeeklyPercent(&value)
	if !ok || got != 0.5 {
		t.Fatalf("normalizeWeeklyPercent(0.5) = %v, %v; want 0.5, true", got, ok)
	}

	headerError := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			headerError <- "missing fixture authorization header"
			return
		}
		if r.Header.Get("anthropic-beta") != anthropicOAuthBeta {
			headerError <- "missing anthropic beta header"
			return
		}
		_, _ = w.Write([]byte(`{"seven_day":{"utilization":9.0,"resets_at":"2026-07-25T00:00:00Z"}}`))
	}))
	defer server.Close()

	fetcher := newHTTPUsageFetcher(server.URL, server.Client().Transport)
	result, category := fetcher.fetch(context.Background(), "fixture-token", time.Second)
	select {
	case message := <-headerError:
		t.Fatal(message)
	default:
	}
	if category != "" {
		t.Fatalf("fetch category = %q", category)
	}
	if result.WeeklyPercentUsed != 9.0 {
		t.Fatalf("weekly percent = %v, want 9", result.WeeklyPercentUsed)
	}
	if result.ResetAt.Format(time.RFC3339) != "2026-07-25T00:00:00Z" {
		t.Fatalf("reset = %s", result.ResetAt)
	}
}

func TestMalformedOrMissingWeeklyQuotaFailsOpen(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		category string
	}{
		{name: "missing", body: `{}`, category: pollErrorInvalidWeekly},
		{name: "null", body: `{"seven_day":{"utilization":null}}`, category: pollErrorInvalidWeekly},
		{name: "negative", body: `{"seven_day":{"utilization":-1}}`, category: pollErrorInvalidWeekly},
		{name: "over one hundred", body: `{"seven_day":{"utilization":101,"resets_at":"2026-07-25T00:00:00Z"}}`, category: pollErrorInvalidWeekly},
		{name: "missing reset", body: `{"seven_day":{"utilization":50}}`, category: pollErrorInvalidWeekly},
		{name: "null reset", body: `{"seven_day":{"utilization":50,"resets_at":null}}`, category: pollErrorInvalidWeekly},
		{name: "malformed reset", body: `{"seven_day":{"utilization":50,"resets_at":"not-a-time"}}`, category: pollErrorInvalidWeekly},
		{name: "numeric reset", body: `{"seven_day":{"utilization":50,"resets_at":1784937600}}`, category: pollErrorInvalidWeekly},
		{name: "malformed json", body: `{"seven_day":`, category: pollErrorInvalidJSON},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			fetcher := newHTTPUsageFetcher(server.URL, server.Client().Transport)
			_, category := fetcher.fetch(context.Background(), "token", time.Second)
			if category != test.category {
				t.Fatalf("category = %q, want %q", category, test.category)
			}
		})
	}

	now := time.Now().UTC()
	runtime := newTestRuntime(&fakeHost{}, nil, now)
	response, decisionError := runtime.pick(claudeRequest(candidate("unknown", 0)))
	if decisionError != nil || !response.Handled || response.AuthID != "unknown" {
		t.Fatalf("unknown sample response = %#v, error = %#v", response, decisionError)
	}
}

func TestHTTPFailuresAreBoundedAndClassified(t *testing.T) {
	statuses := []struct {
		status   int
		category string
	}{
		{http.StatusUnauthorized, pollErrorUnauthorized},
		{http.StatusForbidden, pollErrorForbidden},
		{http.StatusTooManyRequests, pollErrorRateLimited},
		{http.StatusInternalServerError, pollErrorServer},
		{http.StatusBadRequest, pollErrorHTTP},
	}
	for _, test := range statuses {
		t.Run(fmt.Sprintf("status_%d", test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte("complete-upstream-body-must-not-escape"))
			}))
			defer server.Close()
			fetcher := newHTTPUsageFetcher(server.URL, server.Client().Transport)
			_, category := fetcher.fetch(context.Background(), "secret-token", time.Second)
			if category != test.category {
				t.Fatalf("category = %q, want %q", category, test.category)
			}
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxUsageResponseBytes+1)))
	}))
	defer server.Close()
	fetcher := newHTTPUsageFetcher(server.URL, server.Client().Transport)
	_, category := fetcher.fetch(context.Background(), "secret-token", time.Second)
	if category != pollErrorBodyTooLarge {
		t.Fatalf("oversized category = %q, want %q", category, pollErrorBodyTooLarge)
	}

	timeoutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`{"seven_day":{"utilization":1}}`))
	}))
	defer timeoutServer.Close()
	timeoutFetcher := newHTTPUsageFetcher(timeoutServer.URL, timeoutServer.Client().Transport)
	started := time.Now()
	_, category = timeoutFetcher.fetch(context.Background(), "secret-token", 10*time.Millisecond)
	if category != pollErrorTimeout {
		t.Fatalf("timeout category = %q, want %q", category, pollErrorTimeout)
	}
	if elapsed := time.Since(started); elapsed >= 90*time.Millisecond {
		t.Fatalf("request timeout took %s, want less than upstream delay", elapsed)
	}
}

func TestBodyReadCancellationIsClassified(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan string, 1)
	go func() {
		fetcher := newHTTPUsageFetcher(server.URL, server.Client().Transport)
		_, category := fetcher.fetch(ctx, "secret-token", time.Second)
		result <- category
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("response body did not start")
	}
	cancel()
	select {
	case category := <-result:
		if category != pollErrorCancelled {
			t.Fatalf("body cancellation category = %q, want %q", category, pollErrorCancelled)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled body read did not return")
	}
}

func TestHTTPRedirectIsNotFollowed(t *testing.T) {
	followed := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	fetcher := newHTTPUsageFetcher(redirect.URL, redirect.Client().Transport)
	if _, category := fetcher.fetch(context.Background(), "secret-token", time.Second); category != pollErrorHTTP {
		t.Fatalf("redirect category = %q, want %q", category, pollErrorHTTP)
	}
	if followed {
		t.Fatal("redirect target received the bearer token")
	}
}

func TestNonClaudeRequestsAreUnhandled(t *testing.T) {
	runtime := newTestRuntime(&fakeHost{}, nil, time.Now())
	response, decisionError := runtime.pick(pluginapi.SchedulerPickRequest{
		Provider:   "codex",
		Providers:  []string{"codex"},
		Candidates: []pluginapi.SchedulerAuthCandidate{{ID: "codex-a", Provider: "codex"}},
	})
	if decisionError != nil || response.Handled {
		t.Fatalf("response = %#v, error = %#v; want unhandled", response, decisionError)
	}
}

func TestClaudeSelectionIgnoresBlockedCandidates(t *testing.T) {
	now := time.Now().UTC()
	runtime := newTestRuntime(&fakeHost{}, nil, now)
	runtime.cache.recordSuccess("blocked-high", 50, now.Add(time.Hour), now)
	runtime.cache.recordSuccess("eligible-low", 49.9, now.Add(time.Hour), now)
	response, decisionError := runtime.pick(claudeRequest(
		candidate("blocked-high", 100),
		candidate("eligible-low", 1),
	))
	if decisionError != nil || response.AuthID != "eligible-low" {
		t.Fatalf("response = %#v, error = %#v", response, decisionError)
	}
}

func TestHighestPriorityEligibleCandidateWins(t *testing.T) {
	runtime := newTestRuntime(&fakeHost{}, nil, time.Now())
	response, decisionError := runtime.pick(claudeRequest(
		candidate("low", 1),
		candidate("high", 20),
		candidate("middle", 10),
	))
	if decisionError != nil || response.AuthID != "high" {
		t.Fatalf("response = %#v, error = %#v", response, decisionError)
	}
}

func TestEqualPriorityUsesLexicalAuthID(t *testing.T) {
	runtime := newTestRuntime(&fakeHost{}, nil, time.Now())
	response, decisionError := runtime.pick(claudeRequest(
		candidate("z-auth", 10),
		candidate("a-auth", 10),
		candidate("m-auth", 10),
	))
	if decisionError != nil || response.AuthID != "a-auth" {
		t.Fatalf("response = %#v, error = %#v", response, decisionError)
	}
}

func TestSchedulerRespectsRequestCandidateList(t *testing.T) {
	now := time.Now().UTC()
	runtime := newTestRuntime(&fakeHost{}, nil, now)
	runtime.cache.recordSuccess("not-supplied", 1, now.Add(time.Hour), now)
	response, decisionError := runtime.pick(claudeRequest(candidate("supplied", 1)))
	if decisionError != nil || response.AuthID != "supplied" {
		t.Fatalf("response = %#v, error = %#v", response, decisionError)
	}
}

func TestAllClaudeCandidatesBlockedReturnsExplicitError(t *testing.T) {
	now := time.Now().UTC()
	runtime := newTestRuntime(&fakeHost{}, nil, now)
	runtime.cache.recordSuccess("auth-a", 50, now.Add(time.Hour), now)
	runtime.cache.recordSuccess("auth-b", 80, now.Add(time.Hour), now)
	response, decisionError := runtime.pick(claudeRequest(candidate("auth-a", 0), candidate("auth-b", 0)))
	if response.Handled || decisionError == nil || decisionError.Code != exhaustedErrorCode || decisionError.Message != exhaustedErrorCode {
		t.Fatalf("response = %#v, error = %#v", response, decisionError)
	}
}

func TestUnknownSampleFailsOpen(t *testing.T) {
	runtime := newTestRuntime(&fakeHost{}, nil, time.Now())
	response, decisionError := runtime.pick(claudeRequest(candidate("unknown", 0)))
	if decisionError != nil || response.AuthID != "unknown" || !response.Handled {
		t.Fatalf("response = %#v, error = %#v", response, decisionError)
	}
}

func TestPollingFailureRetainsKnownBlockedState(t *testing.T) {
	now := time.Now().UTC()
	host := &fakeHost{
		entries:  []pluginapi.HostAuthFileEntry{physicalEntry("auth-a", "index-a")},
		authJSON: map[string]json.RawMessage{"index-a": credentialJSON("token-a")},
	}
	fetcher := &fakeFetcher{replies: map[string][]fetchReply{
		"token-a": {{category: pollErrorRateLimited}},
	}}
	runtime := newTestRuntime(host, fetcher.fetch, now)
	runtime.cache.recordSuccess("auth-a", 50, now.Add(time.Hour), now.Add(-time.Minute))
	runtime.pollOnce(context.Background(), defaultPluginConfig())

	sample := runtime.cache.snapshot("auth-a")
	if !sample.HasSample || !sample.blocked(now, 50) || sample.WeeklyPercentUsed != 50 || sample.LastErrorCategory != pollErrorRateLimited {
		t.Fatalf("sample = %#v", sample)
	}
	if !runtime.cache.isBlocked("auth-a", now, 50) {
		t.Fatal("blocked state was cleared by polling failure")
	}
}

func TestSuccessfulBelowCutoffRefreshClearsBlockedState(t *testing.T) {
	now := time.Now().UTC()
	host := &fakeHost{
		entries:  []pluginapi.HostAuthFileEntry{physicalEntry("auth-a", "index-a")},
		authJSON: map[string]json.RawMessage{"index-a": credentialJSON("token-a")},
	}
	fetcher := &fakeFetcher{replies: map[string][]fetchReply{
		"token-a": {{result: usageResult{WeeklyPercentUsed: 49.9, ResetAt: now.Add(time.Hour)}}},
	}}
	runtime := newTestRuntime(host, fetcher.fetch, now)
	runtime.cache.recordSuccess("auth-a", 80, now.Add(time.Hour), now.Add(-time.Minute))
	runtime.pollOnce(context.Background(), defaultPluginConfig())

	sample := runtime.cache.snapshot("auth-a")
	if !sample.HasSample || sample.blocked(now, 50) || sample.WeeklyPercentUsed != 49.9 || sample.LastErrorCategory != "" {
		t.Fatalf("sample = %#v", sample)
	}
}

func TestExpiredResetClearsStaleBlockedSample(t *testing.T) {
	now := time.Now().UTC()
	runtime := newTestRuntime(&fakeHost{}, nil, now)
	runtime.cache.recordSuccess("auth-a", 80, now.Add(-time.Second), now.Add(-time.Hour))
	response, decisionError := runtime.pick(claudeRequest(candidate("auth-a", 0)))
	if decisionError != nil || response.AuthID != "auth-a" {
		t.Fatalf("response = %#v, error = %#v", response, decisionError)
	}
	sample := runtime.cache.snapshot("auth-a")
	if sample.known(now) || sample.blocked(now, 50) {
		t.Fatalf("expired sample = %#v, want fail-open state", sample)
	}
}

func TestOneFailedAccountDoesNotStopRefreshPass(t *testing.T) {
	now := time.Now().UTC()
	host := &fakeHost{
		entries: []pluginapi.HostAuthFileEntry{
			physicalEntry("auth-a", "index-a"),
			physicalEntry("auth-b", "index-b"),
		},
		authJSON:  map[string]json.RawMessage{"index-b": credentialJSON("token-b")},
		getErrors: map[string]error{"index-a": errors.New("fixture read failure")},
	}
	fetcher := &fakeFetcher{replies: map[string][]fetchReply{
		"token-b": {{result: usageResult{WeeklyPercentUsed: 10, ResetAt: now.Add(time.Hour)}}},
	}}
	runtime := newTestRuntime(host, fetcher.fetch, now)
	runtime.pollOnce(context.Background(), defaultPluginConfig())

	if runtime.cache.snapshot("auth-a").LastErrorCategory != pollErrorAuthGet {
		t.Fatalf("auth-a sample = %#v", runtime.cache.snapshot("auth-a"))
	}
	if !runtime.cache.snapshot("auth-b").HasSample {
		t.Fatalf("auth-b sample = %#v", runtime.cache.snapshot("auth-b"))
	}
	_, getCalls := host.counts()
	if getCalls != 2 || fetcher.callCount() != 1 {
		t.Fatalf("get calls = %d, fetch calls = %d", getCalls, fetcher.callCount())
	}
}

func TestPhysicalClaudeAuthMapping(t *testing.T) {
	entries := []pluginapi.HostAuthFileEntry{
		physicalEntry("z-auth", "index-z"),
		{ID: "runtime", AuthIndex: "runtime-index", Provider: "claude", RuntimeOnly: true},
		{ID: "codex", AuthIndex: "codex-index", Provider: "codex", Path: "/fixtures/codex.json"},
		{ID: "no-path", AuthIndex: "no-path-index", Provider: "claude"},
		disabledEntry("disabled", "disabled-index"),
		physicalEntry("a-auth", "index-a"),
		physicalEntry(" invalid", "invalid-index"),
	}
	got := physicalClaudeAuths(entries)
	if len(got) != 2 || got[0].ID != "a-auth" || got[0].AuthIndex != "index-a" || got[1].ID != "z-auth" {
		t.Fatalf("mapped auths = %#v", got)
	}
}

func TestCachePrunesRemovedAuths(t *testing.T) {
	now := time.Now().UTC()
	host := &fakeHost{
		entries:   []pluginapi.HostAuthFileEntry{physicalEntry("auth-a", "index-a")},
		getErrors: map[string]error{"index-a": errors.New("fixture read failure")},
	}
	runtime := newTestRuntime(host, (&fakeFetcher{}).fetch, now)
	runtime.cache.reconcile([]physicalClaudeAuth{
		{ID: "auth-a", AuthIndex: "index-a"},
		{ID: "removed", AuthIndex: "removed-index"},
	})
	runtime.pollOnce(context.Background(), defaultPluginConfig())
	if sample := runtime.cache.snapshot("removed"); sample != (quotaSample{}) {
		t.Fatalf("removed sample = %#v", sample)
	}
}

func TestCredentialReplacementClearsStaleBlock(t *testing.T) {
	now := time.Now().UTC()
	oldEntry := physicalEntry("auth-a", "index-a")
	oldEntry.Account = "same-account"
	oldEntry.ModTime = now
	newEntry := physicalEntry("auth-a", "index-a")
	newEntry.Account = "same-account"
	newEntry.ModTime = now
	newEntry.Path = "/fixtures/replacement.json"
	host := &fakeHost{
		entries:   []pluginapi.HostAuthFileEntry{newEntry},
		getErrors: map[string]error{"index-a": errors.New("fixture read failure")},
	}
	runtime := newTestRuntime(host, (&fakeFetcher{}).fetch, now)
	runtime.cache.reconcile(physicalClaudeAuths([]pluginapi.HostAuthFileEntry{oldEntry}))
	runtime.cache.recordSuccess("auth-a", 80, now.Add(time.Hour), now.Add(-time.Minute))
	runtime.pollOnce(context.Background(), defaultPluginConfig())
	sample := runtime.cache.snapshot("auth-a")
	if sample.HasSample || sample.blocked(now, 50) || sample.LastErrorCategory != pollErrorAuthGet {
		t.Fatalf("replacement sample = %#v", sample)
	}
}

func TestCredentialReplacementClearsBeforeNetworkPolling(t *testing.T) {
	now := time.Now().UTC()
	oldEntry := physicalEntry("auth-b", "index-b")
	oldEntry.ModTime = now.Add(-time.Hour)
	newEntry := physicalEntry("auth-b", "index-b")
	newEntry.ModTime = now
	host := &fakeHost{
		entries: []pluginapi.HostAuthFileEntry{
			physicalEntry("auth-a", "index-a"),
			newEntry,
		},
		authJSON: map[string]json.RawMessage{
			"index-a": credentialJSON("token-a"),
			"index-b": credentialJSON("token-b"),
		},
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releasePoll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releasePoll()
	fetch := func(ctx context.Context, token string, _ time.Duration) (usageResult, string) {
		if token == "token-a" {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return usageResult{}, pollErrorCancelled
			}
		}
		return usageResult{WeeklyPercentUsed: 10, ResetAt: now.Add(time.Hour)}, ""
	}
	runtime := newTestRuntime(host, fetch, now)
	runtime.cache.reconcile(physicalClaudeAuths([]pluginapi.HostAuthFileEntry{oldEntry}))
	runtime.cache.recordSuccess("auth-b", 80, now.Add(time.Hour), now.Add(-time.Minute))
	done := make(chan struct{})
	go func() {
		runtime.pollOnce(context.Background(), defaultPluginConfig())
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first credential was not polled")
	}
	response, decisionError := runtime.pick(claudeRequest(candidate("auth-b", 0)))
	if decisionError != nil || !response.Handled || response.AuthID != "auth-b" {
		t.Fatalf("replacement remained stale during refresh: response=%#v error=%#v", response, decisionError)
	}
	releasePoll()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("poll did not finish")
	}
}

func TestDisabledAuthIsNotPolled(t *testing.T) {
	host := &fakeHost{entries: []pluginapi.HostAuthFileEntry{disabledEntry("auth-a", "index-a")}}
	fetcher := &fakeFetcher{replies: map[string][]fetchReply{}}
	runtime := newTestRuntime(host, fetcher.fetch, time.Now())
	runtime.pollOnce(context.Background(), defaultPluginConfig())
	listCalls, getCalls := host.counts()
	if listCalls != 1 || getCalls != 0 || fetcher.callCount() != 0 {
		t.Fatalf("callbacks: list=%d get=%d fetch=%d", listCalls, getCalls, fetcher.callCount())
	}
}

func TestCancelledPollSkipsHostCallbacks(t *testing.T) {
	host := &fakeHost{}
	runtime := newTestRuntime(host, (&fakeFetcher{}).fetch, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runtime.pollOnce(ctx, defaultPluginConfig())
	listCalls, getCalls := host.counts()
	if listCalls != 0 || getCalls != 0 {
		t.Fatalf("callbacks after cancellation: list=%d get=%d", listCalls, getCalls)
	}
}

func TestReconfigureDoesNotStartDuplicateRefreshWorkers(t *testing.T) {
	host := &fakeHost{}
	fetcher := &fakeFetcher{replies: map[string][]fetchReply{}}
	runtime := newPluginRuntime(host, fetcher.fetch, time.Now)
	cfg := defaultPluginConfig()
	cfg.PollInterval = time.Hour
	runtime.applyConfig(cfg)
	waitFor(t, func() bool {
		listCalls, _ := host.counts()
		return listCalls > 0
	})
	firstDone := runtime.done
	for range 3 {
		runtime.applyConfig(cfg)
	}
	if runtime.done != firstDone {
		t.Fatal("reconfigure replaced the active refresh worker")
	}
	runtime.shutdown()
}

func TestShutdownTerminatesRefreshWorker(t *testing.T) {
	host := &fakeHost{}
	fetcher := &fakeFetcher{replies: map[string][]fetchReply{}}
	runtime := newPluginRuntime(host, fetcher.fetch, time.Now)
	cfg := defaultPluginConfig()
	cfg.PollInterval = time.Hour
	runtime.applyConfig(cfg)
	waitFor(t, func() bool {
		listCalls, _ := host.counts()
		return listCalls > 0
	})
	runtime.shutdown()
	if runtime.wake != nil || runtime.cancel != nil || runtime.done != nil {
		t.Fatalf("lifecycle handles not cleared: wake=%v cancel=%v done=%v", runtime.wake != nil, runtime.cancel != nil, runtime.done != nil)
	}
}

func TestIdleWorkerDoesNotRefreshOnInterval(t *testing.T) {
	host := &fakeHost{}
	runtime := newPluginRuntime(host, (&fakeFetcher{}).fetch, time.Now)
	cfg := defaultPluginConfig()
	cfg.PollInterval = 10 * time.Millisecond
	runtime.applyConfig(cfg)
	defer runtime.shutdown()

	waitFor(t, func() bool {
		listCalls, _ := host.counts()
		return listCalls == 1
	})
	time.Sleep(50 * time.Millisecond)
	listCalls, _ := host.counts()
	if listCalls != 1 {
		t.Fatalf("idle refresh calls = %d, want exactly the startup refresh", listCalls)
	}
}

func TestStaleRequestRefreshesOnlySelectedAuth(t *testing.T) {
	startedAt := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	clock := &testClock{value: startedAt}
	host := &fakeHost{
		entries: []pluginapi.HostAuthFileEntry{
			physicalEntry("auth-a", "index-a"),
			physicalEntry("auth-b", "index-b"),
		},
		authJSON: map[string]json.RawMessage{
			"index-a": credentialJSON("token-a"),
			"index-b": credentialJSON("token-b"),
		},
	}
	fetcher := &fakeFetcher{replies: map[string][]fetchReply{
		"token-a": {{result: usageResult{WeeklyPercentUsed: 10, ResetAt: startedAt.Add(time.Hour)}}},
		"token-b": {
			{result: usageResult{WeeklyPercentUsed: 10, ResetAt: startedAt.Add(time.Hour)}},
			{result: usageResult{WeeklyPercentUsed: 80, ResetAt: startedAt.Add(time.Hour)}},
		},
	}}
	runtime := newPluginRuntime(host, fetcher.fetch, clock.now)
	cfg := defaultPluginConfig()
	cfg.PollInterval = 5 * time.Minute
	runtime.applyConfig(cfg)
	defer runtime.shutdown()
	waitFor(t, func() bool { return fetcher.callCount() == 2 })

	clock.set(startedAt.Add(6 * time.Minute))
	response, decisionError := runtime.pick(claudeRequest(candidate("auth-a", 0), candidate("auth-b", 10)))
	if decisionError != nil || response.AuthID != "auth-b" {
		t.Fatalf("response = %#v, error = %#v", response, decisionError)
	}
	waitFor(t, func() bool { return fetcher.callCount() == 3 })
	if calls := fetcher.callTokens(); len(calls) != 3 || calls[0] != "token-a" || calls[1] != "token-b" || calls[2] != "token-b" {
		t.Fatalf("fetch calls = %#v, want startup auth-a/auth-b then selected auth-b", calls)
	}
	if sample := runtime.cache.snapshot("auth-b"); !sample.blocked(clock.now(), cfg.CutoffPercentUsed) {
		t.Fatalf("selected auth did not refresh to blocked state: %#v", sample)
	}
}

func TestBlockedAuthSleepsUntilReset(t *testing.T) {
	startedAt := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	clock := &testClock{value: startedAt}
	host := &fakeHost{
		entries:  []pluginapi.HostAuthFileEntry{physicalEntry("auth-a", "index-a")},
		authJSON: map[string]json.RawMessage{"index-a": credentialJSON("token-a")},
	}
	fetcher := &fakeFetcher{replies: map[string][]fetchReply{
		"token-a": {
			{result: usageResult{WeeklyPercentUsed: 80, ResetAt: startedAt.Add(time.Hour)}},
			{result: usageResult{WeeklyPercentUsed: 5, ResetAt: startedAt.Add(8 * 24 * time.Hour)}},
		},
	}}
	runtime := newPluginRuntime(host, fetcher.fetch, clock.now)
	cfg := defaultPluginConfig()
	cfg.PollInterval = 5 * time.Minute
	runtime.applyConfig(cfg)
	defer runtime.shutdown()
	waitFor(t, func() bool { return fetcher.callCount() == 1 })

	clock.set(startedAt.Add(6 * time.Minute))
	if _, decisionError := runtime.pick(claudeRequest(candidate("auth-a", 0))); decisionError == nil {
		t.Fatal("blocked auth should remain unavailable before reset")
	}
	time.Sleep(25 * time.Millisecond)
	if calls := fetcher.callCount(); calls != 1 {
		t.Fatalf("blocked auth refreshed before reset: %d calls", calls)
	}

	clock.set(startedAt.Add(time.Hour + time.Minute))
	response, decisionError := runtime.pick(claudeRequest(candidate("auth-a", 0)))
	if decisionError != nil || response.AuthID != "auth-a" {
		t.Fatalf("post-reset response = %#v, error = %#v", response, decisionError)
	}
	waitFor(t, func() bool { return fetcher.callCount() == 2 })
	if sample := runtime.cache.snapshot("auth-a"); sample.blocked(clock.now(), cfg.CutoffPercentUsed) {
		t.Fatalf("post-reset refresh remained blocked: %#v", sample)
	}
}

func TestDisableClearsInflightSuccess(t *testing.T) {
	now := time.Now().UTC()
	host := &fakeHost{
		entries:  []pluginapi.HostAuthFileEntry{physicalEntry("auth-a", "index-a")},
		authJSON: map[string]json.RawMessage{"index-a": credentialJSON("token-a")},
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseFetch := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseFetch()
	fetch := func(context.Context, string, time.Duration) (usageResult, string) {
		close(started)
		<-release
		return usageResult{WeeklyPercentUsed: 80, ResetAt: now.Add(time.Hour)}, ""
	}
	runtime := newPluginRuntime(host, fetch, func() time.Time { return now })
	cfg := defaultPluginConfig()
	cfg.PollInterval = time.Hour
	runtime.applyConfig(cfg)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("poll did not start")
	}
	disabled := cfg
	disabled.Enabled = false
	done := make(chan struct{})
	go func() {
		runtime.applyConfig(disabled)
		close(done)
	}()
	waitFor(t, func() bool { return !runtime.loadedConfig().Enabled })
	releaseFetch()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disable did not finish")
	}
	if sample := runtime.cache.snapshot("auth-a"); sample != (quotaSample{}) {
		t.Fatalf("disabled cache retained in-flight success: %#v", sample)
	}
}

func TestTokensAndResponseBodiesDoNotAppearInLogsOrErrors(t *testing.T) {
	const token = "oauth-access-token-MUST-NOT-LEAK"
	const responseBody = "complete-response-body-MUST-NOT-LEAK"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(responseBody))
	}))
	defer server.Close()

	now := time.Now().UTC()
	host := &fakeHost{
		entries:  []pluginapi.HostAuthFileEntry{physicalEntry("auth-a", "index-a")},
		authJSON: map[string]json.RawMessage{"index-a": credentialJSON(token)},
	}
	httpFetcher := newHTTPUsageFetcher(server.URL, server.Client().Transport)
	runtime := newTestRuntime(host, httpFetcher.fetch, now)
	runtime.pollOnce(context.Background(), defaultPluginConfig())

	logs := host.logText()
	if strings.Contains(logs, token) || strings.Contains(logs, responseBody) || strings.Contains(logs, "refresh-must-stay-secret") {
		t.Fatalf("secret appeared in logs: %s", logs)
	}
	if sample := runtime.cache.snapshot("auth-a"); sample.LastErrorCategory != pollErrorServer {
		t.Fatalf("sample = %#v", sample)
	}
}

func TestSchedulerPickPerformsNoHTTPOrAuthCallbacks(t *testing.T) {
	now := time.Now().UTC()
	host := &fakeHost{}
	fetcher := &fakeFetcher{replies: map[string][]fetchReply{}}
	runtime := newTestRuntime(host, fetcher.fetch, now)
	runtime.cache.recordSuccess("auth-a", 10, now.Add(time.Hour), now)

	response, decisionError := runtime.pick(claudeRequest(candidate("auth-a", 0)))
	if decisionError != nil || response.AuthID != "auth-a" {
		t.Fatalf("response = %#v, error = %#v", response, decisionError)
	}
	listCalls, getCalls := host.counts()
	if listCalls != 0 || getCalls != 0 || fetcher.callCount() != 0 {
		t.Fatalf("callbacks during pick: list=%d get=%d fetch=%d", listCalls, getCalls, fetcher.callCount())
	}
}

func TestManagementStatusRouteExposesOnlySchedulerState(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	runtime := newTestRuntime(&fakeHost{}, (&fakeFetcher{}).fetch, now)
	runtime.cache.reconcile([]physicalClaudeAuth{
		{ID: "auth-a", AuthIndex: "index-a", Name: "claude-a.json"},
		{ID: "auth-b", AuthIndex: "index-b", Name: "claude-b.json"},
	})
	runtime.cache.recordSuccess("auth-a", 50, now.Add(2*time.Hour), now)
	runtime.cache.recordFailure("auth-b", pollErrorRateLimited)

	response := runtime.handleManagement(pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   managementStatusFullPath,
	})
	if response.StatusCode != http.StatusOK || response.Headers.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("management response = %#v", response)
	}
	var status cutoffStatusResponse
	if err := json.Unmarshal(response.Body, &status); err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || len(status.ProtectedModels) != 1 || status.ProtectedModels[0] != defaultProtectedModel || status.CutoffPercentUsed != 50 || len(status.Accounts) != 2 {
		t.Fatalf("status = %#v", status)
	}
	blocked := status.Accounts[0]
	if blocked.ID != "auth-a" || blocked.AuthIndex != "index-a" || blocked.Name != "claude-a.json" || !blocked.Known || !blocked.Blocked || blocked.WeeklyPercentUsed == nil || *blocked.WeeklyPercentUsed != 50 || blocked.SampledAt == "" || blocked.ResetAt == "" {
		t.Fatalf("blocked account = %#v", blocked)
	}
	unknown := status.Accounts[1]
	if unknown.ID != "auth-b" || unknown.Known || unknown.Blocked || unknown.WeeklyPercentUsed != nil || unknown.LastErrorCategory != pollErrorRateLimited {
		t.Fatalf("unknown account = %#v", unknown)
	}

	var rawStatus map[string]any
	if err := json.Unmarshal(response.Body, &rawStatus); err != nil {
		t.Fatal(err)
	}
	rawAccounts, ok := rawStatus["accounts"].([]any)
	if !ok {
		t.Fatalf("raw accounts = %#v", rawStatus["accounts"])
	}
	allowedKeys := map[string]bool{
		"id": true, "auth_index": true, "name": true, "known": true, "blocked": true,
		"weekly_percent_used": true, "sampled_at": true, "reset_at": true, "last_error_category": true,
	}
	for _, rawAccount := range rawAccounts {
		account, okAccount := rawAccount.(map[string]any)
		if !okAccount {
			t.Fatalf("raw account = %#v", rawAccount)
		}
		for key := range account {
			if !allowedKeys[key] {
				t.Fatalf("unsafe or undocumented status field %q in %#v", key, account)
			}
		}
	}

	notFound := runtime.handleManagement(pluginapi.ManagementRequest{Method: http.MethodPost, Path: managementStatusFullPath})
	if notFound.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong-method response = %#v", notFound)
	}
}

func TestManagementStatusExpiresStaleBlockedSample(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	runtime := newTestRuntime(&fakeHost{}, (&fakeFetcher{}).fetch, now)
	runtime.cache.reconcile([]physicalClaudeAuth{{ID: "auth-a", AuthIndex: "index-a", Name: "claude-a.json"}})
	runtime.cache.recordSuccess("auth-a", 75, now.Add(-time.Second), now.Add(-time.Hour))
	runtime.cache.recordFailure("auth-a", pollErrorRateLimited)

	response := runtime.handleManagement(pluginapi.ManagementRequest{Method: http.MethodGet, Path: managementStatusFullPath})
	var status cutoffStatusResponse
	if err := json.Unmarshal(response.Body, &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Accounts) != 1 || status.Accounts[0].Known || status.Accounts[0].Blocked || status.Accounts[0].WeeklyPercentUsed != nil || status.Accounts[0].LastErrorCategory != pollErrorRateLimited {
		t.Fatalf("expired status = %#v", status)
	}
	if sample := runtime.cache.snapshot("auth-a"); sample.known(now) || sample.blocked(now, 50) {
		t.Fatalf("expired cache sample = %#v", sample)
	}
}

func TestManagementRegistration(t *testing.T) {
	registration := managementRegistration()
	if len(registration.Routes) != 1 || registration.Routes[0].Method != http.MethodGet || registration.Routes[0].Path != managementStatusRoute {
		t.Fatalf("management registration = %#v", registration)
	}
}

func TestConfigValidationAndRegistrationMetadata(t *testing.T) {
	enabled := true
	cutoff := 42.5
	raw, errMarshal := json.Marshal(lifecycleRequest{ConfigYAML: []byte(
		"enabled: true\nprotected-models: [claude-sonnet-4-6, CLAUDE-FABLE-5, claude-sonnet-4-6]\ncutoff-percent-used: 42.5\npoll-interval: 30s\nrequest-timeout: 2s\n",
	)})
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	cfg, errConfig := decodeLifecycleConfig(raw)
	if errConfig != nil {
		t.Fatal(errConfig)
	}
	if cfg.Enabled != enabled || strings.Join(cfg.ProtectedModels, ",") != "claude-fable-5,claude-sonnet-4-6" || cfg.CutoffPercentUsed != cutoff || cfg.PollInterval != 30*time.Second || cfg.RequestTimeout != 2*time.Second {
		t.Fatalf("config = %#v", cfg)
	}
	defaults, errDefaults := decodeLifecycleConfig([]byte("{}"))
	if errDefaults != nil || len(defaults.ProtectedModels) != 1 || defaults.ProtectedModels[0] != defaultProtectedModel {
		t.Fatalf("default config = %#v, error = %v", defaults, errDefaults)
	}

	for _, configYAML := range []string{
		"protected-models: []\n",
		"protected-models: ['']\n",
		"cutoff-percent-used: 101\n",
		"cutoff-percent-used: -1\n",
		"poll-interval: 0s\n",
		"request-timeout: nope\n",
	} {
		raw, _ := json.Marshal(lifecycleRequest{ConfigYAML: []byte(configYAML)})
		if _, err := decodeLifecycleConfig(raw); err == nil {
			t.Fatalf("config %q unexpectedly succeeded", configYAML)
		}
	}

	registration := pluginRegistration()
	if registration.Metadata.Name != pluginName ||
		registration.Metadata.Version != pluginVersion ||
		registration.Metadata.Author != "Smarty Pants Inc" ||
		registration.Metadata.GitHubRepository != "https://github.com/Smarty-Pants-Inc/cpa-plugin-quota-router" ||
		!registration.Capabilities.Scheduler || !registration.Capabilities.ManagementAPI {
		t.Fatalf("registration = %#v", registration)
	}
	fields := map[string]pluginapi.ConfigFieldType{}
	for _, field := range registration.Metadata.ConfigFields {
		fields[field.Name] = field.Type
	}
	if fields["protected-models"] != pluginapi.ConfigFieldTypeArray ||
		fields["cutoff-percent-used"] != pluginapi.ConfigFieldTypeNumber ||
		fields["poll-interval"] != pluginapi.ConfigFieldTypeString ||
		fields["request-timeout"] != pluginapi.ConfigFieldTypeString {
		t.Fatalf("config fields = %#v", fields)
	}
}
