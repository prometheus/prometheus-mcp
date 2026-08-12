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
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus-mcp/pkg/mcp/mcptest"
)

// newTestContainer creates a ServerContainer with sensible defaults for testing.
// Tests can modify the returned container's fields directly as needed.
//
// Example usage:
//
//	container := newTestContainer(mockAPI)
//	container.truncationLimit = 100           // Set truncation
//	container.tsdbAdminToolsEnabled = true    // Enable admin tools
//	container.defaultRT = mockRT              // Set custom RoundTripper
func newTestContainer(mockAPI *MockPrometheusAPI) *ServerContainer {
	if mockAPI == nil {
		mockAPI = &MockPrometheusAPI{}
	}
	return &ServerContainer{
		logger:           slog.Default(),
		defaultAPIClient: mockAPI,
		prometheusURL:    "http://localhost:9090",
		defaultRT:        http.DefaultTransport,
		apiTimeout:       30 * time.Second,
		// All other fields default to zero values:
		// truncationLimit:       0  (no truncation)
		// toonOutputEnabled:     false
		// tsdbAdminToolsEnabled: false
		// docsFS:                nil
		// docsSearchIndex:       nil
		// clientLoggingEnabled:  false
	}
}

// mockRoundTripper is a mock implementation of http.RoundTripper for testing management API calls.
type mockRoundTripper struct {
	// RoundTripFunc allows customizing the response for each request.
	RoundTripFunc func(req *http.Request) (*http.Response, error)
}

// RoundTrip implements http.RoundTripper.
func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if m.RoundTripFunc != nil {
		return m.RoundTripFunc(req)
	}
	return nil, errors.New("RoundTripFunc not set")
}

