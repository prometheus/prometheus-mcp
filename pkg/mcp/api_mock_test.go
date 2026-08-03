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
	"sync"
	"testing"
	"time"

	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus-mcp/pkg/mcp/mcptest"
)

var _ promv1.API = (*MockPrometheusAPI)(nil)

// QueryCall records the parameters of a Query call.
type QueryCall struct {
	Query     string
	Timestamp time.Time
}

// QueryRangeCall records the parameters of a QueryRange call.
type QueryRangeCall struct {
	Query string
	Range promv1.Range
}

// LabelNamesCall records the parameters of a LabelNames call.
type LabelNamesCall struct {
	Matches   []string
	StartTime time.Time
	EndTime   time.Time
}

// LabelValuesCall records the parameters of a LabelValues call.
type LabelValuesCall struct {
	Label     string
	Matches   []string
	StartTime time.Time
	EndTime   time.Time
}

// SeriesCall records the parameters of a Series call.
type SeriesCall struct {
	Matches   []string
	StartTime time.Time
	EndTime   time.Time
}

// MetadataCall records the parameters of a Metadata call.
type MetadataCall struct {
	Metric string
	Limit  string
}

// MockPrometheusAPI is a mock implementation of the promv1.API interface.
// It supports both function injection for custom behavior and call tracking
// for verifying that handlers call the API with correct parameters.
//
// Call tracking example:
//
//	mockAPI := &MockPrometheusAPI{}
//	// ... run handler ...
//	require.Len(t, mockAPI.QueryCalls, 1)
//	require.Equal(t, "up", mockAPI.QueryCalls[0].Query)
type MockPrometheusAPI struct {
	// mu protects call tracking fields for concurrent access.
	mu sync.Mutex

	// Function fields for injecting custom behavior.
	AlertManagersFunc   func(ctx context.Context) (promv1.AlertManagersResult, error)
	AlertsFunc          func(ctx context.Context) (promv1.AlertsResult, error)
	BuildinfoFunc       func(ctx context.Context) (promv1.BuildinfoResult, error)
	ConfigFunc          func(ctx context.Context) (promv1.ConfigResult, error)
	CleanTombstonesFunc func(ctx context.Context) error
	DeleteSeriesFunc    func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time) error
	FlagsFunc           func(ctx context.Context) (promv1.FlagsResult, error)
	FormatQueryFunc     func(ctx context.Context, query string) (string, error)
	LabelNamesFunc      func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) (model.LabelNames, promv1.Warnings, error)
	LabelValuesFunc     func(ctx context.Context, label string, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) (model.LabelValues, promv1.Warnings, error)
	MetadataFunc        func(ctx context.Context, metric string, limit string) (map[string][]promv1.Metadata, error)
	QueryFunc           func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error)
	QueryExemplarsFunc  func(ctx context.Context, query string, startTime time.Time, endTime time.Time) ([]promv1.ExemplarQueryResult, error)
	QueryRangeFunc      func(ctx context.Context, query string, r promv1.Range, opts ...promv1.Option) (model.Value, promv1.Warnings, error)
	RulesFunc           func(ctx context.Context, matches []string) (promv1.RulesResult, error)
	RuntimeinfoFunc     func(ctx context.Context) (promv1.RuntimeinfoResult, error)
	SeriesFunc          func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) ([]model.LabelSet, promv1.Warnings, error)
	SnapshotFunc        func(ctx context.Context, skipHead bool) (promv1.SnapshotResult, error)
	TargetsFunc         func(ctx context.Context) (promv1.TargetsResult, error)
	TargetsMetadataFunc func(ctx context.Context, matchTarget string, metric string, limit string) ([]promv1.MetricMetadata, error)
	TSDBBlocksFunc      func(ctx context.Context) (promv1.TSDBBlocksResult, error)
	TSDBFunc            func(ctx context.Context, opts ...promv1.Option) (promv1.TSDBResult, error)
	WALReplayFunc       func(ctx context.Context) (promv1.WalReplayStatus, error)

	// Call tracking fields for verifying handler behavior.
	// These are populated when the corresponding methods are called.
	QueryCalls       []QueryCall
	QueryRangeCalls  []QueryRangeCall
	LabelNamesCalls  []LabelNamesCall
	LabelValuesCalls []LabelValuesCall
	SeriesCalls      []SeriesCall
	MetadataCalls    []MetadataCall
}

