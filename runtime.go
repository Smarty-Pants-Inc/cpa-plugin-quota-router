package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type hostClient interface {
	listAuth() ([]pluginapi.HostAuthFileEntry, error)
	getAuth(authIndex string) (json.RawMessage, error)
	log(level, message string, fields map[string]any)
}

type claudeCredential struct {
	Type        string `json:"type"`
	AccessToken string `json:"access_token"`
}

type pluginRuntime struct {
	lifecycleMu       sync.Mutex
	refreshMu         sync.Mutex
	config            atomic.Pointer[pluginConfig]
	quiesced          atomic.Bool
	filterNegotiated  atomic.Bool
	cache             quotaCache
	host              hostClient
	fetch             usageFetcher
	now               func() time.Time
	wake              chan struct{}
	cancel            context.CancelFunc
	done              chan struct{}
	pendingDiscovery  bool
	pendingIDs        map[string]struct{}
	inFlightDiscovery bool
	inFlightIDs       map[string]struct{}
	discovery         discoveryState
}

// Discovery throttling belongs to the worker, not the prunable account cache.
// It also bounds retries for unsupported and newly encountered candidate IDs.
type discoveryState struct {
	LastAttemptAt     time.Time
	LastSuccessAt     time.Time
	LastErrorCategory string
}

func newPluginRuntime(host hostClient, fetch usageFetcher, now func() time.Time) *pluginRuntime {
	if now == nil {
		now = time.Now
	}
	runtime := &pluginRuntime{
		cache: quotaCache{samples: make(map[string]quotaSample)},
		host:  host,
		fetch: fetch,
		now:   now,
	}
	cfg := defaultPluginConfig()
	runtime.config.Store(&cfg)
	return runtime
}

func (r *pluginRuntime) applyConfig(cfg pluginConfig) {
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()

	r.config.Store(&cfg)
	if !cfg.Enabled {
		r.quiesced.Store(true)
		r.stopLocked()
		r.cache.reconcile(nil)
		return
	}
	if r.cancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		wake := make(chan struct{}, 1)
		done := make(chan struct{})
		r.wake, r.cancel, r.done = wake, cancel, done
		go r.refreshLoop(ctx, wake, done)
		r.queueDiscoveryLocked()
		r.quiesced.Store(false)
		r.log("info", "quota router refresh worker started", map[string]any{
			"cutoff_percent_used": cfg.CutoffPercentUsed,
			"protected_models":    cfg.ProtectedModels,
			"minimum_refresh_age": cfg.PollInterval.String(),
			"request_timeout":     cfg.RequestTimeout.String(),
		})
		return
	}
	r.log("info", "quota router configuration reloaded", map[string]any{
		"cutoff_percent_used": cfg.CutoffPercentUsed,
		"protected_models":    cfg.ProtectedModels,
		"minimum_refresh_age": cfg.PollInterval.String(),
		"request_timeout":     cfg.RequestTimeout.String(),
	})
}

func (r *pluginRuntime) shutdown() {
	if r == nil {
		return
	}
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	r.quiesced.Store(true)
	r.stopLocked()
}

func (r *pluginRuntime) stopLocked() {
	if r.cancel == nil {
		return
	}
	cancel, done := r.cancel, r.done
	cancel()
	if done != nil {
		<-done
	}
	r.wake, r.cancel, r.done = nil, nil, nil
	r.refreshMu.Lock()
	r.pendingDiscovery, r.inFlightDiscovery = false, false
	clear(r.pendingIDs)
	clear(r.inFlightIDs)
	r.refreshMu.Unlock()
	r.log("info", "quota router refresh worker stopped", nil)
}

func (r *pluginRuntime) loadedConfig() pluginConfig {
	if r == nil {
		return defaultPluginConfig()
	}
	if cfg := r.config.Load(); cfg != nil {
		return *cfg
	}
	return defaultPluginConfig()
}

