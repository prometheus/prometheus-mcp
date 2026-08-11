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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestLogger creates a slog.Logger backed by a bytes.Buffer for log
// assertion. The logger writes JSON-formatted entries at LevelDebug so all
// log levels are captured.
func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger, &buf
}

// mockRequest constructs a ServerRequest suitable for middleware tests. The
// Session field is nil because the telemetry middleware never accesses it.
func mockRequest[P mcp.Params](params P) mcp.Request {
	return &mcp.ServerRequest[P]{Params: params}
}

func TestTelemetryMiddleware_Routing(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		method         string
		req            mcp.Request
		nextResult     mcp.Result
		nextErr        error
		wantLogged     string // substring expected in log output; empty means no specific check
		wantNotLogged  string // substring that must NOT appear; empty means no check
		expectNextCall bool
	}{
		{
			name:   "unknown method passes through without instrumentation",
			method: "some/unknown",
			req:    mockRequest(&mcp.PingParams{}),
			nextResult: &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "passthrough"}},
			},
			nextErr:        nil,
			wantNotLogged:  "Calling tool",
			expectNextCall: true,
		},
		{
			name:   "initialize method dispatches to initialize handler",
			method: methodInitialize,
			req: mockRequest(&mcp.InitializeParams{
				ProtocolVersion: "2025-03-26",
				ClientInfo:      &mcp.Implementation{Name: "test-client", Version: "0.1"},
				Capabilities:    &mcp.ClientCapabilities{},
			}),
			nextResult: &mcp.InitializeResult{
				ProtocolVersion: "2025-03-26",
				ServerInfo:      &mcp.Implementation{Name: "test-server", Version: "0.2"},
			},
			nextErr:        nil,
			wantLogged:     "MCP server initialized",
			expectNextCall: true,
		},
		{
			name:   "tools/call method dispatches to tool call handler",
			method: methodToolsCall,
			req: mockRequest(&mcp.CallToolParamsRaw{
				Name:      "query",
				Arguments: json.RawMessage(`{"query":"up"}`),
			}),
			nextResult: &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
			},
			nextErr:        nil,
			wantLogged:     "Calling tool",
			expectNextCall: true,
		},
		{
			name:   "resources/read method dispatches to resource read handler",
			method: methodResourcesRead,
			req: mockRequest(&mcp.ReadResourceParams{
				URI: "prometheus://metrics",
			}),
			nextResult: &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{URI: "prometheus://metrics", Text: "data"}},
			},
			nextErr:        nil,
			wantLogged:     "Calling resource",
			expectNextCall: true,
		},
		{
			name:   "prompts/get method dispatches to prompt get handler",
			method: methodPromptsGet,
			req: mockRequest(&mcp.GetPromptParams{
				Name: "check-system-health",
			}),
			nextResult: &mcp.GetPromptResult{
				Messages: []*mcp.PromptMessage{
					{Role: "user", Content: &mcp.TextContent{Text: "runbook"}},
				},
			},
			nextErr:        nil,
			wantLogged:     "Calling prompt",
			expectNextCall: true,
		},
		{
			name:   "prompts/list method dispatches to prompt list handler",
			method: methodPromptsList,
			req:    mockRequest(&mcp.ListPromptsParams{}),
			nextResult: &mcp.ListPromptsResult{
				Prompts: []*mcp.Prompt{{Name: "check-system-health"}},
			},
			nextErr:        nil,
			wantLogged:     "Listing prompts",
			expectNextCall: true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger, buf := newTestLogger()
			middleware := telemetryMiddleware(logger)

			nextCalled := false
			next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				nextCalled = true
				return tc.nextResult, tc.nextErr
			}

			handler := middleware(next)
			result, err := handler(context.Background(), tc.method, tc.req)

			require.Equal(t, tc.expectNextCall, nextCalled, "next handler call expectation mismatch")

			if tc.nextErr != nil {
				require.ErrorIs(t, err, tc.nextErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.nextResult, result)

			output := buf.String()
			if tc.wantLogged != "" {
				require.Contains(t, output, tc.wantLogged)
			}
			if tc.wantNotLogged != "" {
				require.NotContains(t, output, tc.wantNotLogged)
			}
		})
	}
}

