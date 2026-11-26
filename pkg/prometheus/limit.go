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
	"errors"
	"fmt"
	"io"
	"net/http"
)

// NewResponseSizeLimitRoundTripper returns a RoundTripper that fails reading
// a response body once it grows past limit bytes. A limit of zero or less
// returns rt unchanged.
func NewResponseSizeLimitRoundTripper(limit int64, rt http.RoundTripper) http.RoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}
	if limit <= 0 {
		return rt
	}
	return &responseSizeLimitRoundTripper{limit: limit, rt: rt}
}

type responseSizeLimitRoundTripper struct {
	limit int64
	rt    http.RoundTripper
}

func (l *responseSizeLimitRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := l.rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = limitedBody{http.MaxBytesReader(nil, resp.Body, l.limit)}
	return resp, nil
}

// limitedBody replaces the MaxBytesError text, which names a request body.
type limitedBody struct {
	io.ReadCloser
}

func (b limitedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		err = fmt.Errorf("response body exceeds the limit of %d bytes: request less data", maxBytesErr.Limit)
	}
	return n, err
}
