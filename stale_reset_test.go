package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// These regressions exercise pick -> existing refresh worker -> host get -> fetch.
// No proposed implementation helper is used, so this file compiles on the base.
type staleResetFixture struct {
	runtime *pluginRuntime
	host    *fakeHost
	fetcher *fakeFetcher
	clock   *testClock
	cfg     pluginConfig
	start   time.Time
	started chan struct{}
	release func()
}

func staleResetConfig(t *testing.T, configYAML string) pluginConfig {
	t.Helper()
	raw, err := json.Marshal(lifecycleRequest{ConfigYAML: []byte(configYAML)})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := decodeLifecycleConfig(raw)
	if err != nil {
		t.Fatalf("decode config %q: %v", configYAML, err)
	}
	return cfg
}

func newStaleResetFixture(t *testing.T, configYAML string, resetAfter time.Duration, gated bool, subsequent ...fetchReply) *staleResetFixture {
	t.Helper()
	start := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	f := &staleResetFixture{
		host: &fakeHost{
			entries:  []pluginapi.HostAuthFileEntry{physicalEntry("auth-a", "index-a")},
			authJSON: map[string]json.RawMessage{"index-a": credentialJSON("token-a")},
		},
		clock:   &testClock{value: start},
		cfg:     staleResetConfig(t, configYAML),
		start:   start,
		started: make(chan struct{}, 16),
	}
	replies := append([]fetchReply{{result: usageResult{WeeklyPercentUsed: 100, ResetAt: start.Add(resetAfter)}}}, subsequent...)
	f.fetcher = &fakeFetcher{replies: map[string][]fetchReply{"token-a": replies}}
	release := make(chan struct{})
	var once sync.Once
	f.release = func() { once.Do(func() { close(release) }) }
	fetch := func(ctx context.Context, token string, timeout time.Duration) (usageResult, string) {
		if gated && f.fetcher.callCount() > 0 {
			select {
			case f.started <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-ctx.Done():
				return usageResult{}, pollErrorCancelled
			}
		}
		return f.fetcher.fetch(ctx, token, timeout)
	}
	f.runtime = newPluginRuntime(f.host, fetch, f.clock.now)
	f.runtime.applyConfig(f.cfg)
	t.Cleanup(func() {
		f.release()
		f.runtime.shutdown()
	})
	waitFor(t, func() bool {
		return f.runtime.cache.snapshot("auth-a").HasSample && f.idle()
	})
	f.assertCounts(t, 1, 1)
	return f
}

func (f *staleResetFixture) idle() bool {
	f.runtime.refreshMu.Lock()
	defer f.runtime.refreshMu.Unlock()
	return !f.runtime.pendingAll && !f.runtime.inFlightAll && len(f.runtime.pendingIDs) == 0 && len(f.runtime.inFlightIDs) == 0
}

func (f *staleResetFixture) assertCounts(t *testing.T, gets, fetches int) {
	t.Helper()
	_, gotGets := f.host.counts()
	if gotGets != gets || f.fetcher.callCount() != fetches {
		t.Fatalf("host get/fetch calls = %d/%d, want %d/%d", gotGets, f.fetcher.callCount(), gets, fetches)
	}
}