func TestTelemetryHandleInitialize(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		req           mcp.Request
		nextResult    mcp.Result
		nextErr       error
		wantLogged    string   // substring expected in log output
		wantLogFields []string // additional substrings expected in log output (structured field values)
		wantErr       bool
	}{
		{
			name: "successful initialization logs client and server info",
			req: mockRequest(&mcp.InitializeParams{
				ProtocolVersion: "2025-03-26",
				ClientInfo:      &mcp.Implementation{Name: "test-client", Version: "1.0"},
				Capabilities:    &mcp.ClientCapabilities{},
			}),
			nextResult: &mcp.InitializeResult{
				ProtocolVersion: "2025-03-26",
				ServerInfo:      &mcp.Implementation{Name: "test-server", Version: "2.0"},
			},
			nextErr:       nil,
			wantLogged:    "MCP server initialized",
			wantLogFields: []string{"test-client", "test-server"},
			wantErr:       false,
		},
		{
			name: "successful initialization with nil client info",
			req: mockRequest(&mcp.InitializeParams{
				ProtocolVersion: "2025-03-26",
				Capabilities:    &mcp.ClientCapabilities{},
			}),
			nextResult: &mcp.InitializeResult{
				ProtocolVersion: "2025-03-26",
				ServerInfo:      &mcp.Implementation{Name: "srv", Version: "1"},
			},
			nextErr:    nil,
			wantLogged: "MCP server initialized",
			wantErr:    false,
		},
		{
			name: "successful initialization with nil server info does not panic",
			req: mockRequest(&mcp.InitializeParams{
				ProtocolVersion: "2025-03-26",
				ClientInfo:      &mcp.Implementation{Name: "test-client", Version: "1.0"},
				Capabilities:    &mcp.ClientCapabilities{},
			}),
			nextResult: &mcp.InitializeResult{
				ProtocolVersion: "2025-03-26",
				ServerInfo:      nil,
			},
			nextErr:    nil,
			wantLogged: "MCP server initialized",
			wantErr:    false,
		},
		{
			name: "failed initialization logs error",
			req: mockRequest(&mcp.InitializeParams{
				ProtocolVersion: "2025-03-26",
				ClientInfo:      &mcp.Implementation{Name: "c", Version: "1"},
				Capabilities:    &mcp.ClientCapabilities{},
			}),
			nextResult: nil,
			nextErr:    errors.New("init boom"),
			wantLogged: "MCP initialization failed",
			wantErr:    true,
		},
		{
			name:       "invalid params type falls through gracefully",
			req:        mockRequest(&mcp.PingParams{}),
			nextResult: &mcp.InitializeResult{ProtocolVersion: "2025-03-26"},
			nextErr:    nil,
			wantLogged: "Failed to extract initialize params for telemetry",
			wantErr:    false,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger, buf := newTestLogger()

			nextCalled := false
			next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				nextCalled = true
				return tc.nextResult, tc.nextErr
			}

			result, err := telemetryHandleInitialize(context.Background(), methodInitialize, tc.req, next, logger)

			require.True(t, nextCalled, "expected next handler to be called")

			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			// The handler always returns whatever next returns (possibly nil).
			require.Equal(t, tc.nextResult, result)

			output := buf.String()
			require.Contains(t, output, tc.wantLogged)
			for _, field := range tc.wantLogFields {
				require.Contains(t, output, field, "expected structured log field value %q in output", field)
			}
		})
	}
}

