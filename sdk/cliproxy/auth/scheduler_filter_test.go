package auth

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type filteringScheduler struct {
	fakePluginScheduler
	excluded []string
	filter func(pluginapi.SchedulerFilterRequest) (pluginapi.SchedulerFilterResponse, error)
	requests []pluginapi.SchedulerFilterRequest
}

func (*filteringScheduler) HasSchedulerFilter() bool { return true }
func (s *filteringScheduler) FilterAuths(_ context.Context, req pluginapi.SchedulerFilterRequest) (pluginapi.SchedulerFilterResponse, error) {
	s.requests = append(s.requests, req)
	if s.filter != nil { return s.filter(req) }
	return pluginapi.SchedulerFilterResponse{ExcludedIDs: s.excluded}, nil
}

func filterManager(t *testing.T, selector Selector, auths ...*Auth) (*Manager, *filteringScheduler) {
	t.Helper()
	m := NewManager(nil, selector, nil)
	for _, auth := range auths {
		m.RegisterExecutor(schedulerTestExecutor{provider: auth.Provider})
		if _, err := m.Register(WithSkipPersist(context.Background()), auth); err != nil { t.Fatal(err) }
	}
	f := &filteringScheduler{}
	m.SetPluginScheduler(f)
	return m, f
}

func TestSchedulerFilterPrecedesPriorityAndRetainsNativeSelection(t *testing.T) {
	for _, delegate := range []string{"", pluginapi.SchedulerBuiltinRoundRobin, pluginapi.SchedulerBuiltinFillFirst} {
		t.Run("delegate="+delegate, func(t *testing.T) {
			m, f := filterManager(t, &RoundRobinSelector{},
				&Auth{ID: "high", Provider: "claude", Attributes: map[string]string{"priority": "100"}},
				&Auth{ID: "a", Provider: "claude"}, &Auth{ID: "b", Provider: "claude"})
			f.excluded = []string{"high"}
			if delegate != "" {
				f.resp = pluginapi.SchedulerPickResponse{Handled: true, DelegateBuiltin: delegate}
				f.handled = true
			}
			seen := map[string]bool{}
			for range 4 {
				got, _, err := m.pickNext(context.Background(), "claude", "", cliproxyexecutor.Options{}, nil)
				if err != nil || got == nil || got.ID == "high" { t.Fatalf("got=%v error=%v", got, err) }
				seen[got.ID] = true
			}
			if delegate != pluginapi.SchedulerBuiltinFillFirst && len(seen) != 2 { t.Fatalf("native distribution lost: %v", seen) }
			if len(f.requests) != 4 || len(f.requests[0].Candidates) != 3 { t.Fatalf("filter was priority narrowed: %v", f.requests) }
			for _, req := range f.fakePluginScheduler.requests {
				for _, candidate := range req.Candidates { if candidate.ID == "high" { t.Fatal("legacy scheduler saw excluded ID") } }
			}
		})
	}
}

func TestSchedulerFilterRetainsPinningRetryAndHostEligibility(t *testing.T) {
	m, f := filterManager(t, &FillFirstSelector{},
		&Auth{ID: "a", Provider: "claude"}, &Auth{ID: "b", Provider: "claude"},
		&Auth{ID: "disabled", Provider: "claude", Disabled: true},
		&Auth{ID: "cooling", Provider: "claude", Unavailable: true, NextRetryAfter: time.Now().Add(time.Hour)})
	f.filter = func(req pluginapi.SchedulerFilterRequest) (pluginapi.SchedulerFilterResponse, error) {
		// Reentry demonstrates the filter is outside Manager.mu.
		m.mu.Lock(); m.mu.Unlock()
		for _, c := range req.Candidates {
			if c.ID == "disabled" || c.ID == "cooling" { t.Fatalf("host-ineligible candidate offered: %s", c.ID) }
		}
		return pluginapi.SchedulerFilterResponse{}, nil
	}
	got, _, err := m.pickNext(context.Background(), "claude", "", cliproxyexecutor.Options{}, map[string]struct{}{"a": {}})
	if err != nil || got == nil || got.ID != "b" { t.Fatalf("retry got=%v error=%v", got, err) }
	f.filter = nil
	f.excluded = []string{"a"}
	opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.PinnedAuthMetadataKey: "a"}}
	_, _, err = m.pickNext(context.Background(), "claude", "", opts, nil)
	var quotaErr *Error
	if !errors.As(err, &quotaErr) || quotaErr.Code != "quota_router_exhausted" { t.Fatalf("pinned block escaped: %v", err) }
}

func TestSchedulerFilterMixedProvidersAndInvalidResponse(t *testing.T) {
	m, f := filterManager(t, &FillFirstSelector{}, &Auth{ID: "claude", Provider: "claude"}, &Auth{ID: "codex", Provider: "codex"})
	f.excluded = []string{"claude"}
	f.resp = pluginapi.SchedulerPickResponse{Handled: true, DelegateBuiltin: pluginapi.SchedulerBuiltinRoundRobin}
	f.handled = true
	got, _, provider, err := m.pickNextMixed(context.Background(), []string{"claude", "codex"}, "", cliproxyexecutor.Options{}, nil)
	if err != nil || got == nil || got.ID != "codex" || provider != "codex" { t.Fatalf("mixed got=%v provider=%s error=%v", got, provider, err) }
	for _, excluded := range [][]string{{"foreign"}, {"claude", "claude"}, {"claude", "codex", "extra"}, {"claude", "codex"}} {
		f.excluded = excluded
		got, _, _, err = m.pickNextMixed(context.Background(), []string{"claude", "codex"}, "", cliproxyexecutor.Options{}, nil)
		if err == nil || got != nil { t.Fatalf("invalid/all-excluded response fell back: %v, %v", got, err) }
	}
}

