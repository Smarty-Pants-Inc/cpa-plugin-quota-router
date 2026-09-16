package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type heldDiscoveryHost struct {
	*fakeHost
	entered chan struct{}
	release chan struct{}
}

func (h *heldDiscoveryHost) listAuth() ([]pluginapi.HostAuthFileEntry, error) {
	close(h.entered)
	<-h.release
	return h.fakeHost.listAuth()
}

func TestQuiesceJoinsHostCallbackAndStopsAdmission(t *testing.T) {
	host := &heldDiscoveryHost{fakeHost: &fakeHost{}, entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(host.release) }) }
	runtime := newPluginRuntime(host, (&fakeFetcher{}).fetch, time.Now)
	runtime.applyConfig(defaultPluginConfig())
	defer runtime.shutdown()
	defer release()
	select {
	case <-host.entered:
	case <-time.After(time.Second):
		t.Fatal("host callback did not start")
	}

	// A callback stuck in the worker must not turn filtering into host I/O.
	picked := make(chan struct{})
	go func() { _, _ = runtime.filter(claudeRequest(candidate("unknown"))); close(picked) }()
	select {
	case <-picked:
	case <-time.After(time.Second):
		t.Fatal("pick waited for the host callback")
	}
	done := make(chan struct{})
	go func() { runtime.shutdown(); close(done) }()
	waitFor(t, runtime.quiesced.Load)
	runtime.cache.recordSuccess("blocked", 80, time.Now().Add(time.Hour), time.Now())
	assertExclusions(t, runtime, claudeRequest(candidate("blocked")), "blocked")
	select {
	case <-done:
		t.Fatal("quiesce returned while host callback still owned resources")
	default:
	}
	release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("quiesce did not join the released callback")
	}
}

func TestQuiesceRPCResumesOnlyOnExplicitReconfigure(t *testing.T) {
	now := time.Date(2026, time.September, 16, 0, 0, 0, 0, time.UTC)
	host := &fakeHost{
		entries:  []pluginapi.HostAuthFileEntry{physicalEntry("auth-a", "index-a")},
		authJSON: map[string]json.RawMessage{"index-a": credentialJSON("token-a")},
	}
	started, cancelled := make(chan struct{}), make(chan struct{})
	runtime := newTestRuntime(host, func(ctx context.Context, _ string, _ time.Duration) (usageResult, string) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return usageResult{}, pollErrorCancelled
	}, now)
	previous := activeRuntime
	activeRuntime = runtime
	defer func() { runtime.shutdown(); activeRuntime = previous }()
	if _, err := handleMethod("plugin.register", negotiatedLifecycle(t, "")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("provider fixture did not start")
	}
	for range 2 {
		raw, err := handleMethod("plugin.quiesce", []byte("{}"))
		var response envelope
		if err != nil || json.Unmarshal(raw, &response) != nil || !response.OK {
			t.Fatalf("quiesce response=%s error=%v", raw, err)
		}
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("quiesce did not cancel and join provider work")
	}
	if runtime.cancel != nil || runtime.done != nil || !runtime.quiesced.Load() {
		t.Fatal("quiesce retained an active worker")
	}
	if !runtime.loadedConfig().Enabled || runtime.cache.empty() {
		t.Fatal("quiesce discarded rollback configuration or metadata")
	}
	fetcher := &fakeFetcher{}
	runtime.fetch = fetcher.fetch // Worker is joined; no concurrent replacement.
	if _, err := handleMethod("plugin.reconfigure", negotiatedLifecycle(t, "")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { calls, _ := host.counts(); return calls == 2 })
	waitForRefreshIdle(t, runtime)
	if runtime.quiesced.Load() || runtime.cancel == nil {
		t.Fatal("explicit reconfigure did not resume the worker")
	}
	// Resume reconciles metadata, not an unconditional provider sweep.
	if fetcher.callCount() != 0 {
		t.Fatal("resume bypassed the unchanged credential's usage throttle")
	}
}
