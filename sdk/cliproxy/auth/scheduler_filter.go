package auth

import (
	"context"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func activeSchedulerFilter(scheduler PluginScheduler) PluginSchedulerFilter {
	filter, ok := scheduler.(PluginSchedulerFilter)
	if !ok || !filter.HasSchedulerFilter() {
		return nil
	}
	return filter
}

// filterAvailableAuths runs outside Manager.mu. Input is a cloned, host-admitted
// across-priority snapshot. The context restriction also reaches native builtin
// delegation and its resync/retry, so neither can resurrect an excluded account.
func filterAvailableAuths(ctx context.Context, filter PluginSchedulerFilter, selector Selector, model string, auths []*Auth) (context.Context, []*Auth, []*Auth, error) {
	req := pluginapi.SchedulerFilterRequest{Model: model, Candidates: make([]pluginapi.SchedulerFilterCandidate, 0, len(auths))}
	for _, auth := range auths {
		req.Candidates = append(req.Candidates, pluginapi.SchedulerFilterCandidate{ID: auth.ID, Provider: executorKeyFromAuth(auth)})
	}
	callReq := req
	callReq.Candidates = append([]pluginapi.SchedulerFilterCandidate(nil), req.Candidates...)
	resp, err := filter.FilterAuths(ctx, callReq)
	if err != nil {
		return ctx, nil, nil, err
	}
	excluded, err := pluginapi.ValidateSchedulerExclusions(req, resp)
	if err != nil {
		return ctx, nil, nil, err
	}
	remaining := make([]*Auth, 0, len(auths))
	allowed := make(map[string]struct{}, len(auths))
	for _, auth := range auths {
		if _, blocked := excluded[auth.ID]; !blocked {
			remaining = append(remaining, auth)
			allowed[auth.ID] = struct{}{}
		}
	}
	if len(remaining) == 0 {
		return ctx, nil, nil, &Error{Code: "quota_router_exhausted", Message: "quota_router_exhausted", HTTPStatus: http.StatusServiceUnavailable}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, filteredAuthIDsContextKey{}, allowed)
	priority := highestPriorityAuths(remaining)
	if _, affinity := selector.(*SessionAffinitySelector); affinity {
		return ctx, priority, remaining, nil
	}
	return ctx, priority, priority, nil
}
