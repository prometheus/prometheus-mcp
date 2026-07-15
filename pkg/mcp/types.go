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

import "log/slog"

// Tool input structs for the official modelcontextprotocol/go-sdk library.
// These structs use jsonschema tags for automatic schema generation.
// Fields without omitempty in the json tag are considered required.

// Common embedded structs for reuse across multiple tools.

// EmptyInput is used for tools that have no input parameters.
type EmptyInput struct{}

// TimeRangeInput provides optional start/end time parameters for time-bounded queries.
type TimeRangeInput struct {
	StartTime string `json:"start_time,omitempty" jsonschema:"start timestamp for the query. Accepts: Unix epoch seconds, RFC3339, or a duration string relative to now (e.g. 5m, 1h30m, etc). Defaults to 5m ago."`
	EndTime   string `json:"end_time,omitempty" jsonschema:"end timestamp for the query. Accepts: Unix epoch seconds, RFC3339, or a duration string relative to now (e.g. 5m, 1h30m, etc). Defaults to current time."`
}

// TruncatableInput provides optional truncation limit for query responses.
type TruncatableInput struct {
	TruncationLimit int `json:"truncation_limit,omitempty" jsonschema:"truncation limit for query response in number of lines/entries; use lower limits (e.g. 50-100) for initial exploration, set to -1 to disable truncation"`
}

// Tool definition structs

// QueryInput is the input for the instant query tool.
type QueryInput struct {
	Query     string `json:"query" jsonschema:"the PromQL query to execute"`
	Timestamp string `json:"timestamp,omitempty" jsonschema:"evaluation timestamp for the instant query. Accepts: Unix epoch seconds, RFC3339, or a duration string relative to now e.g. 5m, 1h30m, etc. Defaults to current time."`
	TruncatableInput
}

// LogValue implements slog.LogValuer.
func (qi QueryInput) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("query", qi.Query),
		slog.String("timestamp", qi.Timestamp),
	)
}

// RangeQueryInput is the input for the range query tool.
type RangeQueryInput struct {
	Query string `json:"query" jsonschema:"the PromQL query to execute"`
	Step  string `json:"step,omitempty" jsonschema:"query resolution step width in Go duration format (e.g. '30s', '5m', '1h'), auto-set if unspecified"`
	TimeRangeInput
	TruncatableInput
}

// LogValue implements slog.LogValuer.
func (rqi RangeQueryInput) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("query", rqi.Query),
		slog.String("step", rqi.Step),
		slog.String("start_time", rqi.StartTime),
		slog.String("end_time", rqi.EndTime),
	)
}

// ExemplarQueryInput is the input for the exemplar query tool.
type ExemplarQueryInput struct {
	Query string `json:"query" jsonschema:"the PromQL query to execute"`
	TimeRangeInput
	TruncatableInput
}

// LogValue implements slog.LogValuer.
func (eqi ExemplarQueryInput) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("query", eqi.Query),
		slog.String("start_time", eqi.StartTime),
		slog.String("end_time", eqi.EndTime),
	)
}

// SeriesInput is the input for the series query tool.
type SeriesInput struct {
	Matches []string `json:"matches" jsonschema:"series selector arguments that select the series to return,required"`
	TimeRangeInput
	TruncatableInput
}

// LogValue implements slog.LogValuer.
func (si SeriesInput) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("matches", si.Matches),
		slog.String("start_time", si.StartTime),
		slog.String("end_time", si.EndTime),
	)
}

// LabelNamesInput is the input for the label names query tool.
type LabelNamesInput struct {
	Matches []string `json:"matches,omitempty" jsonschema:"series selector arguments to filter label names"`
	TimeRangeInput
	TruncatableInput
}

// LogValue implements slog.LogValuer.
func (lni LabelNamesInput) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("matches", lni.Matches),
		slog.String("start_time", lni.StartTime),
		slog.String("end_time", lni.EndTime),
	)
}

// LabelValuesInput is the input for the label values query tool.
type LabelValuesInput struct {
	Label   string   `json:"label" jsonschema:"the label to query values for,required"`
	Matches []string `json:"matches,omitempty" jsonschema:"series selector arguments to filter label values"`
	TimeRangeInput
	TruncatableInput
}

// LogValue implements slog.LogValuer.
func (lvi LabelValuesInput) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("label", lvi.Label),
		slog.Any("matches", lvi.Matches),
		slog.String("start_time", lvi.StartTime),
		slog.String("end_time", lvi.EndTime),
	)
}

// MetricMetadataInput is the input for the metric metadata tool.
type MetricMetadataInput struct {
	Metric string `json:"metric,omitempty" jsonschema:"metric name to retrieve metadata for, all metrics if empty"`
	Limit  string `json:"limit,omitempty" jsonschema:"maximum number of metrics to return"`
}

// LogValue implements slog.LogValuer.
func (mmi MetricMetadataInput) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("metric", mmi.Metric),
		slog.String("limit", mmi.Limit),
	)
}

// TargetsMetadataInput is the input for the targets metadata tool.
type TargetsMetadataInput struct {
	MatchTarget string `json:"match_target,omitempty" jsonschema:"label selectors to match targets, all targets if empty"`
	Metric      string `json:"metric,omitempty" jsonschema:"metric name to retrieve metadata for, all metrics if empty"`
	Limit       string `json:"limit,omitempty" jsonschema:"maximum number of targets to match"`
}

// LogValue implements slog.LogValuer.
func (tmi TargetsMetadataInput) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("match_target", tmi.MatchTarget),
		slog.String("metric", tmi.Metric),
		slog.String("limit", tmi.Limit),
	)
}

// DeleteSeriesInput is the input for the delete series admin tool.
type DeleteSeriesInput struct {
	Matches []string `json:"matches" jsonschema:"series selector arguments for series to delete,required"`
	TimeRangeInput
}

// LogValue implements slog.LogValuer.
func (dsi DeleteSeriesInput) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("matches", dsi.Matches),
		slog.String("start_time", dsi.StartTime),
		slog.String("end_time", dsi.EndTime),
	)
}

// SnapshotInput is the input for the snapshot admin tool.
type SnapshotInput struct {
	SkipHead bool `json:"skip_head,omitempty" jsonschema:"skip data present in the head block"`
}

// LogValue implements slog.LogValuer.
func (si SnapshotInput) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Bool("skip_head", si.SkipHead),
	)
}

// DocsReadInput is the input for the docs read tool.
type DocsReadInput struct {
	File string `json:"file" jsonschema:"the name of the documentation file to read"`
}

// LogValue implements slog.LogValuer.
func (dri DocsReadInput) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("file", dri.File),
	)
}

// RunbooksReadInput is the input for the runbooks read tool.
type RunbooksReadInput struct {
	Name string `json:"name" jsonschema:"the skill name of the runbook to read, as returned by runbooks_list (e.g. investigate-error-rates)"`
}

// LogValue implements slog.LogValuer.
func (rri RunbooksReadInput) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", rri.Name),
	)
}

// DocsSearchInput is the input for the docs search tool.
type DocsSearchInput struct {
	Query string `json:"query" jsonschema:"the query to search for in documentation"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum number of search results to return"`
}

// LogValue implements slog.LogValuer.
func (dsi DocsSearchInput) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("query", dsi.Query),
		slog.Int("limit", dsi.Limit),
	)
}
