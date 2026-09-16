package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func waitForNoPluginLoad(t *testing.T, h *Host, id string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		h.mu.Lock(); _, loading := h.loading[id]; h.mu.Unlock()
		if !loading { return }
		select {
		case <-deadline.C: t.Fatal("plugin load cleanup did not settle")
		default: runtime.Gosched()
		}
	}
}

func filterTestPlugin() pluginapi.Plugin {
	plugin := validTestPlugin("alpha")
	plugin.Capabilities.SchedulerFilter = filterFunc(nil)
	return plugin
}

func TestGuardedLifecycleCancellationDoesNotReleaseNativeOwnership(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var quiesces atomic.Int32
	inner := &lifecycleTestClient{call: func(_ context.Context, method string, _ []byte) ([]byte, error) {
		if method == pluginabi.MethodPluginReconfigure { close(entered); <-release } else { quiesces.Add(1) }
		return marshalRPCResult(rpcEmptyResponse{})
	}}
	client := newGuardedPluginClient(inner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan struct{})
	go func() { _, _ = client.Call(ctx, pluginabi.MethodPluginReconfigure, nil); close(returned) }()
	waitForHostTestSignal(t, entered, "native reconfigure")
	cancel()
	waitForHostTestSignal(t, returned, "cancelled reconfigure caller")
	cancelled, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	if _, err := client.Call(cancelled, pluginabi.MethodPluginQuiesce, nil); err == nil { t.Fatal("cancelled lifecycle call succeeded") }
	if quiesces.Load() != 0 { t.Fatal("second lifecycle call overlapped native reconfigure") }
	unblock()
	if _, err := client.Call(context.Background(), pluginabi.MethodPluginQuiesce, nil); err != nil { t.Fatal(err) }
	client.Shutdown()
	if quiesces.Load() != 1 || inner.shutdownCalls.Load() != 1 { t.Fatal("settled lifecycle or shutdown lost") }
}

func TestHostFilterDisableQuiescesAndExplicitReenableResumes(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint("global=", global), func(t *testing.T) {
			events := &lifecycleEventRecorder{}
			client := &lifecycleTestClient{call: func(_ context.Context, method string, _ []byte) ([]byte, error) {
				events.add(method)
				if method == pluginabi.MethodPluginQuiesce { return marshalRPCResult(rpcEmptyResponse{}) }
				return lifecycleRegistrationResult(filterTestPlugin())
			}}
			h := NewForTest(&sequencePluginLoader{clients: []pluginClient{client}})
			t.Cleanup(h.ShutdownAll)
			dir, _ := makeVersionedPluginDir(t, "alpha", "1.0.0")
			h.ApplyConfig(context.Background(), versionedPluginHostConfig(t, dir, "1.0.0"))
			cfg := versionedPluginHostConfig(t, dir, "1.0.0")
			if global { cfg.Plugins.Enabled = false } else { item := cfg.Plugins.Configs["alpha"]; disabled := false; item.Enabled = &disabled; cfg.Plugins.Configs["alpha"] = item }
			h.ApplyConfig(context.Background(), cfg)
			if h.HasSchedulerFilter() || h.PluginRegistered("alpha") { t.Fatal("disabled filter remained active") }
			if !h.PluginLoaded("alpha") { t.Fatal("disabled library custody was lost") }
			h.ApplyConfig(context.Background(), cfg)
			if got := events.snapshot(); len(got) != 2 || got[1] != pluginabi.MethodPluginQuiesce { t.Fatalf("disable quiesce missing/duplicated: %v", got) }
			h.ApplyConfig(context.Background(), versionedPluginHostConfig(t, dir, "1.0.0"))
			if !h.HasSchedulerFilter() { t.Fatal("explicit reconfigure did not resume") }
			if got := events.snapshot(); len(got) != 3 || got[2] != pluginabi.MethodPluginReconfigure { t.Fatalf("resume events=%v", got) }
		})
	}
}