// Observe both the synchronous queue and bounded worker activity for negative
// assertions; merely checking fetch count immediately after pick can miss work.
func (f *staleResetFixture) assertNoRefresh(t *testing.T, gets, fetches int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Millisecond)
	for {
		f.assertCounts(t, gets, fetches)
		if !f.idle() {
			t.Fatal("unexpected refresh queued or in flight")
		}
		if !time.Now().Before(deadline) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func (f *staleResetFixture) pickBlocked(t *testing.T) {
	t.Helper()
	response, decisionError := f.runtime.pick(claudeRequest(candidate("auth-a", 0)))
	if response.Handled || response.AuthID != "" || decisionError == nil || decisionError.Code != exhaustedErrorCode {
		t.Fatalf("still-blocked pick = %#v, error = %#v; want exhausted", response, decisionError)
	}
}

func (f *staleResetFixture) pickEligible(t *testing.T) {
	t.Helper()
	response, decisionError := f.runtime.pick(claudeRequest(candidate("auth-a", 0)))
	if decisionError != nil || !response.Handled || response.AuthID != "auth-a" {
		t.Fatalf("eligible pick = %#v, error = %#v; want handled auth-a", response, decisionError)
	}
}

func (f *staleResetFixture) waitLow(t *testing.T, gets, fetches int) {
	t.Helper()
	waitFor(t, func() bool {
		sample := f.runtime.cache.snapshot("auth-a")
		return sample.HasSample && sample.WeeklyPercentUsed == 5 && sample.SampledAt.Equal(f.clock.now()) && f.idle()
	})
	f.assertCounts(t, gets, fetches)
	if sample := f.runtime.cache.snapshot("auth-a"); sample.blocked(f.clock.now(), f.cfg.CutoffPercentUsed) || sample.LastErrorCategory != "" {
		t.Fatalf("successful low reread did not clear blocked/error state: %#v", sample)
	}
}

func TestStaleResetFirstUseRereadsBeforePollInterval(t *testing.T) {
	for _, afterReset := range []time.Duration{0, time.Nanosecond} {
		t.Run(afterReset.String(), func(t *testing.T) {
			f := newStaleResetFixture(t, "poll-interval: 5m\nblocked-refresh-interval: 0s\n", time.Minute, false,
				fetchReply{result: usageResult{WeeklyPercentUsed: 5, ResetAt: time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)}},
			)
			f.clock.set(f.start.Add(time.Minute - time.Nanosecond))
			f.pickBlocked(t)
			f.assertNoRefresh(t, 1, 1)

			// The sample is only one minute old, but its reset has arrived.
			// Selection must wait for a successful below-cutoff reread.
			f.clock.set(f.start.Add(time.Minute + afterReset))
			statusResponse := f.runtime.handleManagement(pluginapi.ManagementRequest{Method: "GET", Path: managementStatusFullPath})
			var status cutoffStatusResponse
			if err := json.Unmarshal(statusResponse.Body, &status); err != nil {
				t.Fatal(err)
			}
			if statusResponse.StatusCode != 200 || len(status.Accounts) != 1 || status.Accounts[0].Known || !status.Accounts[0].Blocked || status.Accounts[0].WeeklyPercentUsed != nil {
				t.Fatalf("reset-pending must expose unknown but blocked status without a percent: response=%#v status=%#v", statusResponse, status)
			}
			f.pickEligible(t)
			if sample := f.runtime.cache.snapshot("auth-a"); sample.WeeklyPercentUsed != 5 || !sample.SampledAt.Equal(f.clock.now()) {
				t.Fatalf("first-use pick returned before the fresh low read: %#v", sample)
			}
			f.assertCounts(t, 2, 2)
			f.waitLow(t, 2, 2)
			f.pickEligible(t)
			f.assertNoRefresh(t, 2, 2)
		})
	}
}

func TestStaleResetFailedPostResetRereadSelectsVerifiedSeatAndThrottlesFromAttempt(t *testing.T) {
	f := newStaleResetFixture(t, "poll-interval: 5m\nblocked-refresh-interval: 0s\n", time.Minute, false,
		fetchReply{category: pollErrorNetwork},
		fetchReply{result: usageResult{WeeklyPercentUsed: 5, ResetAt: time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)}},
	)
	firstAttempt := f.start.Add(time.Minute)
	f.clock.set(firstAttempt)
	f.host.mu.Lock()
	f.host.entries = append(f.host.entries, physicalEntry("auth-b", "index-b"))
	f.host.authJSON["index-b"] = credentialJSON("token-b")
	f.host.mu.Unlock()
	pickVerified := func() {
		t.Helper()
		f.runtime.cache.recordSuccess("auth-b", 10, f.start.Add(7*24*time.Hour), f.clock.now())
		response, decisionError := f.runtime.pick(claudeRequest(candidate("auth-a", 100), candidate("auth-b", 1)))
		if decisionError != nil || !response.Handled || response.AuthID != "auth-b" {
			t.Fatalf("reset-pending/failed high-priority seat must not displace verified auth-b: response=%#v error=%#v", response, decisionError)
		}
	}
	pickVerified()
	waitFor(t, func() bool {
		return f.runtime.cache.snapshot("auth-a").LastErrorCategory == pollErrorNetwork && f.idle()
	})
	f.assertCounts(t, 2, 2)
	if sample := f.runtime.cache.snapshot("auth-a"); sample.known(f.clock.now()) || !sample.blocked(f.clock.now(), f.cfg.CutoffPercentUsed) || !sample.LastAttemptAt.Equal(firstAttempt) {
		t.Fatalf("failed post-reset reread must remain unknown but ineligible and record its attempt: %#v", sample)
	}
	assertResetOutageWarnings(t, f.host, 0, nil)
	// Repeated picks at reset, at the old sample's poll boundary, and just
	// before the new attempt's boundary must not start a retry storm.
	for _, age := range []time.Duration{time.Minute, 5 * time.Minute, 6*time.Minute - time.Nanosecond} {
		f.clock.set(f.start.Add(age))
		pickVerified()
		f.assertNoRefresh(t, 2, 2)
	}
	f.clock.set(firstAttempt.Add(5 * time.Minute))
	pickVerified()
	f.waitLow(t, 3, 3)
}

