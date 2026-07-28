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
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alpkeskin/gotoon"
	"github.com/blevesearch/bleve/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/config"
	"github.com/prometheus/common/promslog"
	promversion "github.com/prometheus/common/version"
	"github.com/tmc/langchaingo/textsplitter"

	"github.com/prometheus/prometheus-mcp/internal/metrics"
	mcpProm "github.com/prometheus/prometheus-mcp/pkg/prometheus"
)

// Embedded assets for the MCP server.
//
//go:embed assets/*
var assets embed.FS

// bleveSetLogOnce ensures the Bleve logger is only configured once,
// preventing race conditions when multiple goroutines call buildDocsState.
var bleveSetLogOnce sync.Once

// Prometheus metrics for MCP server instrumentation.
var (
	metricServerReady = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: prometheus.BuildFQName(metrics.MetricNamespace, "server", "ready"),
			Help: "Info metric with a static '1' if the MCP server is ready, and '0' otherwise.",
		},
	)

	metricToolCallDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:                        prometheus.BuildFQName(metrics.MetricNamespace, "tool", "call_duration_seconds"),
			Help:                        "Duration of tool calls, per tool, in seconds.",
			Buckets:                     prometheus.ExponentialBuckets(0.25, 2, 10),
			NativeHistogramBucketFactor: 1.1,
		},
		[]string{"tool_name"},
	)

	metricToolCallsFailed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: prometheus.BuildFQName(metrics.MetricNamespace, "tool", "calls_failed_total"),
			Help: "Total number of failures per tool.",
		},
		[]string{"tool_name"},
	)

	metricResourceCallDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:                        prometheus.BuildFQName(metrics.MetricNamespace, "resource", "call_duration_seconds"),
			Help:                        "Duration of resource calls, per resource, in seconds.",
			Buckets:                     prometheus.ExponentialBuckets(0.25, 2, 10),
			NativeHistogramBucketFactor: 1.1,
		},
		[]string{"resource_uri"},
	)

	metricResourceCallsFailed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: prometheus.BuildFQName(metrics.MetricNamespace, "resource", "calls_failed_total"),
			Help: "Total number of failures per resource.",
		},
		[]string{"resource_uri"},
	)

	metricPromptGetDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:                        prometheus.BuildFQName(metrics.MetricNamespace, "prompt", "get_duration_seconds"),
			Help:                        "Duration of prompt get requests, per prompt, in seconds.",
			Buckets:                     prometheus.ExponentialBuckets(0.25, 2, 10),
			NativeHistogramBucketFactor: 1.1,
		},
		[]string{"prompt_name"},
	)

	metricPromptGetsFailed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: prometheus.BuildFQName(metrics.MetricNamespace, "prompt", "gets_failed_total"),
			Help: "Total number of failures per prompt.",
		},
		[]string{"prompt_name"},
	)

	metricPromptListDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:                        prometheus.BuildFQName(metrics.MetricNamespace, "prompt", "list_duration_seconds"),
			Help:                        "Duration of prompt list requests, in seconds.",
			Buckets:                     prometheus.ExponentialBuckets(0.25, 2, 10),
			NativeHistogramBucketFactor: 1.1,
		},
	)

	metricPromptListsFailed = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: prometheus.BuildFQName(metrics.MetricNamespace, "prompt", "lists_failed_total"),
			Help: "Total number of prompt list request failures.",
		},
	)
)

func init() {
	metrics.Registry.MustRegister(
		metricServerReady,
		metricToolCallDuration,
		metricToolCallsFailed,
		metricResourceCallDuration,
		metricResourceCallsFailed,
		metricPromptGetDuration,
		metricPromptGetsFailed,
		metricPromptListDuration,
		metricPromptListsFailed,
	)
}

