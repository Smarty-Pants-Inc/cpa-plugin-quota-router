package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type filterFunc func(context.Context, pluginapi.SchedulerFilterRequest) (pluginapi.SchedulerFilterResponse, error)
func (f filterFunc) Filter(ctx context.Context, req pluginapi.SchedulerFilterRequest) (pluginapi.SchedulerFilterResponse, error) { return f(ctx, req) }

func TestHostSchedulerFilterIndependentOfSchedulerPriority(t *testing.T) {
	calls := 0
	h := newHostWithRecords(
		capabilityRecord{id: "selector", priority: 100, plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{Scheduler: schedulerFunc(func(context.Context, pluginapi.SchedulerPickRequest) (pluginapi.SchedulerPickResponse, error) { return pluginapi.SchedulerPickResponse{}, nil })}}},
		capabilityRecord{id: "quota", priority: 1, plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{SchedulerFilter: filterFunc(func(context.Context, pluginapi.SchedulerFilterRequest) (pluginapi.SchedulerFilterResponse, error) {
			calls++
			return pluginapi.SchedulerFilterResponse{ExcludedIDs: []string{"a"}}, nil
		})}}},
	)
	req := pluginapi.SchedulerFilterRequest{Candidates: []pluginapi.SchedulerFilterCandidate{{ID: "a", Provider: "claude"}}}
	resp, err := h.FilterAuths(context.Background(), req)
	if !h.HasSchedulerFilter() || err != nil || calls != 1 || !slices.Equal(resp.ExcludedIDs, []string{"a"}) { t.Fatalf("response=%v error=%v calls=%d", resp, err, calls) }
	h.fusePlugin("quota", "fixture", "fixture panic")
	if !h.HasSchedulerFilter() { t.Fatal("fused filter stopped being required") }
	if _, err := h.FilterAuths(context.Background(), req); err == nil { t.Fatal("fused filter failed open") }
}

func TestHostSchedulerFilterRejectsInvalidAndMultipleFilters(t *testing.T) {
	for _, ids := range [][]string{{"foreign"}, {"a", "a"}, {"a", "b", "c"}} {
		h := newHostWithRecords(capabilityRecord{id: "quota", plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{SchedulerFilter: filterFunc(func(context.Context, pluginapi.SchedulerFilterRequest) (pluginapi.SchedulerFilterResponse, error) {
			return pluginapi.SchedulerFilterResponse{ExcludedIDs: ids}, nil
		})}}})
		if _, err := h.FilterAuths(context.Background(), pluginapi.SchedulerFilterRequest{Candidates: []pluginapi.SchedulerFilterCandidate{{ID: "a"}, {ID: "b"}}}); err == nil { t.Fatalf("accepted invalid exclusions: %v", ids) }
	}
	filter := filterFunc(func(context.Context, pluginapi.SchedulerFilterRequest) (pluginapi.SchedulerFilterResponse, error) { t.Fatal("unsupported composition invoked a filter"); return pluginapi.SchedulerFilterResponse{}, nil })
	h := newHostWithRecords(capabilityRecord{id: "a", plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{SchedulerFilter: filter}}}, capabilityRecord{id: "b", plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{SchedulerFilter: filter}}})
	if _, err := h.FilterAuths(context.Background(), pluginapi.SchedulerFilterRequest{}); err == nil { t.Fatal("multiple filters accepted") }
}

func TestHostSchedulerFilterNegotiatesBeforePluginStartup(t *testing.T) {
	for _, home := range []bool{false, true} {
		t.Run(fmt.Sprint("home=", home), func(t *testing.T) {
			h := New()
			h.runtimeConfig = &config.Config{Home: config.HomeConfig{Enabled: home}}
			started := false
			client := &lifecycleTestClient{call: func(_ context.Context, method string, raw []byte) ([]byte, error) {
				if method != pluginabi.MethodPluginRegister { t.Fatalf("unexpected method %s", method) }
				var req rpcLifecycleRequest
				if err := json.Unmarshal(raw, &req); err != nil { t.Fatal(err) }
				// Same pre-start requirement as the filter-only plugin consumer.
				if !slices.Contains(req.HostFeatures, pluginapi.SchedulerFilterV1) { return marshalRPCError("unsupported_host", "scheduler_filter_v1 required"), nil }
				started = true
				plugin := validTestPlugin("quota")
				plugin.Capabilities.SchedulerFilter = filterFunc(nil)
				// A nil function still satisfies the non-nil interface capability.
				return lifecycleRegistrationResult(plugin)
			}}
			plugin, err := registerRPCPlugin(context.Background(), h, "quota", client, pluginabi.MethodPluginRegister, nil)
			if home {
				if err == nil || started { t.Fatal("Home started unsupported filter") }
			} else if err != nil || !started || plugin.Capabilities.SchedulerFilter == nil { t.Fatalf("negotiation failed: %v", err) }
		})
	}
}