func TestTelemetryHandleToolCall(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		req           mcp.Request
		nextResult    mcp.Result
		nextErr       error
		wantLogged    string
		wantLogFields []string // additional substrings expected in log output (structured field values)
		wantErr       bool
	}{
		{
			name: "successful tool call logs tool name and duration",
			req: mockRequest(&mcp.CallToolParamsRaw{
				Name:      "query",
				Arguments: json.RawMessage(`{"query":"up"}`),
			}),
			nextResult: &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "result data"}},
			},
			nextErr:       nil,
			wantLogged:    "Finished calling tool",
			wantLogFields: []string{"query", "duration"},
			wantErr:       false,
		},
		{
			name: "failed tool call logs error",
			req: mockRequest(&mcp.CallToolParamsRaw{
				Name:      "query",
				Arguments: json.RawMessage(`{"query":"up"}`),
			}),
			nextResult: &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "error"}},
			},
			nextErr:    errors.New("tool boom"),
			wantLogged: "Failed calling tool",
			wantErr:    true,
		},
		{
			name: "failed type assertion on result returns early without panic",
			req: mockRequest(&mcp.CallToolParamsRaw{
				Name:      "query",
				Arguments: json.RawMessage(`{}`),
			}),
			// Return a non-CallToolResult to trigger failed type assertion.
			nextResult: &mcp.InitializeResult{ProtocolVersion: "2025-03-26"},
			nextErr:    nil,
			wantLogged: "Failed to convert result to call tool result",
			wantErr:    false,
		},
		{
			name: "nil result from next returns early without panic",
			req: mockRequest(&mcp.CallToolParamsRaw{
				Name:      "query",
				Arguments: json.RawMessage(`{}`),
			}),
			// Return nil result with an error to trigger failed type
			// assertion on nil interface value.
			nextResult: nil,
			nextErr:    errors.New("nil result boom"),
			wantLogged: "Failed to convert result to call tool result",
			wantErr:    true,
		},
		{
			name: "tool result with IsError true logs failure",
			req: mockRequest(&mcp.CallToolParamsRaw{
				Name:      "query",
				Arguments: json.RawMessage(`{"query":"bad"}`),
			}),
			nextResult: &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "something went wrong"}},
				IsError: true,
			},
			nextErr:    nil,
			wantLogged: "Failed calling tool",
			wantErr:    false,
		},
		{
			name:       "invalid params type falls through gracefully",
			req:        mockRequest(&mcp.PingParams{}),
			nextResult: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}},
			nextErr:    nil,
			wantLogged: "Failed to extract tool params for telemetry",
			wantErr:    false,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger, buf := newTestLogger()

			nextCalled := false
			next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				nextCalled = true
				return tc.nextResult, tc.nextErr
			}

			result, err := telemetryHandleToolCall(context.Background(), methodToolsCall, tc.req, next, logger)

			require.True(t, nextCalled, "expected next handler to be called")

			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tc.nextResult, result)

			output := buf.String()
			require.Contains(t, output, tc.wantLogged)
			for _, field := range tc.wantLogFields {
				require.Contains(t, output, field, "expected structured log field value %q in output", field)
			}
		})
	}
}

func TestTelemetryHandleResourceRead(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		req           mcp.Request
		nextResult    mcp.Result
		nextErr       error
		wantLogged    string
		wantLogFields []string // additional substrings expected in log output (structured field values)
		wantErr       bool
	}{
		{
			name: "successful resource read logs URI and duration",
			req: mockRequest(&mcp.ReadResourceParams{
				URI: "prometheus://targets",
			}),
			nextResult: &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{URI: "prometheus://targets", Text: "targets data"}},
			},
			nextErr:       nil,
			wantLogged:    "Finished calling resource",
			wantLogFields: []string{"prometheus://targets", "duration"},
			wantErr:       false,
		},
		{
			name: "failed resource read logs error",
			req: mockRequest(&mcp.ReadResourceParams{
				URI: "prometheus://metrics",
			}),
			nextResult: nil,
			nextErr:    errors.New("resource boom"),
			wantLogged: "Failed calling resource",
			wantErr:    true,
		},
		{
			name:       "invalid params type falls through gracefully",
			req:        mockRequest(&mcp.PingParams{}),
			nextResult: &mcp.ReadResourceResult{},
			nextErr:    nil,
			wantLogged: "Failed to extract resource params for telemetry",
			wantErr:    false,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger, buf := newTestLogger()

			nextCalled := false
			next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				nextCalled = true
				return tc.nextResult, tc.nextErr
			}

			result, err := telemetryHandleResourceRead(context.Background(), methodResourcesRead, tc.req, next, logger)

			require.True(t, nextCalled, "expected next handler to be called")

			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tc.nextResult, result)

			output := buf.String()
			require.Contains(t, output, tc.wantLogged)
			for _, field := range tc.wantLogFields {
				require.Contains(t, output, field, "expected structured log field value %q in output", field)
			}
		})
	}
}

