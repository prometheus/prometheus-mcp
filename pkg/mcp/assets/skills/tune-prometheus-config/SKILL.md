---
name: tune-prometheus-config
description: Review and tune Prometheus configuration and performance. Use for scrape interval, retention, storage, relabeling, flag, and capacity questions, or when Prometheus itself is slow or resource-hungry; recommends concrete config changes.
license: Apache-2.0
compatibility: Requires the tools of a connected Prometheus MCP server
---

# Optimizing Prometheus Configuration and Performance

Assess whether Prometheus is configured sensibly for its actual load: scrape configuration, storage and retention, query behavior, and resource headroom. The goal is a small set of concrete, justified configuration changes -- not a tour of every setting.

## Getting oriented

- config returns the full scrape and evaluation configuration; flags the complete command line; runtime_info retention and Go runtime settings; build_info the version.
- tsdb_stats quantifies the load: series counts, cardinality, and which metrics and labels dominate memory.
- query and range_query against Prometheus's own metrics (prometheus_*, process_*, go_*) show how it is coping with that load.
- Prometheus's storage metrics show how the TSDB is keeping up: prometheus_tsdb_compaction_duration_seconds and prometheus_tsdb_wal_fsync_duration_seconds reflect disk pressure, and nonzero prometheus_tsdb_compactions_failed_total or prometheus_tsdb_wal_corruptions_total need attention before any tuning.

## Topics worth exploring

Treat these as starting points and dig where the numbers look off:

- Ingestion pressure: samples/sec and live series, e.g. with query:
  rate(prometheus_tsdb_head_samples_appended_total[5m]) and prometheus_tsdb_head_series
  Combined with retention, these drive disk and memory needs.
- Scrape economics: jobs scraped more often than their data is useful, targets with very large sample counts, and relabeling opportunities to cut cardinality at ingest (the optimize-high-cardinality runbook digs deeper).
- Rule evaluation: prometheus_rule_group_last_duration_seconds approaching the group's interval means rule groups are running out of budget.
- Query behavior: --query.timeout, --query.max-samples, and --query.max-concurrency vs observed load; the query log (if enabled in config) identifies slow queries, and repeated expensive expressions are recording rule candidates (see review-prometheus-rules).
- Runtime tuning: GOGC and GOMAXPROCS vs allocated resources, and whether --auto-gomaxprocs / --auto-gomemlimit are available but unused.
- Retention: --storage.tsdb.retention.time/.size vs actual disk usage and how far back queries actually reach.

## Suggesting changes

Provide specific prometheus.yml snippets or flag changes with the reasoning and expected impact. Recommend validating with promtool check config, applying via reload where available, and rolling out incrementally while watching Prometheus's own metrics for the effect.
