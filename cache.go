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
	Discovery         discoveryStatus       `json:"discovery"`
}

type discoveryStatus struct {
	LastAttemptAt     string `json:"last_attempt_at,omitempty"`
	LastSuccessAt     string `json:"last_success_at,omitempty"`
	LastErrorCategory string `json:"last_error_category,omitempty"`
}

type cutoffAccountStatus struct {
	ID                string   `json:"id"`
	AuthIndex         string   `json:"auth_index,omitempty"`
	Name              string   `json:"name,omitempty"`
	Known             bool     `json:"known"`
	Blocked           bool     `json:"blocked"`
	WeeklyPercentUsed *float64 `json:"weekly_percent_used,omitempty"`
	SampledAt         string   `json:"sampled_at,omitempty"`
	LastAttemptAt     string   `json:"last_attempt_at,omitempty"`
	ResetAt           string   `json:"reset_at,omitempty"`
	LastErrorCategory string   `json:"last_error_category,omitempty"`
}

type quotaSample struct {
	AuthIndex          string
	Name               string
	CredentialRevision string
	HasSample          bool
	WeeklyPercentUsed  float64
	SampledAt          time.Time
	LastAttemptAt      time.Time
	ResetAt            time.Time
	LastErrorCategory  string
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

func (c *quotaCache) reconcile(auths []physicalClaudeAuth) map[string]struct{} {
	keep := make(map[string]struct{}, len(auths))
	changed := make(map[string]struct{})
	c.mu.Lock()
	for _, auth := range auths {
		if strings.TrimSpace(auth.ID) == "" {
			continue
		}
		keep[auth.ID] = struct{}{}
		sample, exists := c.samples[auth.ID]
		if !exists || sample.CredentialRevision != auth.CredentialRevision {
			changed[auth.ID] = struct{}{}
			// A file revision is not a stable quota-account identity. Preserve
			// no prior quota claim when a previously observed revision changes.
			if exists && sample.CredentialRevision != "" {
				sample = quotaSample{}
			}
		}
		sample.AuthIndex = auth.AuthIndex
		sample.Name = strings.TrimSpace(auth.Name)
		sample.CredentialRevision = auth.CredentialRevision
		c.samples[auth.ID] = sample
	}
	for authID := range c.samples {
		if _, ok := keep[authID]; !ok {
			delete(c.samples, authID)
		}
	}
	c.mu.Unlock()
	return changed
}

func (c *quotaCache) auths() []physicalClaudeAuth {
	c.mu.Lock()
	defer c.mu.Unlock()
	auths := make([]physicalClaudeAuth, 0, len(c.samples))
	for id, sample := range c.samples {
		auths = append(auths, physicalClaudeAuth{
			ID: id, AuthIndex: sample.AuthIndex, Name: sample.Name,
			CredentialRevision: sample.CredentialRevision,
		})
	}
	sort.Slice(auths, func(i, j int) bool { return auths[i].ID < auths[j].ID })
	return auths
}

func (c *quotaCache) refreshDue(authID string, now time.Time, cutoff float64, minimumAge time.Duration) bool {
	if strings.TrimSpace(authID) == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sample, exists := c.samples[authID]
	if !exists || sample.AuthIndex == "" || sample.blocked(now, cutoff) {
		return false
	}
	lastCheck := sample.SampledAt
	if sample.LastAttemptAt.After(lastCheck) {
		lastCheck = sample.LastAttemptAt
	}
	if !lastCheck.IsZero() && now.Before(lastCheck.Add(minimumAge)) {
		return false
	}
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
	sample.ResetAt = resetAt
	sample.LastErrorCategory = ""
	c.samples[authID] = sample
	c.mu.Unlock()
}

// The worker commits only to the revision it discovered. This rejects an
// observed replacement, not a host file change that discovery has not seen yet.
func (c *quotaCache) recordRevisionSuccess(auth physicalClaudeAuth, result usageResult, sampledAt time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	sample, exists := c.samples[auth.ID]
	if !exists || sample.CredentialRevision != auth.CredentialRevision {
		return false
	}
	sample.HasSample = true
	sample.WeeklyPercentUsed = result.WeeklyPercentUsed
	sample.SampledAt = sampledAt
	sample.ResetAt = result.ResetAt
	sample.LastErrorCategory = ""
	c.samples[auth.ID] = sample
	return true
}

func (c *quotaCache) recordRevisionFailure(auth physicalClaudeAuth, category string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	sample, exists := c.samples[auth.ID]
	if !exists || sample.CredentialRevision != auth.CredentialRevision {
		return false
	}
	sample.LastErrorCategory = category
	c.samples[auth.ID] = sample
	return true
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
		account.SampledAt = statusTime(sample.SampledAt)
		account.LastAttemptAt = statusTime(sample.LastAttemptAt)
		if known {
			percentUsed := sample.WeeklyPercentUsed
			account.WeeklyPercentUsed = &percentUsed
			account.ResetAt = statusTime(sample.ResetAt)
		}
		accounts = append(accounts, account)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	return accounts
}

func statusTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
