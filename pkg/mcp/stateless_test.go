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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newStatelessTestHandler builds a real MCP server and wraps it in the
// streamable HTTP handler under test.
func newStatelessTestHandler(t *testing.T, stateless bool) http.Handler {
	t.Helper()

	logger, _ := newTestLogger()
	server, _, err := NewServer(context.Background(), ServerConfig{
		Logger:        logger,
		PrometheusURL: "http://127.0.0.1:9090",
	})
	require.NoError(t, err)

	return NewStreamableHTTPHandler(server, logger, time.Minute, stateless)
}

func postJSONRPC(t *testing.T, url, body, sessionID string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// TestStreamableHTTPHandler_Stateless verifies that stateless mode serves
// requests without any session establishment — the property that lets
// load-balanced replicas serve any request — while stateful mode (the
// default) rejects a tool call that skips session initialization.
func TestStreamableHTTPHandler_Stateless(t *testing.T) {
	t.Parallel()

	toolsList := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	t.Run("stateless serves tools/list without a session", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewServer(newStatelessTestHandler(t, true))
		defer ts.Close()

		resp := postJSONRPC(t, ts.URL, toolsList, "")
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("stateful rejects unknown session ids", func(t *testing.T) {
		t.Parallel()

		// The load-balanced failure mode this feature exists to fix: a
		// session id minted by one replica is unknown to another and the
		// request is rejected in stateful mode.
		ts := httptest.NewServer(newStatelessTestHandler(t, false))
		defer ts.Close()

		resp := postJSONRPC(t, ts.URL, toolsList, "SESSIONFROMANOTHERREPLICA")
		defer resp.Body.Close()
		require.NotEqual(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("stateless ignores unknown session ids", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewServer(newStatelessTestHandler(t, true))
		defer ts.Close()

		// A session id minted by a different replica must not be rejected.
		resp := postJSONRPC(t, ts.URL, toolsList, "SESSIONFROMANOTHERREPLICA")
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})
}
