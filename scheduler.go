package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type physicalClaudeAuth struct {
	ID        string
	AuthIndex string
	Name      string
	Identity  string
}

func physicalClaudeAuths(entries []pluginapi.HostAuthFileEntry) []physicalClaudeAuth {
	auths := make([]physicalClaudeAuth, 0, len(entries))
	for _, entry := range entries {
		provider := strings.ToLower(strings.TrimSpace(entry.Provider))
		if provider == "" {
			provider = strings.ToLower(strings.TrimSpace(entry.Type))
		}
		if provider != "claude" || entry.Disabled || strings.EqualFold(strings.TrimSpace(entry.Status), "disabled") || entry.RuntimeOnly || strings.TrimSpace(entry.Path) == "" {
			continue
		}
		id := strings.TrimSpace(entry.ID)
		if id == "" || id != entry.ID || strings.TrimSpace(entry.AuthIndex) == "" {
			continue
		}
		auths = append(auths, physicalClaudeAuth{
			ID:        entry.ID,
			AuthIndex: entry.AuthIndex,
			Name:      strings.TrimSpace(entry.Name),
			Identity:  physicalAuthIdentity(entry),
		})
	}
	sort.Slice(auths, func(i, j int) bool { return auths[i].ID < auths[j].ID })
	return auths
}

func physicalAuthIdentity(entry pluginapi.HostAuthFileEntry) string {
	return fmt.Sprintf("index:%q|path:%q|account:%q|email:%q|file:%d:%d",
		entry.AuthIndex,
		entry.Path,
		strings.TrimSpace(entry.Account),
		strings.ToLower(strings.TrimSpace(entry.Email)),
		entry.Size,
		entry.ModTime.UnixNano(),
	)
}

func (r *pluginRuntime) pick(req pluginapi.SchedulerPickRequest) (pluginapi.SchedulerPickResponse, *envelopeError) {
	cfg := r.loadedConfig()
	if !cfg.Enabled || !isClaudeRequest(req) || !isProtectedModel(req.Model, cfg.ProtectedModels) {
		return pluginapi.SchedulerPickResponse{Handled: false}, nil
	}
	now := r.now()
	var selected *pluginapi.SchedulerAuthCandidate
	var resetPending []*pluginapi.SchedulerAuthCandidate
	claudeCandidates := 0
	for i := range req.Candidates {
		candidate := &req.Candidates[i]
		provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
		if provider != "" && provider != "claude" {
			continue
		}
		if candidate.ID == "" || strings.TrimSpace(candidate.ID) != candidate.ID {
			continue
		}
		claudeCandidates++
		sample := r.cache.snapshot(candidate.ID)
		if sample.resetPending(now, cfg.CutoffPercentUsed) {
			resetPending = append(resetPending, candidate)
			continue
		}
		if sample.blocked(now, cfg.CutoffPercentUsed) {
			r.queueCandidateRefresh(candidate.ID, cfg, now)
			continue
		}
		selected = preferredCandidate(selected, candidate)
	}
	if selected != nil {
		// A verified/otherwise normally eligible seat wins over reset-pending
		// seats regardless of priority; their retries remain asynchronous.
		for _, candidate := range resetPending {
			r.queueCandidateRefresh(candidate.ID, cfg, now)
		}
		r.queueCandidateRefresh(selected.ID, cfg, now)
		return pluginapi.SchedulerPickResponse{AuthID: selected.ID, Handled: true}, nil
	}
	if len(resetPending) > 0 {
		ids := make([]string, 0, len(resetPending))
		for _, candidate := range resetPending {
			ids = append(ids, candidate.ID)
		}
		r.waitResetPending(ids, cfg, now)
		now = r.now()
		for _, candidate := range resetPending {
			sample := r.cache.snapshot(candidate.ID)
			if sample.HasSample && sample.WeeklyPercentUsed < cfg.CutoffPercentUsed {
				selected = preferredCandidate(selected, candidate)
			}
		}
		if selected != nil {
			return pluginapi.SchedulerPickResponse{AuthID: selected.ID, Handled: true}, nil
		}

		// Fail closed: all seats being reset-pending while the quota host is
		// down is a narrow window because accounts reset at different times.
		// An unverified post-reset seat must never be used, even in an outage.
		// Reuse the existing typed no-seat path; successful exhausted reads
		// retain the normal exhausted error below, never becoming eligible.
		if len(resetPending) == claudeCandidates {
			failures := make(map[string]string, len(ids))
			categories := make(map[string]struct{})
			for _, candidate := range resetPending {
				category := r.resetPendingFailure(candidate.ID, now, cfg.CutoffPercentUsed)
				if category == "" {
					break
				}
				failures[candidate.ID] = category
				categories[category] = struct{}{}
			}
			if len(failures) == len(ids) {
				safeCategories := make([]string, 0, len(categories))
				for category := range categories {
					safeCategories = append(safeCategories, category)
				}
				sort.Strings(safeCategories)
				decisionError := &envelopeError{
					Code: exhaustedErrorCode,
					Message: "no verified seat: all candidate seats await a post-reset quota read; quota host error: " +
						strings.Join(safeCategories, ", "),
				}
				sort.Strings(ids)
				r.log("warn", decisionError.Message, map[string]any{
					"seats": ids,
					"error": failures,
				})
				return pluginapi.SchedulerPickResponse{}, decisionError
			}
		}
	}
	if claudeCandidates > 0 {
		return pluginapi.SchedulerPickResponse{}, &envelopeError{Code: exhaustedErrorCode, Message: exhaustedErrorCode}
	}
	return pluginapi.SchedulerPickResponse{Handled: false}, nil
}

func preferredCandidate(selected, candidate *pluginapi.SchedulerAuthCandidate) *pluginapi.SchedulerAuthCandidate {
	if selected == nil || candidate.Priority > selected.Priority ||
		(candidate.Priority == selected.Priority && candidate.ID < selected.ID) {
		return candidate
	}
	return selected
}

func isClaudeRequest(req pluginapi.SchedulerPickRequest) bool {
	if strings.EqualFold(strings.TrimSpace(req.Provider), "claude") {
		return true
	}
	if strings.TrimSpace(req.Provider) != "" || len(req.Providers) != 1 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(req.Providers[0]), "claude")
}

func isProtectedModel(model string, protectedModels []string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	for _, protectedModel := range protectedModels {
		if strings.EqualFold(model, protectedModel) {
			return true
		}
	}
	return false
}

func (r *pluginRuntime) handleManagement(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	if !strings.EqualFold(strings.TrimSpace(req.Method), http.MethodGet) || strings.TrimRight(strings.TrimSpace(req.Path), "/") != managementStatusFullPath {
		body, _ := json.Marshal(map[string]string{"error": "not found"})
		return pluginapi.ManagementResponse{
			StatusCode: http.StatusNotFound,
			Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			Body:       body,
		}
	}
	cfg := r.loadedConfig()
	body, _ := json.Marshal(cutoffStatusResponse{
		Enabled:           cfg.Enabled,
		CutoffPercentUsed: cfg.CutoffPercentUsed,
		ProtectedModels:   cfg.ProtectedModels,
		Accounts:          r.cache.statuses(r.now(), cfg.CutoffPercentUsed),
	})
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       body,
	}
}
