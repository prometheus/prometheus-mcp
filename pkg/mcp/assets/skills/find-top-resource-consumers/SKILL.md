---
name: find-top-resource-consumers
description: Find the top CPU, memory, or disk consumers. Use for capacity reviews, noisy-neighbor hunts, and top-N questions about which jobs, pods, or instances use the most resources or drive TSDB growth; builds topk() queries over discovered metrics.
license: Apache-2.0
compatibility: Requires the tools of a connected Prometheus MCP server
---

# Finding Top Resource Consumers

Identify which services, pods, or instances consume the most CPU, memory, or disk -- or drive the most TSDB growth. Resource metrics vary by environment (node_exporter, cadvisor, kubelet, ...), so discover what exists before querying.

## Getting oriented

- label_values on __name__ (or series with regex matchers) reveals which resource metrics exist: node_cpu_seconds_total, container_memory_working_set_bytes, process_resident_memory_bytes, and so on.
- metric_metadata confirms the type and meaning of whatever discovery turns up.
- tsdb_stats answers the "who produces the most metrics" version of the question via seriesCountByMetricName.
- Prometheus's own metrics exposes monitoring cost per target: query topk(10, scrape_samples_scraped) to find the heaviest targets, and a persistently high scrape_series_added means a target keeps minting new series.

## Topics worth exploring

Treat these as starting points, adapting metric names to what discovery found:

- Top CPU: CPU counters need rate() before ranking, e.g. with query:
  topk(10, sum by (pod) (rate(container_cpu_usage_seconds_total[5m])))
- Top memory: gauges rank directly:
  topk(10, sum by (instance) (container_memory_working_set_bytes))
- Top disk: node_filesystem_avail_bytes vs node_filesystem_size_bytes for fullness; rate(node_disk_written_bytes_total[5m]) for write churn.
- Trend vs snapshot: range_query the same expressions to see whether a top consumer is growing, cyclic, or stable; a stable hog and a leak need different responses.
- TSDB growth: the top metric and label names from tsdb_stats show what is inflating storage, often more actionable than any single consumer.

## Reporting findings

Rank the top consumers with numbers and units, note whether each is growing or stable, and flag anything disproportionate to its peers as a candidate for follow-up.
