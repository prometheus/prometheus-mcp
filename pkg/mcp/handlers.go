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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"

	"github.com/prometheus/prometheus-mcp/internal/metrics"
	mcpProm "github.com/prometheus/prometheus-mcp/pkg/prometheus"
)

// Constants and shared types for handlers.
var (
	// DefaultLookbackDelta is the default time range for queries.
	DefaultLookbackDelta = -5 * time.Minute

	// Prometheus metrics for API call instrumentation.
	metricAPICallsFailed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: prometheus.BuildFQName(metrics.MetricNamespace, "api", "calls_failed_total"),
			Help: "Total number of Prometheus API failures, per endpoint.",
		},
		[]string{"target_path"},
	)

	metricAPICallDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:                        prometheus.BuildFQName(metrics.MetricNamespace, "api", "call_duration_seconds"),
			Help:                        "Duration of Prometheus API calls, per endpoint, in seconds.",
			Buckets:                     prometheus.ExponentialBuckets(0.25, 2, 10),
			NativeHistogramBucketFactor: 1.1,
		},
		[]string{"target_path"},
	)

	errTSDBAdminToolsNotEnabled = errors.New("TSDB admin tools must be enabled with `--dangerous.enable-tsdb-admin-tools` flag")
)

// Management API endpoint constants.
const (
	mgmtAPIEndpointPrefix  = "/-/"
	mgmtAPIHealthyEndpoint = mgmtAPIEndpointPrefix + "healthy"
	mgmtAPIReadyEndpoint   = mgmtAPIEndpointPrefix + "ready"
	mgmtAPIReloadEndpoint  = mgmtAPIEndpointPrefix + "reload"
	mgmtAPIQuitEndpoint    = mgmtAPIEndpointPrefix + "quit"

	// defaultRangeQueryDataPoints is the target number of data points for range
	// queries when step is not explicitly provided. The step is auto-calculated
	// to produce approximately this many data points across the query range.
	defaultRangeQueryDataPoints = 250
)

func init() {
	metrics.Registry.MustRegister(
		metricAPICallsFailed,
		metricAPICallDuration,
	)
}

// queryAPIResponse is the response structure for query API calls.
type queryAPIResponse struct {
	Result   string          `json:"result"`
	Warnings promv1.Warnings `json:"warnings"`
}

// truncateStringByLines truncates a string to the specified number of lines.
// Returns the truncated string and a boolean indicating if truncation occurred.
func truncateStringByLines(s string, limit int) (string, bool) {
	if limit <= 0 {
		// Truncation disabled.
		return s, false
	}

	endMarker := 0
	for range limit {
		// Start from last endMarker marker to find next newline.
		x := strings.Index(s[endMarker:], "\n")
		if x == -1 {
			// No more newlines found, we're below limit, return full string.
			return s, false
		}

		// Always advance past the newline so each iteration starts at
		// the beginning of the next line, and the truncated result
		// includes the trailing newline of the last included line.
		endMarker += x + 1
	}

	// Truncate string by sub-slicing to the end of the last line in the
	// limit.
	return s[:endMarker], true
}

const (
	truncationWarningTemplate = "\n\n" +
		"Warning: The result was truncated because the Prometheus MCP server was started with the flag '--prometheus.truncation-limit=%d'.\n" +
		"You may want to try optimizing your query by refining label filters or using aggregation functions to group results, where possible.\n" +
		"If needed, several tools support a 'truncation_limit'/'limit' argument that can override the global truncation limit on a per-tool-call basis.\n" +
		"This includes the ability to disable truncation on a tool call by setting the truncation limit to -1."
)

// displayTruncationWarning returns a warning message for truncated results.
func displayTruncationWarning(limit int) string {
	return fmt.Sprintf(truncationWarningTemplate, limit)
}

// parseTimeWithDefault parses a time string using ParseTimestampOrDuration.
// If the input is empty, it returns defaultVal. This consolidates the repeated
// optional time parsing pattern used across multiple handlers.
func parseTimeWithDefault(s string, defaultVal time.Time) (time.Time, error) {
	if s == "" {
		return defaultVal, nil
	}
	return mcpProm.ParseTimestampOrDuration(s)
}

// parseTimeRangeInputWithDefaults parses start and end time strings from a
// TimeRangeInput, applying the given defaults when either value is empty. This
// consolidates the repeated pattern used by handlers with independent
// start/end defaults.
func parseTimeRangeInputWithDefaults(tr TimeRangeInput, startDefault, endDefault time.Time) (start, end time.Time, err error) {
	start, err = parseTimeWithDefault(tr.StartTime, startDefault)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("failed to parse start_time: %w", err)
	}
	end, err = parseTimeWithDefault(tr.EndTime, endDefault)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("failed to parse end_time: %w", err)
	}
	return start, end, nil
}

// Tool handler methods for ServerContainer

// QueryHandler handles the instant query tool.
func (s *ServerContainer) QueryHandler(ctx context.Context, req *mcp.CallToolRequest, input QueryInput) (*mcp.CallToolResult, any, error) {
	if input.Query == "" {
		return newToolErrorResult("query parameter is required"), nil, nil
	}

	ts, err := parseTimeWithDefault(input.Timestamp, time.Now())
	if err != nil {
		return newToolErrorResult(fmt.Sprintf("failed to parse timestamp: %v", err)), nil, nil
	}

	truncationLimit := s.GetEffectiveTruncationLimit(input.TruncationLimit)
	result, err := s.queryAPICall(ctx, input.Query, ts, truncationLimit)
	if err != nil {
		return newToolErrorResult("failed making query api call: " + err.Error()), nil, nil
	}

	return newToolTextResult(result), nil, nil
}

