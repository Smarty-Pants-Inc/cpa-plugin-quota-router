package pluginhost

import (
	"context"
	"fmt"
	"sort"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

// pluginQuiesce owns the actual native call, not just the caller's wait. A
// cancelled ApplyConfig retains this receipt and the library until settlement.
// No timer can make callback-table memory safe to free.
type pluginQuiesce struct {
	done chan struct{}
	err error
}

func (h *Host) awaitQuiesce(ctx context.Context, lp *loadedPlugin) bool {
	if h == nil || lp == nil || lp.client == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return false
	}
	h.mu.Lock()
	op := lp.quiesce
	if op == nil {
		op = &pluginQuiesce{done: make(chan struct{})}
		lp.quiesce = op
		go func() {
			defer close(op.done)
			defer func() {
				if recover() != nil {
					op.err = fmt.Errorf("plugin quiesce panicked")
				}
			}()
			// The guarded client retains active-call custody through the actual
			// return. Caller cancellation only ends the wait below.
			_, op.err = callPlugin[rpcEmptyResponse](context.Background(), lp.client, pluginabi.MethodPluginQuiesce, rpcEmptyResponse{})
		}()
	}
	h.mu.Unlock()
	select {
	case <-ctx.Done():
		return false
	case <-op.done:
		if op.err != nil {
			logQuiesceError(lp.id, op.err)
		}
		// Preserve legacy optional-quiesce compatibility, but a negotiated
		// filter explicitly requires the lifecycle half of the contract.
		return ctx.Err() == nil && (op.err == nil || (lp.plugin.Capabilities.SchedulerFilter == nil && quiesceUnsupported(op.err)))
	}
}

func (h *Host) canResumePlugin(lp *loadedPlugin) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, loading := h.loading[lp.id]; loading && lp.registered {
		return false
	}
	if lp.quiesce == nil {
		return true
	}
	select {
	case <-lp.quiesce.done:
		return lp.quiesce.err == nil || (lp.plugin.Capabilities.SchedulerFilter == nil && quiesceUnsupported(lp.quiesce.err))
	default:
		return false
	}
}

func (h *Host) deactivatePlugin(id string) {
	h.mu.Lock()
	records, enabled := h.snapshotWithoutPluginLocked(id)
	h.removePluginRuntimeStateLocked(id)
	h.rebuildActivePluginMapsLocked(records)
	h.snapshot.Store(&Snapshot{enabled: enabled, records: records, quotaSupportedProviders: make(map[string][]string)})
	h.mu.Unlock()
	h.refreshThinkingProviders(records)
}

func (h *Host) quiesceInactivePlugins(ctx context.Context, active map[string]bool) bool {
	h.mu.Lock()
	var inactive []*loadedPlugin
	for id, lp := range h.loaded {
		if !active[id] {
			inactive = append(inactive, lp)
		}
	}
	h.mu.Unlock()
	sort.Slice(inactive, func(i, j int) bool { return inactive[i].id < inactive[j].id })
	// Remove every disabled record before waiting for any callback.
	for _, lp := range inactive {
		h.deactivatePlugin(lp.id)
	}
	settled := true
	for _, lp := range inactive {
		if !h.callQuiesce(ctx, lp) {
			settled = false
		}
	}
	return settled
}
