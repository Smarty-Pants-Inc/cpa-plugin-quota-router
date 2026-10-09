package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const resetOutageMessage = "quota host unavailable; routing on unverified post-reset seats"

// These tests only use interfaces present on 37c418d4 so changed behavior can
// be demonstrated on that base, not merely by a compilation failure.
func assertResetOutageWarnings(t *testing.T, host *fakeHost, want int, failures map[string]string) {
	t.Helper()
	host.mu.Lock()
	logs := append([]string(nil), host.logs...)
	host.mu.Unlock()
	count := 0
	for _, raw := range logs {
		var entry struct {
			Level   string `json:"level"`
			Message string `json:"message"`
			Fields  struct {
				Seats []string          `json:"seats"`
				Error map[string]string `json:"error"`
			} `json:"fields"`
		}
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Message != resetOutageMessage {
			continue
		}
		count++
		if entry.Level != "warn" || !reflect.DeepEqual(entry.Fields.Error, failures) {
			t.Fatalf("outage WARN must name safe errors: %s", raw)
		}
		if !reflect.DeepEqual(entry.Fields.Seats, []string{"auth-a", "auth-b"}) {
			t.Fatalf("outage WARN must name all seats in stable order: %s", raw)
		}
	}
	if count != want {
		t.Fatalf("outage WARN count = %d, want %d; logs:\n%s", count, want, strings.Join(logs, "\n"))
	}
}

func TestResetPendingPendingRereadIsSynchronouslyJoinedAndCoalesced(t *testing.T) {
	f := newStaleResetFixture(t, "poll-interval: 5m\n", time.Minute, true,
		fetchReply{result: usageResult{WeeklyPercentUsed: 5, ResetAt: time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)}},
	)
	f.clock.set(f.start.Add(time.Minute))
	// Simulate work queued by another request. The picks must join the existing
	// gated worker read, not select on stale data or issue additional fetches.
	f.runtime.queueCandidateRefresh("auth-a", f.cfg, f.clock.now())
	select {
	case <-f.started:
	case <-time.After(time.Second):
		t.Fatal("pending reread never reached the fetch path")
	}
	type decision struct {
		response pluginapi.SchedulerPickResponse
		err      *envelopeError
	}
	results := make(chan decision, 32)
	var requests sync.WaitGroup
	for range 32 {
		requests.Add(1)
		go func() {
			defer requests.Done()
			response, err := f.runtime.pick(claudeRequest(candidate("auth-a", 0)))
			results <- decision{response, err}
		}()
	}
	select {
	case result := <-results:
		t.Fatalf("pick returned before pending fresh read completed: %#v", result)
	case <-time.After(30 * time.Millisecond):
	}
	f.assertCounts(t, 2, 1)
	f.release()
	requests.Wait()
	for range 32 {
		result := <-results
		if result.err != nil || !result.response.Handled || result.response.AuthID != "auth-a" {
			t.Fatalf("successful synchronous read did not enable seat: %#v", result)
		}
	}
	f.waitLow(t, 2, 2)
	assertResetOutageWarnings(t, f.host, 0, nil)
}

func TestResetPendingFirstUseWaitsForFreshRead(t *testing.T) {
	f := newStaleResetFixture(t, "poll-interval: 5m\n", time.Minute, true,
		fetchReply{result: usageResult{WeeklyPercentUsed: 5, ResetAt: time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)}},
	)
	f.clock.set(f.start.Add(time.Minute))
	returned := make(chan struct{})
	var response pluginapi.SchedulerPickResponse
	var decisionError *envelopeError
	go func() {
		response, decisionError = f.runtime.pick(claudeRequest(candidate("auth-a", 0)))
		close(returned)
	}()
	select {
	case <-f.started:
	case <-time.After(time.Second):
		t.Fatal("first-use pick did not start a fresh read")
	}
	select {
	case <-returned:
		t.Fatalf("first-use pick routed before fresh read succeeded: response=%#v error=%#v", response, decisionError)
	case <-time.After(30 * time.Millisecond):
	}
	f.release()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("first-use pick did not return after successful read")
	}
	if decisionError != nil || !response.Handled || response.AuthID != "auth-a" {
		t.Fatalf("fresh low read must enable first-use pick: response=%#v error=%#v", response, decisionError)
	}
	f.waitLow(t, 2, 2)
	assertResetOutageWarnings(t, f.host, 0, nil)
}

func TestResetPendingFreshExhaustedNeverEligible(t *testing.T) {
	for _, pastReset := range []bool{false, true} {
		name := "future reset"
		reset := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
		if pastReset {
			name = "host still reports passed reset"
			reset = time.Date(2026, time.July, 26, 12, 1, 0, 0, time.UTC)
		}
		t.Run(name, func(t *testing.T) {
			f := newStaleResetFixture(t, "poll-interval: 5m\n", time.Minute, false,
				fetchReply{result: usageResult{WeeklyPercentUsed: 100, ResetAt: reset}},
			)
			f.clock.set(f.start.Add(time.Minute + time.Nanosecond))
			f.pickBlocked(t)
			waitFor(t, f.idle)
			f.assertCounts(t, 2, 2)
			f.pickBlocked(t)
			f.assertNoRefresh(t, 2, 2)
			assertResetOutageWarnings(t, f.host, 0, nil)
		})
	}
}