func TestHostFilterFailedQuiescePreventsReplacement(t *testing.T) {
	for _, kind := range []string{"error", "unsupported", "panic"} {
		t.Run(kind, func(t *testing.T) {
			var registrations atomic.Int32
			old := &lifecycleTestClient{call: func(_ context.Context, method string, _ []byte) ([]byte, error) {
				if method == pluginabi.MethodPluginQuiesce {
					switch kind {
					case "unsupported": return marshalRPCError("unknown_method", "unsupported"), nil
					case "panic": panic("fixture")
					default: return nil, errors.New("fixture quiesce failed")
					}
				}
				registrations.Add(1)
				return lifecycleRegistrationResult(filterTestPlugin())
			}}
			replacement := &lifecycleTestClient{call: func(context.Context, string, []byte) ([]byte, error) { t.Error("replacement started after failed quiesce"); return lifecycleRegistrationResult(filterTestPlugin()) }}
			h := NewForTest(&sequencePluginLoader{clients: []pluginClient{old, replacement}})
			t.Cleanup(h.ShutdownAll)
			dir, _ := makeVersionedPluginDir(t, "alpha", "1.0.0")
			h.ApplyConfig(context.Background(), versionedPluginHostConfig(t, dir, "1.0.0"))
			writeVersionedPluginFile(t, dir, "alpha", "2.0.0")
			h.ApplyConfig(context.Background(), versionedPluginHostConfig(t, dir, "2.0.0"))
			h.ApplyConfig(context.Background(), versionedPluginHostConfig(t, dir, "1.0.0"))
			if registrations.Load() != 1 || h.PluginRegistered("alpha") { t.Fatal("uncertain old worker was resumed or activated") }
			if !h.HasSchedulerFilter() { t.Fatal("failed required filter silently became optional") }
			if _, err := h.FilterAuths(context.Background(), pluginapi.SchedulerFilterRequest{}); err == nil { t.Fatal("failed filter permitted routing") }
			if old.shutdownCalls.Load() != 0 { t.Fatal("callback custody freed before explicit shutdown") }
		})
	}
}

func TestHostFilterCancelledQuiesceRetainsOneCallAndCallbackCustody(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var quiesces, registrations atomic.Int32
	old := &lifecycleTestClient{call: func(_ context.Context, method string, _ []byte) ([]byte, error) {
		if method == pluginabi.MethodPluginQuiesce {
			quiesces.Add(1)
			close(entered)
			<-release // Models a native host callback with no cancellation API.
			return marshalRPCResult(rpcEmptyResponse{})
		}
		registrations.Add(1)
		return lifecycleRegistrationResult(filterTestPlugin())
	}}
	h := NewForTest(&sequencePluginLoader{clients: []pluginClient{old}})
	t.Cleanup(h.ShutdownAll)
	t.Cleanup(unblock)
	dir, _ := makeVersionedPluginDir(t, "alpha", "1.0.0")
	h.ApplyConfig(context.Background(), versionedPluginHostConfig(t, dir, "1.0.0"))
	writeVersionedPluginFile(t, dir, "alpha", "2.0.0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { h.ApplyConfig(ctx, versionedPluginHostConfig(t, dir, "2.0.0")); close(done) }()
	waitForHostTestSignal(t, entered, "quiesce entry")
	cancel()
	waitForHostTestSignal(t, done, "cancelled caller")
	h.ApplyConfig(context.Background(), versionedPluginHostConfig(t, dir, "1.0.0"))
	if quiesces.Load() != 1 || registrations.Load() != 1 || old.shutdownCalls.Load() != 0 { t.Fatal("cancelled call lost or duplicated ownership") }
	h.mu.Lock(); op := h.loaded["alpha"].quiesce; h.mu.Unlock()
	unblock()
	waitForHostTestSignal(t, op.done, "actual quiesce settlement")
	if registrations.Load() != 1 { t.Fatal("late settlement auto-resumed an obsolete request") }
	h.ApplyConfig(context.Background(), versionedPluginHostConfig(t, dir, "1.0.0"))
	if registrations.Load() != 2 || !h.PluginRegistered("alpha") { t.Fatal("explicit settled resume failed") }
}
