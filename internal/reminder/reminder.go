// Package reminder evaluates the rotation reminders that secrets declare in
// their KV v2 custom_metadata.
package reminder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mcsrobert/openbao-rotation-reminders/internal/openbao"
)

// The custom_metadata keys a secret uses to declare a reminder.
const (
	KeyInterval   = "rotation_interval"
	KeyExpiresAt  = "expires_at"
	KeyWarnBefore = "rotation_warn_before"
	KeyNote       = "rotation_note"
)

var knownKeys = []string{KeyInterval, KeyExpiresAt, KeyWarnBefore, KeyNote}

// Severity orders findings, most urgent first.
type Severity int

// Severities, most urgent first.
const (
	SeverityExpired Severity = iota
	SeverityDue
	SeverityUpcoming
	SeverityInvalid
)

// Severities lists every severity, most urgent first.
var Severities = []Severity{SeverityExpired, SeverityDue, SeverityUpcoming, SeverityInvalid}

// Finding is a secret that needs attention.
type Finding struct {
	Severity Severity
	// Time is the earliest deadline that triggered the finding, or the
	// evaluation time when only the metadata is invalid.
	Time    time.Time
	Key     string
	Reasons []string
	Note    string
}

var errInactive = errors.New("the current version is not active")

// Evaluate checks one secret's metadata at now. It reports whether the secret
// declares a reminder, and a finding when the secret is expired, due, within
// its warning period or has invalid reminder metadata.
//
// Secrets whose current version is deleted or destroyed, or that have no
// version, are not tracked.
func Evaluate(key string, metadata *openbao.Metadata, now time.Time, defaultWarnBefore time.Duration) (bool, *Finding) {
	custom, err := customMetadata(metadata.CustomMetadata)
	if err != nil {
		return true, invalid(key, now, []string{err.Error()}, "")
	}
	names := make([]string, 0, len(custom))
	for name := range custom {
		names = append(names, name)
	}
	slices.Sort(names)
	if !slices.ContainsFunc(names, isReminderKey) {
		return false, nil
	}

	var errs []string
	created, err := currentVersionCreated(metadata, now)
	switch {
	case errors.Is(err, errInactive):
		return false, nil
	case err != nil:
		errs = append(errs, err.Error())
	}
	createdKnown := err == nil

	for _, name := range names {
		if isReminderKey(name) && !slices.Contains(knownKeys, name) {
			errs = append(errs, fmt.Sprintf("unknown metadata key %q", name))
		}
	}

	note := strings.Join(strings.Fields(custom[KeyNote]), " ")
	interval, hasInterval := custom[KeyInterval]
	expiresAt, hasExpiresAt := custom[KeyExpiresAt]
	warnBefore, hasWarnBefore := custom[KeyWarnBefore]
	if !hasInterval && !hasExpiresAt {
		if _, hasNote := custom[KeyNote]; hasWarnBefore || hasNote {
			errs = append(errs, "rotation_warn_before/rotation_note set without rotation_interval or expires_at")
		}
		return true, invalid(key, now, errs, note)
	}

	lead := defaultWarnBefore
	if hasWarnBefore {
		if parsed, err := ParseDuration(warnBefore, true); err != nil {
			errs = append(errs, "rotation_warn_before: "+err.Error())
		} else {
			lead = parsed
		}
	}

	finding := &Finding{Key: key, Note: note}
	triggered := false
	record := func(severity Severity, deadline time.Time, reason string) {
		finding.Reasons = append(finding.Reasons, reason)
		if !triggered || severity < finding.Severity {
			finding.Severity = severity
		}
		if !triggered || deadline.Before(finding.Time) {
			finding.Time = deadline
		}
		triggered = true
	}

	if hasExpiresAt {
		if expires, err := ParseExpiresAt(expiresAt); err != nil {
			errs = append(errs, "expires_at: "+err.Error())
		} else {
			date := formatDate(expires)
			switch {
			case !now.Before(expires):
				record(SeverityExpired, expires, fmt.Sprintf("expired %s (%s ago)", date, FormatDelta(now.Sub(expires))))
			case lead > 0 && !now.Before(expires.Add(-lead)):
				record(SeverityUpcoming, expires, fmt.Sprintf("expires %s (in %s)", date, FormatDelta(expires.Sub(now))))
			}
		}
	}

	if hasInterval {
		if parsed, err := ParseDuration(interval, false); err != nil {
			errs = append(errs, "rotation_interval: "+err.Error())
		} else if createdKnown {
			due := created.Add(parsed)
			date := formatDate(due)
			label := strings.TrimSpace(interval)
			switch {
			case !now.Before(due):
				record(SeverityDue, due, fmt.Sprintf("due %s (%s overdue, interval %s)", date, FormatDelta(now.Sub(due)), label))
			case lead > 0 && !now.Before(due.Add(-lead)):
				record(SeverityUpcoming, due, fmt.Sprintf("due %s (in %s, interval %s)", date, FormatDelta(due.Sub(now)), label))
			}
		}
	}

	for _, err := range errs {
		finding.Reasons = append(finding.Reasons, metadataError(err))
	}
	switch {
	case triggered:
		return true, finding
	case len(errs) > 0:
		finding.Severity, finding.Time = SeverityInvalid, now
		return true, finding
	default:
		return true, nil
	}
}

func invalid(key string, now time.Time, errs []string, note string) *Finding {
	reasons := make([]string, len(errs))
	for i, err := range errs {
		reasons[i] = metadataError(err)
	}
	return &Finding{Severity: SeverityInvalid, Time: now, Key: key, Reasons: reasons, Note: note}
}

func metadataError(err string) string {
	return "metadata error: " + err
}

func isReminderKey(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "rotation_") || strings.HasPrefix(lower, "expire")
}

// customMetadata decodes custom_metadata into strings. Null values are
// dropped, and other non-string values keep their JSON text.
func customMetadata(raw json.RawMessage) (map[string]string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var values map[string]json.RawMessage
	if raw[0] != '{' || json.Unmarshal(raw, &values) != nil {
		return nil, errors.New("custom_metadata is not an object")
	}
	custom := make(map[string]string, len(values))
	for name, value := range values {
		if bytes.Equal(value, []byte("null")) {
			continue
		}
		var text string
		if json.Unmarshal(value, &text) != nil {
			text = string(value)
		}
		custom[name] = text
	}
	return custom, nil
}

// currentVersionCreated returns the creation time of the current version, or
// errInactive when there is no version or it is deleted or destroyed.
func currentVersionCreated(metadata *openbao.Metadata, now time.Time) (time.Time, error) {
	if metadata.CurrentVersion == nil {
		return time.Time{}, errors.New("metadata has no current_version")
	}
	current := *metadata.CurrentVersion
	if current == 0 {
		return time.Time{}, errInactive
	}
	version := metadata.Versions[strconv.FormatInt(current, 10)]
	if version == nil {
		return time.Time{}, fmt.Errorf("current version %d has no metadata entry", current)
	}
	if version.Destroyed {
		return time.Time{}, errInactive
	}
	if version.DeletionTime != "" {
		deletion, err := ParseTimestamp(version.DeletionTime)
		if err != nil {
			return time.Time{}, fmt.Errorf("current version %d deletion_time: %w", current, err)
		}
		if !deletion.After(now) {
			return time.Time{}, errInactive
		}
	}
	if version.CreatedTime == "" {
		return time.Time{}, fmt.Errorf("current version %d missing created_time", current)
	}
	created, err := ParseTimestamp(version.CreatedTime)
	if err != nil {
		return time.Time{}, fmt.Errorf("current version %d created_time: %w", current, err)
	}
	return created, nil
}
