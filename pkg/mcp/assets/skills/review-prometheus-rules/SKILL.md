---
name: review-prometheus-rules
description: Audit and improve recording and alerting rules. Use when reviewing alert coverage or rule health, drafting new alerts or recording rules, investigating flapping or noisy alerts, or speeding up slow dashboards with pre-computed expressions; produces complete rule-group YAML.
license: Apache-2.0
compatibility: Requires the tools of a connected Prometheus MCP server
---

# Reviewing and Improving Prometheus Rules

Recording rules pre-compute expensive or repeated expressions into new series; alerting rules turn expressions into notifications. They live together in rule groups and are best designed together -- good alerts are often built on top of good recording rules. This runbook covers auditing the rules that exist, finding gaps, and drafting improvements.

## Getting oriented

- list_rules returns every rule group -- recording and alerting -- with expressions, labels, and per-rule evaluation health.
- list_alerts shows what is firing right now; useful for separating "badly tuned" from "correctly noisy".
- tsdb_stats reveals series counts and cardinality, which tells you whether a pre-aggregation would actually pay off.
- list_targets can surface scraped services that no rule covers at all.
- Prometheus's own metrics expose rule health at runtime: the ALERTS{} series records pending and firing alerts (range_query it to reconstruct an alert's history and spot flapping), rate(prometheus_rule_evaluation_failures_total[5m]) catches rules that fail to evaluate, and prometheus_rule_group_last_duration_seconds approaching the group's interval means evaluation is running out of budget.

## Topics worth exploring

Treat these as starting points and follow what the data shows, not as a fixed checklist:

- Rule health: list_rules reports evaluation health and last error per rule; broken or always-empty rules are quick wins. Test a suspect expression directly by querying.
- Recording rule candidates: expressions repeated across alerts or dashboards, especially rate/sum/histogram_quantile combinations. Verify a candidate returns what you expect before proposing it, e.g. with range_query:
  sum by (job) (rate(http_requests_total{code=~"5.."}[5m])) / sum by (job) (rate(http_requests_total[5m]))
- Alert quality: alert on symptoms rather than causes, keep severity labels meaningful, balance `for` durations against detection speed, and fill in annotations (summary, description, runbook link).
- Coverage gaps: SLOs without multi-window multi-burn-rate alerts, critical metrics without absent() protection, resource exhaustion without capacity alerting.
- Naming conventions: recording rules follow level:metric:operations (e.g. job:http_requests:rate5m); docs_search has the full convention and the rule config schema.

## Suggesting changes

Propose complete rule-group YAML, and sanity-check every expression against live data with query or range_query first. Explain what each rule is for, and recommend validating with promtool and watching rule evaluation health after deployment.
