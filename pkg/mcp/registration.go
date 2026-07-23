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
	"log/slog"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/common/promslog"
)

// toolRegistration holds a tool definition and its registration function.
// The registration function is used because handlers have different generic types.
type toolRegistration struct {
	tool     *mcp.Tool
	register func(server *mcp.Server, container *ServerContainer)
}

// Tool Groupings.
var (
	// CoreTools is the list of tools that are always loaded.
	CoreTools = []string{
		"docs_list",
		"docs_read",
		"docs_search",
		"runbooks_list",
		"runbooks_read",
		"query",
		"range_query",
		"metric_metadata",
		"label_names",
		"label_values",
		"series",
	}

	// PrometheusTsdbAdminTools are dangerous administrative tools that require explicit enablement.
	PrometheusTsdbAdminTools = []string{
		"clean_tombstones",
		"delete_series",
		"snapshot",
	}

	// PrometheusBackends is a list of directly supported Prometheus API
	// compatible backends. Backends other than prometheus itself may
	// expose a different set of tools more tailored to the backend and/or
	// change functionality of existing tools.
	PrometheusBackends = []string{
		"prometheus",
		"thanos",
	}

	// prometheusToolset contains all the tools to interact with standard
	// prometheus through the HTTP API.
	prometheusToolset map[string]toolRegistration

	// thanosToolset contains all the tools to interact with thanos as a
	// prometheus HTTP API compatible backend.
	thanosToolset map[string]toolRegistration
)

// initPrometheusToolset initializes the prometheus toolset map. Called during
// init to avoid initialization cycles and control initialization order.
func initPrometheusToolset() {
	prometheusToolset = map[string]toolRegistration{
		"query": {
			tool: queryToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, queryToolDef, c.QueryHandler)
			},
		},
		"range_query": {
			tool: rangeQueryToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, rangeQueryToolDef, c.RangeQueryHandler)
			},
		},
		"exemplar_query": {
			tool: exemplarQueryToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, exemplarQueryToolDef, c.ExemplarQueryHandler)
			},
		},
		"series": {
			tool: seriesToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, seriesToolDef, c.SeriesHandler)
			},
		},
		"label_names": {
			tool: labelNamesToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, labelNamesToolDef, c.LabelNamesHandler)
			},
		},
		"label_values": {
			tool: labelValuesToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, labelValuesToolDef, c.LabelValuesHandler)
			},
		},
		"metric_metadata": {
			tool: metricMetadataToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, metricMetadataToolDef, c.MetricMetadataHandler)
			},
		},
		"targets_metadata": {
			tool: targetsMetadataToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, targetsMetadataToolDef, c.TargetsMetadataHandler)
			},
		},
		"alertmanagers": {
			tool: alertmanagersToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, alertmanagersToolDef, c.AlertmanagersHandler)
			},
		},
		"flags": {
			tool: flagsToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, flagsToolDef, c.FlagsHandler)
			},
		},
		"list_alerts": {
			tool: listAlertsToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, listAlertsToolDef, c.ListAlertsHandler)
			},
		},
		"tsdb_stats": {
			tool: tsdbStatsToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, tsdbStatsToolDef, c.TsdbStatsHandler)
			},
		},
		"build_info": {
			tool: buildInfoToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, buildInfoToolDef, c.BuildInfoHandler)
			},
		},
		"config": {
			tool: configToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, configToolDef, c.ConfigHandler)
			},
		},
		"runtime_info": {
			tool: runtimeInfoToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, runtimeInfoToolDef, c.RuntimeInfoHandler)
			},
		},
		"list_rules": {
			tool: listRulesToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, listRulesToolDef, c.ListRulesHandler)
			},
		},
		"list_targets": {
			tool: listTargetsToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, listTargetsToolDef, c.ListTargetsHandler)
			},
		},
		"wal_replay_status": {
			tool: walReplayToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, walReplayToolDef, c.WALReplayHandler)
			},
		},
		// TSDB Admin tools
		"clean_tombstones": {
			tool: cleanTombstonesToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, cleanTombstonesToolDef, c.CleanTombstonesHandler)
			},
		},
		"delete_series": {
			tool: deleteSeriesToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, deleteSeriesToolDef, c.DeleteSeriesHandler)
			},
		},
		"snapshot": {
			tool: snapshotToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, snapshotToolDef, c.SnapshotHandler)
			},
		},
		// Management API tools
		"healthy": {
			tool: healthyToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, healthyToolDef, c.HealthyHandler)
			},
		},
		"ready": {
			tool: readyToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, readyToolDef, c.ReadyHandler)
			},
		},
		"reload": {
			tool: reloadToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, reloadToolDef, c.ReloadHandler)
			},
		},
		"quit": {
			tool: quitToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, quitToolDef, c.QuitHandler)
			},
		},
		// Documentation tools
		"docs_list": {
			tool: docsListToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, docsListToolDef, c.DocsListHandler)
			},
		},
		"docs_read": {
			tool: docsReadToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, docsReadToolDef, c.DocsReadHandler)
			},
		},
		"docs_search": {
			tool: docsSearchToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, docsSearchToolDef, c.DocsSearchHandler)
			},
		},
		// Runbook tools
		"runbooks_list": {
			tool: runbooksListToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, runbooksListToolDef, c.RunbooksListHandler)
			},
		},
		"runbooks_read": {
			tool: runbooksReadToolDef,
			register: func(s *mcp.Server, c *ServerContainer) {
				mcp.AddTool(s, runbooksReadToolDef, c.RunbooksReadHandler)
			},
		},
	}
}

