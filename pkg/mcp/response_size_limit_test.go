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
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/promslog"
	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus-mcp/pkg/mcp/mcptest"
)

func newSizeLimitedContainer(t *testing.T) (*ServerContainer, *atomic.Pointer[string]) {
	t.Helper()

	var gotAuth atomic.Pointer[string]
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		gotAuth.Store(&auth)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"up"},"value":[1700000000,"1"]}]}}`)
	}))
	t.Cleanup(backend.Close)

	_, container, err := NewServer(context.Background(), ServerConfig{
		Logger:            promslog.NewNopLogger(),
		PrometheusURL:     backend.URL,
		PrometheusTimeout: 10 * time.Second,
		MaxResponseSize:   16,
		EnabledTools:      []string{"all"},
	})
	require.NoError(t, err)
	return container, &gotAuth
}

func requireSizeLimitError(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()

	require.True(t, result.IsError)
	require.Len(t, result.Content, 1)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Contains(t, text.Text, "exceeds the limit")
}

func TestQueryToolReportsOversizedResponse(t *testing.T) {
	container, _ := newSizeLimitedContainer(t)

	ts := mcptest.NewTestServer(t)
	mcptest.AddTool(ts, queryToolDef, container.QueryHandler)

	result, err := ts.CallTool(ts.Context(), "query", map[string]any{"query": "up"})
	require.NoError(t, err)
	requireSizeLimitError(t, result)
}

func TestSizeLimitCoversForwardedAuthorization(t *testing.T) {
	container, gotAuth := newSizeLimitedContainer(t)
	ctx := addAuthToContext(context.Background(), "Bearer forwarded-token")

	result, _, err := container.QueryHandler(ctx, &mcp.CallToolRequest{}, QueryInput{Query: "up"})
	require.NoError(t, err)
	requireSizeLimitError(t, result)
	require.Equal(t, "Bearer forwarded-token", *gotAuth.Load())
}

func TestSizeLimitCoversRawHTTPRequests(t *testing.T) {
	container, _ := newSizeLimitedContainer(t)
	failed := metricAPICallsFailed.With(prometheus.Labels{"target_path": "/api/v1/stores"})
	failedBefore := testutil.ToFloat64(failed)

	result, _, err := container.ThanosStoresHandler(context.Background(), &mcp.CallToolRequest{}, EmptyInput{})
	require.NoError(t, err)
	requireSizeLimitError(t, result)
	require.InDelta(t, failedBefore+1, testutil.ToFloat64(failed), 0)
}
