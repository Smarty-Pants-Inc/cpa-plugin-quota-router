package main

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type cutoffStatusResponse struct {
	Enabled                   bool                  `json:"enabled"`
	ProtectedModels           []string              `json:"protected_models"`
	CutoffPercentUsed         float64               `json:"cutoff_percent_used"`
	FiveHourCutoffPercentUsed float64               `json:"five_hour_cutoff_percent_used"`
	Accounts                  []cutoffAccountStatus `json:"accounts"`
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
	AuthIndex           string
	Name                string
	Identity            string
	HasSample           bool
	WeeklyPercentUsed   float64
	FiveHourPercentUsed *float64
	FiveHourResetAt     time.Time
	SevenDayPercentUsed *float64
	SevenDayResetAt     time.Time
	SampledAt           time.Time
	LastAttemptAt       time.Time
	ResetAt             time.Time
	LastErrorCategory   string
}

func (s quotaSample) known(now time.Time) bool {
	return s.HasSample && (s.ResetAt.IsZero() || now.Before(s.ResetAt))
}

// Owner policy: a pre-reset EXHAUSTED sample becomes reset-pending at the
// reset, not eligible. Only a successful fresh read below the cutoff clears it.
// A fresh exhausted read stays blocked even if the host reports a past reset.
func (s quotaSample) resetPending(now time.Time, cutoff float64) bool {
	return s.resetPendingWindows(now, cutoff, cutoff)
}

func (s quotaSample) resetPendingWindows(now time.Time, cutoff, fiveHourCutoff float64) bool {
	if !s.HasSample {
		return false
	}
	if s.SevenDayPercentUsed != nil && *s.SevenDayPercentUsed >= cutoff {
		if !s.SevenDayResetAt.IsZero() && !now.Before(s.SevenDayResetAt) && s.SampledAt.Before(s.SevenDayResetAt) {
			return true
		}
	} else if s.WeeklyPercentUsed >= cutoff {
		if !s.ResetAt.IsZero() && !now.Before(s.ResetAt) && s.SampledAt.Before(s.ResetAt) {
			return true
		}
	}
	if s.FiveHourPercentUsed != nil && *s.FiveHourPercentUsed >= fiveHourCutoff {
		if !s.FiveHourResetAt.IsZero() && !now.Before(s.FiveHourResetAt) && s.SampledAt.Before(s.FiveHourResetAt) {
			return true
		}
	}
	return false
}

func (s quotaSample) blocked(now time.Time, cutoff float64) bool {
	return s.blockedWindows(now, cutoff, cutoff)
}

func (s quotaSample) blockedWindows(_ time.Time, cutoff, fiveHourCutoff float64) bool {
	if !s.HasSample {
		return false
	}
	if s.SevenDayPercentUsed != nil {
		if *s.SevenDayPercentUsed >= cutoff {
			return true
		}
	} else if s.WeeklyPercentUsed >= cutoff {
		return true
	}
	if s.FiveHourPercentUsed != nil && *s.FiveHourPercentUsed >= fiveHourCutoff {
		return true
	}
	return false
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

func (c *quotaCache) claimRefresh(authID string, now time.Time, cutoff float64, minimumAge, blockedRefreshInterval time.Duration) bool {
	return c.claimRefreshWindows(authID, now, cutoff, cutoff, minimumAge, blockedRefreshInterval)
}

func (c *quotaCache) claimRefreshWindows(authID string, now time.Time, cutoff, fiveHourCutoff float64, minimumAge, blockedRefreshInterval time.Duration) bool {
	if strings.TrimSpace(authID) == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sample := c.samples[authID]
	lastCheck := sample.SampledAt
	if sample.LastAttemptAt.After(lastCheck) {
		lastCheck = sample.LastAttemptAt
	}
	if sample.resetPendingWindows(now, cutoff, fiveHourCutoff) {
		// First use at reset bypasses pre-reset timing. Failed post-reset
		// attempts still use the normal retry age, avoiding a request storm.
		if lastCheck.Before(sample.ResetAt) {
			lastCheck = time.Time{}
		}
	} else if sample.blockedWindows(now, cutoff, fiveHourCutoff) {
		if blockedRefreshInterval <= 0 {
			return false
		}
		minimumAge = blockedRefreshInterval
	} else if sample.HasSample && !sample.ResetAt.IsZero() && !now.Before(sample.ResetAt) && lastCheck.Before(sample.ResetAt) {
		lastCheck = time.Time{}
	}
	if !lastCheck.IsZero() && now.Before(lastCheck.Add(minimumAge)) {
		return false
	}
	sample.LastAttemptAt = now
	sample.LastErrorCategory = ""
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
	sample.LastErrorCategory = ""
	c.samples[authID] = sample
	c.mu.Unlock()
}

func (c *quotaCache) recordUsage(authID string, res usageResult, sampledAt time.Time) {
	if strings.TrimSpace(authID) == "" {
		return
	}
	c.mu.Lock()
	sample := c.samples[authID]
	sample.HasSample = true
	sample.WeeklyPercentUsed = res.WeeklyPercentUsed
	sample.ResetAt = res.ResetAt
	sample.FiveHourPercentUsed = res.FiveHourPercentUsed
	sample.FiveHourResetAt = res.FiveHourResetAt
	sample.SevenDayPercentUsed = res.SevenDayPercentUsed
	sample.SevenDayResetAt = res.SevenDayResetAt
	sample.SampledAt = sampledAt
	sample.LastAttemptAt = sampledAt
	sample.LastErrorCategory = ""
	c.samples[authID] = sample
	c.mu.Unlock()
}

func (c *quotaCache) recordSuccess(authID string, percentUsed float64, resetAt, sampledAt time.Time) {
	c.recordUsage(authID, usageResult{
		WeeklyPercentUsed: percentUsed,
		ResetAt:           resetAt,
	}, sampledAt)
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
	return c.isBlockedWindows(authID, now, cutoff, cutoff)
}

func (c *quotaCache) isBlockedWindows(authID string, now time.Time, cutoff, fiveHourCutoff float64) bool {
	c.mu.Lock()
	sample := c.samples[authID]
	c.mu.Unlock()
	return sample.blockedWindows(now, cutoff, fiveHourCutoff)
}

func (c *quotaCache) snapshot(authID string) quotaSample {
	c.mu.Lock()
	sample := c.samples[authID]
	c.mu.Unlock()
	return sample
}

func (c *quotaCache) statuses(now time.Time, cutoff float64) []cutoffAccountStatus {
	return c.statusesWindows(now, cutoff, cutoff)
}

func (c *quotaCache) statusesWindows(now time.Time, cutoff, fiveHourCutoff float64) []cutoffAccountStatus {
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
			Blocked:           sample.blockedWindows(now, cutoff, fiveHourCutoff),
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