// ResetCalls clears all recorded call tracking data.
// Useful for tests that need to verify calls across multiple phases.
func (m *MockPrometheusAPI) ResetCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.QueryCalls = nil
	m.QueryRangeCalls = nil
	m.LabelNamesCalls = nil
	m.LabelValuesCalls = nil
	m.SeriesCalls = nil
	m.MetadataCalls = nil
}

// GetQueryCalls returns a copy of the recorded Query calls.
// Thread-safe for concurrent test execution.
func (m *MockPrometheusAPI) GetQueryCalls() []QueryCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]QueryCall, len(m.QueryCalls))
	copy(result, m.QueryCalls)
	return result
}

// GetQueryRangeCalls returns a copy of the recorded QueryRange calls.
// Thread-safe for concurrent test execution.
func (m *MockPrometheusAPI) GetQueryRangeCalls() []QueryRangeCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]QueryRangeCall, len(m.QueryRangeCalls))
	copy(result, m.QueryRangeCalls)
	return result
}

// GetLabelNamesCalls returns a copy of the recorded LabelNames calls.
// Thread-safe for concurrent test execution.
func (m *MockPrometheusAPI) GetLabelNamesCalls() []LabelNamesCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]LabelNamesCall, len(m.LabelNamesCalls))
	copy(result, m.LabelNamesCalls)
	return result
}

// GetLabelValuesCalls returns a copy of the recorded LabelValues calls.
// Thread-safe for concurrent test execution.
func (m *MockPrometheusAPI) GetLabelValuesCalls() []LabelValuesCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]LabelValuesCall, len(m.LabelValuesCalls))
	copy(result, m.LabelValuesCalls)
	return result
}

// GetSeriesCalls returns a copy of the recorded Series calls.
// Thread-safe for concurrent test execution.
func (m *MockPrometheusAPI) GetSeriesCalls() []SeriesCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]SeriesCall, len(m.SeriesCalls))
	copy(result, m.SeriesCalls)
	return result
}

// GetMetadataCalls returns a copy of the recorded Metadata calls.
// Thread-safe for concurrent test execution.
func (m *MockPrometheusAPI) GetMetadataCalls() []MetadataCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]MetadataCall, len(m.MetadataCalls))
	copy(result, m.MetadataCalls)
	return result
}