// RangeQueryHandler handles the range query tool.
func (s *ServerContainer) RangeQueryHandler(ctx context.Context, req *mcp.CallToolRequest, input RangeQueryInput) (*mcp.CallToolResult, any, error) {
	if input.Query == "" {
		return newToolErrorResult("query parameter is required"), nil, nil
	}

	endTs, err := parseTimeWithDefault(input.EndTime, time.Now())
	if err != nil {
		return newToolErrorResult(fmt.Sprintf("failed to parse end_time: %v", err)), nil, nil
	}

	startTs, err := parseTimeWithDefault(input.StartTime, endTs.Add(DefaultLookbackDelta))
	if err != nil {
		return newToolErrorResult(fmt.Sprintf("failed to parse start_time: %v", err)), nil, nil
	}

	// Calculate step based on actual time range (after parsing user input).
	var step time.Duration
	if input.Step != "" {
		parsedModelStep, err := model.ParseDuration(input.Step)
		if err != nil {
			return newToolErrorResult(fmt.Sprintf("failed to parse step: %v", err)), nil, nil
		}
		parsedStep := time.Duration(parsedModelStep)
		if parsedStep <= 0 {
			return newToolErrorResult("step must be a positive duration (e.g. '30s', '5m', '1h', '1d')"), nil, nil
		}
		step = parsedStep
	} else {
		// Auto-calculate step to produce approximately defaultRangeQueryDataPoints data points.
		resolution := math.Max(math.Floor(endTs.Sub(startTs).Seconds()/defaultRangeQueryDataPoints), 1)
		step = time.Duration(resolution) * time.Second
	}

	truncationLimit := s.GetEffectiveTruncationLimit(input.TruncationLimit)
	result, err := s.rangeQueryAPICall(ctx, input.Query, startTs, endTs, step, truncationLimit)
	if err != nil {
		return newToolErrorResult("failed making range query api call: " + err.Error()), nil, nil
	}
	return newToolTextResult(result), nil, nil
}

// ExemplarQueryHandler handles the exemplar query tool.
func (s *ServerContainer) ExemplarQueryHandler(ctx context.Context, req *mcp.CallToolRequest, input ExemplarQueryInput) (*mcp.CallToolResult, any, error) {
	if input.Query == "" {
		return newToolErrorResult("query parameter is required"), nil, nil
	}

	endTs, err := parseTimeWithDefault(input.EndTime, time.Now())
	if err != nil {
		return newToolErrorResult(fmt.Sprintf("failed to parse end_time: %v", err)), nil, nil
	}

	startTs, err := parseTimeWithDefault(input.StartTime, endTs.Add(DefaultLookbackDelta))
	if err != nil {
		return newToolErrorResult(fmt.Sprintf("failed to parse start_time: %v", err)), nil, nil
	}

	truncationLimit := s.GetEffectiveTruncationLimit(input.TruncationLimit)
	result, err := s.exemplarQueryAPICall(ctx, input.Query, startTs, endTs, truncationLimit)
	if err != nil {
		return newToolErrorResult("failed making exemplar api call: " + err.Error()), nil, nil
	}
	return newToolTextResult(result), nil, nil
}

// SeriesHandler handles the series query tool.
func (s *ServerContainer) SeriesHandler(ctx context.Context, req *mcp.CallToolRequest, input SeriesInput) (*mcp.CallToolResult, any, error) {
	if len(input.Matches) == 0 {
		return newToolErrorResult("at least one matches parameter is required"), nil, nil
	}

	startTs, endTs, err := parseTimeRangeInputWithDefaults(input.TimeRangeInput, time.Time{}, time.Time{})
	if err != nil {
		return newToolErrorResult(err.Error()), nil, nil
	}

	truncationLimit := s.GetEffectiveTruncationLimit(input.TruncationLimit)
	result, err := s.seriesAPICall(ctx, input.Matches, startTs, endTs, truncationLimit)
	if err != nil {
		return newToolErrorResult("failed making series api call: " + err.Error()), nil, nil
	}
	return newToolTextResult(result), nil, nil
}

// LabelNamesHandler handles the label names query tool.
func (s *ServerContainer) LabelNamesHandler(ctx context.Context, req *mcp.CallToolRequest, input LabelNamesInput) (*mcp.CallToolResult, any, error) {
	startTs, endTs, err := parseTimeRangeInputWithDefaults(input.TimeRangeInput, time.Time{}, time.Time{})
	if err != nil {
		return newToolErrorResult(err.Error()), nil, nil
	}

	truncationLimit := s.GetEffectiveTruncationLimit(input.TruncationLimit)
	result, err := s.labelNamesAPICall(ctx, input.Matches, startTs, endTs, truncationLimit)
	if err != nil {
		return newToolErrorResult("failed making label names api call: " + err.Error()), nil, nil
	}
	return newToolTextResult(result), nil, nil
}

// LabelValuesHandler handles the label values query tool.
func (s *ServerContainer) LabelValuesHandler(ctx context.Context, req *mcp.CallToolRequest, input LabelValuesInput) (*mcp.CallToolResult, any, error) {
	if input.Label == "" {
		return newToolErrorResult("label parameter is required"), nil, nil
	}

	startTs, endTs, err := parseTimeRangeInputWithDefaults(input.TimeRangeInput, time.Time{}, time.Time{})
	if err != nil {
		return newToolErrorResult(err.Error()), nil, nil
	}

	truncationLimit := s.GetEffectiveTruncationLimit(input.TruncationLimit)
	result, err := s.labelValuesAPICall(ctx, input.Label, input.Matches, startTs, endTs, truncationLimit)
	if err != nil {
		return newToolErrorResult("failed making label values api call: " + err.Error()), nil, nil
	}
	return newToolTextResult(result), nil, nil
}

