---
name: check-system-health
description: Health-check the Prometheus server and its targets. Use for status, uptime, or is-monitoring-OK questions; reviews readiness, active alerts, target health, TSDB stats, and runtime info.
license: Apache-2.0
compatibility: Requires the tools of a connected Prometheus MCP server
---

# Understanding System Health

Build an overall picture of whether Prometheus itself is healthy and whether it is successfully monitoring what it should. A good health check covers the server, its targets, and the data being collected.

## Getting oriented

- healthy and ready report whether the server is up and able to serve queries.
- build_info and runtime_info identify the version, storage retention, and runtime settings; flags exposes the full command line.
- list_targets shows every scrape target and its health; list_alerts shows what is actively firing.
- tsdb_stats summarizes series counts, cardinality, and ingestion (the load side of the picture).
- query Prometheus's self-monitoring metrics directly: rate(prometheus_tsdb_head_samples_appended_total[5m]) shows ingestion throughput, and counters like prometheus_rule_evaluation_failures_total or prometheus_notifications_dropped_total should sit at zero -- any steady rate on them is a problem in itself.

## Topics worth exploring

Treat these as starting points and follow what the data shows:

- Firing alerts: anything already alerting is the fastest pointer to known problems; the labels identify the affected services.
- Target health: down or flapping targets in list_targets mean missing data, and their lastError usually says why.
- Scrape performance: how close scrapes run to their timeout, e.g. with query:
  topk(10, scrape_duration_seconds)
- TSDB pressure: series counts and per-metric cardinality from tsdb_stats; sudden growth is an early warning sign.
- Prometheus's own resources: if node_exporter or cadvisor metrics are available, check CPU, memory, and disk headroom, e.g.:
  process_resident_memory_bytes{job="prometheus"}
- Data freshness: confirm critical metrics are current, e.g. query up, or time() - timestamp(<metric>) to spot staleness.

## Reporting findings

Summarize server status, alert and target health, and TSDB load, and call out anything that needs attention together with the evidence that shows it.
