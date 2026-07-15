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
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// emptyInputSchema is an explicit JSON Schema for tools that take no
// parameters. The MCP SDK's auto-generated schema for EmptyInput omits the
// "properties" key, which causes issues with OpenAI's stricter validation.
// Including an empty "properties" object is semantically identical.
//
// See: https://github.com/prometheus/prometheus-mcp/issues/119
var emptyInputSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)

// ptr returns a pointer to the given value.
//
// Needed mostly since `true` is a constant and we can't take the address of it
// directly for things that need a pointer to a bool.
func ptr[T any](v T) *T {
	return &v
}

// Tool definitions for the MCP server.
var (
	queryToolDef = &mcp.Tool{
		Name:        "query",
		Description: "Execute an instant query against the Prometheus datasource, returning one value per series at a single point in time",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Instant Query",
			ReadOnlyHint: true,
		},
	}

	rangeQueryToolDef = &mcp.Tool{
		Name:        "range_query",
		Description: "Execute a range query against the Prometheus datasource, returning values over a time window; use for graphing and trend analysis",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Range Query",
			ReadOnlyHint: true,
		},
	}

	exemplarQueryToolDef = &mcp.Tool{
		Name:        "exemplar_query",
		Description: "Execute an exemplar query against the Prometheus datasource to find trace exemplars associated with metric samples",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Exemplar Query",
			ReadOnlyHint: true,
		},
	}

	seriesToolDef = &mcp.Tool{
		Name:        "series",
		Description: "Finds series by label matches",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Find Series",
			ReadOnlyHint: true,
		},
	}

	labelNamesToolDef = &mcp.Tool{
		Name:        "label_names",
		Description: "Returns the unique label names present in the block in sorted order by given time range and matches",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Label Names",
			ReadOnlyHint: true,
		},
	}

	labelValuesToolDef = &mcp.Tool{
		Name:        "label_values",
		Description: "Performs a query for the values of the given label, time range and matches",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Label Values",
			ReadOnlyHint: true,
		},
	}

	metricMetadataToolDef = &mcp.Tool{
		Name:        "metric_metadata",
		Description: "Returns metadata about metrics currently scraped by the metric name.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Metric Metadata",
			ReadOnlyHint: true,
		},
	}

	targetsMetadataToolDef = &mcp.Tool{
		Name:        "targets_metadata",
		Description: "Returns metadata about metrics currently scraped by the target ",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Targets Metadata",
			ReadOnlyHint: true,
		},
	}

	alertmanagersToolDef = &mcp.Tool{
		Name:        "alertmanagers",
		Description: "Get overview of Prometheus Alertmanager discovery",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "Alertmanagers",
			ReadOnlyHint: true,
		},
	}

	flagsToolDef = &mcp.Tool{
		Name:        "flags",
		Description: "Get runtime flags",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "Runtime Flags",
			ReadOnlyHint: true,
		},
	}

	listAlertsToolDef = &mcp.Tool{
		Name:        "list_alerts",
		Description: "List all active alerts",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Alerts",
			ReadOnlyHint: true,
		},
	}

	tsdbStatsToolDef = &mcp.Tool{
		Name:        "tsdb_stats",
		Description: "Get usage and cardinality statistics from the TSDB",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "TSDB Stats",
			ReadOnlyHint: true,
		},
	}

	buildInfoToolDef = &mcp.Tool{
		Name:        "build_info",
		Description: "Get Prometheus build information",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "Build Info",
			ReadOnlyHint: true,
		},
	}

	configToolDef = &mcp.Tool{
		Name:        "config",
		Description: "Get Prometheus configuration",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "Configuration",
			ReadOnlyHint: true,
		},
	}

	runtimeInfoToolDef = &mcp.Tool{
		Name:        "runtime_info",
		Description: "Get Prometheus runtime information",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "Runtime Info",
			ReadOnlyHint: true,
		},
	}

	listRulesToolDef = &mcp.Tool{
		Name:        "list_rules",
		Description: "List all alerting and recording rules that are loaded",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Rules",
			ReadOnlyHint: true,
		},
	}

	listTargetsToolDef = &mcp.Tool{
		Name:        "list_targets",
		Description: "Get overview of Prometheus target discovery",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Targets",
			ReadOnlyHint: true,
		},
	}

	walReplayToolDef = &mcp.Tool{
		Name:        "wal_replay_status",
		Description: "Get current WAL replay status",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "WAL Replay Status",
			ReadOnlyHint: true,
		},
	}

	// TSDB Admin tools.
	cleanTombstonesToolDef = &mcp.Tool{
		Name:        "clean_tombstones",
		Description: "Removes the deleted data from disk and cleans up the existing tombstones. I/O- and CPU-intensive; prefer low-traffic periods and confirm with the user before calling.",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:           "Clean Tombstones",
			DestructiveHint: ptr(true),
		},
	}

	deleteSeriesToolDef = &mcp.Tool{
		Name:        "delete_series",
		Description: "Deletes data for a selection of series in a time range. Both start_time and end_time are required to prevent accidental deletion of all data. Deletion creates tombstones; run clean_tombstones or wait for compaction to reclaim disk space. Confirm the time range and matchers with the user before calling.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete Series",
			DestructiveHint: ptr(true),
		},
	}

	snapshotToolDef = &mcp.Tool{
		Name:        "snapshot",
		Description: "creates a snapshot of all current data into snapshots/<datetime>-<rand> under the TSDB's data directory and returns the directory as response. Safe, but confirm with the user before calling.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Create Snapshot",
			DestructiveHint: ptr(true),
		},
	}

	// Management API tools.
	healthyToolDef = &mcp.Tool{
		Name:        "healthy",
		Description: "Management API endpoint that can be used to check Prometheus health.",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "Health Check",
			ReadOnlyHint: true,
		},
	}

	readyToolDef = &mcp.Tool{
		Name:        "ready",
		Description: "Management API endpoint that can be used to check Prometheus is ready to serve traffic (i.e. respond to queries.)",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "Readiness Check",
			ReadOnlyHint: true,
		},
	}

	reloadToolDef = &mcp.Tool{
		Name:        "reload",
		Description: "Management API endpoint that can be used to trigger a reload of the Prometheus configuration and rule files. Safe; applies changes without downtime.",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:           "Reload Config",
			DestructiveHint: ptr(true),
		},
	}

	quitToolDef = &mcp.Tool{
		Name:        "quit",
		Description: "Management API endpoint that can be used to trigger a graceful shutdown of Prometheus. Only call this when the user explicitly requests a shutdown.",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:           "Shutdown",
			DestructiveHint: ptr(true),
		},
	}

	// Documentation tools.
	docsListToolDef = &mcp.Tool{
		Name:        "docs_list",
		Description: "List of Official Prometheus Documentation Files.",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Documentation",
			ReadOnlyHint: true,
		},
	}

	docsReadToolDef = &mcp.Tool{
		Name:        "docs_read",
		Description: "Read the named markdown file containing official Prometheus documentation from the prometheus/docs repo",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Read Documentation",
			ReadOnlyHint: true,
		},
	}

	docsSearchToolDef = &mcp.Tool{
		Name:        "docs_search",
		Description: "Search the markdown files containing official Prometheus documentation from the prometheus/docs repo",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Search Documentation",
			ReadOnlyHint: true,
		},
	}

	// Thanos-specific tools.
	thanosStoresToolDef = &mcp.Tool{
		Name:        "list_stores",
		Description: "List all store API servers",
		InputSchema: emptyInputSchema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Stores",
			ReadOnlyHint: true,
		},
	}
)