// MetricMetadataHandler handles the metric metadata tool.
func (s *ServerContainer) MetricMetadataHandler(ctx context.Context, req *mcp.CallToolRequest, input MetricMetadataInput) (*mcp.CallToolResult, any, error) {
	result, err := s.metricMetadataAPICall(ctx, input.Metric, input.Limit)
	if err != nil {
		return newToolErrorResult("failed making metric metadata api call: " + err.Error()), nil, nil
	}
	return newToolTextResult(result), nil, nil
}

// TargetsMetadataHandler handles the targets metadata tool.
func (s *ServerContainer) TargetsMetadataHandler(ctx context.Context, req *mcp.CallToolRequest, input TargetsMetadataInput) (*mcp.CallToolResult, any, error) {
	result, err := s.targetsMetadataAPICall(ctx, input.MatchTarget, input.Metric, input.Limit)
	if err != nil {
		return newToolErrorResult("failed making targets metadata api call: " + err.Error()), nil, nil
	}
	return newToolTextResult(result), nil, nil
}

// callAPIAndReturnToolResult encapsulates the common pattern for simple tool
// handlers that call a single API method and return the result as a tool text
// response.
func callAPIAndReturnToolResult(ctx context.Context, call func(context.Context) (string, error), errPrefix string) (*mcp.CallToolResult, any, error) {
	result, err := call(ctx)
	if err != nil {
		return newToolErrorResult(errPrefix + err.Error()), nil, nil
	}
	return newToolTextResult(result), nil, nil
}

// AlertmanagersHandler handles the alertmanagers tool.
func (s *ServerContainer) AlertmanagersHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	return callAPIAndReturnToolResult(ctx, s.alertmanagersAPICall, "failed making alertmanagers api call: ")
}

// FlagsHandler handles the flags tool.
func (s *ServerContainer) FlagsHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	return callAPIAndReturnToolResult(ctx, s.flagsAPICall, "failed making flags api call: ")
}

// ListAlertsHandler handles the list alerts tool.
func (s *ServerContainer) ListAlertsHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	return callAPIAndReturnToolResult(ctx, s.listAlertsAPICall, "failed making list alerts api call: ")
}

// TsdbStatsHandler handles the TSDB stats tool.
func (s *ServerContainer) TsdbStatsHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	return callAPIAndReturnToolResult(ctx, s.tsdbStatsAPICall, "failed making TSDB stats api call: ")
}

// BuildInfoHandler handles the build info tool.
func (s *ServerContainer) BuildInfoHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	return callAPIAndReturnToolResult(ctx, s.buildinfoAPICall, "failed making build info api call: ")
}

// ConfigHandler handles the config tool.
func (s *ServerContainer) ConfigHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	return callAPIAndReturnToolResult(ctx, s.configAPICall, "failed making config api call: ")
}

// RuntimeInfoHandler handles the runtime info tool.
func (s *ServerContainer) RuntimeInfoHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	return callAPIAndReturnToolResult(ctx, s.runtimeinfoAPICall, "failed making runtime info api call: ")
}

// ListRulesHandler handles the list rules tool.
func (s *ServerContainer) ListRulesHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	return callAPIAndReturnToolResult(ctx, s.rulesAPICall, "failed making rules api call: ")
}

// ListTargetsHandler handles the list targets tool.
func (s *ServerContainer) ListTargetsHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	return callAPIAndReturnToolResult(ctx, s.targetsAPICall, "failed making targets api call: ")
}

// WALReplayHandler handles the WAL replay status tool.
func (s *ServerContainer) WALReplayHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	return callAPIAndReturnToolResult(ctx, s.walReplayAPICall, "failed making WAL replay api call: ")
}

// Prometheus TSDB Admin tool handlers

// CleanTombstonesHandler handles the clean tombstones admin tool.
func (s *ServerContainer) CleanTombstonesHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	if !s.tsdbAdminToolsEnabled {
		return newToolErrorResult("failed making clean tombstones api call: " + errTSDBAdminToolsNotEnabled.Error()), nil, nil
	}

	logger := s.GetToolLogger(req, nil)

	logger.Warn("executing TSDB admin operation: clean tombstones")

	result, err := s.cleanTombstonesAPICall(ctx)
	if err != nil {
		return newToolErrorResult("failed making clean tombstones api call: " + err.Error()), nil, nil
	}

	logger.Warn("clean tombstones completed successfully")
	return newToolTextResult(result), nil, nil
}

// DeleteSeriesHandler handles the delete series admin tool.
func (s *ServerContainer) DeleteSeriesHandler(ctx context.Context, req *mcp.CallToolRequest, input DeleteSeriesInput) (*mcp.CallToolResult, any, error) {
	if !s.tsdbAdminToolsEnabled {
		return newToolErrorResult("failed making delete series api call: " + errTSDBAdminToolsNotEnabled.Error()), nil, nil
	}

	if len(input.Matches) == 0 {
		return newToolErrorResult("at least one matches parameter is required"), nil, nil
	}

	// Require explicit time bounds for destructive operations. Without them,
	// Prometheus defaults to deleting across the entire retention window.
	// This is almost certainly not intended behavior from the end user,
	// but if it is, make them ask for it.
	if input.StartTime == "" || input.EndTime == "" {
		return newToolErrorResult("both start_time and end_time are required for delete_series to prevent accidental deletion of all data; specify explicit time bounds"), nil, nil
	}

	logger := s.GetToolLogger(req, input)

	startTs, endTs, err := parseTimeRangeInputWithDefaults(input.TimeRangeInput, time.Time{}, time.Time{})
	if err != nil {
		return newToolErrorResult(err.Error()), nil, nil
	}

	logger.Warn("executing TSDB admin operation: delete series")

	result, err := s.deleteSeriesAPICall(ctx, input.Matches, startTs, endTs)
	if err != nil {
		return newToolErrorResult("failed making delete series api call: " + err.Error()), nil, nil
	}

	logger.Warn("delete series completed successfully")
	return newToolTextResult(result), nil, nil
}

