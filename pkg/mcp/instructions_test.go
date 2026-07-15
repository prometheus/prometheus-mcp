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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderInstructions(t *testing.T) {
	fullToolset := getToolset(toolsetConfig{
		enabledTools: []string{"all"},
		logger:       slog.Default(),
	})
	thanosBackendToolset := getToolset(toolsetConfig{
		enabledTools:      []string{"all"},
		prometheusBackend: "thanos",
		logger:            slog.Default(),
	})

	t.Run("tsdb admin tools disabled by default", func(t *testing.T) {
		instrx, err := renderInstructions(ServerConfig{}, fullToolset)
		require.NoError(t, err)

		require.Contains(t, instrx, "are disabled on this server")
		require.NotContains(t, instrx, "--web.enable-admin-api")
	})

	t.Run("tsdb admin tools enabled", func(t *testing.T) {
		cfg := ServerConfig{TSDBAdminToolsEnabled: true}
		instrx, err := renderInstructions(cfg, fullToolset)
		require.NoError(t, err)

		require.Contains(t, instrx, "--web.enable-admin-api")
		require.NotContains(t, instrx, "are disabled on this server")
	})

	t.Run("thanos backend prunes unsupported tool guidance", func(t *testing.T) {
		cfg := ServerConfig{PrometheusBackend: "thanos"}
		instrx, err := renderInstructions(cfg, thanosBackendToolset)
		require.NoError(t, err)

		// Thanos toolset removes the TSDB admin tools, so none of their
		// guidance should render regardless of the enablement flag.
		require.NotContains(t, instrx, "delete_series")
		require.NotContains(t, instrx, "clean_tombstones")
		require.NotContains(t, instrx, "--dangerous.enable-tsdb-admin-tools")

		// A configured backend replaces the generic compatibility note.
		require.Contains(t, instrx, `configured for the "thanos" backend`)
		require.NotContains(t, instrx, "Any Prometheus-API-compatible backend")
	})

	t.Run("runbooks pointer follows toolset", func(t *testing.T) {
		instrx, err := renderInstructions(ServerConfig{}, fullToolset)
		require.NoError(t, err)
		require.Contains(t, instrx, "runbooks_list")

		instrx, err = renderInstructions(ServerConfig{}, map[string]toolRegistration{})
		require.NoError(t, err)
		require.NotContains(t, instrx, "runbooks_list")
	})

	t.Run("no backend renders generic compatibility note", func(t *testing.T) {
		instrx, err := renderInstructions(ServerConfig{}, fullToolset)
		require.NoError(t, err)

		require.Contains(t, instrx, "Any Prometheus-API-compatible backend")
		require.NotContains(t, instrx, "configured for the")
	})

	t.Run("backend name is normalized to lowercase", func(t *testing.T) {
		cfg := ServerConfig{PrometheusBackend: "THANOS"}
		instrx, err := renderInstructions(cfg, thanosBackendToolset)
		require.NoError(t, err)

		require.Contains(t, instrx, `configured for the "thanos" backend`)
	})

	t.Run("no unrendered template markers remain", func(t *testing.T) {
		for name, toolset := range map[string]map[string]toolRegistration{
			"full":   fullToolset,
			"thanos": thanosBackendToolset,
		} {
			instrx, err := renderInstructions(ServerConfig{}, toolset)
			require.NoError(t, err, "toolset %s", name)

			require.NotContains(t, instrx, "{{", "toolset %s", name)
			require.NotContains(t, instrx, "}}", "toolset %s", name)
			require.NotEmpty(t, instrx, "toolset %s", name)
		}
	})
}
