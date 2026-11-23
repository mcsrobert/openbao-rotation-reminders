package digest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mcsrobert/openbao-rotation-reminders/internal/reminder"
)

var base = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func finding(severity reminder.Severity, key string, offset time.Duration, reasons ...string) reminder.Finding {
	return reminder.Finding{Severity: severity, Time: base.Add(offset), Key: key, Reasons: reasons}
}

func TestBuildSingleFinding(t *testing.T) {
	tests := []struct {
		severity reminder.Severity
		title    string
		priority string
		tags     string
		header   string
	}{
		{reminder.SeverityExpired, "OpenBao: app/key expired", "urgent", "rotating_light,key", "EXPIRED"},
		{reminder.SeverityDue, "OpenBao: app/key due for rotation", "high", "warning,key", "DUE FOR ROTATION"},
		{reminder.SeverityUpcoming, "OpenBao: app/key expiring soon", "default", "key", "EXPIRING SOON"},
		{reminder.SeverityInvalid, "OpenBao: app/key has invalid rotation metadata", "default", "key", "INVALID METADATA"},
	}
	for _, test := range tests {
		got := Build([]reminder.Finding{finding(test.severity, "app/key", 0, "reason")})
		want := Digest{Title: test.title, Priority: test.priority, Tags: test.tags, Body: test.header + ":\n- app/key: reason"}
		if got != want {
			t.Errorf("Build(%v) = %+v, want %+v", test.severity, got, want)
		}
	}
}

func TestBuildTitles(t *testing.T) {
	tests := []struct {
		name       string
		severities []reminder.Severity
		want       string
	}{
		{"several expired", []reminder.Severity{reminder.SeverityExpired, reminder.SeverityExpired}, "OpenBao: 2 secrets expired"},
		{"several due", []reminder.Severity{reminder.SeverityDue, reminder.SeverityDue, reminder.SeverityDue}, "OpenBao: 3 secrets due for rotation"},
		{"several upcoming", []reminder.Severity{reminder.SeverityUpcoming, reminder.SeverityUpcoming}, "OpenBao: 2 secrets expiring soon"},
		{"several invalid", []reminder.Severity{reminder.SeverityInvalid, reminder.SeverityInvalid}, "OpenBao: 2 secrets have invalid rotation metadata"},
		{"expired and due", []reminder.Severity{reminder.SeverityDue, reminder.SeverityExpired, reminder.SeverityDue}, "OpenBao: 1 expired, 2 due"},
		{"upcoming and invalid", []reminder.Severity{reminder.SeverityInvalid, reminder.SeverityUpcoming, reminder.SeverityUpcoming}, "OpenBao: 2 expiring soon, 1 invalid"},
		{
			"all severities",
			[]reminder.Severity{reminder.SeverityInvalid, reminder.SeverityUpcoming, reminder.SeverityDue, reminder.SeverityExpired},
			"OpenBao: 1 expired, 1 due, 1 expiring soon, 1 invalid",
		},
	}
	for _, test := range tests {
		var findings []reminder.Finding
		for i, severity := range test.severities {
			findings = append(findings, finding(severity, fmt.Sprintf("key-%d", i), 0, "reason"))
		}
		if got := Build(findings).Title; got != test.want {
			t.Errorf("%s: title = %q, want %q", test.name, got, test.want)
		}
	}
}

func TestBuildTitlesFitNtfysLimit(t *testing.T) {
	fits := strings.Repeat("a", 1024-len("OpenBao:  has invalid rotation metadata"))
	if got := Build([]reminder.Finding{finding(reminder.SeverityInvalid, fits, 0, "reason")}).Title; got != "OpenBao: "+fits+" has invalid rotation metadata" || len(got) != 1024 {
		t.Errorf("title = %q", got)
	}
	long := fits + "a"
	got := Build([]reminder.Finding{finding(reminder.SeverityInvalid, long, 0, "reason")})
	if got.Title != "OpenBao: 1 secret has invalid rotation metadata" {
		t.Errorf("title = %q", got.Title)
	}
	if !strings.Contains(got.Body, "- "+long+": reason") {
		t.Errorf("body does not name the secret:\n%s", got.Body)
	}
}

