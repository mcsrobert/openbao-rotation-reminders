package reminder

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	durationPattern = regexp.MustCompile(`^([0-9]+)([hdw])$`)
	datePattern     = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	durationUnits   = map[string]time.Duration{
		"h": time.Hour,
		"d": 24 * time.Hour,
		"w": 7 * 24 * time.Hour,
	}
)

// ParseDuration parses a whole number followed by h, d or w, such as 80d,
// 12w or 24h. Zero is only accepted with allowZero.
func ParseDuration(value string, allowZero bool) (time.Duration, error) {
	match := durationPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return 0, fmt.Errorf("invalid duration %q (use e.g. 80d, 12w, 24h)", value)
	}
	unit := durationUnits[match[2]]
	amount, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil || amount > math.MaxInt64/int64(unit) {
		return 0, fmt.Errorf("duration %q is too large", value)
	}
	if amount == 0 && !allowZero {
		return 0, fmt.Errorf("duration %q must be positive", value)
	}
	return time.Duration(amount) * unit, nil
}

// ParseTimestamp parses an RFC 3339 timestamp, with optional fractional
// seconds and a required time zone offset, and returns it in UTC.
func ParseTimestamp(value string) (time.Time, error) {
	text := strings.TrimSpace(value)
	if len(text) > 10 && (text[10] == 't' || text[10] == ' ') {
		text = text[:10] + "T" + text[11:]
	}
	if strings.HasSuffix(text, "z") {
		text = strings.TrimSuffix(text, "z") + "Z"
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err == nil {
		return parsed.UTC(), nil
	}
	if _, err := time.Parse("2006-01-02T15:04:05.999999999", text); err == nil {
		return time.Time{}, fmt.Errorf("timestamp %q must include a timezone (e.g. Z or +00:00)", value)
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %q (use RFC3339, e.g. 2027-03-01T00:00:00Z)", value)
}

// ParseExpiresAt parses a date, YYYY-MM-DD at midnight UTC, or an RFC 3339
// timestamp.
func ParseExpiresAt(value string) (time.Time, error) {
	text := strings.TrimSpace(value)
	if datePattern.MatchString(text) {
		parsed, err := time.Parse(time.DateOnly, text)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid date %q", value)
		}
		return parsed, nil
	}
	if strings.ContainsAny(text, "Tt ") {
		return ParseTimestamp(text)
	}
	return time.Time{}, fmt.Errorf("invalid expires_at %q (use YYYY-MM-DD or RFC3339)", value)
}

// FormatDelta formats a duration as whole days, hours or minutes, rounding
// down and showing at least 1m.
func FormatDelta(delta time.Duration) string {
	seconds := int64(max(delta, 0) / time.Second)
	days, rest := seconds/86400, seconds%86400
	if days >= 1 {
		return fmt.Sprintf("%dd", days)
	}
	if hours := rest / 3600; hours >= 1 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dm", max(1, rest/60))
}

func formatDate(t time.Time) string {
	return t.UTC().Format(time.DateOnly)
}