// ServerConfig holds configuration for creating a new MCP server.
type ServerConfig struct {
	Logger                *slog.Logger
	PrometheusURL         string
	PrometheusBackend     string
	PrometheusTimeout     time.Duration
	TruncationLimit       int
	RoundTripper          http.RoundTripper
	TSDBAdminToolsEnabled bool
	EnabledTools          []string
	DocsFS                fs.FS
	ToonOutputEnabled     bool
	ClientLoggingEnabled  bool
	KeepAlive             time.Duration
}

// NewServer creates a new MCP server using the official Go SDK.
func NewServer(ctx context.Context, cfg ServerConfig) (*mcp.Server, *ServerContainer, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = promslog.NewNopLogger()
	}

	container, err := newServerContainer(cfg)
	if err != nil {
		return nil, nil, err
	}

	// Select the appropriate toolset based on configuration.
	toolsetMap := getToolset(toolsetConfig{
		enabledTools:      cfg.EnabledTools,
		prometheusBackend: cfg.PrometheusBackend,
		logger:            logger,
	})
	toolset := toolsetToToolRegistrationSlice(toolsetMap)

	// A render failure falls back to a bare server name: instructions are
	// worth having but not worth failing startup over.
	instrx, err := renderInstructions(cfg, toolsetMap)
	if err != nil {
		logger.Error("Failed to render server instructions", "err", err)
		instrx = "Prometheus MCP Server"
	}

	// Declare the SEP-2640 skills extension so skill-aware hosts can
	// discover the skill:// resources. Logging is pinned explicitly
	// because a non-nil Capabilities overrides the SDK's historical
	// {"logging":{}} default; the remaining capabilities are still
	// inferred from the registered features.
	caps := &mcp.ServerCapabilities{Logging: &mcp.LoggingCapabilities{}}
	caps.AddExtension(skillsExtensionCapability, nil)

	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    "prometheus-mcp",
			Title:   "Prometheus MCP Server",
			Version: promversion.Info(),
		},
		&mcp.ServerOptions{
			Instructions: instrx,
			Logger:       logger.WithGroup("go_sdk_logger"),
			KeepAlive:    cfg.KeepAlive,
			Capabilities: caps,
		},
	)

	// Register tools.
	registerTools(server, container, toolset)

	// Register resources.
	registerResources(server, container)

	// Register prompts.
	registerPrompts(server)

	// Add telemetry middleware for metrics and logging.
	server.AddReceivingMiddleware(telemetryMiddleware(logger))

	logger.Info("MCP server created",
		"prometheus_url", cfg.PrometheusURL,
		"tool_count", len(toolset),
	)

	return server, container, nil
}

// NewStreamableHTTPHandler creates an HTTP handler for the MCP server using
// stateless HTTP transport. In stateless mode, each request is handled
// independently without session tracking, and responses are returned as
// application/json rather than text/event-stream.
// It wraps the handler with auth context middleware to forward Authorization headers.
func NewStreamableHTTPHandler(server *mcp.Server, logger *slog.Logger) http.Handler {
	handler := mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server {
			return server
		},
		&mcp.StreamableHTTPOptions{
			Stateless:    true,
			JSONResponse: true,
			Logger:       logger,
		},
	)

	// Wrap with auth context middleware
	return authContextMiddleware(handler)
}

// authHeaderKey is the context key for storing the Authorization header.
type authHeaderKey struct{}

// addAuthToContext adds an Authorization header value to the context.
func addAuthToContext(ctx context.Context, auth string) context.Context {
	return context.WithValue(ctx, authHeaderKey{}, auth)
}

// getAuthFromContext retrieves the Authorization header from the context.
func getAuthFromContext(ctx context.Context) string {
	if auth, ok := ctx.Value(authHeaderKey{}).(string); ok {
		return auth
	}
	return ""
}