func TestBuildPriorityFollowsTheMostUrgentFinding(t *testing.T) {
	got := Build([]reminder.Finding{
		finding(reminder.SeverityInvalid, "a", 0, "reason"),
		finding(reminder.SeverityDue, "b", 0, "reason"),
	})
	if got.Priority != "high" || got.Tags != "warning,key" {
		t.Errorf("priority = %q, tags = %q", got.Priority, got.Tags)
	}
}

func TestBuildBody(t *testing.T) {
	invalid := finding(reminder.SeverityInvalid, "app/typo", 0, `metadata error: unknown metadata key "rotation_intervall"`)
	expired := finding(reminder.SeverityExpired, "github/token", -24*time.Hour, "expired 2026-05-31 (1d ago)", "due 2026-05-01 (31d overdue, interval 30d)")
	expired.Note = "Regenerate the PAT in GitHub"
	got := Build([]reminder.Finding{
		finding(reminder.SeverityDue, "z/late", -2*time.Hour, "due b"),
		invalid,
		finding(reminder.SeverityDue, "b/second", -time.Hour, "due c"),
		expired,
		finding(reminder.SeverityDue, "a/second", -time.Hour, "due a"),
	})
	want := `EXPIRED:
- github/token: expired 2026-05-31 (1d ago); due 2026-05-01 (31d overdue, interval 30d)
  Note: Regenerate the PAT in GitHub

DUE FOR ROTATION:
- z/late: due b
- a/second: due a
- b/second: due c

INVALID METADATA:
- app/typo: metadata error: unknown metadata key "rotation_intervall"`
	if got.Body != want {
		t.Errorf("body:\n%s\nwant:\n%s", got.Body, want)
	}
	if got.Title != "OpenBao: 1 expired, 3 due, 1 invalid" {
		t.Errorf("title = %q", got.Title)
	}
}

func TestBuildDoesNotReorderItsInput(t *testing.T) {
	findings := []reminder.Finding{
		finding(reminder.SeverityInvalid, "b", 0, "reason"),
		finding(reminder.SeverityExpired, "a", 0, "reason"),
	}
	Build(findings)
	if findings[0].Key != "b" {
		t.Error("Build sorted the caller's slice")
	}
}

func TestTruncate(t *testing.T) {
	short := strings.Repeat("x", MaxBodyBytes)
	if got := Truncate(short); got != short {
		t.Error("a body of exactly 4096 bytes was truncated")
	}

	var lines []string
	for i := range 200 {
		lines = append(lines, fmt.Sprintf("- secret/%03d: expired 2026-05-01 (31d ago) é", i))
	}
	body := strings.Join(lines, "\n")
	got := Truncate(body)

	const marker = "\n... (truncated; check pod logs for full list)"
	kept, found := strings.CutSuffix(got, marker)
	if !found {
		t.Fatalf("missing truncation marker:\n%s", got)
	}
	if !strings.HasPrefix(body, kept+"\n") {
		t.Fatal("the truncated body is not a prefix of whole lines")
	}
	if size := len(kept) + 1; size > 3900 {
		t.Errorf("kept %d bytes including the last newline, want at most 3900", size)
	}
	next := lines[strings.Count(kept, "\n")+1]
	if len(kept)+1+len(next)+1 <= 3900 {
		t.Error("another whole line would have fit")
	}
	if len(got) > MaxBodyBytes {
		t.Errorf("truncated body is %d bytes", len(got))
	}
}

func TestTruncateCountsBytes(t *testing.T) {
	line := strings.Repeat("é", 1000)
	body := line + "\n" + line + "\n" + line
	if len([]rune(body)) > MaxBodyBytes {
		t.Fatal("the test body should be short in runes")
	}
	got := Truncate(body)
	if got != line+"\n... (truncated; check pod logs for full list)" {
		t.Errorf("got %d bytes: %q", len(got), got[len(got)-60:])
	}
}

func TestTruncateLongFirstLine(t *testing.T) {
	got := Truncate(strings.Repeat("x", 5000))
	if got != "\n... (truncated; check pod logs for full list)" {
		t.Errorf("got %q", got)
	}
}