// SnapshotHandler handles the snapshot admin tool.
func (s *ServerContainer) SnapshotHandler(ctx context.Context, req *mcp.CallToolRequest, input SnapshotInput) (*mcp.CallToolResult, any, error) {
	if !s.tsdbAdminToolsEnabled {
		return newToolErrorResult("failed making snapshot api call: " + errTSDBAdminToolsNotEnabled.Error()), nil, nil
	}

	logger := s.GetToolLogger(req, input)

	logger.Warn("executing TSDB admin operation: snapshot")

	result, err := s.snapshotAPICall(ctx, input.SkipHead)
	if err != nil {
		return newToolErrorResult("failed making snapshot api call: " + err.Error()), nil, nil
	}

	logger.Warn("snapshot completed successfully")
	return newToolTextResult(result), nil, nil
}

// Management API handlers

// HealthyHandler handles the healthy check tool.
func (s *ServerContainer) HealthyHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	result, err := s.doManagementAPICall(ctx, http.MethodGet, mgmtAPIHealthyEndpoint)
	if err != nil {
		return newToolErrorResult("failed making healthy api call: " + err.Error()), nil, nil
	}
	return newToolTextResult(result), nil, nil
}

// ReadyHandler handles the ready check tool.
func (s *ServerContainer) ReadyHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	result, err := s.doManagementAPICall(ctx, http.MethodGet, mgmtAPIReadyEndpoint)
	if err != nil {
		return newToolErrorResult("failed making ready api call: " + err.Error()), nil, nil
	}
	return newToolTextResult(result), nil, nil
}

// ReloadHandler handles the reload config tool.
func (s *ServerContainer) ReloadHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	logger := s.GetToolLogger(req, nil)

	logger.Warn("triggering Prometheus configuration reload")

	result, err := s.doManagementAPICall(ctx, http.MethodPost, mgmtAPIReloadEndpoint)
	if err != nil {
		return newToolErrorResult("failed making reload api call: " + err.Error()), nil, nil
	}

	logger.Warn("reload completed successfully")
	return newToolTextResult(result), nil, nil
}

// QuitHandler handles the quit/shutdown tool.
func (s *ServerContainer) QuitHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	logger := s.GetToolLogger(req, nil)

	logger.Warn("triggering Prometheus shutdown")

	result, err := s.doManagementAPICall(ctx, http.MethodPost, mgmtAPIQuitEndpoint)
	if err != nil {
		return newToolErrorResult("failed making quit api call: " + err.Error()), nil, nil
	}

	logger.Warn("quit signal sent successfully - Prometheus is shutting down")
	return newToolTextResult(result), nil, nil
}

// Documentation tool handlers

// DocsListHandler handles the docs list tool.
func (s *ServerContainer) DocsListHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	resourceReq := &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{
			URI: resourcePrefix + "docs",
		},
	}

	resourceResult, err := s.DocsListResourceHandler(ctx, resourceReq)
	if err != nil {
		return newToolErrorResult("failed listing docs: " + err.Error()), nil, nil
	}

	return embedResourceContentsInToolResult(resourceResult, &mcp.CallToolResult{}), nil, nil
}

// DocsReadHandler handles the docs read tool.
func (s *ServerContainer) DocsReadHandler(ctx context.Context, req *mcp.CallToolRequest, input DocsReadInput) (*mcp.CallToolResult, any, error) {
	if input.File == "" {
		return newToolErrorResult("file parameter is required"), nil, nil
	}

	// Construct the resource URI with proper URL encoding
	uri := resourcePrefix + "docs/" + url.PathEscape(input.File)
	resourceReq := &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{
			URI: uri,
		},
	}

	resourceResult, err := s.DocsReadResourceHandler(ctx, resourceReq)
	if err != nil {
		return newToolErrorResult("failed reading doc file: " + err.Error()), nil, nil
	}

	return embedResourceContentsInToolResult(resourceResult, &mcp.CallToolResult{}), nil, nil
}

