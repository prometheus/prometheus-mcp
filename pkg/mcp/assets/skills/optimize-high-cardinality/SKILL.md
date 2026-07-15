---
name: optimize-high-cardinality
description: Find and reduce high-cardinality metrics and labels. Use when series counts or memory grow, queries slow down, or tsdb_stats shows label explosions; pinpoints offending labels and proposes relabeling, dropping, and aggregation fixes.
license: Apache-2.0
compatibility: Requires the tools of a connected Prometheus MCP server
---

# Exploring, Identifying, and Optimizing High-Cardinality Metrics

Cardinality (the number of unique label combinations) drives Prometheus memory, storage, and query cost. Find the metrics and labels responsible for series explosions and propose fixes that keep the signal while shedding the cost.

## Getting oriented

- tsdb_stats is the primary instrument: seriesCountByMetricName ranks the biggest metrics, labelValueCountByLabelName exposes labels with huge value sets, and memoryInBytesByLabelName shows what they cost.
- series and label_values let you inspect a suspect metric's actual label combinations and values.
- targets_metadata shows which targets expose a metric, i.e. where a fix would need to be applied.
- Prometheus's own TSDB metrics track cardinality live: range_query prometheus_tsdb_head_series for overall growth, rate(prometheus_tsdb_head_series_created_total[5m]) for churn, and topk(10, scrape_series_added) to attribute new series to the targets creating them.

## Topics worth exploring

Treat these as starting points and follow the biggest numbers:

- Which label drives a metric's cardinality: compare per-label series counts, e.g. with query:
  count by (path) (http_requests_total)
  Repeat for other labels; the grouping that yields the most series is the driver.
- Unbounded label value review: IDs, UUIDs, URL paths with embedded parameters, timestamps, etc. label_values on a suspect label makes these obvious.
- Growth over time: range_query count(<metric>) to see whether cardinality is stable, stepped (deploys), or unbounded (leaking labels).

## Suggesting changes

Quantify each proposal's impact (series before vs after) since advice depends on results of investigation, provide concrete metric_relabel_configs or recording rule YAML or suggested next actions.