// TestAuthContextMiddleware tests the HTTP middleware that extracts
// Authorization headers and adds them to the request context.
func TestAuthContextMiddleware(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		authHeader    string
		expectedAuth  string
		otherHeaders  map[string]string
		verifyHeaders bool
	}{
		{
			name:         "extracts Bearer token from Authorization header",
			authHeader:   "Bearer my-secret-token",
			expectedAuth: "Bearer my-secret-token",
		},
		{
			name:         "extracts Basic auth from Authorization header",
			authHeader:   "Basic dXNlcm5hbWU6cGFzc3dvcmQ=",
			expectedAuth: "Basic dXNlcm5hbWU6cGFzc3dvcmQ=",
		},
		{
			name:         "handles missing Authorization header gracefully",
			authHeader:   "",
			expectedAuth: "",
		},
		{
			name:         "handles Authorization header with only whitespace",
			authHeader:   "   ",
			expectedAuth: "   ", // Note: middleware stores the raw value, does not trim
		},
		{
			name:          "preserves other headers and request properties",
			authHeader:    "Bearer test-token",
			expectedAuth:  "Bearer test-token",
			otherHeaders:  map[string]string{"X-Custom-Header": "custom-value", "Content-Type": "application/json"},
			verifyHeaders: true,
		},
		{
			name:         "handles token without type prefix",
			authHeader:   "raw-token-value",
			expectedAuth: "raw-token-value",
		},
		{
			name:         "handles multi-word Bearer token",
			authHeader:   "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ",
			expectedAuth: "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var capturedAuth string
			var capturedReq *http.Request

			innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedAuth = getAuthFromContext(r.Context())
				capturedReq = r
				w.WriteHeader(http.StatusOK)
			})

			handler := authContextMiddleware(innerHandler)

			req := httptest.NewRequest(http.MethodPost, "/mcp/v1", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}

			// Add other headers if specified.
			for key, value := range tc.otherHeaders {
				req.Header.Set(key, value)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			require.Equal(t, http.StatusOK, rr.Code)

			// Verify auth context value.
			// Empty auth headers are not added to context, but whitespace-only ones are.
			if tc.authHeader == "" {
				require.Empty(t, capturedAuth)
			} else {
				require.Equal(t, tc.expectedAuth, capturedAuth)
			}

			// Verify other headers were preserved.
			if tc.verifyHeaders {
				for key, value := range tc.otherHeaders {
					require.Equal(t, value, capturedReq.Header.Get(key))
				}
			}
		})
	}
}

// TestAuthContextMiddleware_RequestMethod verifies that the middleware
// correctly handles requests with different HTTP methods.
func TestAuthContextMiddleware_RequestMethod(t *testing.T) {
	t.Parallel()

	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions}

	for _, method := range methods {
		method := method
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			var capturedAuth string
			var capturedMethod string

			innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedAuth = getAuthFromContext(r.Context())
				capturedMethod = r.Method
				w.WriteHeader(http.StatusOK)
			})

			handler := authContextMiddleware(innerHandler)

			req := httptest.NewRequest(method, "/mcp/v1", nil)
			req.Header.Set("Authorization", "Bearer method-test-token")

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			require.Equal(t, http.StatusOK, rr.Code)
			require.Equal(t, method, capturedMethod)
			require.Equal(t, "Bearer method-test-token", capturedAuth)
		})
	}
}