// newMockHTTPResponse creates a mock HTTP response with the given status code and body.
func newMockHTTPResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestQueryHandler(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		args           map[string]any
		globalLimit    int
		mockQueryFunc  func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error)
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{
				"query":     "vector(1)",
				"timestamp": "1756143048",
			},
			mockQueryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				require.Equal(t, "vector(1)", query)
				return model.Vector{&model.Sample{
					Metric:    model.Metric{},
					Value:     model.SampleValue(1),
					Timestamp: model.TimeFromUnix(ts.Unix()),
				}}, nil, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.JSONEq(t, `{"result":"{} => 1 @[1756143048]","warnings":null}`, result)
			},
		},
		{
			name: "missing query",
			args: map[string]any{},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				// SDK validates required parameters before handler is called,
				// returning validation errors as tool results (IsError=true), not Go errors.
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "query")
			},
		},
		{
			name: "empty query",
			args: map[string]any{"query": ""},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "query parameter is required")
			},
		},
		{
			name: "API error",
			args: map[string]any{"query": "up"},
			mockQueryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				return nil, nil, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
		{
			name: "invalid timestamp",
			args: map[string]any{"query": "up", "timestamp": "not-a-real-timestamp"},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "failed to parse timestamp")
			},
		},
		{
			name: "truncation - truncated output with warning",
			args: map[string]any{
				"query":            "vector(1)",
				"timestamp":        "1756143048",
				"truncation_limit": 1,
			},
			mockQueryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				require.Equal(t, "vector(1)", query)
				return model.Vector{
					&model.Sample{
						Metric:    model.Metric{},
						Value:     model.SampleValue(1),
						Timestamp: model.TimeFromUnix(ts.Unix()),
					},
					&model.Sample{
						Metric:    model.Metric{},
						Value:     model.SampleValue(2),
						Timestamp: model.TimeFromUnix(ts.Unix()),
					},
				}, nil, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				// truncateStringByLines includes the trailing newline of the last line,
				// so the truncated line ends with \n before the warning separator.
				expectedWarning := strings.ReplaceAll(displayTruncationWarning(1), "\n", "\\n")
				expectedResult := fmt.Sprintf(`{"result":"{} => 1 @[1756143048]\n%s","warnings":null}`, expectedWarning)
				require.JSONEq(t, expectedResult, result)
			},
		},
		{
			name: "truncation - not truncated, no warning",
			args: map[string]any{
				"query":            "vector(1)",
				"timestamp":        "1756143048",
				"truncation_limit": 0,
			},
			mockQueryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				require.Equal(t, "vector(1)", query)
				return model.Vector{&model.Sample{
					Metric:    model.Metric{},
					Value:     model.SampleValue(1),
					Timestamp: model.TimeFromUnix(ts.Unix()),
				}}, nil, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.JSONEq(t, `{"result":"{} => 1 @[1756143048]","warnings":null}`, result)
			},
		},
		{
			name: "truncation - disabled with -1",
			args: map[string]any{
				"query":            "vector(1)",
				"timestamp":        "1756143048",
				"truncation_limit": -1,
			},
			globalLimit: 1,
			mockQueryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				return model.Vector{
					&model.Sample{
						Metric:    model.Metric{},
						Value:     model.SampleValue(1),
						Timestamp: model.TimeFromUnix(ts.Unix()),
					},
					&model.Sample{
						Metric:    model.Metric{},
						Value:     model.SampleValue(2),
						Timestamp: model.TimeFromUnix(ts.Unix()),
					},
				}, nil, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.JSONEq(t, `{"result":"{} => 1 @[1756143048]\n{} => 2 @[1756143048]","warnings":null}`, result)
			},
		},
		{
			name: "truncation - fallback to global limit with 0",
			args: map[string]any{
				"query":            "vector(1)",
				"timestamp":        "1756143048",
				"truncation_limit": 0,
			},
			globalLimit: 1,
			mockQueryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				return model.Vector{
					&model.Sample{
						Metric:    model.Metric{},
						Value:     model.SampleValue(1),
						Timestamp: model.TimeFromUnix(ts.Unix()),
					},
					&model.Sample{
						Metric:    model.Metric{},
						Value:     model.SampleValue(2),
						Timestamp: model.TimeFromUnix(ts.Unix()),
					},
				}, nil, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				// truncateStringByLines includes the trailing newline of the last line,
				// so the truncated line ends with \n before the warning separator.
				expectedWarning := strings.ReplaceAll(displayTruncationWarning(1), "\n", "\\n")
				expectedResult := fmt.Sprintf(`{"result":"{} => 1 @[1756143048]\n%s","warnings":null}`, expectedWarning)
				require.JSONEq(t, expectedResult, result)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{QueryFunc: tc.mockQueryFunc}
			container := newTestContainer(mockAPI)
			container.truncationLimit = tc.globalLimit

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, queryToolDef, container.QueryHandler)

			result, err := ts.CallTool(ts.Context(), "query", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestRangeQueryHandler(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		args           map[string]any
		mockQueryFunc  func(ctx context.Context, query string, r promv1.Range, opts ...promv1.Option) (model.Value, promv1.Warnings, error)
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{
				"query":      "vector(1)",
				"start_time": "1756143048",
				"end_time":   "1756143148",
				"step":       "15s",
			},
			mockQueryFunc: func(ctx context.Context, query string, r promv1.Range, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				require.Equal(t, "vector(1)", query)
				return model.Matrix{
					&model.SampleStream{
						Metric: model.Metric{},
						Values: []model.SamplePair{
							{Timestamp: model.TimeFromUnix(1756143048), Value: 1},
						},
					},
				}, nil, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "1756143048")
			},
		},
		{
			name: "missing query",
			args: map[string]any{},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				// SDK validates required parameters before handler is called,
				// returning validation errors as tool results (IsError=true), not Go errors.
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "query")
			},
		},
		{
			name: "empty query",
			args: map[string]any{"query": ""},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "query parameter is required")
			},
		},
		{
			name: "API error",
			args: map[string]any{"query": "up"},
			mockQueryFunc: func(ctx context.Context, query string, r promv1.Range, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				return nil, nil, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
		{
			name: "invalid start_time",
			args: map[string]any{"query": "up", "start_time": "not-a-real-timestamp"},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "failed to parse start_time")
			},
		},
		{
			name: "invalid end_time",
			args: map[string]any{"query": "up", "end_time": "not-a-real-timestamp"},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "failed to parse end_time")
			},
		},
		{
			name: "invalid step",
			args: map[string]any{"query": "up", "step": "not-a-real-duration"},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "failed to parse step")
			},
		},
		{
			name: "zero step is rejected",
			args: map[string]any{"query": "up", "step": "0s"},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "step must be a positive duration")
			},
		},
		{
			name: "negative step is rejected",
			args: map[string]any{"query": "up", "step": "-5m"},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "failed to parse step")
			},
		},
		{
			name: "auto-step calculation from user-provided time range",
			args: map[string]any{
				"query":      "up",
				"start_time": "1756140000",
				"end_time":   "1756143600",
			},
			mockQueryFunc: func(ctx context.Context, query string, r promv1.Range, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				require.Equal(t, "up", query)
				// Auto-step should be floor(3600/250) = 14 seconds.
				require.Equal(t, 14*time.Second, r.Step, "auto-calculated step should be 14s for a 1-hour range")
				return model.Matrix{
					&model.SampleStream{
						Metric: model.Metric{},
						Values: []model.SamplePair{
							{Timestamp: model.TimeFromUnix(1756140000), Value: 1},
						},
					},
				}, nil, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "1756140000")
			},
		},
		{
			name: "step with days unit (1d)",
			args: map[string]any{
				"query":      "up",
				"start_time": "1756140000",
				"end_time":   "1756743600",
				"step":       "1d",
			},
			mockQueryFunc: func(ctx context.Context, query string, r promv1.Range, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				require.Equal(t, 24*time.Hour, r.Step, "1d step should equal 24 hours")
				return model.Matrix{
					&model.SampleStream{
						Metric: model.Metric{},
						Values: []model.SamplePair{
							{Timestamp: model.TimeFromUnix(1756140000), Value: 1},
						},
					},
				}, nil, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "1756140000")
			},
		},
		{
			name: "step with years unit (1y)",
			args: map[string]any{
				"query":      "up",
				"start_time": "1724600000",
				"end_time":   "1756143600",
				"step":       "1y",
			},
			mockQueryFunc: func(ctx context.Context, query string, r promv1.Range, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				// model.Duration defines 1y = 365 days.
				expectedStep := 365 * 24 * time.Hour
				require.Equal(t, expectedStep, r.Step, "1y step should equal 365.25 days")
				return model.Matrix{
					&model.SampleStream{
						Metric: model.Metric{},
						Values: []model.SamplePair{
							{Timestamp: model.TimeFromUnix(1724600000), Value: 1},
						},
					},
				}, nil, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "1724600000")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{QueryRangeFunc: tc.mockQueryFunc}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, rangeQueryToolDef, container.RangeQueryHandler)

			result, err := ts.CallTool(ts.Context(), "range_query", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestSeriesHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		args           map[string]any
		mockSeriesFunc func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) ([]model.LabelSet, promv1.Warnings, error)
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{
				"matches": []string{"up"},
			},
			mockSeriesFunc: func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) ([]model.LabelSet, promv1.Warnings, error) {
				return []model.LabelSet{
					{"__name__": "up", "job": "prometheus"},
				}, nil, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "up")
				require.Contains(t, result, "prometheus")
			},
		},
		{
			name: "missing matches",
			args: map[string]any{},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				// SDK validates required parameters before handler is called,
				// returning validation errors as tool results (IsError=true), not Go errors.
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "matches")
			},
		},
		{
			name: "empty matches",
			args: map[string]any{"matches": []string{}},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "at least one matches parameter is required")
			},
		},
		{
			name: "API error",
			args: map[string]any{"matches": []string{"up"}},
			mockSeriesFunc: func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) ([]model.LabelSet, promv1.Warnings, error) {
				return nil, nil, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
		{
			name: "invalid start_time",
			args: map[string]any{"matches": []string{"up"}, "start_time": "not-a-real-timestamp"},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "failed to parse start_time")
			},
		},
		{
			name: "invalid end_time",
			args: map[string]any{"matches": []string{"up"}, "end_time": "not-a-real-timestamp"},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "failed to parse end_time")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{SeriesFunc: tc.mockSeriesFunc}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, seriesToolDef, container.SeriesHandler)

			result, err := ts.CallTool(ts.Context(), "series", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestLabelValuesHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                string
		args                map[string]any
		mockLabelValuesFunc func(ctx context.Context, label string, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) (model.LabelValues, promv1.Warnings, error)
		validateResult      func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{
				"label": "job",
			},
			mockLabelValuesFunc: func(ctx context.Context, label string, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) (model.LabelValues, promv1.Warnings, error) {
				require.Equal(t, "job", label)
				return model.LabelValues{"prometheus", "node"}, nil, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "prometheus")
				require.Contains(t, result, "node")
			},
		},
		{
			name: "missing label",
			args: map[string]any{},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				// SDK validates required parameters before handler is called,
				// returning validation errors as tool results (IsError=true), not Go errors.
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "label")
			},
		},
		{
			name: "empty label",
			args: map[string]any{"label": ""},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "label parameter is required")
			},
		},
		{
			name: "API error",
			args: map[string]any{"label": "job"},
			mockLabelValuesFunc: func(ctx context.Context, label string, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) (model.LabelValues, promv1.Warnings, error) {
				return nil, nil, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
		{
			name: "invalid start_time",
			args: map[string]any{"label": "job", "start_time": "not-a-real-timestamp"},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "failed to parse start_time")
			},
		},
		{
			name: "invalid end_time",
			args: map[string]any{"label": "job", "end_time": "not-a-real-timestamp"},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "failed to parse end_time")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{LabelValuesFunc: tc.mockLabelValuesFunc}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, labelValuesToolDef, container.LabelValuesHandler)

			result, err := ts.CallTool(ts.Context(), "label_values", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestLabelNamesHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name               string
		args               map[string]any
		mockLabelNamesFunc func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) (model.LabelNames, promv1.Warnings, error)
		validateResult     func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{},
			mockLabelNamesFunc: func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) (model.LabelNames, promv1.Warnings, error) {
				return model.LabelNames{"__name__", "job", "instance"}, nil, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "__name__")
				require.Contains(t, result, "job")
			},
		},
		{
			name: "API error",
			args: map[string]any{},
			mockLabelNamesFunc: func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) (model.LabelNames, promv1.Warnings, error) {
				return nil, nil, errors.New("api error")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "api error")
			},
		},
		{
			name: "invalid start_time",
			args: map[string]any{"start_time": "not-a-real-timestamp"},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "failed to parse start_time")
			},
		},
		{
			name: "invalid end_time",
			args: map[string]any{"end_time": "not-a-real-timestamp"},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "failed to parse end_time")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{LabelNamesFunc: tc.mockLabelNamesFunc}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, labelNamesToolDef, container.LabelNamesHandler)

			result, err := ts.CallTool(ts.Context(), "label_names", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestExemplarQueryHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                   string
		args                   map[string]any
		mockQueryExemplarsFunc func(ctx context.Context, query string, startTime time.Time, endTime time.Time) ([]promv1.ExemplarQueryResult, error)
		validateResult         func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{
				"query":      "http_requests_total",
				"start_time": "1756143048",
				"end_time":   "1756143148",
			},
			mockQueryExemplarsFunc: func(ctx context.Context, query string, startTime time.Time, endTime time.Time) ([]promv1.ExemplarQueryResult, error) {
				require.Equal(t, "http_requests_total", query)
				return []promv1.ExemplarQueryResult{
					{
						SeriesLabels: model.LabelSet{"__name__": "http_requests_total"},
						Exemplars: []promv1.Exemplar{
							{
								Labels:    model.LabelSet{"trace_id": "abc123"},
								Value:     1.0,
								Timestamp: model.TimeFromUnix(1756143048),
							},
						},
					},
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "http_requests_total")
			},
		},
		{
			name: "missing query",
			args: map[string]any{},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				// SDK validates required parameters before handler is called,
				// returning validation errors as tool results (IsError=true), not Go errors.
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "query")
			},
		},
		{
			name: "empty query",
			args: map[string]any{"query": ""},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "query parameter is required")
			},
		},
		{
			name: "API error",
			args: map[string]any{"query": "up"},
			mockQueryExemplarsFunc: func(ctx context.Context, query string, startTime time.Time, endTime time.Time) ([]promv1.ExemplarQueryResult, error) {
				return nil, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
		{
			name: "invalid start_time",
			args: map[string]any{"query": "up", "start_time": "not-a-real-timestamp"},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "failed to parse start_time")
			},
		},
		{
			name: "invalid end_time",
			args: map[string]any{"query": "up", "end_time": "not-a-real-timestamp"},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "failed to parse end_time")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{QueryExemplarsFunc: tc.mockQueryExemplarsFunc}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, exemplarQueryToolDef, container.ExemplarQueryHandler)

			result, err := ts.CallTool(ts.Context(), "exemplar_query", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestMetricMetadataHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name             string
		args             map[string]any
		globalLimit      int
		mockMetadataFunc func(ctx context.Context, metric string, limit string) (map[string][]promv1.Metadata, error)
		validateResult   func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success with params",
			args: map[string]any{
				"metric": "http_requests_total",
				"limit":  "10",
			},
			mockMetadataFunc: func(ctx context.Context, metric string, limit string) (map[string][]promv1.Metadata, error) {
				require.Equal(t, "http_requests_total", metric)
				return map[string][]promv1.Metadata{
					"http_requests_total": {
						{
							Type: "counter",
							Help: "Total number of HTTP requests",
							Unit: "",
						},
					},
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "http_requests_total")
				require.Contains(t, result, "counter")
			},
		},
		{
			name: "success no params",
			args: map[string]any{},
			mockMetadataFunc: func(ctx context.Context, metric string, limit string) (map[string][]promv1.Metadata, error) {
				return map[string][]promv1.Metadata{
					"up": {
						{
							Type: "gauge",
							Help: "Target status",
							Unit: "",
						},
					},
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "up")
			},
		},
		{
			name: "API error",
			args: map[string]any{},
			mockMetadataFunc: func(ctx context.Context, metric string, limit string) (map[string][]promv1.Metadata, error) {
				return nil, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
		{
			name: "specific numeric limit passed to API",
			args: map[string]any{
				"metric": "up",
				"limit":  "5",
			},
			mockMetadataFunc: func(ctx context.Context, metric string, limit string) (map[string][]promv1.Metadata, error) {
				require.Equal(t, "5", limit, "explicit limit should be passed directly to the API")
				return map[string][]promv1.Metadata{
					"up": {{Type: "gauge", Help: "Target status", Unit: ""}},
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "up")
				// When a non-zero limit is used, a truncation warning is appended.
				require.Contains(t, result, "truncated")
			},
		},
		{
			name:        "empty limit falls back to global truncation limit",
			args:        map[string]any{},
			globalLimit: 42,
			mockMetadataFunc: func(ctx context.Context, metric string, limit string) (map[string][]promv1.Metadata, error) {
				require.Equal(t, "42", limit, "empty limit should fall back to the global truncation limit")
				return map[string][]promv1.Metadata{
					"up": {{Type: "gauge", Help: "Target status", Unit: ""}},
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "up")
				// The global truncation limit triggers a truncation warning.
				require.Contains(t, result, "truncated")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{MetadataFunc: tc.mockMetadataFunc}
			container := newTestContainer(mockAPI)
			container.truncationLimit = tc.globalLimit

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, metricMetadataToolDef, container.MetricMetadataHandler)

			result, err := ts.CallTool(ts.Context(), "metric_metadata", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestTargetsMetadataHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                    string
		args                    map[string]any
		mockTargetsMetadataFunc func(ctx context.Context, matchTarget string, metric string, limit string) ([]promv1.MetricMetadata, error)
		validateResult          func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success with params",
			args: map[string]any{
				"match_target": "{job=\"prometheus\"}",
				"metric":       "http_requests_total",
				"limit":        "10",
			},
			mockTargetsMetadataFunc: func(ctx context.Context, matchTarget string, metric string, limit string) ([]promv1.MetricMetadata, error) {
				require.Equal(t, "{job=\"prometheus\"}", matchTarget)
				require.Equal(t, "http_requests_total", metric)
				return []promv1.MetricMetadata{
					{
						Target: map[string]string{"job": "prometheus"},
						Metric: "http_requests_total",
						Type:   "counter",
						Help:   "Total number of HTTP requests",
						Unit:   "",
					},
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "http_requests_total")
				require.Contains(t, result, "prometheus")
			},
		},
		{
			name: "success no params",
			args: map[string]any{},
			mockTargetsMetadataFunc: func(ctx context.Context, matchTarget string, metric string, limit string) ([]promv1.MetricMetadata, error) {
				return []promv1.MetricMetadata{
					{
						Target: map[string]string{"job": "node"},
						Metric: "node_cpu_seconds_total",
						Type:   "counter",
						Help:   "Seconds the CPUs spent in each mode",
						Unit:   "",
					},
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "node_cpu_seconds_total")
			},
		},
		{
			name: "API error",
			args: map[string]any{},
			mockTargetsMetadataFunc: func(ctx context.Context, matchTarget string, metric string, limit string) ([]promv1.MetricMetadata, error) {
				return nil, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{TargetsMetadataFunc: tc.mockTargetsMetadataFunc}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, targetsMetadataToolDef, container.TargetsMetadataHandler)

			result, err := ts.CallTool(ts.Context(), "targets_metadata", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestListTargetsHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name            string
		args            map[string]any
		mockTargetsFunc func(ctx context.Context) (promv1.TargetsResult, error)
		validateResult  func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{},
			mockTargetsFunc: func(ctx context.Context) (promv1.TargetsResult, error) {
				return promv1.TargetsResult{
					Active: []promv1.ActiveTarget{
						{
							DiscoveredLabels: map[string]string{"__address__": "localhost:9090"},
							Labels:           model.LabelSet{"job": "prometheus"},
							ScrapePool:       "prometheus",
							ScrapeURL:        "http://localhost:9090/metrics",
							Health:           promv1.HealthGood,
						},
					},
					Dropped: []promv1.DroppedTarget{},
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "prometheus")
				require.Contains(t, result, "localhost:9090")
			},
		},
		{
			name: "API error",
			args: map[string]any{},
			mockTargetsFunc: func(ctx context.Context) (promv1.TargetsResult, error) {
				return promv1.TargetsResult{}, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{TargetsFunc: tc.mockTargetsFunc}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, listTargetsToolDef, container.ListTargetsHandler)

			result, err := ts.CallTool(ts.Context(), "list_targets", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestListRulesHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		args           map[string]any
		mockRulesFunc  func(ctx context.Context, matches []string) (promv1.RulesResult, error)
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{},
			mockRulesFunc: func(ctx context.Context, matches []string) (promv1.RulesResult, error) {
				return promv1.RulesResult{
					Groups: []promv1.RuleGroup{
						{
							Name: "example",
							File: "/etc/prometheus/rules.yml",
							Rules: []any{
								promv1.RecordingRule{
									Name:   "job:http_requests:rate5m",
									Query:  "sum(rate(http_requests_total[5m])) by (job)",
									Health: promv1.RuleHealthGood,
								},
							},
						},
					},
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "example")
			},
		},
		{
			name: "API error",
			args: map[string]any{},
			mockRulesFunc: func(ctx context.Context, matches []string) (promv1.RulesResult, error) {
				return promv1.RulesResult{}, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{RulesFunc: tc.mockRulesFunc}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, listRulesToolDef, container.ListRulesHandler)

			result, err := ts.CallTool(ts.Context(), "list_rules", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestRuntimeInfoHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                string
		args                map[string]any
		mockRuntimeinfoFunc func(ctx context.Context) (promv1.RuntimeinfoResult, error)
		validateResult      func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{},
			mockRuntimeinfoFunc: func(ctx context.Context) (promv1.RuntimeinfoResult, error) {
				return promv1.RuntimeinfoResult{
					StartTime:           time.Now(),
					CWD:                 "/prometheus",
					ReloadConfigSuccess: true,
					GoroutineCount:      50,
					GOMAXPROCS:          4,
					GOGC:                "100",
					GODEBUG:             "",
					StorageRetention:    "15d",
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "/prometheus")
				require.Contains(t, result, "15d")
			},
		},
		{
			name: "API error",
			args: map[string]any{},
			mockRuntimeinfoFunc: func(ctx context.Context) (promv1.RuntimeinfoResult, error) {
				return promv1.RuntimeinfoResult{}, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{RuntimeinfoFunc: tc.mockRuntimeinfoFunc}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, runtimeInfoToolDef, container.RuntimeInfoHandler)

			result, err := ts.CallTool(ts.Context(), "runtime_info", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestConfigHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		args           map[string]any
		mockConfigFunc func(ctx context.Context) (promv1.ConfigResult, error)
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{},
			mockConfigFunc: func(ctx context.Context) (promv1.ConfigResult, error) {
				return promv1.ConfigResult{
					YAML: "global:\n  scrape_interval: 15s\n",
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "scrape_interval")
			},
		},
		{
			name: "API error",
			args: map[string]any{},
			mockConfigFunc: func(ctx context.Context) (promv1.ConfigResult, error) {
				return promv1.ConfigResult{}, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{ConfigFunc: tc.mockConfigFunc}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, configToolDef, container.ConfigHandler)

			result, err := ts.CallTool(ts.Context(), "config", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestBuildInfoHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name              string
		args              map[string]any
		mockBuildinfoFunc func(ctx context.Context) (promv1.BuildinfoResult, error)
		validateResult    func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{},
			mockBuildinfoFunc: func(ctx context.Context) (promv1.BuildinfoResult, error) {
				return promv1.BuildinfoResult{
					Version:   "2.45.0",
					Revision:  "abc123",
					Branch:    "HEAD",
					BuildUser: "root@localhost",
					BuildDate: "20231001",
					GoVersion: "go1.21.0",
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "2.45.0")
				require.Contains(t, result, "go1.21.0")
			},
		},
		{
			name: "API error",
			args: map[string]any{},
			mockBuildinfoFunc: func(ctx context.Context) (promv1.BuildinfoResult, error) {
				return promv1.BuildinfoResult{}, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{BuildinfoFunc: tc.mockBuildinfoFunc}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, buildInfoToolDef, container.BuildInfoHandler)

			result, err := ts.CallTool(ts.Context(), "build_info", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestFlagsHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		args           map[string]any
		mockFlagsFunc  func(ctx context.Context) (promv1.FlagsResult, error)
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{},
			mockFlagsFunc: func(ctx context.Context) (promv1.FlagsResult, error) {
				return promv1.FlagsResult{
					"storage.tsdb.path":      "/prometheus",
					"storage.tsdb.retention": "15d",
					"web.listen-address":     "0.0.0.0:9090",
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "storage.tsdb.path")
				require.Contains(t, result, "/prometheus")
			},
		},
		{
			name: "API error",
			args: map[string]any{},
			mockFlagsFunc: func(ctx context.Context) (promv1.FlagsResult, error) {
				return promv1.FlagsResult{}, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{FlagsFunc: tc.mockFlagsFunc}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, flagsToolDef, container.FlagsHandler)

			result, err := ts.CallTool(ts.Context(), "flags", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestListAlertsHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		args           map[string]any
		mockAlertsFunc func(ctx context.Context) (promv1.AlertsResult, error)
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{},
			mockAlertsFunc: func(ctx context.Context) (promv1.AlertsResult, error) {
				return promv1.AlertsResult{
					Alerts: []promv1.Alert{
						{
							Labels:      model.LabelSet{"alertname": "HighMemoryUsage", "severity": "warning"},
							Annotations: model.LabelSet{"summary": "Memory usage is high"},
							State:       promv1.AlertStateFiring,
						},
					},
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "HighMemoryUsage")
				require.Contains(t, result, "warning")
			},
		},
		{
			name: "API error",
			args: map[string]any{},
			mockAlertsFunc: func(ctx context.Context) (promv1.AlertsResult, error) {
				return promv1.AlertsResult{}, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{AlertsFunc: tc.mockAlertsFunc}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, listAlertsToolDef, container.ListAlertsHandler)

			result, err := ts.CallTool(ts.Context(), "list_alerts", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestAlertmanagersHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                  string
		args                  map[string]any
		mockAlertManagersFunc func(ctx context.Context) (promv1.AlertManagersResult, error)
		validateResult        func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{},
			mockAlertManagersFunc: func(ctx context.Context) (promv1.AlertManagersResult, error) {
				return promv1.AlertManagersResult{
					Active: []promv1.AlertManager{
						{
							URL: "http://localhost:9093",
						},
					},
					Dropped: []promv1.AlertManager{},
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "localhost:9093")
			},
		},
		{
			name: "API error",
			args: map[string]any{},
			mockAlertManagersFunc: func(ctx context.Context) (promv1.AlertManagersResult, error) {
				return promv1.AlertManagersResult{}, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{AlertManagersFunc: tc.mockAlertManagersFunc}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, alertmanagersToolDef, container.AlertmanagersHandler)

			result, err := ts.CallTool(ts.Context(), "alertmanagers", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestTsdbStatsHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		args           map[string]any
		mockTSDBFunc   func(ctx context.Context, opts ...promv1.Option) (promv1.TSDBResult, error)
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{},
			mockTSDBFunc: func(ctx context.Context, opts ...promv1.Option) (promv1.TSDBResult, error) {
				return promv1.TSDBResult{
					HeadStats: promv1.TSDBHeadStats{
						NumSeries:     1000,
						NumLabelPairs: 500,
						ChunkCount:    2000,
						MinTime:       1609459200000,
						MaxTime:       1609545600000,
					},
					SeriesCountByMetricName: []promv1.Stat{
						{Name: "http_requests_total", Value: 100},
					},
					LabelValueCountByLabelName: []promv1.Stat{
						{Name: "job", Value: 10},
					},
					MemoryInBytesByLabelName: []promv1.Stat{
						{Name: "__name__", Value: 1024},
					},
					SeriesCountByLabelValuePair: []promv1.Stat{
						{Name: "job=prometheus", Value: 50},
					},
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "http_requests_total")
				require.Contains(t, result, "1000")
			},
		},
		{
			name: "API error",
			args: map[string]any{},
			mockTSDBFunc: func(ctx context.Context, opts ...promv1.Option) (promv1.TSDBResult, error) {
				return promv1.TSDBResult{}, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
		{
			name: "404 endpoint not supported",
			args: map[string]any{},
			mockTSDBFunc: func(ctx context.Context, opts ...promv1.Option) (promv1.TSDBResult, error) {
				return promv1.TSDBResult{}, &promv1.Error{
					Type: promv1.ErrClient,
					Msg:  "client error: 404",
				}
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "may not be supported by your version of Prometheus")
				require.Contains(t, result, "/api/v1/status/tsdb")
				require.Contains(t, result, "build_info")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{TSDBFunc: tc.mockTSDBFunc}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, tsdbStatsToolDef, container.TsdbStatsHandler)

			result, err := ts.CallTool(ts.Context(), "tsdb_stats", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestWALReplayHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name              string
		args              map[string]any
		mockWALReplayFunc func(ctx context.Context) (promv1.WalReplayStatus, error)
		validateResult    func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{},
			mockWALReplayFunc: func(ctx context.Context) (promv1.WalReplayStatus, error) {
				return promv1.WalReplayStatus{
					Min:     1,
					Max:     100,
					Current: 50,
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "50")
			},
		},
		{
			name: "API error",
			args: map[string]any{},
			mockWALReplayFunc: func(ctx context.Context) (promv1.WalReplayStatus, error) {
				return promv1.WalReplayStatus{}, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{WALReplayFunc: tc.mockWALReplayFunc}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, walReplayToolDef, container.WALReplayHandler)

			result, err := ts.CallTool(ts.Context(), "wal_replay_status", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

// TSDB Admin Handler Tests

func TestCleanTombstonesHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                    string
		args                    map[string]any
		adminToolsEnabled       bool
		mockCleanTombstonesFunc func(ctx context.Context) error
		validateResult          func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name:              "success",
			args:              map[string]any{},
			adminToolsEnabled: true,
			mockCleanTombstonesFunc: func(ctx context.Context) error {
				return nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "success")
			},
		},
		{
			name:              "admin tools not enabled",
			args:              map[string]any{},
			adminToolsEnabled: false,
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "TSDB admin tools must be enabled")
			},
		},
		{
			name:              "API error",
			args:              map[string]any{},
			adminToolsEnabled: true,
			mockCleanTombstonesFunc: func(ctx context.Context) error {
				return errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{CleanTombstonesFunc: tc.mockCleanTombstonesFunc}
			container := newTestContainer(mockAPI)
			container.tsdbAdminToolsEnabled = tc.adminToolsEnabled

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, cleanTombstonesToolDef, container.CleanTombstonesHandler)

			result, err := ts.CallTool(ts.Context(), "clean_tombstones", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestDeleteSeriesHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                 string
		args                 map[string]any
		adminToolsEnabled    bool
		mockDeleteSeriesFunc func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time) error
		validateResult       func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success with time range",
			args: map[string]any{
				"matches":    []string{"http_requests_total"},
				"start_time": "1756143048",
				"end_time":   "1756143148",
			},
			adminToolsEnabled: true,
			mockDeleteSeriesFunc: func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time) error {
				require.Equal(t, []string{"http_requests_total"}, matches)
				require.False(t, startTime.IsZero())
				require.False(t, endTime.IsZero())
				return nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "success")
			},
		},
		{
			name: "admin tools not enabled",
			args: map[string]any{
				"matches": []string{"up"},
			},
			adminToolsEnabled: false,
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "TSDB admin tools must be enabled")
			},
		},
		{
			name:              "missing matches",
			args:              map[string]any{},
			adminToolsEnabled: true,
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				// SDK validates required parameters before handler is called,
				// returning validation errors as tool results (IsError=true), not Go errors.
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "matches")
			},
		},
		{
			name:              "empty matches",
			args:              map[string]any{"matches": []string{}},
			adminToolsEnabled: true,
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "at least one matches parameter is required")
			},
		},
		{
			name: "invalid start_time",
			args: map[string]any{
				"matches":    []string{"up"},
				"start_time": "not-a-real-timestamp",
				"end_time":   "1756143148",
			},
			adminToolsEnabled: true,
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "failed to parse start_time")
			},
		},
		{
			name: "invalid end_time",
			args: map[string]any{
				"matches":    []string{"up"},
				"start_time": "1756143048",
				"end_time":   "not-a-real-timestamp",
			},
			adminToolsEnabled: true,
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "failed to parse end_time")
			},
		},
		{
			name: "missing time range",
			args: map[string]any{
				"matches": []string{"up"},
			},
			adminToolsEnabled: true,
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "start_time and end_time are required")
			},
		},
		{
			name: "missing start_time only",
			args: map[string]any{
				"matches":  []string{"up"},
				"end_time": "1756143148",
			},
			adminToolsEnabled: true,
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "start_time and end_time are required")
			},
		},
		{
			name: "missing end_time only",
			args: map[string]any{
				"matches":    []string{"up"},
				"start_time": "1756143048",
			},
			adminToolsEnabled: true,
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "start_time and end_time are required")
			},
		},
		{
			name: "API error",
			args: map[string]any{
				"matches":    []string{"up"},
				"start_time": "1756143048",
				"end_time":   "1756143148",
			},
			adminToolsEnabled: true,
			mockDeleteSeriesFunc: func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time) error {
				return errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{DeleteSeriesFunc: tc.mockDeleteSeriesFunc}
			container := newTestContainer(mockAPI)
			container.tsdbAdminToolsEnabled = tc.adminToolsEnabled

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, deleteSeriesToolDef, container.DeleteSeriesHandler)

			result, err := ts.CallTool(ts.Context(), "delete_series", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestSnapshotHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name              string
		args              map[string]any
		adminToolsEnabled bool
		mockSnapshotFunc  func(ctx context.Context, skipHead bool) (promv1.SnapshotResult, error)
		validateResult    func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name:              "success without skip_head",
			args:              map[string]any{},
			adminToolsEnabled: true,
			mockSnapshotFunc: func(ctx context.Context, skipHead bool) (promv1.SnapshotResult, error) {
				require.False(t, skipHead)
				return promv1.SnapshotResult{
					Name: "20231001T120000Z-abc123",
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "20231001T120000Z-abc123")
			},
		},
		{
			name: "success with skip_head true",
			args: map[string]any{
				"skip_head": true,
			},
			adminToolsEnabled: true,
			mockSnapshotFunc: func(ctx context.Context, skipHead bool) (promv1.SnapshotResult, error) {
				require.True(t, skipHead)
				return promv1.SnapshotResult{
					Name: "20231001T130000Z-def456",
				}, nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "20231001T130000Z-def456")
			},
		},
		{
			name:              "admin tools not enabled",
			args:              map[string]any{},
			adminToolsEnabled: false,
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "TSDB admin tools must be enabled")
			},
		},
		{
			name:              "API error",
			args:              map[string]any{},
			adminToolsEnabled: true,
			mockSnapshotFunc: func(ctx context.Context, skipHead bool) (promv1.SnapshotResult, error) {
				return promv1.SnapshotResult{}, errors.New("prometheus exploded")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "prometheus exploded")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{SnapshotFunc: tc.mockSnapshotFunc}
			container := newTestContainer(mockAPI)
			container.tsdbAdminToolsEnabled = tc.adminToolsEnabled

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, snapshotToolDef, container.SnapshotHandler)

			result, err := ts.CallTool(ts.Context(), "snapshot", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

// Management API Handler Tests

func TestHealthyHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		args           map[string]any
		mockRTFunc     func(req *http.Request) (*http.Response, error)
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{},
			mockRTFunc: func(req *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, req.Method)
				require.Contains(t, req.URL.Path, "/-/healthy")
				return newMockHTTPResponse(http.StatusOK, "Prometheus Server is Healthy.\n"), nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "Prometheus Server is Healthy")
			},
		},
		{
			name: "server unhealthy",
			args: map[string]any{},
			mockRTFunc: func(req *http.Request) (*http.Response, error) {
				return newMockHTTPResponse(http.StatusServiceUnavailable, "Service Unavailable"), nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "non-ok HTTP status code: 503")
			},
		},
		{
			name: "network error",
			args: map[string]any{},
			mockRTFunc: func(req *http.Request) (*http.Response, error) {
				return nil, errors.New("connection refused")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "connection refused")
			},
		},
		{
			name: "404 endpoint not supported",
			args: map[string]any{},
			mockRTFunc: func(req *http.Request) (*http.Response, error) {
				return newMockHTTPResponse(http.StatusNotFound, "Not Found"), nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "may not be supported by your version of Prometheus")
				require.Contains(t, result, "/-/healthy")
				require.Contains(t, result, "build_info")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{}
			mockRT := &mockRoundTripper{RoundTripFunc: tc.mockRTFunc}
			container := newTestContainer(mockAPI)
			container.defaultRT = mockRT

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, healthyToolDef, container.HealthyHandler)

			result, err := ts.CallTool(ts.Context(), "healthy", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestReadyHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		args           map[string]any
		mockRTFunc     func(req *http.Request) (*http.Response, error)
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{},
			mockRTFunc: func(req *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, req.Method)
				require.Contains(t, req.URL.Path, "/-/ready")
				return newMockHTTPResponse(http.StatusOK, "Prometheus Server is Ready.\n"), nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "Prometheus Server is Ready")
			},
		},
		{
			name: "server not ready",
			args: map[string]any{},
			mockRTFunc: func(req *http.Request) (*http.Response, error) {
				return newMockHTTPResponse(http.StatusServiceUnavailable, "Service Unavailable"), nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "non-ok HTTP status code: 503")
			},
		},
		{
			name: "network error",
			args: map[string]any{},
			mockRTFunc: func(req *http.Request) (*http.Response, error) {
				return nil, errors.New("connection refused")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "connection refused")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{}
			mockRT := &mockRoundTripper{RoundTripFunc: tc.mockRTFunc}
			container := newTestContainer(mockAPI)
			container.defaultRT = mockRT

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, readyToolDef, container.ReadyHandler)

			result, err := ts.CallTool(ts.Context(), "ready", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestReloadHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		args           map[string]any
		mockRTFunc     func(req *http.Request) (*http.Response, error)
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{},
			mockRTFunc: func(req *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodPost, req.Method)
				require.Contains(t, req.URL.Path, "/-/reload")
				return newMockHTTPResponse(http.StatusOK, ""), nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
			},
		},
		{
			name: "reload disabled",
			args: map[string]any{},
			mockRTFunc: func(req *http.Request) (*http.Response, error) {
				return newMockHTTPResponse(http.StatusForbidden, "Lifecycle API is not enabled"), nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "non-ok HTTP status code: 403")
			},
		},
		{
			name: "network error",
			args: map[string]any{},
			mockRTFunc: func(req *http.Request) (*http.Response, error) {
				return nil, errors.New("connection refused")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "connection refused")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{}
			mockRT := &mockRoundTripper{RoundTripFunc: tc.mockRTFunc}
			container := newTestContainer(mockAPI)
			container.defaultRT = mockRT

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, reloadToolDef, container.ReloadHandler)

			result, err := ts.CallTool(ts.Context(), "reload", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestQuitHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		args           map[string]any
		mockRTFunc     func(req *http.Request) (*http.Response, error)
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{},
			mockRTFunc: func(req *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodPost, req.Method)
				require.Contains(t, req.URL.Path, "/-/quit")
				return newMockHTTPResponse(http.StatusOK, ""), nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
			},
		},
		{
			name: "quit disabled",
			args: map[string]any{},
			mockRTFunc: func(req *http.Request) (*http.Response, error) {
				return newMockHTTPResponse(http.StatusForbidden, "Lifecycle API is not enabled"), nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "non-ok HTTP status code: 403")
			},
		},
		{
			name: "network error",
			args: map[string]any{},
			mockRTFunc: func(req *http.Request) (*http.Response, error) {
				return nil, errors.New("connection refused")
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "connection refused")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{}
			mockRT := &mockRoundTripper{RoundTripFunc: tc.mockRTFunc}
			container := newTestContainer(mockAPI)
			container.defaultRT = mockRT

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, quitToolDef, container.QuitHandler)

			result, err := ts.CallTool(ts.Context(), "quit", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

// Documentation Handler Tests

// mockDocsFS creates an in-memory FS with test documentation files.
func mockDocsFS() fs.FS {
	return fstest.MapFS{
		"querying/basics.md": &fstest.MapFile{
			Data: []byte("---\ntitle: Querying Basics\n---\n# Querying Basics\n\nPromQL is the query language..."),
		},
		"querying/functions.md": &fstest.MapFile{
			Data: []byte("# PromQL Functions\n\n## rate()\n\nCalculates per-second average rate..."),
		},
		"alerting/overview.md": &fstest.MapFile{
			Data: []byte("# Alerting Overview\n\nAlertmanager handles alerts..."),
		},
	}
}

// newTestContainerWithDocs creates a test container with docs support.
// This is a convenience wrapper that uses newTestContainer and initializes docs search.
func newTestContainerWithDocs(mockAPI *MockPrometheusAPI, docsFS fs.FS) (*ServerContainer, error) {
	container := newTestContainer(mockAPI)

	if docsFS != nil {
		state, err := buildDocsState(slog.Default(), docsFS)
		if err != nil {
			return nil, err
		}
		container.swapDocsState(state)
	}

	return container, nil
}

func TestDocsListHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		docsFS         fs.FS
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name:   "success - lists all markdown files",
			docsFS: mockDocsFS(),
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "querying/basics.md")
				require.Contains(t, result, "querying/functions.md")
				require.Contains(t, result, "alerting/overview.md")
			},
		},
		{
			name:   "error - no docs filesystem",
			docsFS: nil,
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var container *ServerContainer
			var err error
			if tc.docsFS == nil {
				container = newTestContainer(&MockPrometheusAPI{})
			} else {
				container, err = newTestContainerWithDocs(&MockPrometheusAPI{}, tc.docsFS)
				require.NoError(t, err)
			}

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, docsListToolDef, container.DocsListHandler)
			ts.AddResourceTemplate(docsReadResourceTemplate, container.DocsReadResourceHandler)
			ts.AddResource(docsListResource, container.DocsListResourceHandler)

			result, err := ts.CallTool(ts.Context(), "docs_list", map[string]any{})

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestDocsReadHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		args           map[string]any
		docsFS         fs.FS
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name:   "success - reads file content",
			args:   map[string]any{"file": "querying/basics.md"},
			docsFS: mockDocsFS(),
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "Querying Basics")
				require.Contains(t, result, "PromQL is the query language")
				// Verify frontmatter was stripped
				require.NotContains(t, result, "title:")
			},
		},
		{
			name:   "missing file parameter",
			args:   map[string]any{},
			docsFS: mockDocsFS(),
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				// SDK validates required parameters before handler is called,
				// returning validation errors as tool results (IsError=true), not Go errors.
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "file")
			},
		},
		{
			name:   "empty file parameter",
			args:   map[string]any{"file": ""},
			docsFS: mockDocsFS(),
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "file parameter is required")
			},
		},
		{
			name:   "file not found",
			args:   map[string]any{"file": "nonexistent.md"},
			docsFS: mockDocsFS(),
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "failed reading doc file")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			container, err := newTestContainerWithDocs(&MockPrometheusAPI{}, tc.docsFS)
			require.NoError(t, err)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, docsReadToolDef, container.DocsReadHandler)
			ts.AddResourceTemplate(docsReadResourceTemplate, container.DocsReadResourceHandler)

			result, err := ts.CallTool(ts.Context(), "docs_read", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestDocsSearchHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		args           map[string]any
		docsFS         fs.FS
		skipDocsInit   bool
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name:   "success - finds matching docs",
			args:   map[string]any{"query": "PromQL"},
			docsFS: mockDocsFS(),
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "Found")
				require.Contains(t, result, "querying")
			},
		},
		{
			name:   "success with limit",
			args:   map[string]any{"query": "PromQL", "limit": 1},
			docsFS: mockDocsFS(),
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "Found")
			},
		},
		{
			name:   "no results found",
			args:   map[string]any{"query": "nonexistent-term-xyz123"},
			docsFS: mockDocsFS(),
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "No documentation found matching query")
			},
		},
		{
			name:   "empty query",
			args:   map[string]any{"query": ""},
			docsFS: mockDocsFS(),
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "query parameter is required")
			},
		},
		{
			name:         "nil search index returns error",
			args:         map[string]any{"query": "PromQL"},
			docsFS:       mockDocsFS(),
			skipDocsInit: true,
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "search index not initialized")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var container *ServerContainer
			if tc.skipDocsInit {
				// Create container with docsFS set but without initializing
				// the search index, simulating a failed index initialization.
				container = newTestContainer(&MockPrometheusAPI{})
				// Store state with fs but no search index.
				container.swapDocsState(&docsState{fs: tc.docsFS})
			} else {
				var err error
				container, err = newTestContainerWithDocs(&MockPrometheusAPI{}, tc.docsFS)
				require.NoError(t, err)
			}

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, docsSearchToolDef, container.DocsSearchHandler)
			ts.AddResourceTemplate(docsReadResourceTemplate, container.DocsReadResourceHandler)

			result, err := ts.CallTool(ts.Context(), "docs_search", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

// Thanos Handler Tests

func TestThanosStoresHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		args           map[string]any
		mockRTFunc     func(req *http.Request) (*http.Response, error)
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success",
			args: map[string]any{},
			mockRTFunc: func(req *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, req.Method)
				require.Contains(t, req.URL.Path, "/api/v1/stores")
				return newMockHTTPResponse(http.StatusOK, `{"status":"success","data":{"sidecar":[{"name":"sidecar-1"}]}}`), nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "sidecar-1")
			},
		},
		{
			name: "API error",
			args: map[string]any{},
			mockRTFunc: func(req *http.Request) (*http.Response, error) {
				return newMockHTTPResponse(http.StatusInternalServerError, "Internal Server Error"), nil
			},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "non-ok HTTP status code")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &MockPrometheusAPI{}
			mockRT := &mockRoundTripper{RoundTripFunc: tc.mockRTFunc}
			container := newTestContainer(mockAPI)
			container.defaultRT = mockRT

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, thanosStoresToolDef, container.ThanosStoresHandler)

			result, err := ts.CallTool(ts.Context(), "list_stores", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

// Infrastructure / Helper Tests

func TestGetEffectiveTruncationLimit(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name         string
		globalLimit  int
		perCallLimit int
		expected     int
	}{
		{
			name:         "per-call limit overrides global",
			globalLimit:  100,
			perCallLimit: 50,
			expected:     50,
		},
		{
			name:         "negative per-call disables truncation",
			globalLimit:  100,
			perCallLimit: -1,
			expected:     0,
		},
		{
			name:         "zero per-call uses global limit",
			globalLimit:  100,
			perCallLimit: 0,
			expected:     100,
		},
		{
			name:         "both zero means no truncation",
			globalLimit:  0,
			perCallLimit: 0,
			expected:     0,
		},
		{
			name:         "large per-call limit",
			globalLimit:  10,
			perCallLimit: 1000,
			expected:     1000,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			container := &ServerContainer{
				truncationLimit: tc.globalLimit,
			}

			result := container.GetEffectiveTruncationLimit(tc.perCallLimit)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestFormatOutput(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name        string
		toonEnabled bool
		data        any
		validate    func(t *testing.T, result string, err error)
	}{
		{
			name:        "JSON mode - simple map",
			toonEnabled: false,
			data:        map[string]string{"key": "value"},
			validate: func(t *testing.T, result string, err error) {
				require.NoError(t, err)
				require.JSONEq(t, `{"key":"value"}`, result)
			},
		},
		{
			name:        "JSON mode - complex struct",
			toonEnabled: false,
			data: promv1.AlertManagersResult{
				Active: []promv1.AlertManager{{URL: "http://am:9093"}},
			},
			validate: func(t *testing.T, result string, err error) {
				require.NoError(t, err)
				require.Contains(t, result, "http://am:9093")
			},
		},
		{
			name:        "TOON mode - simple map",
			toonEnabled: true,
			data:        map[string]string{"key": "value"},
			validate: func(t *testing.T, result string, err error) {
				require.NoError(t, err)
				require.NotEmpty(t, result)
				// Basic check that it's not JSON
				require.NotContains(t, result, `{"key":"value"}`)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			container := &ServerContainer{
				toonOutputEnabled: tc.toonEnabled,
			}

			result, err := container.FormatOutput(tc.data)
			tc.validate(t, result, err)
		})
	}
}

func TestGetAPIClient(t *testing.T) {
	t.Parallel()
	t.Run("returns default client when no auth in context", func(t *testing.T) {
		mockAPI := &MockPrometheusAPI{}
		container := &ServerContainer{
			logger:           slog.Default(),
			defaultAPIClient: mockAPI,
			defaultRT:        http.DefaultTransport,
			prometheusURL:    "http://localhost:9090",
		}

		ctx := context.Background()
		client, rt := container.GetAPIClient(ctx)

		require.Equal(t, mockAPI, client)
		require.Equal(t, http.DefaultTransport, rt)
	})

	t.Run("creates new client with Bearer auth from context", func(t *testing.T) {
		mockAPI := &MockPrometheusAPI{}
		container := &ServerContainer{
			logger:           slog.Default(),
			defaultAPIClient: mockAPI,
			defaultRT:        http.DefaultTransport,
			prometheusURL:    "http://localhost:9090",
		}

		ctx := addAuthToContext(context.Background(), "Bearer my-token")
		client, rt := container.GetAPIClient(ctx)

		// Should return a new client, not the default
		require.NotEqual(t, mockAPI, client)
		require.NotEqual(t, http.DefaultTransport, rt)
	})
}

func TestTruncateStringByLines(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		input          string
		limit          int
		expectedOutput string
		expectTrunc    bool
	}{
		{
			name:           "no truncation needed - under limit",
			input:          "line1\nline2\nline3",
			limit:          5,
			expectedOutput: "line1\nline2\nline3",
			expectTrunc:    false,
		},
		{
			name:           "truncation at limit",
			input:          "line1\nline2\nline3\nline4\nline5",
			limit:          2,
			expectedOutput: "line1\nline2\n",
			expectTrunc:    true,
		},
		{
			name:           "limit of 0 disables truncation",
			input:          "line1\nline2\nline3",
			limit:          0,
			expectedOutput: "line1\nline2\nline3",
			expectTrunc:    false,
		},
		{
			name:           "negative limit disables truncation",
			input:          "line1\nline2\nline3",
			limit:          -1,
			expectedOutput: "line1\nline2\nline3",
			expectTrunc:    false,
		},
		{
			name:           "single line",
			input:          "single line",
			limit:          1,
			expectedOutput: "single line",
			expectTrunc:    false,
		},
		{
			name:           "empty string",
			input:          "",
			limit:          10,
			expectedOutput: "",
			expectTrunc:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, truncated := truncateStringByLines(tc.input, tc.limit)
			require.Equal(t, tc.expectedOutput, result)
			require.Equal(t, tc.expectTrunc, truncated)
		})
	}
}

func TestDocsListResourceHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		docsFS         fs.FS
		validateResult func(t *testing.T, result string, err error)
	}{
		{
			name:   "success - lists all docs files",
			docsFS: mockDocsFS(),
			validateResult: func(t *testing.T, result string, err error) {
				require.NoError(t, err)
				require.Contains(t, result, "querying/basics.md")
				require.Contains(t, result, "querying/functions.md")
				require.Contains(t, result, "alerting/overview.md")
			},
		},
		{
			name:   "error - no docs filesystem",
			docsFS: nil,
			validateResult: func(t *testing.T, result string, err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "failed to list docs")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var container *ServerContainer
			var err error
			if tc.docsFS != nil {
				container, err = newTestContainerWithDocs(&MockPrometheusAPI{}, tc.docsFS)
				require.NoError(t, err)
			} else {
				container = newTestContainer(&MockPrometheusAPI{})
			}

			ts := mcptest.NewTestServer(t)
			ts.AddResource(docsListResource, container.DocsListResourceHandler)

			result, readErr := ts.ReadResource(ts.Context(), "prometheus://docs")

			resultText := mcptest.GetResourceText(result)
			tc.validateResult(t, resultText, readErr)
		})
	}
}

func TestDocsReadResourceHandler(t *testing.T) {
	t.Parallel()
	// TestDocsReadResourceHandler tests the docs read resource handler directly.
	// Note: We test the handler directly because the tool-based docs_read
	// tests already cover the same functionality through the MCP protocol.
	testCases := []struct {
		name           string
		uri            string
		docsFS         fs.FS
		validateResult func(t *testing.T, result *mcpsdk.ReadResourceResult, err error)
	}{
		{
			name:   "success - reads file content",
			uri:    "prometheus://docs/querying/basics.md",
			docsFS: mockDocsFS(),
			validateResult: func(t *testing.T, result *mcpsdk.ReadResourceResult, err error) {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, result.Contents, 1)
				require.Contains(t, result.Contents[0].Text, "Querying Basics")
				require.Contains(t, result.Contents[0].Text, "PromQL is the query language")
			},
		},
		{
			name:   "file not found",
			uri:    "prometheus://docs/nonexistent/file.md",
			docsFS: mockDocsFS(),
			validateResult: func(t *testing.T, result *mcpsdk.ReadResourceResult, err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "failed to read file from docs")
			},
		},
		{
			name:   "invalid scheme",
			uri:    "http://docs/querying/basics.md",
			docsFS: mockDocsFS(),
			validateResult: func(t *testing.T, result *mcpsdk.ReadResourceResult, err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "invalid docs resource URI scheme")
			},
		},
		{
			name:   "missing filename",
			uri:    "prometheus://docs/",
			docsFS: mockDocsFS(),
			validateResult: func(t *testing.T, result *mcpsdk.ReadResourceResult, err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "at least 1 filename is required")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			container, err := newTestContainerWithDocs(&MockPrometheusAPI{}, tc.docsFS)
			require.NoError(t, err)

			ctx := context.Background()
			req := &mcpsdk.ReadResourceRequest{
				Params: &mcpsdk.ReadResourceParams{
					URI: tc.uri,
				},
			}

			result, handlerErr := container.DocsReadResourceHandler(ctx, req)
			tc.validateResult(t, result, handlerErr)
		})
	}
}

