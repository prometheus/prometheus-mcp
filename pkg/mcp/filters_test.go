// Copyright The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mcp

import (
	"testing"

	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/stretchr/testify/require"
)

func ruleNames(res promv1.RulesResult) map[string][]string {
	out := map[string][]string{}
	for _, g := range res.Groups {
		for _, r := range g.Rules {
			switch v := r.(type) {
			case promv1.AlertingRule:
				out[g.Name] = append(out[g.Name], v.Name)
			case promv1.RecordingRule:
				out[g.Name] = append(out[g.Name], v.Name)
			}
		}
	}
	return out
}

func TestListRulesInputFilter(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		input ListRulesInput
		want  map[string][]string
	}{
		{
			name:  "no filters returns everything",
			input: ListRulesInput{},
			want: map[string][]string{
				"alerts":     {"HighErrorRate", "InstanceDown", "job:http_requests:rate5m"},
				"idle_group": {"idle:rule"},
			},
		},
		{
			name:  "type recording",
			input: ListRulesInput{Type: "recording"},
			want: map[string][]string{
				"alerts":     {"job:http_requests:rate5m"},
				"idle_group": {"idle:rule"},
			},
		},
		{
			name:  "state excludes recording rules",
			input: ListRulesInput{State: "inactive"},
			want:  map[string][]string{"alerts": {"InstanceDown"}},
		},
		{
			name:  "health",
			input: ListRulesInput{Health: "err"},
			want:  map[string][]string{"idle_group": {"idle:rule"}},
		},
		{
			name:  "rule group",
			input: ListRulesInput{RuleGroup: "idle_group"},
			want:  map[string][]string{"idle_group": {"idle:rule"}},
		},
		{
			name:  "rule name",
			input: ListRulesInput{RuleName: "InstanceDown"},
			want:  map[string][]string{"alerts": {"InstanceDown"}},
		},
		{
			name:  "filters combine with AND",
			input: ListRulesInput{Type: "alerting", RuleGroup: "idle_group"},
			want:  map[string][]string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, tc.input.validate())
			require.Equal(t, tc.want, ruleNames(tc.input.filter(filterTestRules())))
		})
	}
}

func TestListRulesInputValidate(t *testing.T) {
	t.Parallel()
	require.ErrorContains(t, ListRulesInput{Type: "alert"}.validate(), `invalid type "alert"`)
	require.ErrorContains(t, ListRulesInput{State: "FIRING"}.validate(), `invalid state "FIRING"`)
	require.ErrorContains(t, ListRulesInput{Health: "bad"}.validate(), `invalid health "bad"`)
}

func TestListTargetsInputFilter(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		input       ListTargetsInput
		wantActive  int
		wantDropped int
	}{
		{"no filters returns everything", ListTargetsInput{}, 3, 1},
		{"state any", ListTargetsInput{State: "any"}, 3, 1},
		{"state active", ListTargetsInput{State: "active"}, 3, 0},
		{"state dropped", ListTargetsInput{State: "dropped"}, 0, 1},
		{"health", ListTargetsInput{Health: "up"}, 1, 0},
		{"scrape pool", ListTargetsInput{ScrapePool: "node"}, 2, 0},
		{"health and pool", ListTargetsInput{Health: "down", ScrapePool: "node"}, 1, 0},
		{"unknown pool", ListTargetsInput{ScrapePool: "nope"}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, tc.input.validate())
			got := tc.input.filter(filterTestTargets())
			require.Len(t, got.Active, tc.wantActive)
			require.Len(t, got.Dropped, tc.wantDropped)
		})
	}
}

func TestListTargetsInputValidate(t *testing.T) {
	t.Parallel()
	require.ErrorContains(t, ListTargetsInput{State: "gone"}.validate(), `invalid state "gone"`)
	require.ErrorContains(t, ListTargetsInput{Health: "ok"}.validate(), `invalid health "ok"`)
}