// TestFullAuthFlow tests the complete auth flow from HTTP request through
// tool execution to Prometheus API call.
func TestFullAuthFlow(t *testing.T) {
	t.Parallel()

	t.Run("auth header flows from HTTP request to Prometheus API call", func(t *testing.T) {
		t.Parallel()

		var prometheusReceivedAuth string
		var mu sync.Mutex

		// Create a mock Prometheus server that captures the Authorization header.
		promServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			prometheusReceivedAuth = r.Header.Get("Authorization")
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
		}))
		defer promServer.Close()

		// Create container pointing to mock Prometheus server.
		container := newTestContainer(nil)
		container.prometheusURL = promServer.URL
		container.defaultRT = http.DefaultTransport

		// Create context with auth header.
		ctx := addAuthToContext(context.Background(), "Bearer my-secret-api-token")

		// Get an API client with auth from context.
		_, rt := container.GetAPIClient(ctx)

		// Make a request through the RoundTripper to verify auth is forwarded.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, promServer.URL+"/api/v1/query", nil)
		require.NoError(t, err)

		_, err = rt.RoundTrip(req)
		require.NoError(t, err)

		mu.Lock()
		defer mu.Unlock()
		require.Equal(t, "Bearer my-secret-api-token", prometheusReceivedAuth)
	})

	t.Run("Basic auth flows through to Prometheus API call", func(t *testing.T) {
		t.Parallel()

		var prometheusReceivedAuth string
		var mu sync.Mutex

		promServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			prometheusReceivedAuth = r.Header.Get("Authorization")
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
		}))
		defer promServer.Close()

		container := newTestContainer(nil)
		container.prometheusURL = promServer.URL
		container.defaultRT = http.DefaultTransport

		ctx := addAuthToContext(context.Background(), "Basic dXNlcm5hbWU6cGFzc3dvcmQ=")

		_, rt := container.GetAPIClient(ctx)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, promServer.URL+"/api/v1/query", nil)
		require.NoError(t, err)

		_, err = rt.RoundTrip(req)
		require.NoError(t, err)

		mu.Lock()
		defer mu.Unlock()
		require.Equal(t, "Basic dXNlcm5hbWU6cGFzc3dvcmQ=", prometheusReceivedAuth)
	})

	t.Run("token without type prefix assumes Bearer", func(t *testing.T) {
		t.Parallel()

		var prometheusReceivedAuth string
		var mu sync.Mutex

		promServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			prometheusReceivedAuth = r.Header.Get("Authorization")
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
		}))
		defer promServer.Close()

		container := newTestContainer(nil)
		container.prometheusURL = promServer.URL
		container.defaultRT = http.DefaultTransport

		// Token without type prefix - should be treated as Bearer.
		ctx := addAuthToContext(context.Background(), "raw-token-only")

		_, rt := container.GetAPIClient(ctx)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, promServer.URL+"/api/v1/query", nil)
		require.NoError(t, err)

		_, err = rt.RoundTrip(req)
		require.NoError(t, err)

		mu.Lock()
		defer mu.Unlock()
		// createClientWithAuth should add Bearer prefix for raw tokens.
		require.Equal(t, "Bearer raw-token-only", prometheusReceivedAuth)
	})

	t.Run("different requests get different auth contexts", func(t *testing.T) {
		t.Parallel()

		var mu sync.Mutex
		receivedAuths := make(map[string]string)

		// Create mock Prometheus server that tracks auth headers by request path.
		promServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			receivedAuths[r.URL.Path] = r.Header.Get("Authorization")
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
		}))
		defer promServer.Close()

		container := newTestContainer(nil)
		container.prometheusURL = promServer.URL
		container.defaultRT = http.DefaultTransport

		// Simulate two concurrent requests with different auth contexts.
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			ctx := addAuthToContext(context.Background(), "Bearer tenant-a-token")
			_, rt := container.GetAPIClient(ctx)

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, promServer.URL+"/api/v1/tenant-a", nil)
			assert.NoError(t, err)

			_, err = rt.RoundTrip(req)
			assert.NoError(t, err)
		}()

		go func() {
			defer wg.Done()
			ctx := addAuthToContext(context.Background(), "Bearer tenant-b-token")
			_, rt := container.GetAPIClient(ctx)

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, promServer.URL+"/api/v1/tenant-b", nil)
			assert.NoError(t, err)

			_, err = rt.RoundTrip(req)
			assert.NoError(t, err)
		}()

		wg.Wait()

		mu.Lock()
		defer mu.Unlock()

		// Verify each tenant received their correct auth token.
		require.Equal(t, "Bearer tenant-a-token", receivedAuths["/api/v1/tenant-a"])
		require.Equal(t, "Bearer tenant-b-token", receivedAuths["/api/v1/tenant-b"])
	})

	t.Run("no auth in context uses default client without auth header", func(t *testing.T) {
		t.Parallel()

		var prometheusReceivedAuth string
		var requestReceived bool
		var mu sync.Mutex

		promServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			prometheusReceivedAuth = r.Header.Get("Authorization")
			requestReceived = true
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
		}))
		defer promServer.Close()

		mockAPI := &MockPrometheusAPI{}
		container := newTestContainer(mockAPI)
		container.prometheusURL = promServer.URL
		container.defaultRT = http.DefaultTransport

		// Context without auth.
		ctx := context.Background()

		client, rt := container.GetAPIClient(ctx)

		// Should return the default mock client.
		require.Equal(t, mockAPI, client)
		require.Equal(t, http.DefaultTransport, rt)

		// Make a request with the default RoundTripper.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, promServer.URL+"/api/v1/query", nil)
		require.NoError(t, err)

		_, err = rt.RoundTrip(req)
		require.NoError(t, err)

		mu.Lock()
		defer mu.Unlock()
		require.True(t, requestReceived)
		// Default client should not add auth headers.
		require.Empty(t, prometheusReceivedAuth)
	})
}

