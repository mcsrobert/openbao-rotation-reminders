package reminder

import (
	"strings"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		value     string
		allowZero bool
		want      time.Duration
		err       string
	}{
		{value: "24h", want: 24 * time.Hour},
		{value: "80d", want: 80 * 24 * time.Hour},
		{value: "12w", want: 12 * 7 * 24 * time.Hour},
		{value: " 7d\n", want: 7 * 24 * time.Hour},
		{value: "0d", allowZero: true, want: 0},
		{value: "0h", allowZero: true, want: 0},
		{value: "0d", err: `duration "0d" must be positive`},
		{value: "", err: `invalid duration "" (use e.g. 80d, 12w, 24h)`},
		{value: "90", err: "invalid duration"},
		{value: "90m", err: "invalid duration"},
		{value: "90D", err: "invalid duration"},
		{value: "-1d", err: "invalid duration"},
		{value: "1.5d", err: "invalid duration"},
		{value: "1d2h", err: "invalid duration"},
		{value: "99999999999w", err: `duration "99999999999w" is too large`},
		{value: "99999999999999999999h", err: "is too large"},
	}
	for _, test := range tests {
		got, err := ParseDuration(test.value, test.allowZero)
		if test.err != "" {
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Errorf("ParseDuration(%q) error = %v, want %q", test.value, err, test.err)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Errorf("ParseDuration(%q) = %v, %v, want %v", test.value, got, err, test.want)
		}
	}
}

func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		value string
		want  time.Time
		err   string
	}{
		{value: "2027-03-01T00:00:00Z", want: time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)},
		{value: "2026-01-02T03:04:05.123456789Z", want: time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)},
		{value: "2026-01-02T03:04:05.123456Z", want: time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)},
		{value: "2027-03-01T02:00:00+02:00", want: time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)},
		{value: "2027-03-01t00:00:00z", want: time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)},
		{value: " 2027-03-01 00:00:00-01:00 ", want: time.Date(2027, 3, 1, 1, 0, 0, 0, time.UTC)},
		{value: "2027-03-01T00:00:00", err: `timestamp "2027-03-01T00:00:00" must include a timezone (e.g. Z or +00:00)`},
		{value: "2027-03-01T00:00:00.5", err: "must include a timezone"},
		{value: "2027-03-01", err: `invalid timestamp "2027-03-01"`},
		{value: "2027-02-30T00:00:00Z", err: "invalid timestamp"},
		{value: "2027-03-01T00:00:00+23:59", want: time.Date(2027, 2, 28, 0, 1, 0, 0, time.UTC)},
		{value: "2099-03-01T12:00:00+00:60", err: `invalid timestamp "2099-03-01T12:00:00+00:60"`},
		{value: "2099-03-01T12:00:00-24:00", err: "invalid timestamp"},
		{value: "tomorrow", err: "invalid timestamp"},
	}
	for _, test := range tests {
		got, err := ParseTimestamp(test.value)
		if test.err != "" {
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Errorf("ParseTimestamp(%q) error = %v, want %q", test.value, err, test.err)
			}
			continue
		}
		if err != nil || !got.Equal(test.want) || got.Location() != time.UTC {
			t.Errorf("ParseTimestamp(%q) = %v, %v, want %v", test.value, got, err, test.want)
		}
	}
}

func TestParseExpiresAt(t *testing.T) {
	tests := []struct {
		value string
		want  time.Time
		err   string
	}{
		{value: "2027-03-01", want: time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)},
		{value: " 2027-03-01 ", want: time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)},
		{value: "2027-03-01T12:30:00+01:00", want: time.Date(2027, 3, 1, 11, 30, 0, 0, time.UTC)},
		{value: "2027-03-01T12:30:00.000000001Z", want: time.Date(2027, 3, 1, 12, 30, 0, 1, time.UTC)},
		{value: "2027-02-30", err: `invalid date "2027-02-30"`},
		{value: "2027-3-1", err: `invalid expires_at "2027-3-1" (use YYYY-MM-DD or RFC3339)`},
		{value: "01/03/2027", err: "invalid expires_at"},
		{value: "2027-03-01T12:30:00", err: "must include a timezone"},
	}
	for _, test := range tests {
		got, err := ParseExpiresAt(test.value)
		if test.err != "" {
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Errorf("ParseExpiresAt(%q) error = %v, want %q", test.value, err, test.err)
			}
			continue
		}
		if err != nil || !got.Equal(test.want) {
			t.Errorf("ParseExpiresAt(%q) = %v, %v, want %v", test.value, got, err, test.want)
		}
	}
}

func TestFormatDelta(t *testing.T) {
	tests := []struct {
		delta time.Duration
		want  string
	}{
		{-time.Hour, "1m"},
		{0, "1m"},
		{59 * time.Second, "1m"},
		{2*time.Minute + 59*time.Second, "2m"},
		{59 * time.Minute, "59m"},
		{time.Hour, "1h"},
		{23*time.Hour + 59*time.Minute, "23h"},
		{24 * time.Hour, "1d"},
		{47 * time.Hour, "1d"},
		{400 * 24 * time.Hour, "400d"},
	}
	for _, test := range tests {
		if got := FormatDelta(test.delta); got != test.want {
			t.Errorf("FormatDelta(%v) = %q, want %q", test.delta, got, test.want)
		}
	}
}