func (r *pluginRuntime) queueDiscoveryLocked() {
	if r.wake == nil {
		return
	}
	r.refreshMu.Lock()
	r.pendingDiscovery = true
	clear(r.pendingIDs)
	wake := r.wake
	r.refreshMu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (r *pluginRuntime) queueCandidateRefresh(authID string, cfg pluginConfig, now time.Time) {
	authID = strings.TrimSpace(authID)
	// ponytail: refresh is best effort. Do not make a request wait behind a
	// lifecycle join whose host callback has no cancellation contract.
	if r.quiesced.Load() || !r.lifecycleMu.TryLock() {
		return
	}
	defer r.lifecycleMu.Unlock()
	if r.quiesced.Load() || r.wake == nil || r.cancel == nil || !r.loadedConfig().Enabled {
		return
	}
	r.refreshMu.Lock()
	if !r.pendingDiscovery && !r.inFlightDiscovery &&
		(r.discovery.LastAttemptAt.IsZero() || !now.Before(r.discovery.LastAttemptAt.Add(cfg.PollInterval))) {
		r.pendingDiscovery = true
	}
	_, pending := r.pendingIDs[authID]
	_, inFlight := r.inFlightIDs[authID]
	if !pending && !inFlight && r.cache.refreshDue(authID, now, cfg.CutoffPercentUsed, cfg.PollInterval) {
		if r.pendingIDs == nil {
			r.pendingIDs = make(map[string]struct{})
		}
		r.pendingIDs[authID] = struct{}{}
	}
	queued := r.pendingDiscovery || len(r.pendingIDs) > 0
	wake := r.wake
	r.refreshMu.Unlock()
	if queued {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

func (r *pluginRuntime) refreshLoop(ctx context.Context, wake <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
		for {
			discover, authIDs := r.takePendingRefresh()
			if !discover && len(authIDs) == 0 {
				break
			}
			cfg := r.loadedConfig()
			if cfg.Enabled {
				r.refreshAuths(ctx, cfg, discover, authIDs)
			}
			r.finishRefresh(discover, authIDs)
			if ctx.Err() != nil {
				return
			}
		}
	}
}

func (r *pluginRuntime) takePendingRefresh() (bool, map[string]struct{}) {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	discover := r.pendingDiscovery
	if !discover && len(r.pendingIDs) == 0 {
		return false, nil
	}
	r.pendingDiscovery = false
	r.inFlightDiscovery = discover
	authIDs := r.pendingIDs
	r.pendingIDs = make(map[string]struct{})
	if r.inFlightIDs == nil {
		r.inFlightIDs = make(map[string]struct{}, len(authIDs))
	}
	for authID := range authIDs {
		r.inFlightIDs[authID] = struct{}{}
	}
	return discover, authIDs
}

func (r *pluginRuntime) finishRefresh(discover bool, authIDs map[string]struct{}) {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	if discover {
		r.inFlightDiscovery = false
	}
	for authID := range authIDs {
		delete(r.inFlightIDs, authID)
	}
}

func (r *pluginRuntime) discoverAuths(ctx context.Context) ([]physicalClaudeAuth, map[string]struct{}, bool) {
	if r == nil || r.host == nil || ctx.Err() != nil {
		return nil, nil, false
	}
	r.refreshMu.Lock()
	r.discovery.LastAttemptAt = r.now()
	r.refreshMu.Unlock()
	entries, err := r.host.listAuth()
	if ctx.Err() != nil {
		return nil, nil, false
	}
	r.refreshMu.Lock()
	if err != nil {
		r.discovery.LastErrorCategory = "auth_list"
	} else {
		r.discovery.LastSuccessAt = r.now()
		r.discovery.LastErrorCategory = ""
	}
	r.refreshMu.Unlock()
	if err != nil {
		r.log("warn", "quota router auth discovery failed", map[string]any{"category": "auth_list"})
		return nil, nil, false
	}
	auths := physicalClaudeAuths(entries)
	return auths, r.cache.reconcile(auths), true
}

func (r *pluginRuntime) refreshAuths(ctx context.Context, cfg pluginConfig, discover bool, authIDs map[string]struct{}) {
	if r.fetch == nil || ctx.Err() != nil {
		return
	}
	var auths []physicalClaudeAuth
	var changed map[string]struct{}
	if discover {
		var ok bool
		auths, changed, ok = r.discoverAuths(ctx)
		if !ok {
			return
		}
	} else {
		auths = r.cache.auths()
	}
	for _, auth := range auths {
		if ctx.Err() != nil {
			return
		}
		_, selected := authIDs[auth.ID]
		_, revised := changed[auth.ID]
		if (selected || revised) && r.cache.refreshDue(auth.ID, r.now(), cfg.CutoffPercentUsed, cfg.PollInterval) {
			r.pollAuth(ctx, auth, cfg)
		}
	}
}

func (r *pluginRuntime) pollAuth(ctx context.Context, auth physicalClaudeAuth, cfg pluginConfig) {
	if ctx.Err() != nil {
		return
	}
	r.cache.recordAttempt(auth.ID, r.now())
	rawAuth, err := r.host.getAuth(auth.AuthIndex)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		r.recordPollFailure(auth, pollErrorAuthGet)
		return
	}
	var credential claudeCredential
	if json.Unmarshal(rawAuth, &credential) != nil {
		r.recordPollFailure(auth, pollErrorAuthGet)
		return
	}
	token := strings.TrimSpace(credential.AccessToken)
	if !strings.EqualFold(strings.TrimSpace(credential.Type), "claude") || token == "" {
		r.recordPollFailure(auth, pollErrorMissingToken)
		return
	}
	result, category := r.fetch(ctx, token, cfg.RequestTimeout)
	if category != "" {
		if category != pollErrorCancelled || ctx.Err() == nil {
			r.recordPollFailure(auth, category)
		}
		return
	}
	if ctx.Err() != nil || !r.cache.recordRevisionSuccess(auth, result, r.now()) {
		return
	}
	r.log("debug", "quota router quota refreshed", map[string]any{
		"auth_id":             auth.ID,
		"weekly_percent_used": result.WeeklyPercentUsed,
		"blocked":             result.WeeklyPercentUsed >= cfg.CutoffPercentUsed,
	})
}

func (r *pluginRuntime) recordPollFailure(auth physicalClaudeAuth, category string) {
	if !r.cache.recordRevisionFailure(auth, category) {
		return
	}
	r.log("warn", "quota router quota refresh failed", map[string]any{
		"auth_id":  auth.ID,
		"category": category,
	})
}

func (r *pluginRuntime) discoveryStatus() discoveryStatus {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	return discoveryStatus{
		LastAttemptAt:     statusTime(r.discovery.LastAttemptAt),
		LastSuccessAt:     statusTime(r.discovery.LastSuccessAt),
		LastErrorCategory: r.discovery.LastErrorCategory,
	}
}

func (r *pluginRuntime) log(level, message string, fields map[string]any) {
	if r != nil && r.host != nil {
		r.host.log(level, message, fields)
	}
}