func TestStaleResetFreshExhaustedDoesNotReread(t *testing.T) {
	f := newStaleResetFixture(t, "poll-interval: 5m\n", 7*24*time.Hour, false)
	for _, age := range []time.Duration{time.Minute, 6 * time.Minute} {
		f.clock.set(f.start.Add(age))
		f.pickBlocked(t)
		f.assertNoRefresh(t, 1, 1)
	}
}

func TestStaleResetBlockedBackstopBoundaryAndCoalescing(t *testing.T) {
	for _, test := range []struct {
		name  string
		yaml  string
		bound time.Duration
	}{
		{name: "default six hours", yaml: "poll-interval: 5m\n", bound: 6 * time.Hour},
		{name: "configured independently of poll interval", yaml: "poll-interval: 5m\nblocked-refresh-interval: 2m\n", bound: 2 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newStaleResetFixture(t, test.yaml, 7*24*time.Hour, true,
				fetchReply{result: usageResult{WeeklyPercentUsed: 5, ResetAt: time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)}},
			)
			f.clock.set(f.start.Add(test.bound - time.Nanosecond))
			f.pickBlocked(t)
			f.assertNoRefresh(t, 1, 1)

			f.clock.set(f.start.Add(test.bound))
			f.assertNoRefresh(t, 1, 1) // Advancing time alone must not trigger work.
			f.pickBlocked(t)           // At exactly the bound, queue work but do not unblock.
			select {
			case <-f.started:
			case <-time.After(2 * time.Second):
				t.Fatal("blocked backstop did not reach host get/fetch at the exact boundary")
			}
			f.assertCounts(t, 2, 1)
			if sample := f.runtime.cache.snapshot("auth-a"); !sample.blocked(f.clock.now(), f.cfg.CutoffPercentUsed) {
				t.Fatalf("queued reread prematurely unblocked account: %#v", sample)
			}

			// Concurrent request bursts must coalesce while the existing worker is
			// inside fetch, without blocking selection on that fetch.
			var requests sync.WaitGroup
			failures := make(chan pluginapi.SchedulerPickResponse, 32)
			for range 32 {
				requests.Add(1)
				go func() {
					defer requests.Done()
					response, decisionError := f.runtime.pick(claudeRequest(candidate("auth-a", 0)))
					if response.Handled || response.AuthID != "" || decisionError == nil || decisionError.Code != exhaustedErrorCode {
						failures <- response
					}
				}()
			}
			joined := make(chan struct{})
			go func() { requests.Wait(); close(joined) }()
			select {
			case <-joined:
			case <-time.After(2 * time.Second):
				t.Fatal("scheduler requests waited for the gated usage fetch")
			}
			if len(failures) != 0 {
				t.Fatalf("%d requests selected a still-blocked account", len(failures))
			}
			f.assertCounts(t, 2, 1)
			f.release()
			f.waitLow(t, 2, 2)
			f.pickEligible(t)
			f.assertNoRefresh(t, 2, 2)
		})
	}
}

