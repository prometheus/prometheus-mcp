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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseTimestamp(t *testing.T) {
	testCases := []struct {
		name          string
		ts            string
		expectedTime  time.Time
		expectedError bool
	}{
		{
			name:          "Unix Timestamp",
			ts:            "1136214245",
			expectedTime:  time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC),
			expectedError: false,
		},
		{
			name:          "Unix Timestamp with Fractional Seconds",
			ts:            "1136214245.123",
			expectedTime:  time.Date(2006, 1, 2, 15, 4, 5, 123000000, time.UTC),
			expectedError: false,
		},
		{
			name:          "RFC3339 Timestamp",
			ts:            "2006-01-02T15:04:05Z",
			expectedTime:  time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC),
			expectedError: false,
		},
		{
			name:          "RFC3339Nano Timestamp",
			ts:            "2006-01-02T15:04:05.123Z",
			expectedTime:  time.Date(2006, 1, 2, 15, 4, 5, 123000000, time.UTC),
			expectedError: false,
		},
		{
			name:          "Invalid Timestamp",
			ts:            "invalid",
			expectedTime:  time.Time{},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTimestamp(tc.ts)
			switch tc.expectedError {
			case true:
				require.Error(t, err)
			default:
				require.NoError(t, err)
				require.True(t, tc.expectedTime.Equal(got), "expected times to be equal", "expected", tc.expectedTime, "got", got)
			}
		})
	}
}

func TestParseTimestampOrDuration(t *testing.T) {
	// Verify timestamp input falls through to ParseTimestamp.
	t.Run("Timestamp fallthrough", func(t *testing.T) {
		got, err := ParseTimestampOrDuration("1136214245")
		require.NoError(t, err)
		expected := time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC)
		require.True(t, expected.Equal(got), "expected times to be equal", "expected", expected, "got", got)
	})

	// Relative inputs resolve against the current time. Offsets are signed:
	// negative is in the past, positive in the future, zero is "now" itself.
	relativeCases := []struct {
		name   string
		input  string
		offset time.Duration
	}{
		{name: "5 minutes", input: "5m", offset: -5 * time.Minute},
		{name: "1 hour", input: "1h", offset: -1 * time.Hour},
		{name: "1 hour 30 minutes", input: "1h30m", offset: -(1*time.Hour + 30*time.Minute)},
		{name: "30 seconds", input: "30s", offset: -30 * time.Second},
		{name: "1 day", input: "1d", offset: -24 * time.Hour},
		{name: "1 year", input: "1y", offset: -365 * 24 * time.Hour},
		// A leading sign is tolerated and ignored: "-5m" is still 5 minutes ago.
		{name: "Negative 5 minutes", input: "-5m", offset: -5 * time.Minute},
		{name: "Bare now", input: "now", offset: 0},
		{name: "Uppercase now", input: "NOW", offset: 0},
		{name: "Now minus 5 minutes", input: "now-5m", offset: -5 * time.Minute},
		{name: "Now plus 5 minutes", input: "now+5m", offset: 5 * time.Minute},
		{name: "Now minus 5 minutes with spaces", input: " now - 5m ", offset: -5 * time.Minute},
	}

	for _, tc := range relativeCases {
		t.Run(tc.name, func(t *testing.T) {
			before := time.Now()
			got, err := ParseTimestampOrDuration(tc.input)
			after := time.Now()

			require.NoError(t, err)
			// Bound the result by the clock readings around the call so
			// time elapsed during the test cannot fail it.
			require.WithinRange(t, got, before.Add(tc.offset), after.Add(tc.offset))
		})
	}

	// Malformed "now" expressions report the accepted forms rather than
	// falling through to the generic timestamp/duration error.
	nowErrorCases := []struct {
		name  string
		input string
	}{
		{name: "Now with trailing sign only", input: "now-"},
		{name: "Now with trailing garbage", input: "nowish"},
		{name: "Now with invalid duration", input: "now-1x"},
		{name: "Now with signed duration", input: "now--5m"},
	}

	for _, tc := range nowErrorCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseTimestampOrDuration(tc.input)
			require.ErrorContains(t, err, nowExpressionErrorHelp)
		})
	}

	// Test invalid input.
	t.Run("Invalid input", func(t *testing.T) {
		_, err := ParseTimestampOrDuration("not-a-timestamp-or-duration")
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot parse")
	})
}
