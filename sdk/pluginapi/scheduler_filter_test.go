package pluginapi

import "testing"

func TestValidateSchedulerExclusions(t *testing.T) {
	req := SchedulerFilterRequest{Candidates: []SchedulerFilterCandidate{{ID: "a", Provider: "claude"}, {ID: "b", Provider: "codex"}}}
	for _, tc := range []struct {
		name string
		ids []string
		valid bool
	}{
		{"none", nil, true}, {"one", []string{"a"}, true}, {"all", []string{"a", "b"}, true},
		{"unknown", []string{"c"}, false}, {"duplicate", []string{"a", "a"}, false},
		{"excess", []string{"a", "b", "c"}, false}, {"empty", []string{""}, false},
		{"whitespace", []string{" a "}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateSchedulerExclusions(req, SchedulerFilterResponse{ExcludedIDs: tc.ids})
			if (err == nil) != tc.valid || (tc.valid && len(got) != len(tc.ids)) {
				t.Fatalf("excluded=%v error=%v valid=%v", got, err, tc.valid)
			}
		})
	}
}