// Runbook Handler Tests

func TestRunbooksListHandler(t *testing.T) {
	t.Parallel()

	container := newTestContainer(&MockPrometheusAPI{})

	ts := mcptest.NewTestServer(t)
	mcptest.AddTool(ts, runbooksListToolDef, container.RunbooksListHandler)

	result, err := ts.CallTool(ts.Context(), "runbooks_list", map[string]any{})
	require.NoError(t, err)
	require.False(t, result.IsError)

	resultText := mcptest.GetResultText(result)
	for _, def := range runbookDefs {
		require.Contains(t, resultText, def.Name)
		require.Contains(t, resultText, def.Description)
	}
}

func TestRunbooksReadHandler(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		args           map[string]any
		validateResult func(t *testing.T, result string, isError bool, err error)
	}{
		{
			name: "success - reads runbook content",
			args: map[string]any{"name": "check-system-health"},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.False(t, isError)
				require.Contains(t, result, "# Understanding System Health")
				require.Contains(t, result, "list_alerts")
			},
		},
		{
			name: "empty name parameter",
			args: map[string]any{"name": ""},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "name parameter is required")
			},
		},
		{
			name: "runbook not found",
			args: map[string]any{"name": "nonexistent-skill"},
			validateResult: func(t *testing.T, result string, isError bool, err error) {
				require.NoError(t, err)
				require.True(t, isError)
				require.Contains(t, result, "unknown runbook")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			container := newTestContainer(&MockPrometheusAPI{})

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, runbooksReadToolDef, container.RunbooksReadHandler)

			result, err := ts.CallTool(ts.Context(), "runbooks_read", tc.args)

			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError
			tc.validateResult(t, resultText, isError, err)
		})
	}
}

