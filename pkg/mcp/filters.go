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
	"fmt"
	"slices"

	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
)

var (
	ruleTypes     = []string{"alerting", "recording"}
	ruleStates    = []string{"firing", "pending", "inactive"}
	ruleHealths   = []string{"ok", "err", "unknown"}
	targetStates  = []string{"active", "dropped", "any"}
	targetHealths = []string{"up", "down", "unknown"}
)

// checkEnum returns an error if value is set and not one of allowed.
func checkEnum(field, value string, allowed []string) error {
	if value == "" || slices.Contains(allowed, value) {
		return nil
	}
	return fmt.Errorf("invalid %s %q: must be one of %v", field, value, allowed)
}

// validate checks that the enumerated filters hold known values.
func (lri ListRulesInput) validate() error {
	for _, c := range []struct {
		field, value string
		allowed      []string
	}{
		{"type", lri.Type, ruleTypes},
		{"state", lri.State, ruleStates},
		{"health", lri.Health, ruleHealths},
	} {
		if err := checkEnum(c.field, c.value, c.allowed); err != nil {
			return err
		}
	}
	return nil
}

// filter returns the subset of res that satisfies the filters. Groups left
// without rules are dropped, but only when a rule-level filter is set, so an
// unfiltered call returns the API response unchanged.
func (lri ListRulesInput) filter(res promv1.RulesResult) promv1.RulesResult {
	if lri.Type == "" && lri.State == "" && lri.Health == "" && lri.RuleName == "" && lri.RuleGroup == "" {
		return res
	}

	groups := make([]promv1.RuleGroup, 0, len(res.Groups))
	for _, g := range res.Groups {
		if lri.RuleGroup != "" && g.Name != lri.RuleGroup {
			continue
		}
		rules := make(promv1.Rules, 0, len(g.Rules))
		for _, r := range g.Rules {
			if lri.keepRule(r) {
				rules = append(rules, r)
			}
		}
		if len(rules) == 0 {
			continue
		}
		g.Rules = rules
		groups = append(groups, g)
	}
	return promv1.RulesResult{Groups: groups}
}

func (lri ListRulesInput) keepRule(r any) bool {
	var name, ruleType, state string
	var health promv1.RuleHealth
	switch v := r.(type) {
	case promv1.AlertingRule:
		name, ruleType, state, health = v.Name, "alerting", v.State, v.Health
	case promv1.RecordingRule:
		name, ruleType, health = v.Name, "recording", v.Health
	default:
		return false
	}

	switch {
	case lri.Type != "" && lri.Type != ruleType:
		return false
	// Only alerting rules have a state.
	case lri.State != "" && lri.State != state:
		return false
	case lri.Health != "" && lri.Health != string(health):
		return false
	case lri.RuleName != "" && lri.RuleName != name:
		return false
	}
	return true
}

// validate checks that the enumerated filters hold known values.
func (lti ListTargetsInput) validate() error {
	if err := checkEnum("state", lti.State, targetStates); err != nil {
		return err
	}
	return checkEnum("health", lti.Health, targetHealths)
}

// filter returns the subset of res that satisfies the filters. Dropped targets
// carry neither health nor scrape pool, so they are omitted when either filter
// is set.
func (lti ListTargetsInput) filter(res promv1.TargetsResult) promv1.TargetsResult {
	if lti.State == "" && lti.Health == "" && lti.ScrapePool == "" {
		return res
	}

	out := promv1.TargetsResult{
		Active:  []promv1.ActiveTarget{},
		Dropped: []promv1.DroppedTarget{},
	}
	if lti.State != "dropped" {
		for _, t := range res.Active {
			if lti.Health != "" && lti.Health != string(t.Health) {
				continue
			}
			if lti.ScrapePool != "" && lti.ScrapePool != t.ScrapePool {
				continue
			}
			out.Active = append(out.Active, t)
		}
	}
	if lti.State != "active" && lti.Health == "" && lti.ScrapePool == "" {
		out.Dropped = res.Dropped
	}
	return out
}