// thanosRemovedTools lists tools from prometheusToolset that Thanos does not support.
var thanosRemovedTools = append(
	PrometheusTsdbAdminTools,
	[]string{
		"alertmanagers",
		"config",
		"wal_replay_status",
		"reload",
		"quit",
	}...,
)

// initThanosToolset initializes the thanos toolset map. Called during
// init to avoid initialization cycles and control initialization order.
//
// It starts from prometheusToolset, removes unsupported tools, and adds
// Thanos-specific tools (list_stores).
func initThanosToolset() {
	thanosToolset = make(map[string]toolRegistration)
	for name, tool := range prometheusToolset {
		if !slices.Contains(thanosRemovedTools, name) {
			thanosToolset[name] = tool
		}
	}

	// Add Thanos-specific tools.
	thanosToolset["list_stores"] = toolRegistration{
		tool: thanosStoresToolDef,
		register: func(s *mcp.Server, c *ServerContainer) {
			mcp.AddTool(s, thanosStoresToolDef, c.ThanosStoresHandler)
		},
	}
}

func init() {
	initPrometheusToolset()
	initThanosToolset()
}

// registerTools registers the given toolset with the MCP server.
func registerTools(server *mcp.Server, container *ServerContainer, toolset []toolRegistration) {
	for _, tool := range toolset {
		tool.register(server, container)
	}
}

type toolsetConfig struct {
	enabledTools      []string
	prometheusBackend string
	logger            *slog.Logger
}

func getToolset(cfg toolsetConfig) map[string]toolRegistration {
	logger := cfg.logger
	if logger == nil {
		logger = promslog.NewNopLogger()
	}

	toolset := make(map[string]toolRegistration)

	switch {
	case len(cfg.enabledTools) == 1 && cfg.enabledTools[0] == "all":
		logger.Info("Setting tools based on provided toolset", "toolset", "all")
		for name, tool := range prometheusToolset {
			toolset[name] = tool
		}
	case len(cfg.enabledTools) == 1 && cfg.enabledTools[0] == "core":
		logger.Info("Setting tools based on provided toolset", "toolset", "core")
		for _, toolName := range CoreTools {
			toolset[toolName] = prometheusToolset[toolName]
		}
	default:
		// Always include core tools, plus any additional specified tools.
		enabledTools := slices.Concat(cfg.enabledTools, CoreTools)
		slices.Sort(enabledTools)
		enabledTools = slices.Compact(enabledTools)
		logger.Info("Setting tools based on provided toolset", "toolset", enabledTools)

		for _, toolName := range enabledTools {
			val, ok := prometheusToolset[toolName]
			if !ok {
				logger.Warn("Failed to find tool to register", "tool_name", toolName)
				continue
			}

			logger.Debug("Adding tool to toolset for registration", "tool_name", toolName)
			toolset[toolName] = val
		}
	}

	backend := strings.ToLower(cfg.prometheusBackend)
	switch backend {
	case "": // Keep loaded toolset
	case "prometheus":
		logger.Info("Setting tools based on provided prometheus backend", "backend", backend)
		backendToolset := make(map[string]toolRegistration)
		for name, tool := range prometheusToolset {
			backendToolset[name] = tool
		}
		toolset = backendToolset
	case "thanos":
		logger.Info("Setting tools based on provided prometheus backend", "backend", backend)
		backendToolset := make(map[string]toolRegistration)
		for name, tool := range thanosToolset {
			backendToolset[name] = tool
		}
		toolset = backendToolset
	default:
		logger.Warn("Prometheus backend does not have custom tool support, keeping existing toolset",
			"backend", backend, "toolset", cfg.enabledTools)
	}

	return toolset
}

// toolsetToToolRegistrationSlice converts a toolset map to a slice of toolRegistrations.
// This is useful for passing to RegisterTools which expects a slice.
func toolsetToToolRegistrationSlice(toolset map[string]toolRegistration) []toolRegistration {
	result := make([]toolRegistration, 0, len(toolset))
	for _, tool := range toolset {
		result = append(result, tool)
	}
	return result
}
