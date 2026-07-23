---
name: troubleshoot-missing-data
description: Diagnose missing metrics, absent series, or scrape gaps. Use when queries unexpectedly return empty, a metric stopped reporting, a target is down or unscraped, or dashboards show no data; walks target health, metadata, and config checks.
license: Apache-2.0
compatibility: Requires the tools of a connected Prometheus MCP server
---

# Troubleshooting Missing Data

Work out why an expected metric or series is absent: the target is not being scraped, the target no longer exposes it, the labels changed, or the query itself misses data that exists. Narrow down where the pipeline breaks.

## Getting oriented

- list_targets shows scrape target health, the lastError for failing scrapes, and the labels applied at scrape time.
- label_values on __name__ (optionally with matchers) confirms whether a metric name exists at all right now.
- targets_metadata lists which metrics a given target actually exposes.
- config shows the scrape configuration: job definitions, relabeling, and intervals.
- Prometheus's own scrape metrics explain silent drops: nonzero rates on the prometheus_target_scrapes_* rejection counters (sample_out_of_order, sample_out_of_bounds, duplicate_timestamp, exceeded_sample_limit) mean samples arrived but were rejected at ingestion.

## Topics worth exploring

Treat these as starting points and follow where the data stops:

- Is the target up and scraped? query up{job="<job>"} and check list_targets for down or missing targets and their scrape errors.
- Does the metric exist under a different shape? Search broadly, e.g. series with a matcher like {__name__=~".*<keyword>.*"} -- renames and label changes are common after upgrades.
- Did it exist before? range_query the series over a wide window to find exactly when it disappeared, then correlate with deploys or config changes.
- Is relabeling dropping it? Look through relabel_configs and metric_relabel_configs in config for rules that drop the target, metric, or label you expected.
- Is it the query, not the data? Staleness handling (series go stale after 5m without samples), a too-narrow time window, or an over-strict matcher can make live data look absent.

## Reporting findings

State where the data stops existing (target, scrape, relabeling, or query), since when, and what change would restore it.