// DocsSearchHandler handles the docs search tool.
func (s *ServerContainer) DocsSearchHandler(ctx context.Context, req *mcp.CallToolRequest, input DocsSearchInput) (*mcp.CallToolResult, any, error) {
	logger := s.GetToolLogger(req, input)

	if input.Query == "" {
		return newToolErrorResult("query parameter is required"), nil, nil
	}

	matchingChunkIDs, err := s.SearchDocs(input.Query, input.Limit)
	if err != nil {
		return newToolErrorResult("failed searching docs: " + err.Error()), nil, nil
	}

	if len(matchingChunkIDs) == 0 {
		return newToolTextResult("No documentation found matching query: " + input.Query), nil, nil
	}

	// Extract unique file names from chunk IDs.
	matchingDocsFiles := []string{}
	docsFilesSeen := make(map[string]struct{})
	for _, chunkID := range matchingChunkIDs {
		parts := strings.Split(chunkID, "#")
		if len(parts) != 2 {
			// Valid chunk format is `filename#chunkID`.
			logger.Warn("skipping malformed chunk ID", "chunk_id", chunkID)
			continue
		}
		name := parts[0]
		if _, seen := docsFilesSeen[name]; !seen {
			docsFilesSeen[name] = struct{}{}
			matchingDocsFiles = append(matchingDocsFiles, name)
		}
	}

	logger.Debug("docs search completed", "matching_files", len(matchingDocsFiles))

	// Read all matching files via the resource handler and combine results.
	resourceResults := make([]*mcp.ReadResourceResult, 0, len(matchingDocsFiles))
	for _, file := range matchingDocsFiles {
		uri := resourcePrefix + "docs/" + url.PathEscape(file)
		// Allocate a fresh request per iteration to avoid shared-struct mutation
		// across concurrent or sequential calls to DocsReadResourceHandler.
		resourceReq := &mcp.ReadResourceRequest{Params: &mcp.ReadResourceParams{URI: uri}}

		resourceResult, err := s.DocsReadResourceHandler(ctx, resourceReq)
		if err != nil {
			return newToolErrorResult("failed reading doc file: " + err.Error()), nil, nil
		}
		resourceResults = append(resourceResults, resourceResult)
	}

	searchSummary := fmt.Sprintf("Found %d documentation files matching query %q: %q", len(matchingDocsFiles), input.Query, matchingDocsFiles)

	combinedSearchContents := concatResourceContents(resourceResults...)
	content := make([]mcp.Content, 0, len(combinedSearchContents)+1)
	content = append(content, &mcp.TextContent{Text: searchSummary})
	for _, rc := range combinedSearchContents {
		content = append(content, &mcp.EmbeddedResource{Resource: rc})
	}

	return &mcp.CallToolResult{Content: content}, nil, nil
}

// Runbook tool handlers

// RunbooksListHandler handles the runbooks list tool.
func (s *ServerContainer) RunbooksListHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	return newToolTextResult(listRunbooks()), nil, nil
}

// RunbooksReadHandler handles the runbooks read tool. It serves the runbook
// through its skill:// resource so tool results carry the canonical URI.
func (s *ServerContainer) RunbooksReadHandler(ctx context.Context, req *mcp.CallToolRequest, input RunbooksReadInput) (*mcp.CallToolResult, any, error) {
	if input.Name == "" {
		return newToolErrorResult("name parameter is required"), nil, nil
	}

	// Validate against the registry before building a URI from the input.
	if _, ok := getRunbookDef(input.Name); !ok {
		return newToolErrorResult(fmt.Sprintf("unknown runbook %q; use runbooks_list to see available runbooks", input.Name)), nil, nil
	}

	resourceReq := &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{
			URI: skillURI(input.Name),
		},
	}

	resourceResult, err := s.SkillReadResourceHandler(ctx, resourceReq)
	if err != nil {
		return newToolErrorResult("failed reading runbook: " + err.Error()), nil, nil
	}

	return embedResourceContentsInToolResult(resourceResult, &mcp.CallToolResult{}), nil, nil
}

// Thanos-specific handlers

// ThanosStoresHandler handles the Thanos list stores tool.
func (s *ServerContainer) ThanosStoresHandler(ctx context.Context, req *mcp.CallToolRequest, input EmptyInput) (*mcp.CallToolResult, any, error) {
	_, rt := s.GetAPIClient(ctx)

	ctx, cancel := context.WithTimeout(ctx, s.apiTimeout)
	defer cancel()

	path := "/api/v1/stores"
	result, err := s.doHTTPRequest(ctx, http.MethodGet, rt, path, true)
	if err != nil {
		return newToolErrorResult("failed getting stores from Thanos: " + err.Error()), nil, nil
	}
	return newToolTextResult(result), nil, nil
}

// Prometheus API call methods on ServerContainer

// formatTruncatedQueryAPIResponse applies line-based truncation to a result string, adds
// a warning if truncated, wraps it in a queryAPIResponse with optional
// warnings, and formats the output.
func (s *ServerContainer) formatTruncatedQueryAPIResponse(resultString string, warnings promv1.Warnings, truncationLimit int) (string, error) {
	truncatedResult, truncated := truncateStringByLines(resultString, truncationLimit)
	if truncated {
		resultString = truncatedResult + displayTruncationWarning(truncationLimit)
	}
	return s.FormatOutput(queryAPIResponse{
		Result:   resultString,
		Warnings: warnings,
	})
}

func (s *ServerContainer) queryAPICall(ctx context.Context, query string, ts time.Time, truncationLimit int) (string, error) {
	client, _ := s.GetAPIClient(ctx)
	ctx, cancel := context.WithTimeout(ctx, s.apiTimeout)
	defer cancel()

	path := "/api/v1/query"
	startTs := time.Now()
	result, warnings, err := client.Query(ctx, query, ts)
	metricAPICallDuration.With(prometheus.Labels{"target_path": path}).Observe(time.Since(startTs).Seconds())
	if err != nil {
		metricAPICallsFailed.With(prometheus.Labels{"target_path": path}).Inc()
		return "", fmt.Errorf("failed to execute instant query: %w", wrapErrorIfNotFound(err, path))
	}

	return s.formatTruncatedQueryAPIResponse(result.String(), warnings, truncationLimit)
}

