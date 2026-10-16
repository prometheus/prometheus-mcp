---
name: troubleshoot-missing-data
description: Diagnose missing metrics, absent series, or scrape gaps. Use when queries unexpectedly return empty, a metric stopped reporting, a target is down or unscraped, or dashboards show no data; walks target health, metadata, and config checks.
license: Apache-2.0
compatibility: Requires the tools of a connected Prometheus MCP server
---

# Troubleshooting Missing Data

Modified 2026-10-11: clarify metadata, sample presence and stale-marker handling.

Work out why an expected metric or series is absent: the target is not being scraped, the target no longer exposes it, the labels changed, or the query itself misses data that exists. Narrow down where the pipeline breaks.

## Getting oriented

- list_targets shows scrape target health, the lastError for failing scrapes, and the labels applied at scrape time.
- label_values on __name__ (optionally with matchers) discovers metric names from metadata; it does not prove current samples exist. Confirm sample presence with query or range_query at the relevant times.
- targets_metadata reports collected metric metadata for a target; compare it with scrape health and sample queries rather than treating it as a live read of the target endpoint.
- config shows the scrape configuration: job definitions, relabeling, and intervals.
- Prometheus's own scrape metrics explain silent drops: nonzero rates on the prometheus_target_scrapes_* rejection counters (sample_out_of_order, sample_out_of_bounds, duplicate_timestamp, exceeded_sample_limit) mean samples arrived but were rejected at ingestion.

## Topics worth exploring

Treat these as starting points and follow where the data stops:

- Is the target up and scraped? query up{job="<job>"} and check list_targets for down or missing targets and their scrape errors.
- Does the metric exist under a different shape? Search broadly, e.g. series with a matcher like {__name__=~".*<keyword>.*"} -- renames and label changes are common after upgrades.
- Did it exist before? range_query the series over a suitable window and resolution to bound when it disappeared, then correlate with deploys or config changes. Query step, lookback and stale markers affect the visible boundary; do not claim an exact disappearance time from a coarse graph.
- Is relabeling dropping it? Look through relabel_configs and metric_relabel_configs in config for rules that drop the target, metric, or label you expected.
- Is it the query, not the data? Instant selectors use a configurable lookback (5m by default), but stale markers can make a series disappear earlier. Check query lookback, exporter timestamps and track_timestamps_staleness, alongside narrow windows and strict matchers. A stale marker and an expired lookback are different causes of absent query results.

## Reporting findings

State where the data stops existing (target, scrape, relabeling, or query), since when, and what change would restore it.
