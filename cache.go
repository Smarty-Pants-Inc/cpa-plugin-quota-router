package main

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type cutoffStatusResponse struct {
	Enabled           bool                  `json:"enabled"`
	ProtectedModels   []string              `json:"protected_models"`
	CutoffPercentUsed float64               `json:"cutoff_percent_used"`
	Accounts          []cutoffAccountStatus `json:"accounts"`
}

type cutoffAccountStatus struct {
	ID                string   `json:"id"`
	AuthIndex         string   `json:"auth_index,omitempty"`
	Name              string   `json:"name,omitempty"`
	Known             bool     `json:"known"`
	Blocked           bool     `json:"blocked"`
	WeeklyPercentUsed *float64 `json:"weekly_percent_used,omitempty"`
	SampledAt         string   `json:"sampled_at,omitempty"`
	ResetAt           string   `json:"reset_at,omitempty"`
	LastErrorCategory string   `json:"last_error_category,omitempty"`
}

type quotaSample struct {
	AuthIndex         string
	Name              string
	Identity          string
	HasSample         bool
	WeeklyPercentUsed float64
	SampledAt         time.Time
	LastAttemptAt     time.Time
	ResetAt           time.Time
	LastErrorCategory string
}

func (s quotaSample) known(now time.Time) bool {
	return s.HasSample && (s.ResetAt.IsZero() || now.Before(s.ResetAt))
}

func (s quotaSample) blocked(now time.Time, cutoff float64) bool {
	return s.known(now) && s.WeeklyPercentUsed >= cutoff
}

type quotaCache struct {
	mu      sync.Mutex
	samples map[string]quotaSample
}

func (c *quotaCache) empty() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.samples) == 0
}

func (c *quotaCache) reconcile(auths []physicalClaudeAuth) {
	keep := make(map[string]struct{}, len(auths))
	c.mu.Lock()
	for _, auth := range auths {
		if strings.TrimSpace(auth.ID) == "" {
			continue
		}
		keep[auth.ID] = struct{}{}
		sample := c.samples[auth.ID]
		if sample.Identity != "" && auth.Identity != "" && sample.Identity != auth.Identity {
			sample = quotaSample{}
		}
		sample.AuthIndex = auth.AuthIndex
		sample.Name = strings.TrimSpace(auth.Name)
		sample.Identity = auth.Identity
		c.samples[auth.ID] = sample
	}
	for authID := range c.samples {
		if _, ok := keep[authID]; !ok {
			delete(c.samples, authID)
		}
	}
	c.mu.Unlock()
}

func (c *quotaCache) claimRefresh(authID string, now time.Time, cutoff float64, minimumAge time.Duration) bool {
	if strings.TrimSpace(authID) == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sample := c.samples[authID]
	if sample.blocked(now, cutoff) {
		return false
	}
	lastCheck := sample.SampledAt
	if sample.LastAttemptAt.After(lastCheck) {
		lastCheck = sample.LastAttemptAt
	}
	if !lastCheck.IsZero() && now.Before(lastCheck.Add(minimumAge)) {
		return false
	}
	sample.LastAttemptAt = now
	c.samples[authID] = sample
	return true
}

func (c *quotaCache) recordAttempt(authID string, attemptedAt time.Time) {
	if strings.TrimSpace(authID) == "" {
		return
	}
	c.mu.Lock()
	sample := c.samples[authID]
	sample.LastAttemptAt = attemptedAt
	c.samples[authID] = sample
	c.mu.Unlock()
}

func (c *quotaCache) recordSuccess(authID string, percentUsed float64, resetAt, sampledAt time.Time) {
	if strings.TrimSpace(authID) == "" {
		return
	}
	c.mu.Lock()
	sample := c.samples[authID]
	sample.HasSample = true
	sample.WeeklyPercentUsed = percentUsed
	sample.SampledAt = sampledAt
	sample.LastAttemptAt = sampledAt
	sample.ResetAt = resetAt
	sample.LastErrorCategory = ""
	c.samples[authID] = sample
	c.mu.Unlock()
}

func (c *quotaCache) recordFailure(authID, category string) {
	if strings.TrimSpace(authID) == "" {
		return
	}
	c.mu.Lock()
	sample := c.samples[authID]
	sample.LastErrorCategory = category
	c.samples[authID] = sample
	c.mu.Unlock()
}

func (c *quotaCache) isBlocked(authID string, now time.Time, cutoff float64) bool {
	c.mu.Lock()
	sample := c.samples[authID]
	c.mu.Unlock()
	return sample.blocked(now, cutoff)
}

func (c *quotaCache) snapshot(authID string) quotaSample {
	c.mu.Lock()
	sample := c.samples[authID]
	c.mu.Unlock()
	return sample
}

func (c *quotaCache) statuses(now time.Time, cutoff float64) []cutoffAccountStatus {
	c.mu.Lock()
	defer c.mu.Unlock()

	accounts := make([]cutoffAccountStatus, 0, len(c.samples))
	for authID, sample := range c.samples {
		known := sample.known(now)
		account := cutoffAccountStatus{
			ID:                authID,
			AuthIndex:         sample.AuthIndex,
			Name:              sample.Name,
			Known:             known,
			Blocked:           sample.blocked(now, cutoff),
			LastErrorCategory: sample.LastErrorCategory,
		}
		if known {
			percentUsed := sample.WeeklyPercentUsed
			account.WeeklyPercentUsed = &percentUsed
			if !sample.SampledAt.IsZero() {
				account.SampledAt = sample.SampledAt.UTC().Format(time.RFC3339Nano)
			}
			account.ResetAt = sample.ResetAt.UTC().Format(time.RFC3339Nano)
		}
		accounts = append(accounts, account)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	return accounts
}
