package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func waitForRefreshIdle(t *testing.T, runtime *pluginRuntime) {
	t.Helper()
	waitFor(t, func() bool {
		runtime.refreshMu.Lock()
		defer runtime.refreshMu.Unlock()
		return !runtime.pendingDiscovery && !runtime.inFlightDiscovery && len(runtime.pendingIDs) == 0 && len(runtime.inFlightIDs) == 0
	})
}

func TestBlockedRequestDiscoversReplacement(t *testing.T) {
	startedAt := time.Date(2026, time.September, 16, 0, 0, 0, 0, time.UTC)
	clock := &testClock{value: startedAt}
	host := &fakeHost{
		entries:  []pluginapi.HostAuthFileEntry{physicalEntry("auth-a", "index-a")},
		authJSON: map[string]json.RawMessage{"index-a": credentialJSON("old-token")},
	}
	fetcher := &fakeFetcher{replies: map[string][]fetchReply{
		"old-token": {{result: usageResult{WeeklyPercentUsed: 80, ResetAt: startedAt.Add(time.Hour)}}},
		"new-token": {{result: usageResult{WeeklyPercentUsed: 10, ResetAt: startedAt.Add(time.Hour)}}},
	}}
	runtime := newPluginRuntime(host, fetcher.fetch, clock.now)
	cfg := defaultPluginConfig()
	runtime.applyConfig(cfg)
	defer runtime.shutdown()
	waitFor(t, func() bool { return runtime.cache.isBlocked("auth-a", clock.now(), cfg.CutoffPercentUsed) })
	waitForRefreshIdle(t, runtime)

	host.mu.Lock()
	host.entries[0].ModTime = startedAt.Add(time.Minute)
	host.authJSON["index-a"] = credentialJSON("new-token")
	host.mu.Unlock()
	clock.set(startedAt.Add(cfg.PollInterval))

	// The current request still uses the cached decision. Recovery must come
	// from the filter's worker trigger, not a test-only call to pollOnce.
	assertExclusions(t, runtime, claudeRequest(candidate("auth-a")), "auth-a")
	waitFor(t, func() bool { return fetcher.callCount() == 2 })
	waitForRefreshIdle(t, runtime)
	assertExclusions(t, runtime, claudeRequest(candidate("auth-a")))
	if listCalls, _ := host.counts(); listCalls != 2 {
		t.Fatalf("discovery calls = %d, want startup plus one request-triggered pass", listCalls)
	}
}

func TestUnsupportedRequestsRetainDiscoveryThrottle(t *testing.T) {
	startedAt := time.Date(2026, time.September, 16, 0, 0, 0, 0, time.UTC)
	clock := &testClock{value: startedAt}
	host := &fakeHost{}
	fetcher := &fakeFetcher{}
	runtime := newPluginRuntime(host, fetcher.fetch, clock.now)
	cfg := defaultPluginConfig()
	runtime.applyConfig(cfg)
	defer runtime.shutdown()
	waitFor(t, func() bool { calls, _ := host.counts(); return calls == 1 })
	waitForRefreshIdle(t, runtime)

	for pass := range 2 {
		clock.set(startedAt.Add(time.Duration(pass+1) * cfg.PollInterval))
		for range 5 {
			assertExclusions(t, runtime, claudeRequest(candidate("runtime-only")))
			waitForRefreshIdle(t, runtime)
		}
		if calls, gets := host.counts(); calls != pass+2 || gets != 0 {
			t.Fatalf("callbacks in interval %d: list=%d get=%d; want list=%d get=0", pass, calls, gets, pass+2)
		}
		if !runtime.cache.empty() || fetcher.callCount() != 0 {
			t.Fatal("unsupported candidate created quota state or provider calls")
		}
	}
}

func TestUnprotectedRequestsDoNotDiscover(t *testing.T) {
	startedAt := time.Date(2026, time.September, 16, 0, 0, 0, 0, time.UTC)
	clock := &testClock{value: startedAt}
	host := &fakeHost{}
	runtime := newPluginRuntime(host, (&fakeFetcher{}).fetch, clock.now)
	cfg := defaultPluginConfig()
	runtime.applyConfig(cfg)
	defer runtime.shutdown()
	waitFor(t, func() bool { calls, _ := host.counts(); return calls == 1 })
	waitForRefreshIdle(t, runtime)
	clock.set(startedAt.Add(cfg.PollInterval))
	for _, request := range []filterRequest{
		claudeModelRequest(defaultProtectedModel+"(high)", candidate("unknown")),
		claudeModelRequest("unconfigured-alias", candidate("unknown")),
		{Model: defaultProtectedModel, Candidates: []filterCandidate{{ID: "unknown", Provider: "codex"}}},
	} {
		assertExclusions(t, runtime, request)
	}
	waitForRefreshIdle(t, runtime)
	if calls, gets := host.counts(); calls != 1 || gets != 0 {
		t.Fatalf("unprotected callbacks: list=%d get=%d", calls, gets)
	}
}

