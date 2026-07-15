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

// Package mcptest provides test utilities for MCP server testing.
package mcptest

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestServer wraps an MCP server and client for testing purposes.
// It provides a simple interface to register tools and resources, then call them
// through the full MCP protocol stack using in-memory transports.
type TestServer struct {
	Server  *mcp.Server
	session *mcp.ClientSession
	t       *testing.T
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewTestServer creates a new test server with in-memory transports.
// The server and client are automatically connected and ready for use.
func NewTestServer(t *testing.T) *TestServer {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	// Create in-memory transports - these are connected to each other.
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	// Create the server.
	impl := &mcp.Implementation{Name: "test-server", Version: "test"}
	server := mcp.NewServer(impl, nil)

	// Connect server first (required before client connects).
	_, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("failed to connect server: %v", err)
	}

	// Create and connect the client.
	client := mcp.NewClient(impl, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("failed to connect client: %v", err)
	}

	ts := &TestServer{
		Server:  server,
		session: clientSession,
		t:       t,
		ctx:     ctx,
		cancel:  cancel,
	}

	// Register cleanup.
	t.Cleanup(func() {
		ts.Close()
	})

	return ts
}

// AddTool registers a tool with the test server.
// The handler should match the signature expected by mcp.AddTool.
func AddTool[T, O any](ts *TestServer, tool *mcp.Tool, handler func(context.Context, *mcp.CallToolRequest, T) (*mcp.CallToolResult, O, error)) {
	mcp.AddTool(ts.Server, tool, handler)
}

// CallTool invokes a tool by name with the given arguments.
// Returns the tool result or an error.
func (ts *TestServer) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	return ts.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
}

// AddResource registers a static resource with the test server.
func (ts *TestServer) AddResource(resource *mcp.Resource, handler func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error)) {
	ts.Server.AddResource(resource, handler)
}

// AddResourceTemplate registers a resource template with the test server.
func (ts *TestServer) AddResourceTemplate(template *mcp.ResourceTemplate, handler func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error)) {
	ts.Server.AddResourceTemplate(template, handler)
}

// ReadResource invokes a resource by URI through the MCP protocol.
// Returns the resource result or an error.
func (ts *TestServer) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	return ts.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
}

// ListTools returns the tools registered on the server as seen through the
// MCP protocol. The returned schemas include any auto-generated fields
// (e.g., InputSchema) that the SDK produces during registration.
func (ts *TestServer) ListTools(ctx context.Context) (*mcp.ListToolsResult, error) {
	return ts.session.ListTools(ctx, &mcp.ListToolsParams{})
}

// ListPrompts returns the prompts registered on the server as seen through
// the MCP protocol.
func (ts *TestServer) ListPrompts(ctx context.Context) (*mcp.ListPromptsResult, error) {
	return ts.session.ListPrompts(ctx, &mcp.ListPromptsParams{})
}

// GetPrompt fetches a prompt by name through the MCP protocol.
func (ts *TestServer) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	return ts.session.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      name,
		Arguments: args,
	})
}

// Close shuts down the test server and releases resources.
// This is automatically called via t.Cleanup(), but can be called
// manually if needed.
func (ts *TestServer) Close() {
	if ts.cancel != nil {
		ts.cancel()
	}
}

// Context returns the context associated with this test server.
func (ts *TestServer) Context() context.Context {
	return ts.ctx
}

// GetResultText extracts text content from a CallToolResult.
// It concatenates all TextContent and EmbeddedResource text items in the result.
func GetResultText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var sb strings.Builder
	for _, c := range result.Content {
		switch content := c.(type) {
		case *mcp.TextContent:
			sb.WriteString(content.Text)
		case *mcp.EmbeddedResource:
			// Extract text from embedded resources.
			if content.Resource != nil && content.Resource.Text != "" {
				sb.WriteString(content.Resource.Text)
			}
		}
	}
	return sb.String()
}

// GetResourceText extracts text content from a ReadResourceResult.
// It concatenates all text content from the result's Contents.
func GetResourceText(result *mcp.ReadResourceResult) string {
	if result == nil {
		return ""
	}
	var sb strings.Builder
	for _, c := range result.Contents {
		if c.Text != "" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String()
}
