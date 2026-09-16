package pluginhost

import (
	"context"
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func (h *Host) schedulerFilterSupported() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runtimeConfig == nil || !h.runtimeConfig.Home.Enabled
}

func (a *rpcPluginAdapter) Filter(ctx context.Context, req pluginapi.SchedulerFilterRequest) (pluginapi.SchedulerFilterResponse, error) {
	return callPlugin[pluginapi.SchedulerFilterResponse](ctx, a.client, pluginabi.MethodSchedulerFilter, req)
}

func (h *Host) HasSchedulerFilter() bool {
	if h == nil {
		return false
	}
	for _, record := range h.activeRecords() {
		if record.plugin.Capabilities.SchedulerFilter != nil {
			return true
		}
	}
	// A desired filter with uncertain lifecycle custody remains a routing
	// requirement even while its active capability record is withdrawn.
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runtimeConfig != nil && h.runtimeConfig.Plugins.Enabled {
		for id, lp := range h.loaded {
			item := h.runtimeConfig.Plugins.Configs[id]
			if item.Enabled != nil && *item.Enabled && lp.plugin.Capabilities.SchedulerFilter != nil {
				return true
			}
		}
	}
	return false
}

// FilterAuths is independent of scheduler precedence. A filter failure must not
// turn into an unhandled scheduler result and silently bypass the restriction.
func (h *Host) FilterAuths(ctx context.Context, req pluginapi.SchedulerFilterRequest) (resp pluginapi.SchedulerFilterResponse, err error) {
	var selected *capabilityRecord
	for _, record := range h.activeRecords() {
		if record.plugin.Capabilities.SchedulerFilter == nil {
			continue
		}
		if selected != nil {
			return resp, fmt.Errorf("multiple scheduler filters are unsupported")
		}
		copyRecord := record
		selected = &copyRecord
	}
	if selected == nil {
		return resp, fmt.Errorf("scheduler filter is no longer active")
	}
	if !h.schedulerFilterSupported() || h.isPluginFused(selected.id) || !h.recordCurrent(*selected) {
		return resp, fmt.Errorf("scheduler filter is unavailable")
	}
	defer func() {
		if recover() != nil {
			// Do not remove the filter from routing: subsequent requests must
			// still fail rather than select without the required restriction.
			resp = pluginapi.SchedulerFilterResponse{}
			err = fmt.Errorf("scheduler filter panicked")
		}
	}()
	callReq := req
	callReq.Candidates = append([]pluginapi.SchedulerFilterCandidate(nil), req.Candidates...)
	resp, err = selected.plugin.Capabilities.SchedulerFilter.Filter(ctx, callReq)
	if err != nil {
		return pluginapi.SchedulerFilterResponse{}, err
	}
	if _, err = pluginapi.ValidateSchedulerExclusions(req, resp); err != nil {
		return pluginapi.SchedulerFilterResponse{}, err
	}
	return resp, nil
}