func TestDiscoveryFailureIsThrottledAcrossPruning(t *testing.T) {
	startedAt := time.Date(2026, time.September, 16, 0, 0, 0, 0, time.UTC)
	clock := &testClock{value: startedAt}
	host := &fakeHost{listError: errors.New("private-path-and-host-error")}
	runtime := newPluginRuntime(host, (&fakeFetcher{}).fetch, clock.now)
	cfg := defaultPluginConfig()
	runtime.applyConfig(cfg)
	defer runtime.shutdown()
	waitFor(t, func() bool { calls, _ := host.counts(); return calls == 1 })
	waitForRefreshIdle(t, runtime)
	for range 5 {
		runtime.cache.reconcile(nil)
		_, _ = runtime.filter(claudeRequest(candidate("unsupported")))
		waitForRefreshIdle(t, runtime)
	}
	if calls, _ := host.counts(); calls != 1 {
		t.Fatalf("failed discovery retried before interval: %d calls", calls)
	}
	status := runtime.discoveryStatus()
	if status.LastAttemptAt != statusTime(startedAt) || status.LastSuccessAt != "" || status.LastErrorCategory != "auth_list" {
		t.Fatalf("failed discovery status = %#v", status)
	}
	if strings.Contains(host.logText(), "private-path") {
		t.Fatal("discovery exposed a raw host error")
	}
	host.mu.Lock()
	host.listError = nil
	host.mu.Unlock()
	clock.set(startedAt.Add(cfg.PollInterval))
	_, _ = runtime.filter(claudeRequest(candidate("unsupported")))
	waitForRefreshIdle(t, runtime)
	status = runtime.discoveryStatus()
	if status.LastSuccessAt != statusTime(clock.now()) || status.LastErrorCategory != "" {
		t.Fatalf("discovery did not recover: %#v", status)
	}
}

func TestPollResultCannotOverwriteObservedRevision(t *testing.T) {
	now := time.Date(2026, time.September, 16, 0, 0, 0, 0, time.UTC)
	oldEntry := physicalEntry("auth-a", "index-a")
	newEntry := oldEntry
	newEntry.ModTime = now
	host := &fakeHost{authJSON: map[string]json.RawMessage{"index-a": credentialJSON("old-token")}}
	var runtime *pluginRuntime
	runtime = newTestRuntime(host, func(context.Context, string, time.Duration) (usageResult, string) {
		runtime.cache.reconcile(physicalClaudeAuths([]pluginapi.HostAuthFileEntry{newEntry}))
		return usageResult{WeeklyPercentUsed: 80, ResetAt: now.Add(time.Hour)}, ""
	}, now)
	auths := physicalClaudeAuths([]pluginapi.HostAuthFileEntry{oldEntry})
	runtime.cache.reconcile(auths)
	runtime.pollAuth(context.Background(), auths[0], defaultPluginConfig())
	if sample := runtime.cache.snapshot("auth-a"); sample.HasSample || sample.CredentialRevision != physicalAuthRevision(newEntry) {
		t.Fatalf("stale result overwrote observed replacement: %#v", sample)
	}
}

func TestDiscoveryDoesNotRefreshUnselectedUnchangedAccounts(t *testing.T) {
	startedAt := time.Date(2026, time.September, 16, 0, 0, 0, 0, time.UTC)
	clock := &testClock{value: startedAt}
	host := &fakeHost{
		entries: []pluginapi.HostAuthFileEntry{physicalEntry("blocked", "index-a"), physicalEntry("unselected", "index-b")},
		authJSON: map[string]json.RawMessage{"index-a": credentialJSON("token-a"), "index-b": credentialJSON("token-b")},
	}
	fetcher := &fakeFetcher{replies: map[string][]fetchReply{
		"token-a": {{result: usageResult{WeeklyPercentUsed: 80, ResetAt: startedAt.Add(time.Hour)}}},
		"token-b": {{result: usageResult{WeeklyPercentUsed: 10, ResetAt: startedAt.Add(time.Hour)}}},
	}}
	runtime := newPluginRuntime(host, fetcher.fetch, clock.now)
	cfg := defaultPluginConfig()
	runtime.applyConfig(cfg)
	defer runtime.shutdown()
	waitFor(t, func() bool { return fetcher.callCount() == 2 })
	waitForRefreshIdle(t, runtime)
	clock.set(startedAt.Add(cfg.PollInterval))
	_, _ = runtime.filter(claudeRequest(candidate("blocked")))
	waitForRefreshIdle(t, runtime)
	if calls, gets := host.counts(); calls != 2 || gets != 2 || fetcher.callCount() != 2 {
		t.Fatalf("metadata pass became provider sweep: list=%d get=%d fetch=%d", calls, gets, fetcher.callCount())
	}
}

func TestConcurrentBlockedRequestsCoalesceDiscovery(t *testing.T) {
	startedAt := time.Date(2026, time.September, 16, 0, 0, 0, 0, time.UTC)
	clock := &testClock{value: startedAt}
	host := &fakeHost{
		entries:  []pluginapi.HostAuthFileEntry{physicalEntry("auth-a", "index-a")},
		authJSON: map[string]json.RawMessage{"index-a": credentialJSON("token-a")},
	}
	fetcher := &fakeFetcher{replies: map[string][]fetchReply{
		"token-a": {{result: usageResult{WeeklyPercentUsed: 80, ResetAt: startedAt.Add(time.Hour)}}},
	}}
	runtime := newPluginRuntime(host, fetcher.fetch, clock.now)
	cfg := defaultPluginConfig()
	runtime.applyConfig(cfg)
	defer runtime.shutdown()
	waitFor(t, func() bool { return runtime.cache.isBlocked("auth-a", clock.now(), cfg.CutoffPercentUsed) })
	waitForRefreshIdle(t, runtime)
	clock.set(startedAt.Add(cfg.PollInterval))
	var requests sync.WaitGroup
	for range 32 {
		requests.Go(func() { _, _ = runtime.filter(claudeRequest(candidate("auth-a"))) })
	}
	requests.Wait()
	waitForRefreshIdle(t, runtime)
	if calls, gets := host.counts(); calls != 2 || gets != 1 || fetcher.callCount() != 1 {
		t.Fatalf("unchanged block: list=%d get=%d fetch=%d; want 2/1/1", calls, gets, fetcher.callCount())
	}
}