// authContextMiddleware creates an HTTP middleware that extracts the Authorization
// header from requests and adds it to the request context.
func authContextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if auth := r.Header.Get("Authorization"); auth != "" {
			ctx = addAuthToContext(ctx, auth)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// docsState holds the documentation filesystem and search index.
// It is designed to be swapped atomically for live documentation updates.
type docsState struct {
	fs          fs.FS
	searchIndex bleve.Index
}

// ServerContainer holds all dependencies needed by tool and resource handlers.
// It replaces the middleware-based context smuggling pattern needed with the
// mcp-go library with explicit dependency injection.
type ServerContainer struct {
	// Core dependencies.
	logger            *slog.Logger
	defaultAPIClient  promv1.API
	prometheusURL     string
	defaultRT         http.RoundTripper
	defaultHTTPClient http.Client

	// Configuration values the MCP server needs to use/cares about.
	truncationLimit       int
	toonOutputEnabled     bool
	tsdbAdminToolsEnabled bool
	apiTimeout            time.Duration
	clientLoggingEnabled  bool

	// Docs state management.
	docsMu sync.RWMutex
	docs   *docsState
}

// newServerContainer creates a new ServerContainer with the given configuration.
func newServerContainer(cfg ServerConfig) (*ServerContainer, error) {
	client, err := mcpProm.NewAPIClient(cfg.PrometheusURL, cfg.RoundTripper)
	if err != nil {
		return nil, fmt.Errorf("failed to create default API client: %w", err)
	}

	container := &ServerContainer{
		logger:                cfg.Logger,
		defaultAPIClient:      client,
		prometheusURL:         cfg.PrometheusURL,
		defaultRT:             cfg.RoundTripper,
		defaultHTTPClient:     http.Client{Transport: cfg.RoundTripper},
		truncationLimit:       cfg.TruncationLimit,
		toonOutputEnabled:     cfg.ToonOutputEnabled,
		tsdbAdminToolsEnabled: cfg.TSDBAdminToolsEnabled,
		apiTimeout:            cfg.PrometheusTimeout,
		clientLoggingEnabled:  cfg.ClientLoggingEnabled,
	}

	// Initialize docs search if FS is provided.
	if cfg.DocsFS != nil {
		state, err := buildDocsState(cfg.Logger, cfg.DocsFS)
		if err != nil {
			cfg.Logger.Error("Failed to initialize docs search", "err", err)
			// Non-fatal - continue without docs search.
		}

		container.swapDocsState(state)
	}

	return container, nil
}

// GetAPIClient returns a Prometheus API client, optionally with auth from context.
// If an Authorization header is present in the context, a new client with those
// credentials is created. Otherwise, the default client is returned.
func (s *ServerContainer) GetAPIClient(ctx context.Context) (promv1.API, http.RoundTripper) {
	auth := getAuthFromContext(ctx)
	if auth != "" {
		client, rt := s.createClientWithAuth(auth)
		if client != nil {
			return client, rt
		}
		s.logger.Warn("Failed to create client with provided auth, falling back to default client")
	}

	return s.defaultAPIClient, s.defaultRT
}

// createClientWithAuth creates a new API client with the given Authorization header.
func (s *ServerContainer) createClientWithAuth(authorization string) (promv1.API, http.RoundTripper) {
	var authType, secret string
	if strings.Contains(authorization, " ") {
		parts := strings.SplitN(authorization, " ", 2)
		authType = parts[0]
		secret = parts[1]
	} else {
		s.logger.Debug("Assuming Bearer auth type for Authorization header with no type specified")
		authType = "Bearer"
		secret = authorization
	}

	rt := config.NewAuthorizationCredentialsRoundTripper(authType, config.NewInlineSecret(secret), s.defaultRT)
	client, err := mcpProm.NewAPIClient(s.prometheusURL, rt)
	if err != nil {
		s.logger.Error("Failed to create API client with credentials", "err", err)
		return nil, nil
	}
	return client, rt
}

// FormatOutput encodes data as JSON or TOON based on configuration.
func (s *ServerContainer) FormatOutput(data any) (string, error) {
	if s.toonOutputEnabled {
		toonEncoded, err := gotoon.Encode(data)
		if err != nil {
			return "", fmt.Errorf("failed to TOON encode data: %w", err)
		}

		return toonEncoded, nil
	}

	jsonEncoded, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("failed to JSON marshal data: %w", err)
	}
	return string(jsonEncoded), nil
}

