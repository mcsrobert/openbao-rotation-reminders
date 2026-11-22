package reminder

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/mcsrobert/openbao-rotation-reminders/internal/openbao"
)

var testNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

const testWarn = 14 * 24 * time.Hour

// secret returns metadata JSON for a secret whose current version 1 was
// created on 2026-03-01 at noon, 92 days before testNow.
func secret(custom string) string {
	return withVersion(custom, `{"created_time":"2026-03-01T12:00:00Z","deletion_time":"","destroyed":false}`)
}

func withVersion(custom, version string) string {
	return fmt.Sprintf(`{"current_version":1,"versions":{"1":%s},"custom_metadata":%s}`, version, custom)
}

func date(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
		tracked  bool
		want     *Finding
	}{
		{
			name:     "no custom metadata",
			metadata: secret(`null`),
		},
		{
			name:     "empty custom metadata",
			metadata: secret(`{}`),
		},
		{
			name:     "unrelated custom metadata",
			metadata: secret(`{"owner":"me","rotation":"yes"}`),
		},
		{
			name:     "null reminder values are ignored",
			metadata: secret(`{"rotation_interval":null,"owner":"me"}`),
		},
		{
			name:     "custom metadata is not an object",
			metadata: secret(`"rotation_interval=90d"`),
			tracked:  true,
			want:     &Finding{Severity: SeverityInvalid, Time: testNow, Reasons: []string{"metadata error: custom_metadata is not an object"}},
		},
		{
			name:     "interval not due",
			metadata: secret(`{"rotation_interval":"180d"}`),
			tracked:  true,
		},
		{
			name:     "interval due",
			metadata: secret(`{"rotation_interval":"90d"}`),
			tracked:  true,
			want:     &Finding{Severity: SeverityDue, Time: date("2026-05-30T12:00:00Z"), Reasons: []string{"due 2026-05-30 (2d overdue, interval 90d)"}},
		},
		{
			name:     "interval due exactly now",
			metadata: secret(`{"rotation_interval":" 92d "}`),
			tracked:  true,
			want:     &Finding{Severity: SeverityDue, Time: testNow, Reasons: []string{"due 2026-06-01 (1m overdue, interval 92d)"}},
		},
		{
			name:     "interval in hours and weeks",
			metadata: secret(`{"rotation_interval":"13w"}`),
			tracked:  true,
			want:     &Finding{Severity: SeverityDue, Time: date("2026-05-31T12:00:00Z"), Reasons: []string{"due 2026-05-31 (1d overdue, interval 13w)"}},
		},
		{
			name:     "interval within warn_before",
			metadata: secret(`{"rotation_interval":"93d","rotation_warn_before":"7d"}`),
			tracked:  true,
			want:     &Finding{Severity: SeverityUpcoming, Time: date("2026-06-02T12:00:00Z"), Reasons: []string{"due 2026-06-02 (in 1d, interval 93d)"}},
		},
		{
			name:     "interval before warn_before",
			metadata: secret(`{"rotation_interval":"100d","rotation_warn_before":"7d"}`),
			tracked:  true,
		},
		{
			name:     "interval does not warn by default",
			metadata: secret(`{"rotation_interval":"93d"}`),
			tracked:  true,
		},
		{
			name:     "nanosecond created time",
			metadata: withVersion(`{"rotation_interval":"1h"}`, `{"created_time":"2026-06-01T10:59:59.999999999Z","deletion_time":"","destroyed":false}`),
			tracked:  true,
			want:     &Finding{Severity: SeverityDue, Time: date("2026-06-01T11:59:59.999999999Z"), Reasons: []string{"due 2026-06-01 (1m overdue, interval 1h)"}},
		},
		{
			name:     "expired",
			metadata: secret(`{"expires_at":"2026-05-01"}`),
			tracked:  true,
			want:     &Finding{Severity: SeverityExpired, Time: date("2026-05-01T00:00:00Z"), Reasons: []string{"expired 2026-05-01 (31d ago)"}},
		},
		{
			name:     "expires exactly now",
			metadata: secret(`{"expires_at":"2026-06-01T12:00:00Z"}`),
			tracked:  true,
			want:     &Finding{Severity: SeverityExpired, Time: testNow, Reasons: []string{"expired 2026-06-01 (1m ago)"}},
		},
		{
			name:     "expired with an offset",
			metadata: secret(`{"expires_at":"2026-06-01T13:00:00+02:00"}`),
			tracked:  true,
			want:     &Finding{Severity: SeverityExpired, Time: date("2026-06-01T11:00:00Z"), Reasons: []string{"expired 2026-06-01 (1h ago)"}},
		},
		{
			name:     "expires within the default warning",
			metadata: secret(`{"expires_at":"2026-06-10"}`),
			tracked:  true,
			want:     &Finding{Severity: SeverityUpcoming, Time: date("2026-06-10T00:00:00Z"), Reasons: []string{"expires 2026-06-10 (in 8d)"}},
		},
		{
			name:     "expiry date is shown in UTC",
			metadata: secret(`{"expires_at":"2026-06-02T01:00:00+02:00"}`),
			tracked:  true,
			want:     &Finding{Severity: SeverityUpcoming, Time: date("2026-06-01T23:00:00Z"), Reasons: []string{"expires 2026-06-01 (in 11h)"}},
		},
		{
			name:     "expires after the default warning",
			metadata: secret(`{"expires_at":"2026-06-20"}`),
			tracked:  true,
		},
		{
			name:     "expires within a longer warn_before",
			metadata: secret(`{"expires_at":"2026-06-20","rotation_warn_before":"30d"}`),
			tracked:  true,
			want:     &Finding{Severity: SeverityUpcoming, Time: date("2026-06-20T00:00:00Z"), Reasons: []string{"expires 2026-06-20 (in 18d)"}},
		},
		{
			name:     "zero warn_before disables the expiry warning",
			metadata: secret(`{"expires_at":"2026-06-02","rotation_warn_before":"0d"}`),
			tracked:  true,
		},
		{
			name:     "expired and due",
			metadata: secret(`{"expires_at":"2026-05-01","rotation_interval":"90d"}`),
			tracked:  true,
			want: &Finding{Severity: SeverityExpired, Time: date("2026-05-01T00:00:00Z"), Reasons: []string{
				"expired 2026-05-01 (31d ago)",
				"due 2026-05-30 (2d overdue, interval 90d)",
			}},
		},
		{
			name:     "due and expiring soon",
			metadata: secret(`{"expires_at":"2026-06-05","rotation_interval":"90d"}`),
			tracked:  true,
			want: &Finding{Severity: SeverityDue, Time: date("2026-05-30T12:00:00Z"), Reasons: []string{
				"expires 2026-06-05 (in 3d)",
				"due 2026-05-30 (2d overdue, interval 90d)",
			}},
		},
		{
			name:     "note is collapsed",
			metadata: secret(`{"rotation_interval":"90d","rotation_note":"  Regenerate in\n the  admin\tconsole "}`),
			tracked:  true,
			want:     &Finding{Severity: SeverityDue, Time: date("2026-05-30T12:00:00Z"), Reasons: []string{"due 2026-05-30 (2d overdue, interval 90d)"}, Note: "Regenerate in the admin console"},
		},
		{
			name:     "unknown rotation key",
			metadata: secret(`{"rotation_interval":"180d","rotation_intervall":"30d"}`),
			tracked:  true,
			want:     &Finding{Severity: SeverityInvalid, Time: testNow, Reasons: []string{`metadata error: unknown metadata key "rotation_intervall"`}},
		},
		{
			name:     "unknown keys are matched without case",
			metadata: secret(`{"Expires_At":"2020-01-01","EXPIRES":"2020","expiry":"2020","Rotation_Interval":"1d"}`),
			tracked:  true,
			want: &Finding{Severity: SeverityInvalid, Time: testNow, Reasons: []string{
				`metadata error: unknown metadata key "EXPIRES"`,
				`metadata error: unknown metadata key "Expires_At"`,
				`metadata error: unknown metadata key "Rotation_Interval"`,
			}},
		},
		{
			name:     "trigger with an unknown key",
			metadata: secret(`{"expires_at":"2026-05-01","rotation_typo":"x"}`),
			tracked:  true,
			want: &Finding{Severity: SeverityExpired, Time: date("2026-05-01T00:00:00Z"), Reasons: []string{
				"expired 2026-05-01 (31d ago)",
				`metadata error: unknown metadata key "rotation_typo"`,
			}},
		},
		{
			name:     "note without a reminder",
			metadata: secret(`{"rotation_note":"Rotate   it\n now"}`),
			tracked:  true,
			want: &Finding{Severity: SeverityInvalid, Time: testNow, Note: "Rotate it now", Reasons: []string{
				"metadata error: rotation_warn_before/rotation_note set without rotation_interval or expires_at",
			}},
		},
		{
			name:     "warn_before without a reminder",
			metadata: secret(`{"rotation_warn_before":"7d"}`),
			tracked:  true,
			want: &Finding{Severity: SeverityInvalid, Time: testNow, Reasons: []string{
				"metadata error: rotation_warn_before/rotation_note set without rotation_interval or expires_at",
			}},
		},
		{
			name:     "invalid interval",
			metadata: secret(`{"rotation_interval":"90"}`),
			tracked:  true,
			want: &Finding{Severity: SeverityInvalid, Time: testNow, Reasons: []string{
				`metadata error: rotation_interval: invalid duration "90" (use e.g. 80d, 12w, 24h)`,
			}},
		},
		{
			name:     "zero interval",
			metadata: secret(`{"rotation_interval":"0d"}`),
			tracked:  true,
			want:     &Finding{Severity: SeverityInvalid, Time: testNow, Reasons: []string{`metadata error: rotation_interval: duration "0d" must be positive`}},
		},
		{
			name:     "non-string value",
			metadata: secret(`{"rotation_interval":90}`),
			tracked:  true,
			want: &Finding{Severity: SeverityInvalid, Time: testNow, Reasons: []string{
				`metadata error: rotation_interval: invalid duration "90" (use e.g. 80d, 12w, 24h)`,
			}},
		},
		{
			name:     "invalid expires_at",
			metadata: secret(`{"expires_at":"soon"}`),
			tracked:  true,
			want: &Finding{Severity: SeverityInvalid, Time: testNow, Reasons: []string{
				`metadata error: expires_at: invalid expires_at "soon" (use YYYY-MM-DD or RFC3339)`,
			}},
		},
		{
			name:     "expires_at without a time zone",
			metadata: secret(`{"expires_at":"2027-01-01T00:00:00"}`),
			tracked:  true,
			want: &Finding{Severity: SeverityInvalid, Time: testNow, Reasons: []string{
				`metadata error: expires_at: timestamp "2027-01-01T00:00:00" must include a timezone (e.g. Z or +00:00)`,
			}},
		},
		{
			name:     "invalid warn_before falls back to the default",
			metadata: secret(`{"expires_at":"2026-06-10","rotation_warn_before":"two weeks"}`),
			tracked:  true,
			want: &Finding{Severity: SeverityUpcoming, Time: date("2026-06-10T00:00:00Z"), Reasons: []string{
				"expires 2026-06-10 (in 8d)",
				`metadata error: rotation_warn_before: invalid duration "two weeks" (use e.g. 80d, 12w, 24h)`,
			}},
		},
		{
			name:     "soft-deleted",
			metadata: withVersion(`{"expires_at":"2020-01-01","rotation_typo":"x"}`, `{"created_time":"2026-03-01T12:00:00Z","deletion_time":"2026-05-01T00:00:00Z","destroyed":false}`),
		},
		{
			name:     "deleted exactly now",
			metadata: withVersion(`{"expires_at":"2020-01-01"}`, `{"created_time":"2026-03-01T12:00:00Z","deletion_time":"2026-06-01T12:00:00Z","destroyed":false}`),
		},
		{
			name:     "scheduled deletion is still active",
			metadata: withVersion(`{"expires_at":"2026-05-01"}`, `{"created_time":"2026-03-01T12:00:00Z","deletion_time":"2026-06-01T13:00:00Z","destroyed":false}`),
			tracked:  true,
			want:     &Finding{Severity: SeverityExpired, Time: date("2026-05-01T00:00:00Z"), Reasons: []string{"expired 2026-05-01 (31d ago)"}},
		},
		{
			name:     "destroyed",
			metadata: withVersion(`{"expires_at":"2020-01-01"}`, `{"created_time":"2026-03-01T12:00:00Z","deletion_time":"","destroyed":true}`),
		},
		{
			name:     "no versions",
			metadata: `{"current_version":0,"versions":{},"custom_metadata":{"expires_at":"2020-01-01","rotation_typo":"x"}}`,
		},
		{
			name:     "missing current version entry",
			metadata: `{"current_version":2,"versions":{"1":{"created_time":"2026-03-01T12:00:00Z"}},"custom_metadata":{"rotation_interval":"90d"}}`,
			tracked:  true,
			want:     &Finding{Severity: SeverityInvalid, Time: testNow, Reasons: []string{"metadata error: current version 2 has no metadata entry"}},
		},
		{
			name:     "missing current version entry with a trigger",
			metadata: `{"current_version":2,"versions":{"1":null},"custom_metadata":{"rotation_interval":"90d","expires_at":"2026-05-01"}}`,
			tracked:  true,
			want: &Finding{Severity: SeverityExpired, Time: date("2026-05-01T00:00:00Z"), Reasons: []string{
				"expired 2026-05-01 (31d ago)",
				"metadata error: current version 2 has no metadata entry",
			}},
		},
		{
			name:     "missing created time",
			metadata: withVersion(`{"rotation_interval":"90d"}`, `{"deletion_time":"","destroyed":false}`),
			tracked:  true,
			want:     &Finding{Severity: SeverityInvalid, Time: testNow, Reasons: []string{"metadata error: current version 1 missing created_time"}},
		},
		{
			name:     "invalid created time",
			metadata: withVersion(`{"rotation_interval":"90d"}`, `{"created_time":"yesterday"}`),
			tracked:  true,
			want: &Finding{Severity: SeverityInvalid, Time: testNow, Reasons: []string{
				`metadata error: current version 1 created_time: invalid timestamp "yesterday" (use RFC3339, e.g. 2027-03-01T00:00:00Z)`,
			}},
		},
		{
			name:     "missing current version",
			metadata: `{"versions":{},"custom_metadata":{"rotation_interval":"90d"}}`,
			tracked:  true,
			want:     &Finding{Severity: SeverityInvalid, Time: testNow, Reasons: []string{"metadata error: metadata has no current_version"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var metadata openbao.Metadata
			if err := json.Unmarshal([]byte(test.metadata), &metadata); err != nil {
				t.Fatal(err)
			}
			tracked, got := Evaluate("app/key", &metadata, testNow, testWarn)
			if tracked != test.tracked {
				t.Errorf("tracked = %v, want %v", tracked, test.tracked)
			}
			if test.want != nil {
				test.want.Key = "app/key"
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("finding = %+v, want %+v", got, test.want)
			}
		})
	}
}
