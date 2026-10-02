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
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus-mcp/pkg/mcp/mcptest"
)

// getToolNames returns the names of all tools in a toolset map.
// The returned slice is sorted for consistent comparison in tests.
// This is a test helper function.
func getToolNames(toolset map[string]toolRegistration) []string {
	names := make([]string, 0, len(toolset))
	for name := range toolset {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func TestGetToolset(t *testing.T) {
	t.Run("all tools loads entire prometheus toolset", func(t *testing.T) {
		cfg := toolsetConfig{
			enabledTools: []string{"all"},
			logger:       slog.Default(),
		}

		toolset := getToolset(cfg)
		names := getToolNames(toolset)

		// Verify the returned toolset exactly matches the prometheus toolset.
		expectedNames := getToolNames(prometheusToolset)
		require.ElementsMatch(t, expectedNames, names, "toolset should contain exactly the prometheus toolset")
	})

	t.Run("core tools loads only core subset", func(t *testing.T) {
		cfg := toolsetConfig{
			enabledTools: []string{"core"},
			logger:       slog.Default(),
		}

		toolset := getToolset(cfg)
		names := getToolNames(toolset)

		// Should contain exactly the core tools.
		for _, coreTool := range CoreTools {
			require.Contains(t, names, coreTool, "expected core tool %q to be present", coreTool)
		}

		// Should NOT contain non-core tools.
		require.NotContains(t, names, "config")
		require.NotContains(t, names, "quit")

		require.Len(t, toolset, len(CoreTools))
	})

	t.Run("custom tool list includes core plus specified tools", func(t *testing.T) {
		cfg := toolsetConfig{
			enabledTools: []string{"alertmanagers", "config"},
			logger:       slog.Default(),
		}

		toolset := getToolset(cfg)
		names := getToolNames(toolset)

		// Should contain core tools.
		for _, coreTool := range CoreTools {
			require.Contains(t, names, coreTool, "expected core tool %q to be present", coreTool)
		}

		// Should contain the specified additional tools.
		require.Contains(t, names, "alertmanagers")
		require.Contains(t, names, "config")

		// Should NOT contain other non-core tools not in the list.
		require.NotContains(t, names, "reload")
		require.NotContains(t, names, "quit")
	})

	t.Run("invalid tool names are ignored with warning", func(t *testing.T) {
		cfg := toolsetConfig{
			enabledTools: []string{"nonexistent_tool", "alertmanagers"},
			logger:       slog.Default(),
		}

		toolset := getToolset(cfg)
		names := getToolNames(toolset)

		// Should still have core tools and valid specified tools.
		require.Contains(t, names, "alertmanagers")
		for _, coreTool := range CoreTools {
			require.Contains(t, names, coreTool)
		}

		// Invalid tool should not be present.
		require.NotContains(t, names, "nonexistent_tool")
	})

	t.Run("thanos backend overrides toolset with thanos-specific tools", func(t *testing.T) {
		cfg := toolsetConfig{
			enabledTools:      []string{"all"}, // This would normally load prometheus
			prometheusBackend: "thanos",
			logger:            slog.Default(),
		}

		toolset := getToolset(cfg)
		names := getToolNames(toolset)

		// Thanos-specific tool should be present.
		require.Contains(t, names, "list_stores")

		// Prometheus-only tools should NOT be present.
		require.NotContains(t, names, "alertmanagers")
		require.NotContains(t, names, "config")
		require.NotContains(t, names, "wal_replay_status")
		require.NotContains(t, names, "reload")
		require.NotContains(t, names, "quit")

		// Shared tools should still be present.
		require.Contains(t, names, "query")
		require.Contains(t, names, "range_query")
		require.Contains(t, names, "docs_search")

		// Should have exactly the thanos toolset size.
		require.Len(t, toolset, len(thanosToolset))
	})

	t.Run("prometheus backend explicitly loads full prometheus toolset", func(t *testing.T) {
		cfg := toolsetConfig{
			enabledTools:      []string{"core"}, // Would normally just load core
			prometheusBackend: "prometheus",
			logger:            slog.Default(),
		}

		toolset := getToolset(cfg)
		names := getToolNames(toolset)

		// Backend override should give us all prometheus tools.
		require.Contains(t, names, "alertmanagers")
		require.Contains(t, names, "config")
		require.Contains(t, names, "reload")
		require.Len(t, toolset, len(prometheusToolset))
	})

	t.Run("unknown backend keeps existing toolset with warning", func(t *testing.T) {
		cfg := toolsetConfig{
			enabledTools:      []string{"alertmanagers"},
			prometheusBackend: "super-awesome-tsdb", // Unknown backend
			logger:            slog.Default(),
		}

		toolset := getToolset(cfg)
		names := getToolNames(toolset)

		// Should keep the custom toolset (core + alertmanagers).
		require.Contains(t, names, "alertmanagers")
		for _, coreTool := range CoreTools {
			require.Contains(t, names, coreTool)
		}

		// Should NOT have thanos-specific tools.
		require.NotContains(t, names, "list_stores")
	})

	t.Run("empty enabled tools loads only core", func(t *testing.T) {
		cfg := toolsetConfig{
			enabledTools: []string{},
			logger:       slog.Default(),
		}

		toolset := getToolset(cfg)
		names := getToolNames(toolset)

		// Should contain exactly core tools.
		for _, coreTool := range CoreTools {
			require.Contains(t, names, coreTool)
		}
		require.Len(t, toolset, len(CoreTools))
	})

	t.Run("duplicate tools in enabled list are deduplicated", func(t *testing.T) {
		cfg := toolsetConfig{
			enabledTools: []string{"query", "query", "alertmanagers", "alertmanagers"},
			logger:       slog.Default(),
		}

		toolset := getToolset(cfg)
		names := getToolNames(toolset)

		// Count occurrences of each tool.
		counts := make(map[string]int)
		for _, name := range names {
			counts[name]++
		}

		// No duplicates should exist.
		for name, count := range counts {
			require.Equal(t, 1, count, "tool %s should appear exactly once", name)
		}
	})

	t.Run("case insensitive backend matching", func(t *testing.T) {
		cfg := toolsetConfig{
			enabledTools:      []string{"core"},
			prometheusBackend: "THANOS",
			logger:            slog.Default(),
		}

		toolset := getToolset(cfg)
		names := getToolNames(toolset)

		require.Contains(t, names, "list_stores")
		require.NotContains(t, names, "alertmanagers")
	})

	t.Run("nil logger uses default", func(t *testing.T) {
		cfg := toolsetConfig{
			enabledTools: []string{"core"},
			logger:       nil,
		}

		require.NotPanics(t, func() {
			toolset := getToolset(cfg)
			require.NotNil(t, toolset)
			require.Len(t, toolset, len(CoreTools))
		})
	})

	t.Run("mixed case backend prometheus", func(t *testing.T) {
		cfg := toolsetConfig{
			enabledTools:      []string{"core"},
			prometheusBackend: "Prometheus", // Mixed case
			logger:            slog.Default(),
		}

		toolset := getToolset(cfg)

		require.Len(t, toolset, len(prometheusToolset))
	})
}

// TestToolsetContents verifies the actual contents of the toolsets.
func TestToolsetContents(t *testing.T) {
	t.Run("thanosToolset excludes prometheus-only tools", func(t *testing.T) {
		prometheusOnly := []string{
			"alertmanagers",
			"config",
			"wal_replay_status",
			"reload",
			"quit",
		}

		for _, tool := range prometheusOnly {
			_, exists := thanosToolset[tool]
			require.False(t, exists, "thanos toolset should not contain %s", tool)
		}
	})

	t.Run("thanosToolset includes thanos-specific tools", func(t *testing.T) {
		_, exists := thanosToolset["list_stores"]
		require.True(t, exists, "thanos toolset should contain list_stores")
	})

	t.Run("all core tools exist in prometheusToolset", func(t *testing.T) {
		for _, coreTool := range CoreTools {
			_, exists := prometheusToolset[coreTool]
			require.True(t, exists, "core tool %s should exist in prometheusToolset", coreTool)
		}
	})

	t.Run("all TSDB admin tools exist in prometheusToolset", func(t *testing.T) {
		for _, adminTool := range PrometheusTsdbAdminTools {
			_, exists := prometheusToolset[adminTool]
			require.True(t, exists, "TSDB admin tool %s should exist in prometheusToolset", adminTool)
		}
	})
}

// TestToolInputSchemaProperties verifies that every tool's InputSchema
// includes a "properties" key after full MCP registration. This is a
// regression test for OpenAI API compatibility -- the OpenAI-compatible
// endpoint requires all tool schemas to have "properties".
//
// The test registers all tools on a real in-memory MCP server and then
// calls ListTools through the protocol, so it validates the *actual*
// schemas that clients receive -- including any auto-generated fields
// the SDK produces for tools that accept parameters, and any explicit
// schemas set for parameter-less (EmptyInput) tools.
//
// It also verifies that no schema node declares "type" as an array, since
// single-type dialects such as Gemini's reject them.
//
// See: https://github.com/prometheus/prometheus-mcp/issues/119
func TestToolInputSchemaProperties(t *testing.T) {
	container := newTestContainer(nil)

	toolsets := map[string]map[string]toolRegistration{
		"prometheus": prometheusToolset,
		"thanos":     thanosToolset,
	}

	// Tools whose "matches" input is a []string.
	matchesTools := []string{"series", "label_names", "label_values", "delete_series"}

	for tsName, toolset := range toolsets {
		t.Run(tsName, func(t *testing.T) {
			ts := mcptest.NewTestServer(t)

			for _, reg := range toolset {
				reg.register(ts.Server, container)
			}

			result, err := ts.ListTools(ts.Context())
			require.NoError(t, err, "ListTools failed")
			require.Len(t, result.Tools, len(toolset), "registered tool count mismatch")

			for _, tool := range result.Tools {
				data, err := json.Marshal(tool.InputSchema)
				require.NoErrorf(t, err, "%s/%s: failed to marshal InputSchema", tsName, tool.Name)

				var schema map[string]any
				err = json.Unmarshal(data, &schema)
				require.NoErrorf(t, err, "%s/%s: failed to unmarshal InputSchema", tsName, tool.Name)

				_, hasProperties := schema["properties"]
				require.Truef(t, hasProperties, "%s/%s: InputSchema missing 'properties' key (required for OpenAI API compatibility)", tsName, tool.Name)

				requireSingleTypeSchema(t, tsName+"/"+tool.Name, "(root)", schema)

				if slices.Contains(matchesTools, tool.Name) {
					props, _ := schema["properties"].(map[string]any)
					matches, ok := props["matches"].(map[string]any)
					require.Truef(t, ok, "%s/%s: InputSchema missing 'matches' property", tsName, tool.Name)
					matches = maps.Clone(matches)
					delete(matches, "description")
					require.Equalf(t, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, matches,
						"%s/%s: unexpected 'matches' schema", tsName, tool.Name)
				}
			}
		})
	}
}

// requireSingleTypeSchema fails if node or any schema nested under it declares
// "type" as an array.
func requireSingleTypeSchema(t *testing.T, tool, path string, node map[string]any) {
	t.Helper()

	_, isArray := node["type"].([]any)
	require.Falsef(t, isArray, "%s: %s has type %v; single-type schema dialects such as Gemini's reject type arrays, set the tool's InputSchema with inputSchemaFor", tool, path, node["type"])

	if props, ok := node["properties"].(map[string]any); ok {
		for name, prop := range props {
			if sub, ok := prop.(map[string]any); ok {
				requireSingleTypeSchema(t, tool, path+".properties."+name, sub)
			}
		}
	}
	for _, key := range []string{"items", "additionalProperties"} {
		if sub, ok := node[key].(map[string]any); ok {
			requireSingleTypeSchema(t, tool, path+"."+key, sub)
		}
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		subs, _ := node[key].([]any)
		for i, s := range subs {
			if sub, ok := s.(map[string]any); ok {
				requireSingleTypeSchema(t, tool, fmt.Sprintf("%s.%s[%d]", path, key, i), sub)
			}
		}
	}
}
