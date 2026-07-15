---
name: investigate-error-rates
description: Investigate elevated error rates and failing requests. Use when errors or 5xx/gRPC failures spike, SLOs burn, or an error alert fires; quantifies error ratios with rate(), compares to baseline, and isolates affected jobs and instances.
license: Apache-2.0
compatibility: Requires the tools of a connected Prometheus MCP server
---

# Investigating High Error Response Rates

Figure out how bad the errors are, when they started, and where they are concentrated. Error metrics vary by system, so discover what actually exists before querying.

## Getting oriented

- Error signals are usually counters: HTTP status codes (http_requests_total{code=~"5.."}), gRPC codes (grpc_server_handled_total{grpc_code!="OK"}), or dedicated *_errors_total / *_failures_total metrics.
- Discover what this system exposes with series or label_values on __name__ using regex matchers, and confirm semantics with metric_metadata.
- list_alerts shows whether an error-related alert is already firing and carries useful labels to start from.
- The ALERTS{alertstate="firing"} series Prometheus generates is the queryable form of the same information: range_query it to see when alerts started firing and line their history up against the error timeline.

## Topics worth exploring

Treat these as starting points and follow what the data shows:

- Current error rate: counters need rate() before aggregating, e.g. with query:
  sum by (job) (rate(http_requests_total{code=~"5.."}[5m]))
- Error ratio vs raw count: a ratio against total traffic distinguishes "more errors" from "more traffic":
  sum(rate(http_requests_total{code=~"5.."}[5m])) / sum(rate(http_requests_total[5m]))
- When it started: range_query the same expression over hours or days and look for the inflection point; compare against a known-good baseline window.
- Where it is concentrated: aggregate by job, instance, handler, or method; topk(10, ...) keeps output manageable.
- What else changed at that time: deploys and restarts (process_start_time_seconds), saturation (CPU, memory, connection pools), and latency histograms for the same services.

## Reporting findings

Aim for a clear statement of impact (error ratio and affected traffic), onset time, and the narrowest scope (service, instance, or route) that explains the errors, backed by the queries you ran.