func TestSkillsIndexResourceHandler(t *testing.T) {
	t.Parallel()

	container := newTestContainer(&MockPrometheusAPI{})

	req := &mcpsdk.ReadResourceRequest{
		Params: &mcpsdk.ReadResourceParams{URI: skillsIndexURI},
	}
	result, err := container.SkillsIndexResourceHandler(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, result.Contents, 1)
	require.Equal(t, "application/json", result.Contents[0].MIMEType)

	var index skillsIndex
	require.NoError(t, json.Unmarshal([]byte(result.Contents[0].Text), &index))
	require.Equal(t, skillsIndexSchema, index.Schema)
	require.Len(t, index.Skills, len(runbookDefs))
	for i, def := range runbookDefs {
		require.Equal(t, def.Name, index.Skills[i].Name)
		require.Equal(t, "skill-md", index.Skills[i].Type)
		require.Equal(t, def.Description, index.Skills[i].Description)
		require.Equal(t, skillURI(def.Name), index.Skills[i].URL)
	}
}

func TestSkillReadResourceHandler(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		uri            string
		validateResult func(t *testing.T, result *mcpsdk.ReadResourceResult, err error)
	}{
		{
			name: "success - reads SKILL.md by skill URI",
			uri:  "skill://check-system-health/SKILL.md",
			validateResult: func(t *testing.T, result *mcpsdk.ReadResourceResult, err error) {
				require.NoError(t, err)
				require.Len(t, result.Contents, 1)
				require.Equal(t, "text/markdown", result.Contents[0].MIMEType)
				require.Contains(t, result.Contents[0].Text, "name: check-system-health")
				require.Contains(t, result.Contents[0].Text, "# Understanding System Health")
			},
		},
		{
			name: "invalid scheme is rejected",
			uri:  "prometheus://check-system-health/SKILL.md",
			validateResult: func(t *testing.T, result *mcpsdk.ReadResourceResult, err error) {
				require.ErrorContains(t, err, "invalid skill resource URI scheme")
			},
		},
		{
			name: "non-SKILL.md path is rejected",
			uri:  "skill://check-system-health/other.md",
			validateResult: func(t *testing.T, result *mcpsdk.ReadResourceResult, err error) {
				require.ErrorContains(t, err, "only SKILL.md files are served")
			},
		},
		{
			name: "unknown skill returns error",
			uri:  "skill://nonexistent-skill/SKILL.md",
			validateResult: func(t *testing.T, result *mcpsdk.ReadResourceResult, err error) {
				require.ErrorContains(t, err, "unknown runbook")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			container := newTestContainer(&MockPrometheusAPI{})

			req := &mcpsdk.ReadResourceRequest{
				Params: &mcpsdk.ReadResourceParams{URI: tc.uri},
			}
			result, err := container.SkillReadResourceHandler(context.Background(), req)
			tc.validateResult(t, result, err)
		})
	}
}