// TestAddAuthToContext tests the addAuthToContext helper function.
func TestAddAuthToContext(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		auth     string
		expected string
	}{
		{
			name:     "adds Bearer token to context",
			auth:     "Bearer token123",
			expected: "Bearer token123",
		},
		{
			name:     "adds Basic auth to context",
			auth:     "Basic dXNlcjpwYXNz",
			expected: "Basic dXNlcjpwYXNz",
		},
		{
			name:     "adds empty string to context",
			auth:     "",
			expected: "",
		},
		{
			name:     "adds arbitrary string to context",
			auth:     "CustomAuth xyz",
			expected: "CustomAuth xyz",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := addAuthToContext(context.Background(), tc.auth)
			result := getAuthFromContext(ctx)
			require.Equal(t, tc.expected, result)
		})
	}
}

// TestGetAuthFromContext tests the getAuthFromContext helper function.
func TestGetAuthFromContext(t *testing.T) {
	t.Parallel()

	t.Run("returns empty string for context without auth", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		require.Empty(t, getAuthFromContext(ctx))
	})

	t.Run("returns auth value from context", func(t *testing.T) {
		t.Parallel()
		ctx := addAuthToContext(context.Background(), "Bearer secret")
		require.Equal(t, "Bearer secret", getAuthFromContext(ctx))
	})

	t.Run("returns empty string for context with wrong type value", func(t *testing.T) {
		t.Parallel()
		// Create context with wrong type for auth key.
		ctx := context.WithValue(context.Background(), authHeaderKey{}, 12345)
		require.Empty(t, getAuthFromContext(ctx))
	})
}

