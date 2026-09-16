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
	ID                 string
	AuthIndex          string
	Name               string
	CredentialRevision string
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
			ID:                 entry.ID,
			AuthIndex:          entry.AuthIndex,
			Name:               strings.TrimSpace(entry.Name),
			CredentialRevision: physicalAuthRevision(entry),
		})
	}
	sort.Slice(auths, func(i, j int) bool { return auths[i].ID < auths[j].ID })
	return auths
}

func physicalAuthRevision(entry pluginapi.HostAuthFileEntry) string {
	return fmt.Sprintf("index:%q|path:%q|account:%q|email:%q|file:%d:%d",
		entry.AuthIndex,
		entry.Path,
		strings.TrimSpace(entry.Account),
		strings.ToLower(strings.TrimSpace(entry.Email)),
		entry.Size,
		entry.ModTime.UnixNano(),
	)
}

// These wire types match the negotiated host feature without changing the
// unrelated v7.2.100 SDK dependency or native C ABI.
const schedulerFilterV1 = "scheduler_filter_v1"

type filterCandidate struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
}

type filterRequest struct {
	Model      string            `json:"model"`
	Candidates []filterCandidate `json:"candidates"`
}

type filterResponse struct {
	ExcludedIDs []string `json:"excluded_ids"`
}

func (r *pluginRuntime) filter(req filterRequest) (filterResponse, *envelopeError) {
	response := filterResponse{ExcludedIDs: []string{}}
	seen := make(map[string]struct{}, len(req.Candidates))
	for _, candidate := range req.Candidates {
		_, duplicate := seen[candidate.ID]
		if candidate.ID == "" || strings.TrimSpace(candidate.ID) != candidate.ID || strings.TrimSpace(candidate.Provider) == "" || duplicate {
			return response, &envelopeError{Code: "invalid_candidates", Message: "candidate IDs must be nonempty and unique with a provider"}
		}
		seen[candidate.ID] = struct{}{}
	}
	cfg := r.loadedConfig()
	if !cfg.Enabled || !isProtectedModel(req.Model, cfg.ProtectedModels) {
		return response, nil
	}
	now := r.now()
	claude := false
	for _, candidate := range req.Candidates {
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), "claude") {
			continue
		}
		claude = true
		if r.cache.isBlocked(candidate.ID, now, cfg.CutoffPercentUsed) {
			response.ExcludedIDs = append(response.ExcludedIDs, candidate.ID)
		} else {
			// Native selection happens after this callback. Refresh offered,
			// eligible credentials when due; never choose an account here.
			r.queueCandidateRefresh(candidate.ID, cfg, now)
		}
	}
	if claude {
		r.queueCandidateRefresh("", cfg, now)
	}
	return response, nil
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
		Discovery:         r.discoveryStatus(),
	})
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       body,
	}
}
