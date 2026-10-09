package main

import (
	"context"
	"encoding/json"
	"errors"
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
	lifecycleMu sync.Mutex
	refreshMu   sync.Mutex
	config      atomic.Pointer[pluginConfig]
	cache       quotaCache
	host        hostClient
	fetch       usageFetcher
	now         func() time.Time
	wake        chan struct{}
	cancel      context.CancelFunc
	done        chan struct{}
	pendingAll  bool
	pendingIDs  map[string]struct{}
	inFlightAll bool
	inFlightIDs map[string]struct{}
	// Closed/replaced on worker completion so synchronous picks can join the
	// existing per-seat coalesced work without polling or duplicate fetches.
	refreshChanged chan struct{}
}

func newPluginRuntime(host hostClient, fetch usageFetcher, now func() time.Time) *pluginRuntime {
	if now == nil {
		now = time.Now
	}
	runtime := &pluginRuntime{
		cache:          quotaCache{samples: make(map[string]quotaSample)},
		host:           host,
		fetch:          fetch,
		now:            now,
		refreshChanged: make(chan struct{}),
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
		r.queueAllRefreshLocked()
		r.log("info", "quota router refresh worker started", map[string]any{
			"cutoff_percent_used": cfg.CutoffPercentUsed,
			"protected_models":    cfg.ProtectedModels,
			"minimum_refresh_age": cfg.PollInterval.String(),
			"request_timeout":     cfg.RequestTimeout.String(),
		})
		return
	}
	if r.cache.empty() {
		r.queueAllRefreshLocked()
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
	r.pendingAll, r.inFlightAll = false, false
	clear(r.pendingIDs)
	clear(r.inFlightIDs)
	r.notifyRefreshLocked()
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

func (r *pluginRuntime) queueAllRefreshLocked() {
	if r.wake == nil {
		return
	}
	r.refreshMu.Lock()
	r.pendingAll = true
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
	if authID == "" {
		return
	}
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if r.wake == nil || r.cancel == nil || !r.loadedConfig().Enabled {
		return
	}
	r.refreshMu.Lock()
	if r.pendingAll || r.inFlightAll {
		r.refreshMu.Unlock()
		return
	}
	if _, exists := r.pendingIDs[authID]; exists {
		r.refreshMu.Unlock()
		return
	}
	if _, exists := r.inFlightIDs[authID]; exists {
		r.refreshMu.Unlock()
		return
	}
	if !r.cache.claimRefresh(authID, now, cfg.CutoffPercentUsed, cfg.PollInterval, cfg.BlockedRefreshInterval) {
		r.refreshMu.Unlock()
		return
	}
	if r.pendingIDs == nil {
		r.pendingIDs = make(map[string]struct{})
	}
	r.pendingIDs[authID] = struct{}{}
	wake := r.wake
	r.refreshMu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
}

// Owner policy: when no normally eligible seat exists, selection waits for a
// bounded fresh read of reset-pending seats through the same worker/fetch path.
// Queueing still obeys post-failure retry age; an existing pending/in-flight
// refresh is joined, never duplicated. A wait timeout alone is NOT an outage.
func (r *pluginRuntime) waitResetPending(authIDs []string, cfg pluginConfig, now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), resetReadTimeout(cfg))
	defer cancel()
	for _, authID := range authIDs {
		r.queueCandidateRefresh(authID, cfg, now)
	}
	for {
		r.refreshMu.Lock()
		changed := r.refreshChanged
		active := r.pendingAll || r.inFlightAll
		for _, authID := range authIDs {
			_, pending := r.pendingIDs[authID]
			_, inFlight := r.inFlightIDs[authID]
			active = active || pending || inFlight
		}
		r.refreshMu.Unlock()
		if !active {
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return
		}
	}
}

func resetReadTimeout(cfg pluginConfig) time.Duration {
	return min(cfg.RequestTimeout, 2*time.Second)
}

// Report only safe categories for the fail-closed post-reset no-seat error.
// Empty means the seat is no longer reset-pending (e.g. a fresh exhausted read).
// Incomplete work is not evidence of a host outage, but still cannot enable it.
func (r *pluginRuntime) resetPendingFailure(authID string, now time.Time, cutoff float64) string {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	_, pending := r.pendingIDs[authID]
	_, inFlight := r.inFlightIDs[authID]
	sample := r.cache.snapshot(authID)
	if !sample.resetPending(now, cutoff) {
		return ""
	}
	if r.pendingAll || r.inFlightAll || pending || inFlight {
		return "read_pending"
	}
	if sample.LastAttemptAt.Before(sample.ResetAt) {
		return "unverified"
	}
	switch sample.LastErrorCategory {
	case pollErrorAuthList, pollErrorAuthGet, pollErrorTimeout, pollErrorNetwork,
		pollErrorUnauthorized, pollErrorForbidden, pollErrorRateLimited,
		pollErrorServer, pollErrorHTTP, pollErrorRead, pollErrorInvalidJSON,
		pollErrorInvalidWeekly, pollErrorBodyTooLarge, pollErrorMissingToken, pollErrorCancelled,
		pollErrorInvalidCredential:
		return sample.LastErrorCategory
	default:
		return "unverified"
	}
}