func newResetPendingPair(t *testing.T, second fetchReply) *staleResetFixture {
	t.Helper()
	start := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	f := &staleResetFixture{
		host: &fakeHost{
			entries:  []pluginapi.HostAuthFileEntry{physicalEntry("auth-a", "index-a"), physicalEntry("auth-b", "index-b")},
			authJSON: map[string]json.RawMessage{"index-a": credentialJSON("token-a"), "index-b": credentialJSON("token-b")},
		},
		clock: &testClock{value: start}, cfg: defaultPluginConfig(), start: start,
	}
	initial := fetchReply{result: usageResult{WeeklyPercentUsed: 100, ResetAt: start.Add(time.Minute)}}
	f.fetcher = &fakeFetcher{replies: map[string][]fetchReply{
		"token-a": {initial, {category: pollErrorNetwork}},
		"token-b": {initial, second},
	}}
	f.runtime = newPluginRuntime(f.host, f.fetcher.fetch, f.clock.now)
	f.runtime.applyConfig(f.cfg)
	t.Cleanup(f.runtime.shutdown)
	waitFor(t, func() bool {
		return f.runtime.cache.snapshot("auth-a").HasSample && f.runtime.cache.snapshot("auth-b").HasSample && f.idle()
	})
	f.assertCounts(t, 2, 2)
	f.clock.set(start.Add(time.Minute))
	return f
}

func TestResetPendingAllHostDownFallbackWarnsOncePerPick(t *testing.T) {
	for _, failure := range []string{pollErrorNetwork, "auth_get", "auth_list"} {
		t.Run(failure, func(t *testing.T) {
			f := newResetPendingPair(t, fetchReply{category: pollErrorNetwork})
			f.host.mu.Lock()
			if failure == "auth_get" {
				f.host.getErrors = map[string]error{"index-a": errors.New("fixture host down"), "index-b": errors.New("fixture host down")}
			}
			if failure == "auth_list" {
				f.host.listError = errors.New("fixture host down")
			}
			f.host.mu.Unlock()
			for pick := 1; pick <= 2; pick++ {
				// Unknown policy retains the usual priority/lexical ordering.
				priorityB, wantID := 10, "auth-a"
				if pick == 2 {
					priorityB, wantID = 100, "auth-b"
				}
				response, decisionError := f.runtime.pick(claudeRequest(candidate("auth-b", priorityB), candidate("auth-a", 10)))
				if decisionError != nil || !response.Handled || response.AuthID != wantID {
					t.Fatalf("all-host-down fallback pick = %#v error=%#v", response, decisionError)
				}
				assertResetOutageWarnings(t, f.host, pick, map[string]string{"auth-a": failure, "auth-b": failure})
				for _, id := range []string{"auth-a", "auth-b"} {
					if sample := f.runtime.cache.snapshot(id); !sample.blocked(f.clock.now(), f.cfg.CutoffPercentUsed) || sample.LastErrorCategory != failure {
						t.Fatalf("outage fallback must not clear cached block: id=%s sample=%#v", id, sample)
					}
				}
			}
			gets, fetches := 4, 4
			if failure == "auth_get" {
				fetches = 2
			}
			if failure == "auth_list" {
				gets, fetches = 2, 2
			}
			f.assertNoRefresh(t, gets, fetches)
		})
	}
}

func TestResetPendingMixedFetchFailureAndFreshExhaustedNeverFallback(t *testing.T) {
	f := newResetPendingPair(t, fetchReply{result: usageResult{WeeklyPercentUsed: 100, ResetAt: time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)}})
	response, decisionError := f.runtime.pick(claudeRequest(candidate("auth-a", 100), candidate("auth-b", 1)))
	if response.Handled || decisionError == nil || decisionError.Code != exhaustedErrorCode {
		t.Fatalf("one fresh exhausted read must prevent all-seat outage fallback: response=%#v error=%#v", response, decisionError)
	}
	waitFor(t, f.idle)
	f.assertCounts(t, 4, 4)
	assertResetOutageWarnings(t, f.host, 0, nil)
}

func TestResetPendingWaitIsBoundedAndUnfinishedReadCannotFallback(t *testing.T) {
	f := newStaleResetFixture(t, "poll-interval: 5m\nrequest-timeout: 30ms\n", time.Minute, true)
	f.clock.set(f.start.Add(time.Minute))
	// An uncooperative fetch ignores cancellation. Selection must nevertheless
	// return within its own bound, fail closed, and leave the read coalesced.
	originalFetch := f.runtime.fetch
	f.runtime.shutdown()
	started, release := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	f.runtime.fetch = func(ctx context.Context, token string, timeout time.Duration) (usageResult, string) {
		started <- struct{}{}
		<-release
		return originalFetch(ctx, token, timeout)
	}
	// Keep the exhausted cache; restarting the worker queues an all-auth read.
	f.runtime.applyConfig(f.cfg)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("gated fetch did not start")
	}
	start := time.Now()
	f.pickBlocked(t)
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Fatalf("bounded synchronous wait took %s", elapsed)
	}
	f.assertCounts(t, 2, 1)
	assertResetOutageWarnings(t, f.host, 0, nil)
	unblock()
	f.release()
}

func TestResetPendingMissingCredentialDoesNotTriggerOutageFallback(t *testing.T) {
	f := newStaleResetFixture(t, "poll-interval: 5m\n", time.Minute, false)
	f.host.mu.Lock()
	f.host.authJSON["index-a"] = json.RawMessage(`{"type":"claude"}`)
	f.host.mu.Unlock()
	f.clock.set(f.start.Add(time.Minute))
	f.pickBlocked(t)
	waitFor(t, f.idle)
	if sample := f.runtime.cache.snapshot("auth-a"); sample.LastErrorCategory != pollErrorMissingToken || !sample.blocked(f.clock.now(), f.cfg.CutoffPercentUsed) {
		t.Fatalf("missing credential must remain blocked: %#v", sample)
	}
	assertResetOutageWarnings(t, f.host, 0, nil)
}
