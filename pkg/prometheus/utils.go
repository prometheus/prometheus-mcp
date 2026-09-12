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
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/common/model"
)

const (
	// nowPrefix is the leading token of Grafana style relative time
	// expressions ("now", "now-1h").
	nowPrefix = "now"

	// nowExpressionErrorHelp names the accepted forms for error messages.
	nowExpressionErrorHelp = "expected now, now-<duration>, or now+<duration>"
)

var (
	// Values here are copied and un-exported from Prometheus codebase:
	// https://github.com/prometheus/prometheus/blob/main/web/api/v1/api.go#L884-L905

	// minTime is the default timestamp used for the start of optional time ranges.
	// Exposed to let downstream projects reference it.
	//
	// Historical note: This should just be time.Unix(math.MinInt64/1000, 0).UTC(),
	// but it was set to a higher value in the past due to a misunderstanding.
	// The value is still low enough for practical purposes, so we don't want
	// to change it now, avoiding confusion for importers of this variable.
	minTime = time.Unix(math.MinInt64/1000+62135596801, 0).UTC()

	// maxTime is the default timestamp used for the end of optional time ranges.
	// Exposed to let downstream projects to reference it.
	//
	// Historical note: This should just be time.Unix(math.MaxInt64/1000, 0).UTC(),
	// but it was set to a lower value in the past due to a misunderstanding.
	// The value is still high enough for practical purposes, so we don't want
	// to change it now, avoiding confusion for importers of this variable.
	maxTime = time.Unix(math.MaxInt64/1000-62135596801, 999999999).UTC()

	minTimeFormatted = minTime.Format(time.RFC3339Nano)
	maxTimeFormatted = maxTime.Format(time.RFC3339Nano)
)

// ParseTimestamp parses a timestamp from a string. The timestamp can be either
// a unix epoch timestamp or RFC33339 format.
//
// Copied from Prometheus codebase:
// https://github.com/prometheus/prometheus/blob/main/web/api/v1/api.go#L2082-L2103
func ParseTimestamp(s string) (time.Time, error) {
	if t, err := strconv.ParseFloat(s, 64); err == nil {
		s, ns := math.Modf(t)
		ns = math.Round(ns*1000) / 1000
		return time.Unix(int64(s), int64(ns*float64(time.Second))).UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}

	// Stdlib's time parser can only handle 4 digit years. As a workaround until
	// that is fixed we want to at least support our own boundary times.
	// Context: https://github.com/prometheus/client_golang/issues/614
	// Upstream issue: https://github.com/golang/go/issues/20555
	switch s {
	case minTimeFormatted:
		return minTime, nil
	case maxTimeFormatted:
		return maxTime, nil
	}
	return time.Time{}, fmt.Errorf("cannot parse %q to a valid timestamp", s)
}

// ParseTimestampOrDuration provides extended timestamp support to Prometheus'
// upstream timestamp handling. LLMs often use duration strings (eg, "1h",
// "5m", etc). Even when tool parameter descriptions are extended to be even
// more explicit about supported formats, I often observe LLMs attempt duration
// strings first, fail, observe the error, try 2 or 3 more times with different
// formats and possibly make shell calls to `date`/`python` for time handling,
// etc. Accepting duration strings from the get go avoids a lot of LLM
// confusion and failed/extra tool calls.
//
// The same reasoning applies to Grafana's relative time idiom: clients
// regularly send "now" or "now-1h" because that is what they type into
// Grafana, so those forms are accepted too.
func ParseTimestampOrDuration(s string) (time.Time, error) {
	s = strings.TrimSpace(s)

	// "now" expressions are handled first; nothing else this parser
	// accepts starts with "now".
	if strings.HasPrefix(strings.ToLower(s), nowPrefix) {
		return parseNowExpression(s)
	}

	if t, err := ParseTimestamp(s); err == nil {
		return t, nil
	}

	if dur, err := model.ParseDurationAllowNegative(s); err == nil {
		// Durations always represent a time in the past relative to now.
		// Both "5m" and "-5m" mean "5 minutes ago" -- we normalize
		// negative durations so that callers (typically LLMs) get
		// consistent behavior regardless of sign convention.
		return time.Now().Add(-time.Duration(dur).Abs()), nil
	}

	return time.Time{}, fmt.Errorf("cannot parse %q to a valid timestamp or duration", s)
}

// parseNowExpression resolves "now", "now-<duration>" and "now+<duration>"
// against the current time. Durations use Prometheus duration syntax, so their
// unit suffixes stay case sensitive even though the "now" token is not.
func parseNowExpression(s string) (time.Time, error) {
	now := time.Now()

	if strings.ToLower(s) == nowPrefix {
		return now, nil
	}

	rest := strings.TrimSpace(s[len(nowPrefix):])
	sign := rest[0]
	if sign != '-' && sign != '+' {
		return time.Time{}, fmt.Errorf("cannot parse %q: %s", s, nowExpressionErrorHelp)
	}

	// A sign on the duration itself (eg, "now--5m") is rejected by
	// model.ParseDuration, which only accepts unsigned durations.
	dur, err := model.ParseDuration(strings.TrimSpace(rest[1:]))
	if err != nil {
		return time.Time{}, fmt.Errorf("cannot parse %q: %s: %w", s, nowExpressionErrorHelp, err)
	}

	if sign == '-' {
		return now.Add(-time.Duration(dur)), nil
	}

	return now.Add(time.Duration(dur)), nil
}