func TestStaleResetBlockedBackstopWhileAnotherAccountIsSelected(t *testing.T) {
	start := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	reset := start.Add(7 * 24 * time.Hour)
	clock := &testClock{value: start}
	host := &fakeHost{
		entries: []pluginapi.HostAuthFileEntry{physicalEntry("auth-a", "index-a"), physicalEntry("auth-b", "index-b")},
		authJSON: map[string]json.RawMessage{
			"index-a": credentialJSON("token-a"),
			"index-b": credentialJSON("token-b"),
		},
	}
	fetcher := &fakeFetcher{replies: map[string][]fetchReply{
		"token-a": {
			{result: usageResult{WeeklyPercentUsed: 80, ResetAt: reset}},
			{result: usageResult{WeeklyPercentUsed: 5, ResetAt: reset}},
		},
		"token-b": {{result: usageResult{WeeklyPercentUsed: 10, ResetAt: reset}}},
	}}
	cfg := staleResetConfig(t, "poll-interval: 5m\nblocked-refresh-interval: 2m\n")
	runtime := newPluginRuntime(host, fetcher.fetch, clock.now)
	runtime.applyConfig(cfg)
	t.Cleanup(runtime.shutdown)
	f := &staleResetFixture{runtime: runtime, host: host, fetcher: fetcher, clock: clock, cfg: cfg, start: start}
	waitFor(t, func() bool {
		return runtime.cache.snapshot("auth-a").HasSample && runtime.cache.snapshot("auth-b").HasSample && f.idle()
	})
	f.assertCounts(t, 2, 2)
	clock.set(start.Add(2 * time.Minute))
	response, decisionError := runtime.pick(claudeRequest(candidate("auth-a", 100), candidate("auth-b", 1)))
	if decisionError != nil || !response.Handled || response.AuthID != "auth-b" {
		t.Fatalf("blocked high-priority auth must remain excluded: response=%#v error=%#v", response, decisionError)
	}
	f.waitLow(t, 3, 3)
	if tokens := fetcher.callTokens(); len(tokens) != 3 || tokens[2] != "token-a" {
		t.Fatalf("backstop must reread blocked auth-a, not fresh selected auth-b: %#v", tokens)
	}
	if sample := runtime.cache.snapshot("auth-b"); !sample.SampledAt.Equal(start) {
		t.Fatalf("fresh selected auth-b was unnecessarily refreshed: %#v", sample)
	}
	f.assertNoRefresh(t, 3, 3)
}

func TestStaleResetBlockedBackstopFailureUsesLatestAttempt(t *testing.T) {
	for _, authGetFails := range []bool{false, true} {
		name := "usage fetch failure"
		if authGetFails {
			name = "host get failure"
		}
		t.Run(name, func(t *testing.T) {
			replies := []fetchReply{{category: pollErrorNetwork}, {result: usageResult{WeeklyPercentUsed: 5, ResetAt: time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)}}}
			category, failedFetches, totalFetches := pollErrorNetwork, 2, 3
			if authGetFails {
				replies = replies[1:]
				category, failedFetches, totalFetches = pollErrorAuthGet, 1, 2
			}
			f := newStaleResetFixture(t, "poll-interval: 5m\nblocked-refresh-interval: 2m\n", 7*24*time.Hour, false, replies...)
			if authGetFails {
				f.host.mu.Lock()
				f.host.getErrors = map[string]error{"index-a": errors.New("fixture reread failure")}
				f.host.mu.Unlock()
			}
			f.clock.set(f.start.Add(2 * time.Minute))
			f.pickBlocked(t)
			waitFor(t, func() bool { return f.runtime.cache.snapshot("auth-a").LastErrorCategory == category && f.idle() })
			f.assertCounts(t, 2, failedFetches)
			sample := f.runtime.cache.snapshot("auth-a")
			if !sample.blocked(f.clock.now(), f.cfg.CutoffPercentUsed) || !sample.SampledAt.Equal(f.start) || !sample.LastAttemptAt.Equal(f.clock.now()) {
				t.Fatalf("failed reread must retain old blocked sample and record new attempt: %#v", sample)
			}
			for _, age := range []time.Duration{3 * time.Minute, 4*time.Minute - time.Nanosecond} {
				f.clock.set(f.start.Add(age))
				f.pickBlocked(t)
				f.assertNoRefresh(t, 2, failedFetches)
			}
			if authGetFails {
				f.host.mu.Lock()
				delete(f.host.getErrors, "index-a")
				f.host.mu.Unlock()
			}
			f.clock.set(f.start.Add(4 * time.Minute))
			f.pickBlocked(t)
			f.waitLow(t, 3, totalFetches)
			f.pickEligible(t)
			f.assertNoRefresh(t, 3, totalFetches)
		})
	}
}