// TestAuthContextMiddleware_Integration tests the full HTTP request flow
// through the middleware to verify context propagation.
func TestAuthContextMiddleware_Integration(t *testing.T) {
	t.Parallel()

	t.Run("middleware propagates auth through handler chain", func(t *testing.T) {
		t.Parallel()

		var capturedAuths []string
		var mu sync.Mutex

		// Create a chain of handlers to verify context propagation.
		innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := getAuthFromContext(r.Context())
			mu.Lock()
			capturedAuths = append(capturedAuths, auth)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		})

		handler := authContextMiddleware(innerHandler)

		// Make multiple requests with different auth values.
		tokens := []string{"Bearer token1", "Bearer token2", "Basic creds"}
		for _, token := range tokens {
			req := httptest.NewRequest(http.MethodPost, "/mcp/v1", nil)
			req.Header.Set("Authorization", token)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			require.Equal(t, http.StatusOK, rr.Code)
		}

		mu.Lock()
		defer mu.Unlock()
		require.Equal(t, tokens, capturedAuths)
	})

	t.Run("middleware handles request body correctly", func(t *testing.T) {
		t.Parallel()

		var capturedBody string
		var readErr error

		innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			readErr = err
			capturedBody = string(body)
			w.WriteHeader(http.StatusOK)
		})

		handler := authContextMiddleware(innerHandler)

		requestBody := `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"query"}}`
		req := httptest.NewRequest(http.MethodPost, "/mcp/v1", strings.NewReader(requestBody))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		require.NoError(t, readErr)
		require.Equal(t, http.StatusOK, rr.Code)
		require.Equal(t, requestBody, capturedBody)
	})
}

// TestForwardedHeadersContext tests storing and retrieving forwarded headers
// from a context.
func TestForwardedHeadersContext(t *testing.T) {
	t.Parallel()

	t.Run("stores and retrieves headers", func(t *testing.T) {
		t.Parallel()

		headers := http.Header{}
		headers.Set("X-Scope-Orgid", "tenant-a")

		ctx := addForwardedHeadersToContext(context.Background(), headers)
		got := getForwardedHeadersFromContext(ctx)
		require.Equal(t, "tenant-a", got.Get("X-Scope-OrgID"))
	})

	t.Run("returns nil for context without headers", func(t *testing.T) {
		t.Parallel()

		require.Nil(t, getForwardedHeadersFromContext(context.Background()))
	})
}

// TestAuthContextMiddleware_ForwardHeaders tests that the middleware captures
// additional configured headers into the request context.
func TestAuthContextMiddleware_ForwardHeaders(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		forwardHeaders  []string
		requestHeaders  map[string][]string
		expectedHeaders map[string][]string
		expectNil       bool
	}{
		{
			name:            "captures configured header",
			forwardHeaders:  []string{"X-Scope-OrgID"},
			requestHeaders:  map[string][]string{"X-Scope-OrgID": {"tenant-a"}},
			expectedHeaders: map[string][]string{"X-Scope-OrgID": {"tenant-a"}},
		},
		{
			name:            "preserves multiple values of a configured header",
			forwardHeaders:  []string{"X-Scope-OrgID"},
			requestHeaders:  map[string][]string{"X-Scope-OrgID": {"tenant-a", "tenant-b"}},
			expectedHeaders: map[string][]string{"X-Scope-OrgID": {"tenant-a", "tenant-b"}},
		},
		{
			name:           "ignores headers that are not configured",
			forwardHeaders: []string{"X-Scope-OrgID"},
			requestHeaders: map[string][]string{"X-Custom-Header": {"custom-value"}},
			expectNil:      true,
		},
		{
			name:           "captures nothing when no headers configured",
			forwardHeaders: nil,
			requestHeaders: map[string][]string{"X-Scope-OrgID": {"tenant-a"}},
			expectNil:      true,
		},
		{
			name:           "captures multiple configured headers",
			forwardHeaders: []string{"X-Scope-OrgID", "X-Request-ID"},
			requestHeaders: map[string][]string{
				"X-Scope-OrgID": {"tenant-a"},
				"X-Request-ID":  {"req-123"},
			},
			expectedHeaders: map[string][]string{
				"X-Scope-OrgID": {"tenant-a"},
				"X-Request-ID":  {"req-123"},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var captured http.Header

			innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured = getForwardedHeadersFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			})

			middleware := authContextMiddleware(innerHandler, tc.forwardHeaders...)

			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			for name, values := range tc.requestHeaders {
				for _, v := range values {
					req.Header.Add(name, v)
				}
			}

			rr := httptest.NewRecorder()
			middleware.ServeHTTP(rr, req)
			require.Equal(t, http.StatusOK, rr.Code)

			if tc.expectNil {
				require.Nil(t, captured)
				return
			}
			for name, values := range tc.expectedHeaders {
				require.Equal(t, values, captured.Values(name))
			}
		})
	}
}