// Implement all methods of promv1.API to delegate to the function fields.
func (m *MockPrometheusAPI) AlertManagers(ctx context.Context) (promv1.AlertManagersResult, error) {
	if m.AlertManagersFunc != nil {
		return m.AlertManagersFunc(ctx)
	}
	return promv1.AlertManagersResult{}, nil
}
func (m *MockPrometheusAPI) Alerts(ctx context.Context) (promv1.AlertsResult, error) {
	if m.AlertsFunc != nil {
		return m.AlertsFunc(ctx)
	}
	return promv1.AlertsResult{}, nil
}
func (m *MockPrometheusAPI) Buildinfo(ctx context.Context) (promv1.BuildinfoResult, error) {
	if m.BuildinfoFunc != nil {
		return m.BuildinfoFunc(ctx)
	}
	return promv1.BuildinfoResult{}, nil
}
func (m *MockPrometheusAPI) Config(ctx context.Context) (promv1.ConfigResult, error) {
	if m.ConfigFunc != nil {
		return m.ConfigFunc(ctx)
	}
	return promv1.ConfigResult{}, nil
}
func (m *MockPrometheusAPI) CleanTombstones(ctx context.Context) error {
	if m.CleanTombstonesFunc != nil {
		return m.CleanTombstonesFunc(ctx)
	}
	return nil
}
func (m *MockPrometheusAPI) DeleteSeries(ctx context.Context, matches []string, startTime time.Time, endTime time.Time) error {
	if m.DeleteSeriesFunc != nil {
		return m.DeleteSeriesFunc(ctx, matches, startTime, endTime)
	}
	return nil
}
func (m *MockPrometheusAPI) Flags(ctx context.Context) (promv1.FlagsResult, error) {
	if m.FlagsFunc != nil {
		return m.FlagsFunc(ctx)
	}
	return promv1.FlagsResult{}, nil
}
func (m *MockPrometheusAPI) FormatQuery(ctx context.Context, query string) (string, error) {
	if m.FormatQueryFunc != nil {
		return m.FormatQueryFunc(ctx, query)
	}
	return "", nil
}
func (m *MockPrometheusAPI) LabelNames(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) (model.LabelNames, promv1.Warnings, error) {
	// Record the call for verification.
	m.mu.Lock()
	m.LabelNamesCalls = append(m.LabelNamesCalls, LabelNamesCall{
		Matches:   matches,
		StartTime: startTime,
		EndTime:   endTime,
	})
	m.mu.Unlock()

	if m.LabelNamesFunc != nil {
		return m.LabelNamesFunc(ctx, matches, startTime, endTime, opts...)
	}
	return nil, nil, nil
}
func (m *MockPrometheusAPI) LabelValues(ctx context.Context, label string, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) (model.LabelValues, promv1.Warnings, error) {
	// Record the call for verification.
	m.mu.Lock()
	m.LabelValuesCalls = append(m.LabelValuesCalls, LabelValuesCall{
		Label:     label,
		Matches:   matches,
		StartTime: startTime,
		EndTime:   endTime,
	})
	m.mu.Unlock()

	if m.LabelValuesFunc != nil {
		return m.LabelValuesFunc(ctx, label, matches, startTime, endTime, opts...)
	}
	return nil, nil, nil
}
func (m *MockPrometheusAPI) Metadata(ctx context.Context, metric string, limit string) (map[string][]promv1.Metadata, error) {
	// Record the call for verification.
	m.mu.Lock()
	m.MetadataCalls = append(m.MetadataCalls, MetadataCall{
		Metric: metric,
		Limit:  limit,
	})
	m.mu.Unlock()

	if m.MetadataFunc != nil {
		return m.MetadataFunc(ctx, metric, limit)
	}
	return nil, nil
}
func (m *MockPrometheusAPI) Query(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
	// Record the call for verification.
	m.mu.Lock()
	m.QueryCalls = append(m.QueryCalls, QueryCall{
		Query:     query,
		Timestamp: ts,
	})
	m.mu.Unlock()

	if m.QueryFunc != nil {
		return m.QueryFunc(ctx, query, ts, opts...)
	}
	return nil, nil, nil
}
func (m *MockPrometheusAPI) QueryExemplars(ctx context.Context, query string, startTime time.Time, endTime time.Time) ([]promv1.ExemplarQueryResult, error) {
	if m.QueryExemplarsFunc != nil {
		return m.QueryExemplarsFunc(ctx, query, startTime, endTime)
	}
	return nil, nil
}
func (m *MockPrometheusAPI) QueryRange(ctx context.Context, query string, r promv1.Range, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
	// Record the call for verification.
	m.mu.Lock()
	m.QueryRangeCalls = append(m.QueryRangeCalls, QueryRangeCall{
		Query: query,
		Range: r,
	})
	m.mu.Unlock()

	if m.QueryRangeFunc != nil {
		return m.QueryRangeFunc(ctx, query, r, opts...)
	}
	return nil, nil, nil
}
func (m *MockPrometheusAPI) Rules(ctx context.Context, matches []string) (promv1.RulesResult, error) {
	if m.RulesFunc != nil {
		return m.RulesFunc(ctx, matches)
	}
	return promv1.RulesResult{}, nil
}
func (m *MockPrometheusAPI) Runtimeinfo(ctx context.Context) (promv1.RuntimeinfoResult, error) {
	if m.RuntimeinfoFunc != nil {
		return m.RuntimeinfoFunc(ctx)
	}
	return promv1.RuntimeinfoResult{}, nil
}
func (m *MockPrometheusAPI) Series(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) ([]model.LabelSet, promv1.Warnings, error) {
	// Record the call for verification.
	m.mu.Lock()
	m.SeriesCalls = append(m.SeriesCalls, SeriesCall{
		Matches:   matches,
		StartTime: startTime,
		EndTime:   endTime,
	})
	m.mu.Unlock()

	if m.SeriesFunc != nil {
		return m.SeriesFunc(ctx, matches, startTime, endTime, opts...)
	}
	return nil, nil, nil
}
func (m *MockPrometheusAPI) Snapshot(ctx context.Context, skipHead bool) (promv1.SnapshotResult, error) {
	if m.SnapshotFunc != nil {
		return m.SnapshotFunc(ctx, skipHead)
	}
	return promv1.SnapshotResult{}, nil
}
func (m *MockPrometheusAPI) Targets(ctx context.Context) (promv1.TargetsResult, error) {
	if m.TargetsFunc != nil {
		return m.TargetsFunc(ctx)
	}
	return promv1.TargetsResult{}, nil
}
func (m *MockPrometheusAPI) TargetsMetadata(ctx context.Context, matchTarget string, metric string, limit string) ([]promv1.MetricMetadata, error) {
	if m.TargetsMetadataFunc != nil {
		return m.TargetsMetadataFunc(ctx, matchTarget, metric, limit)
	}
	return nil, nil
}
func (m *MockPrometheusAPI) TSDBBlocks(ctx context.Context) (promv1.TSDBBlocksResult, error) {
	if m.TSDBBlocksFunc != nil {
		return m.TSDBBlocksFunc(ctx)
	}
	return promv1.TSDBBlocksResult{}, nil
}
func (m *MockPrometheusAPI) TSDB(ctx context.Context, opts ...promv1.Option) (promv1.TSDBResult, error) {
	if m.TSDBFunc != nil {
		return m.TSDBFunc(ctx, opts...)
	}
	return promv1.TSDBResult{}, nil
}
func (m *MockPrometheusAPI) WalReplay(ctx context.Context) (promv1.WalReplayStatus, error) {
	if m.WALReplayFunc != nil {
		return m.WALReplayFunc(ctx)
	}
	return promv1.WalReplayStatus{}, nil
}