func (s *ServerContainer) rangeQueryAPICall(ctx context.Context, query string, start, end time.Time, step time.Duration, truncationLimit int) (string, error) {
	client, _ := s.GetAPIClient(ctx)
	ctx, cancel := context.WithTimeout(ctx, s.apiTimeout)
	defer cancel()

	path := "/api/v1/query_range"
	startTs := time.Now()
	result, warnings, err := client.QueryRange(ctx, query, promv1.Range{Start: start, End: end, Step: step})
	metricAPICallDuration.With(prometheus.Labels{"target_path": path}).Observe(time.Since(startTs).Seconds())
	if err != nil {
		metricAPICallsFailed.With(prometheus.Labels{"target_path": path}).Inc()
		return "", fmt.Errorf("failed to execute range query: %w", wrapErrorIfNotFound(err, path))
	}

	return s.formatTruncatedQueryAPIResponse(result.String(), warnings, truncationLimit)
}

func (s *ServerContainer) exemplarQueryAPICall(ctx context.Context, query string, start, end time.Time, truncationLimit int) (string, error) {
	client, _ := s.GetAPIClient(ctx)
	ctx, cancel := context.WithTimeout(ctx, s.apiTimeout)
	defer cancel()

	path := "/api/v1/query_exemplars"
	startTs := time.Now()
	res, err := client.QueryExemplars(ctx, query, start, end)
	metricAPICallDuration.With(prometheus.Labels{"target_path": path}).Observe(time.Since(startTs).Seconds())
	if err != nil {
		metricAPICallsFailed.With(prometheus.Labels{"target_path": path}).Inc()
		return "", fmt.Errorf("failed to execute exemplar query: %w", wrapErrorIfNotFound(err, path))
	}

	var resultSB strings.Builder
	for _, r := range res {
		b, err := json.Marshal(r)
		if err != nil {
			return "", fmt.Errorf("failed to marshal exemplar: %w", err)
		}
		resultSB.Write(b)
		resultSB.WriteString("\n")
	}
	return s.formatTruncatedQueryAPIResponse(resultSB.String(), nil, truncationLimit)
}

func (s *ServerContainer) seriesAPICall(ctx context.Context, matches []string, start, end time.Time, truncationLimit int) (string, error) {
	client, _ := s.GetAPIClient(ctx)
	ctx, cancel := context.WithTimeout(ctx, s.apiTimeout)
	defer cancel()

	path := "/api/v1/series"
	startTs := time.Now()
	result, warnings, err := client.Series(ctx, matches, start, end)
	metricAPICallDuration.With(prometheus.Labels{"target_path": path}).Observe(time.Since(startTs).Seconds())
	if err != nil {
		metricAPICallsFailed.With(prometheus.Labels{"target_path": path}).Inc()
		return "", fmt.Errorf("failed to get series: %w", wrapErrorIfNotFound(err, path))
	}

	lsets := make([]string, len(result))
	for i, lset := range result {
		lsets[i] = lset.String()
	}

	return s.formatTruncatedQueryAPIResponse(strings.Join(lsets, "\n"), warnings, truncationLimit)
}

func (s *ServerContainer) labelNamesAPICall(ctx context.Context, matches []string, start, end time.Time, truncationLimit int) (string, error) {
	client, _ := s.GetAPIClient(ctx)
	ctx, cancel := context.WithTimeout(ctx, s.apiTimeout)
	defer cancel()

	path := "/api/v1/labels"
	startTs := time.Now()
	result, warnings, err := client.LabelNames(ctx, matches, start, end)
	metricAPICallDuration.With(prometheus.Labels{"target_path": path}).Observe(time.Since(startTs).Seconds())
	if err != nil {
		metricAPICallsFailed.With(prometheus.Labels{"target_path": path}).Inc()
		return "", fmt.Errorf("failed to get label names: %w", wrapErrorIfNotFound(err, path))
	}

	names := make([]string, len(result))
	for i, name := range result {
		names[i] = string(name)
	}

	return s.formatTruncatedQueryAPIResponse(strings.Join(names, "\n"), warnings, truncationLimit)
}

func (s *ServerContainer) labelValuesAPICall(ctx context.Context, label string, matches []string, start, end time.Time, truncationLimit int) (string, error) {
	client, _ := s.GetAPIClient(ctx)
	ctx, cancel := context.WithTimeout(ctx, s.apiTimeout)
	defer cancel()

	path := "/api/v1/label/:name/values"
	startTs := time.Now()
	result, warnings, err := client.LabelValues(ctx, label, matches, start, end)
	metricAPICallDuration.With(prometheus.Labels{"target_path": path}).Observe(time.Since(startTs).Seconds())
	if err != nil {
		metricAPICallsFailed.With(prometheus.Labels{"target_path": path}).Inc()
		return "", fmt.Errorf("failed to get label values: %w", wrapErrorIfNotFound(err, path))
	}

	lvals := make([]string, len(result))
	for i, lval := range result {
		lvals[i] = string(lval)
	}

	return s.formatTruncatedQueryAPIResponse(strings.Join(lvals, "\n"), warnings, truncationLimit)
}