// TestFullHeaderForwardingFlow tests that forwarded headers captured from an
// incoming request reach the Prometheus backend, alongside Authorization.
func TestFullHeaderForwardingFlow(t *testing.T) {
	t.Parallel()

	newCaptureServer := func(mu *sync.Mutex, auth, orgID *string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			*auth = r.Header.Get("Authorization")
			*orgID = r.Header.Get("X-Scope-OrgID")
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
		}))
	}

	t.Run("forwarded header and auth both reach Prometheus", func(t *testing.T) {
		t.Parallel()

		var mu sync.Mutex
		var receivedAuth, receivedOrgID string
		promServer := newCaptureServer(&mu, &receivedAuth, &receivedOrgID)
		defer promServer.Close()

		container := newTestContainer(nil)
		container.prometheusURL = promServer.URL
		container.defaultRT = http.DefaultTransport

		headers := http.Header{}
		headers.Set("X-Scope-Orgid", "tenant-a")
		ctx := addAuthToContext(context.Background(), "Bearer my-secret-api-token")
		ctx = addForwardedHeadersToContext(ctx, headers)

		_, rt := container.GetAPIClient(ctx)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, promServer.URL+"/api/v1/query", nil)
		require.NoError(t, err)
		_, err = rt.RoundTrip(req)
		require.NoError(t, err)

		mu.Lock()
		defer mu.Unlock()
		require.Equal(t, "Bearer my-secret-api-token", receivedAuth)
		require.Equal(t, "tenant-a", receivedOrgID)
	})

	t.Run("forwarded header reaches Prometheus without auth", func(t *testing.T) {
		t.Parallel()

		var mu sync.Mutex
		var receivedAuth, receivedOrgID string
		promServer := newCaptureServer(&mu, &receivedAuth, &receivedOrgID)
		defer promServer.Close()

		container := newTestContainer(nil)
		container.prometheusURL = promServer.URL
		container.defaultRT = http.DefaultTransport

		headers := http.Header{}
		headers.Set("X-Scope-Orgid", "tenant-b")
		ctx := addForwardedHeadersToContext(context.Background(), headers)

		_, rt := container.GetAPIClient(ctx)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, promServer.URL+"/api/v1/query", nil)
		require.NoError(t, err)
		_, err = rt.RoundTrip(req)
		require.NoError(t, err)

		mu.Lock()
		defer mu.Unlock()
		require.Empty(t, receivedAuth)
		require.Equal(t, "tenant-b", receivedOrgID)
	})

	t.Run("default client sends neither auth nor forwarded headers", func(t *testing.T) {
		t.Parallel()

		var mu sync.Mutex
		var receivedAuth, receivedOrgID string
		promServer := newCaptureServer(&mu, &receivedAuth, &receivedOrgID)
		defer promServer.Close()

		container := newTestContainer(nil)
		container.prometheusURL = promServer.URL
		container.defaultRT = http.DefaultTransport

		ctx := context.Background()
		_, rt := container.GetAPIClient(ctx)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, promServer.URL+"/api/v1/query", nil)
		require.NoError(t, err)
		_, err = rt.RoundTrip(req)
		require.NoError(t, err)

		mu.Lock()
		defer mu.Unlock()
		require.Empty(t, receivedAuth)
		require.Empty(t, receivedOrgID)
	})
}
