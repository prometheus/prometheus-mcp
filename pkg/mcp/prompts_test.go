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
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus-mcp/pkg/mcp/mcptest"
)

func TestListPrompts(t *testing.T) {
	t.Parallel()

	ts := mcptest.NewTestServer(t)
	registerPrompts(ts.Server)

	result, err := ts.ListPrompts(ts.Context())
	require.NoError(t, err)
	require.Len(t, result.Prompts, len(runbookDefs))

	promptsByName := make(map[string]*mcpsdk.Prompt)
	for _, p := range result.Prompts {
		promptsByName[p.Name] = p
	}

	for _, def := range runbookDefs {
		p, ok := promptsByName[def.Name]
		require.True(t, ok, "runbook %q not registered as a prompt", def.Name)
		require.Equal(t, def.Title, p.Title)
		require.Equal(t, def.Description, p.Description)
	}
}

func TestGetPrompt(t *testing.T) {
	t.Parallel()

	ts := mcptest.NewTestServer(t)
	registerPrompts(ts.Server)

	t.Run("each prompt returns its runbook content", func(t *testing.T) {
		for _, def := range runbookDefs {
			result, err := ts.GetPrompt(ts.Context(), def.Name, nil)
			require.NoError(t, err, "prompt %q", def.Name)
			require.Equal(t, def.Description, result.Description)
			require.Len(t, result.Messages, 1)

			msg := result.Messages[0]
			require.Equal(t, mcpsdk.Role("user"), msg.Role)

			text, ok := msg.Content.(*mcpsdk.TextContent)
			require.True(t, ok, "prompt %q content should be text", def.Name)
			require.Contains(t, text.Text, runbookPromptPreamble)
			require.Contains(t, text.Text, "# "+def.Title)
			require.Contains(t, text.Text, "## Topics worth exploring")
		}
	})

	t.Run("unknown prompt returns error", func(t *testing.T) {
		_, err := ts.GetPrompt(ts.Context(), "nonexistent_prompt", nil)
		require.Error(t, err)
	})
}