func (s *ServerContainer) metricMetadataAPICall(ctx context.Context, metric, limit string) (string, error) {
	client, _ := s.GetAPIClient(ctx)
	ctx, cancel := context.WithTimeout(ctx, s.apiTimeout)
	defer cancel()

	path := "/api/v1/metadata"
	startTs := time.Now()

	truncationLimit := s.truncationLimit
	if limit == "" && truncationLimit != 0 {
		limit = strconv.Itoa(truncationLimit)
	}

	limitInt := 0
	if limit != "" {
		n, err := strconv.Atoi(limit)
		if err != nil {
			return "", fmt.Errorf("failed to convert limit to int: %w", err)
		}
		if n < 0 {
			// In the `truncation_limit` field of other tools, `-1`
			// means unlimited. If we're given a negative number
			// here, normalize it to 0 for the prom API call to
			// treat it as unlimited.
			n = 0
		}
		limitInt = n
	}

	if limitInt == 0 {
		limit = ""
	}

	mm, err := client.Metadata(ctx, metric, limit)
	metricAPICallDuration.With(prometheus.Labels{"target_path": path}).Observe(time.Since(startTs).Seconds())
	if err != nil {
		metricAPICallsFailed.With(prometheus.Labels{"target_path": path}).Inc()
		return "", fmt.Errorf("failed to get metric metadata from Prometheus: %w", wrapErrorIfNotFound(err, path))
	}

	encodedData, err := s.FormatOutput(mm)
	if err != nil {
		return "", fmt.Errorf("failed to encode metric metadata: %w", err)
	}

	if limitInt != 0 {
		encodedData += displayTruncationWarning(limitInt)
	}

	return encodedData, nil
}

func (s *ServerContainer) targetsMetadataAPICall(ctx context.Context, matchTarget, metric, limit string) (string, error) {
	client, _ := s.GetAPIClient(ctx)
	ctx, cancel := context.WithTimeout(ctx, s.apiTimeout)
	defer cancel()

	path := "/api/v1/targets/metadata"
	startTs := time.Now()

	truncationLimit := s.truncationLimit
	if limit == "" && truncationLimit != 0 {
		limit = strconv.Itoa(truncationLimit)
	}

	limitInt := 0
	if limit != "" {
		n, err := strconv.Atoi(limit)
		if err != nil {
			return "", fmt.Errorf("failed to convert limit to int: %w", err)
		}
		if n < 0 {
			// In the `truncation_limit` field of other tools, `-1`
			// means unlimited. If we're given a negative number
			// here, normalize it to 0 for the prom API call to
			// treat it as unlimited.
			n = 0
		}
		limitInt = n
	}

	if limitInt == 0 {
		limit = ""
	}

	tm, err := client.TargetsMetadata(ctx, matchTarget, metric, limit)
	metricAPICallDuration.With(prometheus.Labels{"target_path": path}).Observe(time.Since(startTs).Seconds())
	if err != nil {
		metricAPICallsFailed.With(prometheus.Labels{"target_path": path}).Inc()
		return "", fmt.Errorf("failed to get target metadata from Prometheus: %w", wrapErrorIfNotFound(err, path))
	}

	encodedData, err := s.FormatOutput(tm)
	if err != nil {
		return "", fmt.Errorf("failed to encode target metadata: %w", err)
	}

	if limitInt != 0 {
		encodedData += displayTruncationWarning(limitInt)
	}

	return encodedData, nil
}

// doSimpleAPICall encapsulates the common pattern for Prometheus API calls that
// take no parameters beyond context: get client, set timeout, record metrics,
// call the API, and format the result.
func (s *ServerContainer) doSimpleAPICall(ctx context.Context, path, errMsg string, call func(context.Context, promv1.API) (any, error)) (string, error) {
	client, _ := s.GetAPIClient(ctx)
	ctx, cancel := context.WithTimeout(ctx, s.apiTimeout)
	defer cancel()

	startTs := time.Now()
	result, err := call(ctx, client)
	metricAPICallDuration.With(prometheus.Labels{"target_path": path}).Observe(time.Since(startTs).Seconds())
	if err != nil {
		metricAPICallsFailed.With(prometheus.Labels{"target_path": path}).Inc()
		return "", fmt.Errorf("%s: %w", errMsg, wrapErrorIfNotFound(err, path))
	}

	return s.FormatOutput(result)
}

func (s *ServerContainer) alertmanagersAPICall(ctx context.Context) (string, error) {
	return s.doSimpleAPICall(ctx, "/api/v1/alertmanagers", "failed to get alertmanager status from Prometheus",
		func(ctx context.Context, client promv1.API) (any, error) {
			return client.AlertManagers(ctx)
		})
}

func (s *ServerContainer) flagsAPICall(ctx context.Context) (string, error) {
	return s.doSimpleAPICall(ctx, "/api/v1/status/flags", "failed to get runtime flags from Prometheus",
		func(ctx context.Context, client promv1.API) (any, error) {
			return client.Flags(ctx)
		})
}

func (s *ServerContainer) listAlertsAPICall(ctx context.Context) (string, error) {
	return s.doSimpleAPICall(ctx, "/api/v1/alerts", "failed to get alerts from Prometheus",
		func(ctx context.Context, client promv1.API) (any, error) {
			return client.Alerts(ctx)
		})
}

func (s *ServerContainer) tsdbStatsAPICall(ctx context.Context) (string, error) {
	return s.doSimpleAPICall(ctx, "/api/v1/status/tsdb", "failed to get tsdb stats from Prometheus",
		func(ctx context.Context, client promv1.API) (any, error) {
			return client.TSDB(ctx)
		})
}

func (s *ServerContainer) buildinfoAPICall(ctx context.Context) (string, error) {
	return s.doSimpleAPICall(ctx, "/api/v1/status/buildinfo", "failed to get build info from Prometheus",
		func(ctx context.Context, client promv1.API) (any, error) {
			return client.Buildinfo(ctx)
		})
}

func (s *ServerContainer) configAPICall(ctx context.Context) (string, error) {
	return s.doSimpleAPICall(ctx, "/api/v1/status/config", "failed to get configuration from Prometheus",
		func(ctx context.Context, client promv1.API) (any, error) {
			return client.Config(ctx)
		})
}

