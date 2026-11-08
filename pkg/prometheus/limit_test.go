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

package prometheus

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const vectorResponse = `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"up"},"value":[1700000000,"1"]}]}}`

func TestResponseSizeLimitRoundTripper(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, vectorResponse)
	}))
	t.Cleanup(srv.Close)

	tests := []struct {
		name    string
		limit   int64
		wantErr bool
	}{
		{name: "body below limit", limit: int64(len(vectorResponse)) + 1},
		{name: "body exactly at limit", limit: int64(len(vectorResponse))},
		{name: "body above limit", limit: int64(len(vectorResponse)) - 1, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewAPIClient(srv.URL, NewResponseSizeLimitRoundTripper(tc.limit, http.DefaultTransport))
			require.NoError(t, err)

			_, _, _, err = client.Query(context.Background(), "up", time.Unix(1700000000, 0))
			if tc.wantErr {
				require.ErrorContains(t, err, "exceeds the limit")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestNewResponseSizeLimitRoundTripperDisabled(t *testing.T) {
	require.Same(t, http.DefaultTransport, NewResponseSizeLimitRoundTripper(0, nil))
	require.Same(t, http.DefaultTransport, NewResponseSizeLimitRoundTripper(-1, http.DefaultTransport))
}