// TestMockAPICallTracking demonstrates and verifies the call tracking
// functionality of MockPrometheusAPI.
func TestMockAPICallTracking(t *testing.T) {
	t.Parallel()

	t.Run("Query call tracking", func(t *testing.T) {
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

		// Make a query call.
		result, err := ts.CallTool(ts.Context(), "query", map[string]any{
			"query":     "up{job=\"prometheus\"}",
			"timestamp": "1756143048",
		})

		require.NoError(t, err)
		require.False(t, result.IsError)

		// Verify the call was tracked.
		calls := mockAPI.GetQueryCalls()
		require.Len(t, calls, 1)
		require.Equal(t, "up{job=\"prometheus\"}", calls[0].Query)
		require.Equal(t, int64(1756143048), calls[0].Timestamp.Unix())
	})

	t.Run("QueryRange call tracking", func(t *testing.T) {
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
				}, nil, nil
			},
		}
		container := newTestContainer(mockAPI)

		ts := mcptest.NewTestServer(t)
		mcptest.AddTool(ts, rangeQueryToolDef, container.RangeQueryHandler)

		// Make a range query call.
		result, err := ts.CallTool(ts.Context(), "range_query", map[string]any{
			"query":      "rate(http_requests_total[5m])",
			"start_time": "1756140000",
			"end_time":   "1756143600",
			"step":       "30s",
		})

		require.NoError(t, err)
		require.False(t, result.IsError)

		// Verify the call was tracked.
		calls := mockAPI.GetQueryRangeCalls()
		require.Len(t, calls, 1)
		require.Equal(t, "rate(http_requests_total[5m])", calls[0].Query)
		require.Equal(t, int64(1756140000), calls[0].Range.Start.Unix())
		require.Equal(t, int64(1756143600), calls[0].Range.End.Unix())
		require.Equal(t, 30*time.Second, calls[0].Range.Step)
	})

	t.Run("LabelNames call tracking", func(t *testing.T) {
		t.Parallel()

		mockAPI := &MockPrometheusAPI{
			LabelNamesFunc: func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) (model.LabelNames, promv1.Warnings, error) {
				return model.LabelNames{"__name__", "job", "instance"}, nil, nil
			},
		}
		container := newTestContainer(mockAPI)

		ts := mcptest.NewTestServer(t)
		mcptest.AddTool(ts, labelNamesToolDef, container.LabelNamesHandler)

		// Make a label_names call with matches.
		result, err := ts.CallTool(ts.Context(), "label_names", map[string]any{
			"matches": []string{"up", "node_cpu_seconds_total"},
		})

		require.NoError(t, err)
		require.False(t, result.IsError)

		// Verify the call was tracked.
		calls := mockAPI.GetLabelNamesCalls()
		require.Len(t, calls, 1)
		require.Equal(t, []string{"up", "node_cpu_seconds_total"}, calls[0].Matches)
	})

	t.Run("LabelValues call tracking", func(t *testing.T) {
		t.Parallel()

		mockAPI := &MockPrometheusAPI{
			LabelValuesFunc: func(ctx context.Context, label string, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) (model.LabelValues, promv1.Warnings, error) {
				return model.LabelValues{"prometheus", "node_exporter"}, nil, nil
			},
		}
		container := newTestContainer(mockAPI)

		ts := mcptest.NewTestServer(t)
		mcptest.AddTool(ts, labelValuesToolDef, container.LabelValuesHandler)

		// Make a label_values call.
		result, err := ts.CallTool(ts.Context(), "label_values", map[string]any{
			"label":   "job",
			"matches": []string{"up"},
		})

		require.NoError(t, err)
		require.False(t, result.IsError)

		// Verify the call was tracked.
		calls := mockAPI.GetLabelValuesCalls()
		require.Len(t, calls, 1)
		require.Equal(t, "job", calls[0].Label)
		require.Equal(t, []string{"up"}, calls[0].Matches)
	})

	t.Run("Series call tracking", func(t *testing.T) {
		t.Parallel()

		mockAPI := &MockPrometheusAPI{
			SeriesFunc: func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) ([]model.LabelSet, promv1.Warnings, error) {
				return []model.LabelSet{
					{"__name__": "up", "job": "prometheus"},
				}, nil, nil
			},
		}
		container := newTestContainer(mockAPI)

		ts := mcptest.NewTestServer(t)
		mcptest.AddTool(ts, seriesToolDef, container.SeriesHandler)

		// Make a series call.
		result, err := ts.CallTool(ts.Context(), "series", map[string]any{
			"matches":    []string{"http_requests_total{method=\"GET\"}"},
			"start_time": "1756140000",
			"end_time":   "1756143600",
		})

		require.NoError(t, err)
		require.False(t, result.IsError)

		// Verify the call was tracked.
		calls := mockAPI.GetSeriesCalls()
		require.Len(t, calls, 1)
		require.Equal(t, []string{"http_requests_total{method=\"GET\"}"}, calls[0].Matches)
		require.Equal(t, int64(1756140000), calls[0].StartTime.Unix())
		require.Equal(t, int64(1756143600), calls[0].EndTime.Unix())
	})

	t.Run("Metadata call tracking", func(t *testing.T) {
		t.Parallel()

		mockAPI := &MockPrometheusAPI{
			MetadataFunc: func(ctx context.Context, metric string, limit string) (map[string][]promv1.Metadata, error) {
				return map[string][]promv1.Metadata{
					"http_requests_total": {{Type: "counter", Help: "Total HTTP requests", Unit: ""}},
				}, nil
			},
		}
		container := newTestContainer(mockAPI)

		ts := mcptest.NewTestServer(t)
		mcptest.AddTool(ts, metricMetadataToolDef, container.MetricMetadataHandler)

		// Make a metric_metadata call.
		result, err := ts.CallTool(ts.Context(), "metric_metadata", map[string]any{
			"metric": "http_requests_total",
			"limit":  "10",
		})

		require.NoError(t, err)
		require.False(t, result.IsError)

		// Verify the call was tracked.
		calls := mockAPI.GetMetadataCalls()
		require.Len(t, calls, 1)
		require.Equal(t, "http_requests_total", calls[0].Metric)
		require.Equal(t, "10", calls[0].Limit)
	})

	t.Run("ResetCalls clears all tracking data", func(t *testing.T) {
		t.Parallel()

		mockAPI := &MockPrometheusAPI{
			QueryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				return model.Vector{}, nil, nil
			},
			LabelNamesFunc: func(ctx context.Context, matches []string, startTime time.Time, endTime time.Time, opts ...promv1.Option) (model.LabelNames, promv1.Warnings, error) {
				return model.LabelNames{}, nil, nil
			},
		}
		container := newTestContainer(mockAPI)

		ts := mcptest.NewTestServer(t)
		mcptest.AddTool(ts, queryToolDef, container.QueryHandler)
		mcptest.AddTool(ts, labelNamesToolDef, container.LabelNamesHandler)

		// Make some calls.
		_, _ = ts.CallTool(ts.Context(), "query", map[string]any{"query": "up"})
		_, _ = ts.CallTool(ts.Context(), "label_names", map[string]any{})

		// Verify calls were tracked.
		require.Len(t, mockAPI.GetQueryCalls(), 1)
		require.Len(t, mockAPI.GetLabelNamesCalls(), 1)

		// Reset and verify.
		mockAPI.ResetCalls()

		require.Empty(t, mockAPI.GetQueryCalls())
		require.Empty(t, mockAPI.GetLabelNamesCalls())
	})

	t.Run("Multiple calls are tracked in order", func(t *testing.T) {
		t.Parallel()

		mockAPI := &MockPrometheusAPI{
			QueryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				return model.Vector{}, nil, nil
			},
		}
		container := newTestContainer(mockAPI)

		ts := mcptest.NewTestServer(t)
		mcptest.AddTool(ts, queryToolDef, container.QueryHandler)

		// Make multiple calls.
		queries := []string{"up", "node_cpu_seconds_total", "http_requests_total"}
		for _, q := range queries {
			_, _ = ts.CallTool(ts.Context(), "query", map[string]any{"query": q})
		}

		// Verify all calls were tracked in order.
		calls := mockAPI.GetQueryCalls()
		require.Len(t, calls, 3)
		for i, q := range queries {
			require.Equal(t, q, calls[i].Query, "call %d should have query %q", i, q)
		}
	})

	t.Run("Thread-safe call tracking with concurrent calls", func(t *testing.T) {
		t.Parallel()

		mockAPI := &MockPrometheusAPI{
			QueryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
				// Small delay to increase chance of race conditions.
				time.Sleep(time.Millisecond)
				return model.Vector{}, nil, nil
			},
		}
		container := newTestContainer(mockAPI)

		ts := mcptest.NewTestServer(t)
		mcptest.AddTool(ts, queryToolDef, container.QueryHandler)

		const numGoroutines = 20
		done := make(chan struct{}, numGoroutines)

		// Make concurrent calls.
		for i := 0; i < numGoroutines; i++ {
			go func(id int) {
				_, _ = ts.CallTool(ts.Context(), "query", map[string]any{
					"query": "concurrent_test",
				})
				done <- struct{}{}
			}(i)
		}

		// Wait for all goroutines.
		for i := 0; i < numGoroutines; i++ {
			<-done
		}

		// Verify all calls were tracked.
		calls := mockAPI.GetQueryCalls()
		require.Len(t, calls, numGoroutines, "all concurrent calls should be tracked")
	})
}