func (s *ServerContainer) runtimeinfoAPICall(ctx context.Context) (string, error) {
	return s.doSimpleAPICall(ctx, "/api/v1/status/runtimeinfo", "failed to get runtime info from Prometheus",
		func(ctx context.Context, client promv1.API) (any, error) {
			return client.Runtimeinfo(ctx)
		})
}

func (s *ServerContainer) rulesAPICall(ctx context.Context) (string, error) {
	return s.doSimpleAPICall(ctx, "/api/v1/rules", "failed to get rules from Prometheus",
		func(ctx context.Context, client promv1.API) (any, error) {
			return client.Rules(ctx, nil)
		})
}

func (s *ServerContainer) targetsAPICall(ctx context.Context) (string, error) {
	return s.doSimpleAPICall(ctx, "/api/v1/targets", "failed to get targets from Prometheus",
		func(ctx context.Context, client promv1.API) (any, error) {
			return client.Targets(ctx)
		})
}

func (s *ServerContainer) walReplayAPICall(ctx context.Context) (string, error) {
	return s.doSimpleAPICall(ctx, "/api/v1/status/walreplay", "failed to get WAL replay status from Prometheus",
		func(ctx context.Context, client promv1.API) (any, error) {
			return client.WalReplay(ctx)
		})
}

func (s *ServerContainer) cleanTombstonesAPICall(ctx context.Context) (string, error) {
	return s.doSimpleAPICall(ctx, "/api/v1/admin/tsdb/clean_tombstones", "failed to clean tombstones from Prometheus",
		func(ctx context.Context, client promv1.API) (any, error) {
			return "success", client.CleanTombstones(ctx)
		})
}

func (s *ServerContainer) deleteSeriesAPICall(ctx context.Context, matches []string, start, end time.Time) (string, error) {
	client, _ := s.GetAPIClient(ctx)
	ctx, cancel := context.WithTimeout(ctx, s.apiTimeout)
	defer cancel()

	path := "/api/v1/admin/tsdb/delete_series"
	startTs := time.Now()
	err := client.DeleteSeries(ctx, matches, start, end)
	metricAPICallDuration.With(prometheus.Labels{"target_path": path}).Observe(time.Since(startTs).Seconds())
	if err != nil {
		metricAPICallsFailed.With(prometheus.Labels{"target_path": path}).Inc()
		return "", fmt.Errorf("failed to delete series from Prometheus: %w", wrapErrorIfNotFound(err, path))
	}

	return "success", nil
}

func (s *ServerContainer) snapshotAPICall(ctx context.Context, skipHead bool) (string, error) {
	client, _ := s.GetAPIClient(ctx)
	ctx, cancel := context.WithTimeout(ctx, s.apiTimeout)
	defer cancel()

	path := "/api/v1/admin/tsdb/snapshot"
	startTs := time.Now()
	ss, err := client.Snapshot(ctx, skipHead)
	metricAPICallDuration.With(prometheus.Labels{"target_path": path}).Observe(time.Since(startTs).Seconds())
	if err != nil {
		metricAPICallsFailed.With(prometheus.Labels{"target_path": path}).Inc()
		return "", fmt.Errorf("failed to create Prometheus snapshot: %w", wrapErrorIfNotFound(err, path))
	}

	return s.FormatOutput(ss)
}

func (s *ServerContainer) doManagementAPICall(ctx context.Context, method, path string) (string, error) {
	_, rt := s.GetAPIClient(ctx)
	ctx, cancel := context.WithTimeout(ctx, s.apiTimeout)
	defer cancel()

	data, err := s.doHTTPRequest(ctx, method, rt, path, false)
	if err != nil {
		return "", fmt.Errorf("failed to make Prometheus Management API call to %s: %w", path, err)
	}

	return strings.Trim(data, "\n\""), nil
}

// doHTTPRequest makes an HTTP request using the provided round tripper.
func (s *ServerContainer) doHTTPRequest(ctx context.Context, method string, rt http.RoundTripper, requestPath string, expectJSON bool) (string, error) {
	fullPath, err := url.JoinPath(s.prometheusURL, requestPath)
	if err != nil {
		return "", fmt.Errorf("failed to construct URL for request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullPath, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	// Reuse the cached client for the default transport to share its idle
	// connection pool. For auth-overridden transports create a one-off client.
	var httpClient *http.Client
	if s.defaultHTTPClient.Transport != nil && rt == s.defaultHTTPClient.Transport {
		httpClient = &s.defaultHTTPClient
	} else {
		httpClient = &http.Client{Transport: rt}
	}

	startTs := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make HTTP request: %w", err)
	}
	defer resp.Body.Close()
	metricAPICallDuration.With(prometheus.Labels{"target_path": requestPath}).Observe(time.Since(startTs).Seconds())

	if resp.StatusCode != http.StatusOK {
		metricAPICallsFailed.With(prometheus.Labels{"target_path": requestPath}).Inc()
		if resp.StatusCode == http.StatusNotFound {
			return "", &ErrEndpointNotSupported{
				Endpoint:   requestPath,
				StatusCode: resp.StatusCode,
			}
		}
		return "", fmt.Errorf("received non-ok HTTP status code: %d", resp.StatusCode)
	}

	// TODO(@tjhop): add an io.LimitReader and enforce max response body
	// size? Should it be user configurable (flag)?
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	var data any
	if expectJSON {
		err = json.Unmarshal(body, &data)
		if err != nil {
			return "", fmt.Errorf("failed to unmarshal JSON response: %w", err)
		}
	} else {
		data = string(body)
	}

	return s.FormatOutput(data)
}