func TestSchedulerFilterErrorCannotReachSelection(t *testing.T) {
	selector := &trackingSelector{}
	m, f := filterManager(t, selector, &Auth{ID: "a", Provider: "claude"}, &Auth{ID: "b", Provider: "codex"})
	f.filter = func(pluginapi.SchedulerFilterRequest) (pluginapi.SchedulerFilterResponse, error) {
		return pluginapi.SchedulerFilterResponse{}, errors.New("malformed filter RPC result")
	}
	if got, _, err := m.pickNext(context.Background(), "claude", "", cliproxyexecutor.Options{}, nil); err == nil || got != nil {
		t.Fatalf("single-provider filter error selected: %v %v", got, err)
	}
	if got, _, _, err := m.pickNextMixed(context.Background(), []string{"claude", "codex"}, "", cliproxyexecutor.Options{}, nil); err == nil || got != nil {
		t.Fatalf("mixed-provider filter error selected: %v %v", got, err)
	}
	if len(f.requests) != 2 || selector.calls != 0 || len(f.fakePluginScheduler.requests) != 0 {
		t.Fatal("filter error reached native or legacy selection")
	}
}

func TestSchedulerFilterPreservesNativeAffinityAcrossPriorityRecovery(t *testing.T) {
	affinity := NewSessionAffinitySelectorWithConfig(SessionAffinityConfig{Fallback: &RoundRobinSelector{}, TTL: time.Hour})
	defer affinity.Stop()
	m, f := filterManager(t, affinity, &Auth{ID: "high", Provider: "claude", Status: StatusActive, Attributes: map[string]string{"priority": "100"}}, &Auth{ID: "low", Provider: "claude", Status: StatusActive})
	opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.DerivedSessionIDMetadataKey: "fixture-session"}}
	f.excluded = []string{"high"}
	first, _, err := m.pickNext(context.Background(), "claude", "", opts, nil)
	if err != nil || first == nil || first.ID != "low" { t.Fatalf("first=%v error=%v", first, err) }
	f.excluded = nil
	second, _, err := m.pickNext(context.Background(), "claude", "", opts, nil)
	if err != nil || second == nil || second.ID != "low" { t.Fatalf("affinity lost after recovery: %v error=%v", second, err) }
	f.excluded = []string{"low"}
	third, _, err := m.pickNext(context.Background(), "claude", "", opts, nil)
	if err != nil || third == nil || third.ID != "high" { t.Fatalf("excluded affinity resurrected: %v error=%v", third, err) }
}

func TestSchedulerFilterCannotRewriteItsOfferedSet(t *testing.T) {
	m, f := filterManager(t, &FillFirstSelector{}, &Auth{ID: "a", Provider: "claude"})
	f.filter = func(req pluginapi.SchedulerFilterRequest) (pluginapi.SchedulerFilterResponse, error) {
		req.Candidates[0].ID = "foreign"
		return pluginapi.SchedulerFilterResponse{ExcludedIDs: []string{"foreign"}}, nil
	}
	if got, _, err := m.pickNext(context.Background(), "claude", "", cliproxyexecutor.Options{}, nil); err == nil || got != nil {
		t.Fatalf("mutated offer accepted: %v error=%v", got, err)
	}
}

func TestSchedulerFilterHomeIsExplicitlyUnsupported(t *testing.T) {
	m, _ := filterManager(t, &FillFirstSelector{}, &Auth{ID: "a", Provider: "claude"})
	m.SetConfig(&internalconfig.Config{Home: internalconfig.HomeConfig{Enabled: true}})
	_, _, err := m.pickNext(context.Background(), "claude", "", cliproxyexecutor.Options{}, nil)
	var authErr *Error
	if !errors.As(err, &authErr) || authErr.Code != "scheduler_filter_unsupported" { t.Fatalf("Home bypassed filter: %v", err) }
}

func TestSchedulerFilterScopeSurvivesNativeResyncAndDoesNotLeak(t *testing.T) {
	m, f := filterManager(t, &FillFirstSelector{}, &Auth{ID: "a", Provider: "claude"}, &Auth{ID: "b", Provider: "claude"})
	ctx, _, _, err := filterAvailableAuths(context.Background(), f, &FillFirstSelector{}, "exact-alias(high)", []*Auth{{ID: "b", Provider: "claude"}})
	if err != nil { t.Fatal(err) }
	if f.requests[0].Model != "exact-alias(high)" || !reflect.DeepEqual(f.requests[0].Candidates, []pluginapi.SchedulerFilterCandidate{{ID: "b", Provider: "claude"}}) { t.Fatalf("request changed: %v", f.requests[0]) }
	m.syncScheduler()
	got, _, err := m.pickViaBuiltinScheduler(ctx, schedulerStrategyFillFirst, "claude", nil, "", cliproxyexecutor.Options{}, nil)
	if err != nil || got.ID != "b" { t.Fatalf("resync escaped scope: %v error=%v", got, err) }
	got, _, err = m.pickNext(context.Background(), "claude", "", cliproxyexecutor.Options{}, nil)
	if err != nil || got.ID != "a" { t.Fatalf("scope leaked to next request: %v error=%v", got, err) }
}