// TestQueryHandlerTimeFormats tests that the query handler correctly parses
// various timestamp formats that LLMs commonly use.
func TestQueryHandlerTimeFormats(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		timestamp     string
		expectError   bool
		errorContains string
		validateTime  func(t *testing.T, ts time.Time)
	}{
		{
			name:        "Unix timestamp seconds",
			timestamp:   "1756143048",
			expectError: false,
			validateTime: func(t *testing.T, ts time.Time) {
				// 1756143048 is a Unix timestamp.
				expected := time.Unix(1756143048, 0).UTC()
				require.Equal(t, expected.Unix(), ts.Unix())
			},
		},
		{
			name:        "Unix timestamp float with fractional seconds",
			timestamp:   "1756143048.123",
			expectError: false,
			validateTime: func(t *testing.T, ts time.Time) {
				// Should be approximately 1756143048 seconds + 123ms.
				require.Equal(t, int64(1756143048), ts.Unix())
				// Allow some tolerance for floating point rounding.
				require.InDelta(t, 123000000, ts.Nanosecond(), 1000000)
			},
		},
		{
			name:        "RFC3339 format",
			timestamp:   "2025-01-15T12:00:00Z",
			expectError: false,
			validateTime: func(t *testing.T, ts time.Time) {
				expected, _ := time.Parse(time.RFC3339, "2025-01-15T12:00:00Z")
				require.True(t, expected.Equal(ts))
			},
		},
		{
			name:        "RFC3339 with positive timezone offset",
			timestamp:   "2025-01-15T12:00:00+05:00",
			expectError: false,
			validateTime: func(t *testing.T, ts time.Time) {
				expected, _ := time.Parse(time.RFC3339, "2025-01-15T12:00:00+05:00")
				require.True(t, expected.Equal(ts))
			},
		},
		{
			name:        "RFC3339 with negative timezone offset",
			timestamp:   "2025-01-15T12:00:00-08:00",
			expectError: false,
			validateTime: func(t *testing.T, ts time.Time) {
				expected, _ := time.Parse(time.RFC3339, "2025-01-15T12:00:00-08:00")
				require.True(t, expected.Equal(ts))
			},
		},
		{
			name:        "RFC3339Nano with nanoseconds",
			timestamp:   "2025-01-15T12:00:00.123456789Z",
			expectError: false,
			validateTime: func(t *testing.T, ts time.Time) {
				expected, _ := time.Parse(time.RFC3339Nano, "2025-01-15T12:00:00.123456789Z")
				require.True(t, expected.Equal(ts))
			},
		},
		{
			name:        "Duration 5m (relative to now)",
			timestamp:   "5m",
			expectError: false,
			validateTime: func(t *testing.T, ts time.Time) {
				// Should be approximately 5 minutes ago.
				fiveMinutesAgo := time.Now().Add(-5 * time.Minute)
				require.InDelta(t, fiveMinutesAgo.Unix(), ts.Unix(), 2)
			},
		},
		{
			name:        "Duration 1h (relative to now)",
			timestamp:   "1h",
			expectError: false,
			validateTime: func(t *testing.T, ts time.Time) {
				oneHourAgo := time.Now().Add(-1 * time.Hour)
				require.InDelta(t, oneHourAgo.Unix(), ts.Unix(), 2)
			},
		},
		{
			name:        "Duration 30s (relative to now)",
			timestamp:   "30s",
			expectError: false,
			validateTime: func(t *testing.T, ts time.Time) {
				thirtySecondsAgo := time.Now().Add(-30 * time.Second)
				require.InDelta(t, thirtySecondsAgo.Unix(), ts.Unix(), 2)
			},
		},
		{
			name:        "Invalid - empty string",
			timestamp:   "",
			expectError: false, // Empty timestamp means "now", not an error.
			validateTime: func(t *testing.T, ts time.Time) {
				// When no timestamp is provided, it defaults to now.
				require.InDelta(t, time.Now().Unix(), ts.Unix(), 2)
			},
		},
		{
			name:          "Invalid - text like 'yesterday'",
			timestamp:     "yesterday",
			expectError:   true,
			errorContains: "failed to parse timestamp",
		},
		{
			name:          "Invalid - malformed date",
			timestamp:     "2025-13-45",
			expectError:   true,
			errorContains: "failed to parse timestamp",
		},
		{
			name:          "Invalid - random string",
			timestamp:     "not-a-timestamp-at-all",
			expectError:   true,
			errorContains: "failed to parse timestamp",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var capturedTime time.Time

			mockAPI := &MockPrometheusAPI{
				QueryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
					capturedTime = ts
					return model.Vector{&model.Sample{
						Metric:    model.Metric{},
						Value:     model.SampleValue(1),
						Timestamp: model.TimeFromUnix(ts.Unix()),
					}}, nil, nil
				},
			}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, queryToolDef, container.QueryHandler)

			args := map[string]any{
				"query": "up",
			}
			if tc.timestamp != "" {
				args["timestamp"] = tc.timestamp
			}

			result, err := ts.CallTool(ts.Context(), "query", args)
			resultText := mcptest.GetResultText(result)
			isError := result != nil && result.IsError

			if tc.expectError {
				require.True(t, isError || err != nil, "expected an error for timestamp %q", tc.timestamp)
				if tc.errorContains != "" && resultText != "" {
					require.Contains(t, resultText, tc.errorContains)
				}
			} else {
				require.NoError(t, err)
				require.False(t, isError, "unexpected error: %s", resultText)
				if tc.validateTime != nil {
					tc.validateTime(t, capturedTime)
				}
			}
		})
	}
}

