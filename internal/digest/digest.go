// Package digest formats findings as one notification.
package digest

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/mcsrobert/openbao-rotation-reminders/internal/reminder"
)

const (
	// MaxBodyBytes is the largest body ntfy delivers as a message rather than
	// an attachment.
	MaxBodyBytes       = 4096
	truncatedBodyBytes = 3900
	truncationMarker   = "\n... (truncated; check pod logs for full list)"
)

type style struct {
	header   string
	short    string
	singular string
	plural   string
	priority string
	tags     string
}

var styles = map[reminder.Severity]style{
	reminder.SeverityExpired:  {"EXPIRED", "expired", "expired", "expired", "urgent", "rotating_light,key"},
	reminder.SeverityDue:      {"DUE FOR ROTATION", "due", "due for rotation", "due for rotation", "high", "warning,key"},
	reminder.SeverityUpcoming: {"EXPIRING SOON", "expiring soon", "expiring soon", "expiring soon", "default", "key"},
	reminder.SeverityInvalid:  {"INVALID METADATA", "invalid", "has invalid rotation metadata", "have invalid rotation metadata", "default", "key"},
}

// Digest is the notification for a set of findings.
type Digest struct {
	Title    string
	Priority string
	Tags     string
	// Body lists every finding. Use Truncate before sending it to ntfy.
	Body string
}

// Build returns the digest for findings, which must not be empty. Findings
// are grouped by severity and ordered by their earliest deadline, then key.
// The priority and tags follow the most urgent finding.
func Build(findings []reminder.Finding) Digest {
	ordered := slices.Clone(findings)
	slices.SortStableFunc(ordered, func(a, b reminder.Finding) int {
		return cmp.Or(cmp.Compare(a.Severity, b.Severity), a.Time.Compare(b.Time), strings.Compare(a.Key, b.Key))
	})
	top := styles[ordered[0].Severity]

	var counts, sections []string
	for _, severity := range reminder.Severities {
		var group []reminder.Finding
		for _, finding := range ordered {
			if finding.Severity == severity {
				group = append(group, finding)
			}
		}
		if len(group) == 0 {
			continue
		}
		lines := []string{styles[severity].header + ":"}
		for _, finding := range group {
			lines = append(lines, fmt.Sprintf("- %s: %s", finding.Key, strings.Join(finding.Reasons, "; ")))
			if finding.Note != "" {
				lines = append(lines, "  Note: "+finding.Note)
			}
		}
		counts = append(counts, fmt.Sprintf("%d %s", len(group), styles[severity].short))
		sections = append(sections, strings.Join(lines, "\n"))
	}

	var title string
	switch {
	case len(ordered) == 1:
		title = fmt.Sprintf("OpenBao: %s %s", ordered[0].Key, top.singular)
	case len(sections) == 1:
		title = fmt.Sprintf("OpenBao: %d secrets %s", len(ordered), top.plural)
	default:
		title = "OpenBao: " + strings.Join(counts, ", ")
	}

	return Digest{
		Title:    title,
		Priority: top.priority,
		Tags:     top.tags,
		Body:     strings.Join(sections, "\n\n"),
	}
}

// Truncate shortens a body longer than MaxBodyBytes to its whole lines within
// 3900 bytes and appends a marker that points to the pod logs.
func Truncate(body string) string {
	if len(body) <= MaxBodyBytes {
		return body
	}
	var kept []string
	used := 0
	for line := range strings.SplitSeq(body, "\n") {
		size := len(line) + 1
		if used+size > truncatedBodyBytes {
			break
		}
		kept = append(kept, line)
		used += size
	}
	return strings.Join(kept, "\n") + truncationMarker
}