// GetEffectiveTruncationLimit returns the per-call limit if set, otherwise the global limit.
func (s *ServerContainer) GetEffectiveTruncationLimit(perCallLimit int) int {
	// Negative means the tool wants to override and disable truncation.
	if perCallLimit < 0 {
		return 0
	}

	// perCallLimit set means we're asking for a specific truncation limit.
	if perCallLimit > 0 {
		return perCallLimit
	}

	// Otherwise, return global truncation limit.
	return s.truncationLimit
}

// Docs search methods

// errDocsNotProvided is returned when docs filesystem is not configured.
var errDocsNotProvided = errors.New("docs filesystem not provided")

// swapDocsState replaces the current docs state with a new one.  It acquires
// the write lock, and only after successfully swapping the new docs in does it
// attempt to close the old docs search index.
func (s *ServerContainer) swapDocsState(state *docsState) {
	s.docsMu.Lock()
	oldState := s.docs
	s.docs = state
	s.docsMu.Unlock()

	if oldState != nil && oldState.searchIndex != nil {
		if err := oldState.searchIndex.Close(); err != nil {
			s.logger.Error("failed to close old search index", "err", err)
		}
	}
}

// buildDocsState creates a new docsState from the given filesystem.
// It chunks the markdown files and builds a search index.
func buildDocsState(logger *slog.Logger, docsFS fs.FS) (*docsState, error) {
	if docsFS == nil {
		return nil, errDocsNotProvided
	}

	splitter := textsplitter.NewMarkdownTextSplitter(
		textsplitter.WithAllowedSpecial([]string{"all"}),
		textsplitter.WithChunkOverlap(docChunkOverlap),
		textsplitter.WithChunkSize(docChunkSize),
		textsplitter.WithCodeBlocks(true),
		textsplitter.WithHeadingHierarchy(true),
		textsplitter.WithJoinTableRows(true),
		textsplitter.WithKeepSeparator(true),
		textsplitter.WithReferenceLinks(true),
	)

	docFiles, err := getDocFileNames(docsFS)
	if err != nil {
		return nil, fmt.Errorf("failed listing docs files: %w", err)
	}

	// Configure bleve logger only once, avoids race conditions if building
	// multiple docs states concurrently.
	bleveSetLogOnce.Do(func() {
		bleve.SetLog(slog.NewLogLogger(logger.Handler(), slog.LevelDebug))
	})
	mapping := bleve.NewIndexMapping()
	searchIndex, err := bleve.NewMemOnly(mapping)
	if err != nil {
		return nil, fmt.Errorf("failed to create in-memory search index: %w", err)
	}

	for _, fn := range docFiles {
		content, err := getDocFileContent(docsFS, fn)
		if err != nil {
			logger.Error("Failed reading doc file", "file", fn, "err", err)
			continue
		}

		textChunks, err := splitter.SplitText(content)
		if err != nil {
			logger.Error("Failed to split doc into chunks", "file", fn, "err", err)
			continue
		}

		for i, c := range textChunks {
			newChunk := chunk{
				ID:      i + 1,
				Name:    fn,
				Content: c,
			}
			if err := searchIndex.Index(newChunk.String(), newChunk); err != nil {
				logger.Error("Failed to index chunk", "chunk_id", newChunk.String(), "err", err)
				continue
			}
		}
	}

	return &docsState{
		fs:          docsFS,
		searchIndex: searchIndex,
	}, nil
}

// SearchDocs searches the docs index and returns matching chunk IDs.
func (s *ServerContainer) SearchDocs(q string, limit int) ([]string, error) {
	s.docsMu.RLock()
	defer s.docsMu.RUnlock()

	ds := s.docs
	if ds == nil || ds.searchIndex == nil {
		return nil, errors.New("docs search index not initialized")
	}

	if limit < 1 {
		limit = defaultDocsSearchLimit
	}

	query := bleve.NewMatchQuery(q)
	query.Fuzziness = 1

	req := bleve.NewSearchRequest(query)
	req.Size = limit
	req.Highlight = bleve.NewHighlight()
	req.Fields = []string{"*"}

	searchRes, err := ds.searchIndex.Search(req)
	if err != nil {
		return nil, fmt.Errorf("failed to search docs index: %w", err)
	}

	var result []string
	for _, hit := range searchRes.Hits {
		result = append(result, hit.ID)
	}
	return result, nil
}