// TestQueryHandlerWithWarnings verifies that Prometheus warnings are included
// in the tool response.
func TestQueryHandlerWithWarnings(t *testing.T) {
	t.Parallel()

	t.Run("single warning is included in response", func(t *testing.T) {
		t.Parallel()

		mockAPI := &MockPrometheusAPI{
			QueryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				return model.Vector{&model.Sample{
					Metric:    model.Metric{},
					Value:     model.SampleValue(1),
					Timestamp: model.TimeFromUnix(ts.Unix()),
				}}, promv1.Warnings{"instant query warning"}, nil
			},
		}
		container := newTestContainer(mockAPI)

		ts := mcptest.NewTestServer(t)
		mcptest.AddTool(ts, queryToolDef, container.QueryHandler)

		result, err := ts.CallTool(ts.Context(), "query", map[string]any{
			"query": "up",
		})

		require.NoError(t, err)
		require.False(t, result.IsError)

		resultText := mcptest.GetResultText(result)
		require.Contains(t, resultText, "instant query warning")

		// Verify the warnings field is properly structured in JSON.
		var parsed struct {
			Result   string   `json:"result"`
			Warnings []string `json:"warnings"`
		}
		require.NoError(t, json.Unmarshal([]byte(resultText), &parsed))
		require.Len(t, parsed.Warnings, 1)
		require.Equal(t, "instant query warning", parsed.Warnings[0])
	})

	t.Run("multiple warnings are included in response", func(t *testing.T) {
		t.Parallel()

		mockAPI := &MockPrometheusAPI{
			QueryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				return model.Vector{&model.Sample{
					Metric:    model.Metric{},
					Value:     model.SampleValue(1),
					Timestamp: model.TimeFromUnix(ts.Unix()),
				}}, promv1.Warnings{"warning 1", "warning 2", "warning 3"}, nil
			},
		}
		container := newTestContainer(mockAPI)

		ts := mcptest.NewTestServer(t)
		mcptest.AddTool(ts, queryToolDef, container.QueryHandler)

		result, err := ts.CallTool(ts.Context(), "query", map[string]any{
			"query": "up",
		})

		require.NoError(t, err)
		require.False(t, result.IsError)

		resultText := mcptest.GetResultText(result)

		var parsed struct {
			Result   string   `json:"result"`
			Warnings []string `json:"warnings"`
		}
		require.NoError(t, json.Unmarshal([]byte(resultText), &parsed))
		require.Len(t, parsed.Warnings, 3)
		require.Equal(t, []string{"warning 1", "warning 2", "warning 3"}, parsed.Warnings)
	})

	t.Run("no warnings results in null warnings field", func(t *testing.T) {
		t.Parallel()

		mockAPI := &MockPrometheusAPI{
			QueryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				return model.Vector{&model.Sample{
					Metric:    model.Metric{},
					Value:     model.SampleValue(1),
					Timestamp: model.TimeFromUnix(ts.Unix()),
				}}, nil, nil
			},
		}
		container := newTestContainer(mockAPI)

		ts := mcptest.NewTestServer(t)
		mcptest.AddTool(ts, queryToolDef, container.QueryHandler)

		result, err := ts.CallTool(ts.Context(), "query", map[string]any{
			"query": "up",
		})

		require.NoError(t, err)
		require.False(t, result.IsError)

		resultText := mcptest.GetResultText(result)
		require.Contains(t, resultText, `"warnings":null`)
	})
}

// TestRangeQueryHandlerWithWarnings verifies that range_query also propagates warnings.
func TestRangeQueryHandlerWithWarnings(t *testing.T) {
	t.Parallel()

	mockAPI := &MockPrometheusAPI{
		QueryRangeFunc: func(ctx context.Context, query string, r promv1.Range, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
			return model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{},
					Values: []model.SamplePair{
						{Timestamp: model.TimeFromUnix(1756143048), Value: 1},
					},
				},
			}, promv1.Warnings{"range query warning"}, nil
		},
	}
	container := newTestContainer(mockAPI)

	ts := mcptest.NewTestServer(t)
	mcptest.AddTool(ts, rangeQueryToolDef, container.RangeQueryHandler)

	result, err := ts.CallTool(ts.Context(), "range_query", map[string]any{
		"query":      "up",
		"start_time": "1756140000",
		"end_time":   "1756143600",
		"step":       "15s",
	})

	require.NoError(t, err)
	require.False(t, result.IsError)

	resultText := mcptest.GetResultText(result)

	var parsed struct {
		Result   string   `json:"result"`
		Warnings []string `json:"warnings"`
	}
	require.NoError(t, json.Unmarshal([]byte(resultText), &parsed))
	require.Len(t, parsed.Warnings, 1)
	require.Equal(t, "range query warning", parsed.Warnings[0])
}

// TestConcurrentQueryCalls verifies thread safety under parallel tool calls.
func TestConcurrentQueryCalls(t *testing.T) {
	t.Parallel()

	const numGoroutines = 50
	const callsPerGoroutine = 10

	var mu sync.Mutex
	queryCounts := make(map[string]int)

	mockAPI := &MockPrometheusAPI{
		QueryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
			mu.Lock()
			queryCounts[query]++
			mu.Unlock()

			time.Sleep(1 * time.Millisecond)
			return model.Vector{&model.Sample{
				Metric:    model.Metric{"query": model.LabelValue(query)},
				Value:     model.SampleValue(1),
				Timestamp: model.TimeFromUnix(ts.Unix()),
			}}, nil, nil
		},
	}
	container := newTestContainer(mockAPI)

	ts := mcptest.NewTestServer(t)
	mcptest.AddTool(ts, queryToolDef, container.QueryHandler)

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines*callsPerGoroutine)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < callsPerGoroutine; j++ {
				query := "up{goroutine=\"" + string(rune('A'+goroutineID%26)) + "\"}"
				result, err := ts.CallTool(ts.Context(), "query", map[string]any{
					"query": query,
				})
				if err != nil {
					errors <- err
					continue
				}
				if result.IsError {
					errors <- fmt.Errorf("tool returned IsError=true for query goroutine %d call %d", goroutineID, j)
				}
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Collect any errors.
	var errCount int
	for err := range errors {
		if err != nil {
			errCount++
			t.Logf("Error during concurrent call: %v", err)
		}
	}

	// All calls should succeed without errors.
	require.Zero(t, errCount, "expected no errors during concurrent calls")

	// Verify total call count.
	mu.Lock()
	totalCalls := 0
	for _, count := range queryCounts {
		totalCalls += count
	}
	mu.Unlock()

	require.Equal(t, numGoroutines*callsPerGoroutine, totalCalls, "expected all calls to be processed")
}

