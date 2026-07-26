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
	lifecycleMu sync.Mutex
	config      atomic.Pointer[pluginConfig]
	cache       quotaCache
	host        hostClient
	fetch       usageFetcher
	now         func() time.Time
	wake        chan struct{}
	cancel      context.CancelFunc
	done        chan struct{}
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
		r.stopLocked()
		r.cache.reconcile(nil)
		return
	}
	if r.cancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		wake := make(chan struct{}, 1)
		done := make(chan struct{})
		r.wake, r.cancel, r.done = wake, cancel, done
		go r.pollLoop(ctx, wake, done)
		r.log("info", "claude weekly cutoff poller started", map[string]any{
			"cutoff_percent_used": cfg.CutoffPercentUsed,
			"protected_models":    cfg.ProtectedModels,
			"poll_interval":       cfg.PollInterval.String(),
			"request_timeout":     cfg.RequestTimeout.String(),
		})
		return
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
	r.log("info", "claude weekly cutoff configuration reloaded", map[string]any{
		"cutoff_percent_used": cfg.CutoffPercentUsed,
		"protected_models":    cfg.ProtectedModels,
		"poll_interval":       cfg.PollInterval.String(),
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
	r.wake, r.cancel, r.done = nil, nil, nil
	cancel()
	if done != nil {
		<-done
	}
	r.log("info", "claude weekly cutoff poller stopped", nil)
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

func (r *pluginRuntime) pollLoop(ctx context.Context, wake <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	for {
		if ctx.Err() != nil {
			return
		}
		cfg := r.loadedConfig()
		if !cfg.Enabled {
			return
		}
		r.pollOnce(ctx, cfg)
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(r.loadedConfig().PollInterval)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			return
		case <-wake:
			stopTimer(timer)
		case <-timer.C:
		}
	}
}

func stopTimer(timer *time.Timer) {
	if timer == nil || timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

func (r *pluginRuntime) pollOnce(ctx context.Context, cfg pluginConfig) {
	if r == nil || r.host == nil || r.fetch == nil {
		return
	}
	if ctx.Err() != nil {
		return
	}
	entries, err := r.host.listAuth()
	if err != nil {
		r.log("warn", "claude weekly cutoff auth discovery failed", map[string]any{"category": "auth_list"})
		return
	}
	auths := physicalClaudeAuths(entries)
	r.cache.reconcile(auths)
	for _, auth := range auths {
		if ctx.Err() != nil {
			return
		}
		r.pollAuth(ctx, auth, cfg)
	}
}

func (r *pluginRuntime) pollAuth(ctx context.Context, auth physicalClaudeAuth, cfg pluginConfig) {
	rawAuth, err := r.host.getAuth(auth.AuthIndex)
	if err != nil {
		r.recordPollFailure(auth.ID, pollErrorAuthGet)
		return
	}
	var credential claudeCredential
	if json.Unmarshal(rawAuth, &credential) != nil {
		r.recordPollFailure(auth.ID, pollErrorAuthGet)
		return
	}
	token := strings.TrimSpace(credential.AccessToken)
	if !strings.EqualFold(strings.TrimSpace(credential.Type), "claude") || token == "" {
		r.recordPollFailure(auth.ID, pollErrorMissingToken)
		return
	}
	result, category := r.fetch(ctx, token, cfg.RequestTimeout)
	if category != "" {
		if category != pollErrorCancelled || ctx.Err() == nil {
			r.recordPollFailure(auth.ID, category)
		}
		return
	}
	r.cache.recordSuccess(auth.ID, result.WeeklyPercentUsed, result.ResetAt, r.now())
	r.log("debug", "claude weekly cutoff quota refreshed", map[string]any{
		"auth_id":             auth.ID,
		"weekly_percent_used": result.WeeklyPercentUsed,
		"blocked":             result.WeeklyPercentUsed >= cfg.CutoffPercentUsed,
	})
}

func (r *pluginRuntime) recordPollFailure(authID, category string) {
	r.cache.recordFailure(authID, category)
	r.log("warn", "claude weekly cutoff quota refresh failed", map[string]any{
		"auth_id":  authID,
		"category": category,
	})
}

func (r *pluginRuntime) log(level, message string, fields map[string]any) {
	if r != nil && r.host != nil {
		r.host.log(level, message, fields)
	}
}
