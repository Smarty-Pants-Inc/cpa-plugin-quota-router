package main

import (
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func negotiatedLifecycle(t *testing.T, config string) []byte {
	t.Helper()
	raw, err := json.Marshal(lifecycleRequest{SchemaVersion: 6, ConfigYAML: []byte(config), HostFeatures: []string{schedulerFilterV1}})
	if err != nil { t.Fatal(err) }
	return raw
}

func useRPCRuntime(t *testing.T, r *pluginRuntime) {
	t.Helper()
	previous := activeRuntime
	activeRuntime = r
	t.Cleanup(func() { r.shutdown(); activeRuntime = previous })
}

func rpcEnvelope(t *testing.T, method string, request []byte) envelope {
	t.Helper()
	raw, err := handleMethod(method, request)
	var result envelope
	if err != nil || json.Unmarshal(raw, &result) != nil { t.Fatalf("%s: response=%s error=%v", method, raw, err) }
	return result
}

func rpcExclusions(t *testing.T, req filterRequest, want ...string) {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil { t.Fatal(err) }
	result := rpcEnvelope(t, "scheduler.filter", raw)
	var response filterResponse
	var fields map[string]json.RawMessage
	if !result.OK || json.Unmarshal(result.Result, &response) != nil || !slices.Equal(response.ExcludedIDs, want) {
		t.Fatalf("filter response=%s error=%#v; want %v", result.Result, result.Error, want)
	}
	if json.Unmarshal(result.Result, &fields) != nil || len(fields) != 1 || fields["excluded_ids"] == nil {
		t.Fatalf("filter returned selection or non-contract fields: %s", result.Result)
	}
}

func TestFilterRPCRefusesBeforeWorkerOrCallbacks(t *testing.T) {
	for _, request := range []string{``, `null`, `{}`, `{"host_features":[]}`, `{"host_features":["scheduler_filter_v2"]}`, `{"host_features":["scheduler"]}`} {
		t.Run(request, func(t *testing.T) {
			host := &fakeHost{}
			fetcher := &fakeFetcher{}
			r := newPluginRuntime(host, fetcher.fetch, time.Now)
			useRPCRuntime(t, r)
			for _, method := range []string{"plugin.register", "plugin.reconfigure", "scheduler.filter"} {
				result := rpcEnvelope(t, method, []byte(request))
				if result.OK || result.Error == nil || result.Error.Code != "unsupported_host" { t.Fatalf("%s accepted: %#v", method, result) }
			}
			if r.done != nil || r.cancel != nil || r.filterNegotiated.Load() { t.Fatal("unsupported host started a worker") }
			if lists, gets := host.counts(); lists != 0 || gets != 0 || fetcher.callCount() != 0 || host.logText() != "" {
				t.Fatal("unsupported host caused callbacks or provider work")
			}
		})
	}
}

func TestFilterRPCUsesRealWorkerAndNeverSelects(t *testing.T) {
	start := time.Date(2026, time.September, 16, 0, 0, 0, 0, time.UTC)
	clock := &testClock{value: start}
	host := &fakeHost{
		entries: []pluginapi.HostAuthFileEntry{physicalEntry("a", "ia"), physicalEntry("b", "ib"), physicalEntry("not-offered", "ic")},
		authJSON: map[string]json.RawMessage{"ia": credentialJSON("ta"), "ib": credentialJSON("tb"), "ic": credentialJSON("tc")},
	}
	fetcher := &fakeFetcher{replies: map[string][]fetchReply{
		"ta": {{result: usageResult{WeeklyPercentUsed: 80, ResetAt: start.Add(time.Hour)}}},
		"tb": {{result: usageResult{WeeklyPercentUsed: 10, ResetAt: start.Add(time.Hour)}}},
		"tc": {{result: usageResult{WeeklyPercentUsed: 80, ResetAt: start.Add(time.Hour)}}},
		"replacement": {{result: usageResult{WeeklyPercentUsed: 5, ResetAt: start.Add(time.Hour)}}},
	}}
	r := newPluginRuntime(host, fetcher.fetch, clock.now)
	useRPCRuntime(t, r)
	registration := rpcEnvelope(t, "plugin.register", negotiatedLifecycle(t, ""))
	var capabilities struct { Capabilities map[string]bool `json:"capabilities"` }
	if !registration.OK || json.Unmarshal(registration.Result, &capabilities) != nil || !capabilities.Capabilities[schedulerFilterV1] || capabilities.Capabilities["scheduler"] {
		t.Fatalf("not a filter-only registration: %s", registration.Result)
	}
	waitFor(t, func() bool { return r.cache.snapshot("not-offered").HasSample })
	waitForRefreshIdle(t, r)
	worker := r.done
	if result := rpcEnvelope(t, "plugin.reconfigure", negotiatedLifecycle(t, "")); !result.OK || r.done != worker { t.Fatal("reconfigure duplicated worker") }
	rpcExclusions(t, claudeRequest(candidate("a"), candidate("b"), candidate("unknown")), "a")
	rpcExclusions(t, claudeRequest(candidate("b"))) // Blocked unoffered IDs stay absent.
	rpcExclusions(t, filterRequest{Model: defaultProtectedModel, Candidates: []filterCandidate{{ID: "a", Provider: "codex"}, candidate("b")}})
	rpcExclusions(t, claudeModelRequest(defaultProtectedModel+"(high)", candidate("a")))
	if result := rpcEnvelope(t, "scheduler.pick", []byte(`{}`)); result.OK || result.Error.Code != "unknown_method" { t.Fatal("legacy picker is still callable") }
	for _, raw := range []string{
		`{"model":"claude-fable-5","candidates":[{"id":"a","provider":"claude"},{"id":"a","provider":"claude"}]}`,
		`{"model":"claude-fable-5","candidates":[{"id":"a","provider":"claude"},{"id":" a ","provider":"claude"}]}`,
	} {
		if result := rpcEnvelope(t, "scheduler.filter", []byte(raw)); result.OK || result.Error.Code != "invalid_candidates" { t.Fatal("invalid set accepted") }
	}
	// Request-driven recovery must go through the real RPC and existing worker.
	host.mu.Lock()
	host.entries[0].ModTime = start.Add(time.Minute)
	host.authJSON["ia"] = credentialJSON("replacement")
	host.mu.Unlock()
	clock.set(start.Add(defaultPluginConfig().PollInterval))
	rpcExclusions(t, claudeRequest(candidate("a")), "a")
	waitFor(t, func() bool { return r.cache.snapshot("a").WeeklyPercentUsed == 5 })
	waitForRefreshIdle(t, r)
	rpcExclusions(t, claudeRequest(candidate("a")))
	if fetcher.callCount() != 4 { t.Fatalf("unexpected provider sweep: %v", fetcher.callTokens()) }
	clock.set(start.Add(2*defaultPluginConfig().PollInterval))
	rpcExclusions(t, claudeRequest(candidate("b"), candidate("a")))
	waitFor(t, func() bool { return fetcher.callCount() == 6 })
	waitForRefreshIdle(t, r)
	if r.cache.snapshot("a").LastAttemptAt != clock.now() || r.cache.snapshot("b").LastAttemptAt != clock.now() {
		t.Fatal("filter refreshed a chosen account instead of both due offers")
	}
}

func TestFilterRPCLifecycleRetainsCallbackUntilPhysicalJoin(t *testing.T) {
	for _, action := range []string{"quiesce", "disable", "feature-loss"} {
		t.Run(action, func(t *testing.T) {
			host := &heldDiscoveryHost{fakeHost: &fakeHost{}, entered: make(chan struct{}), release: make(chan struct{})}
			var once sync.Once
			release := func() { once.Do(func() { close(host.release) }) }
			r := newPluginRuntime(host, (&fakeFetcher{}).fetch, time.Now)
			useRPCRuntime(t, r)
			defer release()
			if result := rpcEnvelope(t, "plugin.register", negotiatedLifecycle(t, "")); !result.OK { t.Fatal(result.Error) }
			select { case <-host.entered: case <-time.After(time.Second): t.Fatal("worker did not enter callback") }
			method, request := "plugin.quiesce", []byte(`{}`)
			if action == "disable" { method, request = "plugin.reconfigure", negotiatedLifecycle(t, "enabled: false\n") }
			if action == "feature-loss" { method = "plugin.reconfigure" }
			done := make(chan []byte, 1)
			go func() { raw, _ := handleMethod(method, request); done <- raw }()
			waitFor(t, r.quiesced.Load)
			// The host may cancel its wait, but the native call must retain
			// this worker/callback until actual completion. No early receipt.
			select { case <-done: t.Fatal("lifecycle returned before callback joined"); default: }
			release()
			select {
			case raw := <-done:
				var result envelope
				if json.Unmarshal(raw, &result) != nil || result.OK != (action != "feature-loss") { t.Fatalf("lifecycle result=%s", raw) }
			case <-time.After(time.Second): t.Fatal("lifecycle failed to join")
			}
			if r.done != nil || r.cancel != nil { t.Fatal("worker ownership retained after actual join") }
			// Swap only after join; the next register represents explicit host
			// resume/replacement admission, never a late automatic rollback.
			resumedHost := &fakeHost{}
			r.host = resumedHost
			if result := rpcEnvelope(t, "plugin.reconfigure", negotiatedLifecycle(t, "")); !result.OK { t.Fatal(result.Error) }
			waitFor(t, func() bool { lists, _ := resumedHost.counts(); return lists == 1 })
			if r.done == nil || r.quiesced.Load() { t.Fatal("explicit negotiated resume failed") }
		})
	}
}