// TestConcurrentMultipleToolCalls verifies thread safety when calling different
// tools concurrently.
func TestConcurrentMultipleToolCalls(t *testing.T) {
	t.Parallel()

	const numGoroutines = 20

	var mu sync.Mutex
	callCounts := make(map[string]int)

	mockAPI := &MockPrometheusAPI{
		QueryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
			mu.Lock()
			callCounts["query"]++
			mu.Unlock()
			time.Sleep(1 * time.Millisecond)
			return model.Vector{&model.Sample{
				Metric:    model.Metric{},
				Value:     model.SampleValue(1),
				Timestamp: model.TimeFromUnix(ts.Unix()),
			}}, nil, nil
		},
		LabelNamesFunc: func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) (model.LabelNames, promv1.Warnings, error) {
			mu.Lock()
			callCounts["label_names"]++
			mu.Unlock()
			time.Sleep(1 * time.Millisecond)
			return model.LabelNames{"__name__", "job", "instance"}, nil, nil
		},
		LabelValuesFunc: func(ctx context.Context, label string, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) (model.LabelValues, promv1.Warnings, error) {
			mu.Lock()
			callCounts["label_values"]++
			mu.Unlock()
			time.Sleep(1 * time.Millisecond)
			return model.LabelValues{"prometheus", "node"}, nil, nil
		},
		SeriesFunc: func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) ([]model.LabelSet, promv1.Warnings, error) {
			mu.Lock()
			callCounts["series"]++
			mu.Unlock()
			time.Sleep(1 * time.Millisecond)
			return []model.LabelSet{{"__name__": "up"}}, nil, nil
		},
	}
	container := newTestContainer(mockAPI)

	ts := mcptest.NewTestServer(t)
	mcptest.AddTool(ts, queryToolDef, container.QueryHandler)
	mcptest.AddTool(ts, labelNamesToolDef, container.LabelNamesHandler)
	mcptest.AddTool(ts, labelValuesToolDef, container.LabelValuesHandler)
	mcptest.AddTool(ts, seriesToolDef, container.SeriesHandler)

	var wg sync.WaitGroup
	errs := make(chan error, numGoroutines*4)

	// Launch goroutines for each tool type.
	for i := 0; i < numGoroutines; i++ {
		// Query tool.
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := ts.CallTool(ts.Context(), "query", map[string]any{"query": "up"})
			if err != nil {
				errs <- err
			} else if result.IsError {
				errs <- errors.New("query tool returned IsError=true")
			}
		}()

		// Label names tool.
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := ts.CallTool(ts.Context(), "label_names", map[string]any{})
			if err != nil {
				errs <- err
			} else if result.IsError {
				errs <- errors.New("label_names tool returned IsError=true")
			}
		}()

		// Label values tool.
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := ts.CallTool(ts.Context(), "label_values", map[string]any{"label": "job"})
			if err != nil {
				errs <- err
			} else if result.IsError {
				errs <- errors.New("label_values tool returned IsError=true")
			}
		}()

		// Series tool.
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := ts.CallTool(ts.Context(), "series", map[string]any{"matches": []string{"up"}})
			if err != nil {
				errs <- err
			} else if result.IsError {
				errs <- errors.New("series tool returned IsError=true")
			}
		}()
	}

	wg.Wait()
	close(errs)

	// Collect any errors.
	var errCount int
	for err := range errs {
		if err != nil {
			errCount++
			t.Logf("Error during concurrent call: %v", err)
		}
	}

	require.Zero(t, errCount, "expected no errors during concurrent multi-tool calls")

	// Verify each tool was called the expected number of times.
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, numGoroutines, callCounts["query"])
	require.Equal(t, numGoroutines, callCounts["label_names"])
	require.Equal(t, numGoroutines, callCounts["label_values"])
	require.Equal(t, numGoroutines, callCounts["series"])
}

// TestRangeQueryStepCalculation tests the automatic step calculation.
func TestRangeQueryStepCalculation(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		startTime    string
		endTime      string
		expectedStep time.Duration
	}{
		{
			name:         "1 hour range defaults to 14s step",
			startTime:    "1756140000",
			endTime:      "1756143600",     // 3600 seconds apart.
			expectedStep: 14 * time.Second, // floor(3600/250) = 14.
		},
		{
			name:         "24 hour range defaults to 345s step",
			startTime:    "1756056000",
			endTime:      "1756142400",      // 86400 seconds apart.
			expectedStep: 345 * time.Second, // floor(86400/250) = 345.
		},
		{
			name:         "5 minute range defaults to 1s step",
			startTime:    "1756143000",
			endTime:      "1756143300",    // 300 seconds apart.
			expectedStep: 1 * time.Second, // floor(300/250) = 1.
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var capturedStep time.Duration

			mockAPI := &MockPrometheusAPI{
				QueryRangeFunc: func(ctx context.Context, query string, r promv1.Range, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
					capturedStep = r.Step
					return model.Matrix{
						&model.SampleStream{
							Metric: model.Metric{},
							Values: []model.SamplePair{
								{Timestamp: model.TimeFromUnix(1756143048), Value: 1},
							},
						},
					}, nil, nil
				},
			}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, rangeQueryToolDef, container.RangeQueryHandler)

			result, err := ts.CallTool(ts.Context(), "range_query", map[string]any{
				"query":      "up",
				"start_time": tc.startTime,
				"end_time":   tc.endTime,
				// Auto-calculate step.
			})

			require.NoError(t, err)
			require.False(t, result.IsError)
			require.Equal(t, tc.expectedStep, capturedStep, "auto-calculated step should match expected")
		})
	}
}

// TestSeriesHandlerMultipleMatchers tests series with multiple matchers.
func TestSeriesHandlerMultipleMatchers(t *testing.T) {
	t.Parallel()

	var capturedMatches []string

	mockAPI := &MockPrometheusAPI{
		SeriesFunc: func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) ([]model.LabelSet, promv1.Warnings, error) {
			capturedMatches = matches
			return []model.LabelSet{
				{"__name__": "http_requests_total", "method": "GET"},
				{"__name__": "http_requests_total", "method": "POST"},
			}, nil, nil
		},
	}
	container := newTestContainer(mockAPI)

	ts := mcptest.NewTestServer(t)
	mcptest.AddTool(ts, seriesToolDef, container.SeriesHandler)

	result, err := ts.CallTool(ts.Context(), "series", map[string]any{
		"matches": []string{
			`http_requests_total{method="GET"}`,
			`http_requests_total{method="POST"}`,
		},
	})

	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Len(t, capturedMatches, 2)
	require.Contains(t, capturedMatches, `http_requests_total{method="GET"}`)
	require.Contains(t, capturedMatches, `http_requests_total{method="POST"}`)

	resultText := mcptest.GetResultText(result)
	require.Contains(t, resultText, "http_requests_total")
	require.Contains(t, resultText, "GET")
	require.Contains(t, resultText, "POST")
}

// TestSeriesHandlerSpecialCharactersInMatchers tests matchers with special characters.
func TestSeriesHandlerSpecialCharactersInMatchers(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		matcher string
	}{
		{
			name:    "regex matcher",
			matcher: `http_requests_total{method=~"GET|POST"}`,
		},
		{
			name:    "negative matcher",
			matcher: `http_requests_total{method!="DELETE"}`,
		},
		{
			name:    "negative regex matcher",
			matcher: `http_requests_total{method!~"DELETE|PUT"}`,
		},
		{
			name:    "matcher with special characters in value",
			matcher: `http_requests_total{path="/api/v1/query"}`,
		},
		{
			name:    "matcher with unicode",
			matcher: `http_requests_total{service="api-\u4e2d\u6587"}`,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var capturedMatcher string

			mockAPI := &MockPrometheusAPI{
				SeriesFunc: func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) ([]model.LabelSet, promv1.Warnings, error) {
					if len(matches) > 0 {
						capturedMatcher = matches[0]
					}
					return []model.LabelSet{
						{"__name__": "http_requests_total"},
					}, nil, nil
				},
			}
			container := newTestContainer(mockAPI)

			ts := mcptest.NewTestServer(t)
			mcptest.AddTool(ts, seriesToolDef, container.SeriesHandler)

			result, err := ts.CallTool(ts.Context(), "series", map[string]any{
				"matches": []string{tc.matcher},
			})

			require.NoError(t, err)
			require.False(t, result.IsError)
			require.Equal(t, tc.matcher, capturedMatcher)
		})
	}
}

// Health endpoint tests. These intentionally do not run in parallel: SetReady
// writes the shared package-level server ready gauge.

func TestSetReady(t *testing.T) {
	s := &ServerContainer{}

	require.False(t, s.Ready())

	s.SetReady(true)
	require.True(t, s.Ready())
	require.InDelta(t, 1, testutil.ToFloat64(metricServerReady), 0)

	s.SetReady(false)
	require.False(t, s.Ready())
	require.InDelta(t, 0, testutil.ToFloat64(metricServerReady), 0)
}

func TestHandleHealthy(t *testing.T) {
	s := &ServerContainer{}

	rec := httptest.NewRecorder()
	s.HandleHealthy().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/-/healthy", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Healthy")
}

func TestHandleReady(t *testing.T) {
	s := &ServerContainer{}

	rec := httptest.NewRecorder()
	s.HandleReady().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/-/ready", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "Not Ready")

	s.SetReady(true)
	rec = httptest.NewRecorder()
	s.HandleReady().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/-/ready", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Ready")

	s.SetReady(false)
	rec = httptest.NewRecorder()
	s.HandleReady().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/-/ready", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
