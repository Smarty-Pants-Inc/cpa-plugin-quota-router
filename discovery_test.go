package main

import (
	"encoding/json"
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
		return !runtime.pendingAll && !runtime.inFlightAll && len(runtime.pendingIDs) == 0 && len(runtime.inFlightIDs) == 0
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
	// from pick's worker trigger, not a test-only call to pollOnce.
	if _, decisionError := runtime.pick(claudeRequest(candidate("auth-a", 0))); decisionError == nil {
		t.Fatal("first request must retain the cached block")
	}
	waitFor(t, func() bool { return fetcher.callCount() == 2 })
	waitForRefreshIdle(t, runtime)
	response, decisionError := runtime.pick(claudeRequest(candidate("auth-a", 0)))
	if decisionError != nil || response.AuthID != "auth-a" {
		t.Fatalf("replacement did not recover: response=%#v error=%#v", response, decisionError)
	}
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
			response, decisionError := runtime.pick(claudeRequest(candidate("runtime-only", 0)))
			if decisionError != nil || response.AuthID != "runtime-only" {
				t.Fatalf("unsupported candidate must fail open: response=%#v error=%#v", response, decisionError)
			}
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
		requests.Go(func() { _, _ = runtime.pick(claudeRequest(candidate("auth-a", 0))) })
	}
	requests.Wait()
	waitForRefreshIdle(t, runtime)
	if calls, gets := host.counts(); calls != 2 || gets != 1 || fetcher.callCount() != 1 {
		t.Fatalf("unchanged block: list=%d get=%d fetch=%d; want 2/1/1", calls, gets, fetcher.callCount())
	}
}