func (r *pluginRuntime) notifyRefreshLocked() {
	close(r.refreshChanged)
	r.refreshChanged = make(chan struct{})
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
			all, authIDs := r.takePendingRefresh()
			if !all && len(authIDs) == 0 {
				break
			}
			cfg := r.loadedConfig()
			if cfg.Enabled {
				r.refreshAuths(ctx, cfg, all, authIDs)
			}
			r.finishRefresh(all, authIDs)
			if ctx.Err() != nil {
				return
			}
		}
	}
}

func (r *pluginRuntime) takePendingRefresh() (bool, map[string]struct{}) {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	if r.pendingAll {
		r.pendingAll = false
		clear(r.pendingIDs)
		r.inFlightAll = true
		return true, nil
	}
	if len(r.pendingIDs) == 0 {
		return false, nil
	}
	authIDs := r.pendingIDs
	r.pendingIDs = make(map[string]struct{})
	if r.inFlightIDs == nil {
		r.inFlightIDs = make(map[string]struct{}, len(authIDs))
	}
	for authID := range authIDs {
		r.inFlightIDs[authID] = struct{}{}
	}
	return false, authIDs
}

func (r *pluginRuntime) finishRefresh(all bool, authIDs map[string]struct{}) {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	defer r.notifyRefreshLocked()
	if all {
		r.inFlightAll = false
		return
	}
	for authID := range authIDs {
		delete(r.inFlightIDs, authID)
	}
}

func (r *pluginRuntime) pollOnce(ctx context.Context, cfg pluginConfig) {
	r.refreshAuths(ctx, cfg, true, nil)
}

func (r *pluginRuntime) refreshAuths(ctx context.Context, cfg pluginConfig, all bool, authIDs map[string]struct{}) {
	if r == nil || r.host == nil || r.fetch == nil || ctx.Err() != nil {
		return
	}
	entries, err := r.host.listAuth()
	if err != nil {
		if all {
			authIDs = make(map[string]struct{})
			for _, account := range r.cache.statuses(r.now(), cfg.CutoffPercentUsed) {
				authIDs[account.ID] = struct{}{}
			}
		}
		for authID := range authIDs {
			r.cache.recordAttempt(authID, r.now())
			r.cache.recordFailure(authID, pollErrorAuthList)
		}
		r.log("warn", "quota router auth discovery failed", map[string]any{"category": pollErrorAuthList})
		return
	}
	auths := physicalClaudeAuths(entries)
	r.cache.reconcile(auths)
	for _, auth := range auths {
		if ctx.Err() != nil {
			return
		}
		if !all {
			if _, selected := authIDs[auth.ID]; !selected {
				continue
			}
		}
		r.pollAuth(ctx, auth, cfg)
	}
}

func (r *pluginRuntime) pollAuth(ctx context.Context, auth physicalClaudeAuth, cfg pluginConfig) {
	if r.cache.snapshot(auth.ID).resetPending(r.now(), cfg.CutoffPercentUsed) {
		// Bound the fresh fetch too; pick's separate deadline also bounds a
		// blocked host callback (the host API itself has no context argument).
		cfg.RequestTimeout = resetReadTimeout(cfg)
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.RequestTimeout)
		defer cancel()
	}
	r.cache.recordAttempt(auth.ID, r.now())
	rawAuth, err := r.host.getAuth(auth.AuthIndex)
	if err != nil {
		r.recordPollFailure(auth.ID, pollErrorAuthGet)
		return
	}
	var credential claudeCredential
	// A successful host callback can still return an invalid local file.
	// Decode/type validation failures are not host outages.
	if json.Unmarshal(rawAuth, &credential) != nil || !strings.EqualFold(strings.TrimSpace(credential.Type), "claude") {
		r.recordPollFailure(auth.ID, pollErrorInvalidCredential)
		return
	}
	token := strings.TrimSpace(credential.AccessToken)
	if token == "" {
		r.recordPollFailure(auth.ID, pollErrorMissingToken)
		return
	}
	result, category := r.fetch(ctx, token, cfg.RequestTimeout)
	if category != "" {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			category = pollErrorTimeout
		}
		if category != pollErrorCancelled || ctx.Err() == nil {
			r.recordPollFailure(auth.ID, category)
		}
		return
	}
	r.cache.recordSuccess(auth.ID, result.WeeklyPercentUsed, result.ResetAt, r.now())
	r.log("debug", "quota router quota refreshed", map[string]any{
		"auth_id":             auth.ID,
		"weekly_percent_used": result.WeeklyPercentUsed,
		"blocked":             result.WeeklyPercentUsed >= cfg.CutoffPercentUsed,
	})
}

func (r *pluginRuntime) recordPollFailure(authID, category string) {
	r.cache.recordFailure(authID, category)
	r.log("warn", "quota router quota refresh failed", map[string]any{
		"auth_id":  authID,
		"category": category,
	})
}

func (r *pluginRuntime) log(level, message string, fields map[string]any) {
	if r != nil && r.host != nil {
		r.host.log(level, message, fields)
	}
}