func TestStaleResetBlockedBackstopHighResultUsesLatestSample(t *testing.T) {
	reset := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	f := newStaleResetFixture(t, "poll-interval: 5m\nblocked-refresh-interval: 2m\n", 7*24*time.Hour, false,
		fetchReply{result: usageResult{WeeklyPercentUsed: 60, ResetAt: reset}},
		fetchReply{result: usageResult{WeeklyPercentUsed: 5, ResetAt: reset}},
	)
	f.clock.set(f.start.Add(2 * time.Minute))
	f.pickBlocked(t)
	waitFor(t, func() bool {
		sample := f.runtime.cache.snapshot("auth-a")
		return sample.WeeklyPercentUsed == 60 && sample.SampledAt.Equal(f.clock.now()) && f.idle()
	})
	f.assertCounts(t, 2, 2)
	f.clock.set(f.start.Add(4*time.Minute - time.Nanosecond))
	f.pickBlocked(t)
	f.assertNoRefresh(t, 2, 2)
	f.clock.set(f.start.Add(4 * time.Minute))
	f.pickBlocked(t)
	f.waitLow(t, 3, 3)
	f.pickEligible(t)
}

func TestStaleResetBlockedBackstopZeroDisables(t *testing.T) {
	f := newStaleResetFixture(t, "poll-interval: 5m\nblocked-refresh-interval: 0s\n", 7*24*time.Hour, false)
	for _, age := range []time.Duration{6 * time.Hour, 24 * time.Hour} {
		f.clock.set(f.start.Add(age))
		f.pickBlocked(t)
		f.assertNoRefresh(t, 1, 1)
	}
}

func TestStaleResetBlockedRefreshConfigContract(t *testing.T) {
	for _, test := range []struct {
		name string
		yaml string
		want time.Duration
	}{
		{name: "default", want: 6 * time.Hour},
		{name: "explicit duration", yaml: "blocked-refresh-interval: 2m\n", want: 2 * time.Minute},
		{name: "disabled", yaml: "blocked-refresh-interval: 0s\n", want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := staleResetConfig(t, test.yaml)
			// Reflection checks the requested field without making the base fail
			// compilation; all behavioral configuration goes through YAML decode.
			field := reflect.ValueOf(cfg).FieldByName("BlockedRefreshInterval")
			if !field.IsValid() || field.Type() != reflect.TypeOf(time.Duration(0)) {
				t.Fatal("pluginConfig must expose BlockedRefreshInterval as time.Duration")
			}
			if got := time.Duration(field.Int()); got != test.want {
				t.Fatalf("BlockedRefreshInterval = %s, want %s", got, test.want)
			}
		})
	}
	for _, value := range []string{"-1ns", "-6h", "not-a-duration"} {
		t.Run("invalid "+value, func(t *testing.T) {
			raw, err := json.Marshal(lifecycleRequest{ConfigYAML: []byte("blocked-refresh-interval: " + value + "\n")})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeLifecycleConfig(raw); err == nil {
				t.Fatalf("blocked-refresh-interval %q unexpectedly accepted", value)
			}
		})
	}
	t.Run("registration", func(t *testing.T) {
		count := 0
		for _, field := range pluginRegistration().Metadata.ConfigFields {
			if field.Name == "blocked-refresh-interval" {
				count++
				if field.Type != pluginapi.ConfigFieldTypeString {
					t.Fatalf("blocked-refresh-interval field type = %v, want string", field.Type)
				}
			}
		}
		if count != 1 {
			t.Fatalf("blocked-refresh-interval registration count = %d, want exactly one", count)
		}
	})
}