// GetDocFileNames returns a list of all doc file names.
func (s *ServerContainer) GetDocFileNames() ([]string, error) {
	s.docsMu.RLock()
	defer s.docsMu.RUnlock()

	ds := s.docs
	if ds == nil || ds.fs == nil {
		return nil, errDocsNotProvided
	}
	return getDocFileNames(ds.fs)
}

// GetDocFileContent returns the content of a doc file.
func (s *ServerContainer) GetDocFileContent(path string) (string, error) {
	s.docsMu.RLock()
	defer s.docsMu.RUnlock()

	ds := s.docs
	if ds == nil || ds.fs == nil {
		return "", errDocsNotProvided
	}
	return getDocFileContent(ds.fs, path)
}

// Logging helper methods

// GetToolLogger returns an slog.Logger for use within tool handlers.
// If client logging is enabled, returns a chained logger that logs to both
// the application's log output and sends notifications to the MCP client.
// Otherwise, returns the server's local logger.
//
// The tool name is extracted from the request and used as the logger name
// in MCP client notifications. If input is provided (non-nil), it will be
// added to the logger context. Input types should implement slog.LogValuer
// for structured logging.
//
// This is to supplement the standard logging that every tool gets through the
// telemetry middleware. Since this can also send logs to the MCP client via
// protocol notifications, it's intended to be used for important contextual
// messages that should notify the user and log as appropriate. Currently, it
// is primarily used by the TSDB Admin tools to do extra logging around admin
// tool calls.
func (s *ServerContainer) GetToolLogger(req *mcp.CallToolRequest, input any) *slog.Logger {
	logger := s.logger
	if s.clientLoggingEnabled {
		logger = getChainedLogger(logger, req, req.Params.Name)
	}

	if input != nil {
		logger = logger.With(slog.Any("input", input))
	}

	return logger
}

// MCP result helper methods

// newToolTextResult creates a new CallToolResult with text content.
func newToolTextResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

// newToolErrorResult creates a new CallToolResult indicating an error. We
// ensure that `IsError` is set enabled so clients know it's an error.
//
// Because the telemetry middleware automatically checks the `IsError` field,
// all calls to `newToolErrorResult` result in the tool call failure metric
// incrementing, as well as an error log being automatically generated for the
// tool call failure.
func newToolErrorResult(err string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: err}},
		IsError: true,
	}
}

// embedResourceContentsInToolResult takes a ReadResourceResult and embeds it
// into a CallToolResult by appending a new embedded resource onto the
// CallToolResult's contents. It returns the modified CallToolResult.  This
// allows tools to reuse resource handlers and return their results as embedded
// resources.
func embedResourceContentsInToolResult(resourceResult *mcp.ReadResourceResult, toolResult *mcp.CallToolResult) *mcp.CallToolResult {
	embeddedRes := make([]mcp.Content, len(resourceResult.Contents))
	for i, r := range resourceResult.Contents {
		embeddedRes[i] = &mcp.EmbeddedResource{Resource: r}
	}

	toolResult.Content = append(toolResult.Content, embeddedRes...)
	return toolResult
}

// concatResourceContents concatenates Contents from multiple ReadResourceResults into a single slice.
// This allows callers to use the combined contents as needed (e.g., as embedded resources in a tool result).
func concatResourceContents(results ...*mcp.ReadResourceResult) []*mcp.ResourceContents {
	var allContents []*mcp.ResourceContents
	for _, result := range results {
		allContents = append(allContents, result.Contents...)
	}
	return allContents
}
