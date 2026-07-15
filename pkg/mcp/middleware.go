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
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
)

// MCP method names. Needed because the go-sdk does not export them.
// https://github.com/modelcontextprotocol/go-sdk/blob/13488f7da1ed8eda47413df3420ab64026696798/mcp/protocol.go#L1330-L1357
const (
	methodInitialize    = "initialize"
	methodToolsCall     = "tools/call"
	methodResourcesRead = "resources/read"
	methodPromptsGet    = "prompts/get"
	methodPromptsList   = "prompts/list"
)

// telemetryMiddleware creates an MCP middleware that instruments MCP method
// calls with metrics and logging.
func telemetryMiddleware(logger *slog.Logger) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case methodInitialize:
				return telemetryHandleInitialize(ctx, method, req, next, logger)
			case methodToolsCall:
				return telemetryHandleToolCall(ctx, method, req, next, logger)
			case methodResourcesRead:
				return telemetryHandleResourceRead(ctx, method, req, next, logger)
			case methodPromptsGet:
				return telemetryHandlePromptGet(ctx, method, req, next, logger)
			case methodPromptsList:
				return telemetryHandlePromptList(ctx, method, req, next, logger)
			}

			// If no supported method matches, run handler directly
			// without additional instrumentation.
			return next(ctx, method, req)
		}
	}
}

// telemetryHandleInitialize handles the MCP initialization, logging client
// info and setting the server ready metric after successful initialization.
func telemetryHandleInitialize(ctx context.Context, method string, req mcp.Request, next mcp.MethodHandler, logger *slog.Logger) (mcp.Result, error) {
	params, ok := req.GetParams().(*mcp.InitializeParams)
	if !ok {
		// Can't extract init params, pass through without instrumentation.
		logger.Warn("Failed to extract initialize params for telemetry", "method", method)
		return next(ctx, method, req)
	}

	result, err := next(ctx, method, req)
	if err != nil {
		logger.Error("MCP initialization failed", "error", err)
		return result, err
	}

	// Extract client info for logging.
	clientName := ""
	clientVersion := ""
	if params.ClientInfo != nil {
		clientName = params.ClientInfo.Name
		clientVersion = params.ClientInfo.Version
	}

	// Extract server info from result for logging.
	serverName := ""
	serverVersion := ""
	if initResult, ok := result.(*mcp.InitializeResult); ok && initResult.ServerInfo != nil {
		serverName = initResult.ServerInfo.Name
		serverVersion = initResult.ServerInfo.Version
	}

	logger.Debug("MCP server initialized",
		"client_name", clientName,
		"client_version", clientVersion,
		"protocol_version", params.ProtocolVersion,
		"server_name", serverName,
		"server_version", serverVersion,
	)

	metricServerReady.Set(1)

	return result, nil
}

// telemetryHandleToolCall instruments a tools/call request with metrics and logging.
func telemetryHandleToolCall(ctx context.Context, method string, req mcp.Request, next mcp.MethodHandler, logger *slog.Logger) (mcp.Result, error) {
	params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok {
		// Can't extract tool info, pass through without instrumentation.
		logger.Warn("Failed to extract tool params for telemetry", "method", method)
		return next(ctx, method, req)
	}

	toolName := params.Name
	args := params.Arguments
	logger = logger.With("tool_name", toolName, "request_arguments", args)

	logger.Debug("Calling tool")
	startTime := time.Now()
	result, err := next(ctx, method, req)
	duration := time.Since(startTime)

	metricToolCallDuration.With(prometheus.Labels{"tool_name": toolName}).Observe(duration.Seconds())
	logger.Debug("Finished calling tool", "duration", duration)
	toolResult, ok := result.(*mcp.CallToolResult)
	if !ok {
		metricToolCallsFailed.With(prometheus.Labels{"tool_name": toolName}).Inc()
		logger.Error("Failed to convert result to call tool result")
		return result, err
	}
	if err != nil || toolResult.IsError {
		metricToolCallsFailed.With(prometheus.Labels{"tool_name": toolName}).Inc()
		logger.Error("Failed calling tool", "result", result, "error", err)
	}

	return result, err
}

// telemetryHandlePromptGet instruments a prompts/get request with metrics and logging.
func telemetryHandlePromptGet(ctx context.Context, method string, req mcp.Request, next mcp.MethodHandler, logger *slog.Logger) (mcp.Result, error) {
	params, ok := req.GetParams().(*mcp.GetPromptParams)
	if !ok {
		// Can't extract prompt info, pass through without instrumentation.
		logger.Warn("Failed to extract prompt params for telemetry", "method", method)
		return next(ctx, method, req)
	}

	promptName := params.Name
	logger = logger.With("prompt_name", promptName)

	logger.Debug("Calling prompt")

	startTime := time.Now()
	result, err := next(ctx, method, req)
	duration := time.Since(startTime)

	metricPromptGetDuration.With(prometheus.Labels{"prompt_name": promptName}).Observe(duration.Seconds())
	logger.Debug("Finished calling prompt", "duration", duration)

	if err != nil {
		metricPromptGetsFailed.With(prometheus.Labels{"prompt_name": promptName}).Inc()
		logger.Error("Failed calling prompt", "result", result, "error", err)
	}

	return result, err
}

// telemetryHandlePromptList instruments a prompts/list request with metrics
// and logging. Listings carry no parameters, so the metrics are unlabelled.
func telemetryHandlePromptList(ctx context.Context, method string, req mcp.Request, next mcp.MethodHandler, logger *slog.Logger) (mcp.Result, error) {
	logger.Debug("Listing prompts")

	startTime := time.Now()
	result, err := next(ctx, method, req)
	duration := time.Since(startTime)

	metricPromptListDuration.Observe(duration.Seconds())
	if listResult, ok := result.(*mcp.ListPromptsResult); ok {
		logger = logger.With("prompt_count", len(listResult.Prompts))
	}
	logger.Debug("Finished listing prompts", "duration", duration)

	if err != nil {
		metricPromptListsFailed.Inc()
		logger.Error("Failed listing prompts", "result", result, "error", err)
	}

	return result, err
}

// telemetryHandleResourceRead instruments a resources/read request with metrics and logging.
func telemetryHandleResourceRead(ctx context.Context, method string, req mcp.Request, next mcp.MethodHandler, logger *slog.Logger) (mcp.Result, error) {
	params, ok := req.GetParams().(*mcp.ReadResourceParams)
	if !ok {
		// Can't extract resource info, pass through without instrumentation.
		logger.Warn("Failed to extract resource params for telemetry", "method", method)
		return next(ctx, method, req)
	}

	uri := params.URI
	logger = logger.With("resource_uri", uri)

	logger.Debug("Calling resource")

	startTime := time.Now()
	result, err := next(ctx, method, req)
	duration := time.Since(startTime)

	metricResourceCallDuration.With(prometheus.Labels{"resource_uri": uri}).Observe(duration.Seconds())
	logger.Debug("Finished calling resource", "duration", duration)

	if err != nil {
		metricResourceCallsFailed.With(prometheus.Labels{"resource_uri": uri}).Inc()
		logger.Error("Failed calling resource", "result", result, "error", err)
	}

	return result, err
}
