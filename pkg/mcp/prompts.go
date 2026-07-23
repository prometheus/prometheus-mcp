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
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// runbookPromptPreamble prefixes runbook content when served as a prompt, so
// the model treats the runbook as a workflow to execute rather than a
// document to summarize.
const runbookPromptPreamble = "Follow this Prometheus runbook using the tools available from this MCP server. Adapt its guidance to the user's specific context, skip anything that relies on tools this session doesn't have, and report findings as you go."

func registerPrompts(server *mcp.Server) {
	for _, def := range runbookDefs {
		server.AddPrompt(
			&mcp.Prompt{
				Name:        def.Name,
				Title:       def.Title,
				Description: def.Description,
			},
			runbookPromptHandler(def),
		)
	}
}

// runbookPromptHandler returns a PromptHandler serving the given runbook's
// content as a single user message. The SKILL.md body is served without its
// frontmatter, which is metadata not needed by models.
func runbookPromptHandler(def runbookDef) mcp.PromptHandler {
	return func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Description: def.Description,
			Messages: []*mcp.PromptMessage{
				{
					Role:    "user",
					Content: &mcp.TextContent{Text: runbookPromptPreamble + "\n\n" + def.Body},
				},
			},
		}, nil
	}
}