func TestHostSchedulerFilterAbsentFeatureRefusedBeforeWorkerStart(t *testing.T) {
	started := false
	client := &lifecycleTestClient{call: func(_ context.Context, _ string, raw []byte) ([]byte, error) {
		var req rpcLifecycleRequest
		if err := json.Unmarshal(raw, &req); err != nil { return nil, err }
		if !slices.Contains(req.HostFeatures, pluginapi.SchedulerFilterV1) { return marshalRPCError("unsupported_host", "scheduler_filter_v1 required"), nil }
		started = true
		return lifecycleRegistrationResult(filterTestPlugin())
	}}
	// Missing feature is the old-host wire shape. This is a synthetic consumer,
	// not a claim that the frozen legacy quota plugin implements the new filter.
	if _, err := registerRPCPlugin(context.Background(), nil, "quota", client, pluginabi.MethodPluginRegister, nil); err == nil || started {
		t.Fatal("unnegotiated consumer started")
	}
}

func TestHostSchedulerFilterRPCMalformedResponseIsNotUnhandled(t *testing.T) {
	for _, response := range []string{
		`{"ok":true}`, `{"ok":true,"result":null}`, `{"ok":true,"result":{}}`,
		`{"ok":true,"result":{"other":[]}}`, `{"ok":true,"result":{"excluded_ids":null}}`,
		`{"ok":true,"result":[]}`, `{"ok":true,"result":{"excluded_ids":[1]}}`,
		`{"ok":false,"error":{"code":"unknown_method","message":"unsupported"}}`, `not-json`,
	} {
		adapter := &rpcPluginAdapter{client: &lifecycleTestClient{call: func(_ context.Context, method string, raw []byte) ([]byte, error) {
			if method != pluginabi.MethodSchedulerFilter { t.Fatalf("method = %s", method) }
			var shape map[string]json.RawMessage
			if err := json.Unmarshal(raw, &shape); err != nil { t.Fatal(err) }
			if len(shape) != 2 || shape["model"] == nil || shape["candidates"] == nil { t.Fatalf("unsafe request shape: %s", raw) }
			return []byte(response), nil
		}}}
		h := newHostWithRecords(capabilityRecord{id: "quota", plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{SchedulerFilter: adapter}}})
		if _, err := h.FilterAuths(context.Background(), pluginapi.SchedulerFilterRequest{Model: "exact(high)", Candidates: []pluginapi.SchedulerFilterCandidate{{ID: "a", Provider: "claude"}}}); err == nil {
			t.Fatalf("malformed response became permission to route: %s", response)
		}
	}
}

func TestHostSchedulerFilterRPCExplicitEmptyArray(t *testing.T) {
	adapter := &rpcPluginAdapter{client: &lifecycleTestClient{call: func(context.Context, string, []byte) ([]byte, error) {
		return []byte(`{"ok":true,"result":{"excluded_ids":[]}}`), nil
	}}}
	h := newHostWithRecords(capabilityRecord{id: "quota", plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{SchedulerFilter: adapter}}})
	response, err := h.FilterAuths(context.Background(), pluginapi.SchedulerFilterRequest{Candidates: []pluginapi.SchedulerFilterCandidate{{ID: "a", Provider: "claude"}}})
	if err != nil || response.ExcludedIDs == nil || len(response.ExcludedIDs) != 0 { t.Fatalf("explicit empty rejected: %v %v", response, err) }
}
